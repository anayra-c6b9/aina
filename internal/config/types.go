// Package config loads aina.yaml strictly and reports structural problems
// as file:line:col errors written in config terms. Semantic checks live in
// the compile package; ${...} secrets are expanded after loading.
package config

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// The yaml tags below are the single source of truth for the vocabulary
// (schema section 7): the loader uses them for unknown-field checks and
// "did you mean" suggestions.

// Config is the whole aina.yaml file.
type Config struct {
	Version   int                `yaml:"version"`
	Listeners Listeners          `yaml:"listeners"`
	Defaults  Defaults           `yaml:"defaults"`
	Services  map[string]Service `yaml:"services"`
	GuardSets map[string][]Step  `yaml:"guard_sets"`
	Groups    []Group            `yaml:"groups"`
	Routes    []Route            `yaml:"routes"`

	src *source // set by Load; nil for a Config built in code
}

// source locates a loaded Config for later error messages. It holds no
// file text: the file may contain plaintext secrets, and %+v prints
// unexported fields.
type source struct {
	file  string
	index *Index
}

// position returns where path is in the loaded file, or a file-less
// position for a Config built in code.
func (c *Config) position(path string) (file string, pos Pos) {
	if c.src == nil {
		return "", Pos{}
	}
	pos, _ = c.src.index.Lookup(path)
	return c.src.file, pos
}

// Listeners holds the three fixed listeners.
type Listeners struct {
	Edge     *Listener `yaml:"edge"`
	Internal *Listener `yaml:"internal"`
	Admin    *Listener `yaml:"admin"`
}

// Listener is one address aina listens on.
type Listener struct {
	Address   string   `yaml:"address"`
	AllowFrom []string `yaml:"allow_from"`
}

// Defaults are inherited by groups, routes and steps.
type Defaults struct {
	Timeout        Duration  `yaml:"timeout"`
	Retries        *int      `yaml:"retries"`
	RetryBackoff   Duration  `yaml:"retry_backoff"`
	RetryOn        RetryList `yaml:"retry_on"`
	MaxBody        Size      `yaml:"max_body"`
	Auth           string    `yaml:"auth"`
	CallerHeader   string    `yaml:"caller_header"`
	StripHeaders   []string  `yaml:"strip_headers"`
	ForwardHeaders []string  `yaml:"forward_headers"`
}

// Service is one entry in the service registry.
type Service struct {
	URL            string          `yaml:"url"`
	Health         *Health         `yaml:"health"`
	APIKey         SecretList      `yaml:"api_key"`
	SendKeyAs      string          `yaml:"send_key_as"`
	Timeout        Duration        `yaml:"timeout"`
	ForwardHeaders *[]string       `yaml:"forward_headers"` // nil = inherit, empty = forward nothing
	CircuitBreaker *CircuitBreaker `yaml:"circuit_breaker"`
}

// Health configures the background health check of a service.
type Health struct {
	Path           string   `yaml:"path"`
	Interval       Duration `yaml:"interval"`
	Timeout        Duration `yaml:"timeout"`
	UnhealthyAfter *int     `yaml:"unhealthy_after"`
}

// CircuitBreaker configures the breaker of a service.
type CircuitBreaker struct {
	Failures *int     `yaml:"failures"`
	Cooldown Duration `yaml:"cooldown"`
}

// Group is a set of routes that share settings.
type Group struct {
	Name      string     `yaml:"name"`
	Prefix    string     `yaml:"prefix"`
	Listener  string     `yaml:"listener"`
	UseGuards StringList `yaml:"use_guards"`
	Timeout   Duration   `yaml:"timeout"`
	Retries   *int       `yaml:"retries"`
	MaxBody   Size       `yaml:"max_body"`
	Routes    []Route    `yaml:"routes"`
}

// Route is one route, standalone or inside a group.
type Route struct {
	Name          string         `yaml:"name"`
	Match         Match          `yaml:"match"`
	Callers       StringList     `yaml:"callers"`
	RequestSchema *RequestSchema `yaml:"request_schema"`
	Timeout       Duration       `yaml:"timeout"`
	Retries       *int           `yaml:"retries"`
	MaxBody       Size           `yaml:"max_body"`
	UseGuards     StringList     `yaml:"use_guards"`
	Proxy         string         `yaml:"proxy"`
	StripPrefix   string         `yaml:"strip_prefix"`
	Steps         []Step         `yaml:"steps"`
	Respond       *Respond       `yaml:"respond"`
}

// Match selects the requests a route handles.
type Match struct {
	Listener string `yaml:"listener"`
	Method   string `yaml:"method"`
	Path     string `yaml:"path"`
}

// Step is one call in a route or guard set. Expressions (params, query,
// map) stay raw until the expr package compiles them.
type Step struct {
	ID       string         `yaml:"id"`
	Guard    bool           `yaml:"guard"`
	Call     string         `yaml:"call"`
	Endpoint string         `yaml:"endpoint"`
	Params   map[string]any `yaml:"params"`
	Query    map[string]any `yaml:"query"`
	Forward  StringList     `yaml:"forward"`
	Map      map[string]any `yaml:"map"`
	Expect   *Expect        `yaml:"expect"`
	OnFail   *OnFail        `yaml:"on_fail"`
	Retries  *int           `yaml:"retries"`
	Timeout  Duration       `yaml:"timeout"`
	Optional bool           `yaml:"optional"`
	Cache    Duration       `yaml:"cache"`
	Undo     *UndoStep      `yaml:"undo"`
}

// UndoStep is the single compensating call of an action step (v1: one
// call, not a list).
type UndoStep struct {
	Call     string         `yaml:"call"`
	Endpoint string         `yaml:"endpoint"`
	Params   map[string]any `yaml:"params"`
	Query    map[string]any `yaml:"query"`
	Forward  StringList     `yaml:"forward"`
	Map      map[string]any `yaml:"map"`
	Timeout  Duration       `yaml:"timeout"`
	Retries  *int           `yaml:"retries"`
}

// Expect describes a successful step response.
type Expect struct {
	Status *int   `yaml:"status"`
	Rule   string `yaml:"rule"`
}

// OnFail says what to answer when a guard step fails.
type OnFail struct {
	Respond *Respond `yaml:"respond"`
}

// Respond builds a response. Body values are raw expressions.
type Respond struct {
	Status  *int   `yaml:"status"`
	Body    any    `yaml:"body"`
	Message string `yaml:"message"`
}

// RequestSchema checks the request body before any call.
type RequestSchema struct {
	Body map[string]FieldSchema `yaml:"body"`
}

// FieldSchema checks one body field.
type FieldSchema struct {
	Type     string   `yaml:"type"`
	Required bool     `yaml:"required"`
	Min      *float64 `yaml:"min"`
	Max      *float64 `yaml:"max"`
}

// StringList is a one-or-many field: a single value means a list of one.
type StringList []string

// UnmarshalYAML accepts one string or a list of strings.
func (l *StringList) UnmarshalYAML(unmarshal func(any) error) error {
	var many []string
	if err := unmarshal(&many); err == nil {
		*l = many
		return nil
	}
	var one string
	if err := unmarshal(&one); err != nil {
		return fmt.Errorf("decode one-or-many value: %w", err)
	}
	*l = StringList{one}
	return nil
}

// RetryList is retry_on: one value or a list, elements kept raw (words or
// numbers) and checked later by compile. Use RetryCode on each element.
type RetryList []any

// UnmarshalYAML accepts one scalar or a list of scalars.
func (l *RetryList) UnmarshalYAML(unmarshal func(any) error) error {
	var many []any
	if err := unmarshal(&many); err == nil {
		*l = many
		return nil
	}
	var one any
	if err := unmarshal(&one); err != nil {
		return fmt.Errorf("decode retry_on: %w", err)
	}
	*l = RetryList{one}
	return nil
}

// RetryCode converts a decoded retry_on element to a status code. Numbers
// decode as uint64 or int64 inside `any`. A quoted "502" is a string and
// is not a code.
func RetryCode(v any) (int, bool) {
	switch n := v.(type) {
	case uint64:
		if n > 1<<31-1 {
			return 0, false
		}
		return int(n), true
	case int64:
		if n < -1<<31 || n > 1<<31-1 {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

// Duration is a duration such as 100ms, 3s or 2m. Set is false when the
// field was not written.
type Duration struct {
	time.Duration
	Set bool
}

// UnmarshalYAML parses a duration string.
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return fmt.Errorf("decode duration: %w", err)
	}
	v, err := parseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration{Duration: v, Set: true}
	return nil
}

// Size is a byte size such as 512KB or 1MB (binary units). Set is false
// when the field was not written.
type Size struct {
	Bytes int64
	Set   bool
}

// UnmarshalYAML parses a size string.
func (s *Size) UnmarshalYAML(unmarshal func(any) error) error {
	var str string
	if err := unmarshal(&str); err != nil {
		return fmt.Errorf("decode size: %w", err)
	}
	v, err := parseSize(str)
	if err != nil {
		return err
	}
	*s = Size{Bytes: v, Set: true}
	return nil
}

var (
	durationRe = regexp.MustCompile(`^([0-9]+)(ms|s|m|h)$`)
	sizeRe     = regexp.MustCompile(`^([0-9]+)(B|KB|MB)$`)
)

var durationUnits = map[string]time.Duration{
	"ms": time.Millisecond, "s": time.Second, "m": time.Minute, "h": time.Hour,
}

var sizeUnits = map[string]int64{"B": 1, "KB": 1 << 10, "MB": 1 << 20}

// parseDuration accepts a whole number followed by one unit: ms, s, m, h.
func parseDuration(s string) (time.Duration, error) {
	m := durationRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	unit := durationUnits[m[2]]
	if err != nil || n > int64(1<<63-1)/int64(unit) {
		return 0, fmt.Errorf("duration %q is too large", s)
	}
	return time.Duration(n) * unit, nil
}

// parseSize accepts a whole number followed by one unit: B, KB, MB.
func parseSize(s string) (int64, error) {
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	unit := sizeUnits[m[2]]
	if err != nil || n > (1<<63-1)/unit {
		return 0, fmt.Errorf("size %q is too large", s)
	}
	return n * unit, nil
}
