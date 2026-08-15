package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindProjectRoot_FindsGitDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "subdir", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindProjectRoot(nested)
	if err != nil {
		t.Fatalf("FindProjectRoot() error = %v", err)
	}
	wantAbs, _ := filepath.Abs(root)
	if got != wantAbs {
		t.Errorf("FindProjectRoot() = %q, want %q", got, wantAbs)
	}
}

func TestFindFileAtProjectRoot_FindsFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, ".agents", "cadre.yaml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := FindFileAtProjectRoot(filepath.Join(".agents", "cadre.yaml"), nested)
	if !ok {
		t.Fatal("expected to find the file")
	}
	if got != target {
		t.Errorf("got %q, want %q", got, target)
	}
}

func TestFindFileAtProjectRoot_StopsAtGitBoundary(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file with the target name exists ABOVE the .git boundary -- must
	// not be picked up.
	outside := filepath.Join(filepath.Dir(root), ".agents", "cadre.yaml")
	os.MkdirAll(filepath.Dir(outside), 0o755)
	os.WriteFile(outside, []byte("x"), 0o644)

	_, ok := FindFileAtProjectRoot(filepath.Join(".agents", "cadre.yaml"), nested)
	if ok {
		t.Fatal("must not find a file above the .git boundary")
	}
}

func TestFindFileAtProjectRoot_NoGitBoundaryNoFile(t *testing.T) {
	dir := t.TempDir()
	_, ok := FindFileAtProjectRoot(filepath.Join(".agents", "cadre.yaml"), dir)
	if ok {
		t.Fatal("expected no file found")
	}
}

func TestFindProjectRoot_GitFileForWorktree(t *testing.T) {
	// A linked worktree's `.git` is a file containing a `gitdir:` pointer,
	// not a directory.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := FindProjectRoot(root)
	if err != nil {
		t.Fatalf("FindProjectRoot() error = %v", err)
	}
	wantAbs, _ := filepath.Abs(root)
	if got != wantAbs {
		t.Errorf("FindProjectRoot() = %q, want %q", got, wantAbs)
	}
}

func TestFindProjectRoot_NoGitBoundary(t *testing.T) {
	// t.TempDir() lives under the OS temp directory (os.TempDir()), not
	// inside this repository's checkout, so no .git boundary is expected
	// above it. This assumption could in principle be violated by an
	// unusual CI/sandbox layout that nests the temp directory inside a git
	// checkout; if this test starts failing for that reason, it is an
	// environment change, not a regression in FindProjectRoot.
	dir := t.TempDir()
	_, err := FindProjectRoot(dir)
	if !errors.Is(err, ErrRepoRootNotFound) {
		t.Errorf("FindProjectRoot() error = %v, want ErrRepoRootNotFound", err)
	}
}

func TestRepoRoot_UsesCurrentWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	got, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot() error = %v", err)
	}
	wantAbs, _ := filepath.Abs(root)
	if got != wantAbs {
		t.Errorf("RepoRoot() = %q, want %q", got, wantAbs)
	}
}

func TestFindInstallationRoot_NormalCheckout(t *testing.T) {
	// Create a fake checkout with roster/ directory
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "roster"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "subdir", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}

	// Isolate this test from CADRE_REPO_ROOT
	oldEnv := os.Getenv("CADRE_REPO_ROOT")
	t.Cleanup(func() {
		if oldEnv == "" {
			os.Unsetenv("CADRE_REPO_ROOT")
		} else {
			os.Setenv("CADRE_REPO_ROOT", oldEnv)
		}
	})
	os.Unsetenv("CADRE_REPO_ROOT")

	got, err := FindInstallationRoot()
	if err != nil {
		t.Fatalf("FindInstallationRoot() error = %v", err)
	}
	wantAbs, _ := filepath.Abs(root)
	if got != wantAbs {
		t.Errorf("FindInstallationRoot() = %q, want %q", got, wantAbs)
	}
}

func TestFindAncestorWith_DirectMarkerBeatsNestedMarker(t *testing.T) {
	// Regression test: direct marker (roster/) should beat nested marker (suite/roster/)
	// even when the nested marker appears lower in the tree.
	// This simulates the real repository structure where both exist.

	root := t.TempDir()

	// Create nested plugin marker lower in tree
	pluginDir := filepath.Join(root, "plugin")
	suiteDir := filepath.Join(pluginDir, "suite")
	pluginRosterDir := filepath.Join(suiteDir, "roster")
	if err := os.MkdirAll(pluginRosterDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create direct marker at root (higher in tree)
	rootRosterDir := filepath.Join(root, "roster")
	if err := os.MkdirAll(rootRosterDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Test from plugin/tools (should climb to root, not stop at plugin/suite)
	startDir := filepath.Join(pluginDir, "tools")
	if err := os.MkdirAll(startDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, found := findAncestorWith(startDir, "roster", 64)
	if !found {
		t.Fatal("findAncestorWith() should find roster")
	}

	wantAbs, _ := filepath.Abs(root)
	if got != wantAbs {
		t.Errorf("findAncestorWith() = %q, want %q (should find direct marker at root, not nested marker at plugin/suite)", got, wantAbs)
	}
}

func TestFindAncestorWith_PluginLayoutWhenNoDirectMarker(t *testing.T) {
	// Test that plugin layout (suite/roster/) is found when no direct marker exists.
	// This covers plugin-only installations without a checkout-style root.

	root := t.TempDir()
	pluginRoot := filepath.Join(root, "plugin")
	suiteDir := filepath.Join(pluginRoot, "suite")
	rosterDir := filepath.Join(suiteDir, "roster")

	if err := os.MkdirAll(rosterDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Start from plugin/bin (no direct roster/ anywhere)
	binDir := filepath.Join(pluginRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, found := findAncestorWith(binDir, "roster", 64)
	if !found {
		t.Fatal("findAncestorWith() should find suite/roster as fallback")
	}

	want, _ := filepath.Abs(suiteDir)
	if got != want {
		t.Errorf("findAncestorWith() = %q, want %q", got, want)
	}
}

func TestFindInstallationRoot_UsesCADRERepoRootEnvVar(t *testing.T) {
	// Test that CADRE_REPO_ROOT environment variable takes precedence
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "roster"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldEnv := os.Getenv("CADRE_REPO_ROOT")
	t.Cleanup(func() {
		if oldEnv == "" {
			os.Unsetenv("CADRE_REPO_ROOT")
		} else {
			os.Setenv("CADRE_REPO_ROOT", oldEnv)
		}
	})

	os.Setenv("CADRE_REPO_ROOT", root)

	// Change to a directory that has no roster/ and no .git
	tempDir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}

	got, err := FindInstallationRoot()
	if err != nil {
		t.Fatalf("FindInstallationRoot() error = %v", err)
	}
	wantAbs, _ := filepath.Abs(root)
	if got != wantAbs {
		t.Errorf("FindInstallationRoot() = %q, want %q", got, wantAbs)
	}
}

func TestFindInstallationRoot_FailsWhenNoRosterFound(t *testing.T) {
	// Test that FindInstallationRoot fails when roster/ is not found anywhere
	tempDir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}

	oldEnv := os.Getenv("CADRE_REPO_ROOT")
	t.Cleanup(func() {
		if oldEnv == "" {
			os.Unsetenv("CADRE_REPO_ROOT")
		} else {
			os.Setenv("CADRE_REPO_ROOT", oldEnv)
		}
	})
	os.Unsetenv("CADRE_REPO_ROOT")

	_, err = FindInstallationRoot()
	if err == nil {
		t.Fatal("expected FindInstallationRoot to fail when roster/ is not found")
	}
}

func TestFindAncestorWith_RejectsEmptySuiteDirectory(t *testing.T) {
	// Negative case: a directory containing suite/ but no roster/ beneath it
	// should not resolve as an installation.
	root := t.TempDir()

	// Create suite/ but empty (no suite/roster/)
	suiteDir := filepath.Join(root, "suite")
	if err := os.Mkdir(suiteDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Start from suite and walk up; should not find anything
	got, found := findAncestorWith(suiteDir, "roster", 64)
	if found {
		t.Errorf("findAncestorWith() should reject empty suite/, got: %q", got)
	}
}

// The pip/pipx wheel layout: the binary lands in <prefix>/bin and its roster
// data in <prefix>/share/cadre/roster, so the installation root has to be
// findable by ascending from the executable across that shape.
func TestFindAncestorWithResolvesTheWheelLayout(t *testing.T) {
	prefix := t.TempDir()
	installed := filepath.Join(prefix, "share", "cadre")
	if err := os.MkdirAll(filepath.Join(installed, "roster"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, found := findAncestorWith(binDir, "roster", 64)
	if !found {
		t.Fatal("a wheel-installed layout must be findable from <prefix>/bin")
	}
	if got != installed {
		t.Errorf("root = %q, want %q -- the directory roster/ lives in, not the prefix", got, installed)
	}
}

func TestACheckoutBeatsAWheelInstallHoweverFarAway(t *testing.T) {
	// The case this ordering exists for: a developer with cadre pip-installed
	// into a venv, working inside a checkout. The checkout is authoritative,
	// even though the venv's share/cadre/roster sits far closer to cwd.
	//
	// Testing this needs the wheel layout *nearer* than the checkout, because
	// a per-level search that tried every layout at each rung would pass a
	// test where the checkout was nearer and fail only here.
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	if err := os.MkdirAll(filepath.Join(checkout, "roster"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The venv lives several levels below the checkout root, with its own
	// installed copy one level above the search start.
	venv := filepath.Join(checkout, "a", "b", "c", ".venv")
	if err := os.MkdirAll(filepath.Join(venv, "share", "cadre", "roster"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := filepath.Join(venv, "bin")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}

	got, found := findAncestorWith(start, "roster", 64)
	if !found {
		t.Fatal("expected a match")
	}
	if got != checkout {
		t.Errorf("root = %q, want the checkout %q -- an installed copy must never "+
			"shadow the checkout a developer is working in", got, checkout)
	}
}

func TestWheelLayoutIsFoundWhenNoCheckoutIsAnywhereAbove(t *testing.T) {
	// And the converse: with no checkout in the ancestry, the installed copy
	// is the right answer rather than an error.
	prefix := t.TempDir()
	installed := filepath.Join(prefix, "share", "cadre")
	if err := os.MkdirAll(filepath.Join(installed, "roster"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(prefix, "bin", "nested", "deeper")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got, found := findAncestorWith(deep, "roster", 64)
	if !found || got != installed {
		t.Errorf("root = %q found=%v, want %q", got, found, installed)
	}
}

func TestPluginLayoutStillBeatsAWheelInstall(t *testing.T) {
	// suite/ predates share/cadre/ and keeps its precedence: a packaged
	// plugin carries its own complete roster, and an unrelated wheel install
	// higher up must not take over for it.
	root := t.TempDir()
	plugin := filepath.Join(root, "plugin")
	if err := os.MkdirAll(filepath.Join(plugin, "suite", "roster"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "share", "cadre", "roster"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, found := findAncestorWith(plugin, "roster", 64)
	if !found || got != filepath.Join(plugin, "suite") {
		t.Errorf("root = %q found=%v, want the plugin's own suite/", got, found)
	}
}
