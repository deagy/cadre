package kernel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Plan: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of plan_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

func TestPlanRefusesToRedefineAnExistingTask(t *testing.T) {
	// The refusal that protects a record already accumulating approvals. A
	// re-plan with different text is not an update -- it is a different task
	// wearing an ID somebody has already approved gates against.
	root, manifest := initialisedProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}

	first := PlanRequest{Root: root, TaskID: "TASK-9", Task: "add an endpoint to the billing service"}
	if _, err := registry.Plan(first); err != nil {
		t.Fatalf("the first plan failed: %v", err)
	}
	// The same plan again is a no-op, not a conflict.
	if _, err := registry.Plan(first); err != nil {
		t.Errorf("re-planning an identical task was refused: %v", err)
	}

	changed := first
	changed.Task = "something else entirely"
	err := registry.Plan2Error(changed)
	if err == nil {
		t.Fatal("re-planning the same ID with different text was accepted")
	}
	if !strings.Contains(err.Error(), "use a new task ID") {
		t.Errorf("the refusal does not say what to do instead: %v", err)
	}

	// And the existing record is untouched: a refused plan must not have
	// half-written anything.
	record := readTaskDocumentNamed(t, root, "TASK-9", "run-record.json")
	if !strings.Contains(record, "add an endpoint to the billing service") {
		t.Error("the refused re-plan modified the existing run record")
	}
}

// Plan2Error is a readability shim for the tests: Plan returns a result the
// error cases never use.

func TestPlanNeverMarksAGateApproved(t *testing.T) {
	// The invariant `plan` exists under. It may move one gate to `ready` --
	// "the prerequisites are met, somebody may now look at this" -- and that
	// is the furthest it is allowed to go. A planner that could approve would
	// make every gate downstream of it decorative.
	root, manifest := initialisedProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Plan(PlanRequest{
		Root: root, TaskID: "TASK-8", Task: "add an endpoint to the billing service",
	}); err != nil {
		t.Fatal(err)
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(readTaskDocumentNamed(t, root, "TASK-8", "run-record.json")),
		&record); err != nil {
		t.Fatal(err)
	}
	gates, _ := record["lifecycle_gates"].([]any)
	if len(gates) != len(GateIDs) {
		t.Fatalf("expected %d gates, got %d", len(GateIDs), len(gates))
	}
	ready := 0
	for _, raw := range gates {
		gate, _ := raw.(map[string]any)
		switch gate["status"] {
		case "approved":
			t.Errorf("%v was approved by planning", gate["gate_id"])
		case "ready":
			ready++
		}
	}
	// At most one, and for this project exactly one -- G1 has no
	// prerequisites and its authority is assigned in the fixture.
	if ready != 1 {
		t.Errorf("expected exactly one gate to be ready after planning, got %d", ready)
	}
}

func TestPlanRefusesATaskIDThatEscapesTheProject(t *testing.T) {
	root, manifest := initialisedProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	for _, hostile := range []string{"../escape", "..", ".", "a/b", "", "with space"} {
		if err := registry.Plan2Error(PlanRequest{
			Root: root, TaskID: hostile, Task: "add an endpoint",
		}); err == nil {
			t.Errorf("task ID %q was accepted", hostile)
		}
	}
}

func (r *Registry) Plan2Error(request PlanRequest) error {
	_, err := r.Plan(request)
	return err
}

func readTaskDocumentNamed(t *testing.T, root, taskID, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, Overlay, "runs", taskID, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(data)
}
