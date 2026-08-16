package kernel

import (
	"path/filepath"
	"testing"
)

// Gatereviewers: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of gatereviewers_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

func TestAnIndependenceConflictWithholdsALoginEverywhere(t *testing.T) {
	// The poisoning rule, which is the one piece of policy here that is not
	// obvious from the output. A review request is pull-request-wide: inviting
	// somebody to review at all lets them approve every gate the PR touches,
	// so a conflict on one gate withholds them from all of their motivations.
	root, manifest := reviewerFixture(t)
	// The product owner is also the PR author, and holds authority on G1, G2
	// and G6 -- so the conflict on one must withhold the others too.
	mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
		func(document map[string]any) {
			authority, _ := document["product_owner"].(map[string]any)
			authority["assignee"] = "github.com/pr-author"
		})
	mock := gitHubReviewerMock()
	pullRequest, _ := mock["pr"].(map[string]any)
	pullRequest["user"] = map[string]any{"login": "pr-author"}
	users, _ := mock["users"].(map[string]any)
	users["pr-author"] = true
	collaborators, _ := mock["collaborators"].(map[string]any)
	collaborators["acme/app:pr-author"] = true
	writeForgeMock(t, GitHubReadMockEnv, mock)
	writeForgeMock(t, "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE", gitHubReviewsMock())

	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	report, err := registry.RequestGateReviewers(ReviewerRequest{
		Root: root, TaskID: decideTask, Repo: "acme/app", PullRequest: 3,
		AsBot: "sdlc-bot", AllowClassification: "internal",
	})
	if err != nil {
		t.Fatal(err)
	}

	var author *ReviewerEntry
	for index := range report.Reviewers {
		if report.Reviewers[index].Login == "pr-author" {
			author = &report.Reviewers[index]
		}
	}
	if author == nil {
		t.Fatal("the PR author does not appear in the report at all")
	}
	if author.Classification != "withheld-conflict" {
		t.Errorf("the PR author is %q rather than withheld", author.Classification)
	}
	if len(author.Motivations) < 2 {
		t.Fatalf("this login has %d motivation(s); the test needs more than one to mean anything",
			len(author.Motivations))
	}
	if author.WithheldCause == nil || author.WithheldCause.Reason != "pr-author-conflict" {
		t.Errorf("the withheld cause does not name the conflict: %+v", author.WithheldCause)
	}
}

func TestAResolutionFailureDoesNotPoisonOtherMotivations(t *testing.T) {
	// The other half of the same rule, and the reason it is not simply "any
	// refusal withholds". A missing binding or an unknown account is a
	// property of that pair or that login -- it says nothing about whether
	// somebody else can review, and treating it as a conflict would withhold
	// reviewers for no reason.
	root, manifest := reviewerFixture(t)
	mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
		func(document map[string]any) {
			authority, _ := document["product_owner"].(map[string]any)
			authority["assignee"] = "someone@example.com"
		})
	writeForgeMock(t, GitHubReadMockEnv, gitHubReviewerMock())
	writeForgeMock(t, "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE", gitHubReviewsMock())

	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	report, err := registry.RequestGateReviewers(ReviewerRequest{
		Root: root, TaskID: decideTask, Repo: "acme/app", PullRequest: 3,
		AsBot: "sdlc-bot", AllowClassification: "internal",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The refusal is recorded...
	found := false
	for _, refusal := range report.Refusals {
		if refusal.Reason == "no-github-binding" {
			found = true
		}
	}
	if !found {
		t.Fatal("the missing binding was not reported as a refusal")
	}
	// ...and nobody else is withheld because of it.
	for _, entry := range report.Reviewers {
		if entry.Classification == "withheld-conflict" {
			t.Errorf("%s was withheld by an unrelated resolution failure", entry.Login)
		}
	}
}

func TestAReviewOfAnOlderCommitIsStale(t *testing.T) {
	// The distinction a reviewer report exists to make. Treating a review of
	// an earlier commit as satisfied would let a change pushed after an
	// approval inherit that approval.
	requested := map[string]bool{}
	reviews := []any{
		map[string]any{
			"user": map[string]any{"login": "reviewer"}, "state": "APPROVED",
			"submitted_at": "2026-08-14T09:00:00Z", "commit_id": "old999",
		},
	}
	if got := ClassifyReviewerLogin("reviewer", requested, reviews, "abc123"); got != "review-stale" {
		t.Errorf("a review of an older commit classified as %q", got)
	}

	current := []any{
		reviews[0],
		map[string]any{
			"user": map[string]any{"login": "reviewer"}, "state": "APPROVED",
			"submitted_at": "2026-08-15T09:00:00Z", "commit_id": "abc123",
		},
	}
	if got := ClassifyReviewerLogin("reviewer", requested, current, "abc123"); got != "already-reviewed" {
		t.Errorf("a review of the current commit classified as %q", got)
	}

	// A dismissed review is not a state of its own -- it simply does not
	// count, and the login falls through to whatever applies next.
	dismissed := []any{
		map[string]any{
			"user": map[string]any{"login": "reviewer"}, "state": "DISMISSED",
			"submitted_at": "2026-08-15T09:00:00Z", "commit_id": "abc123",
		},
	}
	if got := ClassifyReviewerLogin("reviewer", requested, dismissed, "abc123"); got != "to-request" {
		t.Errorf("a dismissed review classified as %q", got)
	}
	if got := ClassifyReviewerLogin("reviewer", map[string]bool{"reviewer": true},
		dismissed, "abc123"); got != "already-requested" {
		t.Errorf("a dismissed review on a requested reviewer classified as %q", got)
	}
}

func TestThereIsNoAmbiguousUserCodeOnTheGitHubSide(t *testing.T) {
	// A regression guard, not a style rule. GitHub's user lookup is exact, so
	// there is no ambiguous case to report -- and porting GitLab's reason code
	// across would invent a state that can never occur and that a consumer
	// would then have to handle.
	if problemClassifications["github-user-ambiguous"] {
		t.Error("github-user-ambiguous exists in the GitHub problem set")
	}
	if !gitlabProblemClassifications["gitlab-user-ambiguous"] {
		t.Error("gitlab-user-ambiguous is missing from the GitLab problem set")
	}
}

// runPythonKernelWithEnvironment runs the Python kernel with the mock
// variables this process has set, so both sides read the same files.
