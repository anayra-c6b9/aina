package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
)

var (
	durationType   = reflect.TypeFor[Duration]()
	sizeType       = reflect.TypeFor[Size]()
	stringListType = reflect.TypeFor[StringList]()
	secretListType = reflect.TypeFor[SecretList]()
	retryListType  = reflect.TypeFor[RetryList]()
	stepType       = reflect.TypeFor[Step]()
	undoType       = reflect.TypeFor[UndoStep]()
	routeType      = reflect.TypeFor[Route]()
	groupType      = reflect.TypeFor[Group]()
)

// undoForbidden lists step fields that are not allowed inside undo, with
// the reason shown as a hint.
var undoForbidden = map[string]string{
	"id":       "undo has no id: it is identified by its parent step",
	"guard":    "undo is never a guard: it reverses an action step",
	"cache":    "undo results are never cached",
	"on_fail":  "undo failures are reported, not handled by on_fail",
	"optional": "undo is always best-effort, so optional does not apply",
	"expect":   "undo has no expect: any 2xx response counts as undone",
	"undo":     "undo cannot have its own undo",
}

// scope names where a node sits, for messages: named is the innermost
// named thing (step "reserve"), sub the fields below it (on_fail.respond).
type scope struct {
	named string
	sub   string
}

func (s scope) String() string {
	switch {
	case s.named == "" && s.sub == "":
		return "the top level"
	case s.named == "":
		return s.sub
	case s.sub == "":
		return s.named
	}
	return s.sub + " of " + s.named
}

func (s scope) field(name string) scope {
	if s.sub == "" {
		return scope{named: s.named, sub: name}
	}
	return scope{named: s.named, sub: s.sub + "." + name}
}

func (s scope) rename(named string) scope {
	return scope{named: named}
}

// fieldInfo is the yaml vocabulary of one struct type.
type fieldInfo struct {
	byTag map[string]reflect.StructField
	tags  []string
}

// schemaWalker checks the AST against the Config struct types and
// collects every problem it finds.
type schemaWalker struct {
	file   string
	errs   []*ConfigError
	fields map[reflect.Type]*fieldInfo
}

func newSchemaWalker(file string) *schemaWalker {
	return &schemaWalker{file: file, fields: map[reflect.Type]*fieldInfo{}}
}

func (w *schemaWalker) add(n ast.Node, path, msg, hint string) {
	p := nodePos(n)
	w.errs = append(w.errs, &ConfigError{
		File: w.file, Line: p.Line, Col: p.Col, Path: path, Message: msg, Hint: hint,
	})
}

func (w *schemaWalker) info(t reflect.Type) *fieldInfo {
	if fi, ok := w.fields[t]; ok {
		return fi
	}
	fi := &fieldInfo{byTag: map[string]reflect.StructField{}}
	for i := range t.NumField() {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		fi.byTag[tag] = f
		fi.tags = append(fi.tags, tag)
	}
	w.fields[t] = fi
	return fi
}

// unwrap skips anchors and tags (reported separately) and reports false
// for aliases, which cannot be checked.
func unwrap(n ast.Node) (ast.Node, bool) {
	for {
		switch v := n.(type) {
		case *ast.AnchorNode:
			n = v.Value
		case *ast.TagNode:
			n = v.Value
		case *ast.AliasNode:
			return nil, false
		default:
			return n, n != nil
		}
	}
}

// check validates node n against type t. label names the value in
// messages (e.g. "retries" or "each step").
func (w *schemaWalker) check(n ast.Node, t reflect.Type, path, label string, sc scope) {
	n, ok := unwrap(n)
	if !ok {
		return
	}
	if t.Kind() == reflect.Interface {
		return // raw expression or body; checked later by expr/compile
	}
	if _, null := n.(*ast.NullNode); null {
		w.add(n, path, fmt.Sprintf("%s has no value", label), "")
		return
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case durationType:
		w.checkUnitValue(n, path, label, "a duration like 3s", "units: ms, s, m, h", func(s string) error {
			_, err := parseDuration(s)
			return err
		})
		return
	case sizeType:
		w.checkUnitValue(n, path, label, "a size like 512KB", "units: B, KB, MB", func(s string) error {
			_, err := parseSize(s)
			return err
		})
		return
	case stringListType:
		w.checkOneOrMany(n, path, label, isText, "text")
		return
	case retryListType:
		w.checkOneOrMany(n, path, label, isScalar, "a word or a status code")
		return
	case secretListType:
		w.checkSecretList(n, path, label)
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		w.checkStruct(n, t, path, label, sc)
	case reflect.Map:
		w.checkMap(n, t, path, label, sc)
	case reflect.Slice:
		w.checkSlice(n, t, path, label, sc)
	case reflect.String:
		if !isText(n) {
			w.add(n, path, fmt.Sprintf("%s must be text, got %s", label, describe(n)), quoteHint(n))
		}
	case reflect.Int:
		if !isInt(n) {
			w.add(n, path, fmt.Sprintf("%s must be a whole number, got %s", label, describe(n)), "")
		}
	case reflect.Float64:
		if !isInt(n) && !isFloat(n) {
			w.add(n, path, fmt.Sprintf("%s must be a number, got %s", label, describe(n)), "")
		}
	case reflect.Bool:
		if _, ok := n.(*ast.BoolNode); !ok {
			w.add(n, path, fmt.Sprintf("%s must be true or false, got %s", label, describe(n)), "")
		}
	}
}

func (w *schemaWalker) checkUnitValue(n ast.Node, path, label, want, hint string, parse func(string) error) {
	s, ok := n.(*ast.StringNode)
	if ok && parse(s.Value) == nil {
		return
	}
	w.add(n, path, fmt.Sprintf("%s must be %s, got %s", label, want, describe(n)), hint)
}

func (w *schemaWalker) checkOneOrMany(n ast.Node, path, label string, valid func(ast.Node) bool, want string) {
	seq, ok := n.(*ast.SequenceNode)
	if !ok {
		if !valid(n) {
			w.add(n, path, fmt.Sprintf("%s must be one value or a list, got %s", label, describe(n)), "")
		}
		return
	}
	for i, item := range seq.Values {
		item, ok := unwrap(item)
		if ok && !valid(item) {
			w.add(item, indexPath(path, i), fmt.Sprintf("each %s entry must be %s, got %s", label, want, describe(item)), "")
		}
	}
}

// checkSecretList is checkOneOrMany for api_key: messages name the kind
// of value, never the value itself.
func (w *schemaWalker) checkSecretList(n ast.Node, path, label string) {
	seq, ok := n.(*ast.SequenceNode)
	if !ok {
		if !isText(n) {
			w.add(n, path, fmt.Sprintf("%s must be one quoted value or a list, got %s", label, kind(n)), `write "${ENV_VAR}" or "${file:/path}"`)
		}
		return
	}
	for i, item := range seq.Values {
		item, ok := unwrap(item)
		if ok && !isText(item) {
			w.add(item, indexPath(path, i), fmt.Sprintf("each %s entry must be text, got %s", label, kind(item)), `write "${ENV_VAR}" or "${file:/path}"`)
		}
	}
}

func (w *schemaWalker) checkStruct(n ast.Node, t reflect.Type, path, label string, sc scope) {
	m, ok := n.(*ast.MappingNode)
	if !ok {
		if t == undoType {
			if _, isSeq := n.(*ast.SequenceNode); isSeq {
				w.add(n, path, "undo takes a single call in v1", "write one call as a mapping, not a list")
				return
			}
		}
		w.add(n, path, fmt.Sprintf("%s must be a mapping (key: value), got %s", label, describe(n)), "")
		return
	}
	sc = structScope(m, t, sc)
	fi := w.info(t)
	for _, mv := range m.Values {
		if _, merge := mv.Key.(*ast.MergeKeyNode); merge {
			continue // reported by the structural checks
		}
		key := keyName(mv.Key)
		p := keyPath(path, key)
		f, known := fi.byTag[key]
		if !known {
			w.unknownField(mv.Key, t, fi, p, key, sc)
			continue
		}
		w.check(mv.Value, f.Type, p, key, sc.field(key))
	}
}

func (w *schemaWalker) unknownField(key ast.Node, t reflect.Type, fi *fieldInfo, path, name string, sc scope) {
	if t == undoType {
		if reason, ok := undoForbidden[name]; ok {
			w.add(key, path, fmt.Sprintf("field %q is not allowed in %s", name, sc), reason)
			return
		}
	}
	hint := ""
	if s := suggest(name, fi.tags); s != "" {
		hint = fmt.Sprintf("did you mean %q?", s)
	}
	w.add(key, path, fmt.Sprintf("unknown field %q in %s", name, sc), hint)
}

// structScope names the scope of a struct for messages.
func structScope(m *ast.MappingNode, t reflect.Type, sc scope) scope {
	switch t {
	case stepType:
		if id := scalarField(m, "id"); id != "" {
			return sc.rename(fmt.Sprintf("step %q", id))
		}
	case undoType:
		if strings.HasPrefix(sc.named, "step ") {
			return sc.rename("undo of " + sc.named)
		}
		return sc.rename("undo")
	case routeType:
		if name := scalarField(m, "name"); name != "" {
			return sc.rename(fmt.Sprintf("route %q", name))
		}
	case groupType:
		if name := scalarField(m, "name"); name != "" {
			return sc.rename(fmt.Sprintf("group %q", name))
		}
	}
	return sc
}

// scalarField returns the text value of key in m, or "".
func scalarField(m *ast.MappingNode, key string) string {
	for _, mv := range m.Values {
		if keyName(mv.Key) != key {
			continue
		}
		if s, ok := mv.Value.(*ast.StringNode); ok {
			return s.Value
		}
	}
	return ""
}

// mapEntryNames names the entries of the named maps in messages.
var mapEntryNames = map[string]string{
	"services":   "service",
	"guard_sets": "guard set",
}

func (w *schemaWalker) checkMap(n ast.Node, t reflect.Type, path, label string, sc scope) {
	m, ok := n.(*ast.MappingNode)
	if !ok {
		w.add(n, path, fmt.Sprintf("%s must be a mapping (key: value), got %s", label, describe(n)), "")
		return
	}
	noun := mapEntryNames[label]
	for _, mv := range m.Values {
		if _, merge := mv.Key.(*ast.MergeKeyNode); merge {
			continue
		}
		key := keyName(mv.Key)
		entry, entrySc := key, sc.field(key)
		if noun != "" {
			entry = fmt.Sprintf("%s %q", noun, key)
			entrySc = sc.rename(entry)
		}
		w.check(mv.Value, t.Elem(), keyPath(path, key), entry, entrySc)
	}
}

// listItemNames gives list entries a natural name in messages.
var listItemNames = map[string]string{
	"steps":  "each step",
	"routes": "each route",
	"groups": "each group",
}

func (w *schemaWalker) checkSlice(n ast.Node, t reflect.Type, path, label string, sc scope) {
	seq, ok := n.(*ast.SequenceNode)
	if !ok {
		w.add(n, path, fmt.Sprintf("%s must be a list, got %s", label, describe(n)), "")
		return
	}
	item, ok := listItemNames[lastWord(label)]
	if !ok {
		if strings.HasPrefix(label, "guard set ") {
			item = "each step"
		} else {
			item = fmt.Sprintf("each %s entry", label)
		}
	}
	for i, v := range seq.Values {
		w.check(v, t.Elem(), indexPath(path, i), item, sc)
	}
}

func lastWord(s string) string {
	if i := strings.LastIndexByte(s, ' '); i >= 0 {
		return s[i+1:]
	}
	return s
}

func isText(n ast.Node) bool {
	switch n.(type) {
	case *ast.StringNode, *ast.LiteralNode:
		return true
	}
	return false
}

func isInt(n ast.Node) bool {
	in, ok := n.(*ast.IntegerNode)
	if !ok {
		return false
	}
	_, fits := RetryCode(in.Value)
	return fits
}

func isFloat(n ast.Node) bool {
	_, ok := n.(*ast.FloatNode)
	return ok
}

func isScalar(n ast.Node) bool {
	switch n.(type) {
	case *ast.StringNode, *ast.IntegerNode, *ast.FloatNode, *ast.BoolNode:
		return true
	}
	return false
}

// describe renders a node for "got ..." in messages.
func describe(n ast.Node) string {
	switch v := n.(type) {
	case *ast.StringNode:
		return strconv.Quote(v.Value)
	case *ast.MappingNode:
		return "a mapping"
	case *ast.SequenceNode:
		return "a list"
	case *ast.NullNode:
		return "nothing"
	case *ast.LiteralNode:
		return "a block of text"
	}
	if tk := n.GetToken(); tk != nil {
		return tk.Value
	}
	return "an unknown value"
}

// kind names the type of a node without its value.
func kind(n ast.Node) string {
	switch n.(type) {
	case *ast.IntegerNode, *ast.FloatNode, *ast.InfinityNode, *ast.NanNode:
		return "a number"
	case *ast.BoolNode:
		return "true or false"
	case *ast.StringNode, *ast.LiteralNode:
		return "text"
	case *ast.MappingNode:
		return "a mapping"
	case *ast.SequenceNode:
		return "a list"
	case *ast.NullNode:
		return "nothing"
	}
	return "an unknown value"
}

// quoteHint suggests quoting a number or bool where text is expected.
func quoteHint(n ast.Node) string {
	switch n.(type) {
	case *ast.IntegerNode, *ast.FloatNode, *ast.BoolNode:
		return fmt.Sprintf("write \"%s\" in quotes to use it as text", n.GetToken().Value)
	}
	return ""
}
