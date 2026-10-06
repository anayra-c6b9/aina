package config

import "fmt"

const redacted = "[redacted]"

// Secret is one api_key value. It keeps the text written in the file and,
// after Expand, the resolved value. Every formatting path prints
// "[redacted]"; only Reveal returns the value.
type Secret struct {
	raw      string // as written: "${X}", "${file:/p}", or plaintext
	value    string
	resolved bool
}

// Reveal returns the resolved value, or "" when unresolved.
func (s Secret) Reveal() string { return s.value }

// Resolved reports whether Expand found a value for the secret.
func (s Secret) Resolved() bool { return s.resolved }

// Ref returns the ${...} reference as written, or "" for a plaintext value
// so that it never leaks through Ref.
func (s Secret) Ref() string {
	if _, err := parseRef(s.raw); err != nil {
		return ""
	}
	return s.raw
}

// String implements fmt.Stringer.
func (s Secret) String() string { return redacted }

// GoString implements fmt.GoStringer (used by %#v).
func (s Secret) GoString() string { return redacted }

// MarshalText implements encoding.TextMarshaler.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// MarshalJSON implements json.Marshaler.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// SecretList is api_key: one secret or a list (rotation).
type SecretList []Secret

// UnmarshalYAML accepts one string or a list of strings.
func (l *SecretList) UnmarshalYAML(unmarshal func(any) error) error {
	var raw StringList
	if err := raw.UnmarshalYAML(unmarshal); err != nil {
		return fmt.Errorf("decode api_key: %w", err)
	}
	out := make(SecretList, len(raw))
	for i, r := range raw {
		out[i] = Secret{raw: r}
	}
	*l = out
	return nil
}
