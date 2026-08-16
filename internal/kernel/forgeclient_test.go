package kernel

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The forge clients, exercised through their mock files.
//
// These are the only modules in the kernel that reach outside the machine, and
// the mock is how every layer above them is tested -- so the mock's own
// contract matters as much as the network path does. A mock that silently
// answers "no" where the real client would refuse turns every test above it
// into one that proves nothing.
//
// The network path itself is exercised by pointing the client at a `gh`/`glab`
// that is not there, which is the one non-network behaviour of it worth
// pinning: the failure has to say the tool is missing rather than reporting an
// empty result.

// writeMock writes a canned-response file and points the client at it.
func writeMock(t *testing.T, variable string, payload map[string]any) {
	t.Helper()
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mock.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(variable, path)
}

func TestAMockedClientSaysItIsMocked(t *testing.T) {
	// Callers record this in their ledger. A publication that never touched
	// the network must not be indistinguishable from one that did.
	client, err := NewGitHubClient()
	if err != nil {
		t.Fatal(err)
	}
	if client.Mocked() {
		t.Error("a client with no mock configured reported itself mocked")
	}

	writeMock(t, GitHubReadMockEnv, map[string]any{"identity": map[string]any{"login": "bot"}})
	mocked, err := NewGitHubClient()
	if err != nil {
		t.Fatal(err)
	}
	if !mocked.Mocked() {
		t.Error("a client reading from a mock file did not report itself mocked")
	}
}

func TestIdentityVerificationIsCaseInsensitiveAndRefusesAMismatch(t *testing.T) {
	// Everything this kernel publishes is attributed to whoever the forge CLI
	// is authenticated as. A run that publishes as the wrong identity has
	// signed a project's evidence with the wrong name.
	writeMock(t, GitHubReadMockEnv, map[string]any{
		"identity": map[string]any{"login": "SDLC-Bot"},
	})
	client, err := NewGitHubClient()
	if err != nil {
		t.Fatal(err)
	}

	login, err := client.VerifyIdentity("sdlc-bot")
	if err != nil {
		t.Errorf("a login differing only in case was refused: %v", err)
	}
	if login != "SDLC-Bot" {
		t.Errorf("the verified login was rewritten to %q", login)
	}

	if _, err := client.VerifyIdentity("somebody-else"); err == nil {
		t.Error("a mismatched identity was accepted")
	} else if !strings.Contains(err.Error(), "does not match required bot identity") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}

func TestAMissingPullRequestIsItsOwnError(t *testing.T) {
	// A distinct type rather than a string to match on: "that PR does not
	// exist" and "GitHub refused" call for different things from the operator,
	// and telling them apart from prose would be guessing.
	writeMock(t, GitHubReadMockEnv, map[string]any{"identity": map[string]any{"login": "bot"}})
	client, err := NewGitHubClient()
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.FetchPullRequest("acme/app", 7)
	var missing *PullRequestNotFound
	if !errors.As(err, &missing) {
		t.Fatalf("an absent PR gave %T (%v), not PullRequestNotFound", err, err)
	}

	writeMock(t, GitHubReadMockEnv, map[string]any{
		"pr": map[string]any{"number": 7, "head": map[string]any{"sha": "abc"}},
	})
	present, err := NewGitHubClient()
	if err != nil {
		t.Fatal(err)
	}
	pullRequest, err := present.FetchPullRequest("acme/app", 7)
	if err != nil {
		t.Fatalf("a present PR was refused: %v", err)
	}
	if _, ok := jsonNumber(pullRequest["number"]); !ok {
		t.Errorf("the PR came back without its number: %v", pullRequest)
	}
}

func TestRequestedReviewersReportsUsersOnly(t *testing.T) {
	// Teams are excluded deliberately. A gate needs a named human, and "the
	// platform team was asked" does not say who.
	writeMock(t, GitHubReadMockEnv, map[string]any{
		"requested_reviewers": map[string]any{
			"users": []any{
				map[string]any{"login": "reviewer-one"},
				map[string]any{"login": "reviewer-two"},
			},
			"teams": []any{map[string]any{"slug": "platform"}},
		},
	})
	client, err := NewGitHubClient()
	if err != nil {
		t.Fatal(err)
	}
	logins, err := client.FetchRequestedReviewers("acme/app", 7)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(logins, ",") != "reviewer-one,reviewer-two" {
		t.Errorf("expected the two user logins, got %v", logins)
	}

	// Absent means nobody has been asked, which is a real state rather than a
	// malformed fixture.
	writeMock(t, GitHubReadMockEnv, map[string]any{})
	empty, err := NewGitHubClient()
	if err != nil {
		t.Fatal(err)
	}
	logins, err = empty.FetchRequestedReviewers("acme/app", 7)
	if err != nil || len(logins) != 0 {
		t.Errorf("an absent reviewer list gave %v, %v", logins, err)
	}
}

func TestAMockWithNoEntryIsAnErrorNotAFalse(t *testing.T) {
	// The property that keeps every test above these clients honest. If a
	// missing fixture entry answered "no", a test that forgot to set one would
	// pass while proving nothing -- and the reason codes it asserted would be
	// coming from the absence of data rather than from the data.
	writeMock(t, GitHubReadMockEnv, map[string]any{"users": map[string]any{}})
	client, err := NewGitHubClient()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UserExists("nobody-configured"); err == nil {
		t.Error("a mock with no entry for a login answered without one")
	}
	if _, err := client.IsCollaborator("acme/app", "nobody-configured"); err == nil {
		t.Error("a mock with no entry for a collaborator answered without one")
	}

	// And a configured "false" is answered as false rather than as an error,
	// or the check above would pass by refusing everything.
	writeMock(t, GitHubReadMockEnv, map[string]any{
		"users":         map[string]any{"ghost": false},
		"collaborators": map[string]any{"acme/app:ghost": false},
	})
	configured, err := NewGitHubClient()
	if err != nil {
		t.Fatal(err)
	}
	exists, err := configured.UserExists("ghost")
	if err != nil || exists {
		t.Errorf("a configured absent user gave %v, %v", exists, err)
	}
	collaborator, err := configured.IsCollaborator("acme/app", "ghost")
	if err != nil || collaborator {
		t.Errorf("a configured non-collaborator gave %v, %v", collaborator, err)
	}
}

func TestAGitLabUserLookupReturnsEveryMatch(t *testing.T) {
	// GitLab's user lookup is a search and can return several. Picking one
	// here would resolve an ambiguous username silently -- and assigning an
	// approval issue to the wrong person is the failure the gates exist to
	// prevent. The caller decides what "exactly one active match" means.
	writeMock(t, GitLabIssueMockEnv, map[string]any{
		"users": map[string]any{
			"ambiguous": []any{
				map[string]any{"id": 1, "username": "ambiguous", "state": "active"},
				map[string]any{"id": 2, "username": "ambiguous", "state": "blocked"},
			},
		},
	})
	client, err := NewGitLabClient()
	if err != nil {
		t.Fatal(err)
	}
	matches, err := client.ResolveUsername("ambiguous")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Errorf("expected both matches, got %d", len(matches))
	}
}

func TestAnUnavailableIssueLinksAPIIsItsOwnError(t *testing.T) {
	// Reported distinctly so the caller fails closed. Downgrading to "skip the
	// link" would publish an approval issue with no recorded relationship to
	// the gate issue it belongs to, and nothing downstream would know the link
	// was ever intended.
	writeMock(t, GitLabIssueMockEnv, map[string]any{
		"link": map[string]any{
			"7": map[string]any{"error_status": 403, "error": "Issue Links API disabled"},
			"8": map[string]any{"link_type": "relates_to"},
		},
	})
	client, err := NewGitLabClient()
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.CreateIssueLink("acme/app", 7, "acme/app", 9, "relates_to")
	var unavailable *IssueLinksUnavailable
	if !errors.As(err, &unavailable) {
		t.Fatalf("a 403 gave %T (%v), not IssueLinksUnavailable", err, err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("the error does not name the status: %v", err)
	}

	if _, err := client.CreateIssueLink("acme/app", 8, "acme/app", 9, "relates_to"); err != nil {
		t.Errorf("a working instance was refused: %v", err)
	}
}

func TestIssueVerificationNarrowsToWhatIsChecked(t *testing.T) {
	// Read back rather than trusted from the create response: the create says
	// what was sent, this says what the instance stored -- including a label an
	// admin rule stripped or a confidentiality setting a template applied.
	writeMock(t, GitLabIssueMockEnv, map[string]any{
		"verify": map[string]any{
			"7": map[string]any{
				"title": "G1 Intent", "state": "opened",
				"labels":       []any{"agentic-sdlc", "agentic-sdlc-gate-abc"},
				"confidential": true,
				"assignees": []any{
					map[string]any{"username": "zoe"},
					map[string]any{"username": "adam"},
					map[string]any{"username": "zoe"},
				},
				"author":     map[string]any{"username": "sdlc-bot"},
				"references": map[string]any{"full": "acme/app#7"},
				"web_url":    "https://gitlab.example/acme/app/-/issues/7",
			},
		},
	})
	client, err := NewGitLabClient()
	if err != nil {
		t.Fatal(err)
	}
	verification, err := client.FetchIssueVerification("acme/app", 7)
	if err != nil {
		t.Fatal(err)
	}

	if verification.IID != 7 || verification.State != "opened" {
		t.Errorf("the verification lost its identity: %+v", verification)
	}
	if !verification.Confidential {
		t.Error("a confidential issue was reported as public")
	}
	// Assignee usernames are deduplicated and sorted, so a caller comparing
	// them against an expected set is not comparing against GitLab's ordering.
	if strings.Join(verification.AssigneeUsernames, ",") != "adam,zoe" {
		t.Errorf("assignee usernames = %v", verification.AssigneeUsernames)
	}
	if verification.AssigneeCount != 3 {
		t.Errorf("the raw assignee count was deduplicated too: %d", verification.AssigneeCount)
	}
	// The project comes from the full reference when the endpoint omits it.
	if verification.ProjectPath != "acme/app" {
		t.Errorf("project path = %v", verification.ProjectPath)
	}
}

func TestAMissingForgeCLIIsReportedAsMissing(t *testing.T) {
	// The one non-network property of the network path worth pinning: when the
	// tool is not there, the failure has to say so rather than reporting an
	// empty result that reads like "nothing found".
	t.Setenv("PATH", t.TempDir())
	client, err := NewGitHubClient()
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.VerifyIdentity("bot")
	if err == nil {
		t.Fatal("a missing gh was not reported")
	}
	if !strings.Contains(err.Error(), "installed and reachable") {
		t.Errorf("the failure does not point at the missing tool: %v", err)
	}
}

func TestAPathSegmentIsEscapedBeforeItReachesAnAPIURL(t *testing.T) {
	// A repository or login comes from a project's own config, and an
	// unescaped one addresses a different endpoint entirely.
	for _, probe := range []struct{ input, expected string }{
		{"acme/app", "acme%2Fapp"},
		{"group/sub/project", "group%2Fsub%2Fproject"},
		{"../admin", "..%2Fadmin"},
		{"a b", "a%20b"},
		{"tilde~dash-dot.underscore_", "tilde~dash-dot.underscore_"},
		{"café", "caf%C3%A9"},
	} {
		if got := percentEncode(probe.input); got != probe.expected {
			t.Errorf("percentEncode(%q) = %q, want %q", probe.input, got, probe.expected)
		}
	}

	// A repo path keeps its one separator, because the API URL has a slash
	// there -- everything else in it is still escaped.
	for _, probe := range []struct{ input, expected string }{
		{"acme/app", "acme/app"},
		{"acme/../admin", "acme/..%2Fadmin"},
		{"acme/app name", "acme/app%20name"},
	} {
		if got := encodeRepoPath(probe.input); got != probe.expected {
			t.Errorf("encodeRepoPath(%q) = %q, want %q", probe.input, got, probe.expected)
		}
	}
}
