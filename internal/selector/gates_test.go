package selector

import (
	"encoding/json"
	"strings"
	"testing"
)

func contractGates(t *testing.T, raw string) []LifecycleGate {
	t.Helper()
	var gates []LifecycleGate
	if err := json.Unmarshal([]byte(raw), &gates); err != nil {
		t.Fatalf("decoding gates: %v", err)
	}
	return gates
}

func TestGateSequenceImpliesEveryEarlierGate(t *testing.T) {
	// A configured gate implies the whole run up to it: G3 means G1, G2, G3.
	gates := contractGates(t, `[{"id":"G1"},{"id":"G2"},{"id":"G3"},{"id":"G4"}]`)

	effective, ignored, err := GateSequence([]string{"G3"}, nil, gates)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(effective, ",") != "G1,G2,G3" {
		t.Errorf("effective = %v, want G1,G2,G3", effective)
	}
	if len(ignored) != 0 {
		t.Errorf("ignored = %v, want none", ignored)
	}

	// The furthest configured gate decides the run, regardless of input order.
	effective, _, err = GateSequence([]string{"G3", "G1"}, nil, gates)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(effective, ",") != "G1,G2,G3" {
		t.Errorf("effective = %v, want the run up to the furthest configured gate", effective)
	}
}

func TestGateSequenceWithoutAContractImpliesNothing(t *testing.T) {
	// No contract means no declared order, so there is nothing to imply and
	// the configured set is used as-is -- in its own order, not sorted.
	effective, ignored, err := GateSequence([]string{"G3", "G1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(effective, ",") != "G3,G1" {
		t.Errorf("effective = %v, want the configured set unchanged", effective)
	}
	if len(ignored) != 0 {
		t.Errorf("ignored = %v, want none", ignored)
	}
}

func TestGateSequenceReportsIgnoredGatesRatherThanDroppingThem(t *testing.T) {
	gates := contractGates(t, `[{"id":"G1"},{"id":"G2"},{"id":"G3"}]`)
	effective, ignored, err := GateSequence([]string{"G3"}, []string{"G2"}, gates)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(effective, ",") != "G1,G3" {
		t.Errorf("effective = %v, want G1,G3", effective)
	}
	if strings.Join(ignored, ",") != "G2" {
		t.Errorf("ignored = %v, want G2 reported, not silently dropped", ignored)
	}
}

func TestGateSequenceRejectsUnknownGates(t *testing.T) {
	gates := contractGates(t, `[{"id":"G1"}]`)
	if _, _, err := GateSequence([]string{"G9"}, nil, gates); err == nil {
		t.Error("a configured gate absent from the contract must be refused")
	}
	if _, _, err := GateSequence([]string{"G1"}, []string{"G9"}, gates); err == nil {
		t.Error("an ignored gate absent from the contract must be refused")
	}
}

func TestGateAgentsDistinguishesDeclaredFromAbsentReviewAgents(t *testing.T) {
	// This is the distinction default_gate_review_agents depends on. No gate
	// in any shipped contract declares review_agents, so treating "absent" and
	// "declared empty" alike would turn the roster-supplied default into an
	// unconditional hardcode -- or, the other way, silently inject reviewers
	// into a gate that deliberately declared none.
	absent := contractGates(t, `[{"id":"G1"}]`)
	got, err := GateAgents([]string{"G1"}, nil, absent, []string{"code-reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "code-reviewer" {
		t.Errorf("agents = %v, want the roster default when review_agents is absent", got)
	}

	declaredEmpty := contractGates(t, `[{"id":"G1","review_agents":[]}]`)
	got, err = GateAgents([]string{"G1"}, nil, declaredEmpty, []string{"code-reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("agents = %v, want none when the gate declared an empty review_agents", got)
	}

	declared := contractGates(t, `[{"id":"G1","author_agents":["a"],"review_agents":["r"]}]`)
	got, err = GateAgents([]string{"G1"}, nil, declared, []string{"code-reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "a,r" {
		t.Errorf("agents = %v, want the gate's own declarations", got)
	}
}

func TestGateAgentsWithoutAContractContributeNothing(t *testing.T) {
	got, err := GateAgents([]string{"G1"}, nil, nil, []string{"code-reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("agents = %v, want none without a contract", got)
	}
}

func TestBuildQualityGatesReasonDependsOnContractAvailability(t *testing.T) {
	routes := []Match{{ID: "backend", Rule: map[string]any{"quality_gates": []any{"G2"}}}}

	withContract, err := BuildQualityGates(routes, nil,
		contractGates(t, `[{"id":"G1"},{"id":"G2","name":"Architecture","phase":"architecture"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(withContract) != 1 || withContract[0].Reason != "Architecture lifecycle gate (architecture phase)." {
		t.Errorf("reason = %q, want the contract's own name and phase", withContract[0].Reason)
	}

	standalone, err := BuildQualityGates(routes, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(standalone) != 1 || standalone[0].Reason != gateDetailOmitted {
		t.Errorf("reason = %q, want the gate-detail-omitted text", standalone[0].Reason)
	}
	// The plan says the detail is missing rather than inventing one.
	if !strings.Contains(standalone[0].Reason, "Agentic SDLC unavailable") {
		t.Error("the standalone reason must say why the detail is absent")
	}
}

func TestBuildQualityGatesRejectsAnUnknownGate(t *testing.T) {
	routes := []Match{{ID: "backend", Rule: map[string]any{"quality_gates": []any{"G9"}}}}
	if _, err := BuildQualityGates(routes, nil, contractGates(t, `[{"id":"G1"}]`)); err == nil {
		t.Error("routing referencing a gate the contract does not declare must be refused")
	}
}

func TestBuildHumanGatesMapsKernelMutationGates(t *testing.T) {
	risks := []Match{
		{ID: "production", Rule: map[string]any{"human_gate": "production-change"}},
		{ID: "escalation", Rule: map[string]any{"human_gate": "accountable-human-escalation"}},
		{ID: "duplicate", Rule: map[string]any{"human_gate": "production-change"}},
	}
	built := BuildHumanGates(risks)

	if len(built) != 2 {
		t.Fatalf("built %d gates, want 2 (duplicates collapse)", len(built))
	}
	if built[0].KernelMutationGate == nil || *built[0].KernelMutationGate != "production-deployment" {
		t.Errorf("production-change should map to production-deployment, got %v", built[0].KernelMutationGate)
	}
	// A mapped-to-nothing gate is different from an unmapped one: this entry
	// exists in the table with no kernel counterpart, deliberately.
	if built[1].KernelMutationGate != nil {
		t.Errorf("accountable-human-escalation has no kernel gate, got %v", *built[1].KernelMutationGate)
	}
	if !strings.Contains(built[0].Reason, "authorized human") {
		t.Errorf("reason = %q, want the described human requirement", built[0].Reason)
	}
}
