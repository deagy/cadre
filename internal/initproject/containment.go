// containment.go ports init_project.py's A-002 containment/self-checkout
// guard and its single write chokepoint (WriteOverlay), the only function
// in this package allowed to write generated overlay output to disk.
package initproject

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/config"
	"github.com/deagy/cadre/cli/internal/platform"
)

// suiteCheckoutRoot is this Cadre checkout's own root -- the Go equivalent
// of init_project.py's REPO_ROOT (SRC_DIR.parents[2], derived from
// settings.py's own __file__ location at import time, a fixed value
// independent of cwd). A compiled Go binary has no equivalent of __file__,
// so this instead walks from cwd to the nearest .git boundary
// (platform.RepoRoot's algorithm, the same deviation already documented
// and used by roster.root's computed default in
// internal/config/registry.go) -- an approximation that matches Python's
// behavior exactly when invoked from within the checkout (the common case:
// a maintainer testing this feature, or bin/cadre's own self-build/exec
// wrapper always running from inside the checkout it built), and degrades
// gracefully (self-checkout guard simply doesn't fire) rather than
// guessing when invoked from a genuinely unrelated location.
func suiteCheckoutRoot() (string, bool) {
	root, err := platform.RepoRoot()
	if err != nil {
		return "", false
	}
	return root, true
}

func selfCheckoutMarkersPresent(root string) bool {
	teamProfile := filepath.Join(root, "roster", "shared", TeamProfileFilename)
	subcommands := filepath.Join(root, "bin", "subcommands.tsv")
	if info, err := os.Stat(teamProfile); err != nil || info.IsDir() {
		return false
	}
	if info, err := os.Stat(subcommands); err != nil || info.IsDir() {
		return false
	}
	return true
}

// refuseIfSelfCheckoutResolved is the same check as RefuseIfSelfCheckout,
// but operating on a path the caller has already resolved. Callers that
// also need the resolved identity for something else (e.g. WriteOverlay,
// which needs it to compute the destination path too) must resolve
// targetRoot exactly once and pass that SAME resolved value here,
// immediately before using it, rather than resolving it again
// independently (a TOCTOU gap: an earlier, separate resolve used only for
// the check could diverge from a later, separate resolve actually used for
// the write, e.g. via a symlink swapped in between calls).
func refuseIfSelfCheckoutResolved(resolved string) error {
	suiteRoot, ok := suiteCheckoutRoot()
	if ok && config.IsSameOrDescendant(resolved, suiteRoot) {
		return initErrorf(
			"refusing to write into this agents suite's own checkout (%s); "+
				"`cadre init` writes only into a consuming project's .agents/shared/, never here", suiteRoot)
	}
	// Walk from the target up through its ancestors (not just the target
	// itself), so a target that is a genuine subdirectory of an unrelated
	// clone of this same suite is also refused.
	current := config.ResolveExistingAncestor(resolved)
	for {
		if selfCheckoutMarkersPresent(current) {
			return initErrorf(
				"refusing to write: %s contains roster/shared/%s and bin/subcommands.tsv, so it "+
					"looks like another checkout of this same agents suite rather than a consuming project",
				current, TeamProfileFilename)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

// RefuseIfSelfCheckout returns an *InitError if targetRoot is this suite's
// own checkout (or a descendant of it), whether that's this exact clone
// (compared by filesystem identity, not path string) or an unrelated clone
// of the same repository (detected by marker files present at the target
// or any of its ancestors).
func RefuseIfSelfCheckout(targetRoot string) error {
	resolved, err := filepath.Abs(targetRoot)
	if err != nil {
		resolved = targetRoot
	}
	if r, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = r
	}
	return refuseIfSelfCheckoutResolved(resolved)
}

// DiscoverTargetRoot returns the enclosing Git worktree root, when start
// (empty string means cwd) is in one. cadre init owns project-local
// overlays, so running it from a nested directory should still select the
// project rather than that implementation subdirectory. Asks git rather
// than treating a path merely named ".git" as proof of a worktree: linked
// worktrees and submodules are supported, and an incidental non-Git
// directory must not become a write target by default.
func DiscoverTargetRoot(start string) (string, bool) {
	if start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		start = wd
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", start, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	topLevel := strings.TrimSuffix(string(out), "\n")
	if topLevel == "" {
		return "", false
	}
	abs, err := filepath.Abs(topLevel)
	if err != nil {
		return topLevel, true
	}
	return abs, true
}

// ResolveTargetRootOptions bundles ResolveTargetRoot's inputs, mirroring
// resolve_target_root's argparse.Namespace fields.
type ResolveTargetRootOptions struct {
	Target           string // --target
	PositionalTarget string // TARGET
}

// ResolveTargetRoot resolves the positional/legacy target inputs or infers
// one from the CWD.
func ResolveTargetRoot(opts ResolveTargetRootOptions) (string, error) {
	if opts.Target != "" && opts.PositionalTarget != "" {
		return "", initErrorf("provide either TARGET or --target, not both")
	}
	if opts.Target != "" {
		return opts.Target, nil
	}
	if opts.PositionalTarget != "" {
		return opts.PositionalTarget, nil
	}
	inferred, ok := DiscoverTargetRoot("")
	if !ok {
		return "", initErrorf("could not determine a Git worktree from the current directory; provide TARGET or --target")
	}
	return inferred, nil
}

// WriteOverlay is the ONLY function in this package allowed to write
// generated overlay output to disk. Resolves targetRoot/.agents/shared/
// filename, requires it stays under targetRoot's resolved path (rejecting
// symlink escapes), and refuses self-checkout targets (A-002).
//
// Finding B (TOCTOU): targetRoot is resolved exactly ONCE, into
// resolvedRoot, and that same resolved value is what both the destination
// path is built from AND what the self-checkout check runs against,
// immediately before the write. There is no separate, independent resolve
// of targetRoot anywhere else in this function whose result could diverge
// from the one actually used for the write.
func WriteOverlay(targetRoot, filename, content string) (string, error) {
	resolvedRoot, err := filepath.Abs(targetRoot)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(resolvedRoot); err == nil {
		resolvedRoot = r
	}
	if info, err := os.Stat(resolvedRoot); err != nil || !info.IsDir() {
		return "", initErrorf("--target does not exist or is not a directory: %s", targetRoot)
	}
	dest := filepath.Join(resolvedRoot, config.ProjectOverlayRelativeDir, filename)
	resolvedDest := dest
	if r, err := filepath.EvalSymlinks(dest); err == nil {
		resolvedDest = r
	}
	if !config.IsSameOrDescendant(resolvedDest, resolvedRoot) {
		return "", initErrorf("refusing to write outside target root (possible symlink escape): %s", dest)
	}
	// Re-verify self-checkout identity against resolvedRoot itself,
	// immediately before the write, using the SAME resolved value computed
	// above rather than resolving targetRoot again.
	if err := refuseIfSelfCheckoutResolved(resolvedRoot); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

func existingOverlayPath(targetRoot, filename string) string {
	resolved, err := filepath.Abs(targetRoot)
	if err != nil {
		resolved = targetRoot
	}
	return filepath.Join(resolved, config.ProjectOverlayRelativeDir, filename)
}

func readExistingOverlayText(targetRoot, filename string) (string, bool) {
	path := existingOverlayPath(targetRoot, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// RepairStateEntry is one overlay's inspection result from InspectRepairState.
type RepairStateEntry struct {
	Path   string
	Status string // "missing_uses_shipped_default" | "valid_project_overlay"
}

// InspectRepairState inspects every overlay cadre init can own without
// changing it. Sparse overlays intentionally use a missing file to mean
// "keep the shipped default," so a missing file is healthy rather than
// something a repair may materialize. Existing overlays are project
// decisions; repair validates and reports them but never changes a field
// itself.
func InspectRepairState(targetRoot, sharedDefaultsDir string) ([]RepairStateEntry, []string) {
	var state []RepairStateEntry
	var errs []string
	resolvedRoot, err := filepath.Abs(targetRoot)
	if err != nil {
		resolvedRoot = targetRoot
	}

	for _, filename := range InitOverlayFilenames {
		path := existingOverlayPath(targetRoot, filename)
		info, statErr := os.Lstat(path)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			errs = append(errs, filename+": refusing to inspect symlinked overlay: "+path)
			continue
		}
		if statErr != nil {
			state = append(state, RepairStateEntry{Path: path, Status: "missing_uses_shipped_default"})
			continue
		}
		fileInfo, err := os.Stat(path)
		if err != nil || fileInfo.IsDir() {
			errs = append(errs, filename+": overlay is not a regular file: "+path)
			continue
		}
		resolvedPath := path
		if r, err := filepath.EvalSymlinks(path); err == nil {
			resolvedPath = r
		}
		if !config.IsSameOrDescendant(resolvedPath, resolvedRoot) {
			errs = append(errs, filename+": refusing to inspect path outside target root (possible symlink escape): "+path)
			continue
		}
		text, _ := os.ReadFile(path)
		if filename == TechnologyStandardsFilename || filename == GuardrailsFilename {
			if err := checkManagedBlockWellFormed(string(text)); err != nil {
				errs = append(errs, filename+": "+err.Error())
				continue
			}
		}
		// Validates structured content and the autonomy narrowing rule
		// through the same production resolver consumers use.
		if _, err := config.ResolveSharedConfig(sharedDefaultsDir, filename, targetRoot); err != nil {
			errs = append(errs, filename+": "+err.Error())
			continue
		}
		state = append(state, RepairStateEntry{Path: path, Status: "valid_project_overlay"})
	}
	return state, errs
}

func checkManagedBlockWellFormed(text string) error {
	starts := strings.Count(text, ManagedStart)
	ends := strings.Count(text, ManagedEnd)
	if starts != ends || starts > 1 || (starts == 1 && strings.Index(text, ManagedStart) > strings.Index(text, ManagedEnd)) {
		return initErrorf("incomplete or ambiguous cadre init managed block")
	}
	return nil
}

// ValidateOverlayContent round-trips filename/content through
// ResolveSharedConfig against a throwaway project tree, exactly like
// A-001 requires, before any real write is planned.
func ValidateOverlayContent(sharedDefaultsDir, filename, content string) error {
	tmp, err := os.MkdirTemp("", "agents-init-validate-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0o755); err != nil {
		return err
	}
	overlayDir := filepath.Join(tmp, config.ProjectOverlayRelativeDir)
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(overlayDir, filename), []byte(content), 0o644); err != nil {
		return err
	}
	project := filepath.Join(tmp, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		return err
	}
	_, err = config.ResolveSharedConfig(sharedDefaultsDir, filename, project)
	return err
}
