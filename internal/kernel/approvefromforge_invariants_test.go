package kernel

import (
	"path/filepath"
	"strings"
	"testing"
)

// Approvefromforge: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of approvefromforge_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

func TestASourceLinkNeverApprovesAGate(t *testing.T) {
	// The whole distinction between these commands and the adapters above.
	// Linking an issue records where a gate's contribution came from; it is
	// not a human saying the contribution is acceptable, and no amount of
	// linking should ever produce an approved gate.
	freezeClock(t)
	root, manifest := githubBoundProject(t)

	before := readFile(t, filepath.Join(root, Overlay, "runs", approveTask, "run-record.json"))
	for _, number := range []int{7, 8, 9} {
		if _, err := linkIssue(t, root, manifest, "G1", "product_owner",
			&SourceIssue{Number: number, Title: "Intent", State: "opened"}); err != nil {
			t.Fatalf("linking issue %d: %v", number, err)
		}
	}
	after := readFile(t, filepath.Join(root, Overlay, "runs", approveTask, "run-record.json"))
	if before == after {
		t.Fatal("linking changed nothing at all; this test would prove nothing")
	}

	record, err := loadJSONObject(filepath.Join(root, Overlay, "runs", approveTask,
		"run-record.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range listOf(record["lifecycle_gates"]) {
		gate, _ := raw.(map[string]any)
		if gate["status"] == "approved" {
			t.Errorf("%v is approved after three source links", gate["gate_id"])
		}
		if len(listOf(gate["human_approvals"])) > 0 {
			t.Errorf("%v gained a human approval from a source link: %v",
				gate["gate_id"], gate["human_approvals"])
		}
	}
}

func TestRelinkingReplacesRatherThanAccumulates(t *testing.T) {
	// The record field holds one URI at a time. If the evidence refs
	// accumulated, the record would name one issue while carrying evidence
	// for three, and an auditor reading the refs would find two that are no
	// longer the source of anything.
	freezeClock(t)
	root, manifest := githubBoundProject(t)

	for _, number := range []int{7, 8, 9} {
		if _, err := linkIssue(t, root, manifest, "G1", "product_owner",
			&SourceIssue{Number: number, Title: "Intent", State: "opened"}); err != nil {
			t.Fatal(err)
		}
	}

	record, err := loadJSONObject(filepath.Join(root, Overlay, "runs", approveTask,
		"run-record.json"))
	if err != nil {
		t.Fatal(err)
	}
	if record["intent_record_id"] != "gitlab-issue:acme/app:issues/9" {
		t.Errorf("the record names %v, not the issue linked last", record["intent_record_id"])
	}
	gate, _ := listOf(record["lifecycle_gates"])[0].(map[string]any)
	var sourceRefs []string
	for _, raw := range listOf(gate["evidence_refs"]) {
		ref, _ := raw.(map[string]any)
		id := toStringOrEmpty(ref["evidence_id"])
		if strings.HasPrefix(id, "g1-source-gitlab-issue-") {
			sourceRefs = append(sourceRefs, id)
		}
	}
	if len(sourceRefs) != 1 {
		t.Errorf("three links left %d source evidence refs: %v", len(sourceRefs), sourceRefs)
	}
	if len(sourceRefs) == 1 && sourceRefs[0] != "g1-source-gitlab-issue-9" {
		t.Errorf("the surviving ref is %s, not the issue linked last", sourceRefs[0])
	}
	// The unrelated evidence the gate already carried must still be there --
	// dropping by prefix must not drop everything.
	if len(listOf(gate["evidence_refs"])) < 2 {
		t.Errorf("linking removed the gate's other evidence: %v", gate["evidence_refs"])
	}
}

func TestOnlyG1AndG2AcceptASourceLink(t *testing.T) {
	// Unreachable from the CLI, where each subcommand fixes its own gate. It
	// stays enforced in the recorder so a future caller cannot attach an
	// intent record to a gate that has no field to hold it.
	freezeClock(t)
	root, manifest := githubBoundProject(t)
	for _, gateID := range []string{"G3", "G5", "G10"} {
		_, err := linkIssue(t, root, manifest, gateID, "product_owner",
			&SourceIssue{Number: 7, Title: "Intent", State: "opened"})
		if err == nil {
			t.Errorf("%s accepted a source link", gateID)
			continue
		}
		if !strings.Contains(err.Error(), "does not accept a GitLab issue source link") {
			t.Errorf("%s was refused for the wrong reason: %v", gateID, err)
		}
	}
}

func TestAGitLabApprovalCarriesNoPersonalData(t *testing.T) {
	// The data-minimization rule, checked at the boundary where it could be
	// broken: the normalizer is the only thing that reads GitLab's response,
	// so a field it does not extract cannot reach an evidence record.
	records, err := GitLabApprovalRecordsFromAPIResponse(map[string]any{
		"sha": "abc123", "updated_at": "2026-08-15T09:00:00Z",
		"approved_by": []any{map[string]any{"user": map[string]any{
			"id": 77, "username": "product-owner",
			"name":       "A Real Person",
			"email":      "person@example.invalid",
			"avatar_url": "https://example.invalid/a.png",
			"web_url":    "https://gitlab.com/product-owner",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one record, got %d", len(records))
	}
	record, _ := records[0].(map[string]any)
	for _, forbidden := range []string{"name", "email", "avatar_url", "web_url"} {
		if _, present := record[forbidden]; present {
			t.Errorf("the approval record carries %s: %v", forbidden, record)
		}
	}
	// And it does carry the five fields evidence needs, or the check above
	// passes by extracting nothing.
	for _, required := range []string{
		"approval_id", "username", "state", "decided_at", "commit_sha"} {
		if _, present := record[required]; !present {
			t.Errorf("the approval record is missing %s: %v", required, record)
		}
	}
	if record["username"] != "product-owner" {
		t.Errorf("the pseudonymous username was not kept: %v", record)
	}
}

func TestTheLatestReviewDecidesEvenWhenAnEarlierOneApproved(t *testing.T) {
	// Stated directly, because it is the one selection rule that could
	// plausibly have gone the other way -- and the other way would let a
	// withdrawn approval be recorded as evidence.
	reviews := []any{
		review(1, "alice", "APPROVED", "2026-08-14T09:00:00Z", "abc"),
		review(2, "alice", "CHANGES_REQUESTED", "2026-08-15T09:00:00Z", "abc"),
	}
	if _, err := SelectGitHubReview(reviews, "alice", ""); err == nil {
		t.Error("an approval that was superseded by a request for changes was selected")
	}

	// And the reverse: changes requested, then approved, is an approval.
	reversed := []any{
		review(1, "alice", "CHANGES_REQUESTED", "2026-08-14T09:00:00Z", "abc"),
		review(2, "alice", "APPROVED", "2026-08-15T09:00:00Z", "abc"),
	}
	selected, err := SelectGitHubReview(reversed, "alice", "")
	if err != nil {
		t.Fatalf("an approval that superseded a request for changes was refused: %v", err)
	}
	if id, _ := jsonInteger(selected["id"]); id != 2 {
		t.Errorf("selected review %v, not the later one", selected["id"])
	}
}

func TestAReviewWithNoUsableTimestampIsNeverSelected(t *testing.T) {
	// A review with no valid submitted_at cannot be ordered against any other,
	// so selecting it would make "the latest" meaningless.
	reviews := []any{
		review(1, "alice", "APPROVED", "not a date", "abc"),
	}
	if _, err := SelectGitHubReview(reviews, "alice", ""); err == nil {
		t.Error("a review with an unparseable timestamp was selected")
	}
	approvals := []any{
		map[string]any{"approval_id": "1", "username": "alice",
			"state": "approved", "decided_at": nil, "commit_sha": "abc"},
	}
	if _, err := SelectGitLabApproval(approvals, "alice", ""); err == nil {
		t.Error("an approval with no decision time was selected")
	}
}

func TestReviewerMatchingIgnoresCaseOnBothSides(t *testing.T) {
	// GitHub logins and GitLab usernames are case-insensitive, so a project
	// that wrote "Product-Owner" into its authority map must still match a
	// review by "product-owner". Refusing would block a legitimate approval.
	reviews := []any{review(1, "Product-Owner", "APPROVED", "2026-08-15T09:00:00Z", "abc")}
	if _, err := SelectGitHubReview(reviews, "product-owner", ""); err != nil {
		t.Errorf("a differently-cased login did not match: %v", err)
	}
	// And a genuinely different login still does not match.
	if _, err := SelectGitHubReview(reviews, "somebody-else", ""); err == nil {
		t.Error("a different login matched")
	}
}

const approveTask = decideTask

func linkIssue(
	t *testing.T, root, manifest, gateID, role string, issue *SourceIssue,
) (*orderedObject, error) {
	t.Helper()
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	return registry.RecordSourceIssueLink(SourceLinkRequest{
		Root: root, TaskID: approveTask, GateID: gateID, AuthorityRole: role,
		ProjectPath: "acme/app", IssueNumber: issue.Number,
	}, issue)
}
