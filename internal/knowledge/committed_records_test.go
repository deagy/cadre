package knowledge

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The staged records committed to this repository are well-formed.
//
// A pre-commit hook used to check this, by running
// roster/knowledge-store/src/staged_records.py. That file was deleted with the
// rest of the Python staged-records subsystem; the hook was not. It has been
// invoking a path that does not exist, which fails for anyone who has
// pre-commit installed and does nothing at all for anyone who does not.
//
// Either way the records stopped being checked, and nothing said so. The
// remaining tests in this package all build their records in memory, so the 23
// under proposed-knowledge/ were read by nothing.
//
// A test is the better home for this than a hook: it runs in CI for everyone,
// rather than only for contributors who installed the hook.

func stagedRecordsDirectory(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(working))
	directory := filepath.Join(root, "roster", "knowledge-store", "proposed-knowledge")
	if _, err := os.Stat(directory); err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	return directory
}

func TestEveryCommittedStagedRecordIsWellFormed(t *testing.T) {
	directory := stagedRecordsDirectory(t)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("reading %s: %v", directory, err)
	}

	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		// The schema and the .history.json sidecars are not records, and
		// neither is the directory's own README -- which the first version of
		// this test read as one and reported as malformed. Excluded by name
		// rather than by pattern-matching the KS- prefix, so a record that
		// does not follow the naming convention is still checked rather than
		// silently skipped.
		if entry.IsDir() || !strings.HasSuffix(name, ".md") || name == "README.md" {
			continue
		}
		checked++
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil {
				t.Fatal(err)
			}
			findings := ValidateStagedRecordText(string(raw))
			if len(findings) > 0 {
				sort.Strings(findings)
				t.Errorf("%d finding(s):\n  %s", len(findings), strings.Join(findings, "\n  "))
			}
		})
	}

	// Self-vacuity. An empty directory, or a filter that matched nothing,
	// passes this test while validating no record at all -- which is exactly
	// the state the deleted hook left behind.
	if checked < 20 {
		t.Fatalf("read only %d staged records; the directory holds many more, so "+
			"the filter is dropping them", checked)
	}
	t.Logf("validated %d committed staged record(s)", checked)
}

func TestTheValidatorRejectsAMalformedRecord(t *testing.T) {
	// Guards the guard. The check above passes over well-formed records, which
	// is also what it would do if ValidateStagedRecordText returned nothing for
	// everything.
	for _, testCase := range []struct {
		name string
		text string
	}{
		{"no frontmatter at all", "just a body, no delimiters\n"},
		{"an unterminated frontmatter block", "---\nid: KS-1\nsummary: x\n"},
		{"frontmatter that is not key: value", "---\nnot yaml at all\n---\n\nbody\n"},
		{"an empty document", ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if findings := ValidateStagedRecordText(testCase.text); len(findings) == 0 {
				t.Errorf("accepted a malformed record, so the check above proves nothing")
			}
		})
	}
}
