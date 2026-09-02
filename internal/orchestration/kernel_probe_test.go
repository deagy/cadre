package orchestration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stubRun(version string) func(string) (string, error) {
	return func(string) (string, error) { return version + "\n", nil }
}

func noneOnPath(string) []string { return nil }

// TestAStaleKernelIsReportedAsStale is the case this probe exists for. A pipx
// `agentic-sdlc 0.13.2` held the name while the released kernel was not
// installed, and because 0.13.2 sat exactly on the provider bundle's inclusive
// floor, `--require-sdlc` accepted it. Reporting the version was never the
// problem; judging it is the point.
func TestAStaleKernelIsReportedAsStale(t *testing.T) {
	found := func(string) (string, error) { return "/home/someone/.local/bin/agentic-sdlc", nil }

	resolution := ResolveKernel("", "0.14.2", found, noneOnPath, stubRun("0.13.2"), "")

	if !resolution.OK {
		t.Fatalf("a kernel that answered was reported unreachable: %+v", resolution)
	}
	if !resolution.TooOld {
		t.Errorf("0.13.2 against a 0.14.2 pin was not flagged: %+v", resolution)
	}
	if !strings.Contains(resolution.Detail, "0.14.2") {
		t.Errorf("the detail does not say what was expected: %q", resolution.Detail)
	}
}

func TestThePinnedKernelIsNotFlagged(t *testing.T) {
	found := func(string) (string, error) { return "/usr/local/bin/agentic-sdlc", nil }

	for _, version := range []string{"0.14.2", "0.14.3", "0.15.0", "1.0.0"} {
		resolution := ResolveKernel("", "0.14.2", found, noneOnPath, stubRun(version), "")
		if resolution.TooOld {
			t.Errorf("%s was flagged against a 0.14.2 pin", version)
		}
	}
}

// TestAnUnreadableVersionIsNotCalledStale. An unparseable version is a
// different complaint from an old one, and guessing turns it into a false
// alarm on a kernel that may be perfectly current.
func TestAnUnreadableVersionIsNotCalledStale(t *testing.T) {
	found := func(string) (string, error) { return "/usr/local/bin/agentic-sdlc", nil }

	resolution := ResolveKernel("", "0.14.2", found, noneOnPath, stubRun("devel +abc123"), "")
	if resolution.TooOld {
		t.Errorf("an unparseable version was called stale: %+v", resolution)
	}
}

// TestEverySecondKernelOnPathIsNamed: which one answers depends on PATH
// order, and PATH order is not something anyone reads.
func TestEverySecondKernelOnPathIsNamed(t *testing.T) {
	found := func(string) (string, error) { return "/home/someone/.local/bin/agentic-sdlc", nil }
	all := func(string) []string {
		return []string{"/home/someone/.local/bin/agentic-sdlc", "/usr/local/bin/agentic-sdlc"}
	}

	resolution := ResolveKernel("", "0.14.2", found, all, stubRun("0.14.2"), "")
	if len(resolution.Shadowed) != 1 || resolution.Shadowed[0] != "/usr/local/bin/agentic-sdlc" {
		t.Errorf("the shadowed kernel was not named: %+v", resolution.Shadowed)
	}
}

// TestTheOverrideWins, in the same order `cadre select` uses.
func TestTheOverrideWins(t *testing.T) {
	found := func(string) (string, error) {
		t.Error("PATH was consulted while AGENTIC_SDLC_BIN was set")
		return "", errors.New("unreachable")
	}

	resolution := ResolveKernel("/explicit/agentic-sdlc", "0.14.2", found, noneOnPath, stubRun("0.14.2"), "")
	if resolution.Source != "AGENTIC_SDLC_BIN" || resolution.Path != "/explicit/agentic-sdlc" {
		t.Errorf("the override did not win: %+v", resolution)
	}
}

// TestNothingReachableSaysSo rather than reporting a failure. Standalone is a
// supported mode, and `cadre select` says so in the plan.
func TestNothingReachableSaysSo(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("not found") }

	resolution := ResolveKernel("", "0.14.2", missing, noneOnPath, stubRun("0.14.2"), "")
	if resolution.OK || resolution.Path != "" {
		t.Errorf("expected nothing reachable: %+v", resolution)
	}
	if !strings.Contains(resolution.Detail, "standalone") {
		t.Errorf("the detail does not explain what happens: %q", resolution.Detail)
	}
}

// TestThePackagedShimIsFoundWhenNothingIsOnPath.
//
// The case a developer machine cannot produce. `install.sh` puts no kernel on
// PATH and sets no AGENTIC_SDLC_BIN; it runs the packaged shim once to warm the
// cache and leaves it inside the checkout. Every resolver looked only at
// AGENTIC_SDLC_BIN and PATH, so a machine that followed the documented
// instructions had a working kernel and no way to reach it.
func TestThePackagedShimIsFoundWhenNothingIsOnPath(t *testing.T) {
	root := t.TempDir()
	shimDir := filepath.Join(root, "plugin", "plugins", "lifecycle", "bin")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(shimDir, "agentic-sdlc")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	missing := func(string) (string, error) { return "", errors.New("not found") }
	resolution := ResolveKernel("", "0.14.4", missing, noneOnPath, stubRun("0.14.4"), root)
	if resolution.Path != shim {
		t.Errorf("resolved %q, want the packaged shim %q", resolution.Path, shim)
	}
	if resolution.Source != "packaged plugin" {
		t.Errorf("source = %q, want %q", resolution.Source, "packaged plugin")
	}
}

// TestPATHStillBeatsThePackagedShim.
//
// The shim is a last resort, not a new default. An `agentic-sdlc` the operator
// installed is a choice they made about which kernel runs, and a fallback that
// overrode it would silently replace their kernel with a downloaded one.
func TestPATHStillBeatsThePackagedShim(t *testing.T) {
	root := t.TempDir()
	shimDir := filepath.Join(root, "plugin", "plugins", "lifecycle", "bin")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shimDir, "agentic-sdlc"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	found := func(string) (string, error) { return "/usr/local/bin/agentic-sdlc", nil }
	resolution := ResolveKernel("", "0.14.4", found, noneOnPath, stubRun("0.14.4"), root)
	if resolution.Source != "PATH" {
		t.Errorf("source = %q, want PATH -- the packaged shim overrode an installed kernel",
			resolution.Source)
	}
}

// TestNoShimAndNoPathStillReportsNothing.
//
// The fallback must not invent a path. A repoRoot with no packaged plugin in it
// is the ordinary case for a pip or wheel install, and reporting a shim that is
// not there would turn a clear "no kernel" into an exec failure.
func TestNoShimAndNoPathStillReportsNothing(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("not found") }
	resolution := ResolveKernel("", "0.14.4", missing, noneOnPath, stubRun("0.14.4"), t.TempDir())
	if resolution.Path != "" {
		t.Errorf("resolved %q from a root with no shim", resolution.Path)
	}
	if resolution.Detail == "" {
		t.Error("no detail explaining why nothing resolved")
	}
}

// TestThePackagedSuiteLayoutResolvesToo.
//
// CADRE_REPO_ROOT is the checkout root when bin/cadre starts the process and
// `<package>/suite` when the packaged launcher does. Checking only the first
// found nothing in exactly the install this fallback exists for.
func TestThePackagedSuiteLayoutResolvesToo(t *testing.T) {
	pkg := t.TempDir()
	suite := filepath.Join(pkg, "suite")
	shimDir := filepath.Join(pkg, "plugins", "lifecycle", "bin")
	for _, dir := range []string{suite, shimDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	shim := filepath.Join(shimDir, "agentic-sdlc")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	resolved, ok := PackagedKernelShim(suite)
	if !ok || resolved != shim {
		t.Errorf("PackagedKernelShim(%q) = %q, %v; want %q, true", suite, resolved, ok, shim)
	}
}
