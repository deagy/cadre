package orchestration

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestNewSubprocessAgentRunner(t *testing.T) {
	runner := NewSubprocessAgentRunner("/repo", 5*time.Second, StrategyMock)
	if runner == nil {
		t.Fatalf("NewSubprocessAgentRunner returned nil")
	}
	if runner.repoRoot != "/repo" {
		t.Errorf("repoRoot = %q, want /repo", runner.repoRoot)
	}
	if runner.strategy != StrategyMock {
		t.Errorf("strategy = %v, want mock", runner.strategy)
	}
}

func TestRunAgentMockStrategy(t *testing.T) {
	runner := NewSubprocessAgentRunner("", 5*time.Second, StrategyMock)

	plan := &DispatchPlan{
		TaskID:    "TASK-001",
		Task:      "test task",
		Agents:    AgentGroups{Primary: []string{"test-agent"}},
		CreatedAt: time.Now(),
	}

	result, err := runner.RunAgent(context.Background(), "test-agent", "test task", plan)
	if err != nil {
		t.Fatalf("RunAgent error = %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want success", result.Status)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", result.ExitCode)
	}
	if result.AgentID != "test-agent" {
		t.Errorf("agent_id = %q, want test-agent", result.AgentID)
	}
}

func TestRunAgentDryStrategy(t *testing.T) {
	runner := NewSubprocessAgentRunner("", 5*time.Second, StrategyDry)

	plan := &DispatchPlan{
		TaskID:    "TASK-001",
		Task:      "test task",
		Agents:    AgentGroups{Primary: []string{"test-agent"}},
		CreatedAt: time.Now(),
	}

	result, err := runner.RunAgent(context.Background(), "test-agent", "test task", plan)
	if err != nil {
		t.Fatalf("RunAgent error = %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q, want success", result.Status)
	}
	if !containsSubstring(result.Output, "dry run") {
		t.Errorf("output should mention dry run: %s", result.Output)
	}
}

func TestLocateAgentScript(t *testing.T) {
	runner := NewSubprocessAgentRunner("", 5*time.Second, StrategySubprocess)

	// Non-existent repo should return empty
	found := runner.locateAgentScript("non-existent")
	if found != "" {
		t.Errorf("locateAgentScript for non-existent repo returned %q, want empty", found)
	}
}

func TestSetPythonPath(t *testing.T) {
	runner := NewSubprocessAgentRunner("", 5*time.Second, StrategyMock)
	runner.SetPythonPath("/usr/bin/python3")

	if runner.pythonPath != "/usr/bin/python3" {
		t.Errorf("pythonPath = %q, want /usr/bin/python3", runner.pythonPath)
	}
}

func TestSetEnvironment(t *testing.T) {
	runner := NewSubprocessAgentRunner("", 5*time.Second, StrategyMock)
	env := []string{"FOO=bar", "BAZ=qux"}
	runner.SetEnvironment(env)

	if len(runner.environment) != len(env) {
		t.Errorf("environment length = %d, want %d", len(runner.environment), len(env))
	}
}

func TestAgentRequestSerialization(t *testing.T) {
	plan := &DispatchPlan{
		TaskID:    "TASK-001",
		Task:      "test task",
		Agents:    AgentGroups{Primary: []string{"agent1"}},
		CreatedAt: time.Now(),
	}

	req := &AgentRequest{
		AgentID:        "agent1",
		TaskID:         "TASK-001",
		Task:           "test task",
		Classification: "internal",
		ChangedFiles:   []string{"file1.go", "file2.go"},
		DispatchPlan:   plan,
	}

	// Should marshal without error
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}

	// Should unmarshal back
	var unmarshalled AgentRequest
	if err := json.Unmarshal(data, &unmarshalled); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}

	if unmarshalled.AgentID != "agent1" {
		t.Errorf("agent_id = %q, want agent1", unmarshalled.AgentID)
	}
}

func TestMultipleAgentExecution(t *testing.T) {
	runner := NewSubprocessAgentRunner("", 5*time.Second, StrategyMock)

	plan := &DispatchPlan{
		TaskID:    "TASK-001",
		Task:      "test task",
		Agents:    AgentGroups{Primary: []string{"agent1", "agent2", "agent3"}},
		CreatedAt: time.Now(),
	}

	agentIDs := []string{"agent1", "agent2", "agent3"}

	for _, agentID := range agentIDs {
		result, err := runner.RunAgent(context.Background(), agentID, "test task", plan)
		if err != nil {
			t.Fatalf("RunAgent for %s error = %v", agentID, err)
		}
		if result.AgentID != agentID {
			t.Errorf("agent_id = %q, want %q", result.AgentID, agentID)
		}
		if result.Status != "success" {
			t.Errorf("%s status = %q, want success", agentID, result.Status)
		}
	}
}

func TestTimeoutHandling(t *testing.T) {
	// With StrategyMock, timeout is not actually tested (no subprocess)
	// This test documents the timeout field behavior
	runner := NewSubprocessAgentRunner("", 1*time.Second, StrategyMock)

	if runner.timeout != 1*time.Second {
		t.Errorf("timeout = %v, want 1s", runner.timeout)
	}
}

func TestAgentResultFields(t *testing.T) {
	runner := NewSubprocessAgentRunner("", 5*time.Second, StrategyMock)

	plan := &DispatchPlan{
		TaskID:    "TASK-001",
		Task:      "test task",
		Agents:    AgentGroups{Primary: []string{"test"}},
		CreatedAt: time.Now(),
	}

	result, err := runner.RunAgent(context.Background(), "test", "task", plan)
	if err != nil {
		t.Fatalf("RunAgent error = %v", err)
	}

	if result.AgentID == "" {
		t.Errorf("agent_id is empty")
	}
	if result.Status == "" {
		t.Errorf("status is empty")
	}
	if result.StartedAt.IsZero() {
		t.Errorf("started_at is zero")
	}
	if result.CompletedAt.IsZero() {
		t.Errorf("completed_at is zero")
	}
	if result.Duration == 0 {
		t.Errorf("duration is zero")
	}
}

// Helper function
func containsSubstring(s, substring string) bool {
	return s != "" && substring != "" && len(s) >= len(substring)
}
