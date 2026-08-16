package kernel

import (
	"bytes"
	"encoding/json"
	"testing"
)

// Ledgers: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of ledgers_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

func TestAMissingLedgerIsAnEmptyLedgerNotAnError(t *testing.T) {
	// Stated directly rather than by comparison, because it is the one
	// behaviour a caller depends on and the one most likely to be "fixed"
	// into an error by somebody reading the code later.
	root, _ := plannedProject(t)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"list-gate-issues", "--root", root, "--task-id", fixtureTask},
		&stdout, &stderr); code != 0 {
		t.Fatalf("a task with no ledger exited %d: %s", code, stderr.String())
	}
	var ledger map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &ledger); err != nil {
		t.Fatalf("the output was not JSON: %v", err)
	}
	if ledger["task_id"] != fixtureTask {
		t.Errorf("the empty ledger does not name its task: %v", ledger)
	}
	entries, ok := ledger["entries"].(map[string]any)
	if !ok || len(entries) != 0 {
		t.Errorf("entries should be an empty object, got %#v", ledger["entries"])
	}
}

func TestATaskIDCannotEscapeTheProject(t *testing.T) {
	// A task id is a directory name. Without this, `--task-id ../../..` reads
	// a file outside the project entirely -- and these readers print whatever
	// they read.
	root, _ := plannedProject(t)
	for _, hostile := range []string{
		"../../etc", "..", ".", "a/b", "with space", "-leading-dash", "",
		"trailing/", "nul\x00byte",
	} {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"list-gate-issues", "--root", root, "--task-id", hostile},
			&stdout, &stderr)
		if code == 0 {
			t.Errorf("--task-id %q was accepted, printing: %s", hostile, stdout.String())
		}
	}

	// And an ordinary id still works, or the check above would pass by
	// refusing everything.
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"list-gate-issues", "--root", root, "--task-id", fixtureTask},
		&stdout, &stderr); code != 0 {
		t.Errorf("an ordinary task id was refused: %s", stderr.String())
	}
}

func TestRenderIndentedMatchesPythonsJSONDumps(t *testing.T) {
	// The renderer on its own, over the shapes that differ between the two
	// languages' encoders. Each expectation below was taken from
	// `json.dumps(value, indent=2)`.
	for _, probe := range []struct {
		name     string
		document string
		expected string
	}{
		{"key order is the document's", `{"zebra": 1, "alpha": 2}`,
			"{\n  \"zebra\": 1,\n  \"alpha\": 2\n}\n"},
		{"empty containers collapse", `{"a": {}, "b": []}`,
			"{\n  \"a\": {},\n  \"b\": []\n}\n"},
		{"non-ASCII is escaped", `{"s": "café"}`,
			"{\n  \"s\": \"caf\\u00e9\"\n}\n"},
		{"astral runes become surrogate pairs", `{"s": "🚀"}`,
			"{\n  \"s\": \"\\ud83d\\ude80\"\n}\n"},
		{"HTML punctuation is not escaped", `{"s": "a<b>c&d"}`,
			"{\n  \"s\": \"a<b>c&d\"\n}\n"},
		{"integers stay integral", `{"n": 1, "big": 10000000000000000000}`,
			"{\n  \"n\": 1,\n  \"big\": 10000000000000000000\n}\n"},
		{"nesting indents by two", `{"a": [{"b": 1}]}`,
			"{\n  \"a\": [\n    {\n      \"b\": 1\n    }\n  ]\n}\n"},
		{"null and booleans", `{"a": null, "b": true, "c": false}`,
			"{\n  \"a\": null,\n  \"b\": true,\n  \"c\": false\n}\n"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			value, err := DecodeOrdered([]byte(probe.document))
			if err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if rendered := RenderIndented(value); rendered != probe.expected {
				t.Errorf("rendered %q, want %q", rendered, probe.expected)
			}
		})
	}
}
