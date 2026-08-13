package orchestration

import (
	"context"
	"testing"
	"time"
)

func TestNewSubprocessRunner(t *testing.T) {
	runner := NewSubprocessRunner("/path/to/repo")
	if runner == nil {
		t.Fatalf("NewSubprocessRunner returned nil")
	}
}

func TestSubprocessRunnerBasic(t *testing.T) {
	runner := NewSubprocessRunner("/path/to/repo")

	plan := &DispatchPlan{
		TaskID:         "TASK-001",
		Task:           "Test task",
		Classification: "internal",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := runner.RunAgent(ctx, "test-agent", "test task", plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatalf("result is nil")
	}

	if result.AgentID != "test-agent" {
		t.Errorf("agent ID mismatch: got %q", result.AgentID)
	}

	// Currently returns skipped (placeholder)
	if result.Status != "skipped" {
		t.Errorf("expected status skipped for placeholder, got %q", result.Status)
	}
}

func TestSubprocessRunnerNilPlan(t *testing.T) {
	runner := NewSubprocessRunner("/path/to/repo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := runner.RunAgent(ctx, "test-agent", "test task", nil)
	if err == nil {
		t.Errorf("expected error for nil plan")
	}
}
