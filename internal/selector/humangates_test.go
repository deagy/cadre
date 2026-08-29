package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Human gates: where a plan says a person has to decide.
//
// A risk rule carrying `human_gate` means the work it matched may not proceed
// on an agent's judgement alone -- production changes, destructive actions,
// privileged identity changes, risk acceptance. This is the authorship/approval
// separation the repository treats as a hard invariant, expressed in the one
// artifact a dispatcher reads.
//
// The failure mode is quiet in the dangerous direction. A gate that fails to
// appear does not error; the plan is well-formed, the risk is still listed in
// matched_risks, and nothing says a human was meant to be asked.
//
// BuildHumanGates had no test of its own. What existed tested the *rendering*
// of gates in the text plan, which presumes they were built correctly.

func riskWithGate(id, gate string) Match {
	rule := map[string]any{"id": id}
	if gate != "" {
		rule["human_gate"] = gate
	}
	return Match{ID: id, Rule: rule}
}

func TestEveryHumanGateIsRequiredAndCarriesAReason(t *testing.T) {
	// Required is not a field the selector reasons about -- a human gate that
	// is not required is not a gate. It is written explicitly because the plan
	// schema allows the field, and a consumer filtering on it (as the text
	// renderer does) would silently drop a gate defaulted to false.
	gates := BuildHumanGates([]Match{
		riskWithGate("production", "production-change"),
		riskWithGate("destructive", "destructive-action"),
	})
	if len(gates) != 2 {
		t.Fatalf("built %d gates from two gated risks: %+v", len(gates), gates)
	}
	for _, gate := range gates {
		if !gate.Required {
			t.Errorf("%s is not marked required", gate.ID)
		}
		if strings.TrimSpace(gate.Reason) == "" {
			t.Errorf("%s carries no reason; the block tells a reader what to do", gate.ID)
		}
	}
}

func TestARiskWithNoHumanGateContributesNone(t *testing.T) {
	// Most risks raise reviewers rather than a human decision. Turning every
	// matched risk into a gate would make the block meaningless, and a block
	// that always fires is one people learn to skip.
	if gates := BuildHumanGates([]Match{
		riskWithGate("architecture-change", ""),
		riskWithGate("cross-stack", ""),
	}); len(gates) != 0 {
		t.Errorf("ungated risks produced gates: %+v", gates)
	}
	if gates := BuildHumanGates(nil); len(gates) != 0 {
		t.Errorf("no risks produced gates: %+v", gates)
	}
	// An empty string is not a gate id, and must not become one.
	empty := Match{ID: "r", Rule: map[string]any{"id": "r", "human_gate": ""}}
	if gates := BuildHumanGates([]Match{empty}); len(gates) != 0 {
		t.Errorf("an empty human_gate produced a gate: %+v", gates)
	}
}

func TestTwoRisksNamingOneGateProduceOne(t *testing.T) {
	// Conjunctive risks are ordinary: a production database migration trips
	// both the production and the migration rule. A duplicated gate reads as
	// two separate approvals and would have someone chasing a second sign-off
	// that does not exist.
	gates := BuildHumanGates([]Match{
		riskWithGate("production", "production-change"),
		riskWithGate("production-adjacent", "production-change"),
		riskWithGate("destructive", "destructive-action"),
	})
	if len(gates) != 2 {
		t.Fatalf("built %d gates, want the two distinct ones: %+v", len(gates), gates)
	}
	seen := map[string]int{}
	for _, gate := range gates {
		seen[gate.ID]++
	}
	if seen["production-change"] != 1 {
		t.Errorf("production-change appears %d times", seen["production-change"])
	}
	// First-seen order, so the block lines up with matched_risks rather than
	// being sorted into an order nothing else uses.
	if gates[0].ID != "production-change" {
		t.Errorf("gate order = %s first; want the order the risks matched in", gates[0].ID)
	}
}

func TestAnUnrecognisedGateIDStillProducesAGate(t *testing.T) {
	// The direction this must fail in. An id with no description -- a new risk
	// rule, a consuming project's overlay -- must still raise a gate carrying
	// a generic reason. Dropping it would turn "a human must approve this"
	// into silence, which is the one outcome worse than an unhelpful message.
	gates := BuildHumanGates([]Match{riskWithGate("novel", "some-new-obligation")})
	if len(gates) != 1 {
		t.Fatalf("an unrecognised gate id produced %d gates", len(gates))
	}
	if gates[0].ID != "some-new-obligation" {
		t.Errorf("gate id = %q", gates[0].ID)
	}
	if !gates[0].Required {
		t.Error("an unrecognised gate is not required")
	}
	if !strings.Contains(gates[0].Reason, "human") {
		t.Errorf("the fallback reason does not say a human is needed: %q", gates[0].Reason)
	}
	if gates[0].KernelMutationGate != nil {
		t.Errorf("an unmapped gate claims a kernel counterpart: %v", *gates[0].KernelMutationGate)
	}
}

func TestAMappedGateCarriesItsKernelCounterpartAndAnUnmappedOneDoesNot(t *testing.T) {
	// The distinction the map's own comment calls out: a present-but-nil entry
	// says this gate deliberately has no kernel counterpart, which is not the
	// same as a gate nobody has mapped yet. Both come back as a null in the
	// plan, so only the map itself records which was meant.
	gates := BuildHumanGates([]Match{
		riskWithGate("production", "production-change"),
		riskWithGate("escalation", "accountable-human-escalation"),
	})
	byID := map[string]HumanGate{}
	for _, gate := range gates {
		byID[gate.ID] = gate
	}
	mapped, ok := byID["production-change"]
	if !ok {
		t.Fatal("production-change was not built")
	}
	if mapped.KernelMutationGate == nil || *mapped.KernelMutationGate != "production-deployment" {
		t.Errorf("production-change kernel gate = %v, want production-deployment",
			mapped.KernelMutationGate)
	}
	if deliberate, ok := byID["accountable-human-escalation"]; !ok {
		t.Fatal("accountable-human-escalation was not built")
	} else if deliberate.KernelMutationGate != nil {
		t.Errorf("a gate mapped to nil claims a kernel counterpart: %v",
			*deliberate.KernelMutationGate)
	}
}

func TestEveryMappedKernelGateStillExistsInTheContract(t *testing.T) {
	// kernelMutationGateIDs is a hand-authored cross-reference into the
	// kernel's own contract -- accurate when written, and with nothing to
	// catch the kernel renaming or removing an id out from under it. A stale
	// mapping puts an id in the plan that the kernel will not recognise, at
	// the moment someone is trying to record an approval.
	//
	// The contract is read as data, which is one of exactly two couplings the
	// kernel boundary permits; importing internal/kernel here would be the
	// other kind.
	raw, err := os.ReadFile(filepath.Join(selectorRepoRoot(t), "kernel-contracts",
		"mutation-gates.json"))
	if err != nil {
		t.Fatalf("reading the mutation-gates contract: %v", err)
	}
	var contract struct {
		HumanOnly []struct {
			ID string `json:"id"`
		} `json:"human_only"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("parsing the mutation-gates contract: %v", err)
	}
	if len(contract.HumanOnly) == 0 {
		t.Fatal("the contract declares no human-only gates; this test would prove nothing")
	}
	known := map[string]bool{}
	for _, entry := range contract.HumanOnly {
		known[entry.ID] = true
	}

	mapped := 0
	for selectorGate, kernelGate := range kernelMutationGateIDs {
		if kernelGate == nil {
			continue
		}
		mapped++
		if !known[*kernelGate] {
			t.Errorf("%s maps to kernel gate %q, which the contract no longer declares. "+
				"Known: %v", selectorGate, *kernelGate, contract.HumanOnly)
		}
	}
	if mapped == 0 {
		t.Fatal("no gate maps to a kernel counterpart; the reconciliation checks nothing")
	}

	// Deliberately one-directional. The kernel may declare gates this selector
	// never raises -- risk-acceptance is one today -- and requiring a mapping
	// for each would fail whenever the kernel grows a gate that is nothing to
	// do with routing.
}

func TestEveryQualityGateIsAlsoMarkedRequired(t *testing.T) {
	// Found by a mistake worth keeping. A mutation aimed at the human-gate
	// builder patched the quality-gate one instead -- both write
	// `Required: true` and the edit hit the first -- and *nothing failed*.
	//
	// The field is not decorative: FormatPlanText filters on it, and
	// plantext_test.go proves that filter works by showing a gate marked
	// required:false is excluded. So a quality gate built unrequired would
	// vanish from the rendered plan while still sitting in the JSON, and the
	// renderer's own test would keep passing.
	//
	// required_quality_gates is the field's name. A gate in it that is not
	// required is a contradiction the schema permits and nothing else caught.
	// A route contributes gates by declaring quality_gates; a route that
	// declares none produces none, which is why the first attempt at this
	// test built an empty list and said so rather than passing.
	contract := loadLifecycleContract(t)
	gates, err := BuildQualityGates(
		[]Match{{ID: "backend", Rule: map[string]any{
			"id": "backend", "quality_gates": []any{"G3", "G5"},
		}}},
		[]Match{{ID: "production", Rule: map[string]any{
			"id": "production", "quality_gates": []any{"G9"},
		}}},
		contract.Gates)
	if err != nil {
		t.Fatalf("BuildQualityGates: %v", err)
	}
	if len(gates) == 0 {
		t.Fatal("no quality gate was built; this test would prove nothing")
	}
	for _, gate := range gates {
		if !gate.Required {
			t.Errorf("quality gate %s is not marked required, so FormatPlanText "+
				"drops it while the JSON still carries it", gate.ID)
		}
		if strings.TrimSpace(gate.Reason) == "" {
			t.Errorf("quality gate %s carries no reason", gate.ID)
		}
	}
}
