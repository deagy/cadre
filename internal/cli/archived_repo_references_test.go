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

// Docs must not tell anyone to *use* a repository the monorepo merge archived.
//
// Four were merged in and archived: deagy/cadre-lifecycle (-> plugin/),
// deagy/agentic-sdlc (-> kernel/, engine/), deagy/cadre-plugin, and
// deagy/cadre-profile-secure-cloud. An archived repository stays cloneable and
// its URLs keep resolving, so a stale instruction still works -- it just
// silently serves content that will never update again. Nothing fails, and the
// reader has no way to tell.
//
// That is how these survived the merge. They were found by a documentation
// review months later: the README, the RUNBOOK, AGENTS.md, both Cline plugin
// READMEs, a skill, a generator's "--output is required" error message, and
// kernel/pyproject.toml's Repository field -- which broke SECURITY.md's own
// `pip show` provenance check, since it told readers to verify a homepage the
// package did not declare. A later sweep still missed one in plugin/
// CHANGELOG.md's header. Five rounds, five misses.
//
// Ported from plugin/tools/test_archived_repo_references.py.

const archivedRepositories = `(?:cadre-lifecycle|agentic-sdlc|cadre-plugin|cadre-profile-secure-cloud)`

// forbiddenArchivedReference matches only *actionable* references -- a command
// to run, a path to write to, an install spec, or a page to open.
//
// The archived names appear around 150 times here and most are correct:
// provenance records, changelog entries describing the arrangement at the
// time, migration docs, design proposals. A guard that banned the names would
// be unusable and would be deleted, so prose that merely names an archived
// repository is fine and stays fine.
var forbiddenArchivedReference = []struct {
	pattern     *regexp.Regexp
	consequence string
}{
	{regexp.MustCompile(`git\s+clone\s+\S*github\.com/deagy/` + archivedRepositories),
		"clone an archived repository"},
	{regexp.MustCompile(`(?:/plugin|codex\s+plugin|cline\s+plugin)[^\n]*\bdeagy/` + archivedRepositories + `\b`),
		"install from an archived marketplace"},
	{regexp.MustCompile(`pipx?\s+install[^\n]*deagy/` + archivedRepositories),
		"pip/pipx install from an archived repository"},
	{regexp.MustCompile(`--output\s+\S*` + archivedRepositories),
		"generate output into a checkout of an archived repository"},
	{regexp.MustCompile(`github\.com/deagy/` + archivedRepositories + `/(?:releases|tree|blob|issues|security)`),
		"browse a page in an archived repository"},
	{regexp.MustCompile(`(?m)^\s*Repository\s*=\s*"[^"]*deagy/` + archivedRepositories + `"`),
		"declare package metadata pointing at an archived repository"},
}

// historicalRecords are files whose whole purpose is recording the pre-merge
// arrangement. Rewriting one to describe the present would falsify it.
//
// Kept short and specific. A directory is not an acceptable entry: it would
// exempt future files nobody reviewed.
var historicalRecords = map[string]string{
	"CHANGELOG.md": "quotes install commands as they were at the time of each entry; " +
		"rewriting a released version's notes to name a repository that did not " +
		"host it yet would falsify the record",
	"plugin/CHANGELOG.md": "the same, for the packaged distribution",
}

// archivedReferenceOptOut is a line-level escape for a historical reference
// inside an otherwise-live file -- quoting the old marketplace string as the
// example of the bug a test exists to prevent, say. It goes on the offending
// line or the one above it.
//
// Line-level, not file-level: these files are actively edited, and exempting
// the whole file would silently cover a future instruction added to it.
var archivedReferenceOptOut = regexp.MustCompile(`archived-ref-ok:\s*(\S.*)`)

var scannedForArchivedRefs = map[string]bool{
	".md": true, ".py": true, ".toml": true, ".json": true, ".yml": true,
	".yaml": true, ".ts": true, ".sh": true, ".ps1": true,
}

func trackedFilesForArchivedScan(t *testing.T) (root string, paths []string) {
	t.Helper()
	root = filepath.Dir(filepath.Dir(mustGetwd(t)))
	command := exec.Command("git", "ls-files")
	command.Dir = root
	listing, err := command.Output()
	if err != nil {
		t.Skipf("cannot list tracked files: %v", err)
	}
	for _, line := range strings.Split(string(listing), "\n") {
		relative := strings.TrimSpace(line)
		if relative == "" || !scannedForArchivedRefs[filepath.Ext(relative)] {
			continue
		}
		paths = append(paths, relative)
	}
	return root, paths
}

// exemptAsHistoricalRecord also covers the generated copy of an exempt file:
// plugin/suite/ mirrors docs wholesale, and re-listing each one would be noise
// that drifts the moment a doc is renamed.
func exemptAsHistoricalRecord(relative string) bool {
	if _, exempt := historicalRecords[relative]; exempt {
		return true
	}
	mirrored := strings.TrimPrefix(relative, "plugin/suite/")
	if mirrored == relative {
		return false
	}
	_, exempt := historicalRecords[mirrored]
	return exempt
}

func TestNoActionableReferenceToAnArchivedRepository(t *testing.T) {
	root, paths := trackedFilesForArchivedScan(t)
	scanned := 0
	var findings []string
	for _, relative := range paths {
		if exemptAsHistoricalRecord(relative) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			continue
		}
		scanned++
		text := string(content)
		lines := strings.Split(text, "\n")
		for _, forbidden := range forbiddenArchivedReference {
			for _, location := range forbidden.pattern.FindAllStringIndex(text, -1) {
				number := strings.Count(text[:location[0]], "\n") + 1
				// The marker may sit on the offending line or the one above it,
				// so a long line can carry its justification separately.
				start := number - 2
				if start < 0 {
					start = 0
				}
				excused := false
				for _, candidate := range lines[start:number] {
					if archivedReferenceOptOut.MatchString(candidate) {
						excused = true
					}
				}
				if excused {
					continue
				}
				match := strings.TrimSpace(text[location[0]:location[1]])
				if len(match) > 110 {
					match = match[:110]
				}
				findings = append(findings, relative+":"+itoaTag(number)+
					": tells a reader to "+forbidden.consequence+"\n    "+match)
			}
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned %d files; the listing is broken, not the tree", scanned)
	}
	sort.Strings(findings)
	if len(findings) > 0 {
		t.Errorf("%d instruction(s) point at an archived repository:\n  %s\n\n"+
			"These still 'work' -- an archived repo stays cloneable -- so they "+
			"fail silently by serving frozen content. Point at deagy/cadre and "+
			"the in-tree plugin/, kernel/ or engine/ instead. If the reference "+
			"is a deliberate historical record, mark the line archived-ref-ok "+
			"with the reason.", len(findings), strings.Join(findings, "\n  "))
	}
	t.Logf("scanned %d tracked files for actionable archived references", scanned)
}

func TestEveryHistoricalRecordExemptionIsStillEarned(t *testing.T) {
	// An exemption for a file that no longer trips any pattern is a standing
	// hole: it would silently exempt that file's future content too.
	root, _ := trackedFilesForArchivedScan(t)
	for relative, reason := range historicalRecords {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s is exempt without a stated reason", relative)
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Errorf("exempt file does not exist: %s (%v)", relative, err)
			continue
		}
		matched := false
		for _, forbidden := range forbiddenArchivedReference {
			if forbidden.pattern.Match(content) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("%s is exempt but no longer contains any forbidden pattern. "+
				"Remove it from historicalRecords so the file is checked again.", relative)
		}
	}
}
