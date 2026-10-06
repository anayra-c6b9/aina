package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Options controls secret expansion. The CLI flags --no-env-check and
// --allow-plaintext set NoEnvCheck and AllowPlaintext.
type Options struct {
	// LookupEnv reads an environment variable. nil means os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// FS reads ${file:/path} sources; "/run/x" is opened as "run/x".
	// nil means os.DirFS("/").
	FS fs.FS
	// NoEnvCheck leaves a missing variable or unreadable file unresolved
	// instead of failing (for CI without secrets). It is silent.
	NoEnvCheck bool
	// AllowPlaintext accepts a value that is not a ${...} reference.
	AllowPlaintext bool
}

func (o Options) withDefaults() Options {
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
	}
	if o.FS == nil {
		o.FS = os.DirFS("/")
	}
	return o
}

// Expand resolves every services.*.api_key element from the environment
// or a file. It runs on parsed values, never on file text, and a resolved
// value is never expanded again. Problems are returned as an *ErrorList
// whose messages name the variable or file but never a secret, and without
// source excerpts (the line holds the secret). cfg is only updated when
// there are no errors, so Expand can be called again on reload.
func Expand(cfg *Config, opts Options) (warnings []*ConfigError, err error) {
	opts = opts.withDefaults()
	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []*ConfigError
	resolved := make(map[string]SecretList, len(names))
	for _, name := range names {
		keys := cfg.Services[name].APIKey
		out := make(SecretList, len(keys))
		for i, s := range keys {
			p := indexPath(keyPath("services."+name, "api_key"), i)
			e := expander{cfg: cfg, opts: opts, path: p, service: name}
			out[i] = e.expand(s.raw)
			errs = append(errs, e.errs...)
			warnings = append(warnings, e.warns...)
		}
		resolved[name] = out
	}
	if len(errs) > 0 {
		return warnings, newErrorList(errs, nil)
	}
	for name, keys := range resolved {
		svc := cfg.Services[name]
		svc.APIKey = keys
		cfg.Services[name] = svc
	}
	return warnings, nil
}

// expander resolves one api_key element and collects its problems.
type expander struct {
	cfg     *Config
	opts    Options
	path    string
	service string
	errs    []*ConfigError
	warns   []*ConfigError
}

func (e *expander) problem(warning bool, msg, hint string) {
	file, pos := e.cfg.position(e.path)
	ce := &ConfigError{File: file, Line: pos.Line, Col: pos.Col, Path: e.path, Message: msg, Hint: hint, Warning: warning}
	if warning {
		e.warns = append(e.warns, ce)
	} else {
		e.errs = append(e.errs, ce)
	}
}

func (e *expander) expand(raw string) Secret {
	s := Secret{raw: raw}
	r, err := parseRef(raw)
	switch {
	case errors.Is(err, errPlaintext):
		if !e.opts.AllowPlaintext {
			e.problem(false, fmt.Sprintf("api_key in service %q is a plain-text secret", e.service),
				`use "${ENV_VAR}" or "${file:/path}"; --allow-plaintext is for demos only`)
			return s
		}
		s.value, s.resolved = raw, true
	case err != nil:
		e.problem(false, fmt.Sprintf("api_key in service %q %v", e.service, err),
			"the whole value must be one ${NAME} or ${file:/absolute/path}")
	case r.file != "":
		s.value, s.resolved = e.readFile(r.file)
	default:
		s.value, s.resolved = e.lookupEnv(r.env)
	}
	return s
}

func (e *expander) lookupEnv(name string) (string, bool) {
	v, ok := e.opts.LookupEnv(name)
	switch {
	case ok && v != "":
		return v, true
	case e.opts.NoEnvCheck:
		return "", false
	case !ok:
		e.problem(false, fmt.Sprintf("environment variable %q is not set", name), "")
	default:
		e.problem(false, fmt.Sprintf("environment variable %q is empty", name), "")
	}
	return "", false
}

func (e *expander) readFile(p string) (string, bool) {
	name := strings.TrimPrefix(p, "/")
	data, err := fs.ReadFile(e.opts.FS, name)
	if err != nil {
		if !e.opts.NoEnvCheck {
			e.problem(false, fmt.Sprintf("secret file %q %s", p, readFailure(err)), "")
		}
		return "", false
	}
	v := string(data)
	if strings.HasSuffix(v, "\r\n") {
		v = v[:len(v)-2]
	} else if strings.HasSuffix(v, "\n") {
		v = v[:len(v)-1]
	}
	if v == "" {
		e.problem(false, fmt.Sprintf("secret file %q is empty", p), "")
		return "", false
	}
	e.checkMode(p, name)
	return v, true
}

// checkMode warns when a secret file is readable by group or others.
func (e *expander) checkMode(p, name string) {
	if runtime.GOOS == "windows" {
		return
	}
	info, err := fs.Stat(e.opts.FS, name)
	if err != nil {
		return
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		e.problem(true, fmt.Sprintf("secret file %q is readable by group or others (mode %04o)", p, perm),
			"chmod 600 "+p)
	}
}

// readFailure describes why a secret file could not be read, without
// repeating the path.
func readFailure(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "does not exist"
	case errors.Is(err, fs.ErrPermission):
		return "cannot be read: permission denied"
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return "cannot be read: " + pe.Err.Error()
	}
	return "cannot be read"
}

// ref is a parsed ${...} reference: env or file is set.
type ref struct {
	env  string
	file string
}

var (
	errPlaintext = errors.New("is not a ${...} reference")
	envRefRe     = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
	fileRefRe    = regexp.MustCompile(`^\$\{file:([^}]*)\}$`)
)

// parseRef parses a whole value as one reference. Errors describe the
// problem without repeating the value, which may be secret.
func parseRef(raw string) (ref, error) {
	if m := envRefRe.FindStringSubmatch(raw); m != nil {
		return ref{env: m[1]}, nil
	}
	if m := fileRefRe.FindStringSubmatch(raw); m != nil && !strings.Contains(m[1], "${") {
		p := m[1]
		switch {
		case p == "":
			return ref{}, errors.New("has an empty ${file:} path")
		case !path.IsAbs(p):
			return ref{}, fmt.Errorf("uses secret file path %q, which must be absolute", p)
		case path.Clean(p) != p:
			return ref{}, fmt.Errorf("uses secret file path %q, which must be clean (no .., //, or trailing /)", p)
		}
		return ref{file: p}, nil
	}
	if strings.Contains(raw, "${") {
		return ref{}, errors.New("must be exactly one ${NAME} or ${file:/path}, with nothing around it")
	}
	return ref{}, errPlaintext
}
