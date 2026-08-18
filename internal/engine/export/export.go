// Package export reassembles checkpointed graph state into a run record
// shaped by run-record.schema.json.
//
// Ported from engine/agentic_sdlc_langgraph/export.py.
//
// The distinction the module exists to get right: a gate that is in scope but
// has not been reached yet must be exported as applicable-and-pending, never
// as not-applicable. The two look similar in a record and mean opposite things
// -- "still to do" against "out of scope" -- and an earlier version of this
// pipeline conflated them.
package export

import (
	"time"

	"github.com/deagy/cadre/cli/internal/engine/contracts"
)

// ZeroDigest is the placeholder used where a real digest is not computed yet.
const ZeroDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// AllGateIDs is the fixed lifecycle sequence the schema requires.
var AllGateIDs = []string{"G1", "G2", "G3", "G4", "G5", "G6", "G7", "G8", "G9", "G10"}

// gateNames and phaseByGateID mirror lifecycle-gates.json.
//
// Held here rather than read from the contract so this stays a leaf, exactly
// as the Python is. That is a second source of truth, so a test asserts both
// maps against the shipped contract -- they agree today, and the point is to
// find out when they stop.
var gateNames = map[string]string{
	"G1":  "Intent",
	"G2":  "Requirements Baseline",
	"G3":  "Architecture",
	"G4":  "Governance and Data",
	"G5":  "Security and Crypto",
	"G6":  "Verification and Test",
	"G7":  "Evidence",
	"G8":  "Release Readiness",
	"G9":  "Deployment Authorization",
	"G10": "Runtime Conformance",
}

var phaseByGateID = map[string]string{
	"G1":  "intent",
	"G2":  "requirements",
	"G3":  "architecture",
	"G4":  "governance-data",
	"G5":  "security-crypto",
	"G6":  "verify",
	"G7":  "evidence",
	"G8":  "release-readiness",
	"G9":  "deployment-authorization",
	"G10": "runtime-conformance",
}

// Options mirrors export_run_record's keyword arguments.
//
// SequenceGateIDs distinguishes nil from empty deliberately, as the Python
// distinguishes None from []. Nil means "assume every gate is in scope";
// empty means "no gate is", which exports all ten as not-applicable. Reading
// an explicit empty list as "all" would silently widen a caller's scope.
type Options struct {
	AllGateIDs      []string
	SequenceGateIDs []string
	IgnoredGateIDs  []string
	GateBindings    map[string]contracts.GateBinding

	// Now allows a caller to pin recorded_at. Zero means time.Now().
	Now time.Time
}

func basePlaceholderGate(gateID string) map[string]any {
	return map[string]any{
		"tier":                    "lifecycle",
		"gate_id":                 gateID,
		"name":                    gateNames[gateID],
		"applicability":           "applicable",
		"applicability_rationale": nil,
		"status":                  "pending",
		"artifact_bindings":       []any{},
		"preparers":               []any{},
		"independent_verifier":    nil,
		"independence_declaration": map[string]any{
			"verifier_confirmed_not_preparer":   false,
			"verifier_made_material_correction": false,
		},
		"authority_requirements": []any{},
		"human_approvals":        []any{},
		"decided_at":             nil,
		"evidence_refs":          []any{},
		"knowledge_status":       "unavailable",
		"findings":               []any{},
		"exceptions":             []any{},
		"invalidation_history":   []any{},
		"required_reentry_gate":  nil,
	}
}

// pendingPlaceholderGate is in the derived sequence but not yet reached.
func pendingPlaceholderGate(gateID string) map[string]any {
	gate := basePlaceholderGate(gateID)
	gate["applicability_rationale"] = "Lifecycle gate is in the derived sequence for this task but has not yet been reached"
	return gate
}

func notApplicablePlaceholderGate(gateID, rationale string) map[string]any {
	gate := basePlaceholderGate(gateID)
	gate["applicability"] = "not-applicable"
	gate["applicability_rationale"] = rationale
	gate["knowledge_status"] = "not-applicable"
	return gate
}

func executionSummaryGate(gateID string, configured, ignored bool, ignoreReason any, bindings map[string]contracts.GateBinding) map[string]any {
	var requiredAgents, requiredTasks []string
	binding, bound := bindings[gateID]
	if bound {
		// Slot iteration order is not defined for a map, so the gate's own
		// declared order cannot be recovered here; the Python iterated a dict
		// whose order came from the JSON. Sorted for determinism, since this
		// field is a summary rather than a dispatch instruction.
		for _, slot := range sortedKeys(binding.Contributions) {
			contribution := binding.Contributions[slot]
			requiredAgents = appendUnique(requiredAgents, contribution.Agents)
			requiredTasks = appendUnique(requiredTasks, contribution.Tasks)
		}
	}
	if !configured {
		requiredAgents, requiredTasks = nil, nil
	}
	return map[string]any{
		"configured":               configured,
		"ignored":                  ignored,
		"ignore_reason":            ignoreReason,
		"required_agents":          orEmpty(requiredAgents),
		"dispatched_agents":        []any{},
		"required_tasks":           orEmpty(requiredTasks),
		"completed_tasks":          []any{},
		"required_agent_artifacts": []any{},
		"produced_agent_artifacts": []any{},
	}
}

// RunRecord builds a schema-shaped run record from graph state.
func RunRecord(state map[string]any, options Options) map[string]any {
	allGateIDs := options.AllGateIDs
	if len(allGateIDs) == 0 {
		allGateIDs = AllGateIDs
	}

	inSequence := map[string]bool{}
	if options.SequenceGateIDs == nil {
		for _, id := range allGateIDs {
			inSequence[id] = true
		}
	} else {
		for _, id := range options.SequenceGateIDs {
			inSequence[id] = true
		}
	}
	ignored := map[string]bool{}
	for _, id := range options.IgnoredGateIDs {
		ignored[id] = true
	}

	modelled, _ := state["lifecycle_gates"].(map[string]any)

	lifecycleGates := make([]any, 0, len(allGateIDs))
	executionSummaryGates := map[string]any{}

	const ignoredReason = "Explicitly excluded via ignored_gate_ids"
	for _, gateID := range allGateIDs {
		switch {
		case modelled[gateID] != nil:
			lifecycleGates = append(lifecycleGates, modelled[gateID])
			executionSummaryGates[gateID] = executionSummaryGate(gateID, true, false, nil, options.GateBindings)
		case ignored[gateID]:
			lifecycleGates = append(lifecycleGates, notApplicablePlaceholderGate(gateID, ignoredReason))
			executionSummaryGates[gateID] = executionSummaryGate(gateID, false, true, ignoredReason, options.GateBindings)
		case !inSequence[gateID]:
			lifecycleGates = append(lifecycleGates,
				notApplicablePlaceholderGate(gateID, "Not part of the derived gate sequence for this task"))
			executionSummaryGates[gateID] = executionSummaryGate(gateID, false, false, nil, options.GateBindings)
		default:
			lifecycleGates = append(lifecycleGates, pendingPlaceholderGate(gateID))
			executionSummaryGates[gateID] = executionSummaryGate(gateID, true, false, nil, options.GateBindings)
		}
	}

	// The first applicable gate that is not approved sets the phase;
	// "feedback" once every applicable gate is approved.
	currentPhase := "feedback"
	for _, entry := range lifecycleGates {
		gate, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if gate["applicability"] == "not-applicable" {
			continue
		}
		if gate["status"] != "approved" {
			phase, known := phaseByGateID[asString(gate["gate_id"])]
			if !known {
				phase = "intent"
			}
			currentPhase = phase
			break
		}
	}

	recordedAt := options.Now
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}

	return map[string]any{
		"version": 2,
		// get with a default, not `or`: an explicitly empty task_id stays
		// empty. classification and scope below use `or` in the Python and do
		// fall back on empty, which is why they go through orDefault.
		"task_id":                  orAbsent(state["task_id"], "unknown-task"),
		"dispatch_fingerprint":     ZeroDigest,
		"recorded_at":              recordedAt.Format(time.RFC3339Nano),
		"classification":           orDefault(state["classification"], "unclassified"),
		"mode":                     "langgraph-phase1",
		"baseline_revision":        "unresolved",
		"scope":                    orDefault(state["scope"], "unspecified"),
		"disposition":              "pending",
		"intent_record_id":         state["intent_record_id"],
		"requirements_baseline_id": state["requirements_baseline_id"],
		"current_lifecycle_phase":  currentPhase,
		"knowledge_retrieval": map[string]any{
			"status":        "unavailable",
			"reason":        "No portable knowledge source configured in this project",
			"query_ids":     []any{},
			"evidence_refs": []any{},
			"influence":     "none",
		},
		"impact_profile": map[string]any{
			"profile_id":        "phase1-langgraph",
			"status":            "draft",
			"impact_categories": []any{},
			"specialized_boms":  []any{},
			"blocking_unknowns": []any{},
		},
		"lifecycle_gates":         lifecycleGates,
		"specialist_attestations": []any{},
		"re_entry_history":        orEmptyAny(state["re_entry_history"]),
		"execution_summary":       map[string]any{"gates": executionSummaryGates},
		"kernel_version":          "0.1.0-langgraph-phase1",
		"contract_digest":         ZeroDigest,
		"provider_bindings":       []any{},
		"profile":                 "generic",
		"profile_digest":          nil,
		"dispatch_binding_digest": ZeroDigest,
	}
}

func appendUnique(into []string, values []string) []string {
	for _, value := range values {
		seen := false
		for _, existing := range into {
			if existing == value {
				seen = true
				break
			}
		}
		if !seen {
			into = append(into, value)
		}
	}
	return into
}

func sortedKeys(from map[string]contracts.Contribution) []string {
	keys := make([]string, 0, len(from))
	for key := range from {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func orEmptyAny(value any) any {
	if value == nil {
		return []any{}
	}
	return value
}

// orDefault mirrors Python's `x or fallback`: an empty string falls back too,
// which `state.get(k, fallback)` alone would not do.
func orDefault(value any, fallback string) any {
	text, isText := value.(string)
	if value == nil || (isText && text == "") {
		return fallback
	}
	return value
}

func asString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// orAbsent mirrors Python's `dict.get(key, fallback)`: the fallback applies
// only when the key is missing. An explicit empty value is preserved.
func orAbsent(value any, fallback string) any {
	if value == nil {
		return fallback
	}
	return value
}
