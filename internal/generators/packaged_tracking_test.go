package generators

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Everything generate-plugin writes into plugin/ has to be committed.
//
// A GitHub-sourced marketplace serves this repository's tree, so an
// uncommitted file in the distribution is a plugin that installs missing that
// file. The generated half of plugin/ is committed deliberately for exactly
// that reason.
//
// The failure this exists for is not someone forgetting to `git add`. It is a
// generated path that .gitignore also matches, which makes the omission
// unfixable by the normal reflex: `git add -A` skips it silently, and the
// working tree looks correct because the file is right there on disk.
//
// plugin/bin/cadre hit this. The ignore rule for the compiled `cadre` binary
// is unanchored, so it matched the generated shell shim too. The shim stayed
// tracked -- ignore rules do not apply to tracked files -- until a
// generate-plugin run failed between removing it and rewriting it, at which
// point a `git add -A` staged the deletion and nothing could put it back.
// Locally everything passed; CI checked out a tree with no shim in it.

// notPartOfTheDistribution is what legitimately sits under plugin/ without
// being shipped. Each entry is runtime state or a build artifact that a fresh
// install creates for itself; none of them is generated content.
func notPartOfTheDistribution(relative string) bool {
	// Vendored dependencies, installed rather than committed.
	if strings.Contains(relative, "/node_modules/") {
		return true
	}
	// Interpreter bytecode, which outlives the source it was compiled from --
	// these were left behind by test modules deleted in this change.
	if strings.Contains(relative, "/__pycache__/") {
		return true
	}
	// The knowledge store a local run creates: a config naming this machine's
	// paths and the database itself. Committing either would ship one
	// developer's store to every install.
	return strings.HasPrefix(relative, "plugin/.agents/knowledge-store/")
}

func TestEveryPackagedFileIsTrackedByGit(t *testing.T) {
	root := repositoryRoot(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	listing := exec.Command("git", "ls-files", "plugin")
	listing.Dir = root
	output, err := listing.Output()
	if err != nil {
		t.Skipf("cannot list tracked files: %v", err)
	}
	tracked := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		if relative := strings.TrimSpace(line); relative != "" {
			tracked[relative] = true
		}
	}
	if len(tracked) < 100 {
		t.Fatalf("only %d tracked files under plugin/; the listing is broken", len(tracked))
	}

	seen := 0
	var untracked []string
	err = filepath.Walk(filepath.Join(root, "plugin"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if notPartOfTheDistribution(relative) {
			return nil
		}
		seen++
		if !tracked[relative] {
			untracked = append(untracked, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking plugin/: %v", err)
	}
	if seen < 100 {
		t.Fatalf("walked %d files under plugin/; the walk is broken", seen)
	}
	sort.Strings(untracked)
	if len(untracked) > 0 {
		t.Errorf("%d packaged file(s) exist on disk but are not tracked:\n  %s\n\n"+
			"The marketplace serves this repository's tree, so an untracked file is "+
			"one an install does not get. If `git add` refuses, check .gitignore: a "+
			"generated path an ignore rule also matches cannot be restored by "+
			"`git add -A` once it is out of the index.",
			len(untracked), strings.Join(untracked, "\n  "))
	}
	t.Logf("checked %d packaged files against the index", seen)
}

func TestTheGeneratedShimIsNotIgnored(t *testing.T) {
	// Stated separately, because the general check above only fires once the
	// file has already gone missing. This fails while it is still merely
	// ignorable, which is the point at which it is cheap to fix.
	root := repositoryRoot(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	for _, relative := range []string{
		"plugin/bin/cadre",
		"hooks/guard",
		"hooks/bin/cadre-guard-linux-amd64",
	} {
		// --no-index is load-bearing. Without it, check-ignore skips tracked
		// files and reports "not ignored" for every path that still exists in
		// the index -- so this could only ever have fired after the file was
		// already lost, which is the timing flaw it exists to avoid. With it,
		// the rule is evaluated against the path itself and the trap is
		// visible while it is still cheap to fix.
		check := exec.Command("git", "check-ignore", "-q", "--no-index", relative)
		check.Dir = root
		// check-ignore exits 0 when the path IS ignored.
		if err := check.Run(); err == nil {
			t.Errorf("%s is matched by a .gitignore rule. It is generated *and* "+
				"committed, so an ignore rule over it means a failed regeneration "+
				"can delete it and `git add -A` cannot put it back.", relative)
		}
	}
}
