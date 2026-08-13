// Package platform resolves filesystem locations the Cadre CLI depends on:
// the repository root and, eventually, per-OS config/cache directories.
//
// NOTE (Phase 1 scope note, see ADR-001-CLI-GO-REFACTOR.md and
// CADRE_CLI_GO_ARCHITECTURE.md §4.2): this package's full ownership was
// assigned to the application-engineer role working the same refactor
// ("coordinate with app-engineer" in the Phase 1 brief). At the time this
// file was written no application-engineer output existed yet in this
// worktree, so a minimal RepoRoot() is provided here to unblock SDLC
// delegation and the interop layer, which both need it to compile and to be
// testable. This implementation is intentionally narrow (only the .git
// upward-walk described in the architecture doc) and must be reconciled
// with -- not silently overridden by -- any parallel application-engineer
// implementation before Phase 1 is considered merged. Flag any collision to
// the reviewer rather than resolving it unilaterally.
package platform

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrRepoRootNotFound is returned by RepoRoot when no .git boundary is found
// by walking upward from the starting directory.
var ErrRepoRootNotFound = errors.New("platform: no .git boundary found above starting directory")

// RepoRoot walks upward from the current working directory looking for a
// `.git` entry (an ordinary directory in a normal checkout, or a file in a
// linked git worktree, where `.git` contains a `gitdir:` pointer instead of
// the administrative directory itself). The first directory containing
// either is returned as the repository root.
//
// This mirrors bin/cadre.py's REPO_ROOT, which is instead derived from the
// dispatcher script's own file location (BIN_DIR.parent) rather than an
// upward walk -- that shortcut is unavailable to a Go binary because a
// built binary is routinely installed somewhere outside the checkout
// entirely (e.g. on PATH via `go install`), so the working-directory walk
// used here is the only strategy that keeps working after installation.
func RepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return FindProjectRoot(cwd)
}

// FindProjectRoot performs the same upward `.git` walk as RepoRoot, starting
// from an explicit directory rather than the process's current working
// directory. Used by config discovery (application-engineer's
// internal/config package) to locate `.agents/cadre.yaml` relative to an
// arbitrary starting point, and directly by tests here.
//
// The walk is bounded to guard against a pathological filesystem (e.g. a
// symlink cycle) sending it into an unbounded loop; 64 levels is far beyond
// any real checkout depth.
func FindProjectRoot(from string) (string, error) {
	dir, err := filepath.Abs(from)
	if err != nil {
		return "", err
	}

	const maxWalkDepth = 64
	for i := 0; i < maxWalkDepth; i++ {
		gitPath := filepath.Join(dir, ".git")
		if _, statErr := os.Lstat(gitPath); statErr == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without finding a boundary.
			break
		}
		dir = parent
	}

	return "", ErrRepoRootNotFound
}
