package githubapproval

import (
	"strings"
	"testing"
)

func review(login, submittedAt, reviewState string) Review {
	return Review{
		// A realistic GitHub review id. 4242 was too small to distinguish
		// %v from an integer render -- Go prints small float64s without an
		// exponent, so the test passed either way and proved nothing.
		ID:          float64(1234567890),
		User:        map[string]any{"login": login},
		SubmittedAt: submittedAt,
		State:       reviewState,
		CommitID:    strings.Repeat("a", 40),
	}
}

// Latest wins, ordered chronologically rather than lexically.
//
// The Python sorted timestamps as strings, which agrees with chronology only
// while every timestamp carries the same offset. GitHub returns UTC, so the
// defect never fired there -- but the rule it breaks is the one that makes
// "latest review wins" mean anything.
//
// "2026-08-18T12:00:00+02:00" is 10:00Z: an hour EARLIER than
// "2026-08-18T11:00:00Z", while sorting after it as a string. Under the
// Python, the stale APPROVED wins and the CHANGES_REQUESTED that superseded it
// is discarded. Verified against the Python before this was changed.
func TestLatestReviewIsChronologicalNotLexical(t *testing.T) {
	reviews := []Review{
		review("alice", "2026-08-18T11:00:00Z", "CHANGES_REQUESTED"),
		review("alice", "2026-08-18T12:00:00+02:00", "APPROVED"),
	}

	_, err := SelectReview(reviews, "alice", "")
	if err == nil {
		t.Fatal("a stale APPROVED won over a later CHANGES_REQUESTED; " +
			"the timestamps were compared as strings, not instants")
	}
	if !strings.Contains(err.Error(), "not an effective approval") {
		t.Errorf("error was %q, want the latest review to be reported as not an approval", err)
	}
}

func TestLatestApprovalWins(t *testing.T) {
	reviews := []Review{
		review("alice", "2026-08-18T10:00:00Z", "CHANGES_REQUESTED"),
		review("alice", "2026-08-18T11:00:00Z", "APPROVED"),
	}
	selected, err := SelectReview(reviews, "alice", "")
	if err != nil {
		t.Fatalf("SelectReview: %v", err)
	}
	if selected.State != "APPROVED" {
		t.Errorf("selected %s", selected.State)
	}
}

func TestALaterChangesRequestedInvalidatesAnApproval(t *testing.T) {
	reviews := []Review{
		review("alice", "2026-08-18T10:00:00Z", "APPROVED"),
		review("alice", "2026-08-18T11:00:00Z", "CHANGES_REQUESTED"),
	}
	if _, err := SelectReview(reviews, "alice", ""); err == nil {
		t.Error("an approval superseded by a later CHANGES_REQUESTED was accepted")
	}
}

func TestADismissedApprovalIsNotEffective(t *testing.T) {
	dismissed := review("alice", "2026-08-18T10:00:00Z", "APPROVED")
	dismissed.DismissedState = "DISMISSED"
	if _, err := SelectReview([]Review{dismissed}, "alice", ""); err == nil {
		t.Error("a dismissed approval was treated as effective")
	}
}

func TestReviewerAndCommitFiltering(t *testing.T) {
	reviews := []Review{
		review("bob", "2026-08-18T11:00:00Z", "APPROVED"),
		review("alice", "2026-08-18T10:00:00Z", "APPROVED"),
	}

	selected, err := SelectReview(reviews, "ALICE", "") // case-insensitive
	if err != nil {
		t.Fatalf("SelectReview: %v", err)
	}
	if selected.Login() != "alice" {
		t.Errorf("selected %q, want alice", selected.Login())
	}

	if _, err := SelectReview(reviews, "carol", ""); err == nil {
		t.Error("a reviewer with no reviews produced a selection")
	}

	other := strings.Repeat("b", 40)
	if _, err := SelectReview(reviews, "alice", other); err == nil {
		t.Error("a review at a different commit was selected")
	}
}

// A review with no usable timestamp cannot be the latest anything.
func TestReviewsWithoutAValidTimestampAreIgnored(t *testing.T) {
	naive := review("alice", "2026-08-18T10:00:00", "APPROVED") // no offset
	if _, err := SelectReview([]Review{naive}, "alice", ""); err == nil {
		t.Error("a review with a naive timestamp was selected")
	}
}

func TestReviewToApproval(t *testing.T) {
	selected := review("alice", "2026-08-18T10:00:00Z", "APPROVED")

	approval, err := ReviewToApproval(selected, ApprovalOptions{
		GateID: "G3", AuthorityID: "system_architect", RoleLabel: "System Architect",
		Repo: "deagy/cadre", PR: 7, DecidedAt: "2026-08-18T10:05:00Z",
	})
	if err != nil {
		t.Fatalf("ReviewToApproval: %v", err)
	}

	if approval.Status != "approved" {
		t.Errorf("status = %q", approval.Status)
	}
	if approval.Approver == nil || approval.Approver.Kind != "human" {
		t.Errorf("approver = %+v, want a human identity", approval.Approver)
	}
	if len(approval.EvidenceRefs) != 1 {
		t.Fatalf("got %d evidence refs, want 1", len(approval.EvidenceRefs))
	}

	evidence := approval.EvidenceRefs[0]
	// The id must render as an integer: GitHub sends a number, which arrives
	// as float64 and would otherwise appear as 4.242e+03 in a URI that then
	// fails to parse.
	if !strings.Contains(evidence.URI, "review/1234567890") {
		t.Errorf("uri = %q, want the review id rendered as an integer", evidence.URI)
	}
	if ParseReviewURI(evidence.URI) == nil {
		t.Errorf("the built uri %q does not parse back", evidence.URI)
	}
	if len(evidence.Hash) != 64 {
		t.Errorf("hash = %q, want a 64-character sha256", evidence.Hash)
	}
}

// The surviving half of the legacy authorities.json cross-check.
func TestExpectedLoginMustMatch(t *testing.T) {
	selected := review("alice", "2026-08-18T10:00:00Z", "APPROVED")
	options := ApprovalOptions{GateID: "G3", AuthorityID: "a", RoleLabel: "r", Repo: "o/r", PR: 1}

	options.ExpectedLogin = "ALICE"
	if _, err := ReviewToApproval(selected, options); err != nil {
		t.Errorf("a matching login (differing only in case) was rejected: %v", err)
	}

	options.ExpectedLogin = "mallory"
	if _, err := ReviewToApproval(selected, options); err == nil {
		t.Error("a review by someone other than the expected authority was accepted")
	}
}

func TestNormalizeCommitSHA(t *testing.T) {
	full := strings.Repeat("A", 40)
	if got := NormalizeCommitSHA(full); got != strings.Repeat("a", 40) {
		t.Errorf("NormalizeCommitSHA(%q) = %q", full, got)
	}
	for _, bad := range []any{"abc", strings.Repeat("z", 40), 42, nil, ""} {
		if got := NormalizeCommitSHA(bad); got != "" {
			t.Errorf("NormalizeCommitSHA(%#v) = %q, want empty", bad, got)
		}
	}
}
