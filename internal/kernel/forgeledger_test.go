package kernel

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ledger write and the advisory lock.
//
// A ledger records what was published to a forge, which makes it the only
// record of something that happened outside this machine. If it loses an
// entry, the next run creates a second issue for a gate that already has one,
// and nothing detects the duplicate -- because the ledger is what detection
// reads. So these tests are about the write surviving, and about the lock
// never being broken by accident.

func TestALedgerIsWrittenWhereItIsAsked(t *testing.T) {
	root := t.TempDir()
	path, err := LedgerPath(root, Overlay, "TASK-1", "gate-issues-gitlab.json")
	if err != nil {
		t.Fatal(err)
	}
	ledger := map[string]any{
		"schema_version": 1,
		"task_id":        "TASK-1",
		"entries":        map[string]any{"G1": map[string]any{"iid": 7}},
	}
	if err := WriteLedgerFile(path, ledger, ".gate-issues."); err != nil {
		t.Fatalf("writing: %v", err)
	}

	// Sorted keys and a trailing newline, matching what the Python kernel
	// writes -- a ledger is rewritten in full each time, so a stable order is
	// what keeps its diff readable.
	content := readFile(t, path)
	if !strings.HasSuffix(content, "\n") {
		t.Error("the ledger has no trailing newline")
	}
	if index := strings.Index(content, `"entries"`); index == -1 ||
		index > strings.Index(content, `"schema_version"`) {
		t.Errorf("keys are not sorted:\n%s", content)
	}
	var round map[string]any
	if err := json.Unmarshal([]byte(content), &round); err != nil {
		t.Fatalf("the ledger is not valid JSON: %v", err)
	}
	if round["task_id"] != "TASK-1" {
		t.Errorf("the ledger lost its task: %v", round)
	}
}

func TestALedgerIsSortedEvenWhenItArrivesOrdered(t *testing.T) {
	// The case that makes the sort load-bearing. A publisher reads a ledger
	// back -- which preserves the order on disk -- adds an entry, and writes
	// it again. Rendering that in arrival order would reorder the file on
	// every publication and make its diff unreadable, which is the one thing
	// a full rewrite has to avoid.
	root := t.TempDir()
	path, err := LedgerPath(root, Overlay, "TASK-1", "gate-issues-gitlab.json")
	if err != nil {
		t.Fatal(err)
	}
	ledger := ordered(
		"task_id", "TASK-1",
		"schema_version", 1,
		"entries", ordered("G2", ordered("iid", 8), "G1", ordered("iid", 7)),
		"bot_username", "sdlc-bot",
	)
	if err := WriteLedgerFile(path, ledger, ".gate-issues."); err != nil {
		t.Fatal(err)
	}

	content := readFile(t, path)
	for _, pair := range [][2]string{
		{`"bot_username"`, `"entries"`},
		{`"entries"`, `"schema_version"`},
		{`"schema_version"`, `"task_id"`},
		{`"G1"`, `"G2"`}, // nested objects are sorted too
	} {
		first, second := strings.Index(content, pair[0]), strings.Index(content, pair[1])
		if first == -1 || second == -1 || first > second {
			t.Errorf("%s should precede %s:\n%s", pair[0], pair[1], content)
		}
	}
}

func TestWritingALedgerLeavesNoTemporaryFile(t *testing.T) {
	// The write goes through a temporary file in the same directory. One left
	// behind would be read by anything walking the run directory, and it holds
	// a complete-looking ledger.
	root := t.TempDir()
	path, err := LedgerPath(root, Overlay, "TASK-1", "gate-status-github.json")
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := WriteLedgerFile(path, map[string]any{"attempt": attempt}, ".gate-status."); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("expected one file, found %v", names)
	}
}

func TestALedgerReplacesItsPredecessorEntirely(t *testing.T) {
	// Full rewrite, not a merge. A ledger that kept an entry the new one
	// dropped would report an issue as published after it was deleted.
	root := t.TempDir()
	path, err := LedgerPath(root, Overlay, "TASK-1", "gate-issues-gitlab.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteLedgerFile(path,
		map[string]any{"entries": map[string]any{"G1": 1, "G2": 2}}, ".x."); err != nil {
		t.Fatal(err)
	}
	if err := WriteLedgerFile(path,
		map[string]any{"entries": map[string]any{"G1": 1}}, ".x."); err != nil {
		t.Fatal(err)
	}
	if content := readFile(t, path); strings.Contains(content, "G2") {
		t.Errorf("a dropped entry survived the rewrite:\n%s", content)
	}
}

func TestALockIsNeverBrokenWithoutBeingAsked(t *testing.T) {
	// The property that matters most here. A held lock means somebody's
	// publication was interrupted, and resuming it automatically would create
	// forge artifacts the interrupted run may already have created.
	root := t.TempDir()
	path, err := LedgerPath(root, Overlay, "TASK-1", "gate-issues-gitlab.lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := AcquireLockFile(path, false); err != nil {
		t.Fatalf("taking a free lock: %v", err)
	}

	err = AcquireLockFile(path, false)
	if err == nil {
		t.Fatal("a held lock was taken again")
	}
	var held *LedgerLockHeld
	if !errors.As(err, &held) {
		t.Fatalf("the refusal is not a LedgerLockHeld: %v", err)
	}
	// The refusal names who holds it, so the human it sends to the lock file
	// can find the run that left it.
	if !strings.Contains(held.Holder, "pid") || !strings.Contains(held.Holder, "started_at") {
		t.Errorf("the holder record says too little: %q", held.Holder)
	}
	if !strings.Contains(err.Error(), "--break-lock") {
		t.Errorf("the refusal does not say how to override it: %v", err)
	}

	// Only an explicit break takes it.
	if err := AcquireLockFile(path, true); err != nil {
		t.Errorf("--break-lock did not take the lock: %v", err)
	}
	if err := ReleaseLockFile(path); err != nil {
		t.Errorf("releasing: %v", err)
	}
	// Releasing twice is fine: a run whose lock was broken under it should
	// still exit cleanly rather than reporting a second failure on the way.
	if err := ReleaseLockFile(path); err != nil {
		t.Errorf("releasing an absent lock reported %v", err)
	}
	if err := AcquireLockFile(path, false); err != nil {
		t.Errorf("the lock was not actually released: %v", err)
	}
}

func TestTheLedgerBytesAreAContractRatherThanAnImplementationDetail(t *testing.T) {
	// A ledger is a document other tooling reads: an operator greps it, a
	// script parses it, and both break on a formatting change nobody thought
	// was a change. So the bytes are pinned, not the parsed value.
	//
	// This compared against `python3 -c json.dumps(..., indent=2,
	// sort_keys=True)` until the Python kernel was deleted -- the reason given
	// was that Python read these files back for as long as both existed, and
	// that reason went with it. The format did not. Pinned to a literal here,
	// which is the same gate without a second interpreter: sorted keys, two
	// spaces, \uXXXX for anything non-ASCII, `<` and `&` left alone, and a
	// trailing newline.
	//
	// The em-dash, accent and emoji are deliberate: Go's encoding/json emits
	// raw UTF-8 and escapes `<`, `>` and `&`, which is the opposite of this
	// format on both counts. A ledger written by the wrong encoder is readable
	// and wrong.
	ledger := map[string]any{
		"schema_version": 1,
		"task_id":        "TASK-1",
		"project_path":   "acme/app",
		"bot_username":   "sdlc-bot",
		"mocked":         false,
		"entries": map[string]any{
			"G1": map[string]any{
				"iid": 7, "title": "G1 Intent \u2014 caf\u00e9 \u2615",
				"url": "https://gitlab.example/acme/app/-/issues/7",
			},
			"G2": map[string]any{"iid": 8, "title": "G2 <Requirements> & scope"},
		},
	}
	const golden = `{
  "bot_username": "sdlc-bot",
  "entries": {
    "G1": {
      "iid": 7,
      "title": "G1 Intent \u2014 caf\u00e9 \u2615",
      "url": "https://gitlab.example/acme/app/-/issues/7"
    },
    "G2": {
      "iid": 8,
      "title": "G2 <Requirements> & scope"
    }
  },
  "mocked": false,
  "project_path": "acme/app",
  "schema_version": 1,
  "task_id": "TASK-1"
}
`

	root := t.TempDir()
	path, err := LedgerPath(root, Overlay, "TASK-1", "gate-issues-gitlab.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteLedgerFile(path, ledger, ".gate-issues."); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != golden {
		t.Errorf("the ledger bytes changed.\nwant:\n%s\ngot:\n%s", golden, got)
	}
}
