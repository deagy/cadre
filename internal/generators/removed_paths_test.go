package generators

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A removed lifecycle path stays removed, in the source and in what ships.
//
// Two ways a deletion comes undone. The file can come back -- restored from a
// branch, or recreated by someone who found a reference to it and assumed it
// was missing. Or the file stays gone while a *reference* to it survives in
// something the generator copies, which produces a distribution that names a
// path nobody can follow.
//
// The second is the quieter one. Nothing fails to build, nothing fails to
// package: an installed plugin simply carries an instruction pointing at
// somewhere that does not exist, and the person who follows it is the one who
// finds out.
//
// Ported from test_repository_health.py's
// test_removed_lifecycle_migration_utility_cannot_ship and
// test_packaged_runtime_has_no_removed_lifecycle_paths.

// removedLifecyclePaths are strings that must not appear in the distribution,
// each with what it used to be. The reason is the useful half: without it, a
// future reader finds a list of tokens and no way to judge whether an entry is
// still worth enforcing.
var removedLifecyclePaths = map[string]string{
	"migrate_execution_summary": "a one-off lifecycle migration utility, deleted " +
		"once the migration it performed was complete; a distribution naming it " +
		"tells someone to run a script that is not there",
	"plugins/agentic-sdlc": "the pre-merge layout of the lifecycle plugin; the " +
		"packaged tree no longer has that directory, so a path through it " +
		"resolves nowhere on an installed plugin",
}

func TestTheRemovedLifecycleUtilityIsGoneFromTheSource(t *testing.T) {
	// The direct half: the file itself. A deletion that got reverted shows up
	// here rather than as a mysterious reference in the package.
	root := repositoryRoot(t)
	path := filepath.Join(root, "roster", "orchestration", "src",
		"migrate_execution_summary.py")
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s is back. It was a one-off migration utility, deleted once "+
			"the migration was complete.", path)
	}
}

func TestNothingInTheDistributionNamesARemovedLifecyclePath(t *testing.T) {
	packageRoot, _ := freshPackage(t)

	scanned := 0
	var offenders []string
	for _, path := range packagedFiles(t, packageRoot) {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		scanned++
		relative, _ := filepath.Rel(packageRoot, path)
		text := string(content)
		for needle, why := range removedLifecyclePaths {
			if strings.Contains(text, needle) {
				offenders = append(offenders,
					filepath.ToSlash(relative)+" names "+needle+" -- "+why)
			}
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned %d packaged files; the walk is broken", scanned)
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("%d packaged file(s) name a removed lifecycle path:\n  %s\n\n"+
			"Nothing fails to build or package. The installed plugin simply "+
			"carries an instruction pointing somewhere that does not exist.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
	t.Logf("scanned %d packaged files for %d removed paths",
		scanned, len(removedLifecyclePaths))
}

func TestTheRemovedPathScanWouldNoticeOne(t *testing.T) {
	// Guards the guard. The scan above passes over a clean distribution, which
	// is also what it does if the needle list is empty or the comparison never
	// matches.
	if len(removedLifecyclePaths) == 0 {
		t.Fatal("no removed paths are listed; the scan checks nothing")
	}
	for needle, why := range removedLifecyclePaths {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%q is listed without a reason", needle)
		}
		// The needles are substrings, not paths, so a file merely mentioning
		// one in prose is a finding too -- which is intended: an instruction
		// naming a dead path is the failure, whether or not it is a real
		// filename.
		sample := "see " + needle + " for details"
		if !strings.Contains(sample, needle) {
			t.Errorf("the comparison would not find %q in %q", needle, sample)
		}
	}
	// And a distribution that contains none of them is what passing means,
	// rather than the scan having looked at nothing.
	if _, present := removedLifecyclePaths["migrate_execution_summary"]; !present {
		t.Error("the utility this guard was written for is no longer listed")
	}
}
