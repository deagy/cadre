package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Operational documentation may not name a Python file that does not exist.
//
// Nine PRs of deletions left a trail: roster/RUNBOOK.md told people to run
// `python3 roster/orchestration/src/schema_validate.py`, CLAUDE.md described
// the selector as select_agents.py, and a pre-commit hook invoked a
// staged_records.py that had been gone for weeks. None of it failed anything.
// Documentation drifts silently by construction -- nothing executes it.
//
// The scope is deliberately narrow, and the line is not "mentions Python" but
// "tells the reader how this repository works today":
//
//   - Included: the files someone reads to operate or extend the repo. A dead
//     name there is an instruction that cannot be followed.
//   - Excluded: records of what was true at a point in time -- CHANGELOG.md,
//     the lifecycle run records, knowledge-store entries, migration and
//     investigation write-ups, and the refactor plans. Naming the Python they
//     replaced is their subject matter. Rewriting them would be falsifying a
//     record to satisfy a test.
//
// Generated output under plugin/ and cline-plugins/ is excluded because it is
// derived: its content comes from the sources listed here, and fixing a source
// fixes every copy. A dead name that appears only in generated output means
// the generator injects it, which is a different defect from this one.

// operationalDocs are the files that describe how this repository works now.
//
// Listed explicitly rather than discovered, because the interesting decision
// is which documents make a claim about the present -- and that is a judgement
// per file, not a path pattern.
var operationalDocs = []string{
	"AGENTS.md",
	"CLAUDE.md",
	"CONTRIBUTING.md",
	"DISTRIBUTION.md",
	"README.md",
	"REMAINING_PYTHON_SCOPE.md",
	".pre-commit-config.yaml",
	"bin/README.md",
	"docs/adopt-cadre-quickstart.md",
	"docs/sample-selection-output.md",
	"docs/terminology.md",
	"docs/which-runner-am-i-in.md",
	"engine/README.md",
	"internal/knowledge/README.md",
	"kernel/README.md",
	"roster/RUNBOOK.md",
	"roster/catalog.yaml",
	"roster/context-store/README.md",
	"roster/context-store/SECURITY.md",
	"roster/orchestration/GITLAB-EVIDENCE.md",
	"roster/orchestration/SECURITY-CONTROLS.md",
	"roster/orchestration/routing-doctrine.md",
	"roster/shared/README.md",
	"roster/shared/team-profile.yaml",
	"roster/shared/workspace-isolation.md",
	"roster/workflows/unclassified.md",
}

// operationalDocGlobs cover directories where every file is operational.
var operationalDocGlobs = []string{
	filepath.Join(".agents", "skills", "*", "SKILL.md"),
	filepath.Join(".agents", "skills", "*", "references", "*.md"),
}

// namesThatNeverReferToAFileHere are module names that appear in operational
// prose without naming anything in this repository.
//
// Distinct from the inline (deleted) marker, which says "this line
// deliberately names something that was removed". These names never referred
// to a file here at all, so the exemption is global rather than per-line.
//
// Kept as a map with a reason each, so adding one is a sentence somebody has
// to write rather than a silent widening.
var namesThatNeverReferToAFileHere = map[string]string{
	"conftest.py": "a pytest convention, named alongside TestMain and a " +
		"package.json script as examples of repository-controlled code that a " +
		"build tool executes by design",
}

// pythonFileReference matches a bare module filename. Deliberately not a path:
// the same name is written as `select_agents.py`, as
// `roster/orchestration/src/select_agents.py`, and inside backticks, and all
// three are equally wrong once the file is gone.
var pythonFileReference = regexp.MustCompile(`\b([a-z_][a-z0-9_]*\.py)\b`)

// livePythonFiles is every .py this repository still tracks, by basename.
//
// Asked of git rather than the filesystem, and that is not a stylistic
// preference. A filesystem walk finds every stale worktree under .worktrees/
// and .claude/worktrees/ -- each a full checkout of an older commit, complete
// with the Python this migration deleted. The first version of this test
// walked the tree and reported 5 dead references; every other deleted module
// looked alive because a worktree still had a copy. git tracks this
// repository, which is what the documentation is about.
func livePythonFiles(t *testing.T) map[string]bool {
	t.Helper()
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	command := exec.Command("git", "ls-files", "*.py")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Skipf("cannot list tracked files (not a git checkout?): %v", err)
	}
	live := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		live[filepath.Base(line)] = true
	}
	if len(live) < 10 {
		t.Fatalf("git reports %d tracked Python files; the listing is broken, "+
			"not the tree", len(live))
	}
	return live
}

func TestNoOperationalDocNamesADeletedPythonFile(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	if _, err := os.Stat(filepath.Join(root, "roster")); err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	live := livePythonFiles(t)

	paths := append([]string{}, operationalDocs...)
	for _, glob := range operationalDocGlobs {
		matches, err := filepath.Glob(filepath.Join(root, glob))
		if err != nil {
			t.Fatalf("bad glob %q: %v", glob, err)
		}
		for _, match := range matches {
			relative, _ := filepath.Rel(root, match)
			paths = append(paths, filepath.ToSlash(relative))
		}
	}

	read := 0
	var findings []string
	for _, relative := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			// A listed file that is gone is itself a finding: the list is
			// meant to track the operational set, not decay quietly.
			findings = append(findings, relative+" is listed as operational but does not exist")
			continue
		}
		read++
		seen := map[string]bool{}
		for number, line := range strings.Split(string(content), "\n") {
			// A line may name a deleted module deliberately -- a comment
			// explaining what a removed hook used to run is more useful with
			// the name in it, and `git log -S` needs it. The escape is
			// explicit and greppable rather than inferred from phrasing, so
			// the author has to state that the file is gone.
			if strings.Contains(line, "(deleted)") || strings.Contains(line, "(removed)") {
				continue
			}
			for _, match := range pythonFileReference.FindAllStringSubmatch(line, -1) {
				name := match[1]
				if live[name] || seen[name] {
					continue
				}
				if _, exempt := namesThatNeverReferToAFileHere[name]; exempt {
					continue
				}
				seen[name] = true
				findings = append(findings,
					relative+":"+itoaLocal(number+1)+" names "+name+", which does not exist")
			}
		}
	}
	if read < len(operationalDocs) {
		t.Fatalf("read %d of %d listed documents", read, len(operationalDocs))
	}
	sort.Strings(findings)
	if len(findings) > 0 {
		t.Errorf("%d dead Python reference(s) in operational documentation:\n  %s\n\n"+
			"These describe how the repository works now, so a name that does not "+
			"resolve is an instruction nobody can follow. If the document is a "+
			"record of what was once true, it belongs in the excluded set instead "+
			"-- see this file's header.",
			len(findings), strings.Join(findings, "\n  "))
	}
}

func TestTheDeadReferenceScanWouldNoticeOne(t *testing.T) {
	// Guards the guard. The scan passes over clean documentation, which is also
	// what it does if the pattern matches nothing or every name looks live.
	live := livePythonFiles(t)

	for _, dead := range []string{
		"run `python3 roster/orchestration/src/schema_validate.py`",
		"see `select_agents.py`'s header",
		"generate_global_plugin.py writes the distribution",
	} {
		matches := pythonFileReference.FindAllStringSubmatch(dead, -1)
		if len(matches) == 0 {
			t.Errorf("the pattern found no module name in: %s", dead)
			continue
		}
		if live[matches[0][1]] {
			t.Errorf("%q is reported as live; this fixture assumed it was deleted",
				matches[0][1])
		}
	}
	// And a file that does exist is not reported.
	//
	// port_cline_agents.py stood here until it became
	// internal/generators/cline_port.go. The test said to update this fixture
	// rather than the scan if that happened, and then caught itself doing it.
	// guard_workspace_mutation.py stood here too, on the reasoning that a
	// PreToolUse hook must run on an installer's machine with no setup. It is
	// a compiled binary shipped per platform now, which needs no setup either
	// and needs no network. This fixture has been updated twice by the change
	// it was written to notice.
	//
	// bootstrap_sdlc.py remains: it installs the kernel, so it runs before a
	// working cadre is a given.
	for _, alive := range []string{"bootstrap_sdlc.py"} {
		if !live[alive] {
			t.Errorf("%s is expected to still exist; if it was deleted, update this "+
				"fixture rather than the scan", alive)
		}
	}
	// A sentence with no module name at all yields nothing.
	if got := pythonFileReference.FindAllString("the Go CLI has no Python left", -1); len(got) != 0 {
		t.Errorf("the pattern matched %v in a line naming no module", got)
	}
}

func itoaLocal(n int) string { //nolint:unused
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestNoOperationalDocPointsAtAnEmptyTestSuite(t *testing.T) {
	// The companion to the check above, for a failure it cannot see.
	//
	// CLAUDE.md told people to run `unittest discover -s
	// roster/knowledge-store/test` and `-s roster/shared/test` after both
	// directories had been emptied. Neither line names a .py file, so the
	// scan above passes over them -- and `discover` on an empty directory
	// reports "NO TESTS RAN" and exits 0. Following the instruction produced
	// a green result that asserted nothing, which is worse than an
	// instruction that fails.
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	if _, err := os.Stat(filepath.Join(root, "roster")); err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	discover := regexp.MustCompile(`discover\s+(?:-b\s+)?-s\s+(\S+)`)

	checked := 0
	var findings []string
	for _, relative := range operationalDocs {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			continue
		}
		checked++
		for number, line := range strings.Split(string(content), "\n") {
			for _, match := range discover.FindAllStringSubmatch(line, -1) {
				directory := filepath.Join(root, filepath.FromSlash(match[1]))
				entries, err := os.ReadDir(directory)
				if err != nil {
					findings = append(findings, relative+":"+itoaLocal(number+1)+
						" runs a suite in "+match[1]+", which does not exist")
					continue
				}
				tests := 0
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "test_") &&
						strings.HasSuffix(entry.Name(), ".py") {
						tests++
					}
				}
				if tests == 0 {
					findings = append(findings, relative+":"+itoaLocal(number+1)+
						" runs a suite in "+match[1]+", which holds no test files")
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no operational documents were read")
	}
	sort.Strings(findings)
	if len(findings) > 0 {
		t.Errorf("%d documented test invocation(s) would assert nothing:\n  %s\n\n"+
			"`unittest discover` over an empty directory reports NO TESTS RAN and "+
			"exits 0. Following the instruction produces a green result covering "+
			"nothing, which is worse than an instruction that fails.",
			len(findings), strings.Join(findings, "\n  "))
	}
}
