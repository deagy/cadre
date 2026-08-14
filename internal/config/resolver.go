// Package config resolves Cadre's operator settings, mirroring
// roster/shared/src/settings.py's precedence chain: environment variable >
// project-local .agents/cadre.yaml > user-global ~/.config/cadre/config.yaml
// > static/computed default > interactive prompt > not-found.
//
// The field registry (registry.go), validators (fields.go), resolution
// core (resolve.go), config-file discovery/loading (files.go), writing
// (write.go), and interactive prompting (prompt.go) are a full port of
// settings.py, including its security-critical properties: global_only
// trust-scope enforcement (a project-local, clonable-repository file may
// never set a field that selects an executable, a data-store location, or
// an exfiltration-sensitive endpoint), secret-shaped-key rejection (a
// token/api_key/password/secret-named key is refused before it is ever
// written to or read from a config file this package manages), and a
// symlink-escape guard on both the read and write paths.
//
// Deviation from settings.py: roster.root's computed default cannot use
// Python's `Path(__file__).resolve().parents[2])` trick (a compiled Go
// binary has no __file__); see registry.go's roster.root entry.
package config

import (
	"context"
	"errors"
	"fmt"
)

// ErrSettingNotFound is returned by ResolveString when a key resolves to no
// value at any step of the precedence chain. Mirrors settings.py's
// resolve_optional() returning None for an ordinary "not configured"
// outcome -- this is not itself an error condition for most callers.
var ErrSettingNotFound = errors.New("config: setting not found")

// SettingsScopeError reports an attempt to set a global_only-scoped field
// from an untrusted, clonable project-local source. Per settings.py, this
// must never be silently ignored -- it is a security event, not an ordinary
// resolution failure. Mirrors settings.py's SettingsScopeError.
type SettingsScopeError struct {
	Key  string
	File string
}

func (e *SettingsScopeError) Error() string {
	return fmt.Sprintf("%s may only be set via the environment or the user-global config file, "+
		"never the project-local file (%s); project-local configuration is untrusted, clonable "+
		"repository content -- remove this key from there", e.Key, e.File)
}

// SettingsNotFoundError is a required-field variant: distinct from
// ErrSettingNotFound (which resolve_optional-style callers treat as
// "absent, fall back"), this is for a required field with no value
// anywhere in the chain (currently gitlab.base_url and gitlab.project_id).
type SettingsNotFoundError struct {
	Key string
}

func (e *SettingsNotFoundError) Error() string {
	return fmt.Sprintf("config: required setting %q was not found", e.Key)
}

// ResolveString resolves a dotted setting key (e.g. "agentic_sdlc.bin_path")
// to its configured value, using the full env > project-file > global-file
// > default > interactive-prompt precedence chain. Returns
// ErrSettingNotFound if the key resolves to no value anywhere in the chain
// (an unconfigured optional field) -- never a bare empty string with a nil
// error, so callers can distinguish "not configured" from "configured as
// empty" unambiguously. Returns a *SettingsScopeError if a project-local
// file set a global_only field; that must never be treated the same as
// ErrSettingNotFound by a caller.
func ResolveString(ctx context.Context, key string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	value, err := ResolveOptional(key, "")
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", ErrSettingNotFound
	}
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("config: %q resolved to a non-string value (%T)", key, value)
	}
	return s, nil
}
