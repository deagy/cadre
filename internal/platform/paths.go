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
	"strings"
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
// The walk also stops *below* the user's home directory, and that bound is
// the point rather than a safety margin. The .git boundary holds only when
// a project root exists; with no .git anywhere in the ancestry the walk
// otherwise climbed to $HOME, where ~/.agents/<store>/config.json is the
// *global* store's own configuration. The same directory then answered to
// both tiers depending only on where the caller stood: the global store
// resolved as project-local, KNOWLEDGE_STORE_HOME was consulted and
// silently ignored, and writes landed in the shared store while the caller
// believed they were project-scoped. That is not hypothetical -- it
// happened during verification and needed an authorized deletion to
// reverse (issue #249).
//
// A project cannot be $HOME, so refusing to look there costs nothing and
// removes the aliasing entirely.
//
// IMPORTANT: This function's file-existence check uses os.Stat, not os.Lstat,
// so it follows symlinks. The returned path may be a symlink pointing outside
// the project root. Callers that are about to read the discovered file's
// content must guard against symlink escapes using RejectSymlinkEscapeOnRead
// or RejectSymlinkEscapeOnReadWithDepth, depending on the discovered file's
// depth under the project root (see those functions' documentation).
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

	// Resolved once, outside the loop: a walk that consults the environment
	// on every iteration can change its own boundary mid-walk.
	home, homeErr := os.UserHomeDir()
	if homeErr == nil {
		home, _ = filepath.Abs(home)
	}

	const maxWalkDepth = 64
	for i := 0; i < maxWalkDepth; i++ {
		// At or above home is not a project. Checked before the candidate,
		// because the file that would match there is the global store's own.
		if homeErr == nil && (current == home || isAncestorOf(current, home)) {
			return "", false
		}
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

// isAncestorOf reports whether dir is a strict ancestor of other.
func isAncestorOf(dir, other string) bool {
	rel, err := filepath.Rel(dir, other)
	if err != nil || rel == "." {
		return false
	}
	return !strings.HasPrefix(rel, "..")
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

	// Try the environment variable first, against the same layouts the
	// ancestor walk uses -- a variable pointing at an install must resolve
	// exactly as that install would resolve on its own.
	if root := os.Getenv("CADRE_REPO_ROOT"); root != "" {
		for _, layout := range installationLayouts {
			base := root
			if layout != "" {
				base = filepath.Join(root, filepath.FromSlash(layout))
			}
			if _, err := os.Stat(filepath.Join(base, markerPath)); err == nil {
				return base, nil
			}
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

// installationLayouts are the directory shapes an installation can take,
// in precedence order. Each is a prefix that sits between an ancestor
// directory and the marker directory.
//
//   - ""            checkout, and a binary built into one: <root>/roster
//   - "suite"       packaged plugin:                       <plugin>/suite/roster
//   - "share/cadre" pip/pipx wheel:                        <prefix>/share/cadre/roster
//
// The wheel layout is last because it is the only one that can plausibly
// coexist with another: a developer with cadre pip-installed into a venv,
// working inside a checkout, must get the checkout's roster/ and not the
// installed copy. Precedence here, not luck, is what guarantees that.
//
// share/cadre rather than the bare prefix: a `pip install --user` puts
// sys.prefix at ~/.local, and installing a roster/ directory there would
// litter a shared prefix with a name nothing else would recognise.
var installationLayouts = []string{"", "suite", "share/cadre"}

// findAncestorWith walks upward from start looking for an ancestor directory
// containing markerPath under one of the known installation layouts.
//
// One full ascent per layout, in precedence order, rather than one ascent
// testing every layout at each level: a nearer directory matching a
// lower-precedence layout must not beat a further one matching a higher.
// A checkout root ten levels up still wins over a wheel install one level
// up, which is what makes an editable checkout authoritative for a
// developer who also has the wheel installed.
//
// Returns the directory the marker lives directly inside -- for a nested
// layout that is the ancestor plus the layout prefix, not the ancestor
// itself -- and true, or ("", false) if the search exhausts depth or reaches
// the filesystem root.
func findAncestorWith(start, markerPath string, maxDepth int) (string, bool) {
	directory, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}

	for _, layout := range installationLayouts {
		current := directory
		for i := 0; i < maxDepth; i++ {
			base := current
			if layout != "" {
				base = filepath.Join(current, filepath.FromSlash(layout))
			}
			if info, err := os.Stat(filepath.Join(base, markerPath)); err == nil && info.IsDir() {
				return base, true
			}

			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
	}

	return "", false
}

// SameOrDescendantError is returned by RejectSymlinkEscapeOnRead when a
// discovered file's resolved path escapes the project root.
type SameOrDescendantError struct{ msg string }

func (e *SameOrDescendantError) Error() string { return e.msg }

// resolveExistingAncestor returns the nearest existing ancestor of path (or
// path itself, if it already exists). Used so filesystem-identity comparisons
// still work against a path that does not exist yet, by anchoring the
// comparison at whatever prefix of it is already real on disk.
func resolveExistingAncestor(path string) string {
	current, err := filepath.Abs(path)
	if err != nil {
		current = path
	}
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err == nil {
				return resolved
			}
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return current
		}
		current = parent
	}
}

// isSameOrDescendant is a filesystem-identity containment check: true if
// path IS ancestor, or is located under it. Uses device/inode identity
// (os.SameFile) rather than string/resolved-path equality, so it isn't
// fooled by a case-insensitive filesystem where two differently-cased
// paths are actually the same on-disk directory. ancestor is required to
// already exist; path may not exist yet (its nearest existing ancestor is
// used as the anchor for the walk up).
func isSameOrDescendant(path, ancestor string) bool {
	ancestorAbs, err := filepath.Abs(ancestor)
	if err != nil {
		return false
	}
	ancestorResolved, err := filepath.EvalSymlinks(ancestorAbs)
	if err != nil {
		ancestorResolved = ancestorAbs
	}
	ancestorInfo, err := os.Stat(ancestorResolved)
	if err != nil {
		return false
	}

	probe := resolveExistingAncestor(path)
	for {
		probeInfo, err := os.Stat(probe)
		if err == nil && os.SameFile(probeInfo, ancestorInfo) {
			return true
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return false
		}
		probe = parent
	}
}

// RejectSymlinkEscapeOnReadWithDepth guards against symlink escapes for a
// file discovered at a specific relative depth under the project root.
// levelsUp is the number of directory levels from the candidate file to the
// project root (e.g., 2 for .agents/cadre.yaml, 3 for .agents/shared/<filename>).
//
// Discovery's file-exists check follows symlinks (os.Stat, not os.Lstat), so a
// malicious file or symlinked directory shipped in an untrusted, clonable project
// can point outside the project entirely. This function rejects that before the
// file is ever opened/parsed, by verifying that the symlink-resolved path is
// within the project root.
//
// If the candidate resolves outside the project root, returns an error.
// Otherwise returns the candidate path unchanged.
func RejectSymlinkEscapeOnReadWithDepth(candidate string, levelsUp int) (string, error) {
	root := candidate
	for i := 0; i < levelsUp; i++ {
		root = filepath.Dir(root)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		resolvedCandidate = candidate
	}
	if !isSameOrDescendant(resolvedCandidate, rootAbs) {
		return "", &SameOrDescendantError{msg: fmt.Sprintf(
			"%s resolves outside of %s (via a symlink); a project-local configuration "+
				"file/directory may not point outside the project it was found in", candidate, rootAbs)}
	}
	return candidate, nil
}

// RejectSymlinkEscapeOnRead guards the read path for .agents/cadre.yaml files.
// It delegates to RejectSymlinkEscapeOnReadWithDepth with levelsUp=2 since
// .agents/cadre.yaml is exactly two directory levels below the project root.
func RejectSymlinkEscapeOnRead(candidate string) (string, error) {
	return RejectSymlinkEscapeOnReadWithDepth(candidate, 2)
}
