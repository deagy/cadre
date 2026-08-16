package kernel

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The approval recorders, asserted on what they write.
//
// These are the only paths in this kernel that turn something that happened on
// a forge into a recorded human approval, so the thing worth checking is not
// that they agree with another implementation but what ends up in the run
// record: who is named as the approver, what evidence is cited, and whether
// the gate moved.
//
// The invariant underneath all of it: **an approval is bound to the identity
// the project assigned, not to whoever happened to press the button.** A
// reviewer who is not that identity cannot have their review recorded as that
// authority's approval, and one approval is never a gate.

// approvalRecord reads back what a run record now says about a gate.
func approvalRecord(t *testing.T, root, gateID string) map[string]any {
	t.Helper()
	record, err := loadJSONObject(filepath.Join(root, Overlay, "runs", decideTask,
		"run-record.json"))
	if err != nil {
		t.Fatalf("reading the run record: %v", err)
	}
	for _, raw := range listOf(record["lifecycle_gates"]) {
		gate, _ := raw.(map[string]any)
		if gate["gate_id"] == gateID {
			return gate
		}
	}
	t.Fatalf("no gate %s in the run record", gateID)
	return nil
}

func TestAGitHubReviewIsRecordedAgainstTheAssignedAuthority(t *testing.T) {
	freezeClock(t)
	root, manifest := githubBoundProject(t)

	code, output := runCLI(t, "--provider", manifest, "approve-from-github",
		"--root", root, "--task-id", decideTask, "--gate", "G1",
		"--role", "product_owner", "--repo", "acme/app", "--pr", "1",
		"--review-id", "5001", "--reviewer-login", "product-owner",
		"--commit-sha", "abc123", "--decided-at", "2026-08-15T09:00:00+00:00")
	if code != 0 {
		t.Fatalf("recording failed: exit %d\n%s", code, output)
	}

	gate := approvalRecord(t, root, "G1")
	approvals := listOf(gate["human_approvals"])
	if len(approvals) != 1 {
		t.Fatalf("expected one approval, found %d: %v", len(approvals), approvals)
	}
	approval, _ := approvals[0].(map[string]any)
	approver, _ := approval["approver"].(map[string]any)

	// The assignee from the project's authority map -- not the reviewer login,
	// which is a forge handle and not an identity this kernel governs.
	if approver["id"] != "github.com/product-owner" {
		t.Errorf("the approver is %v, not the assigned authority", approver["id"])
	}
	if approver["kind"] != "human" {
		t.Errorf("the approval is recorded as %v, not human", approver["kind"])
	}

	refs := listOf(approval["evidence_refs"])
	if len(refs) != 1 {
		t.Fatalf("expected one evidence ref, found %d", len(refs))
	}
	ref, _ := refs[0].(map[string]any)
	uri := toStringOrEmpty(ref["uri"])
	if _, wellFormed := GitHubReviewLogin(uri); !wellFormed {
		t.Errorf("the evidence URI cannot be decomposed: %q", uri)
	}
	// The hash binds the approval to its own content. An evidence ref with a
	// URI and no hash is a pointer somebody could repoint.
	if len(toStringOrEmpty(ref["hash"])) != 64 {
		t.Errorf("the evidence ref carries no usable hash: %v", ref)
	}
}

func TestAGitLabApprovalIsRecordedTheSameWay(t *testing.T) {
	freezeClock(t)
	root, manifest := gitlabBoundProject(t)

	code, output := runCLI(t, "--provider", manifest, "approve-from-gitlab",
		"--root", root, "--task-id", decideTask, "--gate", "G1",
		"--role", "product_owner", "--project-path", "acme/app", "--mr-iid", "7",
		"--approval-id", "42", "--approver-username", "product-owner",
		"--commit-sha", "abc123", "--decided-at", "2026-08-15T09:00:00+00:00")
	if code != 0 {
		t.Fatalf("recording failed: exit %d\n%s", code, output)
	}

	gate := approvalRecord(t, root, "G1")
	approval, _ := listOf(gate["human_approvals"])[0].(map[string]any)
	refs := listOf(approval["evidence_refs"])
	ref, _ := refs[0].(map[string]any)
	uri := toStringOrEmpty(ref["uri"])
	if _, wellFormed := GitLabMRUsername(uri); !wellFormed {
		t.Errorf("the evidence URI cannot be decomposed: %q", uri)
	}
	// Only the pseudonymous username reaches the URI -- no name, no email.
	if !strings.Contains(uri, "approver/product-owner") {
		t.Errorf("the URI does not name the approver as a username: %q", uri)
	}
}

func TestRecordingTheSameApprovalTwiceReplacesRatherThanAccumulates(t *testing.T) {
	// An operator re-running a command is the ordinary case, not the
	// exceptional one. Two entries for one approver would make a gate look
	// twice-approved by one person.
	freezeClock(t)
	root, manifest := githubBoundProject(t)
	record := func(reviewID string) {
		t.Helper()
		code, output := runCLI(t, "--provider", manifest, "approve-from-github",
			"--root", root, "--task-id", decideTask, "--gate", "G1",
			"--role", "product_owner", "--repo", "acme/app", "--pr", "1",
			"--review-id", reviewID, "--reviewer-login", "product-owner",
			"--commit-sha", "abc123", "--decided-at", "2026-08-15T09:00:00+00:00")
		if code != 0 {
			t.Fatalf("recording failed: exit %d\n%s", code, output)
		}
	}
	record("5001")
	record("5002")

	gate := approvalRecord(t, root, "G1")
	approvals := listOf(gate["human_approvals"])
	if len(approvals) != 1 {
		t.Fatalf("two recordings left %d approvals: %v", len(approvals), approvals)
	}
	approval, _ := approvals[0].(map[string]any)
	ref, _ := listOf(approval["evidence_refs"])[0].(map[string]any)
	if !strings.Contains(toStringOrEmpty(ref["uri"]), "review/5002") {
		t.Errorf("the surviving approval cites %v, not the later review", ref["uri"])
	}
}

func TestOneApprovalIsNotAGateThatNeedsTwo(t *testing.T) {
	// G2 requires a product owner and an engineering lead. A kernel that
	// marked a gate approved on the first approval it saw would look correct
	// on every single-authority gate, which is most of them.
	freezeClock(t)
	root, manifest := githubBoundProject(t)

	code, output := runCLI(t, "--provider", manifest, "approve-from-github",
		"--root", root, "--task-id", decideTask, "--gate", "G2",
		"--role", "product_owner", "--repo", "acme/app", "--pr", "1",
		"--review-id", "5001", "--reviewer-login", "product-owner",
		"--commit-sha", "abc123", "--decided-at", "2026-08-15T09:00:00+00:00")
	if code != 0 {
		t.Fatalf("recording failed: exit %d\n%s", code, output)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, output)
	}
	if report["gate_status"] == "approved" {
		t.Errorf("one of two required authorities approved and the gate moved:\n%s", output)
	}
	gate := approvalRecord(t, root, "G2")
	if gate["status"] == "approved" {
		t.Errorf("the run record marks G2 approved on one approval")
	}
	if len(listOf(gate["human_approvals"])) != 1 {
		t.Errorf("the approval was not recorded at all")
	}
}

func TestAReviewByTheWrongPersonIsNotRecordedAtAll(t *testing.T) {
	// The refusal has to leave the record untouched. One that reported itself
	// correctly and still wrote the approval would pass any check that only
	// read the exit code.
	freezeClock(t)
	root, manifest := githubBoundProject(t)
	before := overlayFingerprint(t, root)

	code, output := runCLI(t, "--provider", manifest, "approve-from-github",
		"--root", root, "--task-id", decideTask, "--gate", "G1",
		"--role", "product_owner", "--repo", "acme/app", "--pr", "1",
		"--review-id", "5001", "--reviewer-login", "somebody-else",
		"--commit-sha", "abc123", "--decided-at", "2026-08-15T09:00:00+00:00")
	if code == 0 {
		t.Fatalf("a review by the wrong person was recorded:\n%s", output)
	}
	if !strings.Contains(output, "does not match assigned authority") {
		t.Errorf("the refusal does not say why:\n%s", output)
	}
	if overlayFingerprint(t, root) != before {
		t.Error("the refusal still changed the project")
	}
}

func TestTheAutomaticAdaptersReportWhichReviewTheyChose(t *testing.T) {
	// An operator who named no review needs to see which one was recorded on
	// their behalf -- otherwise the evidence cites something they never saw.
	freezeClock(t)
	root, manifest := githubBoundProject(t)
	writeForgeMock(t, GitHubReviewsMockEnv, []any{
		review(5001, "product-owner", "APPROVED", "2026-08-14T09:00:00Z", "abc123"),
		review(5002, "product-owner", "APPROVED", "2026-08-15T09:00:00Z", "abc123"),
	})

	code, output := runCLI(t, "--provider", manifest, "approve-from-github-pr",
		"--root", root, "--task-id", decideTask, "--gate", "G1",
		"--role", "product_owner", "--repo", "acme/app", "--pr", "1")
	if code != 0 {
		t.Fatalf("recording failed: exit %d\n%s", code, output)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, output)
	}
	if report["selected_review_id"] != float64(5002) {
		t.Errorf("chose review %v, not the latest", report["selected_review_id"])
	}
	if report["selected_commit_sha"] != "abc123" {
		t.Errorf("did not report the commit it recorded: %v", report["selected_commit_sha"])
	}
}

func TestTheAutomaticGitLabAdapterReportsWhichApprovalItChose(t *testing.T) {
	freezeClock(t)
	root, manifest := gitlabBoundProject(t)
	writeForgeMock(t, GitLabApprovalsMockEnv, map[string]any{
		"sha": "abc123", "updated_at": "2026-08-15T09:00:00Z",
		"approved_by": []any{map[string]any{
			"user": map[string]any{"id": 77, "username": "product-owner"}}},
	})

	code, output := runCLI(t, "--provider", manifest, "approve-from-gitlab-mr",
		"--root", root, "--task-id", decideTask, "--gate", "G1",
		"--role", "product_owner", "--project-path", "acme/app", "--mr-iid", "7")
	if code != 0 {
		t.Fatalf("recording failed: exit %d\n%s", code, output)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, output)
	}
	if report["selected_approval_id"] != "77" {
		t.Errorf("chose approval %v", report["selected_approval_id"])
	}
}

func TestAGateEligibilityRefusalSaysWhichKindItIs(t *testing.T) {
	// Two kinds, and they are not interchangeable: a gate id that does not
	// exist is a typo somebody fixes by typing something else, and a gate the
	// task is not configured for is a statement about the project that
	// somebody has to go and change. Callers map them to different exit codes.
	record, err := loadJSONObject(filepath.Join(func() string {
		root, _ := decidableProject(t)
		return root
	}(), Overlay, "runs", decideTask, "run-record.json"))
	if err != nil {
		t.Fatal(err)
	}
	gateByID := map[string]map[string]any{}
	for _, raw := range listOf(record["lifecycle_gates"]) {
		gate, _ := raw.(map[string]any)
		id, _ := gate["gate_id"].(string)
		gateByID[id] = gate
	}
	dispatch := map[string]any{"gate_dispatch": []any{
		map[string]any{"gate_id": "G1", "status": "required"},
	}}

	for _, probe := range []struct {
		name       string
		gateID     string
		needsHuman bool
	}{
		{"a gate id that does not exist", "G99", false},
		{"a gate the task is not configured for", "G7", true},
	} {
		t.Run(probe.name, func(t *testing.T) {
			err := CheckGateEligibility(probe.gateID, dispatch, gateByID[probe.gateID])
			if err == nil {
				t.Fatalf("%s was accepted", probe.gateID)
			}
			var eligibility *GateEligibilityError
			if !errors.As(err, &eligibility) {
				t.Fatalf("not a GateEligibilityError: %T", err)
			}
			if eligibility.NeedsHuman != probe.needsHuman {
				t.Errorf("NeedsHuman is %v, wanted %v: %s",
					eligibility.NeedsHuman, probe.needsHuman, err)
			}
		})
	}
}
