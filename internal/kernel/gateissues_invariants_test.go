package kernel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Gateissues: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of gateissues_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

func TestASecondRunCreatesNothing(t *testing.T) {
	// Idempotency, stated directly. The forge is asked what exists on every
	// run, so a second run against a world where everything exists must reuse
	// all of it and create none.
	freezeClock(t)
	root, manifest := issuesFixture(t)
	report := dryRunPlan(t, root, manifest)
	digest, _ := report["plan_digest"].(string)

	writeForgeMock(t, GitLabIssueMockEnv, issueMock(t, root, report, false))
	first, err := applyIssues(t, root, manifest, digest)
	if err != nil {
		t.Fatalf("the first run failed: %v", err)
	}
	createdCount := countStatuses(first, "created")
	if createdCount == 0 {
		t.Fatal("the first run created nothing; this test would prove nothing")
	}

	writeForgeMock(t, GitLabIssueMockEnv, issueMock(t, root, report, true))
	second, err := applyIssues(t, root, manifest, digest)
	if err != nil {
		t.Fatalf("the second run failed: %v", err)
	}
	if created := countStatuses(second, "created"); created != 0 {
		t.Errorf("the second run created %d issues", created)
	}
	if reused := countStatuses(second, "reused"); reused != createdCount {
		t.Errorf("the first run created %d issues and the second reused %d",
			createdCount, reused)
	}
}

func TestTheLedgerRecordsAnAttemptBeforeItIsMade(t *testing.T) {
	// The write ordering that makes a crash investigable. A `creating` entry
	// is flushed to disk before GitLab is called, so an interrupted run leaves
	// a record saying an issue may exist -- rather than an issue on the forge
	// that nothing local mentions.
	//
	// Checked by making one create fail and looking for that item's entry.
	// Naming the item matters: a run that wrote no entry for the item it
	// failed on, but wrote one for something else, would satisfy a test that
	// only looked for any `creating` entry anywhere.
	for _, family := range []string{"gate", "approval"} {
		t.Run("a failed "+family+" create", func(t *testing.T) {
			freezeClock(t)
			root, manifest := issuesFixture(t)
			report := dryRunPlan(t, root, manifest)
			digest, _ := report["plan_digest"].(string)

			items := listOf(report[family+"_issues"])
			if len(items) == 0 {
				t.Fatalf("the plan has no %s issues; this case would prove nothing", family)
			}
			first, _ := items[0].(map[string]any)
			label, _ := first["label"].(string)
			gateID, _ := first["gate_id"].(string)
			entryKey := gateID
			if family == "approval" {
				authorityID, _ := first["authority_id"].(string)
				entryKey = gateID + "/" + authorityID
			}

			mock := issueMock(t, root, report, false)
			// No create response for that one issue, so its create fails.
			createResponses, _ := mock["create"].(map[string]any)
			delete(createResponses, FixedLabel+","+label)
			writeForgeMock(t, GitLabIssueMockEnv, mock)

			if _, err := applyIssues(t, root, manifest, digest); err == nil {
				t.Fatal("a failed create was not reported")
			}

			ledger, err := loadJSONObject(filepath.Join(root, Overlay, "runs", issuesTask,
				"gate-issues-gitlab.json"))
			if err != nil {
				t.Fatalf("no ledger survived the failure: %v", err)
			}
			entries, _ := ledger["entries"].(map[string]any)
			entry, present := entries[entryKey].(map[string]any)
			if !present {
				t.Fatalf("the ledger says nothing was attempted for %s: %v", entryKey, entries)
			}
			if entry["status"] != "creating" {
				t.Errorf("%s is recorded as %v, not as an attempt in progress", entryKey, entry["status"])
			}
			if entry["issue_iid"] != nil {
				t.Errorf("a creating entry carries an iid it cannot know: %v", entry)
			}
			if entry["recorded_at"] != nil {
				t.Errorf("a creating entry is recorded as complete: %v", entry)
			}
		})
	}
}

func TestTheDigestIsRecheckedBetweenItems(t *testing.T) {
	// The digest is checked once against what the operator approved, and then
	// again before every single item. The re-check is what stops a run that
	// began under one plan from finishing under another -- somebody editing
	// the run record while forty issues are being created would otherwise get
	// half of each.
	//
	// Exercised directly rather than through a run, because there is no seam
	// for mutating the project between two items of one invocation.
	freezeClock(t)
	root, manifest := issuesFixture(t)
	report := dryRunPlan(t, root, manifest)
	digest, _ := report["plan_digest"].(string)

	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	request := GateIssuesRequest{
		Root: root, TaskID: issuesTask, ProjectPath: "acme/app", AsBot: "sdlc-bot",
		AllowClassification: "internal",
	}
	// The same gate ids the run itself derived, not all ten -- the digest
	// covers which gates were in scope.
	var gateIDs []string
	for _, raw := range listOf(report["gate_ids"]) {
		gateIDs = append(gateIDs, toStringOrEmpty(raw))
	}

	// Unchanged: the re-check must pass, or the assertion below passes for the
	// wrong reason.
	if err := registry.checkDigestUnchanged(root, issuesTask, request, gateIDs,
		contracts, digest, "gate 'G1'"); err != nil {
		t.Fatalf("an untouched project failed its own re-check: %v", err)
	}

	// Somebody invalidated a gate while the run was in flight: the plan the
	// operator approved is no longer the plan.
	mutateJSON(t, filepath.Join(root, Overlay, "runs", issuesTask, "run-record.json"),
		func(document map[string]any) {
			document["re_entry_history"] = append(listOf(document["re_entry_history"]),
				map[string]any{
					"gate_id": "G1", "reason": "requirements changed",
					"invalidated_at": "2026-08-15T09:00:00+00:00",
				})
		})
	err = registry.checkDigestUnchanged(root, issuesTask, request, gateIDs,
		contracts, digest, "gate 'G1'")
	if err == nil {
		t.Fatal("the run record changed mid-run and the re-check said nothing")
	}
	if !strings.Contains(err.Error(), "plan digest changed before gate 'G1'") {
		t.Errorf("the refusal does not name the item it stopped at: %v", err)
	}
	if !strings.Contains(err.Error(), "already-created issues are unaffected") {
		t.Errorf("the refusal does not say what happened to the issues already made: %v", err)
	}
}

func TestTheMarkerFamiliesAreDisjoint(t *testing.T) {
	// Four marker families exist across this kernel. A collision would make
	// one artifact's identity match another's, and each command would then
	// keep reusing the wrong thing.
	markers := map[string]string{
		"gate":     ComputeGateMarker("TASK-1", "G1"),
		"approval": ComputeApprovalMarker("TASK-1", "G1", "product_owner"),
		"status":   ComputeStatusMarker("TASK-1"),
		"nudge":    ComputeNudgeMarker("TASK-1"),
		"task":     TaskHash("TASK-1"),
	}
	seen := map[string]string{}
	for name, marker := range markers {
		if other, collides := seen[marker]; collides {
			t.Errorf("%s and %s produce the same marker %s", name, other, marker)
		}
		seen[marker] = name
		if len(marker) != 16 {
			t.Errorf("%s marker is %d characters", name, len(marker))
		}
	}

	// And the gate marker varies by gate, the approval marker by authority --
	// otherwise every gate would share one issue.
	if ComputeGateMarker("TASK-1", "G1") == ComputeGateMarker("TASK-1", "G2") {
		t.Error("two gates share a marker")
	}
	if ComputeApprovalMarker("TASK-1", "G1", "product_owner") ==
		ComputeApprovalMarker("TASK-1", "G1", "security_lead") {
		t.Error("two authorities share an approval marker")
	}
}

func TestALabelThatAForgeWouldRejectIsRefused(t *testing.T) {
	// A label outside the charset is one GitLab would normalise or reject --
	// and the next run's search would then not find the issue, which turns
	// idempotency into duplication.
	if _, err := GateLabel("Not A Marker"); err == nil {
		t.Error("a label with spaces and capitals was accepted")
	}
	if _, err := ApprovalLabel("marker/with/slashes"); err == nil {
		t.Error("a label with slashes was accepted")
	}
	// And an ordinary marker still works, or the check above passes by
	// refusing everything.
	label, err := GateLabel(ComputeGateMarker("TASK-1", "G1"))
	if err != nil {
		t.Fatalf("an ordinary marker was refused: %v", err)
	}
	if !strings.HasPrefix(label, gateLabelPrefix) {
		t.Errorf("the label lost its prefix: %s", label)
	}
}

func TestAnIssueNeverCarriesTheTaskID(t *testing.T) {
	// The task id is operator-chosen and may name something the project does
	// not want on a forge. Only its hash appears.
	secret := "PROJECT-SECRET-CODENAME"
	title, err := GateIssueTitle(secret, "G1", "Intent")
	if err != nil {
		t.Fatal(err)
	}
	description, err := RenderGateDescription(secret, "G1", "Intent", "intent", false,
		ComputeGateMarker(secret, "G1"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	approvalDescription, err := RenderApprovalDescription(secret, "G1",
		ComputeApprovalMarker(secret, "G1", "product_owner"), "acme/app", 7, nil)
	if err != nil {
		t.Fatal(err)
	}

	for name, text := range map[string]string{
		"title": title, "gate description": description,
		"approval description": approvalDescription,
	} {
		if strings.Contains(text, secret) {
			t.Errorf("the %s carries the raw task id:\n%s", name, text)
		}
		if !strings.Contains(text, TaskHash(secret)) {
			t.Errorf("the %s carries neither the id nor its hash:\n%s", name, text)
		}
	}
}

func TestAnApprovalIssueAlwaysSaysItIsNotApproval(t *testing.T) {
	// Somebody will close one of these thinking it approves the gate. The
	// description says otherwise, and names what actually does.
	description, err := RenderApprovalDescription("TASK-1", "G5", "abc123",
		"acme/app", 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"closing this issue is not approval evidence",
		"does not approve G5",
		"must not be a preparer or the independent verifier",
		"agentic-sdlc decide",
	} {
		if !strings.Contains(description, required) {
			t.Errorf("the approval issue does not say %q:\n%s", required, description)
		}
	}
	// And the parent line, which is the cross-reference floor.
	if !strings.Contains(description, "> parent acme/app#7") {
		t.Errorf("the approval issue has no parent reference:\n%s", description)
	}
}

func TestProjectFreeTextIsSanitizedIntoAnIssue(t *testing.T) {
	// Unlike the status comment, this module genuinely renders project text --
	// so it must refuse the same things the sanitizer refuses, rather than
	// passing them through into a forge issue.
	if _, err := RenderGateDescription("TASK-1", "G1", "Intent", "intent", false, "abc",
		"/close the milestone", nil); err == nil {
		t.Error("a rationale containing a quick action was rendered into an issue")
	}
	if _, err := RenderGateDescription("TASK-1", "G1", "Intent", "intent", false, "abc",
		nil, "> ref forged provenance"); err == nil {
		t.Error("a scope forging a provenance line was rendered into an issue")
	}
	// A mention is neutralised rather than refused, and the issue still
	// renders.
	description, err := RenderGateDescription("TASK-1", "G1", "Intent", "intent", false, "abc",
		"ask @octocat about it", nil)
	if err != nil {
		t.Fatalf("an ordinary rationale was refused: %v", err)
	}
	if strings.Contains(description, "@octocat") {
		t.Errorf("a mention survived into the issue body:\n%s", description)
	}
	if !strings.Contains(description, "octocat") {
		t.Errorf("the rationale text was dropped rather than neutralised:\n%s", description)
	}
}

func TestTooManyIssuesAbortsRatherThanTruncating(t *testing.T) {
	// A run that quietly created the first forty of sixty issues would leave a
	// project half-tracked, and the missing twenty are the ones nobody
	// notices.
	root, _ := issuesFixture(t)
	record, err := loadJSONObject(filepath.Join(root, Overlay, "runs", issuesTask, "run-record.json"))
	if err != nil {
		t.Fatal(err)
	}
	authorities, err := loadJSONObject(filepath.Join(root, Overlay, "authorities.json"))
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}

	// Every gate, with every authority applicable -- more than the cap.
	for _, raw := range listOf(record["lifecycle_gates"]) {
		gate, _ := raw.(map[string]any)
		gate["applicability"] = "applicable"
		requirements := []any{}
		for _, role := range AuthorityRoleOrder {
			requirements = append(requirements, map[string]any{
				"authority_id": role, "authority_type": "human-approver",
				"role": RoleLabels[role], "applicability": "applicable",
			})
		}
		gate["authority_requirements"] = requirements
	}

	_, err = buildIssuePlan(gitlabIssueForge, issuesTask, "acme/app", GateIDs, record, authorities,
		contracts, false, nil)
	if err == nil {
		t.Fatal("a plan larger than the cap was accepted")
	}
	if !strings.Contains(err.Error(), "aborting rather than truncating") {
		t.Errorf("the refusal does not say it refused rather than truncated: %v", err)
	}
}

func TestTheLedgerIsNeverTrustedForExistence(t *testing.T) {
	// The ledger says an issue exists; the forge says it does not. The forge
	// wins, because the ledger can be stale, deleted, or from another machine
	// -- and trusting it would skip creating an issue the project has none of.
	freezeClock(t)
	root, manifest := issuesFixture(t)
	report := dryRunPlan(t, root, manifest)
	digest, _ := report["plan_digest"].(string)

	// A ledger claiming everything was created, against a forge where nothing
	// is findable.
	ledgerPath := filepath.Join(root, Overlay, "runs", issuesTask, "gate-issues-gitlab.json")
	entries := map[string]any{}
	for _, raw := range listOf(report["gate_issues"]) {
		item, _ := raw.(map[string]any)
		gateID, _ := item["gate_id"].(string)
		entries[gateID] = map[string]any{
			"kind": "gate", "gate_id": gateID, "marker": item["marker"],
			"status": "created", "issue_iid": 999, "issue_state": "opened",
		}
	}
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(map[string]any{
		"schema_version": 1, "task_id": issuesTask, "project_path": "acme/app",
		"bot_username": "sdlc-bot", "mocked": true, "entries": entries,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}

	writeForgeMock(t, GitLabIssueMockEnv, issueMock(t, root, report, false))
	result, err := applyIssues(t, root, manifest, digest)
	if err != nil {
		t.Fatal(err)
	}
	if created := countStatuses(result, "created"); created == 0 {
		t.Error("the run trusted the ledger and created nothing")
	}
}

func countStatuses(result *orderedObject, status string) int {
	count := 0
	for _, key := range []string{"gate_results", "approval_results"} {
		for _, raw := range listOf(result.values[key]) {
			entry, ok := raw.(*orderedObject)
			if ok && entry.values["status"] == status {
				count++
			}
		}
	}
	return count
}

// issuesFixture is a project with assigned authorities carrying GitLab
// bindings, and one planned task.
func issuesFixture(t *testing.T) (root, manifest string) {
	t.Helper()
	root, manifest = decidableProject(t)
	mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
		func(document map[string]any) {
			for role, raw := range document {
				authority, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if _, isAuthority := authority["status"]; !isAuthority {
					continue
				}
				authority["gitlab_username"] = strings.ReplaceAll(role, "_", "-")
			}
		})
	return root, manifest
}

// dryRunPlan runs the Go dry run and returns its report, which every mock
// below is built from.

func dryRunPlan(t *testing.T, root, manifest string) map[string]any {
	t.Helper()
	writeForgeMock(t, GitLabIssueMockEnv, map[string]any{
		"identity": map[string]any{"username": "sdlc-bot"},
		"search":   map[string]any{}, "create": map[string]any{},
		"verify": map[string]any{}, "users": map[string]any{},
	})
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--provider", manifest, "create-gate-issues",
		"--root", root, "--task-id", issuesTask, "--project-path", "acme/app",
		"--as-bot", "sdlc-bot", "--allow-classification", "internal"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("the dry run failed: %s%s", stdout.String(), stderr.String())
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return report
}

// issueMock builds the canned GitLab responses for a plan.
//
// created=false is the first-run world: nothing is findable, every create
// succeeds. created=true is the second-run world: everything is findable by
// its label pair, so the run must reuse rather than create.

func issueMock(t *testing.T, root string, report map[string]any, created bool) map[string]any {
	t.Helper()
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	record, err := loadJSONObject(filepath.Join(root, Overlay, "runs", issuesTask, "run-record.json"))
	if err != nil {
		t.Fatal(err)
	}
	gateByID := map[string]map[string]any{}
	for _, raw := range listOf(record["lifecycle_gates"]) {
		if gate, ok := raw.(map[string]any); ok {
			id, _ := gate["gate_id"].(string)
			gateByID[id] = gate
		}
	}

	search := map[string]any{}
	createResponses := map[string]any{}
	verify := map[string]any{}
	users := map[string]any{}
	iid := 100

	addIssue := func(label, title string, assignee string) {
		key := FixedLabel + "," + label
		createResponses[key] = map[string]any{"iid": iid}
		assignees := []any{}
		if assignee != "" {
			assignees = append(assignees, map[string]any{"username": assignee})
		}
		verify[fmt.Sprint(iid)] = map[string]any{
			"title": title, "state": "opened",
			"labels": []any{FixedLabel, label}, "assignees": assignees,
			"author":       map[string]any{"username": "sdlc-bot"},
			"references":   map[string]any{"full": fmt.Sprintf("acme/app#%d", iid)},
			"confidential": false,
		}
		if created {
			search[key] = []any{map[string]any{
				"iid": iid, "labels": []any{FixedLabel, label}}}
		}
		iid++
	}

	for _, raw := range listOf(report["gate_issues"]) {
		item, _ := raw.(map[string]any)
		gateID, _ := item["gate_id"].(string)
		label, _ := item["label"].(string)
		gateName := gateID
		if name, ok := contracts[gateID]["name"].(string); ok && name != "" {
			gateName = name
		}
		title, err := GateIssueTitle(issuesTask, gateID, gateName)
		if err != nil {
			t.Fatal(err)
		}
		addIssue(label, title, "")
	}
	for _, raw := range listOf(report["approval_issues"]) {
		item, _ := raw.(map[string]any)
		gateID, _ := item["gate_id"].(string)
		authorityID, _ := item["authority_id"].(string)
		label, _ := item["label"].(string)
		gateName := gateID
		if name, ok := contracts[gateID]["name"].(string); ok && name != "" {
			gateName = name
		}
		role := authorityID
		for _, requirementRaw := range listOf(gateByID[gateID]["authority_requirements"]) {
			requirement, _ := requirementRaw.(map[string]any)
			if requirement["authority_id"] == authorityID {
				if declared, ok := requirement["role"].(string); ok {
					role = declared
				}
			}
		}
		title, err := ApprovalIssueTitle(issuesTask, gateID, gateName, role)
		if err != nil {
			t.Fatal(err)
		}
		username := strings.ReplaceAll(authorityID, "_", "-")
		users[username] = []any{map[string]any{
			"id": iid, "username": username, "state": "active"}}
		addIssue(label, title, username)
	}

	return map[string]any{
		"identity": map[string]any{"username": "sdlc-bot"},
		"search":   search, "create": createResponses,
		"verify": verify, "users": users,
	}
}

// lowestKey picks one entry out of a mock, by sorted key rather than by Go's
// randomised map order -- so a case damages the same issue on every run, and a
// failure is reproducible rather than a thing that happens one time in seven.

func applyIssues(
	t *testing.T, root, manifest, digest string, extra ...string,
) (*orderedObject, error) {
	t.Helper()
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	request := GateIssuesRequest{
		Root: root, TaskID: issuesTask, ProjectPath: "acme/app", AsBot: "sdlc-bot",
		Apply: true, PlanDigest: digest, AllowClassification: "internal",
		KnowinglyMocked: true,
	}
	for index := 0; index < len(extra); index++ {
		if extra[index] == "--reconcile-assignees" {
			request.ReconcileAssignees = true
		}
	}
	return registry.CreateGateIssues(request)
}

const issuesTask = "TASK-1"
