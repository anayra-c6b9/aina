package config

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
)

// maxErrors caps how many problems one load reports.
const maxErrors = 20

// ConfigError is one problem in a config file. Line and Col are 1-based;
// zero means the problem concerns the whole file.
type ConfigError struct {
	File    string
	Line    int
	Col     int
	Path    string // e.g. routes[0].steps[2].call; "" for file-level problems
	Message string
	Hint    string
	Warning bool // a warning does not stop loading
}

// Error renders the error on one line: file:line:col: message (hint).
// Warnings read file:line:col: warning: message (hint).
func (e *ConfigError) Error() string {
	var b strings.Builder
	b.WriteString(e.File)
	if e.Line > 0 {
		fmt.Fprintf(&b, ":%d:%d", e.Line, e.Col)
	}
	if b.Len() > 0 {
		b.WriteString(": ")
	}
	if e.Warning {
		b.WriteString("warning: ")
	}
	b.WriteString(e.Message)
	if e.Hint != "" {
		fmt.Fprintf(&b, " (%s)", e.Hint)
	}
	return b.String()
}

// ErrorList is every problem found in one file, sorted by position and
// capped at maxErrors. Dropped counts the problems left out.
type ErrorList struct {
	Errs    []*ConfigError
	Dropped int
	src     []byte
}

func newErrorList(errs []*ConfigError, src []byte) *ErrorList {
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Line != errs[j].Line {
			return errs[i].Line < errs[j].Line
		}
		return errs[i].Col < errs[j].Col
	})
	l := &ErrorList{Errs: errs, src: src}
	if len(errs) > maxErrors {
		l.Errs, l.Dropped = errs[:maxErrors], len(errs)-maxErrors
	}
	return l
}

// Error renders all errors one per line, without source excerpts.
func (l *ErrorList) Error() string {
	lines := make([]string, 0, len(l.Errs)+1)
	for _, e := range l.Errs {
		lines = append(lines, e.Error())
	}
	if l.Dropped > 0 {
		lines = append(lines, droppedLine(l.Dropped))
	}
	return strings.Join(lines, "\n")
}

// Format writes every error followed by its source line and a caret under
// the column. It never uses colour.
func (l *ErrorList) Format(w io.Writer) error {
	var b strings.Builder
	for _, e := range l.Errs {
		b.WriteString(e.Error())
		b.WriteByte('\n')
		if !holdsSecret(e.Path) {
			writeExcerpt(&b, l.src, e.Line, e.Col)
		}
	}
	if l.Dropped > 0 {
		b.WriteString(droppedLine(l.Dropped))
		b.WriteByte('\n')
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write config errors: %w", err)
	}
	return nil
}

// holdsSecret reports whether path is an api_key value, whose source line
// must not be printed.
func holdsSecret(path string) bool {
	return strings.Contains(path+".", ".api_key.") || strings.Contains(path, ".api_key[")
}

func droppedLine(n int) string {
	if n == 1 {
		return "... and 1 more error"
	}
	return fmt.Sprintf("... and %d more errors", n)
}

// writeExcerpt writes the source line and a caret, in the style
//
//	42 |       retires: 2
//	   |       ^
func writeExcerpt(b *strings.Builder, src []byte, line, col int) {
	text, ok := sourceLine(src, line)
	if !ok {
		return
	}
	num := fmt.Sprintf("%d", line)
	gutter := strings.Repeat(" ", 3+len(num))
	fmt.Fprintf(b, "   %s | %s\n", num, text)
	pad := min(max(col-1, 0), len([]rune(text)))
	fmt.Fprintf(b, "%s | %s^\n", gutter, caretPad(text, pad))
}

// caretPad keeps tabs from the source so the caret lines up.
func caretPad(text string, n int) string {
	var b strings.Builder
	for i, r := range []rune(text) {
		if i >= n {
			break
		}
		if r == '\t' {
			b.WriteRune('\t')
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// sourceLine returns line n (1-based) of src without its line ending.
func sourceLine(src []byte, n int) (string, bool) {
	if n < 1 {
		return "", false
	}
	for i := 1; i < n; i++ {
		idx := bytes.IndexByte(src, '\n')
		if idx < 0 {
			return "", false
		}
		src = src[idx+1:]
	}
	if idx := bytes.IndexByte(src, '\n'); idx >= 0 {
		src = src[:idx]
	}
	return strings.TrimRight(string(src), "\r"), true
}
