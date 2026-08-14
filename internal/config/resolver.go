// Package config resolves Cadre's operator settings, mirroring
// roster/shared/src/settings.py's precedence chain: environment variable >
// project-local .agents/cadre.yaml > user-global ~/.config/cadre/config.yaml
// > static/computed default > interactive prompt > not-found.
//
// NOTE (Phase 1 scope note, see ADR-001-CLI-GO-REFACTOR.md and
// CADRE_CLI_GO_ARCHITECTURE.md §3): the full precedence chain -- project and
// user-global config file discovery/parsing, global_only scope enforcement,
// and the interactive-prompt step -- was assigned to the
// application-engineer role working the same refactor in parallel. At the
// time this file was written no application-engineer output existed yet in
// this worktree. SDLC delegation (internal/cli/sdlc.go) needs a working
// ResolveString to resolve agentic_sdlc.bin_path -- itself a global_only
// field, per settings.py -- so this file provides a minimal, explicitly
// partial implementation: only the environment-variable step, plus the
// error/sentinel shapes (ErrSettingNotFound, SettingsScopeError) that
// downstream callers are written against. It does not read any config file
// and therefore can neither honor nor violate global_only scope from a
// project-local file -- there is no project-local file support here yet.
//
// This is a placeholder to keep Phase 1 compiling and testable end to end.
// It must be reconciled with -- not silently overridden by -- the full
// application-engineer implementation before Phase 1 is considered merged;
// flag any collision to the reviewer rather than resolving it unilaterally.
package config

import (
	"context"
	"errors"
	"fmt"
	"os"
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
//
// The full application-engineer implementation is expected to raise this
// when a project-local .agents/cadre.yaml sets a global_only key (e.g.
// agentic_sdlc.bin_path, knowledge_store.root, context_store.root). This
// Phase 1 stub never constructs one itself, since it never reads a
// project-local file, but the type is defined here so callers (sdlc.go) can
// already be written against its final shape.
type SettingsScopeError struct {
	Key  string
	File string
}

func (e *SettingsScopeError) Error() string {
	return fmt.Sprintf("config: %q may not be set from project-local file %q (global_only scope)", e.Key, e.File)
}

// SettingsNotFoundError is a required-field variant: distinct from
// ErrSettingNotFound (which resolve_optional-style callers treat as
// "absent, fall back"), this is for a future ResolveRequired that mirrors
// settings.py's resolve_setting(), which raises rather than returning None.
// Not yet used by any Phase 1 caller; defined for the same forward-shape
// reason as SettingsScopeError above.
type SettingsNotFoundError struct {
	Key string
}

func (e *SettingsNotFoundError) Error() string {
	return fmt.Sprintf("config: required setting %q was not found", e.Key)
}

// envVarNames maps a dotted setting key to its actual environment variable
// name, per settings.py's per-field SettingSpec.env_var (see
// roster/shared/src/settings.py, e.g. line 667's
// env_var="AGENTIC_SDLC_BIN" and the registry table starting around line
// 1335). Names are NOT a mechanical CADRE_<KEY_UPPER_SNAKE> transform of the
// dotted key -- settings.py assigns each field's env var explicitly, and
// several (like this one) predate and intentionally omit any CADRE_ prefix.
// Add an entry here for every key ResolveString needs to support; do not
// fall back to a formula, since a formula independently invented here has
// already produced one wrong, unreviewed name (CADRE_AGENTIC_SDLC_BIN_PATH)
// that never matched settings.py.
//
// NOTE (Phase 1 scope, see resolver.go's package doc): this map is expected
// to be superseded/extended by the application-engineer's full settings
// registry port; reconcile rather than override on merge.
var envVarNames = map[string]string{
	"agentic_sdlc.bin_path": "AGENTIC_SDLC_BIN",
}

// ResolveString resolves a dotted setting key (e.g. "agentic_sdlc.bin_path")
// to its configured value.
//
// Phase 1 scope: only the environment-variable step is implemented. The
// environment variable name for each supported key comes from envVarNames
// above, mirroring settings.py's explicit per-field env_var assignment --
// not a mechanical name transform. No project-local or user-global config
// file is read. If no environment variable is set, this returns
// ErrSettingNotFound -- never a bare empty string with a nil error, so
// callers can distinguish "not configured" from "configured as empty"
// unambiguously.
func ResolveString(ctx context.Context, key string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	envKey, ok := envVarNames[key]
	if !ok {
		return "", ErrSettingNotFound
	}

	if val, ok := os.LookupEnv(envKey); ok {
		return val, nil
	}

	return "", ErrSettingNotFound
}
