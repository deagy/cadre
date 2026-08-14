package orchestration

import (
	"context"
	"testing"
	"time"
)

// MockAPIProvider is a mock implementation of APIProvider for testing.
type MockAPIProvider struct {
	name     string
	response string
	err      error
}

func (m *MockAPIProvider) Name() string {
	return m.name
}

func (m *MockAPIProvider) CallAgent(ctx context.Context, request *AgentRequest, modelID string, maxTokens int, temperature float32) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func TestNewAPIAgentRunner(t *testing.T) {
	provider := &MockAPIProvider{name: "test"}
	runner := NewAPIAgentRunner(provider, "test-model", 5*time.Second)

	if runner == nil {
		t.Fatalf("NewAPIAgentRunner returned nil")
	}
	if runner.modelID != "test-model" {
		t.Errorf("modelID = %q, want test-model", runner.modelID)
	}
}

func TestAPIAgentRunnerExecuteSuccess(t *testing.T) {
	jsonResp := `{
		"agent_id": "test-agent",
		"status": "success",
		"findings": ["finding1", "finding2"],
		"output": "test output",
		"exit_code": 0
	}`

	provider := &MockAPIProvider{
		name:     "test",
		response: jsonResp,
	}
	runner := NewAPIAgentRunner(provider, "test-model", 5*time.Second)

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
	if len(result.Findings) != 2 {
		t.Errorf("findings length = %d, want 2", len(result.Findings))
	}
}

func TestAPIAgentRunnerExecuteTextResponse(t *testing.T) {
	provider := &MockAPIProvider{
		name:     "test",
		response: "This is plain text output from the agent",
	}
	runner := NewAPIAgentRunner(provider, "test-model", 5*time.Second)

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
	if result.Output != "This is plain text output from the agent" {
		t.Errorf("output = %q, unexpected", result.Output)
	}
}

func TestAPIAgentRunnerExecuteError(t *testing.T) {
	provider := &MockAPIProvider{
		name: "test",
		err:  errorString("API call failed"),
	}
	runner := NewAPIAgentRunner(provider, "test-model", 5*time.Second)

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

	if result.Status != "failed" {
		t.Errorf("status = %q, want failed", result.Status)
	}
	if result.ExitCode != 1 {
		t.Errorf("exit_code = %d, want 1", result.ExitCode)
	}
}

func TestAPIAgentRunnerSetMaxTokens(t *testing.T) {
	provider := &MockAPIProvider{name: "test", response: "ok"}
	runner := NewAPIAgentRunner(provider, "test-model", 5*time.Second)

	runner.SetMaxTokens(8192)
	if runner.maxTokens != 8192 {
		t.Errorf("maxTokens = %d, want 8192", runner.maxTokens)
	}
}

func TestAPIAgentRunnerSetTemperature(t *testing.T) {
	provider := &MockAPIProvider{name: "test", response: "ok"}
	runner := NewAPIAgentRunner(provider, "test-model", 5*time.Second)

	runner.SetTemperature(0.5)
	if runner.temperature != 0.5 {
		t.Errorf("temperature = %f, want 0.5", runner.temperature)
	}
}

func TestClaudeProviderName(t *testing.T) {
	provider := &ClaudeProvider{}
	if provider.Name() != "claude" {
		t.Errorf("Name() = %q, want claude", provider.Name())
	}
}

func TestOpenAIProviderName(t *testing.T) {
	provider := &OpenAIProvider{}
	if provider.Name() != "openai" {
		t.Errorf("Name() = %q, want openai", provider.Name())
	}
}

func TestClaudeProviderBuildSystemPrompt(t *testing.T) {
	provider := &ClaudeProvider{}
	req := &AgentRequest{
		AgentID:        "code-reviewer",
		TaskID:         "TASK-001",
		Classification: "internal",
		Task:           "Review the code changes",
	}

	prompt := provider.buildSystemPrompt(req)
	if prompt == "" {
		t.Errorf("buildSystemPrompt returned empty string")
	}
	if !containsSubstring(prompt, "code-reviewer") {
		t.Errorf("prompt should contain agent role")
	}
	if !containsSubstring(prompt, "TASK-001") {
		t.Errorf("prompt should contain task ID")
	}
}

func TestOpenAIProviderBuildSystemMessage(t *testing.T) {
	provider := &OpenAIProvider{}
	req := &AgentRequest{
		AgentID:        "security-reviewer",
		TaskID:         "TASK-002",
		Classification: "high",
		Task:           "Review for security issues",
	}

	message := provider.buildSystemMessage(req)
	if message == "" {
		t.Errorf("buildSystemMessage returned empty string")
	}
	if !containsSubstring(message, "security-reviewer") {
		t.Errorf("message should contain agent role")
	}
	if !containsSubstring(message, "TASK-002") {
		t.Errorf("message should contain task ID")
	}
}

// errorString is a simple error type for testing
type errorString string

func (e errorString) Error() string {
	return string(e)
}
