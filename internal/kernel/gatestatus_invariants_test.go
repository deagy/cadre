package kernel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Gatestatus: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of gatestatus_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

// TestOnlyGitLabNotesCarryASystemFlag records an asymmetry that is easy to get
// wrong in either direction.
//
// GitLab's notes endpoint returns both real comments and the forge's own
// activity notes, and one of those can quote a body containing our marker --
// matching it would make this try to edit a note it cannot edit. GitHub's
// issue-comment endpoint returns only real comments, so there is no flag to
// read and none is invented.
func TestOnlyGitLabNotesCarryASystemFlag(t *testing.T) {
	note := normaliseGitLabNote(map[string]any{
		"id": 1, "body": "quoted", "system": true,
		"author": map[string]any{"username": "sdlc-bot"},
	})
	if !note.IsSystem {
		t.Error("a GitLab system note was not marked as one")
	}
	ordinary := normaliseGitLabNote(map[string]any{
		"id": 2, "body": "real", "author": map[string]any{"username": "sdlc-bot"},
	})
	if ordinary.IsSystem {
		t.Error("an ordinary GitLab note was marked as a system note")
	}

	// GitHub has no such field, and a payload carrying one must not acquire
	// the meaning by accident.
	comment := normaliseGitHubComment(map[string]any{
		"id": 3, "body": "real", "system": true,
		"user": map[string]any{"login": "sdlc-bot"},
	})
	if comment.IsSystem {
		t.Error("a GitHub comment picked up a system flag GitHub does not send")
	}
	if comment.Author != "sdlc-bot" || comment.Body != "real" {
		t.Errorf("the comment was not normalised: %+v", comment)
	}
}

func TestTheStatusCommentSaysItIsNotApproval(t *testing.T) {
	// The advisory paragraph exists to be read by somebody about to make a
	// mistake, so its presence is a property of the artifact rather than a
	// stylistic choice. A render that dropped it would look fine.
	freezeClock(t)
	root, manifest := statusProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	projection, err := registry.GateStatusProjection(root, decideTask)
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	body := RenderGateStatusBody(decideTask, projection, contracts, frozenMoment)

	for _, required := range []string{
		"not an approval and is never read back",
		"does not approve any lifecycle gate",
		"Not approval evidence",
		"Reacting or replying to this comment does not approve anything",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("the rendered comment does not say %q:\n%s", required, body)
		}
	}
}

func TestTheStatusCommentRendersNoFreeText(t *testing.T) {
	// The property that makes the absent sanitizer correct rather than an
	// omission. Nothing project-supplied reaches this body -- so a re-entry's
	// actor and reason, which are a real identity and free text, must not
	// appear even though they sit right beside the fields that do.
	freezeClock(t)
	root, manifest := statusProject(t)
	mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
		func(document map[string]any) {
			document["re_entry_history"] = []any{map[string]any{
				"invalidated_at": "2026-08-14T09:00:00+00:00",
				"actor":          "github.com/a-real-person",
				"reason":         "a reason nobody outside this project should read",
				"earliest_gate":  "G4",
			}}
		})
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	projection, err := registry.GateStatusProjection(root, decideTask)
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	body := RenderGateStatusBody(decideTask, projection, contracts, frozenMoment)

	for _, leaked := range []string{"a-real-person", "nobody outside this project"} {
		if strings.Contains(body, leaked) {
			t.Errorf("the comment leaked %q:\n%s", leaked, body)
		}
	}
	// The count and the earliest gate do appear -- otherwise this test would
	// pass on a render that omitted the section entirely.
	if !strings.Contains(body, "Re-entries recorded: 1") {
		t.Errorf("the re-entry count is missing:\n%s", body)
	}
	if !strings.Contains(body, "earliest re-entered gate: G4") {
		t.Errorf("the earliest re-entered gate is missing:\n%s", body)
	}
}

func TestOnlyTheTimestampIsIgnoredWhenComparingBodies(t *testing.T) {
	// The timestamp changes on every invocation by design, so comparing it
	// would make every run rewrite a comment that already says the right
	// thing. Everything else must still count as a change.
	body := "line one\nCurrent phase: intent · rendered 2026-08-15T09:00:00Z\nline three\n"
	later := "line one\nCurrent phase: intent · rendered 2027-01-01T00:00:00Z\nline three\n"
	if canonicaliseForComparison(body) != canonicaliseForComparison(later) {
		t.Error("two bodies differing only in timestamp compared as different")
	}

	changed := "line one\nCurrent phase: build · rendered 2026-08-15T09:00:00Z\nline three\n"
	if canonicaliseForComparison(body) == canonicaliseForComparison(changed) {
		t.Error("a changed phase compared as unchanged")
	}
	elsewhere := "line one\nCurrent phase: intent · rendered 2026-08-15T09:00:00Z\nline four\n"
	if canonicaliseForComparison(body) == canonicaliseForComparison(elsewhere) {
		t.Error("a change after the timestamp line compared as unchanged")
	}
}

func TestTheStatusMarkerIsDomainSeparated(t *testing.T) {
	// The marker identifies this comment among everything else this kernel
	// puts on a forge. Sharing a formula with the issue markers would let one
	// task's gate issue and its status comment collide.
	marker := ComputeStatusMarker("TASK-1")
	if marker == TaskHash("TASK-1") {
		t.Error("the matching marker and the displayed hash are the same value")
	}
	if len(marker) != 16 {
		t.Errorf("the marker is %d characters", len(marker))
	}
	if marker != hexSHA256([]byte("gate-status\x00TASK-1"))[:16] {
		t.Error("the marker formula changed; every existing comment stops matching")
	}

	// Any template version matches, so a future v2 finds and updates a v1
	// comment rather than posting a second one beside it.
	pattern := markerPattern(marker)
	for _, version := range []string{"v1", "v2", "v37"} {
		if !pattern.MatchString("<!-- agentic-sdlc:gate-status:" + version + ":" + marker + " -->") {
			t.Errorf("a %s comment did not match", version)
		}
	}
	if pattern.MatchString("<!-- agentic-sdlc:gate-status:v1:" + TaskHash("TASK-2") + " -->") {
		t.Error("another task's marker matched")
	}
}

func readIfPresent(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// The invariants, stated without reference to the Python kernel.
