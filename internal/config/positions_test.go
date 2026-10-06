package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml/parser"
)

func TestIndexLookup(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(testdataDir, "valid", "aina-v0.3.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	idx := BuildIndex(file.Docs[0].Body)

	tests := []struct {
		name      string
		path      string
		wantPos   Pos
		wantFound string
	}{
		{"existing key", "services.orders.url", Pos{88, 5}, "services.orders.url"},
		{"key in flow map", "services.auth-service.health.interval", Pos{72, 30}, "services.auth-service.health.interval"},
		{"list item", "routes[1]", Pos{217, 5}, "routes[1]"},
		{"key in list item", "routes[1].steps[0].call", Pos{222, 9}, "routes[1].steps[0].call"},
		{"nested list item", "groups[0].routes[0].steps[2].call", Pos{168, 13}, "groups[0].routes[0].steps[2].call"},
		{"missing list falls back to its item", "routes[0].steps[9].call", Pos{210, 5}, "routes[0]"},
		{"missing step falls back to steps", "routes[1].steps[9].call", Pos{220, 5}, "routes[1].steps"},
		{"missing key falls back to map", "services.orders.nope", Pos{87, 3}, "services.orders"},
		{"missing top level", "nope.deeper", Pos{1, 1}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos, found := idx.Lookup(tt.path)
			if pos != tt.wantPos || found != tt.wantFound {
				t.Errorf("Lookup(%q) = %v %q; want %v %q", tt.path, pos, found, tt.wantPos, tt.wantFound)
			}
		})
	}
}

func TestParentPath(t *testing.T) {
	tests := map[string]string{
		"routes[0].steps[2].call": "routes[0].steps[2]",
		"routes[0].steps[2]":      "routes[0].steps",
		"routes[0]":               "routes",
		"routes":                  "",
		"":                        "",
	}
	for in, want := range tests {
		if got := parentPath(in); got != want {
			t.Errorf("parentPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestErrorPathsMatchIndex(t *testing.T) {
	src := "routes:\n  - name: r\n    steps:\n      - id: s\n        retires: 2\n"
	_, err := LoadBytes("aina.yaml", []byte(src))
	list, ok := err.(*ErrorList)
	if !ok || len(list.Errs) != 1 {
		t.Fatalf("err = %v", err)
	}
	e := list.Errs[0]
	file, perr := parser.ParseBytes([]byte(src), 0)
	if perr != nil {
		t.Fatal(perr)
	}
	pos, found := BuildIndex(file.Docs[0].Body).Lookup(e.Path)
	if found != "routes[0].steps[0].retires" || pos != (Pos{e.Line, e.Col}) {
		t.Errorf("error %q at %d:%d, index %q at %v", e.Path, e.Line, e.Col, found, pos)
	}
}
