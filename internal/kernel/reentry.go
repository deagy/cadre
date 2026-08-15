package kernel

import (
	"fmt"
	"os"
)

// `invalidate`, `reenter` and `upgrade` -- the three commands that reach into
// a record after the fact.
//
// The first two are the pair that undoes work, and they are deliberately
// separate. `invalidate` says a gate and everything after it can no longer be
// relied on; `reenter` says the run is being restarted from there. Collapsing
// them into one command would make withdrawal and restart a single decision,
// when the whole point is that somebody decides to withdraw and somebody
// decides -- later, and knowing what was withdrawn -- to begin again.
//
// Both are cascading by construction. Invalidating G4 invalidates G5 through
// G10 as well, because those gates rest on work that has just been withdrawn;
// a gate whose foundation was removed but which keeps its own approval is
// exactly the record neither of these commands may leave behind.
//
// Both also demand an accountable identity and a stated reason. An
// invalidation with no actor is an approval disappearing with nobody's name
// on it.

// RecordSurgeryRequest is one `invalidate` or `reenter` invocation.
type RecordSurgeryRequest struct {
	Root         string
	TaskID       string
	EarliestGate string
	Reason       string
	Actor        string
}

// Invalidate withdraws a gate and every gate after it.
func (r *Registry) Invalidate(request RecordSurgeryRequest) (*orderedObject, error) {
	record, recordPath, err := r.loadOrderedRecord(request.Root, request.TaskID)
	if err != nil {
		return nil, err
	}
	start := gateIndex(request.EarliestGate)
	if start >= len(GateIDs) {
		return nil, fmt.Errorf("unknown lifecycle gate: %s", request.EarliestGate)
	}
	invalidated := GateIDs[start:]
	invalidatedSet := map[string]bool{}
	for _, id := range invalidated {
		invalidatedSet[id] = true
	}

	gates := listOf(record.values["lifecycle_gates"])
	for _, raw := range gates {
		gate, ok := raw.(*orderedObject)
		if !ok {
			continue
		}
		gateID, _ := gate.values["gate_id"].(string)
		if invalidatedSet[gateID] {
			gate.set("status", "invalidated")
			gate.set("required_reentry_gate", request.EarliestGate)
		}
	}

	// The phase moves back to the earliest invalidated gate's own phase. The
	// run is where that gate is again, not where it had reached.
	contract, err := lifecycleGateContracts()
	if err != nil {
		return nil, err
	}
	phase, _ := contract[request.EarliestGate]["phase"].(string)
	record.set("current_lifecycle_phase", phase)

	// Every artifact bound at or after the invalidated gate is named in the
	// history. That list is what tells a later reader which artifacts the
	// withdrawal actually covered, rather than leaving them to re-derive it
	// from a gate range.
	affected := []any{}
	for _, raw := range gatesFrom(gates, start) {
		affected = append(affected, listOf(raw.values["artifact_bindings"])...)
	}

	history := ordered(
		"invalidated_at", nowRFC3339(),
		"actor", request.Actor,
		"reason", request.Reason,
		"earliest_gate", request.EarliestGate,
		"invalidated_gate_ids", asJSONList(invalidated),
		"affected_artifact_bindings", affected,
		"superseding_artifact_id", nil,
	)
	record.set("re_entry_history", append(listOf(record.values["re_entry_history"]), history))
	// The same entry is attached to each invalidated gate as well as to the
	// run: a reader looking at one gate should not have to reconstruct why it
	// is invalid from a list somewhere else in the document.
	for _, gate := range gatesFrom(gates, start) {
		gate.set("invalidation_history",
			append(listOf(gate.values["invalidation_history"]), history))
	}

	if err := writeJSONDocument(recordPath, record); err != nil {
		return nil, err
	}
	return ordered(
		"task_id", request.TaskID,
		"earliest_gate", request.EarliestGate,
		"invalidated_gate_ids", asJSONList(invalidated),
	), nil
}

// Reenter clears an invalidated run back to pending from a gate onwards.
//
// Everything that made those gates approved is removed: approvals, evidence,
// artifact bindings, decision times. Not archived elsewhere and not left in
// place "for reference" -- a cleared gate that still carries its old approval
// is one where the next reader cannot tell what has been re-done.
func (r *Registry) Reenter(request RecordSurgeryRequest) (*orderedObject, error) {
	record, recordPath, err := r.loadOrderedRecord(request.Root, request.TaskID)
	if err != nil {
		return nil, err
	}
	root, err := resolveExisting(request.Root)
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
	start := gateIndex(request.EarliestGate)
	if start >= len(GateIDs) {
		return nil, fmt.Errorf("unknown lifecycle gate: %s", request.EarliestGate)
	}

	gates := listOf(record.values["lifecycle_gates"])
	for _, gate := range gatesFrom(gates, start) {
		gate.set("status", "pending")
		gate.set("required_reentry_gate", nil)
		gate.set("artifact_bindings", []any{})
		gate.set("evidence_refs", []any{})
		gate.set("human_approvals", []any{})
		gate.set("decided_at", nil)

		// A gate-level source link sets the gate's evidence and a top-level
		// record field as a pair. Clearing the evidence without clearing its
		// partner would leave intent_record_id or requirements_baseline_id
		// pointing at evidence that no longer exists.
		gateID, _ := gate.values["gate_id"].(string)
		if field, paired := recordFieldByGate[gateID]; paired {
			record.set(field, nil)
		}
	}

	advanceLifecycle(record, overlay.Routing, contracts)
	record.set("re_entry_history", append(listOf(record.values["re_entry_history"]), ordered(
		"invalidated_at", nowRFC3339(),
		"actor", request.Actor,
		"reason", request.Reason,
		"earliest_gate", request.EarliestGate,
		"invalidated_gate_ids", []any{},
		"affected_artifact_bindings", []any{},
		"superseding_artifact_id", nil,
	)))

	if err := writeJSONDocument(recordPath, record); err != nil {
		return nil, err
	}
	return ordered(
		"task_id", request.TaskID,
		"earliest_gate", request.EarliestGate,
		"status", "reentered",
	), nil
}

// recordFieldByGate pairs a gate with the top-level record field its source
// link populates.
var recordFieldByGate = map[string]string{
	"G1": "intent_record_id",
	"G2": "requirements_baseline_id",
}

// gatesFrom returns the gates at and after a position, skipping anything that
// is not an object.
func gatesFrom(gates []any, start int) []*orderedObject {
	if start > len(gates) {
		start = len(gates)
	}
	var out []*orderedObject
	for _, raw := range gates[start:] {
		if gate, ok := raw.(*orderedObject); ok {
			out = append(out, gate)
		}
	}
	return out
}

// loadOrderedRecord reads a task's run record with its key order intact.
func (r *Registry) loadOrderedRecord(root, taskID string) (*orderedObject, string, error) {
	resolved, err := resolveExisting(root)
	if err != nil {
		return nil, "", err
	}
	safeID, err := SafeTaskID(taskID)
	if err != nil {
		return nil, "", err
	}
	path, err := ConfinedPath(resolved, Overlay, "runs", safeID, "run-record.json")
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	decoded, err := DecodeOrdered(data)
	if err != nil {
		return nil, "", err
	}
	record, ok := decoded.(*orderedObject)
	if !ok {
		return nil, "", fmt.Errorf("%s: run record is not a JSON object", path)
	}
	return record, path, nil
}

// Upgrade reports, and optionally applies, a kernel lock upgrade.
//
// Two modes, and the split is the point: `--check` reports what would change
// and mutates nothing, `--apply` writes. A project pinned to an older kernel
// or an older contract digest should find that out before something starts
// behaving differently, and the check has to be safe to run anywhere for that
// to happen.
func (r *Registry) Upgrade(root string, apply bool) (*orderedObject, error) {
	resolved, err := resolveExisting(root)
	if err != nil {
		return nil, err
	}
	lockPath, err := ConfinedPath(resolved, Overlay, "version.lock")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return nil, err
	}
	decoded, err := DecodeOrdered(data)
	if err != nil {
		return nil, err
	}
	lock, ok := decoded.(*orderedObject)
	if !ok {
		return nil, fmt.Errorf("%s: version lock is not a JSON object", lockPath)
	}

	digest, err := lifecycleContractDigest()
	if err != nil {
		return nil, err
	}
	changes := []any{}
	if lock.values["kernel_version"] != Version {
		changes = append(changes, ordered(
			"field", "kernel_version", "from", lock.values["kernel_version"], "to", Version))
	}
	if lock.values["contract_digest"] != digest {
		changes = append(changes, ordered(
			"field", "contract_digest", "from", lock.values["contract_digest"], "to", digest))
	}

	status := "current"
	if len(changes) > 0 {
		status = "changes-available"
	}
	result := ordered("status", status, "mutation", false, "changes", changes)

	if apply {
		lock.set("plugin_version", Version)
		lock.set("kernel_version", Version)
		lock.set("contracts", 2)
		lock.set("contract_digest", digest)
		if err := writeJSONDocument(lockPath, lock); err != nil {
			return nil, err
		}
		result.set("status", "upgraded")
		result.set("mutation", true)
	}
	return result, nil
}
