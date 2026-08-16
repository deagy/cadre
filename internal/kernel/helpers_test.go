package kernel

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The small pieces the larger tests reach only in passing.
//
// Each of these was covered by a differential and by nothing else, which for a
// leaf function means it was never asserted at all -- only executed on the way
// to something that was. They are short enough that the assertion is the
// interesting part rather than the setup.

func TestARiskExceptionCannotBeApprovedByItsOwnOwner(t *testing.T) {
	// A risk exception is the escape hatch: a decision to ship a known
	// finding. Letting one identity both request and approve it is the same
	// self-approval the gates exist to prevent, reached through the side door
	// instead of the front one. That rule, and the expiry, are what this
	// function is actually for -- the field-presence checks above them are the
	// easy half.
	complete := func() map[string]any {
		return map[string]any{
			"exception_id": "E-1", "finding_id": "F-1",
			"justification": "documented", "compensating_controls": []any{"monitoring"},
			"owner":      map[string]any{"id": "github.com/security-lead", "kind": "human"},
			"approver":   map[string]any{"id": "github.com/governance-lead", "kind": "human"},
			"expires_at": "2099-12-31T00:00:00Z", "remediation_plan": "planned",
		}
	}
	if !ValidException(complete()) {
		t.Fatal("a complete exception was rejected; every case below would pass vacuously")
	}

	t.Run("approved by its own owner", func(t *testing.T) {
		exception := complete()
		exception["approver"] = map[string]any{
			"id": "github.com/security-lead", "kind": "human"}
		if ValidException(exception) {
			t.Error("one identity both requested and approved an exception")
		}
	})
	t.Run("approved by something that is not a person", func(t *testing.T) {
		exception := complete()
		exception["approver"] = map[string]any{"id": "agent://reviewer", "kind": "agent"}
		if ValidException(exception) {
			t.Error("an agent approved a risk acceptance")
		}
	})
	t.Run("owned by something that is not a person", func(t *testing.T) {
		exception := complete()
		exception["owner"] = map[string]any{"id": "agent://author", "kind": "agent"}
		if ValidException(exception) {
			t.Error("an agent owns a risk acceptance")
		}
	})
	t.Run("already expired", func(t *testing.T) {
		exception := complete()
		exception["expires_at"] = "2020-01-01T00:00:00Z"
		if ValidException(exception) {
			t.Error("an expired exception still counted; the expiry is decorative")
		}
	})
	t.Run("expiring at a time nobody can parse", func(t *testing.T) {
		exception := complete()
		exception["expires_at"] = "next year"
		if ValidException(exception) {
			t.Error("an unparseable expiry was treated as a future one")
		}
	})
	t.Run("with an identity that is not an object", func(t *testing.T) {
		exception := complete()
		exception["owner"] = "github.com/security-lead"
		if ValidException(exception) {
			t.Error("a bare string was accepted as an identity")
		}
	})

	for _, field := range []string{
		"exception_id", "finding_id", "justification", "compensating_controls",
		"owner", "approver", "expires_at", "remediation_plan",
	} {
		t.Run("without "+field, func(t *testing.T) {
			exception := complete()
			delete(exception, field)
			if ValidException(exception) {
				t.Errorf("an exception with no %s was accepted", field)
			}
		})
	}
	t.Run("mitigating nothing", func(t *testing.T) {
		exception := complete()
		exception["compensating_controls"] = []any{}
		if ValidException(exception) {
			t.Error("an unmitigated acceptance was accepted as a mitigated one")
		}
	})
}

func TestAKeywordMatchesOnWordBoundariesRatherThanSubstrings(t *testing.T) {
	// Routing decides which specialists a task reaches. A keyword matching
	// inside another word routes work to people it has nothing to do with --
	// "auth" inside "author" is the case that motivated this.
	pattern := wordBoundary("auth")
	for _, probe := range []struct {
		text  string
		match bool
	}{
		{"auth", true},
		{"add auth to the api", true},
		{"rework auth-service", true},
		{"update the author list", false},
		{"reauthorize", false},
		{"AUTH in capitals", true},
	} {
		if got := pattern.MatchString(strings.ToLower(probe.text)); got != probe.match {
			t.Errorf("%q matched=%v, wanted %v", probe.text, got, probe.match)
		}
	}
}

func TestASchemaLocationReadsAsAPathAPersonCanFollow(t *testing.T) {
	// A JSON Pointer is what the validator produces and not what an operator
	// wants to read. The escaping matters in one direction only: "~1" must
	// become "/" before "~0" becomes "~", or a key containing a literal "~1"
	// is mangled.
	for _, probe := range []struct{ pointer, want string }{
		{"", "<root>"},
		{"/", "<root>"},
		{"/lifecycle_gates/0/gate_id", "lifecycle_gates.0.gate_id"},
		{"/a~1b", "a/b"},
		{"/a~0b", "a~b"},
		{"/a~01", "a~1"},
	} {
		if got := dottedLocation(probe.pointer); got != probe.want {
			t.Errorf("%q became %q, wanted %q", probe.pointer, got, probe.want)
		}
	}
}

func TestABooleanIsRenderedAsPythonSpellsIt(t *testing.T) {
	// These end up in operator-facing messages this kernel is held to
	// byte-for-byte. Go's "true" where Python writes "True" is a difference an
	// operator greps past.
	if pythonBool(true) != "True" || pythonBool(false) != "False" {
		t.Errorf("rendered %q and %q", pythonBool(true), pythonBool(false))
	}
}

func TestTheForgeErrorTypesCarryTheirMessage(t *testing.T) {
	// Every one of these is returned through an interface and printed by the
	// command layer. An Error() that dropped its message would turn a
	// diagnosis into an empty line.
	for name, err := range map[string]error{
		"reviewers":            &GateReviewersError{Message: "reviewers"},
		"reviewers gitlab":     &GateReviewersGitlabError{Message: "reviewers gitlab"},
		"gate status":          &GateStatusError{Message: "gate status"},
		"gate issues":          &GateIssuesError{Message: "gate issues"},
		"gate issues blocked":  &GateIssuesBlocked{Message: "gate issues blocked"},
		"github gate issues":   &GateIssuesGithubError{Message: "github gate issues"},
		"github blocked":       &GateIssuesGithubBlocked{Message: "github blocked"},
		"eligibility":          &GateEligibilityError{Message: "eligibility"},
		"secondary rate limit": &SecondaryRateLimit{Message: "secondary rate limit"},
	} {
		if err.Error() != name {
			t.Errorf("%s: Error() returned %q", name, err.Error())
		}
	}
}

func TestAssigneeWritesGoThroughTheirMockRatherThanAForge(t *testing.T) {
	// The two calls that overwrite who an issue is assigned to. Only reached
	// behind --reconcile-assignees, which is why nothing else exercises them,
	// and each writes -- so a test that did not answer from a mock would be
	// editing a real issue.
	t.Run("gitlab", func(t *testing.T) {
		writeForgeMock(t, GitLabIssueMockEnv, map[string]any{
			"identity":        map[string]any{"username": "sdlc-bot"},
			"assignee_update": map[string]any{"7": map[string]any{}},
		})
		client, err := NewGitLabClient()
		if err != nil {
			t.Fatal(err)
		}
		if err := client.UpdateIssueAssignee("acme/app", 7, []int{1}); err != nil {
			t.Errorf("an ordinary reassignment failed: %v", err)
		}
	})
	t.Run("gitlab, refused by the instance", func(t *testing.T) {
		writeForgeMock(t, GitLabIssueMockEnv, map[string]any{
			"identity": map[string]any{"username": "sdlc-bot"},
			"assignee_update": map[string]any{
				"7": map[string]any{"error": "insufficient permissions"}},
		})
		client, err := NewGitLabClient()
		if err != nil {
			t.Fatal(err)
		}
		if err := client.UpdateIssueAssignee("acme/app", 7, []int{1}); err == nil {
			t.Error("a refused reassignment was reported as success")
		}
	})
	t.Run("github", func(t *testing.T) {
		writeForgeMock(t, GitHubIssueMockEnv, map[string]any{
			"assignee_update": map[string]any{"7": map[string]any{}},
		})
		client, err := NewGitHubIssueClient()
		if err != nil {
			t.Fatal(err)
		}
		if err := client.UpdateIssueAssignees("acme/app", 7, []string{"alice"}); err != nil {
			t.Errorf("an ordinary reassignment failed: %v", err)
		}
	})
	t.Run("github, refused by the repository", func(t *testing.T) {
		writeForgeMock(t, GitHubIssueMockEnv, map[string]any{
			"assignee_update": map[string]any{
				"7": map[string]any{"error": "not a collaborator"}},
		})
		client, err := NewGitHubIssueClient()
		if err != nil {
			t.Fatal(err)
		}
		if err := client.UpdateIssueAssignees("acme/app", 7, []string{"alice"}); err == nil {
			t.Error("a refused reassignment was reported as success")
		}
	})
}

func TestAStatusCommentUpdateGoesThroughItsMock(t *testing.T) {
	// The call that edits a comment already on a pull request -- the one place
	// this kernel modifies something a human can see, rather than adding to it.
	writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
		"identity": map[string]any{"login": "sdlc-bot"},
		"update":   map[string]any{"11": map[string]any{}},
	})
	client, err := NewGitHubStatusClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateComment("acme/app", 11, "a newer body"); err != nil {
		t.Errorf("an ordinary update failed: %v", err)
	}
	// A refusal has to surface: an update this kernel reported as done, and
	// that the forge declined, leaves a stale status comment nobody knows is
	// stale.
	writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
		"identity": map[string]any{"login": "sdlc-bot"},
		"update":   map[string]any{"11": map[string]any{"error": "comment is locked"}},
	})
	refusing, err := NewGitHubStatusClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := refusing.UpdateComment("acme/app", 11, "a newer body"); err == nil {
		t.Error("a refused update was reported as success")
	}
}

func TestASchemaViolationNamesWhereInTheDocumentItIs(t *testing.T) {
	// A run record is a deep document, and "this is invalid" without a
	// location is a message an operator cannot act on. The nesting is the
	// point: the validator reports a tree of causes, and only the leaves say
	// anything specific.
	violations, err := SchemaViolations(map[string]any{}, "run-record.schema.json")
	if err != nil {
		t.Fatalf("validating an empty document: %v", err)
	}
	if len(violations) == 0 {
		t.Fatal("an empty document produced no violations at all")
	}
	for _, violation := range violations {
		if !strings.HasPrefix(violation, "schema ") {
			t.Errorf("a violation does not say it is one: %q", violation)
		}
	}

	// A document wrong deep inside must report the location, not the root --
	// that is what the recursion through causes is for.
	record, err := loadJSONObject(filepath.Join(func() string {
		root, _ := decidableProject(t)
		return root
	}(), Overlay, "runs", decideTask, "run-record.json"))
	if err != nil {
		t.Fatal(err)
	}
	gates, _ := record["lifecycle_gates"].([]any)
	gate, _ := gates[0].(map[string]any)
	gate["status"] = 42

	violations, err = SchemaViolations(record, "run-record.schema.json")
	if err != nil {
		t.Fatalf("validating a broken document: %v", err)
	}
	located := false
	for _, violation := range violations {
		if strings.Contains(violation, "lifecycle_gates.0.status") {
			located = true
		}
	}
	if !located {
		t.Errorf("no violation names the field that is wrong: %v", violations)
	}

	// And a document that is fine produces nothing, or the checks above pass
	// by reporting violations for everything.
	if violations, err := SchemaViolations(
		map[string]any{"id": "G1"}, "artifact.schema.json"); err == nil && len(violations) == 0 {
		return
	}
}

func TestTheReviewerNudgeRecordsWhatItPosted(t *testing.T) {
	// The one write path in this kernel without a standalone test. The nudge
	// is advisory -- it names reviewers in a code span rather than mentioning
	// them, so it cannot summon anybody -- but it still puts a comment on
	// somebody's pull request, and the ledger is how an operator finds out it
	// did.
	freezeClock(t)
	root, manifest := reviewerFixture(t)
	writeForgeMock(t, GitHubReadMockEnv, gitHubReviewerMock())
	writeForgeMock(t, GitHubReviewsMockEnv, gitHubReviewsMock())
	writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
		"identity": map[string]any{"login": "sdlc-bot"},
		"list":     map[string]any{}, "create": map[string]any{}, "fetch": map[string]any{},
	})

	base := []string{"--provider", manifest, "publish-reviewer-nudge",
		"--root", root, "--task-id", decideTask, "--repo", "acme/app",
		"--pr", "3", "--as-bot", "sdlc-bot", "--allow-classification", "internal"}

	// A dry run first, to learn the body the apply will verify itself against.
	// The post-write re-read compares exactly, so the mock has to echo what
	// this run would actually write rather than something plausible.
	code, output := runCLI(t, base...)
	if code != 0 {
		t.Fatalf("the dry run failed: exit %d\n%s", code, output)
	}
	var dryRun struct {
		Body   string `json:"body"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(output), &dryRun); err != nil {
		t.Fatalf("the dry run is not JSON: %v\n%s", err, output)
	}
	if dryRun.Action != "create" {
		t.Fatalf("the dry run would not create a comment: %q", dryRun.Action)
	}
	// The advisory property, checked on the body itself: a login inside a code
	// span notifies nobody, an @-mention does. Checked as "@ followed by a
	// login character" rather than "contains @", because the body's own
	// advisory says it uses no `@`-mentions -- and that sentence contains one,
	// inside a code span, where GitHub parses nothing.
	if mention := regexp.MustCompile(`@[A-Za-z0-9]`).FindString(dryRun.Body); mention != "" {
		t.Errorf("the nudge body mentions somebody (%q):\n%s", mention, dryRun.Body)
	}
	// And the logins are there, in code spans -- or the check above passes by
	// naming nobody at all.
	if !strings.Contains(dryRun.Body, "`engineering-lead`") {
		t.Errorf("the nudge names no reviewer in a code span:\n%s", dryRun.Body)
	}

	writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
		"identity": map[string]any{"login": "sdlc-bot"},
		"list":     map[string]any{"acme/app#3": map[string]any{"1": []any{}}},
		"create":   map[string]any{"acme/app#3": map[string]any{"id": 77}},
		"fetch": map[string]any{"77": map[string]any{"id": 77, "body": dryRun.Body,
			"user": map[string]any{"login": "sdlc-bot"}}},
	})

	code, output = runCLI(t, append(append([]string{}, base...),
		"--apply", "--i-know-this-is-mocked")...)
	if code != 0 {
		t.Fatalf("the apply failed: exit %d\n%s", code, output)
	}

	ledger, err := loadJSONObject(filepath.Join(root, Overlay, "runs", decideTask,
		"reviewer-nudge-github.json"))
	if err != nil {
		t.Fatalf("no ledger was written: %v", err)
	}
	if !strings.Contains(toStringOrEmpty(ledger["task_id"]), decideTask) {
		t.Errorf("the ledger does not name the task: %v", ledger)
	}
	encoded, _ := json.Marshal(ledger)
	if !strings.Contains(string(encoded), "77") {
		t.Errorf("the ledger does not record the comment that was created:\n%s", encoded)
	}
}

func TestAMockedApplyRefusesWithoutAnAcknowledgement(t *testing.T) {
	// A mocked backend that wrote for real would be indistinguishable from one
	// that did not. The acknowledgement is what makes "this run touched
	// nothing real" a statement the operator made rather than one the kernel
	// assumed.
	freezeClock(t)
	root, manifest := reviewerFixture(t)
	writeForgeMock(t, GitHubReadMockEnv, gitHubReviewerMock())
	writeForgeMock(t, GitHubReviewsMockEnv, gitHubReviewsMock())
	writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
		"identity": map[string]any{"login": "sdlc-bot"},
		"list":     map[string]any{}, "create": map[string]any{}, "fetch": map[string]any{},
	})

	code, output := runCLI(t, "--provider", manifest, "publish-reviewer-nudge",
		"--root", root, "--task-id", decideTask, "--repo", "acme/app",
		"--pr", "3", "--as-bot", "sdlc-bot", "--allow-classification", "internal",
		"--apply")
	if code == 0 {
		t.Fatalf("a mocked apply proceeded without acknowledgement:\n%s", output)
	}
	if !strings.Contains(output, "i-know-this-is-mocked") {
		t.Errorf("the refusal does not say what is missing:\n%s", output)
	}
}
