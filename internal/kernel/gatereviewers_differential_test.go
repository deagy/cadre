package kernel

import (
	"bytes"
	"path/filepath"
	"testing"
)

// The two reviewer reports, compared with the Python kernel over the same
// mock files.
//
// Both are read-only, so a differential can exercise them completely: the
// forge answers come from a file, the project comes from a fixture, and every
// branch of the classification is reachable by editing one of the two.
//
// The exit code is compared as carefully as the report. It carries the
// distinction that matters operationally -- 0 means these reviewers can be
// requested, 2 means somebody has to act first, and 1 means the report could
// not be built at all. A port that got the document right and the code wrong
// would look correct in every log and break every pipeline reading it.

var gitHubReviewerCases = []struct {
	name    string
	args    []string
	prepare func(t *testing.T, root string, mock map[string]any)
	// expectExit distinguishes "these can be requested" from "somebody has to
	// act" from "the report could not be built".
	expectExit int
}{
	{name: "every configured gate", args: nil},
	{
		// The PR's author cannot be an independent reviewer of it. This is
		// the conflict most likely to occur in practice: the person who did
		// the work often holds authority over one of its gates.
		name: "the pull request's author holds authority",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			pullRequest, _ := mock["pr"].(map[string]any)
			pullRequest["user"] = map[string]any{"login": "engineering-lead"}
		},
		expectExit: 2,
	},
	{name: "one named gate", args: []string{"--gates", "G1"}},
	{name: "two named gates", args: []string{"--gates", "G1,G3"}},
	{
		name: "a gate the plan does not configure", args: []string{"--gates", "G9"},
		expectExit: 1,
	},
	{name: "a gate id that does not exist", args: []string{"--gates", "G99"}, expectExit: 1},
	{
		name: "a login that cannot review this repository",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			collaborators, _ := mock["collaborators"].(map[string]any)
			collaborators["acme/app:product-owner"] = false
		},
		expectExit: 2,
	},
	{
		name: "a login GitHub has never heard of",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			users, _ := mock["users"].(map[string]any)
			users["product-owner"] = false
		},
		expectExit: 2,
	},
	{
		name: "an authority nobody is assigned to",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
				func(document map[string]any) {
					authority, _ := document["engineering_lead"].(map[string]any)
					authority["status"] = "unknown"
					authority["assignee"] = nil
				})
		},
		expectExit: 2,
	},
	{
		name: "an authority with no GitHub binding",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
				func(document map[string]any) {
					authority, _ := document["product_owner"].(map[string]any)
					authority["assignee"] = "someone@example.com"
				})
		},
		expectExit: 2,
	},
	{
		// The poisoning rule. The bot doing the asking cannot also be the
		// reviewer, and that withholds the login from every gate it was
		// motivated by -- not just the one that conflicted.
		name: "the bot is itself an authority",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
				func(document map[string]any) {
					authority, _ := document["product_owner"].(map[string]any)
					authority["assignee"] = "github.com/sdlc-bot"
				})
			users, _ := mock["users"].(map[string]any)
			users["sdlc-bot"] = true
			collaborators, _ := mock["collaborators"].(map[string]any)
			collaborators["acme/app:sdlc-bot"] = true
		},
		expectExit: 2,
	},
	{
		name: "a closed pull request",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			pullRequest, _ := mock["pr"].(map[string]any)
			pullRequest["state"] = "closed"
		},
		expectExit: 1,
	},
	{
		name: "a pull request belonging to another repository",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			pullRequest, _ := mock["pr"].(map[string]any)
			base, _ := pullRequest["base"].(map[string]any)
			repo, _ := base["repo"].(map[string]any)
			repo["full_name"] = "somebody/else"
		},
		expectExit: 1,
	},
	{
		name: "the wrong classification",
		args: []string{"--allow-classification", "restricted"}, expectExit: 1,
	},
	{name: "an identity that is not the bot", args: []string{"--as-bot", "somebody-else"}, expectExit: 1},
}

func TestTheGitHubReviewerReportMatchesThePythonKernel(t *testing.T) {
	for _, probe := range gitHubReviewerCases {
		t.Run(probe.name, func(t *testing.T) {
			root, manifest := reviewerFixture(t)
			mock := gitHubReviewerMock()
			if probe.prepare != nil {
				probe.prepare(t, root, mock)
			}
			writeForgeMock(t, GitHubReadMockEnv, mock)
			writeForgeMock(t, "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE", gitHubReviewsMock())

			args := []string{"--provider", manifest, "request-gate-reviewers",
				"--root", root, "--task-id", decideTask, "--repo", "acme/app",
				"--pr", "3", "--as-bot", "sdlc-bot", "--allow-classification", "internal"}
			args = append(args, probe.args...)

			pythonCode, pythonOutput := runPythonKernelWithEnvironment(t, args)
			var stdout, stderr bytes.Buffer
			goCode := Run(args, &stdout, &stderr)

			if pythonCode != probe.expectExit || goCode != probe.expectExit {
				t.Fatalf("expected exit %d -- python %d, go %d\npython: %s\ngo: %s",
					probe.expectExit, pythonCode, goCode, pythonOutput,
					stdout.String()+stderr.String())
			}
			if got := stdout.String() + stderr.String(); got != pythonOutput {
				t.Errorf("report differs.\npython:\n%s\ngo:\n%s", pythonOutput, got)
			}
		})
	}
}

var gitLabReviewerCases = []struct {
	name       string
	args       []string
	prepare    func(t *testing.T, root string, mock map[string]any)
	expectExit int
}{
	{name: "every configured gate", expectExit: 2},
	{
		name: "a username two accounts answer to",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			users, _ := mock["users"].(map[string]any)
			users["product-owner"] = []any{
				map[string]any{"id": 1, "username": "product-owner", "state": "active"},
				map[string]any{"id": 9, "username": "product-owner", "state": "active"},
			}
		},
		expectExit: 2,
	},
	{
		name: "a username with only a blocked account",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			users, _ := mock["users"].(map[string]any)
			users["product-owner"] = []any{
				map[string]any{"id": 1, "username": "product-owner", "state": "blocked"},
			}
		},
		expectExit: 2,
	},
	{
		name: "a merge request in another project",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			mergeRequest, _ := mock["mr"].(map[string]any)
			references, _ := mergeRequest["references"].(map[string]any)
			references["full"] = "somebody/else!5"
		},
		expectExit: 1,
	},
	{
		name: "a merged merge request",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			mergeRequest, _ := mock["mr"].(map[string]any)
			mergeRequest["state"] = "merged"
		},
		expectExit: 1,
	},
	{
		// The legacy draft convention, which some self-hosted instances still
		// expose instead of the `draft` field.
		name: "a draft detected only from the title",
		prepare: func(t *testing.T, root string, mock map[string]any) {
			mergeRequest, _ := mock["mr"].(map[string]any)
			delete(mergeRequest, "draft")
			mergeRequest["title"] = "WIP: still working"
		},
		expectExit: 2,
	},
}

func TestTheGitLabReviewerReportMatchesThePythonKernel(t *testing.T) {
	for _, probe := range gitLabReviewerCases {
		t.Run(probe.name, func(t *testing.T) {
			root, manifest := reviewerFixture(t)
			mock := gitLabReviewerMock()
			if probe.prepare != nil {
				probe.prepare(t, root, mock)
			}
			writeForgeMock(t, GitLabIssueMockEnv, mock)
			writeForgeMock(t, "AGENTIC_SDLC_TEST_GITLAB_APPROVALS_FILE", map[string]any{
				"sha": "def456", "updated_at": "2026-08-15T09:00:00Z",
				"approved_by": []any{
					map[string]any{"user": map[string]any{"id": 1, "username": "product-owner"}},
				},
			})

			args := []string{"--provider", manifest, "request-gate-reviewers-gitlab",
				"--root", root, "--task-id", decideTask, "--project-path", "acme/app",
				"--mr-iid", "5", "--as-bot", "sdlc-bot", "--allow-classification", "internal"}
			args = append(args, probe.args...)

			pythonCode, pythonOutput := runPythonKernelWithEnvironment(t, args)
			var stdout, stderr bytes.Buffer
			goCode := Run(args, &stdout, &stderr)

			if pythonCode != probe.expectExit || goCode != probe.expectExit {
				t.Fatalf("expected exit %d -- python %d, go %d\npython: %s\ngo: %s",
					probe.expectExit, pythonCode, goCode, pythonOutput,
					stdout.String()+stderr.String())
			}
			if got := stdout.String() + stderr.String(); got != pythonOutput {
				t.Errorf("report differs.\npython:\n%s\ngo:\n%s", pythonOutput, got)
			}
		})
	}
}

// The invariants, stated without reference to the Python kernel.

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
func runPythonKernelWithEnvironment(t *testing.T, args []string) (int, string) {
	t.Helper()
	return pythonKernelIn(filepath.Join(repositoryRoot(t), "kernel"), args...)
}
