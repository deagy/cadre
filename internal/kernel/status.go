package kernel

// `agentic-sdlc status` -- and a warning in the name.
//
// It reads as an inspection command and it is not. It advances the next
// eligible gate to `ready` and writes the record back, so a caller running it
// to look at a task changes that task. That is the Python kernel's behaviour
// and it is ported as-is: a Go `status` that only read would quietly stop
// advancing gates for every project that relies on it, which is a worse
// surprise than the one already there.
//
// What it will not do is infer approval. `advanceLifecycle` moves a gate to
// `ready` -- "the prerequisites are met and somebody may now look at it" --
// and nothing further. Reading a task can make it visible; it can never make
// it approved.
//
// GateStatusProjection is the read-only half, kept separate because there is a
// real caller for it: the gate-status publishers render a task's state onto a
// pull request, and they must be able to do that without the render itself
// moving the task on.

// GateStatusProjection is a task's gate state, computed without writing.
//
// The advance is applied to an in-memory copy only. Callers must not persist
// the record this walked over -- `Status` does its own load-advance-write for
// that reason rather than reusing this one's leftovers.
func (r *Registry) GateStatusProjection(root, taskID string) (*orderedObject, error) {
	record, _, err := r.loadOrderedRecord(root, taskID)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveExisting(root)
	if err != nil {
		return nil, err
	}
	overlay, err := LoadOverlay(resolved)
	if err != nil {
		return nil, err
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		return nil, err
	}
	advanceLifecycle(record, overlay.Routing, contracts)

	gates := []any{}
	for _, raw := range listOf(record.values["lifecycle_gates"]) {
		gate, ok := raw.(*orderedObject)
		if !ok {
			continue
		}
		gates = append(gates, ordered(
			"gate_id", gate.values["gate_id"],
			"status", gate.values["status"],
			"applicability", gate.values["applicability"],
			"required_reentry_gate", gate.values["required_reentry_gate"],
		))
	}
	return ordered(
		"task_id", taskID,
		"current_phase", deriveCurrentPhase(record, contracts),
		"gates", gates,
		"re_entry_history", record.values["re_entry_history"],
		"classification", record.values["classification"],
	), nil
}

// Status reports a task's gate state and persists the advance it computed.
func (r *Registry) Status(root, taskID string) (*orderedObject, error) {
	projection, err := r.GateStatusProjection(root, taskID)
	if err != nil {
		return nil, err
	}

	// Loaded again rather than reusing the projection's copy: the projection
	// returns four fields per gate, and persisting that would replace a full
	// run record with a summary of itself. advanceLifecycle is a pure function
	// of the record and the routing, so the second computation agrees with the
	// first.
	record, recordPath, err := r.loadOrderedRecord(root, taskID)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveExisting(root)
	if err != nil {
		return nil, err
	}
	overlay, err := LoadOverlay(resolved)
	if err != nil {
		return nil, err
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		return nil, err
	}
	advanceLifecycle(record, overlay.Routing, contracts)
	if err := writeJSONDocument(recordPath, record); err != nil {
		return nil, err
	}

	return ordered(
		"task_id", taskID,
		"current_phase", projection.values["current_phase"],
		"gates", projection.values["gates"],
		"re_entry_history", projection.values["re_entry_history"],
	), nil
}
