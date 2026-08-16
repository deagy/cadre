package kernel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reentry: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of reentry_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

func TestInvalidatingAGateInvalidatesEverythingAfterIt(t *testing.T) {
	// The cascade is the whole point. A gate downstream of an invalidated one
	// rests on work that has just been withdrawn, and leaving it approved
	// would mean a record where the foundation is gone and the approval on top
	// of it is not.
	root, manifest := approvedProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Invalidate(RecordSurgeryRequest{
		Root: root, TaskID: decideTask, EarliestGate: "G4",
		Reason: "governance rework", Actor: "github.com/governance-lead",
	}); err != nil {
		t.Fatal(err)
	}

	statuses := gateStatuses(t, root)
	for index, gateID := range GateIDs {
		if index < 3 {
			if statuses[gateID] == "invalidated" {
				t.Errorf("%s is before the invalidated gate and was invalidated anyway", gateID)
			}
			continue
		}
		if statuses[gateID] != "invalidated" {
			t.Errorf("%s is downstream of G4 and is %q", gateID, statuses[gateID])
		}
	}
	// And G1's approval survives, because nothing said it should not.
	if statuses["G1"] != "approved" {
		t.Errorf("G1 was approved before and is now %q", statuses["G1"])
	}
}

func TestInvalidationNamesAnActorAReasonAndTheArtifactsItCovers(t *testing.T) {
	// An invalidation with no actor is an approval disappearing with nobody's
	// name on it. The artifact list matters for the same reason: it is what
	// tells a later reader which artifacts the withdrawal actually covered,
	// rather than leaving them to re-derive it from a gate range.
	root, manifest := approvedProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Invalidate(RecordSurgeryRequest{
		Root: root, TaskID: decideTask, EarliestGate: "G1",
		Reason: "requirements changed", Actor: decideActor,
	}); err != nil {
		t.Fatal(err)
	}

	record := decodeRecord(t, root)
	history, _ := record["re_entry_history"].([]any)
	if len(history) != 1 {
		t.Fatalf("expected one history entry, got %d", len(history))
	}
	entry, _ := history[0].(map[string]any)
	if entry["actor"] != decideActor || entry["reason"] != "requirements changed" {
		t.Errorf("the history does not name who and why: %v", entry)
	}
	if bindings, _ := entry["affected_artifact_bindings"].([]any); len(bindings) == 0 {
		t.Error("the invalidation covers a gate with a bound artifact but names none")
	}
	// The same entry is attached to each invalidated gate, so a reader looking
	// at one gate does not have to reconstruct why it is invalid.
	gates, _ := record["lifecycle_gates"].([]any)
	first, _ := gates[0].(map[string]any)
	if gateHistory, _ := first["invalidation_history"].([]any); len(gateHistory) != 1 {
		t.Errorf("the invalidated gate carries %d history entries", len(gateHistory))
	}
}

func TestReenterClearsEverythingThatMadeAGateApproved(t *testing.T) {
	// A cleared gate that still carries its old approval is one where the next
	// reader cannot tell what has been re-done.
	root, manifest := approvedProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Reenter(RecordSurgeryRequest{
		Root: root, TaskID: decideTask, EarliestGate: "G1",
		Reason: "restarting", Actor: decideActor,
	}); err != nil {
		t.Fatal(err)
	}

	record := decodeRecord(t, root)
	gates, _ := record["lifecycle_gates"].([]any)
	gate, _ := gates[0].(map[string]any)
	if gate["status"] == "approved" {
		t.Error("a re-entered gate is still approved")
	}
	for _, field := range []string{"human_approvals", "evidence_refs", "artifact_bindings"} {
		if values, _ := gate[field].([]any); len(values) != 0 {
			t.Errorf("%s survived re-entry: %v", field, values)
		}
	}
	if gate["decided_at"] != nil {
		t.Errorf("the gate kept its decision time: %v", gate["decided_at"])
	}
	// The paired top-level field goes with it. Clearing the evidence and
	// leaving intent_record_id behind points the record at evidence that no
	// longer exists.
	if record["intent_record_id"] != nil {
		t.Errorf("intent_record_id still points at cleared evidence: %v", record["intent_record_id"])
	}
}

func TestUpgradeCheckWritesNothing(t *testing.T) {
	// The two modes exist so a project can find out it is behind without
	// something changing underneath it. A --check that wrote would make the
	// safe option indistinguishable from the other one.
	root, manifest := approvedProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, Overlay, "version.lock")
	before := readFile(t, lockPath)

	result, err := registry.Upgrade(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.values["mutation"] != false {
		t.Error("--check reported a mutation")
	}
	if after := readFile(t, lockPath); after != before {
		t.Error("--check rewrote the version lock")
	}

	// And --apply does write, or the check above is comparing two no-ops.
	if err := os.WriteFile(lockPath,
		[]byte(strings.Replace(before, `"kernel_version"`, `"kernel_version_was"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Upgrade(root, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, lockPath), Version) {
		t.Error("--apply did not write the current kernel version")
	}
}

// approvedProject is a project whose G1 is approved: the state these commands
// exist to undo.
func approvedProject(t *testing.T) (root, manifest string) {
	t.Helper()
	root, manifest = decidableProject(t)
	makeGateApprovable(t, root)

	// A source link populates both the gate's evidence and a top-level record
	// field. Reenter has to clear the pair together, and a fixture without one
	// cannot show that.
	mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
		func(document map[string]any) {
			document["intent_record_id"] = "github-issue:acme/app:issues/7"
		})

	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Decide(DecideRequest{
		Root: root, TaskID: decideTask, GateID: "G1", AuthorityRole: "product_owner",
		Decision: "approved", ActorID: decideActor, EvidenceURI: decideEvidence,
		DecidedAt: decideWhen,
	}); err != nil {
		t.Fatalf("seeding an approval: %v", err)
	}
	if status := gateStatus(t, root); status != "approved" {
		t.Fatalf("the fixture's G1 is %s, not approved; these cases would prove nothing", status)
	}
	return root, manifest
}

func gateStatuses(t *testing.T, root string) map[string]string {
	t.Helper()
	statuses := map[string]string{}
	gates, _ := decodeRecord(t, root)["lifecycle_gates"].([]any)
	for _, raw := range gates {
		gate, _ := raw.(map[string]any)
		gateID, _ := gate["gate_id"].(string)
		status, _ := gate["status"].(string)
		statuses[gateID] = status
	}
	return statuses
}

func decodeRecord(t *testing.T, root string) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(readFile(t,
		filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"))), &record); err != nil {
		t.Fatal(err)
	}
	return record
}
