package orchestration

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mockAgentRunner is a test implementation of AgentRunner.
type mockAgentRunner struct {
	results map[string]*AgentResult
	errors  map[string]error
	delays  map[string]time.Duration
}

func (m *mockAgentRunner) RunAgent(ctx context.Context, agentID string, task string, plan *DispatchPlan) (*AgentResult, error) {
	// Simulate delay if configured
	if delay, ok := m.delays[agentID]; ok {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return &AgentResult{
				AgentID: agentID,
				Status:  "timeout",
				Error:   ctx.Err().Error(),
			}, ctx.Err()
		}
	}

	// Return error if configured
	if err, ok := m.errors[agentID]; ok {
		return nil, err
	}

	// Return result if configured
	if res, ok := m.results[agentID]; ok {
		res.AgentID = agentID
		res.StartedAt = time.Now()
		res.CompletedAt = time.Now()
		return res, nil
	}

	// Default success
	return &AgentResult{
		AgentID:     agentID,
		Status:      "success",
		ExitCode:    0,
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
	}, nil
}

func TestExecutorRunSimplePlan(t *testing.T) {
	runner := &mockAgentRunner{
		results: make(map[string]*AgentResult),
		errors:  make(map[string]error),
	}

	executor := NewExecutor(runner, 4, 5*time.Second)

	plan := &DispatchPlan{
		TaskID:         "TASK-001",
		Task:           "Test task",
		Classification: "internal",
		Agents: AgentGroups{
			Primary:   []string{"primary-agent-1"},
			Reviewers: []string{"reviewer-1"},
			Support:   []string{"support-1"},
		},
	}

	result, err := executor.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatalf("result is nil")
	}

	if result.SuccessCount != 3 {
		t.Errorf("expected 3 successful agents, got %d", result.SuccessCount)
	}

	if result.TotalErrors != 0 {
		t.Errorf("expected 0 errors, got %d", result.TotalErrors)
	}

	// Verify wave coordination
	if len(result.Waves) != 3 {
		t.Errorf("expected 3 waves, got %d", len(result.Waves))
	}
}

func TestExecutorCoordinateWaves(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)

	plan := &DispatchPlan{
		Agents: AgentGroups{
			Primary:   []string{"primary-1", "primary-2"},
			Reviewers: []string{"reviewer-1"},
			Support:   []string{"support-1", "support-2", "support-3"},
		},
	}

	waves := executor.coordinateWaves(plan)

	if len(waves) != 3 {
		t.Errorf("expected 3 waves, got %d", len(waves))
	}

	// Primary wave should have 2 agents
	if len(waves[0]) != 2 {
		t.Errorf("expected 2 primary agents in wave 1, got %d", len(waves[0]))
	}

	// Reviewer wave should have 1 agent
	if len(waves[1]) != 1 {
		t.Errorf("expected 1 reviewer in wave 2, got %d", len(waves[1]))
	}

	// Support wave should have 3 agents
	if len(waves[2]) != 3 {
		t.Errorf("expected 3 support agents in wave 3, got %d", len(waves[2]))
	}
}

func TestExecutorDetermineRole(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)

	plan := &DispatchPlan{
		Agents: AgentGroups{
			Primary:   []string{"primary-1"},
			Reviewers: []string{"reviewer-1"},
			Support:   []string{"support-1"},
		},
	}

	tests := []struct {
		agentID      string
		expectedRole string
	}{
		{"primary-1", "primary"},
		{"reviewer-1", "reviewer"},
		{"support-1", "support"},
		{"unknown-agent", "unknown"},
	}

	for _, tt := range tests {
		role := executor.determineRole(tt.agentID, plan)
		if role != tt.expectedRole {
			t.Errorf("determineRole(%q) = %q, want %q", tt.agentID, role, tt.expectedRole)
		}
	}
}

func TestExecutorHandlesErrors(t *testing.T) {
	runner := &mockAgentRunner{
		results: make(map[string]*AgentResult),
		errors: map[string]error{
			"primary-1": ErrAgentFailed,
		},
	}

	executor := NewExecutor(runner, 4, 5*time.Second)

	plan := &DispatchPlan{
		TaskID:         "TASK-001",
		Task:           "Test task",
		Classification: "internal",
		Agents: AgentGroups{
			Primary: []string{"primary-1"},
		},
	}

	result, err := executor.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalErrors != 1 {
		t.Errorf("expected 1 error, got %d", result.TotalErrors)
	}

	agentRes, ok := result.AgentResults["primary-1"]
	if !ok {
		t.Fatalf("agent result not found for primary-1")
	}

	if agentRes.Status != "failed" {
		t.Errorf("expected status failed, got %q", agentRes.Status)
	}
}

func TestExecutorParallelWaveExecution(t *testing.T) {
	runner := &mockAgentRunner{
		results: make(map[string]*AgentResult),
		errors:  make(map[string]error),
		delays: map[string]time.Duration{
			"primary-1": 100 * time.Millisecond,
			"primary-2": 100 * time.Millisecond,
		},
	}

	executor := NewExecutor(runner, 4, 5*time.Second)

	plan := &DispatchPlan{
		TaskID:         "TASK-001",
		Task:           "Test task",
		Classification: "internal",
		Agents: AgentGroups{
			Primary: []string{"primary-1", "primary-2"},
		},
	}

	startTime := time.Now()
	result, err := executor.Execute(context.Background(), plan)
	elapsed := time.Since(startTime)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With parallel execution, should take ~100ms (not ~200ms)
	// Add some buffer for overhead
	if elapsed > 300*time.Millisecond {
		t.Errorf("execution took too long (%v), suggests agents ran sequentially", elapsed)
	}

	if result.SuccessCount != 2 {
		t.Errorf("expected 2 successful agents, got %d", result.SuccessCount)
	}
}

func TestExecutorNilPlanError(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)

	_, err := executor.Execute(context.Background(), nil)
	if err == nil {
		t.Errorf("expected error for nil plan")
	}
}

func TestExecutorEmptyPlan(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)

	plan := &DispatchPlan{
		TaskID:         "TASK-001",
		Task:           "Test task",
		Classification: "internal",
	}

	result, err := executor.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SuccessCount != 0 {
		t.Errorf("expected 0 agents, got %d successes", result.SuccessCount)
	}

	if len(result.Waves) != 0 {
		t.Errorf("expected 0 waves, got %d", len(result.Waves))
	}
}

// ErrAgentFailed is a test error.
var ErrAgentFailed = fmt.Errorf("agent execution failed")
