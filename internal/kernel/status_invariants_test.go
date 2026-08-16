package kernel

import (
	"path/filepath"
	"testing"
)

// Status: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of status_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

func TestStatusAdvancesAGateAndSaysSo(t *testing.T) {
	// The behaviour the name hides, stated outright: running status changes
	// the task. Anyone reading this file should find that here rather than
	// discovering it from a diff.
	root, manifest := approvedProject(t)
	mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
		func(document map[string]any) {
			for _, raw := range listOf(document["lifecycle_gates"]) {
				gate, _ := raw.(map[string]any)
				gate["status"] = "pending"
			}
		})
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}

	before := gateStatuses(t, root)
	if before["G1"] == "ready" {
		t.Fatal("the fixture already has a ready gate; this test would prove nothing")
	}
	if _, err := registry.Status(root, decideTask); err != nil {
		t.Fatal(err)
	}
	if after := gateStatuses(t, root); after["G1"] != "ready" {
		t.Errorf("status did not advance the first eligible gate: %q", after["G1"])
	}
}

func TestStatusNeverApproves(t *testing.T) {
	// The line it must not cross. Advancing to `ready` means "somebody may now
	// look at this"; a status command that could approve would make every gate
	// downstream of it decorative.
	root, manifest := approvedProject(t)
	mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
		func(document map[string]any) {
			for _, raw := range listOf(document["lifecycle_gates"]) {
				gate, _ := raw.(map[string]any)
				gate["status"] = "pending"
			}
		})
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	// Repeatedly, because the failure worth guarding is a status that walks
	// the run forward one gate at a time until everything is approved.
	for attempt := 0; attempt < len(GateIDs)+2; attempt++ {
		if _, err := registry.Status(root, decideTask); err != nil {
			t.Fatal(err)
		}
	}
	statuses := gateStatuses(t, root)
	for _, gateID := range GateIDs {
		if statuses[gateID] == "approved" {
			t.Errorf("%s was approved by repeated status calls", gateID)
		}
	}
	ready := 0
	for _, status := range statuses {
		if status == "ready" {
			ready++
		}
	}
	if ready != 1 {
		t.Errorf("expected exactly one ready gate after repeated calls, got %d", ready)
	}
}

func TestTheProjectionLeavesTheRecordAlone(t *testing.T) {
	// The read-only half exists because the gate-status publishers render a
	// task onto a pull request, and the render must not move the task on.
	root, manifest := approvedProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(root, Overlay, "runs", decideTask, "run-record.json")
	before := readFile(t, recordPath)

	projection, err := registry.GateStatusProjection(root, decideTask)
	if err != nil {
		t.Fatal(err)
	}
	if len(listOf(projection.values["gates"])) != len(GateIDs) {
		t.Errorf("the projection reported %d gates", len(listOf(projection.values["gates"])))
	}
	if readFile(t, recordPath) != before {
		t.Error("the projection wrote to the run record")
	}
}
