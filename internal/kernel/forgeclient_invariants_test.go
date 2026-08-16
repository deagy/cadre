package kernel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Forgeclient: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of forgeclient_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

func TestTheMockCorpusCoversBothOutcomes(t *testing.T) {
	// Self-vacuity: a corpus of only-successful calls agrees with any client
	// that never refuses anything, which is the shape of a mock convention
	// that answers "no" instead of erroring.
	succeeded, refused := 0, 0
	for _, probe := range mockCases {
		path := filepath.Join(t.TempDir(), "mock.json")
		encoded, err := json.MarshalIndent(probe.mock, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(probe.variable, path)
		if _, err := probe.call(t); err == nil {
			succeeded++
			continue
		}
		refused++
	}
	if succeeded < 5 || refused < 3 {
		t.Errorf("the corpus succeeds %d times and refuses %d; it needs both",
			succeeded, refused)
	}
}

func TestBothForgesKeepTheirOwnMockFile(t *testing.T) {
	// A single file multiplexing both forges would let a GitHub test pass on a
	// GitLab fixture, which is the one way these conventions could agree with
	// Python and still be wrong.
	if GitHubReadMockEnv == GitLabIssueMockEnv {
		t.Fatal("both forges read the same environment variable")
	}
	writeMock(t, GitLabIssueMockEnv, map[string]any{
		"identity": map[string]any{"username": "sdlc-bot"},
	})
	// The GitHub client has no mock configured and must not borrow GitLab's.
	if client := gitHubClient(t); client.Mocked() {
		t.Error("the GitHub client read the GitLab mock file")
	}
	if !strings.HasPrefix(GitHubReadMockEnv, "AGENTIC_SDLC_TEST_") ||
		!strings.HasPrefix(GitLabIssueMockEnv, "AGENTIC_SDLC_TEST_") {
		t.Error("a mock variable is not namespaced as a test-only switch")
	}
}

var mockCases = []mockCase{
	{
		name: "github identity", variable: GitHubReadMockEnv,
		mock: map[string]any{"identity": map[string]any{"login": "sdlc-bot"}},
		call: func(t *testing.T) (any, error) {
			return gitHubClient(t).VerifyIdentity("sdlc-bot")
		},
		pythonCall: `github_write.verify_github_identity("sdlc-bot")`,
	},
	{
		name: "github identity mismatch", variable: GitHubReadMockEnv,
		mock: map[string]any{"identity": map[string]any{"login": "somebody-else"}},
		call: func(t *testing.T) (any, error) {
			return gitHubClient(t).VerifyIdentity("sdlc-bot")
		},
		pythonCall: `github_write.verify_github_identity("sdlc-bot")`,
	},
	{
		name: "github identity missing from the mock", variable: GitHubReadMockEnv,
		mock: map[string]any{},
		call: func(t *testing.T) (any, error) {
			return gitHubClient(t).VerifyIdentity("sdlc-bot")
		},
		pythonCall: `github_write.verify_github_identity("sdlc-bot")`,
	},
	{
		name: "github pull request", variable: GitHubReadMockEnv,
		mock: map[string]any{"pr": map[string]any{"number": 7, "state": "open"}},
		call: func(t *testing.T) (any, error) {
			return gitHubClient(t).FetchPullRequest("acme/app", 7)
		},
		pythonCall: `github_write.fetch_github_pr("acme/app", 7)`,
	},
	{
		name: "github pull request absent", variable: GitHubReadMockEnv,
		mock: map[string]any{},
		call: func(t *testing.T) (any, error) {
			return gitHubClient(t).FetchPullRequest("acme/app", 7)
		},
		pythonCall: `github_write.fetch_github_pr("acme/app", 7)`,
	},
	{
		name: "github requested reviewers", variable: GitHubReadMockEnv,
		mock: map[string]any{"requested_reviewers": map[string]any{
			"users": []any{map[string]any{"login": "one"}, map[string]any{"login": "two"}},
			"teams": []any{map[string]any{"slug": "platform"}},
		}},
		call: func(t *testing.T) (any, error) {
			return gitHubClient(t).FetchRequestedReviewers("acme/app", 7)
		},
		pythonCall: `github_write.fetch_requested_reviewers("acme/app", 7)`,
	},
	{
		name: "github requested reviewers absent", variable: GitHubReadMockEnv,
		mock: map[string]any{},
		call: func(t *testing.T) (any, error) {
			return gitHubClient(t).FetchRequestedReviewers("acme/app", 7)
		},
		pythonCall: `github_write.fetch_requested_reviewers("acme/app", 7)`,
	},
	{
		name: "github user exists", variable: GitHubReadMockEnv,
		mock: map[string]any{"users": map[string]any{"octocat": true}},
		call: func(t *testing.T) (any, error) {
			return gitHubClient(t).UserExists("octocat")
		},
		pythonCall: `github_write.check_github_user_exists("octocat")`,
	},
	{
		name: "github user unconfigured", variable: GitHubReadMockEnv,
		mock: map[string]any{"users": map[string]any{}},
		call: func(t *testing.T) (any, error) {
			return gitHubClient(t).UserExists("octocat")
		},
		pythonCall: `github_write.check_github_user_exists("octocat")`,
	},
	{
		name: "github collaborator", variable: GitHubReadMockEnv,
		mock: map[string]any{"collaborators": map[string]any{"acme/app:octocat": false}},
		call: func(t *testing.T) (any, error) {
			return gitHubClient(t).IsCollaborator("acme/app", "octocat")
		},
		pythonCall: `github_write.check_github_collaborator("acme/app", "octocat")`,
	},

	{
		name: "gitlab identity", variable: GitLabIssueMockEnv,
		mock: map[string]any{"identity": map[string]any{"username": "SDLC-Bot"}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).VerifyIdentity("sdlc-bot")
		},
		pythonCall: `gitlab_write.verify_gitlab_identity("sdlc-bot")`,
	},
	{
		name: "gitlab issue search", variable: GitLabIssueMockEnv,
		mock: map[string]any{"search": map[string]any{
			"agentic-sdlc,agentic-sdlc-gate-abc": []any{map[string]any{"iid": 7}},
		}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).SearchIssuesByLabels("acme/app",
				[]string{"agentic-sdlc", "agentic-sdlc-gate-abc"})
		},
		pythonCall: `gitlab_write.search_gitlab_issues_by_labels("acme/app", ["agentic-sdlc", "agentic-sdlc-gate-abc"])`,
	},
	{
		name: "gitlab issue search with no entry", variable: GitLabIssueMockEnv,
		mock: map[string]any{"search": map[string]any{}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).SearchIssuesByLabels("acme/app", []string{"agentic-sdlc"})
		},
		pythonCall: `gitlab_write.search_gitlab_issues_by_labels("acme/app", ["agentic-sdlc"])`,
	},
	{
		name: "gitlab issue create", variable: GitLabIssueMockEnv,
		mock: map[string]any{"create": map[string]any{
			"agentic-sdlc,agentic-sdlc-gate-abc": map[string]any{"iid": 12},
		}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).CreateIssue("acme/app", "G1 Intent", "body",
				[]string{"agentic-sdlc", "agentic-sdlc-gate-abc"}, nil)
		},
		pythonCall: `gitlab_write.create_gitlab_issue("acme/app", "G1 Intent", "body", ["agentic-sdlc", "agentic-sdlc-gate-abc"])`,
	},
	{
		name: "gitlab issue create with no entry", variable: GitLabIssueMockEnv,
		mock: map[string]any{"create": map[string]any{}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).CreateIssue("acme/app", "G1", "body",
				[]string{"agentic-sdlc"}, nil)
		},
		pythonCall: `gitlab_write.create_gitlab_issue("acme/app", "G1", "body", ["agentic-sdlc"])`,
	},
	{
		name: "gitlab issue verification", variable: GitLabIssueMockEnv,
		mock: map[string]any{"verify": map[string]any{
			"7": map[string]any{
				"title": "G1 Intent", "state": "opened",
				"labels":       []any{"agentic-sdlc"},
				"confidential": true,
				"assignees": []any{
					map[string]any{"username": "zoe"}, map[string]any{"username": "adam"},
				},
				"author":     map[string]any{"username": "sdlc-bot"},
				"references": map[string]any{"full": "acme/app#7"},
				"web_url":    "https://gitlab.example/acme/app/-/issues/7",
			},
		}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).FetchIssueVerification("acme/app", 7)
		},
		pythonCall: `gitlab_write.fetch_gitlab_issue_verification("acme/app", 7)`,
	},
	{
		name: "gitlab username resolution", variable: GitLabIssueMockEnv,
		mock: map[string]any{"users": map[string]any{
			"zoe": []any{map[string]any{"id": 4, "username": "zoe", "state": "active"}},
		}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).ResolveUsername("zoe")
		},
		pythonCall: `gitlab_write.resolve_gitlab_user_id("zoe")`,
	},
	{
		name: "gitlab username with no entry", variable: GitLabIssueMockEnv,
		mock: map[string]any{"users": map[string]any{}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).ResolveUsername("nobody")
		},
		pythonCall: `gitlab_write.resolve_gitlab_user_id("nobody")`,
	},
	{
		name: "gitlab merge request", variable: GitLabIssueMockEnv,
		mock: map[string]any{"mr": map[string]any{"iid": 3, "state": "opened"}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).FetchMergeRequest("acme/app", 3)
		},
		pythonCall: `gitlab_write.fetch_gitlab_mr("acme/app", 3)`,
	},
	{
		name: "gitlab merge request absent", variable: GitLabIssueMockEnv,
		mock: map[string]any{},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).FetchMergeRequest("acme/app", 3)
		},
		pythonCall: `gitlab_write.fetch_gitlab_mr("acme/app", 3)`,
	},
	{
		name: "gitlab note creation", variable: GitLabIssueMockEnv,
		mock: map[string]any{"notes_create": map[string]any{
			"acme/app:3": map[string]any{"id": 99},
		}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).CreateMergeRequestNote("acme/app", 3, "body")
		},
		pythonCall: `gitlab_write.create_mr_note("acme/app", 3, "body")`,
	},
	{
		name: "gitlab note page", variable: GitLabIssueMockEnv,
		mock: map[string]any{"notes_list": map[string]any{
			"acme/app:3": map[string]any{"1": []any{map[string]any{"id": 1, "body": "hello"}}},
		}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).ListMergeRequestNotes("acme/app", 3, 1, 100)
		},
		pythonCall: `gitlab_write.list_mr_notes("acme/app", 3, page=1)`,
	},
	{
		name: "gitlab note page beyond the end", variable: GitLabIssueMockEnv,
		mock: map[string]any{"notes_list": map[string]any{}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).ListMergeRequestNotes("acme/app", 3, 2, 100)
		},
		pythonCall: `gitlab_write.list_mr_notes("acme/app", 3, page=2)`,
	},
	{
		name: "gitlab issue link unavailable", variable: GitLabIssueMockEnv,
		mock: map[string]any{"link": map[string]any{
			"7": map[string]any{"error_status": 403, "error": "disabled"},
		}},
		call: func(t *testing.T) (any, error) {
			return gitLabClient(t).CreateIssueLink("acme/app", 7, "acme/app", 9, "relates_to")
		},
		pythonCall: `gitlab_write.create_gitlab_issue_link("acme/app", 7, "acme/app", 9)`,
	},
}

type mockCase struct {
	name string
	// variable is the environment variable naming the mock file.
	variable string
	mock     map[string]any
	// call names the function, and pythonCall is the expression the Python
	// side evaluates. They are separate because the two languages spell the
	// same call differently, not because they do different things.
	call       func(t *testing.T) (any, error)
	pythonCall string
}

func gitHubClient(t *testing.T) *GitHubClient {
	t.Helper()
	client, err := NewGitHubClient()
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func gitLabClient(t *testing.T) *GitLabClient {
	t.Helper()
	client, err := NewGitLabClient()
	if err != nil {
		t.Fatal(err)
	}
	return client
}
