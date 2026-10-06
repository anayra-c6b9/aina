package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

// TestSecretRedaction formats whole configs every way a log line or debug
// dump might, and checks that no secret (resolved or plaintext) appears.
func TestSecretRedaction(t *testing.T) {
	resolved := loadKeys(t, `["${A}", "${file:/run/k}"]`)
	_, err := Expand(resolved, Options{
		LookupEnv: env(map[string]string{"A": fakeSecret}),
		FS:        fstest.MapFS{"run/k": {Data: []byte(fakeSecret + "-file"), Mode: 0o600}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plaintext := loadKeys(t, `"`+fakeSecret+`"`) // raw value, before Expand
	allowed := loadKeys(t, `"`+fakeSecret+`"`)
	if _, err := Expand(allowed, Options{AllowPlaintext: true, LookupEnv: env(nil), FS: fstest.MapFS{}}); err != nil {
		t.Fatal(err)
	}

	for name, cfg := range map[string]*Config{"resolved": resolved, "plaintext": plaintext, "allowed": allowed} {
		js, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		svc := cfg.Services["orders"]
		outputs := map[string]string{
			"%v":           fmt.Sprintf("%v", cfg),
			"%+v":          fmt.Sprintf("%+v", cfg),
			"%#v":          fmt.Sprintf("%#v", cfg),
			"%v service":   fmt.Sprintf("%v", svc),
			"%#v service":  fmt.Sprintf("%#v", svc),
			"%+v list":     fmt.Sprintf("%+v", svc.APIKey),
			"%#v secret":   fmt.Sprintf("%#v", svc.APIKey[0]),
			"%q secret":    fmt.Sprintf("%q", svc.APIKey[0]),
			"%x secret":    fmt.Sprintf("%x", svc.APIKey[0]),
			"json":         string(js),
			"json service": mustJSON(t, svc),
		}
		for verb, out := range outputs {
			if strings.Contains(out, fakeSecret) || strings.Contains(out, fmt.Sprintf("%x", fakeSecret)) {
				t.Errorf("%s: %s leaks the secret:\n%s", name, verb, out)
			}
			if !strings.Contains(out, redacted) && !strings.Contains(out, fmt.Sprintf("%x", redacted)) {
				t.Errorf("%s: %s does not show %s:\n%s", name, verb, redacted, out)
			}
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSecretMethods(t *testing.T) {
	s := Secret{raw: "${X}", value: fakeSecret, resolved: true}
	text, _ := s.MarshalText()
	js, _ := s.MarshalJSON()
	if s.String() != redacted || s.GoString() != redacted || string(text) != redacted || string(js) != `"[redacted]"` {
		t.Errorf("String=%q GoString=%q Text=%q JSON=%s", s.String(), s.GoString(), text, js)
	}
	if s.Reveal() != fakeSecret {
		t.Errorf("Reveal() = %q", s.Reveal())
	}
}

func TestSecretRef(t *testing.T) {
	tests := map[string]string{
		"${X}":            "${X}",
		"${file:/run/k}":  "${file:/run/k}",
		fakeSecret:        "",
		"a-${X}":          "",
		"${file:rel}":     "",
		"${file:/run/k}}": "",
	}
	for raw, want := range tests {
		if got := (Secret{raw: raw}).Ref(); got != want {
			t.Errorf("Ref of %q = %q, want %q", raw, got, want)
		}
	}
}
