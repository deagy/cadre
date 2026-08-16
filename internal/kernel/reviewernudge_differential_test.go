package kernel

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `publish-reviewer-nudge`, compared with the Python kernel.
//
// The rendered comment is the artifact, and what it does *not* contain matters
// as much as what it does: no `@`-mention, no name for anybody withheld by an
// independence conflict, and none of the run record's free text. Each of those
// is a data-exposure or notification property rather than a formatting one, so
// each has its own test rather than being covered incidentally by the byte
// comparison.

func TestTheReviewerNudgeBodyMatchesThePythonKernel(t *testing.T) {
	for _, probe := range []struct {
		name    string
		prepare func(t *testing.T, root string, mock map[string]any)
		apply   bool
		// unacknowledged drops --i-know-this-is-mocked.
		unacknowledged bool
		expectExit     int
		expectContains string
	}{
		{
			name:           "reviewers worth suggesting",
			expectContains: "Suggested reviewers:",
		},
		{
			// The PR author holds authority, so a login is withheld -- counted
			// in the comment, never named.
			name: "a withheld reviewer is counted, not named",
			prepare: func(t *testing.T, root string, mock map[string]any) {
				pullRequest, _ := mock["pr"].(map[string]any)
				pullRequest["user"] = map[string]any{"login": "engineering-lead"}
			},
			expectContains: "not shown due to a gate-independence conflict",
		},
		{
			// Everybody has already reviewed the current head, so there is
			// nothing to nudge about and the comment says so plainly.
			name: "nobody to nudge",
			prepare: func(t *testing.T, root string, mock map[string]any) {
				mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
					func(document map[string]any) {
						// Narrow the run to a single gate, so one review
						// covers everything.
						gates := listOf(document["lifecycle_gates"])
						for index, raw := range gates {
							gate, _ := raw.(map[string]any)
							if index > 0 {
								gate["applicability"] = "not-applicable"
							}
						}
					})
			},
			expectContains: "",
		},
		{
			name:  "posting the comment",
			apply: true, expectContains: `"action": "create"`,
		},
		{
			name:  "a mocked apply with no acknowledgement",
			apply: true, unacknowledged: true, expectExit: 1,
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			freezeClock(t)
			root, manifest := reviewerFixture(t)
			mock := gitHubReviewerMock()
			if probe.prepare != nil {
				probe.prepare(t, root, mock)
			}
			writeForgeMock(t, GitHubReadMockEnv, mock)
			writeForgeMock(t, "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE", gitHubReviewsMock())
			writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list":     map[string]any{}, "create": map[string]any{}, "fetch": map[string]any{},
			})

			base := []string{"--provider", manifest, "publish-reviewer-nudge",
				"--root", root, "--task-id", decideTask, "--repo", "acme/app",
				"--pr", "3", "--as-bot", "sdlc-bot", "--allow-classification", "internal"}

			// A dry run first, to learn the body the apply will verify
			// against -- the fetch echo has to match it exactly.
			if probe.apply {
				var probeOut, probeErr bytes.Buffer
				if code := Run(base, &probeOut, &probeErr); code != 0 {
					t.Fatalf("the dry run that establishes the body failed: %s", probeErr.String())
				}
				var summary struct {
					Body string `json:"body"`
				}
				if err := json.Unmarshal(probeOut.Bytes(), &summary); err != nil {
					t.Fatal(err)
				}
				writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
					"identity": map[string]any{"login": "sdlc-bot"},
					"list":     map[string]any{"acme/app#3": map[string]any{"1": []any{}}},
					"create":   map[string]any{"acme/app#3": map[string]any{"id": 77}},
					"fetch": map[string]any{"77": map[string]any{
						"id": 77, "body": summary.Body,
						"user": map[string]any{"login": "sdlc-bot"}}},
				})
			}

			args := base
			if probe.apply {
				args = append(append([]string{}, base...), "--apply")
				if !probe.unacknowledged {
					args = append(args, "--i-know-this-is-mocked")
				}
			}

			pythonRoot := filepath.Join(t.TempDir(), "python")
			goRoot := filepath.Join(t.TempDir(), "go")
			for _, target := range []string{pythonRoot, goRoot} {
				if err := copyTree(root, target); err != nil {
					t.Fatal(err)
				}
			}

			pythonCode, pythonOutput := runPythonGateStatus(t, replaceRoot(args, root, pythonRoot))
			var stdout, stderr bytes.Buffer
			goCode := Run(replaceRoot(args, root, goRoot), &stdout, &stderr)
			goOutput := stdout.String() + stderr.String()

			if pythonCode != probe.expectExit || goCode != probe.expectExit {
				t.Fatalf("expected exit %d -- python %d, go %d\npython: %s\ngo: %s",
					probe.expectExit, pythonCode, goCode, pythonOutput, goOutput)
			}
			if pythonOutput != goOutput {
				t.Errorf("output differs.\npython:\n%s\ngo:\n%s", pythonOutput, goOutput)
			}
			if probe.expectContains != "" && !strings.Contains(goOutput, probe.expectContains) {
				t.Errorf("the comment does not contain %q:\n%s", probe.expectContains, goOutput)
			}

			// And the ledger, which is what an operator reads afterwards.
			name := filepath.Join(Overlay, "runs", decideTask, "reviewer-nudge-github.json")
			pythonLedger, pythonExists := readIfPresent(filepath.Join(pythonRoot, name))
			goLedger, goExists := readIfPresent(filepath.Join(goRoot, name))
			if pythonExists != goExists {
				t.Errorf("%s: python wrote it=%v, go wrote it=%v", name, pythonExists, goExists)
			} else if pythonExists && pythonLedger != goLedger {
				t.Errorf("%s differs.\npython:\n%s\ngo:\n%s", name, pythonLedger, goLedger)
			}
		})
	}
}

// The invariants, stated without reference to the Python kernel.

func nudgeReport(t *testing.T, root, manifest string) *ReviewerReport {
	t.Helper()
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
	return report
}

func TestTheNudgeNeverMentionsAnyone(t *testing.T) {
	// Load-bearing rather than stylistic: an @-mention in a posted comment
	// does trigger a GitHub notification, which would directly contradict the
	// advisory paragraph's claim that nobody has been notified.
	freezeClock(t)
	root, manifest := reviewerFixture(t)
	writeForgeMock(t, GitHubReadMockEnv, gitHubReviewerMock())
	writeForgeMock(t, "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE", gitHubReviewsMock())

	report := nudgeReport(t, root, manifest)
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	body := RenderReviewerNudgeBody(decideTask, report, contracts, frozenMoment)

	// Self-vacuity: the comment has to actually name somebody, or "no
	// mentions" is trivially true.
	if !strings.Contains(body, "Suggested reviewers:") {
		t.Fatalf("nobody was suggested; this test would prove nothing:\n%s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		// The advisory paragraph talks *about* @-mentions, which is the point
		// of it; what must not appear is one attached to a login.
		if strings.Contains(line, "@-mention") || strings.Contains(line, "`@`-mention") {
			continue
		}
		if strings.Contains(line, "@") {
			t.Errorf("a line carries an @: %q", line)
		}
	}
}

func TestTheNudgeNeverNamesAWithheldReviewer(t *testing.T) {
	// Saying in a public comment that a specific person cannot review because
	// they authored the work is a data-exposure regression from the local
	// report, where that reasoning already lives.
	freezeClock(t)
	root, manifest := reviewerFixture(t)
	mock := gitHubReviewerMock()
	pullRequest, _ := mock["pr"].(map[string]any)
	pullRequest["user"] = map[string]any{"login": "engineering-lead"}
	writeForgeMock(t, GitHubReadMockEnv, mock)
	writeForgeMock(t, "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE", gitHubReviewsMock())

	report := nudgeReport(t, root, manifest)
	withheld := ""
	for _, entry := range report.Reviewers {
		if entry.Classification == withheldClassification {
			withheld = entry.Login
		}
	}
	if withheld == "" {
		t.Fatal("nobody was withheld; this test would prove nothing")
	}

	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	body := RenderReviewerNudgeBody(decideTask, report, contracts, frozenMoment)
	if strings.Contains(body, withheld) {
		t.Errorf("the withheld login %q is named in the comment:\n%s", withheld, body)
	}
	if !strings.Contains(body, "not shown due to a gate-independence conflict") {
		t.Errorf("the withheld reviewer is not even counted:\n%s", body)
	}
	// And the reason is not there either, in any form.
	for _, leaked := range []string{"pr-author-conflict", "self-approval", "actor-is-reviewer"} {
		if strings.Contains(body, leaked) {
			t.Errorf("the conflict reason %q leaked into the comment", leaked)
		}
	}
}

func TestTheNudgeRendersNoProjectFreeText(t *testing.T) {
	// The run record's `role` and `rationale` are free-text schema strings,
	// not closed enums. They are never rendered here, which is why this module
	// needs no sanitizer -- `authority_type` stands in as the coarser signal.
	freezeClock(t)
	root, manifest := reviewerFixture(t)
	mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
		func(document map[string]any) {
			for _, raw := range listOf(document["lifecycle_gates"]) {
				gate, _ := raw.(map[string]any)
				for _, requirementRaw := range listOf(gate["authority_requirements"]) {
					requirement, _ := requirementRaw.(map[string]any)
					requirement["role"] = "SECRET-ROLE-LABEL"
					requirement["rationale"] = "SECRET-RATIONALE-TEXT"
				}
			}
		})
	writeForgeMock(t, GitHubReadMockEnv, gitHubReviewerMock())
	writeForgeMock(t, "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE", gitHubReviewsMock())

	report := nudgeReport(t, root, manifest)
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	body := RenderReviewerNudgeBody(decideTask, report, contracts, frozenMoment)

	for _, leaked := range []string{"SECRET-ROLE-LABEL", "SECRET-RATIONALE-TEXT"} {
		if strings.Contains(body, leaked) {
			t.Errorf("the comment leaked %q:\n%s", leaked, body)
		}
	}
	// The closed-enum stand-in is there, or this passes on a render that
	// omitted the motivation lines entirely.
	if !strings.Contains(body, "(human-approver)") {
		t.Errorf("the authority type is missing from the motivations:\n%s", body)
	}
}

func TestTheNudgeAdvisoryMakesItsOwnClaim(t *testing.T) {
	// A different claim from the gate-status advisory, and it needed its own
	// wording: that one says "not approval evidence", this one says "nobody
	// has been asked or notified". Reusing either for the other would put a
	// true-but-wrong sentence on a comment.
	freezeClock(t)
	root, manifest := reviewerFixture(t)
	writeForgeMock(t, GitHubReadMockEnv, gitHubReviewerMock())
	writeForgeMock(t, "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE", gitHubReviewsMock())
	report := nudgeReport(t, root, manifest)
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	body := RenderReviewerNudgeBody(decideTask, report, contracts, frozenMoment)

	for _, required := range []string{
		"This is a suggestion, not a review request",
		"has not requested a review from anyone",
		"have not been notified",
		"Not a review request",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("the nudge advisory does not say %q:\n%s", required, body)
		}
	}
	if strings.Contains(body, "Not approval evidence") {
		t.Error("the nudge reuses the gate-status advisory's headline claim")
	}
}

func TestTheNudgeMarkerIsItsOwnFamily(t *testing.T) {
	// Three marker families share one comment stream. If two collided, each
	// command would keep overwriting the other's comment, forever.
	nudge := ComputeNudgeMarker("TASK-1")
	status := ComputeStatusMarker("TASK-1")
	if nudge == status {
		t.Error("the nudge and gate-status markers collide for the same task")
	}
	if nudge == TaskHash("TASK-1") {
		t.Error("the nudge marker is the displayed task hash")
	}
	if nudge != hexSHA256([]byte("reviewer-nudge\x00TASK-1"))[:16] {
		t.Error("the marker formula changed; every existing nudge stops matching")
	}

	// And neither pattern matches the other's comment.
	if nudgeMarkerPattern(nudge).MatchString(
		"<!-- agentic-sdlc:gate-status:v1:" + status + " -->") {
		t.Error("the nudge pattern matched a gate-status comment")
	}
	if markerPattern(status).MatchString(
		"<!-- agentic-sdlc:reviewer-nudge:v1:" + nudge + " -->") {
		t.Error("the gate-status pattern matched a nudge comment")
	}
	// Any template version still matches its own family.
	for _, version := range []string{"v1", "v2", "v9"} {
		if !nudgeMarkerPattern(nudge).MatchString(
			"<!-- agentic-sdlc:reviewer-nudge:" + version + ":" + nudge + " -->") {
			t.Errorf("a %s nudge comment did not match", version)
		}
	}
}

func TestOnlyNudgeableClassificationsAreNamed(t *testing.T) {
	// The whole data-minimisation rule in one place: two classifications are
	// named, one is counted, and the rest are absent -- because there is
	// nothing a reader of a PR comment could do about them.
	report := &ReviewerReport{
		Repo: "acme/app", PullRequest: 3,
		Reviewers: []ReviewerEntry{
			{Login: "to-ask", Classification: "to-request"},
			{Login: "went-stale", Classification: "review-stale"},
			{Login: "conflicted", Classification: "withheld-conflict"},
			{Login: "already-asked", Classification: "already-requested"},
			{Login: "already-done", Classification: "already-reviewed"},
			{Login: "no-such-account", Classification: "github-user-unresolved"},
			{Login: "outsider", Classification: "not-a-collaborator"},
		},
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	body := RenderReviewerNudgeBody("TASK-1", report, contracts, frozenMoment)

	for _, named := range []string{"to-ask", "went-stale"} {
		if !strings.Contains(body, named) {
			t.Errorf("%q should be suggested and is not:\n%s", named, body)
		}
	}
	for _, absent := range []string{
		"conflicted", "already-asked", "already-done", "no-such-account", "outsider",
	} {
		if strings.Contains(body, absent) {
			t.Errorf("%q should not appear and does:\n%s", absent, body)
		}
	}
	if !strings.Contains(body, "1 additional reviewer not shown") {
		t.Errorf("the single withheld reviewer is not counted, or is pluralised wrongly:\n%s", body)
	}
}

func TestTheNudgeReusesTheReviewerReport(t *testing.T) {
	// Not a style rule. The report call owns identity verification, the PR
	// fetch and validation, and the requested-reviewers/reviews/user-exists/
	// collaborator checks; a second path through any of those is a second
	// thing to keep in step. So a report failure has to surface from here
	// unchanged rather than being re-derived or swallowed.
	freezeClock(t)
	root, manifest := reviewerFixture(t)
	writeForgeMock(t, GitHubReadMockEnv, gitHubReviewerMock())
	writeForgeMock(t, "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE", gitHubReviewsMock())
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}

	_, err := registry.PublishReviewerNudge(ReviewerNudgeRequest{
		Root: root, TaskID: decideTask, Repo: "acme/app", PullRequest: 3,
		AsBot: "sdlc-bot", AllowClassification: "the-wrong-classification",
	})
	if err == nil {
		t.Fatal("a classification mismatch was not refused")
	}
	if !strings.Contains(err.Error(), "--allow-classification") {
		t.Errorf("the refusal did not come from the reviewer report: %v", err)
	}
}

func TestTheNudgeLedgerIsItsOwnFile(t *testing.T) {
	// Three ledger families live in one run directory. A shared filename would
	// let one publication's record overwrite another's.
	root := t.TempDir()
	nudge, err := LedgerPath(root, Overlay, "TASK-1", "reviewer-nudge-github.json")
	if err != nil {
		t.Fatal(err)
	}
	status, err := LedgerPath(root, Overlay, "TASK-1", "gate-status-github.json")
	if err != nil {
		t.Fatal(err)
	}
	issues, err := LedgerPath(root, Overlay, "TASK-1", "gate-issues-github.json")
	if err != nil {
		t.Fatal(err)
	}
	if nudge == status || nudge == issues || status == issues {
		t.Errorf("two ledger families share a path: %s, %s, %s", nudge, status, issues)
	}
	if filepath.Dir(nudge) != filepath.Dir(status) {
		t.Error("the ledgers are not in the same run directory, so this test checks nothing")
	}
	// The reader returns an empty ledger rather than an error for an absent
	// file, which is what makes `list-reviewer-nudge` safe to run first.
	if _, err := os.Stat(nudge); err == nil {
		t.Fatal("the fixture already has a ledger")
	}
	ledger, err := ReadReviewerNudgeLedger(root, "TASK-1")
	if err != nil {
		t.Fatalf("reading an absent nudge ledger: %v", err)
	}
	if ledger == nil {
		t.Error("an absent ledger read as nil rather than as an empty one")
	}
}
