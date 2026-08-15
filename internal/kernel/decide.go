package kernel

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// `agentic-sdlc decide` -- record a human's decision on a lifecycle gate.
//
// This is the write path an approval actually travels, so it is the one place
// in the kernel where a defect would not merely miss a problem but manufacture
// one: a run record saying somebody approved something.
//
// Every refusal below is therefore synchronous, at write time, rather than
// left to `validate` afterwards. `validate` is a second line -- it reads a
// record that already exists and asks whether it could be true. If the only
// check were there, a forged approval would sit on disk in the meantime, and
// anything reading the record before the next validation would believe it.
//
// The four refusals, in the order they matter:
//
//   - The actor must be exactly the identity assigned to that authority. Not
//     somebody holding the role, not somebody with the right job title -- the
//     assignee named in the project's own authority map.
//   - The actor cannot be a preparer of the gate's work.
//   - The actor cannot be the independent verifier either. Having confirmed
//     the work is what made them the verifier; approving it too collapses two
//     roles the lifecycle keeps apart.
//   - An approval under a policy that requires forge review must cite a
//     well-formed review URI. Otherwise "approved, evidence: trust me" would
//     satisfy a project that configured the opposite.
//
// And one thing this command deliberately cannot do: mark a gate approved on
// its own say-so. `canMarkGateApproved` re-derives that from the whole record
// -- every prerequisite gate approved, applicability settled, artifacts and
// evidence bound, an independent verifier declared, and *every* applicable
// human authority having approved. One authority's decision is one input to
// that, never the conclusion.

// DecideRequest is one `decide` invocation.
type DecideRequest struct {
	Root          string
	TaskID        string
	GateID        string
	AuthorityRole string
	Decision      string
	ActorID       string
	EvidenceURI   string
	Note          string
	DecidedAt     string
}

var decisionValues = map[string]bool{
	"approved": true, "rejected": true, "request-changes": true,
}

// Decide records a gate decision and returns the summary the CLI prints.
func (r *Registry) Decide(request DecideRequest) (*orderedObject, error) {
	if !decisionValues[request.Decision] {
		return nil, fmt.Errorf("unknown decision: %s", request.Decision)
	}
	root, err := resolveExisting(request.Root)
	if err != nil {
		return nil, err
	}
	taskID, err := SafeTaskID(request.TaskID)
	if err != nil {
		return nil, err
	}
	overlay, err := LoadOverlay(root)
	if err != nil {
		return nil, err
	}
	policy, err := ApprovalSourcePolicy(overlay.Project)
	if err != nil {
		return nil, err
	}
	recordPath, err := ConfinedPath(root, Overlay, "runs", taskID, "run-record.json")
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

	gate, requirement, expectedAssignee, err := resolveGateAuthority(
		record, overlay.Authorities, request.GateID, request.AuthorityRole)
	if err != nil {
		return nil, err
	}
	if request.ActorID != expectedAssignee {
		return nil, fmt.Errorf("actor %s does not match assigned authority %s for role %s",
			request.ActorID, expectedAssignee, request.AuthorityRole)
	}

	for _, raw := range listOf(gate.values["preparers"]) {
		preparer, ok := raw.(*orderedObject)
		if ok && preparer.values["id"] == request.ActorID {
			return nil, fmt.Errorf(
				"%s authority %s is a preparer for %s; cannot decide on own work",
				request.AuthorityRole, request.ActorID, request.GateID)
		}
	}
	if verifier, ok := gate.values["independent_verifier"].(*orderedObject); ok {
		verifierID, _ := verifier.values["id"].(string)
		if verifierID != "" && verifierID == request.ActorID {
			return nil, fmt.Errorf(
				"%s authority %s is the independent verifier for %s; cannot also decide",
				request.AuthorityRole, request.ActorID, request.GateID)
		}
	}
	if request.EvidenceURI == "" {
		return nil, fmt.Errorf("--evidence-uri is required")
	}
	if request.Decision == "approved" {
		if err := requireForgeEvidence(policy, request.GateID, request.EvidenceURI); err != nil {
			return nil, err
		}
	}

	decidedAt := request.DecidedAt
	if decidedAt == "" {
		decidedAt = nowRFC3339()
	}
	if !IsValidDatetime(decidedAt) {
		return nil, fmt.Errorf("--decided-at must be a valid RFC 3339 date-time")
	}

	roleLabel := requirement.values["role"]
	classification := record.values["classification"]
	if emptyString(classification) {
		classification = overlay.Project["classification"]
	}
	if emptyString(classification) {
		classification = "internal"
	}

	// The evidence hash binds the decision to its own content: task, gate,
	// authority, decision, actor, URI and time. Re-recording the same decision
	// yields the same hash; changing any part of it does not.
	digest, err := Fingerprint(map[string]any{
		"task_id": taskID, "gate_id": request.GateID, "authority_id": request.AuthorityRole,
		"decision": request.Decision, "actor_id": request.ActorID,
		"evidence_uri": request.EvidenceURI, "decided_at": decidedAt,
	})
	if err != nil {
		return nil, err
	}
	bare := strings.TrimPrefix(digest, "sha256:")

	status := "rejected"
	if request.Decision == "approved" {
		status = "approved"
	}
	approval := ordered(
		"status", status,
		"approver", ordered("id", request.ActorID, "role", roleLabel, "kind", "human"),
		"decided_at", decidedAt,
		"evidence_refs", []any{ordered(
			"evidence_id", fmt.Sprintf("%s-%s-decide-%s",
				strings.ToLower(request.GateID), request.AuthorityRole, bare[:12]),
			"uri", request.EvidenceURI,
			"hash_algorithm", "sha256",
			"hash", bare,
			"classification", classification,
		)},
	)
	if request.Note != "" {
		approval.set("note", request.Note)
	}

	gate.set("human_approvals", replaceApprovalEntry(
		listOf(gate.values["human_approvals"]), request.ActorID, roleLabel, approval))

	contracts, err := lifecycleGateContracts()
	if err != nil {
		return nil, err
	}
	// A rejection withdraws an approval that was already recorded. Leaving the
	// gate approved because it once was is how a withdrawn decision survives
	// the withdrawal.
	if request.Decision != "approved" && gate.values["status"] == "approved" {
		gate.set("status", "pending")
	}
	switch {
	case request.Decision == "approved" && canMarkGateApproved(record, gate, overlay.Authorities):
		gate.set("status", "approved")
		gate.set("decided_at", latestApprovalTime(gate, decidedAt))
		record.set("current_lifecycle_phase", deriveCurrentPhase(record, contracts))
	case request.Decision == "request-changes":
		gate.set("status", "request-changes")
		gate.set("decided_at", decidedAt)
		record.set("current_lifecycle_phase", deriveCurrentPhase(record, contracts))
	}

	if err := writeJSONDocument(recordPath, record); err != nil {
		return nil, err
	}
	return ordered(
		"task_id", taskID,
		"gate_id", request.GateID,
		"authority_id", request.AuthorityRole,
		"decision", request.Decision,
		"actor_id", request.ActorID,
		"gate_status", gate.values["status"],
		"current_phase", deriveCurrentPhase(record, contracts),
	), nil
}

// requireForgeEvidence enforces the project's own approval-source policy at
// write time.
//
// Only when manual fallback is off: with it on, the approval may legitimately
// have happened away from the forge, and the URI is a pointer rather than the
// proof.
func requireForgeEvidence(policy ApprovalPolicy, gateID, evidenceURI string) error {
	if policy.AllowManualFallback {
		return nil
	}
	if policy.HumanGateDefault == "github-review" {
		if _, valid := GitHubReviewLogin(evidenceURI); !valid ||
			!strings.HasPrefix(evidenceURI, "github-review:") {
			return fmt.Errorf(
				"%s approval must be backed by a GitHub review "+
					"(project approval_sources requires github-review)", gateID)
		}
	}
	if policy.HumanGateDefault == "gitlab-mr" {
		if _, valid := GitLabMRUsername(evidenceURI); !valid ||
			!strings.HasPrefix(evidenceURI, "gitlab-mr:") {
			return fmt.Errorf(
				"%s approval must be backed by a GitLab MR approval "+
					"(project approval_sources requires gitlab-mr)", gateID)
		}
	}
	return nil
}

// resolveGateAuthority finds the gate and checks that this authority is one it
// actually requires, and that somebody is assigned to it.
func resolveGateAuthority(
	record *orderedObject, authorities map[string]any, gateID, authorityRole string,
) (gate, requirement *orderedObject, expectedAssignee string, err error) {
	for _, raw := range listOf(record.values["lifecycle_gates"]) {
		candidate, ok := raw.(*orderedObject)
		if ok && candidate.values["gate_id"] == gateID {
			gate = candidate
			break
		}
	}
	if gate == nil {
		return nil, nil, "", fmt.Errorf("unknown gate in run record: %s", gateID)
	}
	authority, ok := authorities[authorityRole].(map[string]any)
	if !ok {
		return nil, nil, "", fmt.Errorf("unknown authority role: %s", authorityRole)
	}
	for _, raw := range listOf(gate.values["authority_requirements"]) {
		candidate, ok := raw.(*orderedObject)
		if ok && candidate.values["authority_type"] == "human-approver" &&
			candidate.values["authority_id"] == authorityRole {
			requirement = candidate
			break
		}
	}
	if requirement == nil {
		return nil, nil, "", fmt.Errorf("%s does not require authority role %s", gateID, authorityRole)
	}
	if requirement.values["applicability"] != "applicable" {
		return nil, nil, "", fmt.Errorf("%s authority role %s is not applicable", gateID, authorityRole)
	}
	expectedAssignee, _ = authority["assignee"].(string)
	if authority["status"] != "assigned" || expectedAssignee == "" {
		return nil, nil, "", fmt.Errorf("authority %s is not assigned", authorityRole)
	}
	return gate, requirement, expectedAssignee, nil
}

// replaceApprovalEntry supersedes this approver's prior *approved* entry.
//
// Only the approved one. A prior rejection keeps its place and its stated
// reason: an approval that follows a rejection is a change of mind, and the
// record should show both rather than the happier one alone.
func replaceApprovalEntry(
	existing []any, approverID string, roleLabel any, entry *orderedObject,
) []any {
	remaining := []any{}
	for _, raw := range existing {
		approval, ok := raw.(*orderedObject)
		if !ok {
			remaining = append(remaining, raw)
			continue
		}
		approver, _ := approval.values["approver"].(*orderedObject)
		superseded := approval.values["status"] == "approved" && approver != nil &&
			approver.values["id"] == approverID && approver.values["role"] == roleLabel
		if !superseded {
			remaining = append(remaining, raw)
		}
	}
	return append(remaining, entry)
}

// approvedApprovals returns the gate's approvals whose status is approved.
func approvedApprovals(gate *orderedObject) []*orderedObject {
	var approvals []*orderedObject
	for _, raw := range listOf(gate.values["human_approvals"]) {
		if approval, ok := raw.(*orderedObject); ok && approval.values["status"] == "approved" {
			approvals = append(approvals, approval)
		}
	}
	return approvals
}

// hasAllRequiredHumanApprovals asks whether every applicable human authority
// has approved -- and specifically whether the assigned identity has.
//
// An unassigned authority makes this false rather than vacuously true: a gate
// requiring an approval from a role nobody holds is not a gate that has been
// approved, it is one that cannot be yet.
func hasAllRequiredHumanApprovals(gate *orderedObject, authorities map[string]any) bool {
	approvals := approvedApprovals(gate)
	for _, raw := range listOf(gate.values["authority_requirements"]) {
		requirement, ok := raw.(*orderedObject)
		if !ok || requirement.values["authority_type"] != "human-approver" ||
			requirement.values["applicability"] != "applicable" {
			continue
		}
		authorityID, _ := requirement.values["authority_id"].(string)
		authority, _ := authorities[authorityID].(map[string]any)
		expectedAssignee, _ := authority["assignee"].(string)
		if expectedAssignee == "" {
			return false
		}
		satisfied := false
		for _, approval := range approvals {
			approver, _ := approval.values["approver"].(*orderedObject)
			if approver != nil && approver.values["id"] == expectedAssignee &&
				approver.values["role"] == requirement.values["role"] {
				satisfied = true
			}
		}
		if !satisfied {
			return false
		}
	}
	return true
}

// canMarkGateApproved re-derives whether the gate as a whole is approved.
//
// Deliberately not "the caller said approved". This reads the record and
// answers from it: the gate is ready, applicable, has artifacts and evidence
// bound, has an independent verifier who declared they did not prepare the
// work, every earlier applicable gate is approved, and every applicable human
// authority has signed. One authority's decision contributes to that; it never
// substitutes for it.
func canMarkGateApproved(record, gate *orderedObject, authorities map[string]any) bool {
	status, _ := gate.values["status"].(string)
	if status != "ready" && status != "approved" {
		return false
	}
	if gate.values["applicability"] != "applicable" {
		return false
	}
	if len(listOf(gate.values["artifact_bindings"])) == 0 ||
		len(listOf(gate.values["evidence_refs"])) == 0 {
		return false
	}
	if _, ok := gate.values["independent_verifier"].(*orderedObject); !ok {
		return false
	}
	declaration, _ := gate.values["independence_declaration"].(*orderedObject)
	if declaration == nil || declaration.values["verifier_confirmed_not_preparer"] != true {
		return false
	}

	gateID, _ := gate.values["gate_id"].(string)
	position := gateIndex(gateID)
	gates := listOf(record.values["lifecycle_gates"])
	if position > len(gates) {
		position = len(gates)
	}
	for _, raw := range gates[:position] {
		prior, ok := raw.(*orderedObject)
		if !ok {
			continue
		}
		if prior.values["applicability"] != "not-applicable" && prior.values["status"] != "approved" {
			return false
		}
	}
	return hasAllRequiredHumanApprovals(gate, authorities)
}

// latestApprovalTime is the gate's decision time: the last of the approvals
// that made it approved, so the gate is not dated earlier than the signature
// that completed it.
func latestApprovalTime(gate *orderedObject, fallback string) string {
	var times []string
	for _, approval := range approvedApprovals(gate) {
		if decided, ok := approval.values["decided_at"].(string); ok && decided != "" {
			times = append(times, decided)
		}
	}
	if len(times) == 0 {
		return fallback
	}
	sort.Strings(times)
	return times[len(times)-1]
}
