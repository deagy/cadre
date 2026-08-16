package kernel

import (
	"fmt"
	"os"
	"strings"
)

// Recording an approval that happened on a forge, and linking a source issue.
//
// These are the adapters that turn a review somebody left on a pull request,
// or an approval on a merge request, into gate approval evidence. They are
// *trusted-API attestations*: they take the forge's own record of who approved
// what as authoritative, and do not attempt independent signing or
// non-repudiation beyond it. What they do enforce is the binding -- the
// reviewer's login must be the one the project assigned to that authority, so
// somebody else's approval on the same pull request cannot be recorded as this
// authority's.
//
// The gate itself is only marked approved when canMarkGateApproved agrees, on
// the whole record: every applicable authority present, none of them a
// preparer or the verifier. Recording one approval never approves a gate by
// itself.
//
// **The link commands are not approvals and cannot become one.** Linking an
// issue as G1's intent record or G2's requirements baseline writes an evidence
// ref and a record field, and touches human_approvals and gate status not at
// all. The authorization is the same -- only an assigned, applicable authority
// for the gate may attach one -- but there is no path from here to an approved
// gate.

// GithubApprovalRequest is one `approve-from-github` invocation.
type GithubApprovalRequest struct {
	Root          string
	TaskID        string
	GateID        string
	AuthorityRole string
	Repo          string
	PullRequest   int
	ReviewID      int
	ReviewerLogin string
	CommitSHA     string
	DecidedAt     string
}

// GitlabApprovalRequest is one `approve-from-gitlab` invocation.
type GitlabApprovalRequest struct {
	Root             string
	TaskID           string
	GateID           string
	AuthorityRole    string
	ProjectPath      string
	MergeRequestIID  int
	ApprovalID       string
	ApproverUsername string
	CommitSHA        string
	DecidedAt        string
}

// approvalContext is what every one of these commands loads first.
type approvalContext struct {
	taskID     string
	recordPath string
	record     *orderedObject
	overlay    *ProjectOverlay
}

func loadApprovalContext(root, taskID string) (*approvalContext, error) {
	resolved, err := resolveExisting(root)
	if err != nil {
		return nil, err
	}
	safeTask, err := SafeTaskID(taskID)
	if err != nil {
		return nil, err
	}
	overlay, err := LoadOverlay(resolved)
	if err != nil {
		return nil, err
	}
	recordPath, err := ConfinedPath(resolved, Overlay, "runs", safeTask, "run-record.json")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return nil, err
	}
	decoded, err := DecodeOrdered(data)
	if err != nil {
		return nil, err
	}
	record, ok := decoded.(*orderedObject)
	if !ok {
		return nil, fmt.Errorf("%s: run record is not a JSON object", recordPath)
	}
	return &approvalContext{taskID: safeTask, recordPath: recordPath,
		record: record, overlay: overlay}, nil
}

// classificationFor is the classification an evidence ref carries.
func (c *approvalContext) classificationFor() any {
	classification := c.record.values["classification"]
	if emptyString(classification) {
		classification = c.overlay.Project["classification"]
	}
	if emptyString(classification) {
		classification = "internal"
	}
	return classification
}

// RecordGithubApproval records a GitHub PR review as gate approval evidence.
func (r *Registry) RecordGithubApproval(
	request GithubApprovalRequest,
) (*orderedObject, error) {
	context, err := loadApprovalContext(request.Root, request.TaskID)
	if err != nil {
		return nil, err
	}
	if _, err := ApprovalSourcePolicy(context.overlay.Project); err != nil {
		return nil, err
	}
	gate, requirement, expectedAssignee, err := resolveGateAuthority(
		context.record, context.overlay.Authorities, request.GateID, request.AuthorityRole)
	if err != nil {
		return nil, err
	}

	// The binding. Without it, anyone's approval on the pull request could be
	// filed as this authority's.
	authority, _ := context.overlay.Authorities[request.AuthorityRole].(map[string]any)
	expectedLogin := AuthorityForgeLogin(authority, "github")
	reviewerLogin := strings.ToLower(request.ReviewerLogin)
	if expectedLogin != "" && reviewerLogin != strings.ToLower(expectedLogin) {
		return nil, fmt.Errorf(
			"GitHub reviewer %s does not match assigned authority login %s",
			request.ReviewerLogin, expectedLogin)
	}

	reviewURI := fmt.Sprintf("github-review:%s:pull/%d:review/%d:reviewer/%s",
		request.Repo, request.PullRequest, request.ReviewID, reviewerLogin)
	if _, wellFormed := GitHubReviewLogin(reviewURI); !wellFormed {
		return nil, fmt.Errorf("invalid GitHub review URI components for %s", reviewURI)
	}

	decidedAt := request.DecidedAt
	if decidedAt == "" {
		decidedAt = nowRFC3339()
	}
	if !IsValidDatetime(decidedAt) {
		return nil, fmt.Errorf("--decided-at must be a valid RFC 3339 date-time")
	}

	digest, err := Fingerprint(map[string]any{
		"task_id": context.taskID, "gate_id": request.GateID,
		"authority_id": request.AuthorityRole, "repo": request.Repo,
		"pull": request.PullRequest, "review_id": request.ReviewID,
		"reviewer_login": request.ReviewerLogin, "decided_at": decidedAt,
		"commit_sha": request.CommitSHA,
	})
	if err != nil {
		return nil, err
	}

	evidenceID := fmt.Sprintf("%s-%s-github-review-%d",
		strings.ToLower(request.GateID), request.AuthorityRole, request.ReviewID)
	result, err := context.applyApproval(gate, requirement, expectedAssignee,
		decidedAt, evidenceID, reviewURI, digest)
	if err != nil {
		return nil, err
	}
	result.set("review_uri", reviewURI)
	return context.finishApproval(result, gate, request.GateID, request.AuthorityRole)
}

// RecordGitlabApproval records a GitLab MR approval as gate approval evidence.
//
// Same trust level as the GitHub adapter: GitLab's own approval state is taken
// as authoritative. Only the pseudonymous username reaches the evidence record
// or the URI -- never a name, an email, or an avatar.
func (r *Registry) RecordGitlabApproval(
	request GitlabApprovalRequest,
) (*orderedObject, error) {
	context, err := loadApprovalContext(request.Root, request.TaskID)
	if err != nil {
		return nil, err
	}
	if _, err := ApprovalSourcePolicy(context.overlay.Project); err != nil {
		return nil, err
	}
	gate, requirement, expectedAssignee, err := resolveGateAuthority(
		context.record, context.overlay.Authorities, request.GateID, request.AuthorityRole)
	if err != nil {
		return nil, err
	}

	authority, _ := context.overlay.Authorities[request.AuthorityRole].(map[string]any)
	expectedUsername := AuthorityForgeLogin(authority, "gitlab")
	approverUsername := strings.ToLower(request.ApproverUsername)
	if expectedUsername != "" && approverUsername != strings.ToLower(expectedUsername) {
		return nil, fmt.Errorf(
			"GitLab approver %s does not match assigned authority username %s",
			request.ApproverUsername, expectedUsername)
	}

	approvalURI := fmt.Sprintf("gitlab-mr:%s:merge_requests/%d:approval/%s:approver/%s",
		request.ProjectPath, request.MergeRequestIID, request.ApprovalID, approverUsername)
	if _, wellFormed := GitLabMRUsername(approvalURI); !wellFormed {
		return nil, fmt.Errorf("invalid GitLab MR approval URI components for %s", approvalURI)
	}

	decidedAt := request.DecidedAt
	if decidedAt == "" {
		decidedAt = nowRFC3339()
	}
	if !IsValidDatetime(decidedAt) {
		return nil, fmt.Errorf("--decided-at must be a valid RFC 3339 date-time")
	}

	digest, err := Fingerprint(map[string]any{
		"task_id": context.taskID, "gate_id": request.GateID,
		"authority_id": request.AuthorityRole, "project_path": request.ProjectPath,
		"merge_request_iid": request.MergeRequestIID, "approval_id": request.ApprovalID,
		"approver_username": approverUsername, "decided_at": decidedAt,
		"commit_sha": request.CommitSHA,
	})
	if err != nil {
		return nil, err
	}

	evidenceID := fmt.Sprintf("%s-%s-gitlab-mr-%s",
		strings.ToLower(request.GateID), request.AuthorityRole, request.ApprovalID)
	result, err := context.applyApproval(gate, requirement, expectedAssignee,
		decidedAt, evidenceID, approvalURI, digest)
	if err != nil {
		return nil, err
	}
	result.set("approval_uri", approvalURI)
	return context.finishApproval(result, gate, request.GateID, request.AuthorityRole)
}

// applyApproval writes the approval into the gate.
//
// Everything from here down is identical for both forges. Only the URI shape,
// the evidence id and what went into the hash differ, which is why those are
// the parameters.
func (c *approvalContext) applyApproval(
	gate, requirement *orderedObject, expectedAssignee, decidedAt,
	evidenceID, evidenceURI, digest string,
) (*orderedObject, error) {
	roleLabel := requirement.values["role"]
	approval := ordered(
		"status", "approved",
		"approver", ordered("id", expectedAssignee, "role", roleLabel, "kind", "human"),
		"decided_at", decidedAt,
		"evidence_refs", []any{ordered(
			"evidence_id", evidenceID,
			"uri", evidenceURI,
			"hash_algorithm", "sha256",
			"hash", strings.TrimPrefix(digest, "sha256:"),
			"classification", c.classificationFor(),
		)},
	)
	gate.set("human_approvals", replaceApprovalEntry(
		listOf(gate.values["human_approvals"]), expectedAssignee, roleLabel, approval))

	contracts, err := lifecycleGateContracts()
	if err != nil {
		return nil, err
	}
	// One approval is not a gate. This asks the whole record whether every
	// applicable authority has now approved, and whether none of them is a
	// preparer or the verifier.
	if canMarkGateApproved(c.record, gate, c.overlay.Authorities) {
		gate.set("status", "approved")
		gate.set("decided_at", latestApprovalTime(gate, decidedAt))
		c.record.set("current_lifecycle_phase", deriveCurrentPhase(c.record, contracts))
	}
	return ordered("task_id", c.taskID), nil
}

// finishApproval persists the record and completes the report.
//
// The report's key order matches the Python kernel's, which builds it as one
// literal: task, gate, authority, the forge URI, then the gate's state. The
// URI was already set by the caller, between the authority and the status.
func (c *approvalContext) finishApproval(
	result, gate *orderedObject, gateID, authorityRole string,
) (*orderedObject, error) {
	contracts, err := lifecycleGateContracts()
	if err != nil {
		return nil, err
	}
	if err := writeJSONDocument(c.recordPath, c.record); err != nil {
		return nil, err
	}
	ordered := ordered(
		"task_id", c.taskID,
		"gate_id", gateID,
		"authority_id", authorityRole,
	)
	// The URI key, whichever this forge called it.
	for _, key := range []string{"review_uri", "approval_uri"} {
		if value, present := result.values[key]; present {
			ordered.set(key, value)
		}
	}
	ordered.set("gate_status", gate.values["status"])
	ordered.set("current_phase", deriveCurrentPhase(c.record, contracts))
	return ordered, nil
}

// SourceLinkRequest is one `link-*-from-*-issue` invocation.
type SourceLinkRequest struct {
	Root          string
	TaskID        string
	GateID        string
	AuthorityRole string
	// Exactly one of these is set, and it decides the forge.
	ProjectPath string
	Repo        string
	IssueNumber int
}

// RecordSourceIssueLink links a forge issue as a gate's recorded source.
//
// Never an approval. It writes an evidence ref and the gate's record field,
// and leaves human_approvals and the gate's status exactly as they were --
// there is no branch here that can mark a gate approved.
func (r *Registry) RecordSourceIssueLink(
	request SourceLinkRequest, issue *SourceIssue,
) (*orderedObject, error) {
	recordField, accepts := recordFieldByGate[request.GateID]
	sourceLabel := "GitLab issue"
	if request.Repo != "" {
		sourceLabel = "GitHub issue"
	}
	if !accepts {
		return nil, fmt.Errorf("gate %s does not accept a %s source link",
			request.GateID, sourceLabel)
	}

	context, err := loadApprovalContext(request.Root, request.TaskID)
	if err != nil {
		return nil, err
	}

	var gate *orderedObject
	for _, raw := range listOf(context.record.values["lifecycle_gates"]) {
		item, ok := raw.(*orderedObject)
		if ok && item.values["gate_id"] == request.GateID {
			gate = item
			break
		}
	}
	if gate == nil {
		return nil, fmt.Errorf("unknown gate in run record: %s", request.GateID)
	}
	// Same authorization as an approval: an assigned authority the gate
	// actually requires, and one whose requirement is applicable. What it
	// cannot do is what an approval does next.
	if _, _, _, err := resolveGateAuthority(context.record, context.overlay.Authorities,
		request.GateID, request.AuthorityRole); err != nil {
		return nil, err
	}

	var issueURI, evidenceInfix string
	var payload map[string]any
	if request.Repo != "" {
		issueURI = fmt.Sprintf("github-issue:%s:issues/%d", request.Repo, issue.Number)
		evidenceInfix = "github-issue"
		payload = map[string]any{
			"task_id": context.taskID, "gate_id": request.GateID,
			"authority_id": request.AuthorityRole, "repo": request.Repo,
			"issue_number": issue.Number, "title": issue.Title,
			"state": issue.State, "web_url": issue.WebURL,
		}
		if !IsGitHubIssueURI(issueURI) {
			return nil, fmt.Errorf("invalid %s URI components for %s", sourceLabel, issueURI)
		}
	} else {
		issueURI = fmt.Sprintf("gitlab-issue:%s:issues/%d", request.ProjectPath, issue.Number)
		evidenceInfix = "gitlab-issue"
		payload = map[string]any{
			"task_id": context.taskID, "gate_id": request.GateID,
			"authority_id": request.AuthorityRole, "project_path": request.ProjectPath,
			"issue_iid": issue.Number, "title": issue.Title,
			"state": issue.State, "web_url": issue.WebURL,
		}
		if !IsGitLabIssueURI(issueURI) {
			return nil, fmt.Errorf("invalid %s URI components for %s", sourceLabel, issueURI)
		}
	}

	digest, err := Fingerprint(payload)
	if err != nil {
		return nil, err
	}
	prefix := fmt.Sprintf("%s-source-%s-", strings.ToLower(request.GateID), evidenceInfix)
	entry := ordered(
		"evidence_id", fmt.Sprintf("%s%d", prefix, issue.Number),
		"uri", issueURI,
		"hash_algorithm", "sha256",
		"hash", strings.TrimPrefix(digest, "sha256:"),
		"classification", context.classificationFor(),
	)

	// Dropped by prefix rather than by exact id. The record field holds one
	// URI at a time, so relinking a *different* issue has to replace the old
	// source evidence rather than accumulate beside it -- matching on the
	// exact id would only replace a re-link of the same issue.
	remaining := []any{}
	for _, raw := range listOf(gate.values["evidence_refs"]) {
		existing, ok := raw.(*orderedObject)
		if ok && strings.HasPrefix(toStringOrEmpty(existing.values["evidence_id"]), prefix) {
			continue
		}
		remaining = append(remaining, raw)
	}
	gate.set("evidence_refs", append(remaining, entry))
	context.record.set(recordField, issueURI)

	if err := writeJSONDocument(context.recordPath, context.record); err != nil {
		return nil, err
	}
	return ordered(
		"task_id", context.taskID,
		"gate_id", request.GateID,
		"record_field", recordField,
		"issue_uri", issueURI,
		"issue_title", issue.Title,
		"issue_state", issue.State,
	), nil
}
