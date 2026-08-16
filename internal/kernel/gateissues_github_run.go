package kernel

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Running `create-github-gate-issues`.
//
// The shape mirrors the GitLab run loop -- plan, digest handshake, lock, then
// one gate at a time with its approvals, re-checking the digest before each --
// and diverges in the four places GitHub forces it to. Each is marked below.

// GithubGateIssuesRequest is one `create-github-gate-issues` invocation.
//
// No link-type field, unlike the GitLab request: GitHub has no Issue Links
// API, and the parent line in an approval issue's description is the whole
// linkage. AllowPublicRepo has no GitLab counterpart either -- see the
// pre-flight below.
type GithubGateIssuesRequest struct {
	Root                string
	TaskID              string
	Repo                string
	AsBot               string
	Gates               []string
	Apply               bool
	PlanDigest          string
	AllowClassification string
	IncludeScope        bool
	ReconcileAssignees  bool
	AllowPublicRepo     bool
	BreakLock           bool
	KnowinglyMocked     bool
}

// githubApprovalRefusal is one authority this run cannot make an issue for.
//
// Carried as an error rather than a return value so the two forge calls that
// discover it -- the user lookup and the collaborator lookup -- can abandon
// the item from wherever they are. Never escapes: the run loop folds it into
// refusals and carries on with the rest of the task.
type githubApprovalRefusal struct {
	GateID      string
	AuthorityID string
	Reason      string
	Detail      string
}

func (e *githubApprovalRefusal) Error() string { return e.Detail }

// githubMutations counts mutative calls so they can be spaced.
//
// Never delays before the first one, never between reads, and never on a dry
// run, which does not reach this code at all. Counting here rather than
// sleeping inside each writer keeps the number of delays a run makes visible
// in one place.
type githubMutations struct{ count int }

func (m *githubMutations) next() {
	if m.count > 0 {
		DelayBetweenMutations()
	}
	m.count++
}

// CreateGithubGateIssues plans and, with Apply, creates the issues.
func (r *Registry) CreateGithubGateIssues(
	request GithubGateIssuesRequest,
) (*orderedObject, error) {
	root, err := resolveExisting(request.Root)
	if err != nil {
		return nil, &GateIssuesGithubError{Message: err.Error()}
	}
	taskID, err := SafeTaskID(request.TaskID)
	if err != nil {
		return nil, &GateIssuesGithubError{Message: err.Error()}
	}

	record, dispatchPlan, authorities, err := loadTaskContext(root, taskID)
	if err != nil {
		return nil, &GateIssuesGithubError{Message: err.Error()}
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		return nil, &GateIssuesGithubError{Message: err.Error()}
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
		return nil, &GateIssuesGithubError{Message: fmt.Sprintf(
			"--allow-classification must be supplied and exactly match the task's classification "+
				"(got %s, task classification is %s)",
			pythonRepr(nonEmptyOrNil(request.AllowClassification)), pythonRepr(record["classification"]))}
	}

	var gateIDs []string
	if len(request.Gates) > 0 {
		for _, gateID := range request.Gates {
			if err := CheckGateEligibility(gateID, dispatchPlan, gateByID[gateID]); err != nil {
				var eligibility *GateEligibilityError
				if errors.As(err, &eligibility) && eligibility.NeedsHuman {
					return nil, &GateIssuesGithubBlocked{Message: err.Error()}
				}
				return nil, &GateIssuesGithubError{Message: err.Error()}
			}
			gateIDs = append(gateIDs, gateID)
		}
	} else {
		gateIDs = defaultGateIDs(dispatchPlan, gateByID)
	}

	plan, digest, err := r.githubPlanAndDigest(taskID, request, gateIDs, record, authorities,
		dispatchPlan, contracts)
	if err != nil {
		return nil, err
	}

	issues, err := NewGitHubIssueClient()
	if err != nil {
		return nil, &GateIssuesGithubError{Message: err.Error()}
	}
	reads, err := NewGitHubClient()
	if err != nil {
		return nil, &GateIssuesGithubError{Message: err.Error()}
	}
	// Either mock makes the run mocked. The two are separate files so a test
	// can arrange reads and writes independently, but a run answering from
	// even one of them has not touched GitHub.
	mocked := reads.Mocked() || issues.Mocked()

	if !request.Apply {
		return githubDryRunReport(digest, request.Repo, gateIDs, mocked, plan), nil
	}

	if request.PlanDigest == "" {
		return nil, &GateIssuesGithubError{
			Message: "--apply requires --plan-digest (from a prior --dry-run)"}
	}
	if request.PlanDigest != digest {
		return nil, &GateIssuesGithubBlocked{Message: fmt.Sprintf(
			"--plan-digest mismatch: recomputed %s != supplied %s -- state changed since "+
				"the --dry-run this digest came from; re-run --dry-run",
			pythonRepr(digest), pythonRepr(request.PlanDigest))}
	}
	if mocked && !request.KnowinglyMocked {
		return nil, &GateIssuesGithubError{Message: fmt.Sprintf(
			"%s or %s is set but --i-know-this-is-mocked was not passed -- refusing to --apply "+
				"against a mocked GitHub backend",
			pythonRepr(GitHubReadMockEnv), pythonRepr(GitHubIssueMockEnv))}
	}

	verifiedLogin, err := reads.VerifyIdentity(request.AsBot)
	if err != nil {
		return nil, &GateIssuesGithubError{Message: err.Error()}
	}

	// **Difference 1: the repository pre-flight.** GitLab has a per-issue
	// confidential flag and this command checks it after every create. GitHub
	// has none, so who can read a gate issue is decided entirely by the
	// repository -- and that has to be settled before the first one exists,
	// not after.
	repoInfo, err := issues.FetchRepo(request.Repo)
	if err != nil {
		return nil, &GateIssuesGithubError{Message: err.Error()}
	}
	if repoInfo["has_issues"] == false {
		return nil, &GateIssuesGithubError{Message: fmt.Sprintf(
			"issues are disabled on repository %s", pythonRepr(request.Repo))}
	}
	if repoInfo["private"] == false && !request.AllowPublicRepo {
		return nil, &GateIssuesGithubError{Message: fmt.Sprintf(
			"repository %s is public and --allow-public-repo was not passed -- gate/approval issues "+
				"carry gate names, phases, sanitized rationale, and authority role labels, and GitHub "+
				"has no per-issue confidential flag; pass --allow-public-repo to proceed anyway",
			pythonRepr(request.Repo))}
	}

	return r.applyGithubGateIssues(root, taskID, request, gateIDs, plan, digest,
		issues, reads, verifiedLogin, mocked, contracts)
}

func (r *Registry) githubPlanAndDigest(
	taskID string, request GithubGateIssuesRequest, gateIDs []string,
	record, authorities, dispatchPlan map[string]any,
	contracts map[string]map[string]any,
) (*issuePlan, string, error) {
	var scope any
	if request.IncludeScope {
		scope = record["scope"]
	}
	plan, err := buildIssuePlan(githubIssueForge, taskID, request.Repo, gateIDs, record,
		authorities, contracts, request.IncludeScope, scope)
	if err != nil {
		return nil, "", err
	}
	digest, err := ComputePlanDigest(githubIssueForge, taskID, request.Repo, gateIDs,
		dispatchPlan["dispatch_fingerprint"], plan.perGate,
		record["disposition"], record["classification"],
		len(listOf(record["re_entry_history"])))
	if err != nil {
		return nil, "", &GateIssuesGithubError{Message: err.Error()}
	}
	return plan, digest, nil
}

// githubFreshDigest re-derives the digest from what is on disk right now.
func (r *Registry) githubFreshDigest(
	root, taskID string, request GithubGateIssuesRequest, gateIDs []string,
	contracts map[string]map[string]any,
) (string, error) {
	record, dispatchPlan, authorities, err := loadTaskContext(root, taskID)
	if err != nil {
		return "", &GateIssuesGithubError{Message: err.Error()}
	}
	_, digest, err := r.githubPlanAndDigest(taskID, request, gateIDs, record, authorities,
		dispatchPlan, contracts)
	return digest, err
}

func (r *Registry) checkGithubDigestUnchanged(
	root, taskID string, request GithubGateIssuesRequest, gateIDs []string,
	contracts map[string]map[string]any, expected, context string,
) error {
	fresh, err := r.githubFreshDigest(root, taskID, request, gateIDs, contracts)
	if err != nil {
		return err
	}
	if fresh != expected {
		return &GateIssuesGithubBlocked{Message: fmt.Sprintf(
			"plan digest changed before %s (a concurrent edit happened) -- aborting "+
				"remaining items; already-created issues are unaffected", context)}
	}
	return nil
}

func githubDryRunReport(
	digest, repo string, gateIDs []string, mocked bool, plan *issuePlan,
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
		"repo", repo,
		"gate_ids", asJSONList(gateIDs),
		"mocked", mocked,
		"gate_issues", gateIssues,
		"approval_issues", approvalIssues,
		"skipped", skippedEntries(plan.skipped),
		"refusals", refusalEntries(plan.refusals),
	)
}

// applyGithubGateIssues walks the plan, creating or reusing each issue.
func (r *Registry) applyGithubGateIssues(
	root, taskID string, request GithubGateIssuesRequest, gateIDs []string,
	plan *issuePlan, digest string, issues *GitHubIssueClient, reads *GitHubClient,
	verifiedLogin string, mocked bool, contracts map[string]map[string]any,
) (*orderedObject, error) {
	lockPath, err := LedgerPath(root, Overlay, taskID, githubLockFile)
	if err != nil {
		return nil, &GateIssuesGithubError{Message: err.Error()}
	}
	if err := AcquireLockFile(lockPath, request.BreakLock); err != nil {
		return nil, &GateIssuesGithubBlocked{Message: err.Error()}
	}
	defer func() { _ = ReleaseLockFile(lockPath) }()

	ledger, err := r.openGithubIssueLedger(root, taskID, request.Repo, verifiedLogin, mocked)
	if err != nil {
		return nil, err
	}

	approvalsByGate := map[string][]ApprovalCandidate{}
	for _, approval := range plan.approvals {
		approvalsByGate[approval.GateID] = append(approvalsByGate[approval.GateID], approval)
	}

	mutations := &githubMutations{}
	gateResults := []any{}
	approvalResults := []any{}
	refusals := append([]RefusalEntry{}, plan.refusals...)
	driftDetected := false

	for _, gate := range plan.gates {
		if err := r.checkGithubDigestUnchanged(root, taskID, request, gateIDs, contracts, digest,
			fmt.Sprintf("gate %s", pythonRepr(gate.GateID))); err != nil {
			return nil, err
		}
		result, gateNumber, err := processGithubGateIssue(
			issues, ledger, request.Repo, gate, verifiedLogin, mocked, mutations)
		if err != nil {
			return nil, err
		}
		gateResults = append(gateResults, result)

		for _, approval := range approvalsByGate[gate.GateID] {
			if err := r.checkGithubDigestUnchanged(root, taskID, request, gateIDs, contracts, digest,
				fmt.Sprintf("approval %s/%s", gate.GateID, approval.AuthorityID)); err != nil {
				return nil, err
			}
			result, drift, err := processGithubApprovalIssue(issues, reads, ledger, taskID,
				request.Repo, approval, gateNumber, verifiedLogin, mocked,
				request.ReconcileAssignees, mutations)
			var refusal *githubApprovalRefusal
			if errors.As(err, &refusal) {
				refusals = append(refusals, RefusalEntry{
					refusal.GateID, refusal.AuthorityID, refusal.Reason, refusal.Detail})
				continue
			}
			if err != nil {
				return nil, err
			}
			approvalResults = append(approvalResults, result)
			driftDetected = driftDetected || drift
		}
	}

	if gateIDs == nil {
		gateIDs = []string{}
	}
	return ordered(
		"mode", "apply",
		"plan_digest", digest,
		"repo", request.Repo,
		"gate_ids", asJSONList(gateIDs),
		"mocked", mocked,
		"bot_login", verifiedLogin,
		"gate_results", gateResults,
		"approval_results", approvalResults,
		"skipped", skippedEntries(plan.skipped),
		"refusals", refusalEntries(refusals),
		"drift_detected", driftDetected,
	), nil
}

func (r *Registry) openGithubIssueLedger(
	root, taskID, repo, botLogin string, mocked bool,
) (*issueLedger, error) {
	path, err := LedgerPath(root, Overlay, taskID, githubLedgerFile)
	if err != nil {
		return nil, &GateIssuesGithubError{Message: err.Error()}
	}
	object, err := loadGithubIssueLedgerFile(path, taskID)
	if err != nil {
		return nil, &GateIssuesGithubError{Message: err.Error()}
	}
	object.set("schema_version", ledgerSchemaVersion)
	object.set("task_id", taskID)
	object.set("repo", repo)
	object.set("bot_login", botLogin)
	object.set("mocked", mocked)

	entries, ok := object.values["entries"].(*orderedObject)
	if !ok {
		entries = &orderedObject{values: map[string]any{}}
	}
	return &issueLedger{path: path, root: root, taskID: taskID,
		entries: entries, object: object}, nil
}

// loadGithubIssueLedgerFile reads the sidecar, or starts an empty one.
//
// Its own skeleton rather than the GitLab command's: this one records a repo
// and a bot login where that one records a project path and a username, and
// the difference is what an operator reads to find out which forge a run
// touched.
func loadGithubIssueLedgerFile(path, taskID string) (*orderedObject, error) {
	ledger := ordered(
		"schema_version", ledgerSchemaVersion,
		"task_id", taskID,
		"repo", nil,
		"bot_login", nil,
		"mocked", false,
		"entries", &orderedObject{values: map[string]any{}},
	)
	data, err := os.ReadFile(path)
	if err != nil {
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

// githubLedgerWrite persists the ledger under this command's temp prefix.
func githubLedgerWrite(ledger *issueLedger) error {
	ledger.object.set("entries", ledger.entries)
	return WriteLedgerFile(ledger.path, ledger.object, ".gate-issues-github.")
}

func githubGateLedgerEntry(
	gate GatePlan, status string, number, state, detail any,
) *orderedObject {
	recordedAt := any(nowRFC3339())
	if status == "creating" {
		recordedAt = nil
	}
	return ordered(
		"kind", "gate", "gate_id", gate.GateID, "marker", gate.Marker, "status", status,
		"issue_number", number, "issue_state", state,
		"attempted_at", nowRFC3339(), "recorded_at", recordedAt, "detail", detail)
}

func githubApprovalLedgerEntry(
	approval ApprovalCandidate, status string, number, state, detail any,
) *orderedObject {
	recordedAt := any(nowRFC3339())
	if status == "creating" {
		recordedAt = nil
	}
	return ordered(
		"kind", "approval", "gate_id", approval.GateID, "authority_id", approval.AuthorityID,
		"marker", approval.Marker, "status", status,
		"issue_number", number, "issue_state", state,
		"attempted_at", nowRFC3339(), "recorded_at", recordedAt, "detail", detail)
}

// githubStructuralError wraps a forge failure with which item it happened on.
func githubStructuralError(context string, err error) error {
	var blocked *GateIssuesGithubBlocked
	if errors.As(err, &blocked) {
		return err
	}
	var structural *GateIssuesGithubError
	if errors.As(err, &structural) {
		return err
	}
	return &GateIssuesGithubError{Message: fmt.Sprintf("%s: %s", context, err.Error())}
}

// processGithubGateIssue creates or reuses one gate's tracking issue.
func processGithubGateIssue(
	issues *GitHubIssueClient, ledger *issueLedger, repo string,
	gate GatePlan, botLogin string, mocked bool, mutations *githubMutations,
) (*orderedObject, int, error) {
	context := "gate " + gate.GateID
	wrap := "gate " + pythonRepr(gate.GateID)

	matches, err := issues.SearchIssuesByLabel(repo, gate.Label)
	if err != nil {
		return nil, 0, githubStructuralError(wrap, err)
	}
	// **Difference 2: what a search result can mean.** A pull request, or a
	// full page, or more than one issue -- each blocks rather than being
	// resolved.
	if err := checkSearchResults(matches, context); err != nil {
		return nil, 0, err
	}

	if len(matches) == 1 {
		entry, _ := matches[0].(map[string]any)
		if err := validateMatchedGithubIssue(entry, gate.Label, githubGateLabelPrefix, context); err != nil {
			return nil, 0, err
		}
		number, _ := jsonNumber(entry["number"])
		verification, err := issues.FetchIssueVerification(repo, number)
		if err != nil {
			return nil, 0, githubStructuralError(wrap, err)
		}
		if !strings.EqualFold(toStringOrEmpty(verification.AuthorLogin), botLogin) {
			ledger.setEntry(gate.GateID, githubGateLedgerEntry(gate, "suspect", number,
				verification.State, "matched issue author does not match the verified bot identity"))
			if err := githubLedgerWrite(ledger); err != nil {
				return nil, 0, &GateIssuesGithubError{Message: err.Error()}
			}
			return nil, 0, &GateIssuesGithubBlocked{Message: fmt.Sprintf(
				"%s: matched issue's author does not match the verified bot identity -- refusing to "+
					"reuse, needs human resolution", context)}
		}
		ledger.setEntry(gate.GateID, githubGateLedgerEntry(gate, "reused", number,
			verification.State, nil))
		if err := githubLedgerWrite(ledger); err != nil {
			return nil, 0, &GateIssuesGithubError{Message: err.Error()}
		}
		return ordered("gate_id", gate.GateID, "status", "reused",
			"issue_number", number, "issue_state", verification.State), number, nil
	}

	// Written before the create, so a crash between the two leaves a record
	// saying an issue may exist rather than an issue nothing local mentions.
	ledger.setEntry(gate.GateID, githubGateLedgerEntry(gate, "creating", nil, nil, nil))
	if err := githubLedgerWrite(ledger); err != nil {
		return nil, 0, &GateIssuesGithubError{Message: err.Error()}
	}

	mutations.next()
	if err := issues.EnsureLabel(repo, gate.Label); err != nil {
		return nil, 0, githubStructuralError(wrap, err)
	}
	mutations.next()
	number, err := issues.CreateIssue(repo, gate.Title, gate.Description,
		[]string{FixedLabel, gate.Label}, nil)
	if err != nil {
		return nil, 0, githubStructuralError(wrap, err)
	}
	verification, err := issues.FetchIssueVerification(repo, number)
	if err != nil {
		return nil, 0, githubStructuralError(wrap, err)
	}

	failures := verifyCreatedGithubIssue(verification, gate.Title, repo, botLogin,
		[]string{FixedLabel, gate.Label}, nil)
	if len(failures) > 0 {
		ledger.setEntry(gate.GateID, githubGateLedgerEntry(gate, "suspect", number,
			verification.State, "post-creation verification failed: "+strings.Join(failures, ", ")))
		if err := githubLedgerWrite(ledger); err != nil {
			return nil, 0, &GateIssuesGithubError{Message: err.Error()}
		}
		return nil, 0, &GateIssuesGithubBlocked{Message: fmt.Sprintf(
			"%s: post-creation verification failed (%s) -- aborting the entire run immediately",
			context, strings.Join(failures, ", "))}
	}

	ledger.setEntry(gate.GateID, githubGateLedgerEntry(gate, "created", number,
		verification.State, nil))
	if err := githubLedgerWrite(ledger); err != nil {
		return nil, 0, &GateIssuesGithubError{Message: err.Error()}
	}
	return ordered("gate_id", gate.GateID, "status", "created",
		"issue_number", number, "issue_state", verification.State), number, nil
}
