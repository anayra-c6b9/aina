package config

import (
	"strings"
	"testing"
)

func TestConfigErrorString(t *testing.T) {
	tests := []struct {
		name string
		err  ConfigError
		want string
	}{
		{"with position and hint",
			ConfigError{File: "aina.yaml", Line: 42, Col: 7, Message: `unknown field "retires" in step "reserve"`, Hint: `did you mean "retries"?`},
			`aina.yaml:42:7: unknown field "retires" in step "reserve" (did you mean "retries"?)`},
		{"no hint",
			ConfigError{File: "aina.yaml", Line: 1, Col: 10, Message: `version must be a whole number, got "one"`},
			`aina.yaml:1:10: version must be a whole number, got "one"`},
		{"whole file",
			ConfigError{File: "aina.yaml", Message: "file contains no configuration"},
			"aina.yaml: file contains no configuration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatExcerpt(t *testing.T) {
	src := []byte("a: 1\n\tb: 2\nc: 3\r\n")
	tests := []struct {
		name      string
		line, col int
		want      string
	}{
		{"caret under column", 1, 4, "   1 | a: 1\n     |    ^\n"},
		{"tab kept for alignment", 2, 2, "   2 | \tb: 2\n     | \t^\n"},
		{"CRLF trimmed", 3, 1, "   3 | c: 3\n     | ^\n"},
		{"line past end", 9, 1, ""},
		{"no position", 0, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			writeExcerpt(&b, src, tt.line, tt.col)
			if got := b.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorListSortAndCap(t *testing.T) {
	var errs []*ConfigError
	for i := 25; i >= 1; i-- {
		errs = append(errs, &ConfigError{File: "f", Line: i, Col: 1, Message: "m"})
	}
	l := newErrorList(errs, nil)
	if len(l.Errs) != maxErrors || l.Dropped != 5 {
		t.Fatalf("got %d errors, %d dropped", len(l.Errs), l.Dropped)
	}
	if l.Errs[0].Line != 1 || l.Errs[maxErrors-1].Line != maxErrors {
		t.Errorf("not sorted by line: first %d, last %d", l.Errs[0].Line, l.Errs[maxErrors-1].Line)
	}
	if !strings.HasSuffix(l.Error(), "... and 5 more errors") {
		t.Errorf("Error() missing cap line:\n%s", l.Error())
	}
}

func TestFormatSkipsSecretLines(t *testing.T) {
	src := []byte("services:\n  orders:\n    api_key: hunter2\n")
	tests := []struct {
		path string
		want bool // excerpt printed
	}{
		{"services.orders.api_key", false},
		{"services.orders.api_key[1]", false},
		{"services.orders.url", true},
		{"services.api_keys", true},
	}
	for _, tt := range tests {
		l := newErrorList([]*ConfigError{{File: "aina.yaml", Line: 3, Col: 14, Path: tt.path, Message: "m"}}, src)
		var b strings.Builder
		if err := l.Format(&b); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(b.String(), "hunter2"); got != tt.want {
			t.Errorf("%s: excerpt printed = %v, want %v:\n%s", tt.path, got, tt.want, b.String())
		}
	}
}
