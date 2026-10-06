package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// MaxFileSize is the largest config file aina reads. Real configs are a
// few KB; the cap keeps a wrong file from stalling the parser.
const MaxFileSize = 1 << 20

// Load reads and strictly decodes the config file at path. Problems in the
// file are returned as an *ErrorList.
func Load(path string) (*Config, error) {
	name := displayName(path)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > MaxFileSize {
		return nil, fileError(name, nil, tooLarge(st.Size()), "")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return LoadBytes(name, data)
}

// displayName is the file name shown in error messages.
func displayName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// LoadBytes strictly decodes data; name is used in error messages.
func LoadBytes(name string, data []byte) (*Config, error) {
	if len(data) > MaxFileSize {
		return nil, fileError(name, data, tooLarge(int64(len(data))), "")
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // UTF-8 BOM from some editors
	if msg, hint := checkText(data); msg != "" {
		return nil, fileError(name, data, msg, hint)
	}
	file, err := parser.ParseBytes(data, 0, parser.AllowDuplicateMapKey())
	if err != nil {
		return nil, newErrorList([]*ConfigError{syntaxError(name, data, err)}, data)
	}
	body, errs := checkDocuments(name, file)
	if body == nil {
		return nil, newErrorList(errs, data)
	}
	errs = append(errs, checkStructure(name, body, "")...)
	sw := newSchemaWalker(name)
	sw.check(body, reflect.TypeFor[Config](), "", "the config file", scope{})
	errs = append(errs, sw.errs...)
	if len(errs) > 0 {
		return nil, newErrorList(errs, data)
	}
	var cfg Config
	if err := yaml.NodeToValue(body, &cfg, yaml.Strict()); err != nil {
		return nil, newErrorList([]*ConfigError{decodeError(name, err)}, data)
	}
	cfg.src = &source{file: name, index: BuildIndex(body)}
	return &cfg, nil
}

func tooLarge(size int64) string {
	return fmt.Sprintf("config file is %.1f MB; the limit is %d MB", float64(size)/(1<<20), MaxFileSize>>20)
}

func fileError(name string, data []byte, msg, hint string) *ErrorList {
	return newErrorList([]*ConfigError{{File: name, Message: msg, Hint: hint}}, data)
}

// checkText rejects files that are not UTF-8 text before parsing.
func checkText(data []byte) (msg, hint string) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return "file is not a text file", fmt.Sprintf("found a NUL byte at offset %d; is this the right file?", i)
	}
	if !utf8.Valid(data) {
		return "file is not valid UTF-8 text", "save the file as UTF-8"
	}
	return "", ""
}

// checkDocuments returns the body of the single document, or nil when the
// file has no usable configuration.
func checkDocuments(name string, file *ast.File) (ast.Node, []*ConfigError) {
	var errs []*ConfigError
	if len(file.Docs) > 1 {
		p := docPos(file.Docs[1])
		errs = append(errs, &ConfigError{
			File: name, Line: p.Line, Col: p.Col,
			Message: "the file has more than one YAML document",
			Hint:    "aina reads one document; remove this ---",
		})
	}
	if len(file.Docs) == 0 || file.Docs[0].Body == nil {
		if len(errs) == 0 {
			errs = append(errs, &ConfigError{File: name, Message: "file contains no configuration"})
		}
		return nil, errs
	}
	body := file.Docs[0].Body
	if inner, ok := unwrap(body); ok {
		if _, isMap := inner.(*ast.MappingNode); !isMap {
			p := nodePos(inner)
			errs = append(errs, &ConfigError{
				File: name, Line: p.Line, Col: p.Col,
				Message: fmt.Sprintf("the config file must be a mapping (key: value), got %s", describe(inner)),
			})
			return nil, errs
		}
	}
	return body, errs
}

func docPos(d *ast.DocumentNode) Pos {
	if d.Start != nil && d.Start.Position != nil {
		return Pos{Line: d.Start.Position.Line, Col: d.Start.Position.Column}
	}
	if d.Body != nil {
		return nodePos(d.Body)
	}
	return Pos{}
}

// checkStructure reports YAML features aina rejects (anchors, aliases,
// merge keys, tags) and duplicate keys, anywhere in the tree.
func checkStructure(name string, n ast.Node, path string) []*ConfigError {
	var errs []*ConfigError
	add := func(at ast.Node, path, msg, hint string) {
		p := nodePos(at)
		errs = append(errs, &ConfigError{File: name, Line: p.Line, Col: p.Col, Path: path, Message: msg, Hint: hint})
	}
	var walk func(n ast.Node, path string)
	walk = func(n ast.Node, path string) {
		switch v := n.(type) {
		case *ast.AnchorNode:
			add(v, path, "anchors (&name) are not supported", "write the value out in full")
			walk(v.Value, path)
		case *ast.AliasNode:
			add(v, path, "aliases (*name) are not supported", "write the value out in full")
		case *ast.TagNode:
			add(v, path, fmt.Sprintf("YAML tags such as %s are not supported", v.Start.Value), "remove the tag")
			walk(v.Value, path)
		case *ast.MappingNode:
			seen := map[string]Pos{}
			for _, mv := range v.Values {
				if _, merge := mv.Key.(*ast.MergeKeyNode); merge {
					add(mv.Key, path, "merge keys (<<) are not supported", "write the keys out in full")
					walk(mv.Value, path)
					continue
				}
				key := keyName(mv.Key)
				p := keyPath(path, key)
				if first, dup := seen[key]; dup {
					add(mv.Key, p, fmt.Sprintf("duplicate key %q", key), fmt.Sprintf("first defined at line %d", first.Line))
				} else {
					seen[key] = nodePos(mv.Key)
				}
				walk(mv.Value, p)
			}
		case *ast.SequenceNode:
			for i, item := range v.Values {
				walk(item, indexPath(path, i))
			}
		}
	}
	walk(n, path)
	return errs
}

// syntaxError turns a parser error into a ConfigError in config terms.
func syntaxError(name string, src []byte, err error) *ConfigError {
	e := &ConfigError{File: name, Message: "invalid YAML", Hint: err.Error()}
	var ye yaml.Error
	if !errors.As(err, &ye) {
		return e
	}
	e.Hint = ye.GetMessage()
	if tk := ye.GetToken(); tk != nil && tk.Position != nil {
		e.Line, e.Col = tk.Position.Line, tk.Position.Column
	}
	line, _ := sourceLine(src, e.Line)
	e.Message, e.Hint = rewriteSyntax(ye.GetMessage(), line)
	return e
}

// rewriteSyntax maps the parser's messages to config terms. Unknown
// messages keep the parser text as the hint.
func rewriteSyntax(msg, line string) (string, string) {
	switch {
	case strings.Contains(msg, "cannot start any token") && strings.Contains(line, "\t"):
		return "tab character in the file", "YAML indentation must use spaces, not tabs"
	case strings.Contains(msg, "double-quoted text"):
		return "unterminated quoted string", `add the closing "`
	case strings.Contains(msg, "single-quoted text"):
		return "unterminated quoted string", "add the closing '"
	case strings.Contains(msg, "must be specified") && strings.Contains(line, "${"):
		return "unquoted ${...} inside a one-line [ ] or { }", `always quote secrets: "${NAME}"`
	case strings.Contains(msg, "must be specified") && strings.Count(line, "{") > 1:
		return "unquoted {param} inside a one-line { }", `quote the path: "/orders/{id}"`
	case strings.Contains(msg, "must be specified"):
		return "a one-line [ ] or { } is not closed properly", `check for a missing "," or closing bracket`
	case strings.Contains(msg, "flow mapping end token"):
		return "a one-line { } is not closed", "add the closing }"
	case strings.Contains(msg, "sequence end token"):
		return "a one-line [ ] is not closed", "add the closing ]"
	}
	return "invalid YAML", msg
}

// decodeError converts a decoder error. The schema walk should catch every
// problem first, so this is a backstop.
func decodeError(name string, err error) *ConfigError {
	e := &ConfigError{File: name, Message: "value cannot be decoded", Hint: err.Error()}
	var ye yaml.Error
	if errors.As(err, &ye) {
		e.Hint = ye.GetMessage()
		if tk := ye.GetToken(); tk != nil && tk.Position != nil {
			e.Line, e.Col = tk.Position.Line, tk.Position.Column
		}
	}
	return e
}
