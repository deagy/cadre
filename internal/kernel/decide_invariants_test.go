package kernel

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// Decide: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of decide_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

func TestDecideRefusesSelfApprovalEvenWhenEverythingElseIsInOrder(t *testing.T) {
	// The case that matters most: a gate that is otherwise completely ready to
	// approve, where the only thing wrong is who is approving it. Every other
	// check passes, so nothing but this one stands between the record and a
	// forged approval.
	for _, role := range []string{"preparers", "independent_verifier"} {
		t.Run(role, func(t *testing.T) {
			root, manifest := decidableProject(t)
			makeGateApprovable(t, root)
			setGateIdentity(role, decideActor)(t, root)

			registry := NewRegistry()
			if err := registry.LoadProvider(manifest); err != nil {
				t.Fatal(err)
			}
			before := readFile(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"))

			_, err := registry.Decide(DecideRequest{
				Root: root, TaskID: decideTask, GateID: "G1", AuthorityRole: "product_owner",
				Decision: "approved", ActorID: decideActor, EvidenceURI: decideEvidence,
				DecidedAt: decideWhen,
			})
			if err == nil {
				t.Fatal("an identity approved work it was involved in producing")
			}
			after := readFile(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"))
			if before != after {
				t.Error("the refused decision still wrote to the run record")
			}
		})
	}
}

func TestDecideCannotApproveAGateOnItsOwnSayS0(t *testing.T) {
	// One authority's "approved" is an input, not a conclusion. Without the
	// artifacts, evidence and verifier declaration a gate requires, the
	// approval is recorded and the gate stays where it was.
	root, manifest := decidableProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Decide(DecideRequest{
		Root: root, TaskID: decideTask, GateID: "G1", AuthorityRole: "product_owner",
		Decision: "approved", ActorID: decideActor, EvidenceURI: decideEvidence,
		DecidedAt: decideWhen,
	}); err != nil {
		t.Fatalf("the decision itself was refused: %v", err)
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(readFile(t,
		filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"))), &record); err != nil {
		t.Fatal(err)
	}
	gates, _ := record["lifecycle_gates"].([]any)
	gate, _ := gates[0].(map[string]any)
	if gate["status"] == "approved" {
		t.Error("a gate with no bound artifacts, evidence or verifier was marked approved")
	}
	approvals, _ := gate["human_approvals"].([]any)
	if len(approvals) != 1 {
		t.Errorf("the decision itself should still be recorded, got %d approvals", len(approvals))
	}
}

func TestARejectionWithdrawsAnEarlierApproval(t *testing.T) {
	// A gate that reached approved and is then rejected must not stay
	// approved. The earlier approval stays in the record as history; the gate
	// does not keep the status it was given.
	root, manifest := decidableProject(t)
	makeGateApprovable(t, root)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	decision := DecideRequest{
		Root: root, TaskID: decideTask, GateID: "G1", AuthorityRole: "product_owner",
		Decision: "approved", ActorID: decideActor, EvidenceURI: decideEvidence,
		DecidedAt: decideWhen,
	}
	if _, err := registry.Decide(decision); err != nil {
		t.Fatal(err)
	}
	if status := gateStatus(t, root); status != "approved" {
		t.Fatalf("the fixture did not reach approved (%s); this test would prove nothing", status)
	}

	decision.Decision = "rejected"
	if _, err := registry.Decide(decision); err != nil {
		t.Fatal(err)
	}
	if status := gateStatus(t, root); status == "approved" {
		t.Error("the gate stayed approved after its approval was withdrawn")
	}
}

func setGateIdentity(field, id string) func(*testing.T, string) {
	return func(t *testing.T, root string) {
		t.Helper()
		mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
			func(document map[string]any) {
				gates, _ := document["lifecycle_gates"].([]any)
				gate, _ := gates[0].(map[string]any)
				identity := map[string]any{"id": id, "kind": "human", "role": "Product Owner"}
				if field == "preparers" {
					gate["preparers"] = []any{identity}
					return
				}
				gate["independent_verifier"] = identity
			})
	}
}

func gateStatus(t *testing.T, root string) string {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(readFile(t,
		filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"))), &record); err != nil {
		t.Fatal(err)
	}
	gates, _ := record["lifecycle_gates"].([]any)
	gate, _ := gates[0].(map[string]any)
	status, _ := gate["status"].(string)
	return status
}
