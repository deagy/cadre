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

// A release tag written down anywhere but a historical record goes stale.
//
// The same class as the pinned marketplace ref in docs_versions_test.go, one
// level more general: any `plugin-vX.Y.Z` or `kernel-vX.Y.Z` outside the
// documents whose job is to record what shipped. Instructions naming a tag rot
// the moment the next release lands, and nothing about them looks wrong.
//
// This guard is not hypothetical. It caught a violation earlier in this
// migration -- a Go test comment naming two real tags as examples -- and the
// Python it is ported from carries its own warning about that:
//
//	Case-insensitive: a sentence-initial, capitalised form of the tag is
//	ordinary prose and would otherwise walk straight past this check.
//	(Deliberately not spelled out here -- this file is scanned too, and a
//	literal example would trip its own guard.)
//
// The same applies here. The pattern is assembled rather than written out.
//
// Ported from test_repository_health.py's
// test_hardcoded_release_tags_are_confined_to_historical_records, the last
// live guard in that file with no Go counterpart.

// releaseTagAllowlist maps a tracked path prefix to why a literal tag belongs
// there. Each is a record of something that happened, and rewriting a record
// to satisfy a scan is worse than the scan not running.
var releaseTagAllowlist = map[string]string{
	"CHANGELOG.md": "a chronological record of exactly what shipped at each tag; " +
		"it must keep citing old tags verbatim",
	"SECURITY.md": "documents a past keyless-tag-signing incident tied to the exact " +
		"historical tag that carries it; the postmortem is only useful if that tag stays named",
	"docs/migration/monorepo-migration.md": "restates that incident plus a completed " +
		"migration's release history -- both historical facts",
	"docs/proposals/":               "proposals argue from what shipped when",
	".github/workflows/release.yml": "the workflow that creates the tags",
}

// releaseTagPatternFor builds the scan pattern without spelling a tag out, so
// this file does not trip the check it implements.
func releaseTagPatternFor() *regexp.Regexp {
	return regexp.MustCompile(`(?i)(?:kernel|plugin)-v\d+\.\d+\.\d+`)
}

func TestHardcodedReleaseTagsAreConfinedToHistoricalRecords(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	command := exec.Command("git", "ls-files")
	command.Dir = root
	listing, err := command.Output()
	if err != nil {
		t.Skipf("cannot list tracked files: %v", err)
	}
	pattern := releaseTagPatternFor()

	scanned := 0
	var offenders []string
	for _, line := range strings.Split(string(listing), "\n") {
		relative := strings.TrimSpace(line)
		if relative == "" {
			continue
		}
		// plugin/ is generated output; its sources are scanned instead, and a
		// finding there would be reported twice.
		if strings.HasPrefix(relative, "plugin/") || allowedToNameATag(relative) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			continue // a directory, a symlink, or something unreadable
		}
		if !isProbablyText(content) {
			continue
		}
		scanned++
		for number, text := range strings.Split(string(content), "\n") {
			if match := pattern.FindString(text); match != "" {
				offenders = append(offenders,
					relative+":"+itoaTag(number+1)+": "+match)
			}
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned %d files; the listing is broken, not the tree", scanned)
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("%d hardcoded release tag(s) outside the historical records:\n  %s\n\n"+
			"A tag written into an instruction is stale the moment the next "+
			"release lands, and nothing about it looks wrong. If the file is a "+
			"record of what shipped, add it to releaseTagAllowlist with the "+
			"reason.", len(offenders), strings.Join(offenders, "\n  "))
	}
	t.Logf("scanned %d tracked files", scanned)
}

func allowedToNameATag(relative string) bool {
	for prefix := range releaseTagAllowlist {
		if relative == prefix || strings.HasPrefix(relative, prefix) {
			return true
		}
	}
	return false
}

// isProbablyText keeps the scan off binaries, which cannot carry an
// instruction and whose bytes occasionally match anything.
func isProbablyText(content []byte) bool {
	limit := len(content)
	if limit > 8000 {
		limit = 8000
	}
	for _, b := range content[:limit] {
		if b == 0 {
			return false
		}
	}
	return true
}

func itoaTag(n int) string {
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

func TestTheReleaseTagScanIsCaseInsensitiveAndBoundedByTheAllowlist(t *testing.T) {
	// Guards the guard, and pins the two decisions that make it useful.
	//
	// Case-insensitivity: a sentence-initial capitalised form is ordinary
	// prose and would otherwise walk straight past. Built from parts so this
	// file does not trip its own check.
	pattern := releaseTagPatternFor()
	prefix := "plugin" + "-v"
	for _, sample := range []string{
		prefix + "1.2.3",
		strings.ToUpper(prefix[:1]) + prefix[1:] + "1.2.3",
		"kernel" + "-v" + "0.13.2",
		"see " + prefix + "9.9.9 for details",
	} {
		if !pattern.MatchString(sample) {
			t.Errorf("the scan would not notice: %s", sample)
		}
	}
	for _, ignored := range []string{
		"plugin version 1.2.3",
		"v1.2.3",
		"the plugin-v directory",
		"cadre-lifecycle@v1.2.3",
	} {
		if pattern.MatchString(ignored) {
			t.Errorf("the scan wrongly matched: %s", ignored)
		}
	}

	// The allowlist is prefix-based, so a directory entry covers its contents
	// and a file entry does not cover a same-named sibling.
	if !allowedToNameATag("docs/proposals/anything.md") {
		t.Error("a directory prefix does not cover its contents")
	}
	if !allowedToNameATag("CHANGELOG.md") {
		t.Error("an exact file entry does not match itself")
	}
	if allowedToNameATag("docs/adopt-cadre-quickstart.md") {
		t.Error("an unrelated document is treated as allowlisted")
	}
	if len(releaseTagAllowlist) == 0 {
		t.Fatal("the allowlist is empty; every record file would fail")
	}
	for path, reason := range releaseTagAllowlist {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s is allowlisted without a reason", path)
		}
	}
}
