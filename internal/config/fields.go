// fields.go ports roster/shared/src/settings.py's field registry: the
// per-key FieldSpec declarations (env var, trust scope, validator kind,
// default) and the validator functions themselves.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const osPathSeparator = filepath.Separator

func isAbsPath(s string) bool {
	return filepath.IsAbs(s)
}

// Scope is a field's trust scope: whether a project-local config file may
// ever set it.
type Scope string

const (
	// ScopeGlobalOnly fields may only be set via env var or the user-global
	// config file -- never the project-local file, which is untrusted,
	// clonable repository content.
	ScopeGlobalOnly Scope = "global_only"
	// ScopeProjectOrGlobal fields may be set from either tier.
	ScopeProjectOrGlobal Scope = "project_or_global"
)

// FieldSpec is one setting's full declaration, mirroring settings.py's
// FieldSpec dataclass.
type FieldSpec struct {
	Key              string
	EnvVar           string // "" if this field has no env var (none currently do, but the shape allows it)
	Scope            Scope
	Kind             string
	Required         bool
	HasStaticDefault bool
	StaticDefault    any                // valid only when HasStaticDefault
	ComputedDefault  func() (any, bool) // returns (value, ok); ok=false means "no default available"
	Secret           bool
}

func (s FieldSpec) displayDefault() any {
	if s.HasStaticDefault {
		return s.StaticDefault
	}
	if s.ComputedDefault != nil {
		if v, ok := s.ComputedDefault(); ok {
			return v
		}
	}
	return nil
}

// validator validates a raw value (from an env var string, or a
// YAML/JSON-decoded value from a config file) against spec, returning the
// normalized value or a *SettingsError.
type validator func(raw any, spec FieldSpec) (any, error)

func validateString(raw any, spec FieldSpec) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return nil, settingsErrorf("%s: expected a string, got %T (%v); quote the value if it came from a YAML file", spec.Key, raw, raw)
	}
	stripped := strings.TrimSpace(s)
	if stripped == "" {
		return nil, settingsErrorf("%s: value is empty/whitespace-only", spec.Key)
	}
	return stripped, nil
}

func validateGitLabBaseURLField(raw any, spec FieldSpec) (any, error) {
	stripped, err := validateString(raw, spec)
	if err != nil {
		return nil, err
	}
	s := stripped.(string)
	if !strings.HasPrefix(strings.ToLower(s), "https://") {
		return nil, settingsErrorf("%s must start with https://: %q", spec.EnvVar, s)
	}
	parsed, err := url.Parse(s)
	if err != nil {
		return nil, settingsErrorf("%s: invalid URL: %v", spec.EnvVar, err)
	}
	if parsed.User != nil {
		return nil, settingsErrorf("%s must not contain URL userinfo (an '@' in the host component): %q", spec.EnvVar, s)
	}
	return strings.TrimRight(s, "/"), nil
}

func validateProjectID(raw any, spec FieldSpec) (any, error) {
	s, ok := raw.(string)
	if !ok {
		return nil, settingsErrorf("%s must be a string (got %T %v); quote numeric-looking project ids in YAML, e.g. \"007\"", spec.Key, raw, raw)
	}
	stripped := strings.TrimSpace(s)
	if stripped == "" {
		return nil, settingsErrorf("%s: value is empty/whitespace-only", spec.Key)
	}
	return stripped, nil
}

// validateTristateBoolValue is the shared implementation ValidateTristateBool
// (the exported entry point gitlab.go-equivalent callers use) and this
// package's own validator both call.
func validateTristateBoolValue(raw any, keyOrEnvVar string) (*bool, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case bool:
		return &v, nil
	case string:
		normalized := strings.ToLower(strings.TrimSpace(v))
		switch normalized {
		case "true":
			b := true
			return &b, nil
		case "false":
			b := false
			return &b, nil
		default:
			return nil, settingsErrorf("%s must be 'true' or 'false' if set: %q", keyOrEnvVar, v)
		}
	default:
		return nil, settingsErrorf("%s must be a boolean or 'true'/'false' string, got %T (%v)", keyOrEnvVar, raw, raw)
	}
}

func validateTristateBool(raw any, spec FieldSpec) (any, error) {
	label := spec.EnvVar
	if label == "" {
		label = spec.Key
	}
	b, err := validateTristateBoolValue(raw, label)
	if err != nil {
		return nil, err
	}
	return b, nil // *bool, possibly nil
}

func hasPathSeparator(s string) bool {
	return strings.Contains(s, "/") || (osPathSeparator != '/' && strings.ContainsRune(s, osPathSeparator))
}

func rejectControlCharacters(value string, spec FieldSpec) error {
	for i, r := range value {
		if unicode.IsPrint(r) {
			continue
		}
		return settingsErrorf("%s must not contain control characters; found %q at position %d of %q", spec.Key, r, i, value)
	}
	return nil
}

func validateExecutable(raw any, spec FieldSpec) (any, error) {
	stripped, err := validateString(raw, spec)
	if err != nil {
		return nil, err
	}
	s := stripped.(string)
	if hasPathSeparator(s) && !isAbsPath(s) {
		return nil, settingsErrorf("%s must be an absolute path or a bare executable name found on PATH, not a relative path: %q", spec.Key, s)
	}
	if strings.HasPrefix(s, "-") {
		return nil, settingsErrorf("%s must not begin with '-' (it would be parsed as an option by the program that runs it, not as a command): %q", spec.Key, s)
	}
	if err := rejectControlCharacters(s, spec); err != nil {
		return nil, err
	}
	return s, nil
}

func validatePath(raw any, spec FieldSpec) (any, error) {
	stripped, err := validateString(raw, spec)
	if err != nil {
		return nil, err
	}
	s := stripped.(string)
	if err := rejectControlCharacters(s, spec); err != nil {
		return nil, err
	}
	return s, nil
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/+-]*$`)

func validateIdentifier(raw any, spec FieldSpec) (any, error) {
	stripped, err := validateString(raw, spec)
	if err != nil {
		return nil, err
	}
	s := stripped.(string)
	if !identifierPattern.MatchString(s) {
		return nil, settingsErrorf("%s must start with a letter or digit and contain only letters, digits, and the characters . _ : / + - (no whitespace or shell metacharacters): %q", spec.Key, s)
	}
	return s, nil
}

// isPrivateHost reports whether host is a loopback/link-local/private
// address, or resolves only to such addresses. A bare hostname is resolved
// once, at validation time, and every address it maps to must be private --
// a name resolving to a mix of private and public addresses is treated as
// public (fails closed).
func isPrivateHost(host string) bool {
	literal := strings.Trim(host, "[]")
	if ip := net.ParseIP(literal); ip != nil {
		return addressIsPrivate(ip)
	}
	addrs, err := net.LookupHost(host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil || !addressIsPrivate(ip) {
			return false
		}
	}
	return true
}

// addressIsPrivate unwraps an IPv4-mapped IPv6 address to the address it
// actually maps to before classifying it, so a mapped public address (e.g.
// ::ffff:8.8.8.8) is judged on the IPv4 address it represents, not treated
// as private by construction.
func addressIsPrivate(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func validateEndpointURL(raw any, spec FieldSpec) (any, error) {
	stripped, err := validateString(raw, spec)
	if err != nil {
		return nil, err
	}
	s := stripped.(string)
	if err := rejectControlCharacters(s, spec); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(s)
	if err != nil {
		return nil, settingsErrorf("%s must be an http:// or https:// URL: %q", spec.Key, s)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, settingsErrorf("%s must be an http:// or https:// URL: %q", spec.Key, s)
	}
	if parsed.User != nil {
		return nil, settingsErrorf("%s must not contain URL userinfo (an '@' in the host component); put the credential in the variable named by runners.api_key_env: %q", spec.Key, s)
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, settingsErrorf("%s must include a host: %q", spec.Key, s)
	}
	if parsed.Scheme == "http" && !isPrivateHost(host) {
		return nil, settingsErrorf("%s: plaintext http:// is only allowed toward a loopback or private-network host (this is a self-hosted-endpoint escape hatch, not a general one); use https:// for %q", spec.Key, host)
	}
	return strings.TrimRight(s, "/"), nil
}

var envVarNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateEnvVarName(raw any, spec FieldSpec) (any, error) {
	stripped, err := validateString(raw, spec)
	if err != nil {
		return nil, err
	}
	s := stripped.(string)
	if !envVarNamePattern.MatchString(s) {
		return nil, settingsErrorf("%s must be an environment variable *name* (letters, digits, underscore; not starting with a digit), not its value: %q", spec.Key, s)
	}
	return s, nil
}

func splitCommaList(raw any) []string {
	if raw == nil {
		return nil
	}
	if s, ok := raw.(string); ok {
		if s == "" {
			return nil
		}
		var out []string
		for _, item := range strings.Split(s, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	}
	if list, ok := raw.([]any); ok {
		var out []string
		for _, item := range list {
			s := strings.TrimSpace(fmt.Sprintf("%v", item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func validateEnvVarNameList(raw any, spec FieldSpec) (any, error) {
	items := splitCommaList(raw)
	for _, item := range items {
		if !envVarNamePattern.MatchString(item) {
			return nil, settingsErrorf("%s must be a comma-separated list of exact environment variable names (no wildcards or prefixes): %q", spec.Key, item)
		}
	}
	return items, nil
}

func validateCommandAllowlist(raw any, spec FieldSpec) (any, error) {
	items := splitCommaList(raw)
	for _, item := range items {
		if hasPathSeparator(item) || strings.HasPrefix(item, "-") {
			return nil, settingsErrorf("%s entries must be bare command names, not paths or options: %q", spec.Key, item)
		}
		if err := rejectControlCharacters(item, spec); err != nil {
			return nil, err
		}
	}
	return items, nil
}

var validators = map[string]validator{
	"gitlab_base_url":   validateGitLabBaseURLField,
	"project_id":        validateProjectID,
	"tristate_bool":     validateTristateBool,
	"executable":        validateExecutable,
	"path":              validatePath,
	"string":            validateString,
	"identifier":        validateIdentifier,
	"endpoint_url":      validateEndpointURL,
	"env_var_name":      validateEnvVarName,
	"env_var_name_list": validateEnvVarNameList,
	"command_allowlist": validateCommandAllowlist,
}

func validate(spec FieldSpec, raw any) (any, error) {
	v, ok := validators[spec.Kind]
	if !ok {
		return nil, settingsErrorf("%s: unknown validator kind %q", spec.Key, spec.Kind)
	}
	return v(raw, spec)
}

func lookupWhich(name string) (string, bool) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	return path, true
}
