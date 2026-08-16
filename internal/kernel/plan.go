package kernel

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// `agentic-sdlc plan` -- turn a sentence describing a task into a dispatch
// plan and the run record that will accumulate what happens to it.
//
// This is the command every other one reads the output of, so two properties
// carry more weight than the rest:
//
//   - It is deterministic. The same task text against the same project
//     routing produces the same plan, and the fingerprint over that plan is
//     what `validate` re-checks later. Wall-clock timestamps are the only
//     part that varies, and they are excluded from the fingerprint for
//     exactly that reason.
//   - It is idempotent, and refuses rather than overwrites. Re-planning a
//     task ID whose text or routing has changed is not an update -- it is a
//     different task wearing an existing ID, with a run record already
//     accumulating approvals against the old one.
//
// It never approves anything. `advanceLifecycle` moves at most one gate to
// `ready`, which means "the prerequisites are met and somebody may now look at
// it" -- never to `approved`.

// nowRFC3339 is the timestamp format the kernel writes: UTC, Z-suffixed.
func nowRFC3339() string {
	return timeNow().UTC().Format("2006-01-02T15:04:05.000000") + "Z"
}

// timeNow is a seam, not a feature. Two behaviours here can only be checked
// against a fixed clock -- the gate-status comment's post-write verification
// compares a rendered body against what the forge echoed back, and that body
// carries a live timestamp -- so tests replace this and restore it. Nothing in
// production ever assigns to it.
var timeNow = time.Now

// ChooseWorkflow picks the workflow for a task and the routes it matched.
//
// Phrase order is the policy: a production deployment is a production release
// even if it also matched a build route, and an incident outranks everything
// below it. The fallthrough is `needs-triage` rather than a guess -- a task
// nothing matched is one a human should look at, and inventing a workflow for
// it would dispatch agents on the strength of no evidence at all.
func ChooseWorkflow(text string, routes []any) (string, []map[string]any) {
	lowered := strings.ToLower(text)

	var matched []map[string]any
	for _, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for _, phraseRaw := range listOf(route["phrases"]) {
			phrase, _ := phraseRaw.(string)
			if phrase != "" && strings.Contains(lowered, strings.ToLower(phrase)) {
				matched = append(matched, route)
				break
			}
		}
	}

	if containsAny(lowered, "deploy to production", "production deployment") {
		return "production-release", matched
	}
	if containsAny(lowered, "major incident", "incident response", "service outage") {
		return "support-escalation", matched
	}
	if anyRouteID(matched, "runtime-assurance") {
		return "runtime-assurance", matched
	}
	if anyRouteID(matched, "debugging") {
		return "debugging", matched
	}

	intake := anyRouteID(matched, "product-intake")
	design := false
	for _, route := range matched {
		id, _ := route["id"].(string)
		if id != "product-intake" && id != "runtime-assurance" && id != "debugging" {
			design = true
		}
	}
	switch {
	case intake && !design:
		return "product-intake", matched
	case len(matched) > 0:
		return "new-service", matched
	default:
		return "needs-triage", matched
	}
}

func containsAny(text string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func anyRouteID(routes []map[string]any, id string) bool {
	for _, route := range routes {
		if route["id"] == id {
			return true
		}
	}
	return false
}

// LifecycleSequence expands the gates a task's routes asked for into the
// prefix of the lifecycle it must pass through.
//
// A task that needs G5 needs G1 through G5: gates are cumulative, and one
// asking for a later gate has implicitly asked for everything before it. The
// returned ignore set is intersected with that prefix, so ignoring a gate the
// task never reaches is not an error -- it is simply not part of this run.
func LifecycleSequence(gateIDs, ignoredGates []string) ([]string, map[string]bool, error) {
	known := map[string]bool{}
	for _, id := range GateIDs {
		known[id] = true
	}
	var unknown []string
	for _, id := range ignoredGates {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		unknown = uniqueStrings(unknown)
		sort.Strings(unknown)
		return nil, nil, fmt.Errorf(
			"ignored_gates contains unknown lifecycle gates: %s", pythonList(unknown))
	}
	if len(gateIDs) == 0 {
		return []string{}, map[string]bool{}, nil
	}

	highest := 0
	for _, id := range gateIDs {
		if index := gateIndex(id); index > highest {
			highest = index
		}
	}
	sequence := append([]string(nil), GateIDs[:highest+1]...)

	inSequence := map[string]bool{}
	for _, id := range sequence {
		inSequence[id] = true
	}
	ignored := map[string]bool{}
	for _, id := range ignoredGates {
		if inSequence[id] {
			ignored[id] = true
		}
	}
	return sequence, ignored, nil
}

// makeGateRecord builds one gate's entry in a fresh run record.
//
// Two states here are deliberately not "fine by default". A gate whose impact
// applicability is unresolved starts `blocked` with applicability `unknown`,
// not `applicable`: nobody has said the gate does not apply, they have not yet
// said anything. And an authority nobody is assigned to is recorded as
// `unknown` applicability rather than omitted, so the gap is visible in the
// record instead of inferred from an absence.
func makeGateRecord(
	gate map[string]any, impact map[string]any, authorities map[string]any, ignored bool,
) *orderedObject {
	gateID, _ := gate["id"].(string)
	affectedUnknown := len(listOf(impact["blocking_unknowns"])) > 0 &&
		(gateID == "G3" || gateID == "G4" || gateID == "G5" || gateID == "G7")

	requirements := []any{}
	for _, raw := range listOf(gate["authority_requirements"]) {
		authorityID, _ := raw.(string)
		if _, known := AuthorityRoles[authorityID]; !known {
			continue
		}
		authority, _ := authorities[authorityID].(map[string]any)
		assigned := authority["status"] == "assigned"
		applicability, rationale := "unknown", "Authority is not assigned"
		if assigned {
			applicability, rationale = "applicable", "Assigned in project authority map"
		}
		requirements = append(requirements, ordered(
			"authority_id", authorityID,
			"authority_type", "human-approver",
			"role", RoleLabels[authorityID],
			"applicability", applicability,
			"rationale", rationale,
		))
	}

	applicability := "applicable"
	rationale := "Lifecycle gate applies by default"
	switch {
	case ignored:
		applicability = "not-applicable"
		rationale = "Explicitly configured lifecycle gate ignore"
	case affectedUnknown:
		applicability = "unknown"
		rationale = "Impact applicability is unresolved"
	}
	status := "pending"
	var reentry any
	if affectedUnknown {
		status = "blocked"
		reentry = gateID
	}

	return ordered(
		"tier", "lifecycle",
		"gate_id", gateID,
		"name", gate["name"],
		"status", status,
		"applicability", applicability,
		"applicability_rationale", rationale,
		"artifact_bindings", []any{},
		"preparers", []any{},
		"independent_verifier", nil,
		"independence_declaration", ordered(
			"verifier_confirmed_not_preparer", false,
			"verifier_made_material_correction", false,
		),
		"authority_requirements", requirements,
		"human_approvals", []any{},
		"decided_at", nil,
		"evidence_refs", []any{},
		"knowledge_status", "unavailable",
		"findings", []any{},
		"exceptions", []any{},
		"invalidation_history", []any{},
		"required_reentry_gate", reentry,
	)
}

// ordered builds a JSON object whose keys stay in the order given.
//
// Key order is part of the output contract: these documents are written to
// disk, read back by other tooling, and diffed by people. Go's map ordering
// would put G10 between G1 and G2.
func ordered(pairs ...any) *orderedObject {
	object := &orderedObject{values: map[string]any{}}
	object.setAll(pairs...)
	return object
}

// setAll appends key/value pairs in the order given.
func (o *orderedObject) setAll(pairs ...any) {
	for index := 0; index+1 < len(pairs); index += 2 {
		key, _ := pairs[index].(string)
		o.set(key, pairs[index+1])
	}
}

// deriveCurrentPhase reports the phase of the earliest gate still outstanding.
func deriveCurrentPhase(record *orderedObject, contracts map[string]map[string]any) string {
	phaseOf := map[string]string{}
	for id, contract := range contracts {
		phase, _ := contract["phase"].(string)
		phaseOf[id] = phase
	}
	for _, raw := range listOf(record.values["lifecycle_gates"]) {
		gate, ok := raw.(*orderedObject)
		if !ok {
			continue
		}
		if gate.values["applicability"] == "not-applicable" {
			continue
		}
		if gate.values["status"] != "approved" {
			gateID, _ := gate.values["gate_id"].(string)
			if phase, known := phaseOf[gateID]; known {
				return phase
			}
			current, _ := record.values["current_lifecycle_phase"].(string)
			if current == "" {
				return "intent"
			}
			return current
		}
	}
	return "feedback"
}

// advanceLifecycle moves at most one gate to `ready`, and never to `approved`.
//
// "Ready" means the prerequisites are met and a human or an agent may now do
// the work -- it is an invitation, not a decision. Everything this function
// refuses to advance past is a question somebody has to answer: an unresolved
// impact applicability, an authority whose applicability is unknown, a gate
// whose project routing binds no agent to the contributions its contract
// requires.
func advanceLifecycle(
	record *orderedObject, routing map[string]any, contracts map[string]map[string]any,
) {
	gates := listOf(record.values["lifecycle_gates"])
	statusByID := map[string]string{}
	for _, raw := range gates {
		if gate, ok := raw.(*orderedObject); ok {
			id, _ := gate.values["gate_id"].(string)
			status, _ := gate.values["status"].(string)
			statusByID[id] = status
		}
	}

	for _, raw := range gates {
		gate, ok := raw.(*orderedObject)
		if !ok {
			continue
		}
		status, _ := gate.values["status"].(string)
		if gate.values["applicability"] == "not-applicable" ||
			status == "approved" || status == "invalidated" {
			continue
		}
		if status != "pending" && status != "blocked" {
			continue
		}
		gateID, _ := gate.values["gate_id"].(string)
		contract := contracts[gateID]

		prerequisitesMet := true
		for _, prerequisiteRaw := range listOf(contract["prerequisites"]) {
			prerequisite, _ := prerequisiteRaw.(string)
			if statusByID[prerequisite] != "approved" {
				prerequisitesMet = false
			}
		}
		if !prerequisitesMet {
			continue
		}
		if gate.values["applicability"] != "applicable" {
			continue
		}
		unresolvedAuthority := false
		for _, requirementRaw := range listOf(gate.values["authority_requirements"]) {
			requirement, ok := requirementRaw.(*orderedObject)
			if ok && requirement.values["applicability"] == "unknown" {
				unresolvedAuthority = true
			}
		}
		if unresolvedAuthority {
			continue
		}

		binding := GateDispatchBinding(contract, routing)
		required := []string{"agents", "tasks", "artifacts"}
		if contract["human_only"] == true {
			// A human-only gate binds no agent by definition, so requiring one
			// would block it forever.
			required = []string{"tasks", "artifacts"}
		}
		if len(listOf(contract["required_contributions"])) > 0 {
			complete := true
			for _, field := range required {
				if len(binding[field]) == 0 {
					complete = false
				}
			}
			if !complete {
				continue
			}
		}

		gate.set("status", "ready")
		break
	}
	record.set("current_lifecycle_phase", deriveCurrentPhase(record, contracts))
}

// wordBoundary matches a keyword as a whole word, the way the Python kernel's
// change-intake regex does: "release" must not match inside "released".
func wordBoundary(keyword string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(strings.ToLower(keyword)) + `([^a-z0-9]|$)`)
}

// PlanRequest is one `plan` invocation.
type PlanRequest struct {
	Root   string
	TaskID string
	Task   string
}

// PlanResult is what `plan` produced and where it put it.
type PlanResult struct {
	Dispatch     *orderedObject
	Record       *orderedObject
	DispatchPath string
	RecordPath   string
}

// Plan builds a task's dispatch plan and run record and writes both.
func (r *Registry) Plan(request PlanRequest) (*PlanResult, error) {
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
	contracts, err := lifecycleGateContracts()
	if err != nil {
		return nil, err
	}
	lifecycle, err := lifecycleGateList()
	if err != nil {
		return nil, err
	}

	routing := overlay.Routing
	workflow, matched := ChooseWorkflow(request.Task, listOf(routing["routes"]))
	if len(r.Providers) == 0 && workflow != "needs-triage" {
		// A plan naming agents no provider supplies is a plan nobody can
		// execute, and it would be written to disk looking executable.
		return nil, fmt.Errorf(
			"agent dispatch requires a loaded provider; rerun with --provider <manifest>, " +
				"or use a kernel-only lifecycle operation")
	}

	var primary, reviewers, support, gates []string
	for _, route := range matched {
		primary = append(primary, stringsIn(route["agents"])...)
		reviewers = append(reviewers, stringsIn(route["reviewers"])...)
		support = append(support, stringsIn(route["support"])...)
		gates = append(gates, stringsIn(route["gates"])...)
	}

	// Change intake matches on whole words rather than substrings, so a task
	// mentioning "released" does not get pulled into the change process.
	intake, _ := routing["change_intake"].(map[string]any)
	loweredTask := strings.ToLower(request.Task)
	changeWork := false
	for _, keywordRaw := range listOf(intake["keywords"]) {
		keyword, _ := keywordRaw.(string)
		if keyword != "" && wordBoundary(keyword).MatchString(loweredTask) {
			changeWork = true
		}
	}
	if changeWork {
		support = append(support, stringsIn(intake["agents"])...)
		gates = append(gates, stringsIn(intake["quality_gates"])...)
	}

	primary = uniqueStrings(primary)
	reviewers = uniqueStrings(reviewers)
	support = uniqueStrings(support)
	gates = uniqueStrings(gates)
	// The Python kernel sorts `gates` numerically here. It is inert -- the
	// only thing that reads the list before it is rebuilt below is
	// LifecycleSequence, which takes the highest index and ignores order, and
	// the rebuild then re-derives the order from the sequence itself. Not
	// carried over, because a sort whose result is discarded reads like it
	// matters. Falsifying it proves nothing either way, which is the tell.

	sequence, ignored, err := LifecycleSequence(gates, stringsIn(routing["ignored_gates"]))
	if err != nil {
		return nil, err
	}
	gates = gates[:0]
	for _, id := range sequence {
		if !ignored[id] {
			gates = append(gates, id)
		}
	}
	// The separation, at the point the plan is built: an agent authoring a
	// change cannot also be its reviewer, so a route naming one in both slots
	// keeps it as the author only.
	reviewers = withoutAny(reviewers, primary)
	if workflow == "production-release" {
		support = uniqueStrings(append(support, "release-engineer"))
		gates = uniqueStrings(append(gates, "G8", "G9"))
	}

	humanGates, err := matchedHumanGates(request.Task)
	if err != nil {
		return nil, err
	}

	required := []any{}
	for _, gateID := range gates {
		var contributing []string
		for _, route := range matched {
			for _, routeGate := range stringsIn(route["gates"]) {
				if routeGate == gateID {
					id, _ := route["id"].(string)
					contributing = append(contributing, id)
					break
				}
			}
		}
		if len(contributing) == 0 {
			contributing = []string{"workflow:" + workflow}
		}
		required = append(required, ordered(
			"id", gateID,
			"required", true,
			"reason", "Required by matched project route or workflow",
			"contributing_routes", asJSONList(contributing),
		))
	}

	bindings := map[string]map[string][]string{}
	for _, gateID := range sequence {
		bindings[gateID] = GateDispatchBinding(contracts[gateID], routing)
	}
	var gateAgents []string
	for _, gateID := range sequence {
		if !ignored[gateID] {
			gateAgents = append(gateAgents, bindings[gateID]["agents"]...)
		}
	}
	support = uniqueStrings(append(support, gateAgents...))
	support = withoutAny(support, primary)
	support = withoutAny(support, reviewers)

	gateDispatch := []any{}
	for _, gateID := range sequence {
		status := "required"
		if ignored[gateID] {
			status = "ignored"
		}
		gateDispatch = append(gateDispatch, ordered(
			"gate_id", gateID,
			"status", status,
			"agents", asJSONList(bindings[gateID]["agents"]),
			"tasks", asJSONList(bindings[gateID]["tasks"]),
			"artifacts", asJSONList(bindings[gateID]["artifacts"]),
		))
	}

	var ignoredList []string
	for _, id := range GateIDs {
		if ignored[id] {
			ignoredList = append(ignoredList, id)
		}
	}

	planStatus := "ready"
	if workflow == "needs-triage" {
		planStatus = "needs-triage"
	}
	project := overlay.Project
	dispatch := ordered(
		"schema_version", 2,
		"task_id", taskID,
		"generated_at", nowRFC3339(),
		"status", planStatus,
		"workflow", workflow,
		"inputs", ordered("task", request.Task, "classification", project["classification"]),
		"matched_routes", asJSONList(routeIDs(matched)),
		"matched_risks", []any{},
		"agents", ordered(
			"primary", asJSONList(primary),
			"reviewers", asJSONList(reviewers),
			"support", asJSONList(support),
		),
		"required_quality_gates", required,
		"ignored_quality_gates", asJSONList(ignoredList),
		"gate_dispatch", gateDispatch,
		"human_gates", humanGates,
		"knowledge_context", ordered(
			"status", "unavailable",
			"reason", "No portable knowledge source configured",
			"requests", []any{},
		),
	)

	plainDispatch, _ := plainValue(dispatch).(map[string]any)
	fingerprint, err := DispatchFingerprint(plainDispatch)
	if err != nil {
		return nil, err
	}
	dispatch.set("dispatch_fingerprint", fingerprint)

	// The impact profile is embedded in the record verbatim, so it is re-read
	// order-preserving rather than taken from the parsed overlay: a document
	// copied into another document keeps the shape its author gave it, and a
	// Go map would come back alphabetised.
	impactDocument, err := loadOrderedDocument(root, "impact-profile.json")
	if err != nil {
		return nil, err
	}
	record, err := r.newRunRecord(taskID, request.Task, fingerprint, overlay, impactDocument,
		lifecycle, contracts, bindings, sequence, ignored)
	if err != nil {
		return nil, err
	}

	dispatchPath, err := ConfinedPath(root, Overlay, "runs", taskID, "dispatch-plan.json")
	if err != nil {
		return nil, err
	}
	recordPath, err := ConfinedPath(root, Overlay, "runs", taskID, "run-record.json")
	if err != nil {
		return nil, err
	}
	if err := refusePlanConflict(dispatchPath, recordPath, taskID, request.Task, fingerprint); err != nil {
		return nil, err
	}

	if err := writeJSONDocument(dispatchPath, dispatch); err != nil {
		return nil, err
	}
	if _, err := os.Stat(recordPath); err != nil {
		advanceLifecycle(record, routing, contracts)
		if err := writeJSONDocument(recordPath, record); err != nil {
			return nil, err
		}
	}
	return &PlanResult{
		Dispatch: dispatch, Record: record,
		DispatchPath: dispatchPath, RecordPath: recordPath,
	}, nil
}

// refusePlanConflict rejects a re-plan that would change what an existing task
// ID means.
//
// Not an overwrite, and not a merge: a run record accumulates approvals
// against a specific plan, so silently re-planning underneath it would leave
// approvals attached to work nobody approved. The caller is told to use a new
// task ID or invalidate the existing run explicitly.
func refusePlanConflict(dispatchPath, recordPath, taskID, task, fingerprint string) error {
	if _, err := os.Stat(dispatchPath); err == nil {
		existing, err := loadJSONObject(dispatchPath)
		if err != nil {
			return err
		}
		inputs, _ := existing["inputs"].(map[string]any)
		if inputs["task"] != task {
			return fmt.Errorf(
				"task ID %s already exists with different task text; use a new task ID", taskID)
		}
		if existing["dispatch_fingerprint"] != fingerprint {
			return fmt.Errorf(
				"task ID %s routing has changed; use a new task ID or explicitly invalidate "+
					"the existing run", taskID)
		}
	}
	if _, err := os.Stat(recordPath); err == nil {
		existing, err := loadJSONObject(recordPath)
		if err != nil {
			return err
		}
		if existing["scope"] != task {
			return fmt.Errorf(
				"task ID %s already exists with different task text; use a new task ID", taskID)
		}
		if existing["dispatch_fingerprint"] != fingerprint {
			return fmt.Errorf(
				"task ID %s has an existing run record for different task or routing state; "+
					"use a new task ID", taskID)
		}
	}
	return nil
}

// newRunRecord builds the record a task's history will be written into.
func (r *Registry) newRunRecord(
	taskID, task, fingerprint string,
	overlay *ProjectOverlay,
	impactDocument any,
	lifecycle []map[string]any,
	contracts map[string]map[string]any,
	bindings map[string]map[string][]string,
	sequence []string,
	ignored map[string]bool,
) (*orderedObject, error) {
	contractDigest, err := lifecycleContractDigest()
	if err != nil {
		return nil, err
	}
	bindingDigest, err := Fingerprint(orDefault(overlay.Routing["gate_bindings"], map[string]any{}))
	if err != nil {
		return nil, err
	}

	gateRecords := []any{}
	for _, gate := range lifecycle {
		gateID, _ := gate["id"].(string)
		gateRecords = append(gateRecords,
			makeGateRecord(gate, overlay.Impact, overlay.Authorities, ignored[gateID]))
	}

	inSequence := map[string]bool{}
	for _, id := range sequence {
		inSequence[id] = true
	}
	executions := &orderedObject{values: map[string]any{}}
	for _, gateID := range GateIDs {
		var ignoreReason any
		if ignored[gateID] {
			ignoreReason = "Configured in project routing"
		}
		binding := bindings[gateID]
		if binding == nil {
			binding = map[string][]string{"agents": {}, "tasks": {}}
		}
		executions.set(gateID, ordered(
			"configured", inSequence[gateID],
			"ignored", ignored[gateID],
			"ignore_reason", ignoreReason,
			"required_agents", asJSONList(binding["agents"]),
			"dispatched_agents", []any{},
			"required_tasks", asJSONList(binding["tasks"]),
			"completed_tasks", []any{},
			"required_agent_artifacts", asJSONArtifacts(GateAgentArtifacts(contracts[gateID])),
			"produced_agent_artifacts", []any{},
		))
	}

	project := overlay.Project
	return ordered(
		"version", 2,
		"task_id", taskID,
		"recorded_at", nowRFC3339(),
		"classification", project["classification"],
		"mode", "planning-review-only",
		"baseline_revision", "unresolved",
		"scope", task,
		"dispatch_fingerprint", fingerprint,
		"kernel_version", Version,
		"contract_digest", contractDigest,
		"provider_bindings", providerRecords(r.Providers),
		"profile", project["profile"],
		"profile_digest", project["profile_digest"],
		"dispatch_binding_digest", bindingDigest,
		"disposition", "pending",
		"intent_record_id", nil,
		"requirements_baseline_id", nil,
		"current_lifecycle_phase", "intent",
		"knowledge_retrieval", ordered(
			"status", "unavailable",
			"reason", "No portable knowledge source configured",
			"query_ids", []any{},
			"evidence_refs", []any{},
			"influence", "none",
		),
		"impact_profile", impactDocument,
		"lifecycle_gates", gateRecords,
		"specialist_attestations", []any{},
		"re_entry_history", []any{},
		"execution_summary", ordered("gates", executions),
	), nil
}

// matchedHumanGates finds the mutation gates a task's own words trigger.
//
// Substring matching, deliberately: these phrases name irreversible actions,
// and the cost of catching one that was only mentioned in passing is a human
// being asked, while the cost of missing one is an agent doing it.
func matchedHumanGates(task string) ([]any, error) {
	raw, err := EmbeddedContract("mutation-gates.json")
	if err != nil {
		return nil, err
	}
	contract, err := decodeJSONObject(raw)
	if err != nil {
		return nil, err
	}
	lowered := strings.ToLower(task)

	matched := []any{}
	seen := map[string]bool{}
	for _, gateRaw := range listOf(contract["human_only"]) {
		gate, ok := gateRaw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := gate["id"].(string)
		for _, phraseRaw := range listOf(gate["phrases"]) {
			phrase, _ := phraseRaw.(string)
			if phrase == "" || !strings.Contains(lowered, phrase) {
				continue
			}
			// Last phrase wins, matching the Python kernel's dict assignment;
			// the entry is replaced rather than appended, so a gate matching
			// two phrases still appears once.
			if !seen[id] {
				seen[id] = true
				matched = append(matched, ordered(
					"id", id, "required", true,
					"reason", "Matched human-only phrase: "+phrase))
				continue
			}
			for index, existing := range matched {
				if entry, ok := existing.(*orderedObject); ok && entry.values["id"] == id {
					matched[index] = ordered(
						"id", id, "required", true,
						"reason", "Matched human-only phrase: "+phrase)
				}
			}
		}
	}
	return matched, nil
}

func lifecycleGateList() ([]map[string]any, error) {
	raw, err := EmbeddedContract("lifecycle-gates.json")
	if err != nil {
		return nil, err
	}
	contract, err := decodeJSONObject(raw)
	if err != nil {
		return nil, err
	}
	var gates []map[string]any
	for _, item := range listOf(contract["gates"]) {
		if gate, ok := item.(map[string]any); ok {
			gates = append(gates, gate)
		}
	}
	return gates, nil
}

// lifecycleContractDigest hashes the whole lifecycle contract, so a record
// says which version of the rules it was written under.
func lifecycleContractDigest() (string, error) {
	raw, err := EmbeddedContract("lifecycle-gates.json")
	if err != nil {
		return "", err
	}
	contract, err := decodeJSONObject(raw)
	if err != nil {
		return "", err
	}
	return Fingerprint(contract)
}

// plainValue converts ordered objects back to plain maps, for the fingerprint,
// which sorts keys itself and must not see an order at all.
func plainValue(value any) any {
	switch typed := value.(type) {
	case *orderedObject:
		plain := map[string]any{}
		for key, item := range typed.values {
			plain[key] = plainValue(item)
		}
		return plain
	case []any:
		list := make([]any, 0, len(typed))
		for _, item := range typed {
			list = append(list, plainValue(item))
		}
		return list
	default:
		return value
	}
}

// writeJSONDocument writes a document the way the Python kernel does:
// json.dumps(value, indent=2) plus a trailing newline.
func writeJSONDocument(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(RenderIndented(value)), 0o644)
}

func withoutAny(values, excluded []string) []string {
	drop := map[string]bool{}
	for _, value := range excluded {
		drop[value] = true
	}
	kept := []string{}
	for _, value := range values {
		if !drop[value] {
			kept = append(kept, value)
		}
	}
	return kept
}

func routeIDs(routes []map[string]any) []string {
	ids := []string{}
	for _, route := range routes {
		id, _ := route["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func orDefault(value any, fallback any) any {
	if value == nil {
		return fallback
	}
	return value
}

// providerRecords renders the loaded providers the way a run record stores
// them: id, version, and both digests, so a later reader can tell whether the
// providers underneath a task have changed since it was planned.
func providerRecords(providers []LoadedProvider) []any {
	records := []any{}
	for _, provider := range providers {
		dependencies := provider.Dependencies
		if dependencies == nil {
			dependencies = []any{}
		}
		records = append(records, ordered(
			"id", provider.ID,
			"version", provider.Version,
			"manifest_sha256", provider.ManifestSHA256,
			"catalog_sha256", provider.CatalogSHA256,
			"dependencies", dependencies,
		))
	}
	return records
}

// loadOrderedDocument reads one overlay document with its key order intact.
func loadOrderedDocument(root, name string) (any, error) {
	path, err := ConfinedPath(root, Overlay, name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeOrdered(data)
}
