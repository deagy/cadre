package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// An instruction may not name a test suite that would run nothing.
//
// `unittest discover` over a directory with no matching files is the quiet
// failure: on Python 3.10 it prints "Ran 0 tests / NO TESTS RAN" and exits 0,
// which passes while asserting nothing, and on 3.12+ it exits 5, which reads as
// a broken command rather than a stale instruction. This repository supports
// both, so the same line is a false green for one reader and a confusing red
// for another.
//
// This is not hypothetical. When the Python suites under roster/ were ported,
// bin/init-cline-memory and bin/bootstrap-cline-worktree kept telling agents to
// run four of them -- roster/knowledge-store/test, roster/orchestration/test,
// roster/shared/test and kernel/test. Those scripts write a Cline memory bank,
// so the instruction reached every agent that opened the repository, and three
// of the four directories still existed with no tests left in them. Nothing
// failed, because nothing runs documentation.
//
// The sibling guard, TestNoOperationalDocNamesADeletedPythonFile, does not
// cover this: a directory is not a deleted .py file, and roster/shared/test is
// still there.

// suiteInstructionFiles are the files that tell someone -- a person or an agent
// -- how to run this repository's tests.
//
// Listed explicitly rather than globbed, for the same reason the sibling guard
// lists its documents: which files make a claim about the present is a
// judgement per file. bin/ scripts are included because two of them generate
// agent-facing memory banks, which is how the stale instructions travelled.
var suiteInstructionFiles = []string{
	"AGENTS.md",
	"CLAUDE.md",
	"CONTRIBUTING.md",
	"README.md",
	"roster/RUNBOOK.md",
	"bin/init-cline-memory",
	"bin/bootstrap-cline-worktree",
	"bin/cadre-test",
	".github/workflows/validate.yml",
	".github/workflows/release.yml",
}

var (
	discoverInvocation = regexp.MustCompile(`unittest\s+discover`)
	discoverStartDir   = regexp.MustCompile(`-s\s+("[^"]+"|\S+)`)
	discoverPattern    = regexp.MustCompile(`-p\s+("[^"]+"|\S+)`)
	// `cd <dir> && ...` makes the start directory relative to somewhere else.
	discoverChdir = regexp.MustCompile(`cd\s+(\S+)\s*&&`)
)

func unquote(value string) string {
	return strings.Trim(value, `"'`)
}

func TestNoInstructionNamesATestSuiteThatWouldRunNothing(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	scanned, invocations := 0, 0
	var findings []string

	for _, relative := range suiteInstructionFiles {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Errorf("%s is listed here but cannot be read: %v", relative, err)
			continue
		}
		scanned++
		for number, line := range strings.Split(string(content), "\n") {
			if !discoverInvocation.MatchString(line) {
				continue
			}
			trimmed := strings.TrimSpace(line)
			// A comment explaining why a leg was removed legitimately names the
			// directory it used to run. So does a line marked (deleted).
			if strings.HasPrefix(trimmed, "#") || strings.Contains(line, "(deleted)") {
				continue
			}
			start := discoverStartDir.FindStringSubmatch(line)
			if start == nil {
				// A prose mention with no -s names no directory to check.
				continue
			}
			invocations++
			directory := unquote(start[1])
			if chdir := discoverChdir.FindStringSubmatch(line); chdir != nil {
				directory = filepath.Join(unquote(chdir[1]), directory)
			}
			pattern := "test*.py"
			if found := discoverPattern.FindStringSubmatch(line); found != nil {
				pattern = unquote(found[1])
			}
			where := relative + ":" + itoaTag(number+1)
			absolute := filepath.Join(root, filepath.FromSlash(directory))
			// Not load-bearing for detection -- Glob over a missing directory
			// returns no matches, so the check below catches this case too.
			// It is here for the message: "does not exist" and "holds no file
			// matching" send a reader to different places, and the first is
			// what a deleted suite looks like.
			if info, err := os.Stat(absolute); err != nil || !info.IsDir() {
				findings = append(findings, where+": "+directory+" does not exist")
				continue
			}
			matches, err := filepath.Glob(filepath.Join(absolute, pattern))
			if err != nil {
				t.Errorf("%s: %q is not a usable pattern: %v", where, pattern, err)
				continue
			}
			if len(matches) == 0 {
				findings = append(findings, where+": "+directory+
					" holds no file matching "+pattern)
			}
		}
	}

	if scanned != len(suiteInstructionFiles) {
		t.Fatalf("read %d of %d listed files", scanned, len(suiteInstructionFiles))
	}
	if invocations == 0 {
		t.Fatal("no discover invocation was found in any instruction file; either " +
			"the last one was removed and this guard should go too, or the scan is broken")
	}
	sort.Strings(findings)
	if len(findings) > 0 {
		t.Errorf("%d instruction(s) name a suite that would run nothing:\n  %s\n\n"+
			"`unittest discover` over an empty directory exits 0 on Python 3.10 "+
			"and 5 on 3.12+, so this is a false green for one reader and a "+
			"confusing failure for another. Point at what actually covers it now.",
			len(findings), strings.Join(findings, "\n  "))
	}
	t.Logf("checked %d discover invocation(s) across %d files", invocations, scanned)
}
