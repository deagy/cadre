package initproject

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func makeGitProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRefuseIfSelfCheckoutOrdinaryTargetAllowed(t *testing.T) {
	dir := makeGitProject(t)
	if err := RefuseIfSelfCheckout(dir); err != nil {
		t.Fatalf("an ordinary project directory must not be refused: %v", err)
	}
}

func TestRefuseIfSelfCheckoutRealCadreCheckoutRefused(t *testing.T) {
	root, ok := suiteCheckoutRoot()
	if !ok {
		t.Skip("not running inside a git checkout")
	}
	if err := RefuseIfSelfCheckout(root); err == nil {
		t.Fatal("expected this suite's own checkout to be refused")
	}
}

func TestRefuseIfSelfCheckoutUnrelatedCloneRefused(t *testing.T) {
	// A directory that merely CONTAINS the two marker files (simulating an
	// unrelated clone of this same suite) must be refused too, even though
	// it is filesystem-distinct from suiteCheckoutRoot().
	dir := makeGitProject(t)
	if err := os.MkdirAll(filepath.Join(dir, "roster", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "roster", "shared", TeamProfileFilename), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "subcommands.tsv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RefuseIfSelfCheckout(dir); err == nil {
		t.Fatal("expected an unrelated clone (marker files present) to be refused")
	}
}

func TestRefuseIfSelfCheckoutDescendantOfUnrelatedCloneRefused(t *testing.T) {
	dir := makeGitProject(t)
	if err := os.MkdirAll(filepath.Join(dir, "roster", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "roster", "shared", TeamProfileFilename), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "bin", "subcommands.tsv"), []byte("x"), 0o644)

	nested := filepath.Join(dir, "some", "nested", "subdir")
	os.MkdirAll(nested, 0o755)

	if err := RefuseIfSelfCheckout(nested); err == nil {
		t.Fatal("expected a descendant of an unrelated clone to be refused too")
	}
}

func TestWriteOverlayRejectsSymlinkEscape(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("symlink creation may be restricted in some CI sandboxes")
	}
	outside := t.TempDir()
	target := makeGitProject(t)
	if err := os.MkdirAll(filepath.Join(target, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, ".agents", "shared")); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	_, err := WriteOverlay(target, TeamProfileFilename, "content")
	if err == nil {
		t.Fatal("expected rejection of a symlink escape")
	}
}

func TestWriteOverlayRefusesSelfCheckoutTarget(t *testing.T) {
	root, ok := suiteCheckoutRoot()
	if !ok {
		t.Skip("not running inside a git checkout")
	}
	tmp := t.TempDir()
	_, err := WriteOverlay(filepath.Join(root, filepath.Base(tmp)), TeamProfileFilename, "content")
	if err == nil {
		t.Fatal("expected WriteOverlay to refuse a target inside this suite's own checkout")
	}
}

func TestWriteOverlayWritesAndReturnsPath(t *testing.T) {
	dir := makeGitProject(t)
	dest, err := WriteOverlay(dir, TeamProfileFilename, "content: here\n")
	if err != nil {
		t.Fatalf("WriteOverlay: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "content: here\n" {
		t.Errorf("written content = %q", data)
	}
}

func TestDiscoverTargetRootFindsGitWorktree(t *testing.T) {
	// DiscoverTargetRoot shells out to a real `git`, which needs a real
	// repository (git init), not just a bare .git directory the way
	// makeGitProject's fixture is for tests that only check for .git's
	// *presence* as a boundary marker.
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Skipf("git init failed in this environment: %v: %s", err, out)
	}
	nested := filepath.Join(dir, "a", "b")
	os.MkdirAll(nested, 0o755)

	found, ok := DiscoverTargetRoot(nested)
	if !ok {
		t.Fatal("expected to discover the enclosing git worktree")
	}
	resolvedDir, _ := filepath.EvalSymlinks(dir)
	resolvedFound, _ := filepath.EvalSymlinks(found)
	if resolvedFound != resolvedDir {
		t.Errorf("found = %q, want %q", found, dir)
	}
}

func TestResolveTargetRootRejectsBothTargetForms(t *testing.T) {
	_, err := ResolveTargetRoot(ResolveTargetRootOptions{Target: "/a", PositionalTarget: "/b"})
	if err == nil {
		t.Fatal("expected an error when both TARGET and --target are given")
	}
}

func TestInspectRepairStateReportsMissingAsHealthy(t *testing.T) {
	dir := makeGitProject(t)
	sharedDir := realSharedDefaultsDirForTest(t)
	state, errs := InspectRepairState(dir, sharedDir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	for _, entry := range state {
		if entry.Status != "missing_uses_shipped_default" {
			t.Errorf("entry = %+v, want all missing_uses_shipped_default for an empty target", entry)
		}
	}
	if len(state) != len(InitOverlayFilenames) {
		t.Errorf("state has %d entries, want %d", len(state), len(InitOverlayFilenames))
	}
}

func realSharedDefaultsDirForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(wd, "..", "..", "roster", "shared")
	if _, err := os.Stat(filepath.Join(dir, TeamProfileFilename)); err != nil {
		t.Skip("not running inside the cadre checkout")
	}
	return dir
}
