package executor

import (
	"fmt"

	"github.com/deagy/cadre/cli/internal/engine/state"
)

// Invalidate marks a gate and everything after it as invalidated.
//
// Deliberately does not clear artifact_bindings, evidence_refs or
// human_approvals: they stay visible as a stale-but-audited record of what was
// believed at the time. Reenter is what clears them, and the two steps are
// separate because "this is no longer valid" and "we are redoing it" are
// different claims, and a run may sit in the first for a while.
//
// Records one Invalidation on the run's history and on each affected gate.
func (e *Executor) Invalidate(taskID, earliestGateID, reason, actor string) (state.Invalidation, error) {
	var record state.Invalidation

	checkpoint, found, err := e.Checkpointer.Load(taskID)
	if err != nil {
		return record, err
	}
	if !found {
		return record, fmt.Errorf("no checkpoint for task %s", taskID)
	}

	affected, err := e.gatesFrom(earliestGateID)
	if err != nil {
		return record, err
	}

	current := checkpoint.State
	var bindings []state.ArtifactBinding
	for _, gateID := range affected {
		if gate, present := current.LifecycleGates[gateID]; present {
			bindings = append(bindings, gate.ArtifactBindings...)
		}
	}

	record = state.Invalidation{
		InvalidatedAt:            e.now(),
		Actor:                    actor,
		Reason:                   reason,
		EarliestGate:             earliestGateID,
		InvalidatedGateIDs:       affected,
		AffectedArtifactBindings: orEmptyBindings(bindings),
	}

	updates := map[string]state.GateState{}
	for _, gateID := range affected {
		gate, present := current.LifecycleGates[gateID]
		if !present {
			continue
		}
		gate.Status = "invalidated"
		reentry := earliestGateID
		gate.RequiredReentryGate = &reentry
		gate.InvalidationHistory = append(gate.InvalidationHistory, record)
		updates[gateID] = gate
	}
	current.LifecycleGates = state.MergeGateUpdates(current.LifecycleGates, updates)
	current.ReEntryHistory = append(current.ReEntryHistory, record)

	// Pending is dropped: whatever decision the run was waiting for is about
	// a gate that no longer stands, and applying it afterwards would answer a
	// question that has been withdrawn.
	if err := e.Checkpointer.Save(taskID, Checkpoint{State: current}); err != nil {
		return record, err
	}
	return record, nil
}

// Reenter resets a gate and everything after it so they run again.
//
// Clears what Invalidate deliberately left: artifact bindings, evidence,
// approvals and the decision timestamp. Those describe work that is about to
// be redone, and leaving them would let a re-run inherit evidence for an
// artifact nobody produced this time.
//
// The agent outputs for those gates go too. They are keyed by gate, kind and
// agent, so a re-dispatch of the same agents would overwrite their slots --
// but an agent no longer bound to the gate would leave its old output behind,
// and the gate decision reads every output for its gate.
//
// Records a second history entry with no invalidated gate ids, marking a reset
// rather than a fresh invalidation.
func (e *Executor) Reenter(taskID, earliestGateID, reason, actor string) (state.Invalidation, error) {
	var record state.Invalidation

	checkpoint, found, err := e.Checkpointer.Load(taskID)
	if err != nil {
		return record, err
	}
	if !found {
		return record, fmt.Errorf("no checkpoint for task %s", taskID)
	}

	affected, err := e.gatesFrom(earliestGateID)
	if err != nil {
		return record, err
	}

	record = state.Invalidation{
		InvalidatedAt:            e.now(),
		Actor:                    actor,
		Reason:                   reason,
		EarliestGate:             earliestGateID,
		InvalidatedGateIDs:       []string{},
		AffectedArtifactBindings: []state.ArtifactBinding{},
	}

	current := checkpoint.State
	reset := map[string]bool{}
	updates := map[string]state.GateState{}
	for _, gateID := range affected {
		reset[gateID] = true
		gate, present := current.LifecycleGates[gateID]
		if !present {
			continue
		}
		gate.Status = "pending"
		gate.RequiredReentryGate = nil
		gate.ArtifactBindings = []state.ArtifactBinding{}
		gate.EvidenceRefs = []state.Evidence{}
		gate.HumanApprovals = []state.Approval{}
		gate.DecidedAt = nil
		updates[gateID] = gate
	}
	current.LifecycleGates = state.MergeGateUpdates(current.LifecycleGates, updates)
	current.ReEntryHistory = append(current.ReEntryHistory, record)

	remaining := map[string]map[string]any{}
	for slot, output := range current.AgentOutputs {
		if !reset[asString(output["gate_id"])] {
			remaining[slot] = output
		}
	}
	current.AgentOutputs = remaining

	if err := e.Checkpointer.Save(taskID, Checkpoint{State: current}); err != nil {
		return record, err
	}
	return record, nil
}

// gatesFrom returns the named gate and every gate after it, in run order.
func (e *Executor) gatesFrom(earliestGateID string) ([]string, error) {
	ordered := e.orderedGates()
	for index, gate := range ordered {
		if gate.ID == earliestGateID {
			ids := make([]string, 0, len(ordered)-index)
			for _, later := range ordered[index:] {
				ids = append(ids, later.ID)
			}
			return ids, nil
		}
	}
	return nil, fmt.Errorf("gate %s is not in this run's sequence", earliestGateID)
}
