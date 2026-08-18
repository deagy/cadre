package executor

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/deagy/cadre/cli/internal/engine/state"
)

// The persistent store must behave exactly as the in-memory one, because the
// executor is written against the interface and a service swaps them.
func TestSQLiteCheckpointerRoundTrips(t *testing.T) {
	store, err := OpenSQLiteCheckpointer(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatalf("OpenSQLiteCheckpointer: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, found, err := store.Load("absent"); err != nil || found {
		t.Errorf("Load of an absent task = (%v, %v), want not found and no error", found, err)
	}

	original := Checkpoint{
		State: state.SDLCState{
			TaskID: "task-1", Scope: "add a feature",
			LifecycleGates: map[string]state.GateState{"G1": {GateID: "G1", Status: "ready"}},
		},
		Pending: &Suspension{Kind: SuspendHumanApproval, GateID: "G1",
			Payload: map[string]any{"reason": "Human approval required for gate G1"}},
	}
	if err := store.Save("task-1", original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, found, err := store.Load("task-1")
	if err != nil || !found {
		t.Fatalf("Load = (%v, %v)", found, err)
	}
	if loaded.State.Scope != "add a feature" || loaded.State.LifecycleGates["G1"].Status != "ready" {
		t.Errorf("state did not round-trip: %+v", loaded.State)
	}
	// Pending is what makes resumption land on the right question.
	if loaded.Pending == nil || loaded.Pending.GateID != "G1" || loaded.Pending.Kind != SuspendHumanApproval {
		t.Errorf("pending did not round-trip: %+v", loaded.Pending)
	}
}

// One row per task: a run has a single current position, and keeping older
// ones would invite resuming from a stale point.
func TestSQLiteCheckpointerReplacesRatherThanAccumulates(t *testing.T) {
	store, err := OpenSQLiteCheckpointer(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatalf("OpenSQLiteCheckpointer: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, status := range []string{"ready", "approved"} {
		if err := store.Save("task-1", Checkpoint{State: state.SDLCState{
			LifecycleGates: map[string]state.GateState{"G1": {GateID: "G1", Status: status}},
		}}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	loaded, _, _ := store.Load("task-1")
	if got := loaded.State.LifecycleGates["G1"].Status; got != "approved" {
		t.Errorf("loaded status = %q, want the most recent save", got)
	}

	if err := store.Rewind("task-1"); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	if _, found, _ := store.Load("task-1"); found {
		t.Error("a rewound run is still loadable")
	}
}

// A whole run through the persistent store, as a service would drive it.
func TestARunSurvivesThePersistentStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.db")
	store, err := OpenSQLiteCheckpointer(path)
	if err != nil {
		t.Fatalf("OpenSQLiteCheckpointer: %v", err)
	}

	first := harness(t)
	first.Checkpointer = store
	if _, err := first.Start("task-1", state.SDLCState{Scope: "add a feature"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A new process, opening the same file.
	reopened, err := OpenSQLiteCheckpointer(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	second := harness(t)
	second.Checkpointer = reopened
	second.Now = func() time.Time { return time.Date(2026, 8, 18, 13, 0, 0, 0, time.UTC) }

	result, err := second.Resume("task-1", approvalDecision("product_owner"))
	if err != nil {
		t.Fatalf("Resume after reopening: %v", err)
	}
	if result.State.LifecycleGates["G1"].Status != "approved" {
		t.Error("the decision was not applied to the run stored on disk")
	}
}
