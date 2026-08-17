package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Which project's config a resolution reads, and what a failure says about it.
//
// `.agents/cadre.yaml` is per-project and arrives with `git clone`, so "which
// project" is not a detail -- it decides whose file is trusted. Two anchors
// exist: an explicit start directory, and this process's working directory.
// The second is right for a CLI a human ran inside a project and wrong for a
// long-lived, project-agnostic process, whose cwd has no relationship to
// whichever project a given call is about.
//
// settings.py had six tests for this. The Go port has the mechanism --
// DisableProjectTierCWDFallback and an explicit start -- and none of the
// tests.

// restoreCWDFallback re-enables the project-tier cwd anchor.
//
// Needed because the flag is package-level and ResetCache does not clear it:
// a test that disables it and returns leaves every later test in the package
// resolving differently. There is no exported way back, which is correct for
// the production API -- a process that has decided its cwd is meaningless does
// not later change its mind -- and is why this reaches the variable directly.
func restoreCWDFallback(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		stateMu.Lock()
		projectTierCWDFallbackDisabledV = false
		stateMu.Unlock()
	})
}

// projectWithSetting writes a checkout whose project-local config sets one
// project-settable field. gitlab.supports_work_item_hierarchy is the only one:
// every other field is global-only and would be refused for that reason before
// the anchoring this file is about ever decides anything.
func projectWithSetting(t *testing.T, value string) string {
	t.Helper()
	dir := makeGitCheckout(t)
	writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"),
		"gitlab:\n  supports_work_item_hierarchy: "+value+"\n")
	return dir
}

func TestAnExplicitStartAnchorsTheProjectTierRegardlessOfCWD(t *testing.T) {
	isolateConfigEnv(t)
	wanted := projectWithSetting(t, "true")
	other := projectWithSetting(t, "false")

	// Stand in the *other* project. The explicit start must win.
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	ResetCache()

	resolved, err := ResolveSetting("gitlab.supports_work_item_hierarchy", wanted)
	if err != nil {
		t.Fatalf("resolving against an explicit start: %v", err)
	}
	value, ok := resolved.(*bool)
	if !ok || value == nil {
		t.Fatalf("resolved to %#v, want a *bool", resolved)
	}
	if !*value {
		t.Error("the working directory's project won over the explicit start; " +
			"a caller that names a project root is not guessing")
	}
}

func TestDisablingTheCWDFallbackSkipsTheProjectTier(t *testing.T) {
	// The reason the opt-out exists: without it, an unrelated directory's
	// config is what gets picked up.
	isolateConfigEnv(t)
	restoreCWDFallback(t)
	unrelated := projectWithSetting(t, "true")

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(unrelated); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	ResetCache()

	// With the fallback live, cwd decides.
	resolved, err := ResolveSetting("gitlab.supports_work_item_hierarchy", "")
	if err != nil {
		t.Fatalf("with the cwd fallback live: %v", err)
	}
	if value, ok := resolved.(*bool); !ok || value == nil || !*value {
		t.Fatalf("cwd's project was not read: %#v", resolved)
	}

	// Disabled, the same call must not read it.
	DisableProjectTierCWDFallback()
	ResetCache()
	resolved, err = ResolveSetting("gitlab.supports_work_item_hierarchy", "")
	if err != nil {
		t.Fatalf("with the cwd fallback disabled: %v", err)
	}
	if value, ok := resolved.(*bool); ok && value != nil && *value {
		t.Error("the project tier was still read from cwd after the fallback was " +
			"disabled; a project-agnostic process would be trusting a config file " +
			"belonging to whatever directory it happens to be standing in")
	}
}

func TestAnExplicitStartIsStillHonoredAfterDisablingTheCWDFallback(t *testing.T) {
	// The opt-out suppresses only the *implicit* anchor. A caller that supplies
	// a validated project root on purpose must still resolve against it --
	// otherwise disabling the fallback would silently disable project config
	// everywhere, which is a much larger change than it reads as.
	isolateConfigEnv(t)
	restoreCWDFallback(t)
	project := projectWithSetting(t, "true")

	DisableProjectTierCWDFallback()
	ResetCache()

	resolved, err := ResolveSetting("gitlab.supports_work_item_hierarchy", project)
	if err != nil {
		t.Fatalf("resolving against an explicit start: %v", err)
	}
	value, ok := resolved.(*bool)
	if !ok || value == nil || !*value {
		t.Errorf("an explicit start was ignored after the cwd fallback was "+
			"disabled: %#v", resolved)
	}
}

func TestAScopeViolationStillRaisesThroughAnExplicitStart(t *testing.T) {
	// The anchoring decides *which* file is read, never whether its contents
	// are trusted. A global-only field in a project file is refused however
	// that file was found.
	isolateConfigEnv(t)
	restoreCWDFallback(t)
	dir := makeGitCheckout(t)
	writeYAML(t, filepath.Join(dir, ".agents", "cadre.yaml"),
		"gitlab:\n  base_url: \"https://attacker.example.com\"\n")

	DisableProjectTierCWDFallback()
	ResetCache()

	_, err := ResolveSetting("gitlab.base_url", dir)
	var scopeErr *SettingsScopeError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("expected a scope violation through an explicit start, got %T: %v",
			err, err)
	}
}

func TestAFailureNamesNoWorkingDirectoryWhenTheTierIsSkipped(t *testing.T) {
	// With the project tier skipped, a "not configured" message that still
	// listed a cwd-derived path would describe a file the resolver never
	// consulted -- sending the reader to edit something that cannot take
	// effect, and printing the operator's directory layout into whatever log
	// caught the error.
	isolateConfigEnv(t)
	restoreCWDFallback(t)
	unrelated := projectWithSetting(t, "true")

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(unrelated); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	DisableProjectTierCWDFallback()
	ResetCache()

	// A required field nothing configures, so the failure is the message.
	_, err = ResolveSetting("gitlab.project_id", "")
	if err == nil {
		t.Fatal("expected a failure for an unconfigured required field")
	}
	if strings.Contains(err.Error(), unrelated) {
		t.Errorf("the failure names the working directory's project, which was "+
			"never read:\n  %v", err)
	}
}
