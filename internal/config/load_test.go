package config

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/invalid/*.expected from current output")

const testdataDir = "../../testdata"

// loadWithDeadline fails the test if loading hangs.
func loadWithDeadline(t *testing.T, name string, data []byte) (*Config, error) {
	t.Helper()
	type result struct {
		cfg *Config
		err error
	}
	done := make(chan result, 1)
	go func() {
		cfg, err := LoadBytes(name, data)
		done <- result{cfg, err}
	}()
	select {
	case r := <-done:
		return r.cfg, r.err
	case <-time.After(5 * time.Second):
		t.Fatalf("loading %s did not finish within 5s", name)
		return nil, nil
	}
}

func formatErr(t *testing.T, err error) string {
	t.Helper()
	var list *ErrorList
	if !errors.As(err, &list) {
		t.Fatalf("error is %T, want *ErrorList: %v", err, err)
	}
	var b bytes.Buffer
	if err := list.Format(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestLoadValid(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(testdataDir, "valid", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no valid testdata: %v", err)
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := loadWithDeadline(t, filepath.Base(path), data)
			if err != nil {
				t.Fatalf("unexpected errors:\n%s", formatErr(t, err))
			}
			if cfg == nil {
				t.Fatal("nil config without error")
			}
		})
	}
}

// TestLoadInvalid compares the formatted errors of every broken config
// with its sibling .expected file.
func TestLoadInvalid(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(testdataDir, "invalid", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no invalid testdata: %v", err)
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = loadWithDeadline(t, "aina.yaml", data)
			if err == nil {
				t.Fatal("expected errors, got none")
			}
			got := formatErr(t, err)
			expPath := strings.TrimSuffix(path, ".yaml") + ".expected"
			if *update {
				if err := os.WriteFile(expPath, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(expPath)
			if err != nil {
				t.Fatalf("missing %s (run with -update to create it):\n%s", expPath, got)
			}
			if got != string(want) {
				t.Errorf("errors differ\n--- got\n%s--- want\n%s", got, want)
			}
		})
	}
}

func TestLoadSchemaFile(t *testing.T) {
	cfg, err := Load(filepath.Join(testdataDir, "valid", "aina-v0.3.yaml"))
	if err != nil {
		t.Fatalf("unexpected errors:\n%s", formatErr(t, err))
	}
	if got := len(cfg.Groups[0].Routes[0].Steps); got != 4 {
		t.Errorf("create-order steps = %d, want 4", got)
	}
	reserve := cfg.Groups[0].Routes[0].Steps[1]
	if reserve.Undo == nil || reserve.Undo.Endpoint != "POST /release" {
		t.Errorf("reserve undo = %+v", reserve.Undo)
	}
	if got := cfg.Defaults.MaxBody.Bytes; got != 1<<20 {
		t.Errorf("max_body = %d, want %d", got, 1<<20)
	}
	if got := cfg.Services["orders"].Timeout.Duration; got != 5*time.Second {
		t.Errorf("orders timeout = %v, want 5s", got)
	}
}

func TestLoadTooLarge(t *testing.T) {
	line := []byte("# padding line for a very large config file\n")
	big := append([]byte("version: 1\n"), bytes.Repeat(line, 5<<20/len(line)+1)...)
	big = big[:5<<20]

	_, err := loadWithDeadline(t, "aina.yaml", big)
	want := "aina.yaml: config file is 5.0 MB; the limit is 1 MB\n"
	if got := formatErr(t, err); got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	path := filepath.Join(t.TempDir(), "aina.yaml")
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Load(path)
	if got := formatErr(t, err); got != want {
		t.Errorf("Load: got %q, want %q", got, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not-exist", err)
	}
}

func TestLoadErrorCap(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(testdataDir, "invalid", "many-errors.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = LoadBytes("aina.yaml", data)
	var list *ErrorList
	if !errors.As(err, &list) {
		t.Fatalf("err = %v", err)
	}
	if len(list.Errs) != maxErrors || list.Dropped != 5 {
		t.Errorf("got %d errors, %d dropped; want %d and 5", len(list.Errs), list.Dropped, maxErrors)
	}
}

// TestLoadNoRawLibraryText checks that odd inputs produce our own wording.
func TestLoadNoRawLibraryText(t *testing.T) {
	for _, name := range []string{
		"empty", "comments-only", "tab-indent", "unterminated-quote", "binary",
		"unquoted-secret-in-list", "unquoted-param-in-flow",
	} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(testdataDir, "invalid", name+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = loadWithDeadline(t, "aina.yaml", data)
			var list *ErrorList
			if !errors.As(err, &list) || len(list.Errs) == 0 {
				t.Fatalf("err = %v", err)
			}
			for _, e := range list.Errs {
				if e.Message == "invalid YAML" {
					t.Errorf("message not rewritten: %s", e.Error())
				}
			}
		})
	}
}
