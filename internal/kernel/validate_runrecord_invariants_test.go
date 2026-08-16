package kernel

import (
	"path/filepath"
	"strings"
	"testing"
)

// Validate runrecord: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of validate_runrecord_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

// TestAnApprovedGateIsInvalidWhenItsApproverPreparedIt states the invariant
// directly rather than by comparison, so that it survives the Python kernel's
// removal -- which is the point of the whole port.
func TestAnApprovedGateIsInvalidWhenItsApproverPreparedIt(t *testing.T) {
	root, manifest := plannedProject(t)
	mutateJSON(t, recordPathIn(root), func(document map[string]any) {
		approveG1(document)
		gate := firstGate(document)
		preparers := listOf(gate["preparers"])
		for _, approval := range gateApprovals(gate) {
			preparers = append(preparers, approval["approver"])
		}
		gate["preparers"] = preparers
	})

	report := goValidateProject(t, root, manifest)
	if report.Valid {
		t.Fatal("a gate approved by the identity that prepared it was called valid")
	}
	if report.Ready {
		t.Error("an invalid project was also reported ready")
	}
}

func recordPathIn(root string) string {
	return filepath.Join(root, Overlay, "runs", fixtureTask, "run-record.json")
}

// approveG1 promotes G1 to an approval that passes every check, so that a
// case breaking one thing is breaking exactly one thing.
func approveG1(document map[string]any) {
	gates, _ := document["lifecycle_gates"].([]any)
	if len(gates) == 0 {
		return
	}
	gate, _ := gates[0].(map[string]any)
	gate["status"] = "approved"
	gate["applicability"] = "applicable"
	gate["decided_at"] = "2026-08-15T09:00:00+00:00"
	gate["evidence_refs"] = []any{map[string]any{
		"evidence_id": "g1-intent", "uri": "github-issue:acme/app:issues/7",
		"hash_algorithm": "sha256", "hash": strings.Repeat("a", 64),
		"classification": "internal",
	}}
	gate["artifact_bindings"] = []any{map[string]any{
		"artifact_id": "intent", "revision": "r1", "digest": "sha256:abc",
	}}
	gate["preparers"] = []any{map[string]any{
		"id": "agent://product-intent-agent", "kind": "agent", "role": "product-intent-agent",
	}}
	gate["independent_verifier"] = map[string]any{
		"id": "agent://code-reviewer", "kind": "agent", "role": "code-reviewer",
	}
	gate["independence_declaration"] = map[string]any{
		"verifier_confirmed_not_preparer": true, "verifier_made_material_correction": false,
	}
}

func firstGate(document map[string]any) map[string]any {
	gates, _ := document["lifecycle_gates"].([]any)
	if len(gates) == 0 {
		return map[string]any{}
	}
	gate, _ := gates[0].(map[string]any)
	return gate
}

func gateApprovals(gate map[string]any) []map[string]any {
	var approvals []map[string]any
	for _, raw := range listOf(gate["human_approvals"]) {
		if approval, ok := raw.(map[string]any); ok {
			approvals = append(approvals, approval)
		}
	}
	return approvals
}

// runRecordProbes each break one thing about a real, planned run record.
//
// `expect` names a substring the resulting report must contain. Without it a
// case could pass by both implementations reporting nothing, which is
// agreement about having checked nothing.

func goValidateProject(t *testing.T, root, manifest string) ValidationReport {
	t.Helper()
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatalf("LoadProvider: %v", err)
	}
	overlay, err := LoadOverlay(root)
	if err != nil {
		t.Fatalf("LoadOverlay: %v", err)
	}
	return registry.ValidateProject(root, overlay)
}
