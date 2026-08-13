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
	"strings"
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

// ResolveString resolves a dotted setting key (e.g. "agentic_sdlc.bin_path")
// to its configured value.
//
// Phase 1 scope: only the environment-variable step is implemented,
// following settings.py's CADRE_<KEY_UPPER_SNAKE_WITH_UNDERSCORES> naming
// (dots become underscores, then upper-cased). No project-local or
// user-global config file is read. If no environment variable is set, this
// returns ErrSettingNotFound -- never a bare empty string with a nil error,
// so callers can distinguish "not configured" from "configured as empty"
// unambiguously.
func ResolveString(ctx context.Context, key string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	envKey := "CADRE_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
	if val, ok := os.LookupEnv(envKey); ok {
		return val, nil
	}

	return "", ErrSettingNotFound
}
