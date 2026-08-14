// resolve.go ports settings.py's resolution core: the env > project-file >
// global-file > static default > computed default > interactive prompt >
// fail-closed precedence chain.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Origin records which precedence step produced a Resolved value.
type Origin string

const (
	OriginEnv         Origin = "env"
	OriginProjectFile Origin = "project-file"
	OriginGlobalFile  Origin = "global-file"
	OriginDefault     Origin = "default"
	OriginComputed    Origin = "computed"
	OriginPrompt      Origin = "prompt"
	OriginUnset       Origin = "unset"
	OriginUnresolved  Origin = "unresolved"
)

// Resolved is one setting's resolved value, its origin, and (when
// file-sourced) the path it came from.
type Resolved struct {
	Key        string
	Value      any
	Origin     Origin
	OriginPath string // "" if none
}

const interactiveEnvVar = "CADRE_INTERACTIVE"

var (
	stateMu                         sync.Mutex
	interactiveDisabled             bool
	projectTierCWDFallbackDisabledV bool
)

// DisableInteractive is a hard opt-out for interactive prompting for the
// remaining lifetime of this process. Call unconditionally at the top of
// any stdio-transport entry point (e.g. an MCP dispatch server), where
// stdin is a protocol channel and prompting would corrupt it.
func DisableInteractive() {
	stateMu.Lock()
	interactiveDisabled = true
	stateMu.Unlock()
}

// DisableProjectTierCWDFallback stops treating this process's working
// directory as a project anchor. Right for a CLI a human ran inside a
// project; wrong for a long-lived, project-agnostic process, whose cwd has
// no relationship to whichever project a given call is actually about.
// After this is called, a resolution with no explicit start skips the
// project tier entirely rather than guessing from cwd; an explicit start is
// still honored.
func DisableProjectTierCWDFallback() {
	stateMu.Lock()
	projectTierCWDFallbackDisabledV = true
	stateMu.Unlock()
}

func projectTierCWDFallbackDisabled() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	return projectTierCWDFallbackDisabledV
}

func interactiveIsDisabled() bool {
	stateMu.Lock()
	defer stateMu.Unlock()
	return interactiveDisabled
}

func displayDefault(s FieldSpec) any {
	return s.displayDefault()
}

func failClosedMessage(s FieldSpec, checks []string, globalPath string) string {
	msg := s.Key + " is not configured.\n"
	for _, c := range checks {
		msg += "  checked: " + c + "\n"
	}
	hint := ""
	if s.EnvVar != "" {
		hint = "Set " + s.EnvVar + " or "
	}
	leaf := s.Key
	top := s.Key
	if idx := lastDot(s.Key); idx >= 0 {
		leaf = s.Key[idx+1:]
		top = s.Key[:idx]
	}
	msg += fmt.Sprintf("%sadd `%s` under `%s:` in %s, or re-run with `cadre --interactive ...`.", hint, leaf, top, globalPath)
	return msg
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

// resolveOptions bundles resolveCore's optional overrides -- an env map,
// a start directory, and prompt I/O funcs -- mirroring settings.py's
// per-call keyword arguments.
type resolveOptions struct {
	start       string
	env         map[string]string
	inputFunc   func(prompt string) (string, error)
	outputFunc  func(text string)
	allowPrompt bool
}

func envLookup(opts resolveOptions, name string) (string, bool) {
	if opts.env != nil {
		v, ok := opts.env[name]
		return v, ok
	}
	return os.LookupEnv(name)
}

func resolveCore(key string, opts resolveOptions) (*Resolved, error) {
	s, err := spec(key)
	if err != nil {
		return nil, err
	}

	var checks []string

	// 1. Environment variable.
	if s.EnvVar != "" {
		if raw, ok := envLookup(opts, s.EnvVar); ok {
			if isBlank(raw) {
				return nil, settingsErrorf("%s is set but empty/whitespace-only", s.EnvVar)
			}
			validated, err := validate(s, raw)
			if err != nil {
				return nil, err
			}
			return &Resolved{Key: s.Key, Value: validated, Origin: OriginEnv}, nil
		}
		checks = append(checks, fmt.Sprintf("environment %s -> not set", s.EnvVar))
	}

	// 2. Project-local file.
	projectPath, err := ProjectConfigPath(opts.start)
	if err != nil {
		return nil, err
	}
	switch {
	case projectPath != "":
		data, err := loadConfigFile(projectPath)
		if err != nil {
			return nil, err
		}
		found, rawValue := lookupNested(data, key)
		if found && s.Scope == ScopeGlobalOnly {
			return nil, &SettingsScopeError{Key: key, File: projectPath}
		}
		if found && rawValue != nil {
			validated, err := validate(s, rawValue)
			if err != nil {
				return nil, err
			}
			return &Resolved{Key: s.Key, Value: validated, Origin: OriginProjectFile, OriginPath: projectPath}, nil
		}
		if found {
			checks = append(checks, fmt.Sprintf("%s -> found, key explicitly null (not set at this tier)", projectPath))
		} else {
			checks = append(checks, fmt.Sprintf("%s -> found, key absent", projectPath))
		}
	case opts.start == "" && projectTierCWDFallbackDisabled():
		checks = append(checks, "project-local config -> not consulted (no project anchor supplied and "+
			"this process does not treat its working directory as one)")
	default:
		yamlCandidate, _, _ := projectConfigCandidates(opts.start)
		expected := yamlCandidate
		if expected == "" {
			base := opts.start
			if base == "" {
				if wd, err := os.Getwd(); err == nil {
					base = wd
				}
			}
			expected = filepath.Join(base, projectConfigDir, projectConfigBasename+".yaml")
		}
		checks = append(checks, expected+" -> not found")
	}

	// 3. User-global file.
	globalYAML, globalJSON := globalConfigCandidates()
	globalSelected, err := selectExisting(globalYAML, globalJSON, "user-global")
	if err != nil {
		return nil, err
	}
	if globalSelected != "" {
		data, err := loadConfigFile(globalSelected)
		if err != nil {
			return nil, err
		}
		found, rawValue := lookupNested(data, key)
		if found && rawValue != nil {
			validated, err := validate(s, rawValue)
			if err != nil {
				return nil, err
			}
			return &Resolved{Key: s.Key, Value: validated, Origin: OriginGlobalFile, OriginPath: globalSelected}, nil
		}
		if found {
			checks = append(checks, fmt.Sprintf("%s -> found, key explicitly null (not set at this tier)", globalSelected))
		} else {
			checks = append(checks, fmt.Sprintf("%s -> found, key absent", globalSelected))
		}
	} else {
		checks = append(checks, globalYAML+" -> not found")
	}

	// 4. Static default.
	if s.HasStaticDefault {
		return &Resolved{Key: s.Key, Value: s.StaticDefault, Origin: OriginDefault}, nil
	}

	// 5. Computed default.
	if s.ComputedDefault != nil {
		if computed, ok := s.ComputedDefault(); ok && computed != nil {
			return &Resolved{Key: s.Key, Value: computed, Origin: OriginComputed}, nil
		}
	}

	// 6. Interactive prompt.
	if opts.allowPrompt {
		env := map[string]string{}
		if opts.env != nil {
			env = opts.env
		} else if v, ok := os.LookupEnv(interactiveEnvVar); ok {
			env[interactiveEnvVar] = v
		}
		if interactiveGateOpen(env) {
			prompted, err := promptFor(s, opts)
			if err != nil {
				return nil, err
			}
			if prompted != nil {
				return prompted, nil
			}
		}
	}

	// 7. Fail closed (only for required fields; optional fields resolve to
	// an absent value instead).
	if s.Required {
		return nil, settingsErrorf("%s", failClosedMessage(s, checks, globalYAML))
	}
	return nil, nil
}

func isBlank(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

// ResolveWithOrigin resolves key, returning its value, origin, and (when
// file-sourced) the source path. Always returns a non-nil *Resolved (an
// unresolved optional field comes back as Origin=OriginDefault, Value=nil,
// matching settings.py's resolve_with_origin fallback shape).
func ResolveWithOrigin(key string, start string) (*Resolved, error) {
	r, err := resolveCore(key, resolveOptions{start: start, allowPrompt: true})
	if err != nil {
		return nil, err
	}
	if r == nil {
		return &Resolved{Key: key, Value: nil, Origin: OriginDefault}, nil
	}
	return r, nil
}

// ResolveSetting resolves key to its value, raising for a required field
// with no value anywhere in the chain.
func ResolveSetting(key string, start string) (any, error) {
	r, err := ResolveWithOrigin(key, start)
	if err != nil {
		return nil, err
	}
	return r.Value, nil
}

// ResolveOptional resolves key, never raising for an ordinary "field simply
// isn't configured" outcome -- it resolves to nil instead. A
// *SettingsScopeError (a project-local file setting a global_only field)
// is deliberately NOT swallowed here: per this package's trust-scope
// invariant it must never be silently ignored, even by this "optional"
// resolver.
func ResolveOptional(key string, start string) (any, error) {
	value, err := ResolveSetting(key, start)
	if err != nil {
		var scopeErr *SettingsScopeError
		if errors.As(err, &scopeErr) {
			return nil, err
		}
		var settingsErr *SettingsError
		if errors.As(err, &settingsErr) {
			return nil, nil
		}
		return nil, err
	}
	return value, nil
}

// ResolveOptionalWithIO is ResolveOptional, but with prompt input/output
// rebound to inputFunc/outputFunc -- used by `cadre config resolve` to
// rebind prompt I/O to the controlling terminal (via OpenTTYIO) when this
// process's own stdout is a shell command-substitution pipe capturing the
// eventual resolved value, so prompt text never lands in that pipe.
func ResolveOptionalWithIO(key, start string, inputFunc func(string) (string, error), outputFunc func(string)) (any, error) {
	r, err := resolveCore(key, resolveOptions{start: start, allowPrompt: true, inputFunc: inputFunc, outputFunc: outputFunc})
	if err != nil {
		var scopeErr *SettingsScopeError
		if errors.As(err, &scopeErr) {
			return nil, err
		}
		var settingsErr *SettingsError
		if errors.As(err, &settingsErr) {
			return nil, nil
		}
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return r.Value, nil
}

// ResolveMany resolves every key in keys, returning a map. Any error from
// an individual key (including a *SettingsScopeError) aborts the whole
// call -- mirrors settings.py's resolve_many, which uses resolve_setting
// (raising), not resolve_optional, per key.
func ResolveMany(keys []string, start string) (map[string]any, error) {
	out := make(map[string]any, len(keys))
	for _, key := range keys {
		v, err := ResolveSetting(key, start)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}

// EffectiveSettings is a non-interactive, never-raising snapshot of every
// known setting's resolved value, origin, and source path -- backs `cadre
// config show`. Secret-classified fields (none currently registered) would
// be excluded here rather than resolved.
func EffectiveSettings(start string) []Resolved {
	var results []Resolved
	for _, key := range KnownKeys() {
		s := FIELDS[key]
		if s.Secret {
			continue
		}
		r, err := resolveCore(key, resolveOptions{start: start, allowPrompt: false})
		if err != nil {
			results = append(results, Resolved{Key: key, Origin: OriginUnresolved})
			continue
		}
		if r == nil {
			results = append(results, Resolved{Key: key, Origin: OriginUnset})
			continue
		}
		results = append(results, *r)
	}
	return results
}
