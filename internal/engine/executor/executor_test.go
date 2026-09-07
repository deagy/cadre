package executor

import (
	"testing"
	"time"

	"github.com/deagy/cadre/cli/internal/engine/agents"
	"github.com/deagy/cadre/cli/internal/engine/contracts"
	"github.com/deagy/cadre/cli/internal/engine/state"
)

// A two-gate configuration with one author and one reviewer per gate.
func harness(t *testing.T) *Executor {
	t.Helper()
	gates := []contracts.Gate{
		{ID: "G1", Name: "Intent", Phase: "intent",
			RequiredContributions: []string{"intent"}, AuthorityRequirements: []string{"product_owner"}},
		{ID: "G2", Name: "Requirements", Phase: "requirements", Prerequisites: []string{"G1"},
			RequiredContributions: []string{"requirements"}, AuthorityRequirements: []string{"product_owner"}},
	}
	profile := contracts.Profile{
		GateBindings: map[string]contracts.GateBinding{
			"G1": {Contributions: map[string]contracts.Contribution{
				"intent": {Agents: []string{"intent-author", "intent-reviewer"}},
			}},
			"G2": {Contributions: map[string]contracts.Contribution{
				"requirements": {Agents: []string{"intent-author", "intent-reviewer"}},
			}},
		},
	}
	return &Executor{
		Gates:   gates,
		Profile: profile,
		AgentCatalog: map[string]contracts.AgentCatalogEntry{
			"intent-author":   {Kind: "author"},
			"intent-reviewer": {Kind: "reviewer"},
		},
		Client:       agents.FakeModelClient{},
		Checkpointer: NewMemoryCheckpointer(),
		Now:          func() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) },
	}
}

func approvalDecision(approverID string) map[string]any {
	return map[string]any{
		"status":   "approved",
		"approver": map[string]any{"id": approverID, "role": "Product Owner", "kind": "human"},
		"evidence_refs": []any{map[string]any{
			"evidence_id": "e1", "uri": "https://example/1", "hash_algorithm": "sha256",
			"hash": "abc", "classification": "internal",
		}},
	}
}

// A run stops at the first gate needing a human, and says which.
//
// Suspension is a return value, not a blocked goroutine: nothing is waiting
// while a human thinks, which is what makes this usable from a service that
// may not be running when the answer arrives.
func TestARunSuspendsAtTheFirstApproval(t *testing.T) {
	executor := harness(t)

	result, err := executor.Start("task-1", state.SDLCState{TaskID: "task-1", Scope: "add a feature"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Done() {
		t.Fatal("the run completed without asking for an approval")
	}
	if result.Suspended.Kind != SuspendHumanApproval || result.Suspended.GateID != "G1" {
		t.Fatalf("suspended on %+v, want a human approval for G1", result.Suspended)
	}

	gate := result.State.LifecycleGates["G1"]
	if gate.Status != "ready" {
		t.Errorf("G1 status = %q, want ready before a decision", gate.Status)
	}
	if len(gate.Preparers) != 1 || gate.IndependentVerifier == nil {
		t.Errorf("G1 recorded %d preparers and verifier %v", len(gate.Preparers), gate.IndependentVerifier)
	}
}

// Approving G1 advances to G2 and stops again.
func TestApprovingAGateAdvancesToTheNext(t *testing.T) {
	executor := harness(t)
	if _, err := executor.Start("task-1", state.SDLCState{TaskID: "task-1", Scope: "add a feature"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	result, err := executor.Resume("task-1", approvalDecision("product_owner"))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Done() {
		t.Fatal("the run completed without reaching G2's approval")
	}
	if result.Suspended.GateID != "G2" {
		t.Errorf("suspended on %s, want G2", result.Suspended.GateID)
	}
	if got := result.State.LifecycleGates["G1"].Status; got != "approved" {
		t.Errorf("G1 status = %q, want approved", got)
	}

	// Approving G2 finishes the run.
	final, err := executor.Resume("task-1", approvalDecision("product_owner"))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !final.Done() {
		t.Fatalf("the run did not finish; still waiting on %+v", final.Suspended)
	}
	if got := final.State.LifecycleGates["G2"].Status; got != "approved" {
		t.Errorf("G2 status = %q, want approved", got)
	}
}

// A run resumes from its checkpoint, not from memory the caller happens to hold.
func TestARunResumesFromTheCheckpoint(t *testing.T) {
	shared := NewMemoryCheckpointer()

	first := harness(t)
	first.Checkpointer = shared
	if _, err := first.Start("task-1", state.SDLCState{TaskID: "task-1", Scope: "add a feature"}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// A different executor instance, as a service process restarting would be.
	second := harness(t)
	second.Checkpointer = shared

	result, err := second.Resume("task-1", approvalDecision("product_owner"))
	if err != nil {
		t.Fatalf("Resume on a fresh executor: %v", err)
	}
	if result.State.LifecycleGates["G1"].Status != "approved" {
		t.Error("the decision was not applied to the checkpointed run")
	}
}

// An unauthorised mutation gate halts before any agent is dispatched.
func TestAnUnauthorisedMutationGateDispatchesNothing(t *testing.T) {
	executor := harness(t)
	executor.MutationGates = []contracts.MutationGate{
		{ID: "production-deployment", Phrases: []string{"deploy to production"}},
	}

	result, err := executor.Start("task-1", state.SDLCState{TaskID: "task-1", Scope: "please deploy to production"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Done() || result.Suspended.Kind != SuspendMutationGate {
		t.Fatalf("suspended on %+v, want a mutation-gate authorisation", result.Suspended)
	}
	if len(result.State.AgentOutputs) != 0 {
		t.Errorf("%d agents ran before authorisation; none may", len(result.State.AgentOutputs))
	}

	halted, err := executor.Resume("task-1", map[string]any{"authorized": false, "reason": "no"})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !halted.Done() {
		t.Fatal("a refused mutation gate did not end the run")
	}
	if !halted.State.RunHalted {
		t.Error("the run is not marked halted")
	}
	if len(halted.State.AgentOutputs) != 0 {
		t.Errorf("%d agents ran after refusal", len(halted.State.AgentOutputs))
	}
	if len(halted.State.LifecycleGates) != 0 {
		t.Errorf("%d gates were decided after refusal", len(halted.State.LifecycleGates))
	}
}

// Anything other than an explicit true is not authorisation.
func TestOnlyAnExplicitTrueAuthorisesAMutationGate(t *testing.T) {
	for name, decision := range map[string]map[string]any{
		"absent":      {},
		"string true": {"authorized": "true"},
		"number one":  {"authorized": 1},
		"nil":         {"authorized": nil},
	} {
		executor := harness(t)
		executor.MutationGates = []contracts.MutationGate{
			{ID: "prod", Phrases: []string{"deploy to production"}},
		}
		if _, err := executor.Start(name, state.SDLCState{Scope: "deploy to production now"}); err != nil {
			t.Fatalf("%s: Start: %v", name, err)
		}
		result, err := executor.Resume(name, decision)
		if err != nil {
			t.Fatalf("%s: Resume: %v", name, err)
		}
		if !result.State.RunHalted {
			t.Errorf("%s: %v was treated as authorisation", name, decision)
		}
	}
}

// An agent that both prepares and reviews blocks its gate.
//
// This is the failure the whole model exists to prevent, so it is enforced at
// the gate decision -- before any human is asked -- and the gate cannot then
// be approved by any decision at all.
//
// This is the simplest, most fundamental case: a single reviewer who is also
// a preparer (or the first reviewer in a multi-reviewer list being the preparer).
// After defect 2 is fixed, the binding system filters by kind, so this scenario
// cannot occur naturally. We test it by directly injecting agent outputs via
// the same technique as TestNonFirstReviewerThatIsAlsoPreparerBlocksTheGate.
func TestAnAgentThatReviewsItsOwnWorkBlocksTheGate(t *testing.T) {
	executor := harness(t)

	// Manually create a scenario where the single (or first) reviewer is also a preparer.
	initial := state.SDLCState{TaskID: "task-1", Scope: "add a feature"}
	initial.LifecycleGates = map[string]state.GateState{}
	initial.AgentOutputs = map[string]map[string]any{
		// Single reviewer who is also a preparer (the violation)
		"G1/reviewer/solo-agent": {
			"gate_id": "G1", "kind": "reviewer",
			"identity": map[string]any{"id": "solo-agent", "role": "Reviewer"},
		},
		// Author - same as the reviewer above
		"G1/author/solo-agent": {
			"gate_id": "G1", "kind": "author",
			"identity": map[string]any{"id": "solo-agent", "role": "Author"},
		},
	}

	// Manually call decideGate to test its logic
	gate := contracts.Gate{ID: "G1", Name: "Intent", AuthorityRequirements: []string{"product_owner"}}

	gateState := executor.decideGate(initial, gate)

	// The gate should be blocked because solo-agent is both author and reviewer
	if gateState.Status != "blocked" {
		t.Fatalf("G1 status = %q, want blocked when the (only) reviewer is also a preparer", gateState.Status)
	}
	if gateState.IndependenceDeclaration.VerifierConfirmedNotPreparer {
		t.Error("the independence declaration claims separation that did not hold")
	}
	if len(gateState.Preparers) != 1 || gateState.Preparers[0].ID != "solo-agent" {
		t.Errorf("preparers = %v, want [solo-agent]", gateState.Preparers)
	}

	// No decision can approve a blocked gate.
	initial.LifecycleGates = map[string]state.GateState{
		"G1": gateState,
	}
	decided := executor.applyApproval(initial, "G1", approvalDecision("product_owner"))
	blocked := decided.LifecycleGates["G1"]
	if blocked.Status == "approved" {
		t.Error("a blocked gate was approved despite the separation-of-duties violation")
	}
	if len(blocked.HumanApprovals) != 1 || blocked.HumanApprovals[0].Status != "rejected" {
		t.Errorf("approval recorded as %+v, want rejected", blocked.HumanApprovals)
	}
}

// An agent that is one of multiple reviewers and also a preparer blocks the gate.
//
// This is the gap in the fix: if a gate has multiple reviewers and a
// non-first reviewer is also a preparer, only checking the first reviewer
// would miss the violation. This test proves the violation is caught
// regardless of reviewer position.
//
// This test directly constructs the scenario by manually setting agent outputs,
// since the binding/routing system filters by kind and prevents this scenario
// from occurring naturally after defect 2 is fixed.
func TestNonFirstReviewerThatIsAlsoPreparerBlocksTheGate(t *testing.T) {
	executor := harness(t)

	// Manually create a scenario where agent-2 appears as both author and reviewer.
	// Start the run to initialize it, then manually inject outputs.
	initial := state.SDLCState{TaskID: "task-1", Scope: "add a feature"}
	initial.LifecycleGates = map[string]state.GateState{}
	initial.AgentOutputs = map[string]map[string]any{
		// First reviewer - not a preparer
		"G1/reviewer/reviewer-1": {
			"gate_id": "G1", "kind": "reviewer",
			"identity": map[string]any{"id": "reviewer-1", "role": "Reviewer 1"},
		},
		// Second reviewer - also a preparer (the violation)
		"G1/reviewer/agent-2": {
			"gate_id": "G1", "kind": "reviewer",
			"identity": map[string]any{"id": "agent-2", "role": "Reviewer 2"},
		},
		// Author - also appears as second reviewer above
		"G1/author/agent-2": {
			"gate_id": "G1", "kind": "author",
			"identity": map[string]any{"id": "agent-2", "role": "Author"},
		},
	}

	// Manually call decideGate to test its logic
	gate := contracts.Gate{ID: "G1", Name: "Intent", AuthorityRequirements: []string{"product_owner"}}

	gateState := executor.decideGate(initial, gate)

	// The gate should be blocked because agent-2 is both author and reviewer
	if gateState.Status != "blocked" {
		t.Fatalf("G1 status = %q, want blocked when a non-first reviewer is also a preparer", gateState.Status)
	}
	if gateState.IndependenceDeclaration.VerifierConfirmedNotPreparer {
		t.Error("the independence declaration claims separation that did not hold")
	}
	if len(gateState.Preparers) != 1 || gateState.Preparers[0].ID != "agent-2" {
		t.Errorf("preparers = %v, want [agent-2]", gateState.Preparers)
	}
}

// Approval fails closed on every route by which an unapproved change could be
// recorded as approved.
func TestApprovalFailsClosed(t *testing.T) {
	cases := map[string]struct {
		decision map[string]any
		reason   string
	}{
		"no evidence": {
			map[string]any{"status": "approved",
				"approver": map[string]any{"id": "product_owner", "kind": "human"}},
			"an approval with no evidence is not checkable",
		},
		"incomplete evidence": {
			map[string]any{"status": "approved",
				"approver":      map[string]any{"id": "product_owner", "kind": "human"},
				"evidence_refs": []any{map[string]any{"evidence_id": "e1", "uri": "https://x"}}},
			"a partially filled reference looks like evidence while pointing at nothing",
		},
		"approver is a preparer": {
			func() map[string]any {
				d := approvalDecision("intent-author") // the gate's author
				return d
			}(),
			"whoever prepared the artifact may not approve it",
		},
		"approver is the verifier": {
			func() map[string]any {
				d := approvalDecision("intent-reviewer") // the gate's reviewer
				return d
			}(),
			"the independent verifier may not also be the approver",
		},
		"unrecognised status": {
			func() map[string]any {
				d := approvalDecision("product_owner")
				d["status"] = "looks-fine-to-me"
				return d
			}(),
			"an unknown status must not pass through as approval",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			executor := harness(t)
			if _, err := executor.Start(name, state.SDLCState{Scope: "add a feature"}); err != nil {
				t.Fatalf("Start: %v", err)
			}
			result, err := executor.Resume(name, testCase.decision)
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			gate := result.State.LifecycleGates["G1"]
			if gate.Status == "approved" {
				t.Errorf("the gate was approved: %s", testCase.reason)
			}
			if len(gate.HumanApprovals) == 0 || gate.HumanApprovals[0].Status == "approved" {
				t.Errorf("the approval was recorded as approved: %s", testCase.reason)
			}
			// The recorded status must be one the schema allows. An
			// unrecognised value passing through is not "approved", so a
			// check for approval alone misses it -- and it writes a value
			// the run record cannot carry.
			if len(gate.HumanApprovals) > 0 {
				switch got := gate.HumanApprovals[0].Status; got {
				case "approved", "rejected", "pending", "not-required":
				default:
					t.Errorf("approval status %q is not one the schema allows", got)
				}
			}
		})
	}
}

// A route-supplied reviewer whose catalog kind is "author" is not treated as a reviewer.
//
// This is the defect in route handling: direct-binding paths filter agents by
// kind, but route-supplied reviewers were not. This test proves that a
// route-supplied agent with kind "author" is silently dropped from the
// reviewer list, matching the direct-binding behavior.
func TestRouteSuppliedReviewerWithWrongKindIsSkipped(t *testing.T) {
	executor := harness(t)
	executor.AgentCatalog = map[string]contracts.AgentCatalogEntry{
		"intent-author":   {Kind: "author"},
		"intent-reviewer": {Kind: "reviewer"},
		"wrong-kind":      {Kind: "author"}, // kind is author, not reviewer
	}
	// A route tries to supply wrong-kind as a reviewer.
	executor.Profile.Routing = []contracts.Route{
		{ID: "r", Phrases: []string{"feature"}, Gates: []string{"G1"}, Reviewers: []string{"wrong-kind"}},
	}

	result, err := executor.Start("task-1", state.SDLCState{Scope: "add a feature"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	gate := result.State.LifecycleGates["G1"]
	// The gate should have exactly one reviewer: intent-reviewer from the binding.
	// wrong-kind from the route should have been silently skipped.
	foundWrongKind := false
	for _, reviewer := range gate.Preparers {
		if reviewer.ID == "wrong-kind" {
			foundWrongKind = true
		}
	}
	if foundWrongKind {
		t.Error("wrong-kind was treated as a preparer when it should have been skipped")
	}

	// Check the agent outputs to see what was actually run.
	slot := "G1/reviewer/wrong-kind"
	if _, exists := result.State.AgentOutputs[slot]; exists {
		t.Errorf("wrong-kind was dispatched as a reviewer when it should have been skipped")
	}
}

// Two agents in the same gate must not overwrite each other.
func TestConcurrentAgentsInAGateDoNotClobber(t *testing.T) {
	executor := harness(t)
	executor.AgentCatalog = map[string]contracts.AgentCatalogEntry{
		"author-a": {Kind: "author"}, "author-b": {Kind: "author"},
		"reviewer-a": {Kind: "reviewer"},
	}
	executor.Profile.GateBindings["G1"] = contracts.GateBinding{
		Contributions: map[string]contracts.Contribution{
			"intent": {Agents: []string{"author-a", "author-b", "reviewer-a"}},
		},
	}

	result, err := executor.Start("task-1", state.SDLCState{Scope: "add a feature"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if len(result.State.AgentOutputs) != 3 {
		t.Errorf("%d outputs recorded, want 3 -- one slot per agent per kind per gate",
			len(result.State.AgentOutputs))
	}
	gate := result.State.LifecycleGates["G1"]
	if len(gate.Preparers) != 2 {
		t.Errorf("%d preparers recorded, want both authors", len(gate.Preparers))
	}
	// Order reaches the run record, so it must not depend on goroutine timing.
	if gate.Preparers[0].ID != "author-a" || gate.Preparers[1].ID != "author-b" {
		t.Errorf("preparers = %v, want a stable order", gate.Preparers)
	}
}

// Gates run in prerequisite order even when the list is given out of order.
func TestGatesRunInPrerequisiteOrder(t *testing.T) {
	executor := harness(t)
	executor.Gates = []contracts.Gate{executor.Gates[1], executor.Gates[0]} // G2 before G1

	result, err := executor.Start("task-1", state.SDLCState{Scope: "add a feature"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.Suspended == nil || result.Suspended.GateID != "G1" {
		t.Errorf("stopped at %+v, want G1 first despite the list order", result.Suspended)
	}
}

// An authority nobody is assigned to is unknown, not absent.
//
// That distinction is what lets validate report an approved gate with an
// unresolved authority as a blocker rather than a defect.
func TestUnassignedAuthoritiesAreRecordedAsUnknown(t *testing.T) {
	executor := harness(t)

	result, err := executor.Start("task-1", state.SDLCState{Scope: "add a feature"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	requirements := result.State.LifecycleGates["G1"].AuthorityRequirements
	if len(requirements) != 1 {
		t.Fatalf("%d authority requirements recorded, want 1", len(requirements))
	}
	if requirements[0].Applicability != "unknown" {
		t.Errorf("applicability = %q, want unknown when nobody is assigned", requirements[0].Applicability)
	}

	assigned := harness(t)
	result, err = assigned.Start("task-2", state.SDLCState{
		Scope:       "add a feature",
		Authorities: map[string]map[string]any{"product_owner": {"status": "assigned"}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := result.State.LifecycleGates["G1"].AuthorityRequirements[0].Applicability; got != "applicable" {
		t.Errorf("applicability = %q, want applicable when assigned", got)
	}
}

// Re-entry actually re-dispatches, rather than flipping a status field.
//
// This is where the port earns its keep: the legacy CLI had no execution
// engine, so its `reenter` only rewrote JSON. Here a re-entered gate must run
// its agents again and stop at its own approval, or "re-entry" means nothing.
func TestReenterRedispatchesTheGate(t *testing.T) {
	executor := harness(t)

	if _, err := executor.Start("task-1", state.SDLCState{Scope: "add a feature"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := executor.Resume("task-1", approvalDecision("product_owner")); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	loaded, _, _ := executor.Checkpointer.Load("task-1")
	if loaded.State.LifecycleGates["G1"].Status != "approved" {
		t.Fatal("G1 was not approved before re-entry")
	}

	record, err := executor.Reenter("task-1", "G1", "requirements changed", "alice")
	if err != nil {
		t.Fatalf("Reenter: %v", err)
	}
	if record.EarliestGate != "G1" || len(record.InvalidatedGateIDs) != 0 {
		t.Errorf("record = %+v, want a reset entry naming G1 with no invalidated ids", record)
	}

	after, _, _ := executor.Checkpointer.Load("task-1")
	g1 := after.State.LifecycleGates["G1"]
	if g1.Status != "pending" {
		t.Errorf("G1 status = %q after re-entry, want pending", g1.Status)
	}
	if len(g1.HumanApprovals) != 0 || len(g1.EvidenceRefs) != 0 || g1.DecidedAt != nil {
		t.Error("re-entry left the previous decision's evidence in place")
	}
	for slot, output := range after.State.AgentOutputs {
		if output["gate_id"] == "G1" {
			t.Errorf("a stale agent output survived re-entry: %s", slot)
		}
	}

	// The next advance must run G1's agents again and stop at its approval.
	result, err := executor.Start("task-1", state.SDLCState{})
	if err != nil {
		t.Fatalf("Start after re-entry: %v", err)
	}
	if result.Done() || result.Suspended.GateID != "G1" {
		t.Fatalf("after re-entry the run reached %+v, want G1's approval again", result.Suspended)
	}
	// Assert on what Reenter actually cleared. Preparers survive re-entry,
	// so checking them proves nothing about whether agents ran again -- the
	// old ones would still be there. The agent outputs were cleared, so
	// their reappearance is the evidence that a dispatch happened.
	redispatched := 0
	for _, output := range result.State.AgentOutputs {
		if output["gate_id"] == "G1" {
			redispatched++
		}
	}
	if redispatched == 0 {
		t.Error("G1 suspended for approval without re-dispatching its agents")
	}
}

// Invalidate records the claim without clearing the audit trail.
func TestInvalidateKeepsTheStaleEvidenceVisible(t *testing.T) {
	executor := harness(t)
	if _, err := executor.Start("task-1", state.SDLCState{Scope: "add a feature"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := executor.Resume("task-1", approvalDecision("product_owner")); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	record, err := executor.Invalidate("task-1", "G1", "upstream change", "alice")
	if err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if len(record.InvalidatedGateIDs) != 2 {
		t.Errorf("invalidated %v, want G1 and everything after it", record.InvalidatedGateIDs)
	}

	after, _, _ := executor.Checkpointer.Load("task-1")
	g1 := after.State.LifecycleGates["G1"]
	if g1.Status != "invalidated" {
		t.Errorf("G1 status = %q, want invalidated", g1.Status)
	}
	if g1.RequiredReentryGate == nil || *g1.RequiredReentryGate != "G1" {
		t.Errorf("required_reentry_gate = %v, want G1", g1.RequiredReentryGate)
	}
	// The point of the split: invalidation states a claim, it does not erase
	// what was believed when the gate was approved.
	if len(g1.HumanApprovals) == 0 || len(g1.EvidenceRefs) == 0 {
		t.Error("invalidation cleared the audit trail; only re-entry may")
	}
	if len(g1.InvalidationHistory) != 1 || len(after.State.ReEntryHistory) != 1 {
		t.Error("the invalidation was not recorded on both the gate and the run")
	}
}

// A gate outside the run's sequence cannot be re-entered.
func TestReenteringAnUnknownGateIsRefused(t *testing.T) {
	executor := harness(t)
	if _, err := executor.Start("task-1", state.SDLCState{Scope: "add a feature"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := executor.Reenter("task-1", "G7", "x", "alice"); err == nil {
		t.Error("re-entering a gate outside the sequence was accepted")
	}
}
