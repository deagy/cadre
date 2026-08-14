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
	"fmt"
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

// FindFileAtProjectRoot walks upward from start (empty string means cwd)
// looking for a project-local file at relativePath. Stops at the first
// directory containing that file, or at the first directory containing
// .git (the project boundary) if no match is found first, so a file above
// the project root is never picked up.
//
// This is the single implementation of the walk-up-to-.git discovery
// convention shared across this repository's project-local override
// mechanisms -- mirrors roster/shared/src/resolve.py's
// find_file_at_project_root exactly (both the algorithm and its "don't
// introduce a fourth distinct find-the-project-root convention" rule,
// which applies equally to this Go port: internal/config's project-local
// .agents/cadre.yaml discovery and internal/orchestration's
// .agents/orchestration/routing-overlay.json discovery both call this
// function rather than reimplementing the walk).
func FindFileAtProjectRoot(relativePath, start string) (string, bool) {
	current := start
	if current == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		current = wd
	}
	current, err := filepath.Abs(current)
	if err != nil {
		return "", false
	}

	const maxWalkDepth = 64
	for i := 0; i < maxWalkDepth; i++ {
		candidate := filepath.Join(current, relativePath)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return "", false
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
	return "", false
}

// FindInstallationRoot locates the root directory of *this CLI's own
// installation* (where roster/, kernel/, provider/ live), rather than the
// user's project.
//
// This differs from FindProjectRoot, which walks up from cwd to find a .git
// boundary (the user's project). A packaged plugin install has no .git at all,
// so FindProjectRoot fails; FindInstallationRoot succeeds by trying
// CADRE_REPO_ROOT, the executable's directory, and cwd in turn.
//
// Resolution order, first hit wins:
//
//  1. $CADRE_REPO_ROOT, exported by bin/cadre and bin/cadre.ps1 so the
//     built binary under .cadre-build-cache/ knows which checkout produced
//     it without any filesystem guessing.
//  2. Upward from the running executable's own directory, which covers a
//     binary built into the checkout (or installed beside a vendored tree)
//     when no wrapper set the variable.
//  3. Upward from the working directory, the last resort, which is correct
//     whenever the caller happens to be inside a Cadre checkout.
//
// It verifies each candidate by checking for the existence of roster/
// (a directory that exists only in the installation, not in user projects).
func FindInstallationRoot() (string, error) {
	const markerPath = "roster"
	const maxWalkDepth = 64

	// Try environment variable first
	if root := os.Getenv("CADRE_REPO_ROOT"); root != "" {
		markerDir := filepath.Join(root, markerPath)
		if _, err := os.Stat(markerDir); err == nil {
			return root, nil
		}
	}

	// Try upward from the executable
	if executable, err := os.Executable(); err == nil {
		if resolved, linkErr := filepath.EvalSymlinks(executable); linkErr == nil {
			executable = resolved
		}
		if root, found := findAncestorWith(filepath.Dir(executable), markerPath, maxWalkDepth); found {
			return root, nil
		}
	}

	// Try upward from the working directory
	if wd, err := os.Getwd(); err == nil {
		if root, found := findAncestorWith(wd, markerPath, maxWalkDepth); found {
			return root, nil
		}
	}

	return "", fmt.Errorf("cannot locate Cadre installation (roster/ directory); set CADRE_REPO_ROOT to a Cadre checkout, or run from inside one")
}

// findAncestorWith walks upward from start looking for an ancestor directory
// containing a specific subdirectory (the markerPath). Also checks for the
// plugin layout where content is at suite/<markerPath> (e.g., <plugin>/suite/roster).
//
// Returns the ancestor (or ancestor/suite if that layout was found) and true if
// found, or ("", false) if the search exhausts depth or reaches the filesystem root.
func findAncestorWith(start, markerPath string, maxDepth int) (string, bool) {
	directory, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}

	for i := 0; i < maxDepth; i++ {
		// Check for direct marker path (normal checkout: <root>/roster)
		candidate := filepath.Join(directory, markerPath)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return directory, true
		}

		// Check for plugin layout (packaged plugin: <plugin>/suite/roster)
		suiteCandidate := filepath.Join(directory, "suite", markerPath)
		if info, err := os.Stat(suiteCandidate); err == nil && info.IsDir() {
			return filepath.Join(directory, "suite"), true
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}

	return "", false
}
