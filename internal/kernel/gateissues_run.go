package kernel

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Creating and reusing gate issues.
//
// The write ordering here is the part worth reading carefully. Before every
// create, a `creating` entry is written to the ledger and flushed to disk --
// *then* GitLab is called. If the process dies between the two, the ledger
// says an issue may exist that nobody has an id for, which is a state a human
// can investigate. The other order would leave an issue on the forge that no
// local record mentions at all.
//
// Everything created is then read back and checked field by field. A forge
// that stored something other than what was sent -- a label an admin rule
// stripped, a confidentiality setting a template applied, an assignee that did
// not take -- means the issue is not the artifact this kernel believes it
// created, and the run aborts immediately rather than carrying on and
// recording a lie.
//
// And the plan digest is re-checked before *every* item, not once at the
// start. A run creating twenty issues takes long enough for somebody to
// reassign an authority halfway through, and the remaining issues would then
// be created from a plan that no longer describes the project.

// GateIssuesRequest is one `create-gate-issues` invocation.
type GateIssuesRequest struct {
	Root                string
	TaskID              string
	ProjectPath         string
	AsBot               string
	Gates               []string
	Apply               bool
	PlanDigest          string
	AllowClassification string
	LinkType            string
	IncludeScope        bool
	ReconcileAssignees  bool
	BreakLock           bool
	KnowinglyMocked     bool
}

// CreateGateIssues plans and, with Apply, creates the issues.
func (r *Registry) CreateGateIssues(request GateIssuesRequest) (*orderedObject, error) {
	root, err := resolveExisting(request.Root)
	if err != nil {
		return nil, &GateIssuesError{Message: err.Error()}
	}
	taskID, err := SafeTaskID(request.TaskID)
	if err != nil {
		return nil, &GateIssuesError{Message: err.Error()}
	}

	record, dispatchPlan, authorities, err := loadTaskContext(root, taskID)
	if err != nil {
		return nil, &GateIssuesError{Message: err.Error()}
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		return nil, &GateIssuesError{Message: err.Error()}
	}

	gateByID := map[string]map[string]any{}
	for _, raw := range listOf(record["lifecycle_gates"]) {
		if gate, ok := raw.(map[string]any); ok {
			id, _ := gate["gate_id"].(string)
			gateByID[id] = gate
		}
	}
	if request.AllowClassification == "" ||
		request.AllowClassification != toStringOrEmpty(record["classification"]) {
		return nil, &GateIssuesError{Message: fmt.Sprintf(
			"--allow-classification must be supplied and exactly match the task's classification "+
				"(got %s, task classification is %s)",
			pythonRepr(nonEmptyOrNil(request.AllowClassification)), pythonRepr(record["classification"]))}
	}

	var gateIDs []string
	if len(request.Gates) > 0 {
		for _, gateID := range request.Gates {
			// A gate the project is not configured for needs somebody to
			// change the project, so it exits 2; a gate id that does not
			// exist is a typo, so it exits 1. Collapsing the two was a real
			// defect -- an operator scripting against exit codes would have
			// treated "this task does not run G9" as a malformed command.
			if err := CheckGateEligibility(gateID, dispatchPlan, gateByID[gateID]); err != nil {
				var eligibility *GateEligibilityError
				if errors.As(err, &eligibility) && eligibility.NeedsHuman {
					return nil, &GateIssuesBlocked{Message: err.Error()}
				}
				return nil, &GateIssuesError{Message: err.Error()}
			}
			gateIDs = append(gateIDs, gateID)
		}
	} else {
		gateIDs = defaultGateIDs(dispatchPlan, gateByID)
	}

	plan, digest, err := r.planAndDigest(taskID, request, gateIDs, record, authorities,
		dispatchPlan, contracts)
	if err != nil {
		return nil, err
	}

	client, err := NewGitLabClient()
	if err != nil {
		return nil, &GateIssuesError{Message: err.Error()}
	}
	mocked := client.Mocked()

	if !request.Apply {
		return dryRunReport(digest, request.ProjectPath, gateIDs, mocked, plan), nil
	}

	// The digest handshake. An apply must be handed the digest a dry run
	// produced, and it must still match -- otherwise the operator approved a
	// plan for a project that has since changed.
	if request.PlanDigest == "" {
		return nil, &GateIssuesError{
			Message: "--apply requires --plan-digest (from a prior --dry-run)"}
	}
	if request.PlanDigest != digest {
		return nil, &GateIssuesBlocked{Message: fmt.Sprintf(
			"--plan-digest mismatch: recomputed %s != supplied %s -- state changed since "+
				"the --dry-run this digest came from; re-run --dry-run",
			pythonRepr(digest), pythonRepr(request.PlanDigest))}
	}
	if mocked && !request.KnowinglyMocked {
		return nil, &GateIssuesError{Message: fmt.Sprintf(
			"%s is set but --i-know-this-is-mocked was not passed -- refusing to --apply "+
				"against a mocked GitLab backend", GitLabIssueMockEnv)}
	}

	verifiedUsername, err := client.VerifyIdentity(request.AsBot)
	if err != nil {
		return nil, &GateIssuesError{Message: err.Error()}
	}
	return r.applyGateIssues(root, taskID, request, gateIDs, plan, digest,
		client, verifiedUsername, mocked)
}

// planAndDigest builds the plan and hashes what it was derived from.
func (r *Registry) planAndDigest(
	taskID string, request GateIssuesRequest, gateIDs []string,
	record, authorities, dispatchPlan map[string]any,
	contracts map[string]map[string]any,
) (*issuePlan, string, error) {
	var scope any
	if request.IncludeScope {
		scope = record["scope"]
	}
	plan, err := buildIssuePlan(taskID, request.ProjectPath, gateIDs, record, authorities,
		contracts, request.IncludeScope, scope)
	if err != nil {
		return nil, "", err
	}
	digest, err := ComputePlanDigest(taskID, request.ProjectPath, gateIDs,
		dispatchPlan["dispatch_fingerprint"], plan.perGate,
		record["disposition"], record["classification"],
		len(listOf(record["re_entry_history"])))
	if err != nil {
		return nil, "", &GateIssuesError{Message: err.Error()}
	}
	return plan, digest, nil
}

// freshDigest re-derives the digest from what is on disk right now.
//
// Re-read rather than remembered: the point is to notice an edit that happened
// after this run started, and a cached copy of the record cannot.
func (r *Registry) freshDigest(
	root, taskID string, request GateIssuesRequest, gateIDs []string,
	contracts map[string]map[string]any,
) (string, error) {
	record, dispatchPlan, authorities, err := loadTaskContext(root, taskID)
	if err != nil {
		return "", &GateIssuesError{Message: err.Error()}
	}
	_, digest, err := r.planAndDigest(taskID, request, gateIDs, record, authorities,
		dispatchPlan, contracts)
	return digest, err
}

func dryRunReport(
	digest, projectPath string, gateIDs []string, mocked bool, plan *issuePlan,
) *orderedObject {
	gateIssues := []any{}
	for _, gate := range plan.gates {
		gateIssues = append(gateIssues, ordered(
			"gate_id", gate.GateID, "marker", gate.Marker, "label", gate.Label))
	}
	approvalIssues := []any{}
	for _, approval := range plan.approvals {
		approvalIssues = append(approvalIssues, ordered(
			"gate_id", approval.GateID, "authority_id", approval.AuthorityID,
			"marker", approval.Marker, "label", approval.Label))
	}
	if gateIDs == nil {
		gateIDs = []string{}
	}
	return ordered(
		"mode", "dry-run",
		"plan_digest", digest,
		"project_path", projectPath,
		"gate_ids", asJSONList(gateIDs),
		"mocked", mocked,
		"gate_issues", gateIssues,
		"approval_issues", approvalIssues,
		"skipped", skippedEntries(plan.skipped),
		"refusals", refusalEntries(plan.refusals),
	)
}

func skippedEntries(entries []SkippedEntry) []any {
	out := []any{}
	for _, entry := range entries {
		out = append(out, ordered(
			"gate_id", entry.GateID, "authority_id", entry.AuthorityID,
			"reason", entry.Reason, "rationale", entry.Rationale))
	}
	return out
}

func refusalEntries(entries []RefusalEntry) []any {
	out := []any{}
	for _, entry := range entries {
		out = append(out, ordered(
			"gate_id", entry.GateID, "authority_id", entry.AuthorityID,
			"reason", entry.Reason, "detail", entry.Detail))
	}
	return out
}

// issueLedger is the sidecar this run records into.
type issueLedger struct {
	path    string
	root    string
	taskID  string
	entries *orderedObject
	object  *orderedObject
}

func (l *issueLedger) setEntry(key string, entry *orderedObject) { l.entries.set(key, entry) }

func (l *issueLedger) write() error {
	l.object.set("entries", l.entries)
	return WriteLedgerFile(l.path, l.object, ".gate-issues.")
}

// applyGateIssues walks the plan, creating or reusing each issue.
func (r *Registry) applyGateIssues(
	root, taskID string, request GateIssuesRequest, gateIDs []string,
	plan *issuePlan, digest string, client *GitLabClient,
	verifiedUsername string, mocked bool,
) (*orderedObject, error) {
	lockPath, err := LedgerPath(root, Overlay, taskID, "gate-issues-"+ForgeGitLab+".lock")
	if err != nil {
		return nil, &GateIssuesError{Message: err.Error()}
	}
	if err := AcquireLockFile(lockPath, request.BreakLock); err != nil {
		return nil, &GateIssuesBlocked{Message: err.Error()}
	}
	defer func() { _ = ReleaseLockFile(lockPath) }()

	ledger, err := r.openIssueLedger(root, taskID, request.ProjectPath, verifiedUsername, mocked)
	if err != nil {
		return nil, err
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		return nil, &GateIssuesError{Message: err.Error()}
	}

	approvalsByGate := map[string][]ApprovalCandidate{}
	for _, approval := range plan.approvals {
		approvalsByGate[approval.GateID] = append(approvalsByGate[approval.GateID], approval)
	}

	gateResults := []any{}
	approvalResults := []any{}
	refusals := append([]RefusalEntry{}, plan.refusals...)
	driftDetected := false

	for _, gate := range plan.gates {
		if err := r.checkDigestUnchanged(root, taskID, request, gateIDs, contracts, digest,
			fmt.Sprintf("gate %s", pythonRepr(gate.GateID))); err != nil {
			return nil, err
		}
		result, gateIID, err := processGateIssue(
			client, ledger, request.ProjectPath, gate, verifiedUsername, mocked)
		if err != nil {
			return nil, err
		}
		gateResults = append(gateResults, result)

		for _, approval := range approvalsByGate[gate.GateID] {
			if err := r.checkDigestUnchanged(root, taskID, request, gateIDs, contracts, digest,
				fmt.Sprintf("approval %s/%s", gate.GateID, approval.AuthorityID)); err != nil {
				return nil, err
			}
			result, drift, err := processApprovalIssue(client, ledger, taskID,
				request.ProjectPath, approval, gateIID, verifiedUsername, mocked,
				request.LinkType, request.ReconcileAssignees)
			var refusal *approvalRefusal
			if errors.As(err, &refusal) {
				// One unresolvable username does not stop the run: the other
				// issues are still worth creating, and the refusal is
				// reported rather than swallowed.
				refusals = append(refusals, RefusalEntry{
					refusal.GateID, refusal.AuthorityID, refusal.Reason, refusal.Detail})
				continue
			}
			if err != nil {
				return nil, err
			}
			if drift {
				driftDetected = true
			}
			approvalResults = append(approvalResults, result)
		}
	}

	if gateIDs == nil {
		gateIDs = []string{}
	}
	return ordered(
		"mode", "apply",
		"plan_digest", digest,
		"project_path", request.ProjectPath,
		"gate_ids", asJSONList(gateIDs),
		"mocked", mocked,
		"bot_username", verifiedUsername,
		"gate_results", gateResults,
		"approval_results", approvalResults,
		"skipped", skippedEntries(plan.skipped),
		"refusals", refusalEntries(refusals),
		"drift_detected", driftDetected,
	), nil
}

// checkDigestUnchanged aborts if the project moved since the plan was made.
func (r *Registry) checkDigestUnchanged(
	root, taskID string, request GateIssuesRequest, gateIDs []string,
	contracts map[string]map[string]any, expected, context string,
) error {
	fresh, err := r.freshDigest(root, taskID, request, gateIDs, contracts)
	if err != nil {
		return err
	}
	if fresh != expected {
		// Already-created issues are left alone. They are real, they are
		// recorded, and deleting them would be a second guess about a forge
		// this run no longer understands the state of.
		return &GateIssuesBlocked{Message: fmt.Sprintf(
			"plan digest changed before %s (a concurrent edit happened) -- aborting "+
				"remaining items; already-created issues are unaffected", context)}
	}
	return nil
}

func (r *Registry) openIssueLedger(
	root, taskID, projectPath, botUsername string, mocked bool,
) (*issueLedger, error) {
	path, err := LedgerPath(root, Overlay, taskID, "gate-issues-"+ForgeGitLab+".json")
	if err != nil {
		return nil, &GateIssuesError{Message: err.Error()}
	}
	// This ledger's own skeleton, not the comment publishers': it records a
	// project path and a map of entries rather than a comment target and a
	// list. Borrowing theirs added three fields nothing here writes, which the
	// differential caught as a byte difference in an otherwise identical file.
	object, err := loadIssueLedgerFile(path, taskID)
	if err != nil {
		return nil, &GateIssuesError{Message: err.Error()}
	}
	object.set("schema_version", ledgerSchemaVersion)
	object.set("task_id", taskID)
	object.set("project_path", projectPath)
	object.set("bot_username", botUsername)
	object.set("mocked", mocked)

	entries, ok := object.values["entries"].(*orderedObject)
	if !ok {
		entries = &orderedObject{values: map[string]any{}}
	}
	return &issueLedger{path: path, root: root, taskID: taskID,
		entries: entries, object: object}, nil
}

// processGateIssue creates or reuses one gate's tracking issue.
func processGateIssue(
	client *GitLabClient, ledger *issueLedger, projectPath string,
	gate GatePlan, botUsername string, mocked bool,
) (*orderedObject, int, error) {
	context := "gate " + gate.GateID
	existing, err := client.SearchIssuesByLabels(projectPath, []string{FixedLabel, gate.Label})
	if err != nil {
		return nil, 0, &GateIssuesError{Message: fmt.Sprintf(
			"gate %s: %s", pythonRepr(gate.GateID), err.Error())}
	}
	if len(existing) > 1 {
		return nil, 0, &GateIssuesBlocked{Message: fmt.Sprintf(
			"gate %s: %d issues matched labels [%s, %s] -- ambiguous identity, needs human resolution",
			gate.GateID, len(existing), FixedLabel, gate.Label)}
	}

	if len(existing) == 1 {
		issue, _ := existing[0].(map[string]any)
		if err := validateMatchedIssue(issue, gate.Label, gateLabelPrefix, context); err != nil {
			return nil, 0, err
		}
		iid, _ := jsonNumber(issue["iid"])
		verification, err := client.FetchIssueVerification(projectPath, iid)
		if err != nil {
			return nil, 0, &GateIssuesError{Message: fmt.Sprintf(
				"gate %s: %s", pythonRepr(gate.GateID), err.Error())}
		}
		// Reusing an issue somebody else authored would attach this task's
		// tracking to a stranger's issue.
		if !strings.EqualFold(toStringOrEmpty(verification.AuthorUsername), botUsername) {
			ledger.setEntry(gate.GateID, gateLedgerEntry(gate, "suspect", iid, verification.State,
				"matched issue author does not match the verified bot identity"))
			if err := ledger.write(); err != nil {
				return nil, 0, &GateIssuesError{Message: err.Error()}
			}
			return nil, 0, &GateIssuesBlocked{Message: fmt.Sprintf(
				"gate %s: matched issue's author does not match the verified bot identity -- "+
					"refusing to reuse, needs human resolution", gate.GateID)}
		}
		ledger.setEntry(gate.GateID, gateLedgerEntry(gate, "reused", iid, verification.State, nil))
		if err := ledger.write(); err != nil {
			return nil, 0, &GateIssuesError{Message: err.Error()}
		}
		return ordered("gate_id", gate.GateID, "status", "reused",
			"issue_iid", iid, "issue_state", verification.State), iid, nil
	}

	// Written before the create, deliberately. A crash between the two leaves
	// a record saying an issue may exist that nobody has an id for -- which a
	// human can investigate. The other order leaves an issue on the forge that
	// no local record mentions.
	ledger.setEntry(gate.GateID, gateLedgerEntry(gate, "creating", nil, nil, nil))
	if err := ledger.write(); err != nil {
		return nil, 0, &GateIssuesError{Message: err.Error()}
	}

	iid, err := client.CreateIssue(projectPath, gate.Title, gate.Description,
		[]string{FixedLabel, gate.Label}, nil)
	if err != nil {
		return nil, 0, &GateIssuesError{Message: fmt.Sprintf(
			"gate %s: %s", pythonRepr(gate.GateID), err.Error())}
	}
	verification, err := client.FetchIssueVerification(projectPath, iid)
	if err != nil {
		return nil, 0, &GateIssuesError{Message: fmt.Sprintf(
			"gate %s: %s", pythonRepr(gate.GateID), err.Error())}
	}

	failures := verifyCreatedIssue(verification, gate.Title, projectPath, botUsername,
		[]string{FixedLabel, gate.Label}, nil)
	if len(failures) > 0 {
		ledger.setEntry(gate.GateID, gateLedgerEntry(gate, "suspect", iid, verification.State,
			"post-creation verification failed: "+strings.Join(failures, ", ")))
		if err := ledger.write(); err != nil {
			return nil, 0, &GateIssuesError{Message: err.Error()}
		}
		return nil, 0, &GateIssuesBlocked{Message: fmt.Sprintf(
			"gate %s: post-creation verification failed (%s) -- aborting the entire run immediately",
			gate.GateID, strings.Join(failures, ", "))}
	}

	ledger.setEntry(gate.GateID, gateLedgerEntry(gate, "created", iid, verification.State, nil))
	if err := ledger.write(); err != nil {
		return nil, 0, &GateIssuesError{Message: err.Error()}
	}
	return ordered("gate_id", gate.GateID, "status", "created",
		"issue_iid", iid, "issue_state", verification.State), iid, nil
}

func gateLedgerEntry(
	gate GatePlan, status string, iid, state any, detail any,
) *orderedObject {
	recordedAt := any(nowRFC3339())
	if status == "creating" {
		recordedAt = nil
	}
	return ordered(
		"kind", "gate", "gate_id", gate.GateID, "marker", gate.Marker, "status", status,
		"issue_iid", iid, "issue_state", state,
		"attempted_at", nowRFC3339(), "recorded_at", recordedAt, "detail", detail)
}

// verifyCreatedIssue compares what the forge stored against what was sent.
//
// Every field, not a sample. Each one is something an instance rule can change
// silently -- a label stripped, confidentiality applied, an assignee dropped --
// and each would leave this kernel recording an artifact that does not exist
// in the form it thinks.
func verifyCreatedIssue(
	verification *IssueVerification, title, projectPath, botUsername string,
	expectedLabels []string, expectedAssignees []string,
) []string {
	var failures []string
	if toStringOrEmpty(verification.Title) != title {
		failures = append(failures, "title")
	}
	if verification.State != "opened" {
		failures = append(failures, "state")
	}
	if !sameStringSet(labelStrings(verification.Labels), expectedLabels) {
		failures = append(failures, "labels")
	}
	if expectedAssignees == nil {
		// A gate issue must carry no assignee at all: it tracks a gate, not a
		// person, and an assignee would read as somebody being asked to act.
		if verification.AssigneeCount != 0 {
			failures = append(failures, "assignee_count")
		}
	} else if !sameStrings(lowercased(verification.AssigneeUsernames), lowercased(expectedAssignees)) {
		failures = append(failures, "assignee_usernames")
	}
	if verification.Confidential {
		failures = append(failures, "confidential")
	}
	if toStringOrEmpty(verification.ProjectPath) != projectPath {
		failures = append(failures, "project_path")
	}
	if !strings.EqualFold(toStringOrEmpty(verification.AuthorUsername), botUsername) {
		failures = append(failures, "author_username")
	}
	return failures
}

func labelStrings(labels []any) []string {
	out := []string{}
	for _, raw := range labels {
		if label, ok := raw.(string); ok {
			out = append(out, label)
		}
	}
	return out
}

func lowercased(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strings.ToLower(value))
	}
	return out
}

// loadIssueLedgerFile reads the gate-issues sidecar, or starts an empty one.
func loadIssueLedgerFile(path, taskID string) (*orderedObject, error) {
	ledger := ordered(
		"schema_version", ledgerSchemaVersion,
		"task_id", taskID,
		"project_path", nil,
		"bot_username", nil,
		"mocked", false,
		"entries", &orderedObject{values: map[string]any{}},
	)
	data, err := os.ReadFile(path)
	if err != nil {
		// No ledger yet is the ordinary first-run case.
		return ledger, nil
	}
	decoded, err := DecodeOrdered(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	object, ok := decoded.(*orderedObject)
	if !ok {
		return ledger, nil
	}
	for _, key := range object.keys {
		ledger.set(key, object.values[key])
	}
	return ledger, nil
}
