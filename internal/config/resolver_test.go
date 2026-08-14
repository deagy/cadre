package config

import (
	"context"
	"errors"
	"testing"
)

func TestResolveString_EnvironmentVariable(t *testing.T) {
	// AGENTIC_SDLC_BIN, not a CADRE_*-prefixed formula name -- this is the
	// actual environment variable settings.py assigns to agentic_sdlc.bin_path
	// (roster/shared/src/settings.py:667, :1335), which callers such as
	// internal/cli/sdlc.go depend on to find the kernel binary.
	t.Setenv("AGENTIC_SDLC_BIN", "/usr/local/bin/agentic-sdlc")

	got, err := ResolveString(context.Background(), "agentic_sdlc.bin_path")
	if err != nil {
		t.Fatalf("ResolveString() error = %v", err)
	}
	if got != "/usr/local/bin/agentic-sdlc" {
		t.Errorf("ResolveString() = %q, want %q", got, "/usr/local/bin/agentic-sdlc")
	}
}

func TestResolveString_NotFound(t *testing.T) {
	_, err := ResolveString(context.Background(), "some.unset.key")
	if !errors.Is(err, ErrSettingNotFound) {
		t.Errorf("ResolveString() error = %v, want ErrSettingNotFound", err)
	}
}

func TestResolveString_EmptyEnvValueIsNotFound(t *testing.T) {
	// An explicitly empty/whitespace-only environment variable makes
	// _resolve_core (settings.py) raise a SettingsError ("X is set but
	// empty/whitespace-only") rather than falling through to the rest of
	// the precedence chain -- but resolve_optional (which ResolveString is
	// built on) catches plain SettingsError and returns None, exactly as
	// it does for an ordinary "not configured" outcome. So the
	// caller-visible behavior through ResolveString is indistinguishable
	// from an unset env var: ErrSettingNotFound, not a propagated error.
	// This is settings.py's actual behavior (verified against
	// roster/shared/src/settings.py's _resolve_core/resolve_optional), not
	// a simplification -- a genuinely wrong-but-set empty env var is
	// silently treated as absent by the Python original too.
	t.Setenv("AGENTIC_SDLC_BIN", "")
	_, err := ResolveString(context.Background(), "agentic_sdlc.bin_path")
	if !errors.Is(err, ErrSettingNotFound) {
		t.Errorf("ResolveString() error = %v, want ErrSettingNotFound", err)
	}
}

func TestResolveString_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ResolveString(ctx, "agentic_sdlc.bin_path")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("ResolveString() error = %v, want context.Canceled", err)
	}
}

func TestSettingsScopeError_Message(t *testing.T) {
	err := &SettingsScopeError{Key: "agentic_sdlc.bin_path", File: ".agents/cadre.yaml"}
	if err.Error() == "" {
		t.Error("SettingsScopeError.Error() returned empty string")
	}
}

func TestSettingsNotFoundError_Message(t *testing.T) {
	err := &SettingsNotFoundError{Key: "some.key"}
	if err.Error() == "" {
		t.Error("SettingsNotFoundError.Error() returned empty string")
	}
}
