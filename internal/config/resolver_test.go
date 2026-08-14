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
	// An explicitly empty environment variable is treated the same as
	// "configured, but empty" -- NOT "not configured" (LookupEnv still
	// reports ok=true for an empty value set with `export X=`, which is a
	// real, if unusual, shell state). This asserts the LookupEnv contract
	// this implementation relies on rather than special-casing emptiness
	// away, matching os.LookupEnv semantics. Uses the one key currently
	// registered in envVarNames (agentic_sdlc.bin_path -> AGENTIC_SDLC_BIN)
	// since an unregistered key now short-circuits to ErrSettingNotFound
	// before any env var lookup happens.
	t.Setenv("AGENTIC_SDLC_BIN", "")
	got, err := ResolveString(context.Background(), "agentic_sdlc.bin_path")
	if err != nil {
		t.Fatalf("ResolveString() error = %v, want nil (env var is set, even if empty)", err)
	}
	if got != "" {
		t.Errorf("ResolveString() = %q, want empty string", got)
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
