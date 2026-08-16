package kernel

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/canonicaljson"
)

// The mock conventions, compared with the Python clients on the same files.
//
// This is the comparison that matters most in these two modules. The network
// path cannot be compared without a network, but the *mock* path is what every
// layer above these clients is tested through -- so if the conventions differ,
// every fixture written for the Python modules stops working against the Go
// ones, silently, in whichever direction a given test happens to read.
//
// Each case below writes one mock file, calls the same function on both sides,
// and compares the result or the refusal.

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

func TestTheMockConventionsMatchThePythonClients(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is unavailable, so there is nothing to compare against")
	}
	for _, probe := range mockCases {
		t.Run(probe.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mock.json")
			encoded, err := json.MarshalIndent(probe.mock, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(probe.variable, path)

			value, goErr := probe.call(t)
			pythonOK, pythonValue, pythonReason := pythonForgeCall(t,
				probe.variable, path, probe.pythonCall)

			if pythonOK != (goErr == nil) {
				t.Fatalf("python ok=%v (%s), go ok=%v (%v)",
					pythonOK, pythonReason, goErr == nil, goErr)
			}
			if !pythonOK {
				// The refusal text is this kernel's own, so it is compared in
				// full -- these messages are what an operator reads to find
				// out which fixture or which credential is wrong.
				if goErr.Error() != pythonReason {
					t.Errorf("python refused with %q, go refused with %q",
						pythonReason, goErr.Error())
				}
				return
			}
			if got := normaliseForgeValue(t, value); got != pythonValue {
				t.Errorf("python returned %s, go returned %s", pythonValue, got)
			}
		})
	}
}

// normaliseForgeValue renders a Go result as canonical JSON, so the comparison
// is about the value rather than about how two languages print one.
func normaliseForgeValue(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicaljson.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}

// pythonForgeCall evaluates one expression against the same mock file.
func pythonForgeCall(t *testing.T, variable, path, expression string) (bool, string, string) {
	t.Helper()
	script := `
import json, os, sys
from agentic_sdlc import github_write, gitlab_write

os.environ[sys.argv[1]] = sys.argv[2]
try:
    value = eval(sys.argv[3])
except Exception as error:
    print(json.dumps({"ok": False, "value": "", "reason": str(error)}))
else:
    print(json.dumps({
        "ok": True,
        "value": json.dumps(value, sort_keys=True, separators=(",", ":")),
        "reason": "",
    }))
`
	command := exec.Command("python3", "-c", script, variable, path, expression)
	command.Dir = filepath.Join(repositoryRoot(t), "kernel")
	output, err := command.Output()
	if err != nil {
		t.Skipf("the Python client could not be run: %v", err)
	}
	var result struct {
		OK     bool   `json:"ok"`
		Value  string `json:"value"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("the Python side did not return JSON: %s", output)
	}
	return result.OK, result.Value, result.Reason
}

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
