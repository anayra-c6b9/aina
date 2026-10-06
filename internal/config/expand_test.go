package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

const fakeSecret = "s3cr3t-VALUE"

// env returns a LookupEnv backed by m.
func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

// loadKeys loads a one-service config with api_key written as yaml.
func loadKeys(t *testing.T, apiKeyYAML string) *Config {
	t.Helper()
	return mustLoad(t, "services:\n  orders:\n    url: http://10.0.0.5:3000\n    api_key: "+apiKeyYAML+"\n")
}

// errorList extracts the *ErrorList and checks that no secret leaks.
func errorList(t *testing.T, err error) *ErrorList {
	t.Helper()
	var list *ErrorList
	if !errors.As(err, &list) {
		t.Fatalf("err = %v (%T), want *ErrorList", err, err)
	}
	if strings.Contains(list.Error(), fakeSecret) {
		t.Fatalf("error output leaks the secret:\n%s", list.Error())
	}
	return list
}

func reveal(cfg *Config) []string {
	var out []string
	for _, s := range cfg.Services["orders"].APIKey {
		out = append(out, s.Reveal())
	}
	return out
}

func TestExpandResolves(t *testing.T) {
	fsys := fstest.MapFS{
		"run/secrets/lf":   {Data: []byte(fakeSecret + "\n"), Mode: 0o600},
		"run/secrets/crlf": {Data: []byte(fakeSecret + "\r\n"), Mode: 0o600},
		"run/secrets/two":  {Data: []byte(fakeSecret + "\n\n"), Mode: 0o600},
		"run/secrets/none": {Data: []byte(fakeSecret), Mode: 0o600},
		"run/secrets/old":  {Data: []byte("old-key\n"), Mode: 0o600},
	}
	opts := Options{LookupEnv: env(map[string]string{"ORDERS_KEY": fakeSecret, "NEW": "new-key"}), FS: fsys}
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{"env", `"${ORDERS_KEY}"`, []string{fakeSecret}},
		{"file", `"${file:/run/secrets/none}"`, []string{fakeSecret}},
		{"file strips \\n", `"${file:/run/secrets/lf}"`, []string{fakeSecret}},
		{"file strips \\r\\n", `"${file:/run/secrets/crlf}"`, []string{fakeSecret}},
		{"file strips only one newline", `"${file:/run/secrets/two}"`, []string{fakeSecret + "\n"}},
		{"rotation list in order", `["${NEW}", "${file:/run/secrets/old}"]`, []string{"new-key", "old-key"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadKeys(t, tt.yaml)
			warns, err := Expand(cfg, opts)
			if err != nil || len(warns) != 0 {
				t.Fatalf("warnings %v, err %v", warns, err)
			}
			got := reveal(cfg)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			for _, s := range cfg.Services["orders"].APIKey {
				if !s.Resolved() {
					t.Error("secret not marked resolved")
				}
			}
		})
	}
}

func TestExpandErrors(t *testing.T) {
	fsys := fstest.MapFS{
		"run/secrets/empty":   {Data: []byte("\n"), Mode: 0o600},
		"run/secrets/blocked": {Data: []byte(fakeSecret), Mode: 0o600},
	}
	blocked := denyFS{FS: fsys, deny: "run/secrets/blocked"}
	opts := Options{LookupEnv: env(map[string]string{"EMPTY": "", "SET": fakeSecret}), FS: blocked}
	tests := []struct {
		name string
		yaml string
		want string // the full error line
	}{
		{"missing variable", `"${ORDERS_KEY}"`,
			`aina.yaml:4:5: environment variable "ORDERS_KEY" is not set`},
		{"empty variable", `"${EMPTY}"`,
			`aina.yaml:4:5: environment variable "EMPTY" is empty`},
		{"missing file", `"${file:/run/secrets/nope}"`,
			`aina.yaml:4:5: secret file "/run/secrets/nope" does not exist`},
		{"empty file", `"${file:/run/secrets/empty}"`,
			`aina.yaml:4:5: secret file "/run/secrets/empty" is empty`},
		{"unreadable file", `"${file:/run/secrets/blocked}"`,
			`aina.yaml:4:5: secret file "/run/secrets/blocked" cannot be read: permission denied`},
		{"plaintext", `"` + fakeSecret + `"`,
			`aina.yaml:4:5: api_key in service "orders" is a plain-text secret (use "${ENV_VAR}" or "${file:/path}"; --allow-plaintext is for demos only)`},
		{"embedded reference", `"` + fakeSecret + `-${SET}"`,
			`aina.yaml:4:5: api_key in service "orders" must be exactly one ${NAME} or ${file:/path}, with nothing around it (the whole value must be one ${NAME} or ${file:/absolute/path})`},
		{"bad variable name", `"${1X}"`,
			`aina.yaml:4:5: api_key in service "orders" must be exactly one ${NAME} or ${file:/path}, with nothing around it (the whole value must be one ${NAME} or ${file:/absolute/path})`},
		{"empty file path", `"${file:}"`,
			`aina.yaml:4:5: api_key in service "orders" has an empty ${file:} path (the whole value must be one ${NAME} or ${file:/absolute/path})`},
		{"relative file path", `"${file:run/secrets/x}"`,
			`aina.yaml:4:5: api_key in service "orders" uses secret file path "run/secrets/x", which must be absolute (the whole value must be one ${NAME} or ${file:/absolute/path})`},
		{"stray } after file reference", `"${file:/run/k}}"`,
			`aina.yaml:4:5: api_key in service "orders" must be exactly one ${NAME} or ${file:/path}, with nothing around it (the whole value must be one ${NAME} or ${file:/absolute/path})`},
		{"unclean file path", `"${file:/run/../etc/x}"`,
			`aina.yaml:4:5: api_key in service "orders" uses secret file path "/run/../etc/x", which must be clean (no .., //, or trailing /) (the whole value must be one ${NAME} or ${file:/absolute/path})`},
		{"second list item positioned", "\n      - \"${SET}\"\n      - \"${MISSING}\"",
			`aina.yaml:6:9: environment variable "MISSING" is not set`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadKeys(t, tt.yaml)
			_, err := Expand(cfg, opts)
			list := errorList(t, err)
			if got := list.Error(); got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestExpandErrorPath(t *testing.T) {
	cfg := loadKeys(t, "[\"${A}\", \"${B}\"]")
	_, err := Expand(cfg, Options{LookupEnv: env(map[string]string{"A": "x"}), FS: fstest.MapFS{}})
	list := errorList(t, err)
	if len(list.Errs) != 1 || list.Errs[0].Path != "services.orders.api_key[1]" {
		t.Fatalf("errors = %v", list.Errs)
	}
}

func TestExpandNoEnvCheck(t *testing.T) {
	opts := Options{LookupEnv: env(nil), FS: fstest.MapFS{}, NoEnvCheck: true}
	for _, yaml := range []string{`"${ORDERS_KEY}"`, `"${file:/run/secrets/nope}"`} {
		t.Run(yaml, func(t *testing.T) {
			cfg := loadKeys(t, yaml)
			warns, err := Expand(cfg, opts)
			if err != nil || len(warns) != 0 {
				t.Fatalf("warnings %v, err %v", warns, err)
			}
			s := cfg.Services["orders"].APIKey[0]
			if s.Resolved() || s.Reveal() != "" {
				t.Errorf("resolved=%v value=%q, want unresolved and empty", s.Resolved(), s.Reveal())
			}
			if s.Ref() != strings.Trim(yaml, `"`) {
				t.Errorf("Ref() = %q", s.Ref())
			}
		})
	}
}

func TestExpandAllowPlaintext(t *testing.T) {
	cfg := loadKeys(t, `"`+fakeSecret+`"`)
	if _, err := Expand(cfg, Options{AllowPlaintext: true, LookupEnv: env(nil), FS: fstest.MapFS{}}); err != nil {
		t.Fatal(err)
	}
	s := cfg.Services["orders"].APIKey[0]
	if !s.Resolved() || s.Reveal() != fakeSecret || s.Ref() != "" {
		t.Errorf("resolved=%v value=%q ref=%q", s.Resolved(), s.Reveal(), s.Ref())
	}
}

// TestExpandHostileValue checks that a secret with YAML syntax is carried
// as plain data and cannot change the structure.
func TestExpandHostileValue(t *testing.T) {
	hostile := "a\"b: c # d\ne: f\n  - g: '${Y}'\n---\n"
	src := "services:\n  orders:\n    url: http://10.0.0.5:3000\n    api_key: \"${K}\"\n" +
		"  inventory:\n    url: http://10.0.0.4:4000\n    api_key: \"${Y}\"\n"
	cfg := mustLoad(t, src)
	_, err := Expand(cfg, Options{LookupEnv: env(map[string]string{"K": hostile, "Y": "y-value"}), FS: fstest.MapFS{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Services) != 2 || cfg.Services["orders"].URL != "http://10.0.0.5:3000" {
		t.Fatalf("structure changed: %+v", cfg.Services)
	}
	keys := cfg.Services["orders"].APIKey
	if len(keys) != 1 || keys[0].Reveal() != hostile {
		t.Errorf("got %d keys, value %q", len(keys), keys[0].Reveal())
	}
}

func TestExpandNoSecondPass(t *testing.T) {
	cfg := loadKeys(t, `"${X}"`)
	opts := Options{LookupEnv: env(map[string]string{"X": "${Y}", "Y": fakeSecret}), FS: fstest.MapFS{}}
	for range 2 { // a second Expand must re-read X, not expand the value
		if _, err := Expand(cfg, opts); err != nil {
			t.Fatal(err)
		}
		if got := reveal(cfg)[0]; got != "${Y}" {
			t.Fatalf("value = %q, want literal ${Y}", got)
		}
	}
}

func TestExpandReload(t *testing.T) {
	fsys := fstest.MapFS{"run/k": {Data: []byte("first\n"), Mode: 0o600}}
	cfg := loadKeys(t, `"${file:/run/k}"`)
	opts := Options{LookupEnv: env(nil), FS: fsys}
	if _, err := Expand(cfg, opts); err != nil {
		t.Fatal(err)
	}
	fsys["run/k"] = &fstest.MapFile{Data: []byte("second\n"), Mode: 0o600}
	if _, err := Expand(cfg, opts); err != nil {
		t.Fatal(err)
	}
	if got := reveal(cfg)[0]; got != "second" {
		t.Errorf("after reload value = %q, want second", got)
	}
}

func TestExpandAllOrNothing(t *testing.T) {
	cfg := loadKeys(t, `["${A}", "${B}"]`)
	if _, err := Expand(cfg, Options{LookupEnv: env(map[string]string{"A": "a1", "B": "b1"}), FS: fstest.MapFS{}}); err != nil {
		t.Fatal(err)
	}
	_, err := Expand(cfg, Options{LookupEnv: env(map[string]string{"A": "a2"}), FS: fstest.MapFS{}})
	errorList(t, err)
	if got := strings.Join(reveal(cfg), ","); got != "a1,b1" {
		t.Errorf("failed Expand changed the config: %s", got)
	}
}

func TestExpandPermissionWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are Unix-only")
	}
	tests := []struct {
		mode     fs.FileMode
		wantWarn bool
	}{
		{0o600, false},
		{0o400, false},
		{0o640, true},
		{0o604, true},
	}
	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			fsys := fstest.MapFS{"run/k": {Data: []byte(fakeSecret), Mode: tt.mode}}
			cfg := loadKeys(t, `"${file:/run/k}"`)
			warns, err := Expand(cfg, Options{LookupEnv: env(nil), FS: fsys})
			if err != nil {
				t.Fatal(err)
			}
			if (len(warns) == 1) != tt.wantWarn || len(warns) > 1 {
				t.Fatalf("warnings = %v", warns)
			}
			if tt.wantWarn {
				want := fmt.Sprintf(`aina.yaml:4:5: warning: secret file "/run/k" is readable by group or others (mode %04o) (chmod 600 /run/k)`, tt.mode)
				if got := warns[0].Error(); got != want {
					t.Errorf("got  %s\nwant %s", got, want)
				}
			}
		})
	}
}

// TestExpandRealDisk uses the default FS on a real group-readable file.
func TestExpandRealDisk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are Unix-only")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir()) // macOS: /var -> /private/var
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "key")
	if err := os.WriteFile(p, []byte(fakeSecret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil { // umask may have narrowed it
		t.Fatal(err)
	}
	cfg := loadKeys(t, `"${file:`+p+`}"`)
	warns, err := Expand(cfg, Options{LookupEnv: env(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Error(), "mode 0644") {
		t.Errorf("warnings = %v", warns)
	}
	if got := reveal(cfg)[0]; got != fakeSecret {
		t.Errorf("value = %q", got)
	}
}

func TestExpandBuiltInCode(t *testing.T) {
	cfg := &Config{Services: map[string]Service{"orders": {APIKey: SecretList{{raw: "${NOPE}"}}}}}
	_, err := Expand(cfg, Options{LookupEnv: env(nil), FS: fstest.MapFS{}})
	list := errorList(t, err)
	if got := list.Error(); got != `environment variable "NOPE" is not set` {
		t.Errorf("got %q", got)
	}
}

// denyFS returns a permission error for one path.
type denyFS struct {
	fs.FS
	deny string
}

func (d denyFS) Open(name string) (fs.File, error) {
	if name == d.deny {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return d.FS.Open(name)
}
