package kernel

import (
	"bytes"
	"path/filepath"
	"testing"
)

// `status`, compared with the Python kernel -- including the part of its
// behaviour that its name hides.
//
// It writes. Running it advances the next eligible gate to `ready` and
// persists the record, so the cases below compare the run record as well as
// the printed output: a port that only printed the advance would agree on
// stdout and silently stop moving gates for every project that relies on it.

func TestStatusAgreesWithThePythonKernel(t *testing.T) {
	for _, probe := range []struct {
		name    string
		prepare func(t *testing.T, root string)
	}{
		{"a task with an approved gate", nil},
		{"a task where nothing has been decided", func(t *testing.T, root string) {
			// Every gate back to pending, so the advance has somewhere to go.
			// Against the approved fixture alone, a port that never advanced
			// anything would still agree.
			mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
				func(document map[string]any) {
					for _, raw := range listOf(document["lifecycle_gates"]) {
						gate, _ := raw.(map[string]any)
						gate["status"] = "pending"
					}
				})
		}},
		{"a task whose gates are all invalidated", func(t *testing.T, root string) {
			mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
				func(document map[string]any) {
					for _, raw := range listOf(document["lifecycle_gates"]) {
						gate, _ := raw.(map[string]any)
						gate["status"] = "invalidated"
						gate["required_reentry_gate"] = "G1"
					}
				})
		}},
	} {
		t.Run(probe.name, func(t *testing.T) {
			pythonRoot, manifest := approvedProject(t)
			if probe.prepare != nil {
				probe.prepare(t, pythonRoot)
			}
			goRoot := t.TempDir()
			if err := copyTree(pythonRoot, goRoot); err != nil {
				t.Fatal(err)
			}

			pythonCode, pythonOutput := runPythonKernel(repositoryRoot(t), "--provider", manifest,
				"status", "--root", pythonRoot, "--task-id", decideTask)
			var stdout, stderr bytes.Buffer
			goCode := Run([]string{"--provider", manifest, "status", "--root", goRoot,
				"--task-id", decideTask}, &stdout, &stderr)

			if pythonCode != 0 || goCode != 0 {
				t.Fatalf("expected success -- python %d, go %d\npython: %s\ngo: %s",
					pythonCode, goCode, pythonOutput, stderr.String())
			}
			if pythonOutput != stdout.String() {
				t.Errorf("output differs.\npython:\n%s\ngo:\n%s", pythonOutput, stdout.String())
			}

			recordPath := filepath.Join(Overlay, "runs", decideTask, "run-record.json")
			if readFile(t, filepath.Join(pythonRoot, recordPath)) !=
				readFile(t, filepath.Join(goRoot, recordPath)) {
				t.Error("the run records differ after status")
			}
		})
	}
}

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
