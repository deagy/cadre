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
