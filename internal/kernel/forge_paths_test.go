package kernel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The forge-facing commands, asserted directly rather than by comparison.
//
// These are the paths that talk to GitHub and GitLab, and every one of them
// was reachable in tests only through a differential against the Python
// kernel. Measured, that left the GitLab reviewer report, the gate-status
// publisher, the GitHub status client and the forge readers at zero coverage
// the moment the comparison went away -- which is to say the code that decides
// what this kernel says to a forge had no test of its own.
//
// None of these reach a network. Every forge call in this kernel answers from
// a mock file when one is configured, which is the property that makes the
// whole surface testable; a test that needed credentials would not exist.

// The four things worth asserting about a command that talks to a forge, and
// the reasons they are not obvious:
//
//   - a dry run prints a plan and calls nothing that writes;
//   - a report says which reviewers it withheld and why, because an operator
//     acting on a partial report needs to know it was partial;
//   - a draft merge request is not something to request review on;
//   - an identity that does not match the bot is refused, because publishing
//     as somebody else is the failure the whole gate mechanism exists around.

// gitLabReviewerWorld configures *both* mocks the reviewer command reads.
//
// Configuring only one lets the command fall through to a real `glab`, which
// fails for want of a binary -- and a test asserting only "this did not
// succeed" then passes for the wrong reason. Two of these did.
func gitLabReviewerWorld(t *testing.T, adjust func(mock map[string]any)) {
	t.Helper()
	mock := gitLabReviewerMock()
	if adjust != nil {
		adjust(mock)
	}
	writeForgeMock(t, GitLabIssueMockEnv, mock)
	writeForgeMock(t, GitLabApprovalsMockEnv, map[string]any{
		"sha": "def456", "updated_at": "2026-08-15T09:00:00Z",
		"approved_by": []any{
			map[string]any{"user": map[string]any{"id": 1, "username": "product-owner"}},
		},
	})
}

func TestTheGitLabReviewerReportNamesWhatItWouldDo(t *testing.T) {
	root, manifest := reviewerFixture(t)
	gitLabReviewerWorld(t, nil)

	code, output := runCLI(t, "--provider", manifest, "request-gate-reviewers-gitlab",
		"--root", root, "--task-id", decideTask, "--project-path", "acme/app",
		"--mr-iid", "5", "--as-bot", "sdlc-bot", "--allow-classification", "internal")
	if code != 0 && code != 2 {
		t.Fatalf("the report failed: exit %d\n%s", code, output)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, output)
	}
	for _, key := range []string{
		"project_path", "mr_iid", "mr_head_sha", "mr_author_username",
		"as_bot_username", "gate_ids", "reviewers",
	} {
		if _, present := report[key]; !present {
			t.Errorf("the report has no %s:\n%s", key, output)
		}
	}
	if report["mr_iid"] != float64(5) {
		t.Errorf("the report names merge request %v, not the one asked for", report["mr_iid"])
	}
	// Every reviewer carries a classification and the gate that motivated
	// them. A report naming people without saying why is one an operator
	// cannot act on.
	for _, raw := range listOf(report["reviewers"]) {
		entry, _ := raw.(map[string]any)
		if entry["classification"] == nil || entry["username"] == nil {
			t.Errorf("a reviewer entry says neither who nor why: %v", entry)
		}
	}
}

func TestADraftMergeRequestIsReportedRatherThanHidden(t *testing.T) {
	// This command reports; it does not decide. A draft is still worth a
	// report -- the reviewers are the same people -- but an operator acting on
	// it needs to know the change is one its author has called unfinished, so
	// the state is carried rather than dropped.
	root, manifest := reviewerFixture(t)
	gitLabReviewerWorld(t, func(mock map[string]any) {
		mergeRequest, _ := mock["mr"].(map[string]any)
		mergeRequest["draft"] = true
	})

	code, output := runCLI(t, "--provider", manifest, "request-gate-reviewers-gitlab",
		"--root", root, "--task-id", decideTask, "--project-path", "acme/app",
		"--mr-iid", "5", "--as-bot", "sdlc-bot", "--allow-classification", "internal")
	if code != 0 && code != 2 {
		t.Fatalf("a draft merge request failed the report: exit %d\n%s", code, output)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, output)
	}
	if report["mr_draft"] != true {
		t.Errorf("the report does not say the merge request is a draft:\n%s", output)
	}
}

func TestAMergeRequestFromAnotherProjectIsRefused(t *testing.T) {
	// The project path is what the operator named; the merge request carries
	// its own. A mismatch means the iid resolved somewhere else, and reporting
	// on it would attach this task's review to a stranger's change.
	root, manifest := reviewerFixture(t)
	gitLabReviewerWorld(t, func(mock map[string]any) {
		mergeRequest, _ := mock["mr"].(map[string]any)
		mergeRequest["references"] = map[string]any{"full": "someone/else!5"}
	})

	code, output := runCLI(t, "--provider", manifest, "request-gate-reviewers-gitlab",
		"--root", root, "--task-id", decideTask, "--project-path", "acme/app",
		"--mr-iid", "5", "--as-bot", "sdlc-bot", "--allow-classification", "internal")
	if code == 0 {
		t.Errorf("a merge request from another project was accepted:\n%s", output)
	}
	// The reason, not just the refusal: this test passed for want of a `glab`
	// binary before the mocks were complete.
	if !strings.Contains(output, "someone/else") && !strings.Contains(output, "acme/app") {
		t.Errorf("the refusal does not name the projects it compared:\n%s", output)
	}
}

func TestAReviewerStateIsClassifiedByWhatTheyHaveAlreadyDone(t *testing.T) {
	// What decides whether a reviewer is asked again. The priority matters:
	// somebody who has approved must not be re-requested just because they are
	// also still listed as a reviewer, which is the ordinary state on GitLab
	// after an approval.
	approved := map[string]bool{"alice": true}
	reviewers := map[string]bool{"alice": true, "bob": true}
	for _, probe := range []struct {
		name     string
		username string
		want     string
	}{
		{"already approved, and still listed", "Alice", "already-approved"},
		{"listed but has not decided", "bob", "already-reviewer"},
		{"nobody has asked yet", "carol", "to-request"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			if got := ClassifyReviewerUsername(probe.username, reviewers, approved); got != probe.want {
				t.Errorf("classified %q as %q, wanted %q", probe.username, got, probe.want)
			}
		})
	}
}

func TestAUsernameResolvesToExactlyOneActiveAccountOrNone(t *testing.T) {
	// GitLab's user lookup is a search, so it can answer with several people
	// or with an account nobody can assign to. Picking one would resolve an
	// ambiguous username silently, and asking the wrong person to review is
	// the failure the gates exist to prevent.
	for _, probe := range []struct {
		name     string
		resolved []any
		want     int
	}{
		{"one active account", []any{
			map[string]any{"id": 1, "username": "alice", "state": "active"}}, 1},
		{"nobody at all", []any{}, 0},
		{"two people answering to one name", []any{
			map[string]any{"id": 1, "username": "alice", "state": "active"},
			map[string]any{"id": 2, "username": "alice", "state": "active"}}, 2},
		{"an account nobody can assign to", []any{
			map[string]any{"id": 3, "username": "alice", "state": "blocked"}}, 0},
	} {
		t.Run(probe.name, func(t *testing.T) {
			writeForgeMock(t, GitLabIssueMockEnv, map[string]any{
				"identity": map[string]any{"username": "sdlc-bot"},
				"users":    map[string]any{"alice": probe.resolved},
			})
			client, err := NewGitLabClient()
			if err != nil {
				t.Fatal(err)
			}
			active, err := resolveActiveUsernames(client, "alice")
			if err != nil {
				t.Fatalf("resolving: %v", err)
			}
			if len(active) != probe.want {
				t.Errorf("resolved to %d accounts, wanted %d", len(active), probe.want)
			}
		})
	}
}

func TestTheGateStatusCommandRefusesAForgeTargetItCannotAddress(t *testing.T) {
	// Each forge names its target differently, and taking the wrong one would
	// post a task's status onto a number that means something else there.
	root, manifest := decidableProject(t)
	for _, probe := range []struct {
		name string
		args []string
	}{
		{"github with a merge request", []string{"--forge", "github", "--mr-iid", "5"}},
		{"gitlab with a pull request", []string{"--forge", "gitlab", "--pr", "5"}},
		{"github with nothing at all", []string{"--forge", "github"}},
		{"gitlab with nothing at all", []string{"--forge", "gitlab"}},
	} {
		t.Run(probe.name, func(t *testing.T) {
			args := []string{"--provider", manifest, "publish-gate-status",
				"--root", root, "--task-id", decideTask, "--as-bot", "b",
				"--allow-classification", "internal"}
			args = append(args, probe.args...)
			code, output := runCLI(t, args...)
			if code == 0 {
				t.Errorf("an unaddressable target was accepted:\n%s", output)
			}
		})
	}
}

func TestTheStatusPublisherDecidesWhatToDoWithWhatItFinds(t *testing.T) {
	// The one decision that can damage somebody else's work. Four outcomes,
	// and the two that refuse are the point: a comment this kernel did not
	// write is never edited, and two matching comments are never disambiguated
	// by guessing.
	bot := "sdlc-bot"
	rendered := "the body this run would post"
	mine := NormalizedComment{ID: 1, Author: bot, Body: rendered}
	for _, probe := range []struct {
		name       string
		matches    []NormalizedComment
		wantAction string
		wantReason string
	}{
		{"nothing there yet", nil, "create", ""},
		{"its own comment, unchanged", []NormalizedComment{mine}, "unchanged", ""},
		{"its own comment, out of date",
			[]NormalizedComment{{ID: 1, Author: bot, Body: "something older"}}, "update", ""},
		{"a comment somebody else wrote",
			[]NormalizedComment{{ID: 1, Author: "a-person", Body: rendered}},
			"blocked", "foreign_author"},
		{"two comments that both match",
			[]NormalizedComment{mine, {ID: 2, Author: bot, Body: rendered}},
			"blocked", "multiple_matches"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			action, reason, _ := ClassifyStatusComment(probe.matches, bot, rendered)
			if action != probe.wantAction || reason != probe.wantReason {
				t.Errorf("decided %q/%q, wanted %q/%q",
					action, reason, probe.wantAction, probe.wantReason)
			}
		})
	}
}

func TestAnAuthorComparisonIgnoresCaseButNotIdentity(t *testing.T) {
	// A forge may echo a login with different capitalisation than the one it
	// was authenticated as. Treating that as a foreign author would refuse to
	// update this kernel's own comment forever.
	rendered := "body"
	action, reason, _ := ClassifyStatusComment(
		[]NormalizedComment{{ID: 1, Author: "SDLC-Bot", Body: "older"}}, "sdlc-bot", rendered)
	if action != "update" {
		t.Errorf("a differently-cased bot login was treated as %q/%q", action, reason)
	}
}

func TestTheForgeReadersRefuseAResponseTheyCannotUse(t *testing.T) {
	// These read a forge and hand the result to something that records
	// evidence. A malformed response has to stop there rather than become an
	// evidence record nobody can trace back.
	t.Run("an issue with a state neither forge uses", func(t *testing.T) {
		writeForgeMock(t, GitLabIssueFetchMock, map[string]any{
			"title": "Something", "state": "merged"})
		if _, err := FetchGitLabIssue("acme/app", 7); err == nil {
			t.Error("an unrecognised issue state was accepted")
		}
	})
	t.Run("a reviews response that is not a list", func(t *testing.T) {
		writeForgeMock(t, GitHubReviewsMockEnv, map[string]any{"reviews": []any{}})
		if _, err := FetchGitHubPullRequestReviews("acme/app", 1); err == nil {
			t.Error("an object was accepted where an array was required")
		}
	})
	t.Run("a reviews list holding something that is not a review", func(t *testing.T) {
		writeForgeMock(t, GitHubReviewsMockEnv, []any{"not a review"})
		if _, err := FetchGitHubPullRequestReviews("acme/app", 1); err == nil {
			t.Error("a list of strings was accepted as a list of reviews")
		}
	})
	t.Run("an approvals response that is not an object", func(t *testing.T) {
		writeForgeMock(t, GitLabApprovalsMockEnv, []any{})
		if _, err := FetchGitLabMergeRequestApprovals("acme/app", 5); err == nil {
			t.Error("an array was accepted where an object was required")
		}
	})
	t.Run("a GitHub issue in an unrecognised state", func(t *testing.T) {
		writeForgeMock(t, GitHubIssueFetchMock, map[string]any{
			"title": "Something", "state": "opened"})
		if _, err := FetchGitHubIssue("acme/app", 7); err == nil {
			t.Error("GitLab's word for open was accepted on GitHub's endpoint")
		}
	})
}

func TestTheForgeReadersAcceptWhatAForgeActuallyReturns(t *testing.T) {
	// The refusals above are only meaningful if the ordinary case passes.
	t.Run("a GitLab issue", func(t *testing.T) {
		writeForgeMock(t, GitLabIssueFetchMock, map[string]any{
			"title": "Ship it", "state": "opened",
			"web_url": "https://gitlab.com/acme/app/-/issues/7"})
		issue, err := FetchGitLabIssue("acme/app", 7)
		if err != nil {
			t.Fatal(err)
		}
		if issue.Title != "Ship it" || issue.State != "opened" || issue.Number != 7 {
			t.Errorf("read back %+v", issue)
		}
	})
	t.Run("a GitHub issue, normalised to the same shape", func(t *testing.T) {
		writeForgeMock(t, GitHubIssueFetchMock, map[string]any{
			"title": "Ship it", "state": "open",
			"html_url": "https://github.com/acme/app/issues/7"})
		issue, err := FetchGitHubIssue("acme/app", 7)
		if err != nil {
			t.Fatal(err)
		}
		// html_url on GitHub, web_url on GitLab, one field downstream.
		if issue.WebURL != "https://github.com/acme/app/issues/7" {
			t.Errorf("the browser link was not normalised: %+v", issue)
		}
	})
	t.Run("merge request approvals, minimised", func(t *testing.T) {
		writeForgeMock(t, GitLabApprovalsMockEnv, map[string]any{
			"sha": "abc", "updated_at": "2026-08-15T09:00:00Z",
			"approved_by": []any{map[string]any{
				"user": map[string]any{"id": 9, "username": "alice", "name": "A Person"}}},
		})
		approvals, err := FetchGitLabMergeRequestApprovals("acme/app", 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(approvals) != 1 {
			t.Fatalf("expected one approval, got %d", len(approvals))
		}
		record, _ := approvals[0].(map[string]any)
		if _, present := record["name"]; present {
			t.Errorf("the approver's name survived the read: %v", record)
		}
	})
}

func TestTheGitHubStatusClientAnswersFromItsMock(t *testing.T) {
	// The client that posts and edits a status comment. Exercised through its
	// mock because every one of these calls writes, and the alternative is a
	// live repository.
	writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
		"identity": map[string]any{"login": "sdlc-bot"},
		"list": map[string]any{"acme/app#1": map[string]any{
			"1": []any{map[string]any{"id": 11, "body": "existing",
				"user": map[string]any{"login": "sdlc-bot"}}},
		}},
		"create": map[string]any{"acme/app#1": map[string]any{"id": 12}},
		"fetch":  map[string]any{"12": map[string]any{"id": 12, "body": "posted"}},
	})
	client, err := NewGitHubStatusClient()
	if err != nil {
		t.Fatal(err)
	}
	if !client.Mocked() {
		t.Fatal("the client did not notice its mock, so this test would reach GitHub")
	}

	login, err := client.VerifyIdentity("sdlc-bot")
	if err != nil {
		t.Fatalf("verifying identity: %v", err)
	}
	if login != "sdlc-bot" {
		t.Errorf("verified as %q", login)
	}
	// The check that matters: publishing as somebody other than the identity
	// the operator named is the failure the gates exist around.
	if _, err := client.VerifyIdentity("somebody-else"); err == nil {
		t.Error("the client accepted an identity it had not authenticated as")
	}

	comments, err := client.ListComments("acme/app", 1, 1, 100)
	if err != nil {
		t.Fatalf("listing comments: %v", err)
	}
	if len(comments) != 1 {
		t.Errorf("read %d comments, expected 1", len(comments))
	}
	// A page nobody wrote is empty rather than an error: pagination stops by
	// reading past the end.
	empty, err := client.ListComments("acme/app", 1, 2, 100)
	if err != nil {
		t.Fatalf("listing a page past the end: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("a page past the end returned %d comments", len(empty))
	}
}

func TestTheLedgerReadersReturnAnEmptyLedgerRatherThanFailing(t *testing.T) {
	// A task nobody has published for has no ledger, and that is an ordinary
	// state rather than an error: an operator asking "what was published"
	// before anything was should get "nothing", not a stack trace.
	root, _ := decidableProject(t)
	for _, probe := range []struct {
		name string
		read func(string, string) (any, error)
	}{
		{"gitlab gate issues", ReadGateIssuesLedger},
		{"github gate issues", ReadGitHubGateIssuesLedger},
		{"gate status, both forges", ReadGateStatusLedgers},
		{"reviewer nudge", ReadReviewerNudgeLedger},
	} {
		t.Run(probe.name, func(t *testing.T) {
			value, err := probe.read(root, decideTask)
			if err != nil {
				t.Fatalf("reading a ledger that does not exist: %v", err)
			}
			if value == nil {
				t.Error("returned nothing at all rather than an empty ledger")
			}
		})
	}
}

func TestAnExtensionThatIsNotThereIsNamedRatherThanIgnored(t *testing.T) {
	// `init --extension` names something the operator expects to be applied.
	// Carrying on without it produces a project that looks initialised and is
	// missing the thing they asked for.
	root := t.TempDir()
	code, output := runCLI(t, "init", "--root", root, "--extension", "not-an-extension")
	if code == 0 {
		t.Errorf("an unknown extension was ignored:\n%s", output)
	}
	if !strings.Contains(output, "not-an-extension") {
		t.Errorf("the failure does not name the extension:\n%s", output)
	}
}

func TestAGateStatusLedgerRecordsWhatWasPublished(t *testing.T) {
	// The ledger is what an operator reads to find out what this kernel put on
	// a pull request. An apply that published and recorded nothing would leave
	// them with a comment they cannot trace.
	freezeClock(t)
	root, manifest := decidableProject(t)
	writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
		"identity": map[string]any{"login": "sdlc-bot"},
		"comments": []any{},
		"list":     map[string]any{},
		"create":   map[string]any{"acme/app#1": map[string]any{"id": 4242}},
		"fetch": map[string]any{"4242": map[string]any{"id": 4242,
			"user": map[string]any{"login": "sdlc-bot"}}},
	})
	writeForgeMock(t, GitHubReadMockEnv, map[string]any{
		"identity": map[string]any{"login": "sdlc-bot"},
		"pulls": map[string]any{"acme/app#1": map[string]any{
			"number": 1, "state": "open", "draft": false,
			"head": map[string]any{"sha": "abc123"},
			"base": map[string]any{"repo": map[string]any{"full_name": "acme/app"}},
			"user": map[string]any{"login": "engineering-lead"}}},
	})

	code, output := runCLI(t, "--provider", manifest, "publish-gate-status",
		"--root", root, "--task-id", decideTask, "--forge", "github",
		"--repo", "acme/app", "--pr", "1", "--as-bot", "sdlc-bot",
		"--allow-classification", "internal", "--apply", "--i-know-this-is-mocked")
	if code != 0 && code != 2 {
		t.Fatalf("publishing failed: exit %d\n%s", code, output)
	}

	ledger := filepath.Join(root, Overlay, "runs", decideTask, "gate-status-github.json")
	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("no ledger was written: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(string(data), "4242") {
		t.Errorf("the ledger does not record the comment that was created:\n%s", data)
	}
}
