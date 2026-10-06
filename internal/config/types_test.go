package config

import (
	"reflect"
	"testing"
	"time"
)

func mustLoad(t *testing.T, src string) *Config {
	t.Helper()
	cfg, err := LoadBytes("aina.yaml", []byte(src))
	if err != nil {
		t.Fatalf("unexpected errors:\n%v", err)
	}
	return cfg
}

func TestOneOrMany(t *testing.T) {
	tests := []struct {
		name string
		src  string
		get  func(*Config) StringList
		want StringList
	}{
		{"api_key string", "services:\n  s:\n    api_key: \"${K}\"\n",
			func(c *Config) StringList { return refs(c.Services["s"].APIKey) }, StringList{"${K}"}},
		{"api_key list", "services:\n  s:\n    api_key:\n      - \"${A}\"\n      - \"${file:/run/b}\"\n",
			func(c *Config) StringList { return refs(c.Services["s"].APIKey) }, StringList{"${A}", "${file:/run/b}"}},
		{"callers any", "routes:\n  - name: r\n    callers: any\n",
			func(c *Config) StringList { return c.Routes[0].Callers }, StringList{"any"}},
		{"callers list", "routes:\n  - name: r\n    callers: [orders, inventory]\n",
			func(c *Config) StringList { return c.Routes[0].Callers }, StringList{"orders", "inventory"}},
		{"use_guards one", "groups:\n  - name: g\n    use_guards: logged-in\n",
			func(c *Config) StringList { return c.Groups[0].UseGuards }, StringList{"logged-in"}},
		{"forward one", "guard_sets:\n  g:\n    - id: a\n      forward: Authorization\n",
			func(c *Config) StringList { return c.GuardSets["g"][0].Forward }, StringList{"Authorization"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.get(mustLoad(t, tt.src)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

// refs lists the references of api_key secrets as written.
func refs(l SecretList) StringList {
	out := make(StringList, len(l))
	for i, s := range l {
		out[i] = s.Ref()
	}
	return out
}

func TestRetryOn(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want RetryList
	}{
		{"words and numbers", "defaults:\n  retry_on: [timeout, 502, 503]\n", RetryList{"timeout", uint64(502), uint64(503)}},
		{"single word", "defaults:\n  retry_on: timeout\n", RetryList{"timeout"}},
		{"single number", "defaults:\n  retry_on: 504\n", RetryList{uint64(504)}},
		{"quoted number stays a string", "defaults:\n  retry_on: [\"502\"]\n", RetryList{"502"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustLoad(t, tt.src).Defaults.RetryOn
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRetryCode(t *testing.T) {
	tests := []struct {
		in     any
		want   int
		wantOK bool
	}{
		{uint64(502), 502, true},
		{int64(503), 503, true},
		{int64(-1), -1, true},
		{504, 504, true},
		{"502", 0, false},
		{"timeout", 0, false},
		{uint64(1) << 40, 0, false},
		{1.5, 0, false},
	}
	for _, tt := range tests {
		got, ok := RetryCode(tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("RetryCode(%#v) = %d, %v; want %d, %v", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestForwardHeadersEmptyVsUnset(t *testing.T) {
	cfg := mustLoad(t, "services:\n  a:\n    forward_headers: []\n  b:\n    url: http://x\n")
	if fh := cfg.Services["a"].ForwardHeaders; fh == nil || len(*fh) != 0 {
		t.Errorf("a: forward_headers = %v, want set and empty", fh)
	}
	if fh := cfg.Services["b"].ForwardHeaders; fh != nil {
		t.Errorf("b: forward_headers = %v, want unset", *fh)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"100ms", 100 * time.Millisecond, false},
		{"3s", 3 * time.Second, false},
		{"2m", 2 * time.Minute, false},
		{"1h", time.Hour, false},
		{"0s", 0, false},
		{"3", 0, true},
		{"1m30s", 0, true},
		{"1.5s", 0, true},
		{"-3s", 0, true},
		{"3 s", 0, true},
		{"300us", 0, true},
		{"99999999999999999999h", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := parseDuration(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseDuration(%q) = %v, %v; want %v, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"512KB", 512 << 10, false},
		{"1MB", 1 << 20, false},
		{"64KB", 64 << 10, false},
		{"100B", 100, false},
		{"1GB", 0, true},
		{"1 MB", 0, true},
		{"1mb", 0, true},
		{"1.5MB", 0, true},
		{"1024", 0, true},
		{"99999999999999999MB", 0, true},
	}
	for _, tt := range tests {
		got, err := parseSize(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseSize(%q) = %v, %v; want %v, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestDurationAndSizeDecode(t *testing.T) {
	cfg := mustLoad(t, "defaults:\n  timeout: 3s\n  max_body: 64KB\n")
	if !cfg.Defaults.Timeout.Set || cfg.Defaults.Timeout.Duration != 3*time.Second {
		t.Errorf("timeout = %+v", cfg.Defaults.Timeout)
	}
	if !cfg.Defaults.MaxBody.Set || cfg.Defaults.MaxBody.Bytes != 64<<10 {
		t.Errorf("max_body = %+v", cfg.Defaults.MaxBody)
	}
	if cfg.Defaults.RetryBackoff.Set {
		t.Error("retry_backoff marked set but not written")
	}
}
