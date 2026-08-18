// Package executor drives a task through its lifecycle gates.
//
// This replaces a compiled LangGraph StateGraph, and is deliberately not a
// graph engine. The topology graph.py builds is derived entirely from the gate
// sequence -- per gate: authors fan out, then reviewers, then a decision, then
// an optional human-approval stop -- with inter-gate edges taken from the
// contract's prerequisites. There are no cycles: re-entry rewinds a
// checkpoint rather than following an edge back. So the gate list *is* the
// shape, and a driver over it is the whole engine.
//
// Suspension is a return value rather than a coroutine. LangGraph's
// interrupt() unwinds the call stack and the checkpointer persists what was
// reached; here, reaching a human decision returns Suspended with what is
// being waited on, and Resume continues from the checkpoint. Nothing blocks a
// goroutine while a human thinks, which is what makes this usable from a
// service that may not be running when the answer arrives.
package executor

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/deagy/cadre/cli/internal/engine/agents"
	"github.com/deagy/cadre/cli/internal/engine/contracts"
	"github.com/deagy/cadre/cli/internal/engine/state"
)

// Suspension kinds.
const (
	SuspendMutationGate  = "mutation_gate"
	SuspendHumanApproval = "human_approval"
)

// Suspension is what the run is waiting for a human to decide.
type Suspension struct {
	Kind    string         `json:"kind"`
	GateID  string         `json:"gate_id"`
	Payload map[string]any `json:"payload"`
}

// Result is one advance of a run.
type Result struct {
	State state.SDLCState `json:"state"`
	// Suspended is nil when the run reached the end of its gates.
	Suspended *Suspension `json:"suspended"`
}

// Done reports whether the run finished rather than stopping for a human.
func (r Result) Done() bool { return r.Suspended == nil }

// Executor drives one configuration of gates, bindings and agents.
type Executor struct {
	Gates         []contracts.Gate
	MutationGates []contracts.MutationGate
	Profile       contracts.Profile
	AgentCatalog  map[string]contracts.AgentCatalogEntry
	Client        agents.ModelClient
	Checkpointer  Checkpointer

	// Now is injectable so records are reproducible in tests.
	Now func() time.Time
}

func (e *Executor) now() string {
	clock := e.Now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return clock().UTC().Format(time.RFC3339Nano)
}

// Start begins a run, or resumes one that is already checkpointed.
func (e *Executor) Start(taskID string, initial state.SDLCState) (Result, error) {
	checkpoint, found, err := e.Checkpointer.Load(taskID)
	if err != nil {
		return Result{}, err
	}
	if found {
		return e.advance(taskID, checkpoint.State)
	}
	if initial.LifecycleGates == nil {
		initial.LifecycleGates = map[string]state.GateState{}
	}
	if initial.AgentOutputs == nil {
		initial.AgentOutputs = map[string]map[string]any{}
	}
	return e.advance(taskID, initial)
}

// Resume applies a human decision and continues.
func (e *Executor) Resume(taskID string, decision map[string]any) (Result, error) {
	checkpoint, found, err := e.Checkpointer.Load(taskID)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, fmt.Errorf("no checkpoint for task %s", taskID)
	}
	if checkpoint.Pending == nil {
		return Result{}, fmt.Errorf("task %s is not waiting on a decision", taskID)
	}

	current := checkpoint.State
	switch checkpoint.Pending.Kind {
	case SuspendMutationGate:
		current = e.applyMutationDecision(current, decision)
	case SuspendHumanApproval:
		current = e.applyApproval(current, checkpoint.Pending.GateID, decision)
	default:
		return Result{}, fmt.Errorf("unknown suspension kind %q", checkpoint.Pending.Kind)
	}
	return e.advance(taskID, current)
}

// advance runs until the next human decision or the end of the gates.
func (e *Executor) advance(taskID string, current state.SDLCState) (Result, error) {
	// The mutation-gate guard runs before any gate dispatch, so an
	// unauthorised destructive task never reaches an agent at all.
	if current.MutationGateDecision == nil {
		matched := contracts.MutationGateGuard(current.Scope, e.MutationGates)
		if len(matched) > 0 {
			entries := make([]any, 0, len(matched))
			for _, match := range matched {
				entries = append(entries, map[string]any{
					"id": match.ID, "phrase": match.Phrase, "reason": match.Reason,
				})
			}
			current.MutationGatePending = map[string]any{"matched": entries}
			return e.suspend(taskID, current, &Suspension{
				Kind: SuspendMutationGate,
				Payload: map[string]any{
					"kind":    SuspendMutationGate,
					"matched": entries,
					"reason":  "Human authorization required before any gate dispatch may proceed",
				},
			})
		}
		current.MutationGatePending = nil
		current.MutationGateDecision = map[string]any{"authorized": true, "implicit": true}
		current.RunHalted = false
	}

	if current.RunHalted {
		return e.complete(taskID, current)
	}

	for _, gate := range e.orderedGates() {
		existing, decided := current.LifecycleGates[gate.ID]
		if decided && isSettled(existing.Status) {
			continue
		}

		if !decided || existing.Status == "" {
			dispatched, err := e.dispatchGate(current, gate)
			if err != nil {
				return Result{}, err
			}
			current.AgentOutputs = state.MergeAgentOutputs(current.AgentOutputs, dispatched)
			current.LifecycleGates = state.MergeGateUpdates(current.LifecycleGates,
				map[string]state.GateState{gate.ID: e.decideGate(current, gate)})
			existing = current.LifecycleGates[gate.ID]
		}

		if requiresHumanApproval(gate, existing) {
			reason := "Human approval required for gate " + gate.ID
			if existing.Status == "blocked" {
				reason = "Separation-of-duties violation: cannot approve"
			}
			return e.suspend(taskID, current, &Suspension{
				Kind: SuspendHumanApproval, GateID: gate.ID,
				Payload: map[string]any{
					"gate_id":                gate.ID,
					"authority_requirements": existing.AuthorityRequirements,
					"reason":                 reason,
				},
			})
		}
	}

	return e.complete(taskID, current)
}

// isSettled reports whether a gate has had its human decision recorded.
//
// A gate is revisited only while it has never been decided. Note that a
// rejected gate does not stop the run: graph.py's inter-gate edges are
// unconditional, so the next gate dispatches regardless and the resulting
// record is refused later by validate, which forbids approving a gate while an
// earlier applicable one is not approved. Reproduced rather than corrected --
// see the package tests, which pin it.
func isSettled(status string) bool {
	switch status {
	case "approved", "request-changes", "blocked":
		return true
	}
	return false
}

func requiresHumanApproval(gate contracts.Gate, current state.GateState) bool {
	if len(current.HumanApprovals) > 0 {
		return false
	}
	return len(gate.AuthorityRequirements) > 0 || gate.HumanOnly
}

// orderedGates returns the gates in prerequisite order.
//
// A topological order rather than the contract's array order, because
// graph.py takes its inter-gate edges from prerequisites and does not assume
// the two agree. Ties keep contract order, so the shipped chain executes
// exactly as declared.
func (e *Executor) orderedGates() []contracts.Gate {
	position := make(map[string]int, len(e.Gates))
	for index, gate := range e.Gates {
		position[gate.ID] = index
	}

	depth := make(map[string]int, len(e.Gates))
	var resolve func(gate contracts.Gate, seen map[string]bool) int
	resolve = func(gate contracts.Gate, seen map[string]bool) int {
		if known, ok := depth[gate.ID]; ok {
			return known
		}
		if seen[gate.ID] {
			return 0 // a cycle in the contract; treat as a root rather than hang
		}
		seen[gate.ID] = true
		deepest := 0
		for _, prerequisite := range gate.Prerequisites {
			index, present := position[prerequisite]
			if !present {
				continue // out of this run's sequence
			}
			if candidate := resolve(e.Gates[index], seen) + 1; candidate > deepest {
				deepest = candidate
			}
		}
		depth[gate.ID] = deepest
		return deepest
	}
	for _, gate := range e.Gates {
		resolve(gate, map[string]bool{})
	}

	ordered := append([]contracts.Gate(nil), e.Gates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if depth[ordered[i].ID] != depth[ordered[j].ID] {
			return depth[ordered[i].ID] < depth[ordered[j].ID]
		}
		return position[ordered[i].ID] < position[ordered[j].ID]
	})
	return ordered
}

// dispatchGate runs a gate's authors, then its reviewers.
//
// Authors complete before reviewers start: a reviewer reviewing an artifact
// that does not exist yet is not a review. Within each phase agents run
// concurrently, and their outputs merge by slot so two agents in the same
// gate cannot overwrite each other.
func (e *Executor) dispatchGate(current state.SDLCState, gate contracts.Gate) (map[string]map[string]any, error) {
	authors, reviewers := e.agentsFor(current, gate)
	outputs := map[string]map[string]any{}

	for _, phase := range []struct {
		kind string
		ids  []string
	}{{"author", authors}, {"reviewer", reviewers}} {
		produced, err := e.runPhase(current, gate, phase.kind, phase.ids)
		if err != nil {
			return nil, err
		}
		for slot, output := range produced {
			outputs[slot] = output
		}
	}
	return outputs, nil
}

func (e *Executor) runPhase(current state.SDLCState, gate contracts.Gate, kind string, agentIDs []string) (map[string]map[string]any, error) {
	produced := make(map[string]map[string]any, len(agentIDs))
	if len(agentIDs) == 0 {
		return produced, nil
	}

	var mutex sync.Mutex
	var waitGroup sync.WaitGroup
	var firstError error

	for _, agentID := range agentIDs {
		waitGroup.Add(1)
		go func(agentID string) {
			defer waitGroup.Done()

			metadata := map[string]any{}
			if entry, known := e.AgentCatalog[agentID]; known {
				metadata["kind"] = entry.Kind
			}
			output, err := agents.Run(
				agents.Dispatch{AgentID: agentID, Kind: kind, Metadata: metadata},
				agents.DispatchRequest{
					GateID: gate.ID, TaskText: current.Scope, Classification: current.Classification,
				},
				e.Client,
			)

			mutex.Lock()
			defer mutex.Unlock()
			if err != nil {
				if firstError == nil {
					firstError = err
				}
				return
			}
			produced[agents.Slot(gate.ID, kind, agentID)] = map[string]any{
				"agent_id": output.AgentID, "kind": output.Kind, "gate_id": output.GateID,
				"identity":          output.Identity,
				"artifact_binding":  output.ArtifactBinding,
				"evidence_ref":      output.EvidenceRef,
				"blocking_question": output.BlockingQuestion,
			}
		}(agentID)
	}
	waitGroup.Wait()
	return produced, firstError
}

// agentsFor resolves a gate's authors and reviewers.
//
// Authors come from the gate's bindings, filtered to catalog kind "author".
// Reviewers are those bound as "reviewer", plus any named by a route that
// matched the task text and lists this gate -- deduplicated, first occurrence
// winning. A catalog agent of any other kind is dispatched as neither.
func (e *Executor) agentsFor(current state.SDLCState, gate contracts.Gate) (authors, reviewers []string) {
	binding := contracts.GateDispatchBinding(gate, e.Profile.GateBindings)

	for _, agentID := range binding.Agents {
		switch e.AgentCatalog[agentID].Kind {
		case "author":
			authors = append(authors, agentID)
		case "reviewer":
			reviewers = append(reviewers, agentID)
		}
	}

	seen := map[string]bool{}
	for _, agentID := range reviewers {
		seen[agentID] = true
	}
	for _, route := range contracts.ChooseRoute(current.Scope, e.Profile.Routing) {
		if !contains(route.Gates, gate.ID) {
			continue
		}
		for _, reviewerID := range route.Reviewers {
			if !seen[reviewerID] {
				seen[reviewerID] = true
				reviewers = append(reviewers, reviewerID)
			}
		}
	}
	return authors, reviewers
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (e *Executor) suspend(taskID string, current state.SDLCState, suspension *Suspension) (Result, error) {
	if err := e.Checkpointer.Save(taskID, Checkpoint{State: current, Pending: suspension}); err != nil {
		return Result{}, err
	}
	return Result{State: current, Suspended: suspension}, nil
}

func (e *Executor) complete(taskID string, current state.SDLCState) (Result, error) {
	if err := e.Checkpointer.Save(taskID, Checkpoint{State: current}); err != nil {
		return Result{}, err
	}
	return Result{State: current}, nil
}
