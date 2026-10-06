package config

import (
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
)

// Pos is a 1-based line and column in the config file.
type Pos struct {
	Line, Col int
}

// Index maps config paths such as services.orders.url or
// routes[0].steps[2].call to their position in the file.
type Index struct {
	m map[string]Pos
}

// BuildIndex records the position of every mapping key and list item
// below root. A mapping entry is recorded at its key.
func BuildIndex(root ast.Node) *Index {
	idx := &Index{m: map[string]Pos{"": {Line: 1, Col: 1}}}
	idx.walk(root, "")
	return idx
}

func (idx *Index) walk(n ast.Node, path string) {
	switch v := n.(type) {
	case *ast.MappingNode:
		for _, mv := range v.Values {
			idx.walkEntry(mv, path)
		}
	case *ast.MappingValueNode:
		idx.walkEntry(v, path)
	case *ast.SequenceNode:
		for i, item := range v.Values {
			p := indexPath(path, i)
			idx.m[p] = nodePos(item)
			idx.walk(item, p)
		}
	case *ast.AnchorNode:
		idx.walk(v.Value, path)
	case *ast.TagNode:
		idx.walk(v.Value, path)
	}
}

func (idx *Index) walkEntry(mv *ast.MappingValueNode, path string) {
	p := keyPath(path, keyName(mv.Key))
	if _, seen := idx.m[p]; !seen { // a duplicate key keeps the first position
		idx.m[p] = nodePos(mv.Key)
	}
	idx.walk(mv.Value, p)
}

// Lookup returns the position of path and the path actually found. When
// path does not exist, it falls back to the nearest existing parent.
func (idx *Index) Lookup(path string) (Pos, string) {
	for {
		if p, ok := idx.m[path]; ok {
			return p, path
		}
		path = parentPath(path)
	}
}

// parentPath strips the last .key or [n] segment.
func parentPath(path string) string {
	cut := max(strings.LastIndexByte(path, '.'), strings.LastIndexByte(path, '['))
	if cut < 0 {
		return ""
	}
	return path[:cut]
}

func keyPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func indexPath(parent string, i int) string {
	return parent + "[" + strconv.Itoa(i) + "]"
}

// keyName is the text of a mapping key.
func keyName(n ast.Node) string {
	if s, ok := n.(*ast.StringNode); ok {
		return s.Value
	}
	if tk := n.GetToken(); tk != nil {
		return tk.Value
	}
	return ""
}

// nodePos is where a node starts. A block mapping's own token is the
// first ':', so it is reported at its first key instead.
func nodePos(n ast.Node) Pos {
	if m, ok := n.(*ast.MappingNode); ok && !m.IsFlowStyle && len(m.Values) > 0 {
		return nodePos(m.Values[0].Key)
	}
	if mv, ok := n.(*ast.MappingValueNode); ok {
		return nodePos(mv.Key)
	}
	tk := n.GetToken()
	if tk == nil || tk.Position == nil {
		return Pos{}
	}
	return Pos{Line: tk.Position.Line, Col: tk.Position.Column}
}
