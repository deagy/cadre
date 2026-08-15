package kernel

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// Run-record validation: the half of `validate` that reads what a project
// claims happened and checks it could have.
//
// The configuration half asks whether a project is set up coherently. This
// half asks whether its record of a task is one the lifecycle permits -- and
// most of these checks exist because the alternative is a record that *looks*
// approved.
//
// The invariants worth naming, since they are what the gates are for:
//
//   - A verifier cannot also be a preparer. An identity that produced the
//     artifact cannot be the one independently confirming it.
//   - A verifier who made a material correction has lost approval authority:
//     having changed the thing, they are now a preparer of it.
//   - An approver cannot be a preparer or the verifier, and an approval must
//     be bound to the identity the project actually assigned to that
//     authority -- not merely to somebody holding the right job title.
//   - A gate cannot be approved before its prerequisites, and cannot advance
//     out of lexical order.
//   - An authority requirement cannot be relabeled. `uat_product_owner` and
//     `product_owner` both display as "Product Owner", so a record swapping
//     one for the other reads as correct to a human and is not.
//   - Once a gate is invalidated, every downstream gate must be too.
//
// Everything here reports rather than raises, for the same reason the
// configuration half does: one malformed field should not hide every other
// problem in the record.

// requiredRunRecordFields are the top-level fields every run record carries.
var requiredRunRecordFields = []string{
	"version", "task_id", "dispatch_fingerprint", "recorded_at", "classification",
	"mode", "baseline_revision", "scope", "disposition", "intent_record_id",
	"requirements_baseline_id", "current_lifecycle_phase", "knowledge_retrieval",
	"impact_profile", "lifecycle_gates", "specialist_attestations", "re_entry_history",
	"kernel_version", "contract_digest", "provider_bindings", "profile", "profile_digest",
	"dispatch_binding_digest",
}

// advancedGateStatuses are the statuses that mean a gate has moved on, and so
// must have everything it was configured to require.
var advancedGateStatuses = map[string]bool{"ready": true, "approved": true}

// runContext is everything a run record is checked against: the project's own
// routing and authorities, the installed agent catalog, and the approval
// policy. All four are read-only here.
type runContext struct {
	routing     map[string]any
	authorities map[string]any
	catalog     map[string]any
	policy      ApprovalPolicy
	contracts   map[string]map[string]any
}

// validateRunRecords walks every task directory under the overlay.
func (r *Registry) validateRunRecords(
	accumulator *validationAccumulator, root string, overlay *ProjectOverlay, policy ApprovalPolicy,
) {
	runRoot, err := ConfinedPath(root, Overlay, "runs")
	if err != nil {
		accumulator.errorf("%s", err.Error())
		return
	}
	entries, err := os.ReadDir(runRoot)
	if err != nil {
		return // a project with no runs yet is not a project with a problem
	}
	var taskDirectories []string
	for _, entry := range entries {
		if entry.IsDir() {
			taskDirectories = append(taskDirectories, entry.Name())
		}
	}
	// Sorted so a report is diffable between runs. Go's directory order is
	// not guaranteed and Python's iterdir order is not either.
	sort.Strings(taskDirectories)

	contracts, err := lifecycleGateContracts()
	if err != nil {
		accumulator.errorf("%s", err.Error())
		return
	}
	catalog, err := r.LoadAgentCatalog()
	if err != nil {
		accumulator.errorf("%s", err.Error())
		catalog = map[string]any{}
	}
	context := &runContext{
		routing:     overlay.Routing,
		authorities: overlay.Authorities,
		catalog:     catalog,
		policy:      policy,
		contracts:   contracts,
	}

	for _, task := range taskDirectories {
		r.validateOneRun(accumulator, root, task, context)
	}
}

// lifecycleGateContracts indexes the bundled lifecycle contract by gate id.
func lifecycleGateContracts() (map[string]map[string]any, error) {
	raw, err := EmbeddedContract("lifecycle-gates.json")
	if err != nil {
		return nil, err
	}
	contract, err := decodeJSONObject(raw)
	if err != nil {
		return nil, err
	}
	gates, _ := contract["gates"].([]any)
	indexed := map[string]map[string]any{}
	for _, item := range gates {
		gate, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := gate["id"].(string)
		indexed[id] = gate
	}
	return indexed, nil
}

func (r *Registry) validateOneRun(
	accumulator *validationAccumulator, root, task string, context *runContext,
) {
	recordPath, err := ConfinedPath(root, Overlay, "runs", task, "run-record.json")
	if err != nil {
		accumulator.errorf("%s", err.Error())
		return
	}
	dispatchPath, err := ConfinedPath(root, Overlay, "runs", task, "dispatch-plan.json")
	if err != nil {
		accumulator.errorf("%s", err.Error())
		return
	}

	// Both or neither. A run record without its dispatch plan is a claim about
	// work with no record of what was asked for; a plan without a record is
	// work nobody accounted for.
	_, recordErr := os.Stat(recordPath)
	_, dispatchErr := os.Stat(dispatchPath)
	if recordErr != nil || dispatchErr != nil {
		accumulator.errorf(
			"%s: dispatch plan and authoritative run record must both exist",
			filepath.Join(root, Overlay, "runs", task))
		return
	}

	record, err := loadJSONObject(recordPath)
	if err != nil {
		accumulator.errorf("%s", err.Error())
		return
	}

	var missing []string
	for _, field := range requiredRunRecordFields {
		if _, present := record[field]; !present {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		accumulator.errorf("%s: missing required fields: %s", recordPath, pythonList(missing))
	}

	violations, err := SchemaViolations(record, "run-record.schema.json")
	if err != nil {
		accumulator.errorf("%s", err.Error())
	}
	for _, violation := range violations {
		accumulator.errorf("%s: %s", recordPath, violation)
	}

	if !IsValidDatetime(record["recorded_at"]) {
		accumulator.errorf("%s: schema recorded_at: %s is not a 'date-time'",
			recordPath, pythonRepr(record["recorded_at"]))
	}

	gates := listOf(record["lifecycle_gates"])
	var gateIDs []string
	for _, raw := range gates {
		gate, _ := raw.(map[string]any)
		id, _ := gate["gate_id"].(string)
		gateIDs = append(gateIDs, id)
	}
	// Exactly G1-G10, in order. A record missing a gate, or holding them in
	// another order, is not a record of this lifecycle.
	if !sameStrings(gateIDs, GateIDs) {
		accumulator.errorf("%s: lifecycle gates must be exactly G1-G10 in order", recordPath)
	}

	dispatch, err := loadJSONObject(dispatchPath)
	if err != nil {
		accumulator.errorf("%s", err.Error())
		return
	}
	configured, ignored := dispatchGateSets(dispatch)
	executions := executionGates(record)

	r.validateGateRecords(accumulator, recordPath, record, gates, context,
		configured, ignored, executions)

	for index, raw := range listOf(record["re_entry_history"]) {
		entry, _ := raw.(map[string]any)
		if !IsValidDatetime(entry["invalidated_at"]) {
			accumulator.errorf(
				"%s: schema re_entry_history.%d.invalidated_at: %s is not a 'date-time'",
				recordPath, index, pythonRepr(entry["invalidated_at"]))
		}
	}

	validateDispatchPlan(accumulator, dispatchPath, recordPath, task, dispatch, record, executions)
}

// validateGateRecords checks each gate in order.
func (r *Registry) validateGateRecords(
	accumulator *validationAccumulator,
	recordPath string,
	record map[string]any,
	gates []any,
	context *runContext,
	configured, ignored map[string]bool,
	executions map[string]map[string]any,
) {
	invalidationStarted := false

	for index, raw := range gates {
		gate, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		gateID, _ := gate["gate_id"].(string)
		contract := context.contracts[gateID]

		for _, evidenceRaw := range listOf(gate["evidence_refs"]) {
			evidence, ok := evidenceRaw.(map[string]any)
			if !ok {
				accumulator.errorf("%s: %s evidence_refs contains a non-object entry",
					recordPath, gateID)
				continue
			}
			uri, _ := evidence["uri"].(string)
			if strings.HasPrefix(uri, "gitlab-issue:") && !IsGitLabIssueURI(uri) {
				accumulator.errorf("%s: %s has an invalid GitLab issue URI", recordPath, gateID)
			}
			if strings.HasPrefix(uri, "github-issue:") && !IsGitHubIssueURI(uri) {
				accumulator.errorf("%s: %s has an invalid GitHub issue URI", recordPath, gateID)
			}
		}

		execution, hasExecution := executions[gateID]
		if !hasExecution {
			accumulator.errorf("%s: %s is missing its required execution record", recordPath, gateID)
			execution = map[string]any{}
		}

		binding := GateDispatchBinding(contract, context.routing)
		expectedAgents := binding["agents"]
		expectedArtifacts := GateAgentArtifacts(contract)

		if execution["configured"] != (configured[gateID] || ignored[gateID]) {
			accumulator.errorf("%s: %s execution configuration does not match dispatch plan",
				recordPath, gateID)
		}
		if execution["ignored"] != ignored[gateID] {
			accumulator.errorf("%s: %s ignore state does not match dispatch plan", recordPath, gateID)
		}
		isConfigured := execution["configured"] == true
		isIgnored := execution["ignored"] == true

		// Present-and-equal, not merely equal: an execution record that omits
		// required_agents entirely is not the same as one declaring none, and
		// treating the two alike would let a record claim a gate needed
		// nobody by saying nothing at all.
		if isConfigured && !jsonEqual(execution["required_agents"], asJSONList(expectedAgents)) {
			accumulator.errorf("%s: %s required agent set does not match lifecycle contract",
				recordPath, gateID)
		}
		if isConfigured && !jsonEqual(execution["required_tasks"], asJSONList(binding["tasks"])) {
			accumulator.errorf("%s: %s required task set does not match lifecycle contract",
				recordPath, gateID)
		}
		if !jsonEqual(execution["required_agent_artifacts"], asJSONArtifacts(expectedArtifacts)) {
			accumulator.errorf("%s: %s required agent artifacts do not match lifecycle contract",
				recordPath, gateID)
		}
		// An ignored gate needs a stated reason. Skipping a gate silently is
		// the difference between a decision and an omission.
		if isIgnored && emptyString(execution["ignore_reason"]) {
			accumulator.errorf("%s: %s ignored gate requires an explicit reason", recordPath, gateID)
		}

		status, _ := gate["status"].(string)
		if advancedGateStatuses[status] && isConfigured && !isIgnored {
			if !sameStringSet(stringsIn(execution["dispatched_agents"]), expectedAgents) {
				accumulator.errorf("%s: %s advanced without dispatching every configured agent",
					recordPath, gateID)
			}
			if !sameStringSet(stringsIn(execution["completed_tasks"]), binding["tasks"]) {
				accumulator.errorf("%s: %s advanced without completing every configured task",
					recordPath, gateID)
			}
			// An artifact without a revision and a digest is not an artifact
			// anyone can point back to; it names something that may since have
			// changed. Only immutable ones count as produced.
			produced := map[[2]string]bool{}
			for _, item := range listOf(execution["produced_agent_artifacts"]) {
				entry, ok := item.(map[string]any)
				if !ok || emptyString(entry["revision"]) || emptyString(entry["digest"]) {
					continue
				}
				agentID, _ := entry["agent_id"].(string)
				artifactID, _ := entry["artifact_id"].(string)
				produced[[2]string{agentID, artifactID}] = true
			}
			for _, required := range expectedArtifacts {
				if !produced[[2]string{required["agent_id"], required["artifact_id"]}] {
					accumulator.errorf(
						"%s: %s advanced without immutable artifacts from every configured agent",
						recordPath, gateID)
					break
				}
			}
		}

		// Lexical order. A gate cannot advance while an earlier configured,
		// non-ignored gate is still outstanding -- that is what "gates" means.
		if advancedGateStatuses[status] && isConfigured {
			for _, priorRaw := range gates[:index] {
				prior, _ := priorRaw.(map[string]any)
				priorID, _ := prior["gate_id"].(string)
				priorStatus, _ := prior["status"].(string)
				priorExecution := executions[priorID]
				if priorStatus != "approved" && priorStatus != "invalidated" &&
					priorExecution["configured"] == true && priorExecution["ignored"] != true {
					accumulator.errorf("%s: %s violates lexical gate order", recordPath, gateID)
					break
				}
			}
		}

		// Independence. The identity that prepared an artifact cannot be the
		// one independently verifying it, and a verifier who corrected the
		// work has become a preparer of it.
		preparers := map[string]bool{}
		for _, preparerRaw := range listOf(gate["preparers"]) {
			if preparer, ok := preparerRaw.(map[string]any); ok {
				if id, ok := preparer["id"].(string); ok {
					preparers[id] = true
				}
			}
		}
		verifier, hasVerifier := gate["independent_verifier"].(map[string]any)
		if hasVerifier {
			if id, ok := verifier["id"].(string); ok && preparers[id] {
				accumulator.errorf("%s: %s verifier is also a preparer", recordPath, gateID)
			}
		}
		declaration, _ := gate["independence_declaration"].(map[string]any)
		if declaration["verifier_made_material_correction"] == true {
			accumulator.errorf(
				"%s: %s verifier made a material correction and lost approval authority",
				recordPath, gateID)
		}

		// Invalidation cascades. Once a gate is invalidated, everything after
		// it rests on work that was withdrawn.
		if invalidationStarted && status != "invalidated" {
			accumulator.errorf("%s: downstream gate %s must be invalidated", recordPath, gateID)
		}
		if status == "invalidated" {
			invalidationStarted = true
			if emptyString(gate["required_reentry_gate"]) {
				accumulator.errorf("%s: %s invalidation is missing required re-entry gate",
					recordPath, gateID)
			}
		}

		validateGateTimestamps(accumulator, recordPath, index, gate)

		if status == "approved" {
			r.validateApprovedGate(accumulator, recordPath, record, gates, index, gate, gateID,
				contract, context, verifier, hasVerifier, declaration, preparers)
		}
	}
}

func validateGateTimestamps(
	accumulator *validationAccumulator, recordPath string, index int, gate map[string]any,
) {
	if decided, present := gate["decided_at"]; present && decided != nil && !IsValidDatetime(decided) {
		accumulator.errorf("%s: schema lifecycle_gates.%d.decided_at: %s is not a 'date-time'",
			recordPath, index, pythonRepr(decided))
	}
	for approvalIndex, raw := range listOf(gate["human_approvals"]) {
		approval, _ := raw.(map[string]any)
		if decided, present := approval["decided_at"]; present && decided != nil && !IsValidDatetime(decided) {
			accumulator.errorf(
				"%s: schema lifecycle_gates.%d.human_approvals.%d.decided_at: %s is not a 'date-time'",
				recordPath, index, approvalIndex, pythonRepr(decided))
		}
	}
	for invalidationIndex, raw := range listOf(gate["invalidation_history"]) {
		invalidation, _ := raw.(map[string]any)
		if !IsValidDatetime(invalidation["invalidated_at"]) {
			accumulator.errorf(
				"%s: schema lifecycle_gates.%d.invalidation_history.%d.invalidated_at: %s is not a 'date-time'",
				recordPath, index, invalidationIndex, pythonRepr(invalidation["invalidated_at"]))
		}
	}
	for exceptionIndex, raw := range listOf(gate["exceptions"]) {
		exception, _ := raw.(map[string]any)
		if !IsValidDatetime(exception["expires_at"]) {
			accumulator.errorf(
				"%s: schema lifecycle_gates.%d.exceptions.%d.expires_at: %s is not a 'date-time'",
				recordPath, index, exceptionIndex, pythonRepr(exception["expires_at"]))
		}
	}
}

// validateApprovedGate applies the checks that only matter once someone has
// said yes -- which is where most of this kernel's reason for existing sits.
func (r *Registry) validateApprovedGate(
	accumulator *validationAccumulator,
	recordPath string,
	record map[string]any,
	gates []any,
	index int,
	gate map[string]any,
	gateID string,
	contract map[string]any,
	context *runContext,
	verifier map[string]any,
	hasVerifier bool,
	declaration map[string]any,
	preparers map[string]bool,
) {
	for _, priorRaw := range gates[:index] {
		prior, _ := priorRaw.(map[string]any)
		if prior["status"] != "approved" && prior["applicability"] != "not-applicable" {
			accumulator.errorf("%s: %s approved before all prerequisite gates", recordPath, gateID)
			break
		}
	}

	if gate["applicability"] != "applicable" ||
		len(listOf(gate["evidence_refs"])) == 0 ||
		len(listOf(gate["artifact_bindings"])) == 0 {
		accumulator.errorf(
			"%s: %s has an unsafe approval without applicability, evidence, or artifact binding",
			recordPath, gateID)
	}

	requirements := listOf(gate["authority_requirements"])
	requirementIDs := map[string]bool{}
	for _, raw := range requirements {
		requirement, _ := raw.(map[string]any)
		authorityID, _ := requirement["authority_id"].(string)
		if requirementIDs[authorityID] {
			accumulator.errorf("%s: %s has duplicate authority requirement %s",
				recordPath, gateID, authorityID)
		}
		requirementIDs[authorityID] = true

		// Relabeling. Two roles share the label "Product Owner", so a record
		// that swaps one for the other reads as correct to a human -- which is
		// exactly why the label is checked against the role rather than
		// trusted.
		if label, known := RoleLabels[authorityID]; known {
			if requirement["authority_type"] != "human-approver" || requirement["role"] != label {
				accumulator.errorf("%s: %s authority %s is relabeled", recordPath, gateID, authorityID)
			}
		}
		if requirement["applicability"] == "not-applicable" && emptyString(requirement["rationale"]) {
			accumulator.errorf("%s: %s not-applicable authority %s lacks rationale",
				recordPath, gateID, authorityID)
		}
	}

	var missingRequirements []string
	for _, raw := range listOf(contract["authority_requirements"]) {
		expected, _ := raw.(string)
		if expected != "" && !requirementIDs[expected] {
			missingRequirements = append(missingRequirements, expected)
		}
	}
	if len(missingRequirements) > 0 {
		missingRequirements = uniqueStrings(missingRequirements)
		sort.Strings(missingRequirements)
		accumulator.errorf("%s: %s is missing authority requirements %s",
			recordPath, gateID, pythonList(missingRequirements))
	}

	for _, raw := range requirements {
		requirement, _ := raw.(map[string]any)
		if requirement["applicability"] == "unknown" {
			accumulator.errorf("%s: %s approved with unresolved authority applicability",
				recordPath, gateID)
			break
		}
	}

	if !hasVerifier || declaration["verifier_confirmed_not_preparer"] != true {
		accumulator.errorf("%s: %s lacks an independent verifier declaration", recordPath, gateID)
	} else if verifier["kind"] == "agent" {
		// Only an agent verifier's `role` is a catalog id. A human verifier's
		// role is a job title, and looking that up in the agent catalog would
		// reject every human-verified gate -- which it once did.
		role, ok := verifier["role"].(string)
		if !ok {
			accumulator.errorf("%s: %s verifier role must be a string", recordPath, gateID)
		} else {
			entry, _ := context.catalog[role].(map[string]any)
			if entry["kind"] != "reviewer" {
				accumulator.errorf("%s: %s verifier agent is not a catalog reviewer", recordPath, gateID)
			}
		}
	}

	r.validateApprovals(accumulator, recordPath, gateID, gate, requirements,
		context, verifier, hasVerifier, preparers)

	if emptyString(gate["decided_at"]) {
		accumulator.errorf("%s: %s approved without a gate decision timestamp", recordPath, gateID)
	}
	for _, raw := range listOf(gate["artifact_bindings"]) {
		binding, _ := raw.(map[string]any)
		if emptyString(binding["digest"]) {
			accumulator.errorf("%s: %s approved with a mutable artifact binding", recordPath, gateID)
			break
		}
	}

	// Open critical/high findings, and accepted ones without a valid
	// exception. "Accepted" without an exception is an acceptance nobody
	// signed.
	for _, raw := range listOf(gate["findings"]) {
		finding, _ := raw.(map[string]any)
		severity, _ := finding["severity"].(string)
		if (severity == "critical" || severity == "high") && finding["status"] == "open" {
			accumulator.errorf("%s: %s has unresolved critical/high findings", recordPath, gateID)
			break
		}
	}
	exceptionFindings := map[string]bool{}
	for _, raw := range listOf(gate["exceptions"]) {
		exception, _ := raw.(map[string]any)
		if ValidException(exception) {
			if id, ok := exception["finding_id"].(string); ok {
				exceptionFindings[id] = true
			}
		}
	}
	for _, raw := range listOf(gate["findings"]) {
		finding, _ := raw.(map[string]any)
		findingID, _ := finding["finding_id"].(string)
		if finding["status"] == "accepted-exception" && !exceptionFindings[findingID] {
			accumulator.errorf("%s: %s accepted finding lacks a valid exception", recordPath, gateID)
		}
	}

	if gateID == "G3" || gateID == "G4" || gateID == "G5" || gateID == "G7" {
		impact, _ := record["impact_profile"].(map[string]any)
		if len(listOf(impact["blocking_unknowns"])) > 0 {
			accumulator.errorf("%s: %s approved while impact applicability is unknown",
				recordPath, gateID)
		}
	}
}

// validateApprovals checks the human approvals recorded on an approved gate.
//
// This is where the separation the whole kernel exists to enforce is finally
// checked against named people: that every applicable human authority actually
// approved, that the approver is a human and is neither a preparer nor the
// verifier, that the approval carries evidence, and that the evidence names
// the same person the project assigned to that authority. An approval which
// satisfies every check but the last is one where the right role approved and
// the wrong person did it.
func (r *Registry) validateApprovals(
	accumulator *validationAccumulator,
	recordPath, gateID string,
	gate map[string]any,
	requirements []any,
	context *runContext,
	verifier map[string]any,
	hasVerifier bool,
	preparers map[string]bool,
) {
	allApprovals := listOf(gate["human_approvals"])
	var approved []map[string]any
	approvedRoles := map[string]bool{}
	for _, raw := range allApprovals {
		approval, ok := raw.(map[string]any)
		if !ok || approval["status"] != "approved" {
			continue
		}
		approved = append(approved, approval)
		if approver, ok := approval["approver"].(map[string]any); ok {
			if role, ok := approver["role"].(string); ok {
				approvedRoles[role] = true
			}
		}
	}

	var unapprovedRoles []string
	for _, raw := range requirements {
		requirement, _ := raw.(map[string]any)
		if requirement["authority_type"] != "human-approver" ||
			requirement["applicability"] != "applicable" {
			continue
		}
		role, _ := requirement["role"].(string)
		if !approvedRoles[role] {
			unapprovedRoles = append(unapprovedRoles, role)
		}
	}
	if len(unapprovedRoles) > 0 {
		unapprovedRoles = uniqueStrings(unapprovedRoles)
		sort.Strings(unapprovedRoles)
		accumulator.errorf("%s: %s lacks required human roles %s",
			recordPath, gateID, pythonList(unapprovedRoles))
	}

	for _, raw := range allApprovals {
		approval, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		approver, approverIsObject := approval["approver"].(map[string]any)
		isApproved := approval["status"] == "approved"

		if isApproved && (!approverIsObject || approver["kind"] != "human") {
			accumulator.errorf("%s: %s approval is not human", recordPath, gateID)
		}
		if approverIsObject {
			approverID, _ := approver["id"].(string)
			verifierID, _ := verifier["id"].(string)
			if preparers[approverID] || (hasVerifier && approverID == verifierID) {
				accumulator.errorf("%s: %s approver is not independent", recordPath, gateID)
			}
		}
		if isApproved && (emptyString(approval["decided_at"]) ||
			len(listOf(approval["evidence_refs"])) == 0) {
			accumulator.errorf("%s: %s approval lacks decision time or approval evidence",
				recordPath, gateID)
		}

		var reviewLogins, approvalUsernames []string
		for _, evidenceRaw := range listOf(approval["evidence_refs"]) {
			evidence, ok := evidenceRaw.(map[string]any)
			if !ok {
				accumulator.errorf("%s: %s approval evidence_refs contains a non-object entry",
					recordPath, gateID)
				continue
			}
			uri, _ := evidence["uri"].(string)
			switch {
			case strings.HasPrefix(uri, "github-review:"):
				login, valid := GitHubReviewLogin(uri)
				if !valid {
					accumulator.errorf("%s: %s approval has an invalid GitHub review URI",
						recordPath, gateID)
					continue
				}
				reviewLogins = append(reviewLogins, login)
			case strings.HasPrefix(uri, "gitlab-mr:"):
				username, valid := GitLabMRUsername(uri)
				if !valid {
					accumulator.errorf("%s: %s approval has an invalid GitLab MR approval URI",
						recordPath, gateID)
					continue
				}
				approvalUsernames = append(approvalUsernames, username)
			}
		}

		if !isApproved {
			continue
		}
		// With manual fallback available the approval can still have happened
		// off-forge, so the missing review is only fatal when the project has
		// closed that door itself.
		if context.policy.HumanGateDefault == "github-review" &&
			!context.policy.AllowManualFallback && len(reviewLogins) == 0 {
			accumulator.errorf("%s: %s approval must be backed by a GitHub review", recordPath, gateID)
		}
		if approverIsObject && len(reviewLogins) > 0 {
			approverLogin := ForgeLoginFromIdentity(approver["id"], "github")
			for _, login := range reviewLogins {
				if approverLogin != "" && approverLogin != login {
					accumulator.errorf("%s: %s GitHub review login does not match approver identity",
						recordPath, gateID)
				}
			}
		}
		if context.policy.HumanGateDefault == "gitlab-mr" &&
			!context.policy.AllowManualFallback && len(approvalUsernames) == 0 {
			accumulator.errorf("%s: %s approval must be backed by a GitLab MR approval",
				recordPath, gateID)
		}
		if approverIsObject && len(approvalUsernames) > 0 {
			approverUsername := ForgeLoginFromIdentity(approver["id"], "gitlab")
			for _, username := range approvalUsernames {
				if approverUsername != "" && approverUsername != username {
					accumulator.errorf("%s: %s GitLab MR approver does not match approver identity",
						recordPath, gateID)
				}
			}
		}
	}

	// And finally: the approval has to come from the person this project
	// assigned to that authority. Everything above checks the shape of the
	// approval; this checks whose it is.
	for _, raw := range requirements {
		requirement, _ := raw.(map[string]any)
		if requirement["authority_type"] != "human-approver" ||
			requirement["applicability"] != "applicable" {
			continue
		}
		authorityID, _ := requirement["authority_id"].(string)
		authority, _ := context.authorities[authorityID].(map[string]any)
		expectedAssignee, _ := authority["assignee"].(string)

		var matching []map[string]any
		for _, approval := range approved {
			approver, ok := approval["approver"].(map[string]any)
			if !ok {
				continue
			}
			if approver["id"] == expectedAssignee && approver["role"] == requirement["role"] {
				matching = append(matching, approval)
			}
		}
		if expectedAssignee == "" || len(matching) == 0 {
			accumulator.errorf("%s: %s approval is not bound to assigned authority %s",
				recordPath, gateID, authorityID)
		}

		if expectedLogin := AuthorityForgeLogin(authority, "github"); expectedLogin != "" {
			for _, approval := range matching {
				if forgeEvidenceMismatch(approval, expectedLogin, GitHubReviewLogin) {
					accumulator.errorf(
						"%s: %s approval GitHub reviewer does not match assigned authority %s",
						recordPath, gateID, authorityID)
				}
			}
		}
		if expectedUsername := AuthorityForgeLogin(authority, "gitlab"); expectedUsername != "" {
			for _, approval := range matching {
				if forgeEvidenceMismatch(approval, expectedUsername, GitLabMRUsername) {
					accumulator.errorf(
						"%s: %s approval GitLab approver does not match assigned authority %s",
						recordPath, gateID, authorityID)
				}
			}
		}
	}
}

// forgeEvidenceMismatch reports whether an approval cites forge evidence
// naming somebody other than expected.
//
// Case-insensitively: forge logins are not case-sensitive, and rejecting an
// approval because the URI said "Octocat" and the assignment said "octocat"
// would fail an approval that genuinely happened.
func forgeEvidenceMismatch(
	approval map[string]any, expected string, parse func(string) (string, bool),
) bool {
	for _, evidenceRaw := range listOf(approval["evidence_refs"]) {
		evidence, ok := evidenceRaw.(map[string]any)
		if !ok {
			continue
		}
		uri, _ := evidence["uri"].(string)
		name, valid := parse(uri)
		if valid && !strings.EqualFold(name, expected) {
			return true
		}
	}
	return false
}

// validateDispatchPlan checks the plan against the record it produced.
func validateDispatchPlan(
	accumulator *validationAccumulator,
	dispatchPath, recordPath, task string,
	dispatch, record map[string]any,
	executions map[string]map[string]any,
) {
	taskDirectory := filepath.Dir(recordPath)

	violations, err := SchemaViolations(dispatch, "selection.schema.json")
	if err != nil {
		accumulator.errorf("%s", err.Error())
	}
	for _, violation := range violations {
		accumulator.errorf("%s: %s", dispatchPath, violation)
	}

	if dispatch["task_id"] != record["task_id"] || dispatch["task_id"] != task {
		accumulator.errorf("%s: task IDs do not match directory, dispatch, and run record", taskDirectory)
	}
	inputs, _ := dispatch["inputs"].(map[string]any)
	if inputs["task"] != record["scope"] {
		accumulator.errorf("%s: dispatch task and run-record scope do not match", taskDirectory)
	}
	if dispatch["dispatch_fingerprint"] != record["dispatch_fingerprint"] {
		accumulator.errorf("%s: dispatch and run-record fingerprints do not match", taskDirectory)
	}

	// The integrity check: recompute the fingerprint from the plan's own
	// content and compare. A plan edited after the fact no longer hashes to
	// what it recorded.
	computed, err := DispatchFingerprint(dispatch)
	if err != nil {
		accumulator.errorf("%s", err.Error())
	} else if dispatch["dispatch_fingerprint"] != computed {
		accumulator.errorf(
			"%s: stored dispatch fingerprint does not match current dispatch content", dispatchPath)
	}

	agents, _ := dispatch["agents"].(map[string]any)
	primary := stringsIn(agents["primary"])
	reviewers := map[string]bool{}
	for _, reviewer := range stringsIn(agents["reviewers"]) {
		reviewers[reviewer] = true
	}
	var overlap []string
	for _, agent := range primary {
		if reviewers[agent] {
			overlap = append(overlap, agent)
		}
	}
	if len(overlap) > 0 {
		overlap = uniqueStrings(overlap)
		sort.Strings(overlap)
		accumulator.errorf("%s: dispatch author/reviewer overlap: %s", dispatchPath, pythonList(overlap))
	}

	sequence := listOf(dispatch["gate_dispatch"])
	var requiredIDs, dispatchedRequired, dispatchedIgnored, sequenceIDs []string
	for _, raw := range listOf(dispatch["required_quality_gates"]) {
		gate, _ := raw.(map[string]any)
		id, _ := gate["id"].(string)
		requiredIDs = append(requiredIDs, id)
	}
	for _, raw := range sequence {
		item, _ := raw.(map[string]any)
		id, _ := item["gate_id"].(string)
		sequenceIDs = append(sequenceIDs, id)
		switch item["status"] {
		case "required":
			dispatchedRequired = append(dispatchedRequired, id)
		case "ignored":
			dispatchedIgnored = append(dispatchedIgnored, id)
		}
	}
	if !sameStrings(requiredIDs, dispatchedRequired) {
		accumulator.errorf("%s: required quality gates do not match gate dispatch", dispatchPath)
	}
	if !sameStrings(stringsIn(dispatch["ignored_quality_gates"]), dispatchedIgnored) {
		accumulator.errorf("%s: configured ignored gates do not match gate dispatch", dispatchPath)
	}
	sortedSequence := append([]string(nil), sequenceIDs...)
	sort.SliceStable(sortedSequence, func(a, b int) bool {
		return gateIndex(sortedSequence[a]) < gateIndex(sortedSequence[b])
	})
	if !sameStrings(sequenceIDs, sortedSequence) {
		accumulator.errorf("%s: gate dispatch must be in lexical order", dispatchPath)
	}

	for _, raw := range sequence {
		item, _ := raw.(map[string]any)
		gateID, _ := item["gate_id"].(string)
		execution, hasExecution := executions[gateID]

		if item["status"] == "ignored" {
			if !hasExecution || len(execution) == 0 || execution["ignored"] != true ||
				emptyString(execution["ignore_reason"]) {
				accumulator.errorf("%s: ignored %s lacks explicit execution waiver", dispatchPath, gateID)
			}
			continue
		}
		if hasExecution && len(execution) > 0 {
			if execution["ignored"] == true {
				accumulator.errorf("%s: required %s is marked ignored in the run record",
					dispatchPath, gateID)
			}
			if !jsonEqual(execution["required_tasks"], item["tasks"]) {
				accumulator.errorf("%s: %s task dispatch does not match the lifecycle contract",
					dispatchPath, gateID)
			}
			if len(listOf(item["artifacts"])) > 0 && len(listOf(execution["required_agent_artifacts"])) > 0 {
				accumulator.errorf("%s: %s artifact dispatch does not match configured agents",
					dispatchPath, gateID)
			}
		}
	}
}

func gateIndex(id string) int {
	for index, gate := range GateIDs {
		if gate == id {
			return index
		}
	}
	return len(GateIDs)
}

func dispatchGateSets(dispatch map[string]any) (configured, ignored map[string]bool) {
	configured, ignored = map[string]bool{}, map[string]bool{}
	for _, raw := range listOf(dispatch["gate_dispatch"]) {
		item, _ := raw.(map[string]any)
		id, _ := item["gate_id"].(string)
		switch item["status"] {
		case "required":
			configured[id] = true
		case "ignored":
			ignored[id] = true
		}
	}
	return configured, ignored
}

func executionGates(record map[string]any) map[string]map[string]any {
	summary, _ := record["execution_summary"].(map[string]any)
	gates, _ := summary["gates"].(map[string]any)
	out := map[string]map[string]any{}
	for id, raw := range gates {
		if gate, ok := raw.(map[string]any); ok {
			out[id] = gate
		}
	}
	return out
}

func listOf(value any) []any {
	list, _ := value.([]any)
	return list
}

// jsonEqual compares two parsed-JSON values the way Python's != would.
//
// The distinction it preserves is between absent and empty. A run record that
// omits required_agents is not making the same claim as one declaring none,
// and collapsing the two would let a record say a gate needed nobody by
// saying nothing at all -- which is exactly the shape a hand-edited record
// takes.
func jsonEqual(left, right any) bool { return reflect.DeepEqual(left, right) }

// asJSONList lifts a []string into the []any shape a parsed document holds,
// so the comparison above is between like and like. Never nil: the contract
// side always states a list, even an empty one.
func asJSONList(values []string) []any {
	list := make([]any, 0, len(values))
	for _, value := range values {
		list = append(list, value)
	}
	return list
}

// asJSONArtifacts does the same for the per-gate artifact requirement. The
// lifecycle contract declares none today, so this is almost always an empty
// list -- the point being that a record must still say so.
func asJSONArtifacts(values []map[string]string) []any {
	list := make([]any, 0, len(values))
	for _, value := range values {
		entry := map[string]any{}
		for key, item := range value {
			entry[key] = item
		}
		list = append(list, entry)
	}
	return list
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameStringSet(left, right []string) bool {
	leftSet, rightSet := map[string]bool{}, map[string]bool{}
	for _, value := range left {
		leftSet[value] = true
	}
	for _, value := range right {
		rightSet[value] = true
	}
	if len(leftSet) != len(rightSet) {
		return false
	}
	for value := range leftSet {
		if !rightSet[value] {
			return false
		}
	}
	return true
}

// pythonRepr renders a value the way Python's %r does, for the schema
// messages that quote what they rejected.
func pythonRepr(value any) string {
	if value == nil {
		return "None"
	}
	if text, ok := value.(string); ok {
		return "'" + text + "'"
	}
	if boolean, ok := value.(bool); ok {
		if boolean {
			return "True"
		}
		return "False"
	}
	return fmt.Sprint(value)
}
