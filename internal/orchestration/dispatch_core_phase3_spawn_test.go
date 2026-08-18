package orchestration

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSpawnClaudeCodeChildEmptyPrompt(t *testing.T) {
	result := SpawnClaudeCodeChild("", "claude-sonnet-5", "", nil, 10.0)

	status := result["status"].(string)
	if status != "error" {
		t.Errorf("empty prompt should return error status, got %q", status)
	}
}

func TestSpawnClaudeCodeChildEmptyModel(t *testing.T) {
	result := SpawnClaudeCodeChild("test prompt", "", "", nil, 10.0)

	status := result["status"].(string)
	if status != "error" {
		t.Errorf("empty model should return error status, got %q", status)
	}
}

func TestSpawnClaudeCodeChildInvalidModel(t *testing.T) {
	result := SpawnClaudeCodeChild("test prompt", "invalid-model-xyz", SandboxReadOnly, nil, 10.0)

	status := result["status"].(string)
	if status != "error" {
		t.Errorf("invalid model should return error status, got %q", status)
	}

	if reason, ok := result["reason"].(string); !ok || reason == "" {
		t.Errorf("error result missing reason")
	}
}

func TestSpawnClaudeCodeChildInvalidSandbox(t *testing.T) {
	result := SpawnClaudeCodeChild("test prompt", "claude-sonnet-5", "invalid-sandbox", nil, 10.0)

	// "denied", not "error": an unrecognised sandbox mode is a refusal on the
	// request's merits, not a fault in this process. The distinction is what
	// tells a caller to fix the role file rather than report a bug.
	status := result["status"].(string)
	if status != "denied" {
		t.Errorf("invalid sandbox should be denied, got %q", status)
	}
	// And it must not have guessed a permission mode -- the only direction a
	// guess can go here is wider.
	if reason, _ := result["reason"].(string); !strings.Contains(reason, "invalid-sandbox") {
		t.Errorf("the refusal should name the sandbox mode it rejected: %q", reason)
	}
}

func TestSpawnClaudeCodeChildValidModels(t *testing.T) {
	models := []string{
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-haiku-4.5",
	}

	for _, model := range models {
		result := SpawnClaudeCodeChild("test prompt", model, SandboxReadOnly, nil, 10.0)

		// Should not error on validation; actual execution may fail (claude CLI not available)
		status := result["status"].(string)
		if status == "error" {
			// Check if error is about missing claude CLI or validation
			if reason, ok := result["reason"].(string); ok && reason != "" {
				// If it's a validation error (contains "invalid"), that's a test failure
				if containsStr5(reason, "invalid") {
					t.Errorf("model %q should be valid, got error: %s", model, reason)
				}
			}
		}
	}
}

func TestSpawnCodexChildUnavailable(t *testing.T) {
	// "Unavailable" means no codex binary resolves. That has to be arranged,
	// not assumed: this asserted it by leaving the setting unset and relying on
	// codex being absent from PATH, so it passed on CI and failed on any
	// developer machine with the CLI installed -- where it spawned the real
	// thing and got "failed" instead.
	noRunnerBinary(t)
	result := SpawnCodexChild("test prompt", "claude-sonnet-5", nil, 10.0)

	status := result["status"].(string)
	if status != "unavailable" {
		t.Errorf("Codex runner should return unavailable status, got %q", status)
	}
}

func TestExecuteDispatchChildNilContext(t *testing.T) {
	result := ExecuteDispatchChild(nil, RunnerClaudeCode, nil, 10.0)

	status := result["status"].(string)
	if status != "error" {
		t.Errorf("nil context should return error status, got %q", status)
	}
}

func TestExecuteDispatchChildInvalidContext(t *testing.T) {
	ctx := &DispatchContext{
		RoleID: "", // Invalid - empty role ID
		Model:  "claude-sonnet-5",
	}

	result := ExecuteDispatchChild(ctx, RunnerClaudeCode, nil, 10.0)

	status := result["status"].(string)
	if status != "error" {
		t.Errorf("invalid context should return error status, got %q", status)
	}
}

func TestExecuteDispatchChildDefaultRunner(t *testing.T) {
	// This one only checks that validation did not reject the model or
	// sandbox, so it does not care what the child is -- but without a stub it
	// launches whatever `claude` resolves to, which is a real agent session.
	stubRunner(t)
	ctx := &DispatchContext{
		RoleID:  "test-role",
		Model:   "claude-sonnet-5",
		Sandbox: SandboxReadOnly,
		Prompt:  "test prompt",
	}

	// Without explicit runner, should default to Claude Code
	result := ExecuteDispatchChild(ctx, "", nil, 10.0)

	// Should attempt Claude Code execution (may fail due to CLI not available)
	status := result["status"].(string)
	if status == "error" {
		if reason, ok := result["reason"].(string); ok && reason != "" {
			// If error is about invalid model/sandbox, that's bad
			if containsStr5(reason, "invalid") {
				t.Errorf("default runner validation failed: %s", reason)
			}
		}
	}
}

func TestExecuteDispatchChildCodexRunner(t *testing.T) {
	// Same as above: the absence of a binary is the precondition, so it is
	// arranged rather than inherited from whatever the machine has installed.
	noRunnerBinary(t)
	ctx := &DispatchContext{
		RoleID:  "test-role",
		Model:   "claude-sonnet-5",
		Sandbox: SandboxReadOnly,
		Prompt:  "test prompt",
	}

	result := ExecuteDispatchChild(ctx, RunnerCodex, nil, 10.0)

	// Codex runner should be unavailable
	status := result["status"].(string)
	if status != "unavailable" {
		t.Errorf("Codex runner should return unavailable, got %q", status)
	}
}

func TestExecuteDispatchChildAPIRunner(t *testing.T) {
	ctx := &DispatchContext{
		RoleID:  "test-role",
		Model:   "claude-sonnet-5",
		Sandbox: SandboxReadOnly,
		Prompt:  "test prompt",
	}

	result := ExecuteDispatchChild(ctx, RunnerAPI, nil, 10.0)

	// API runner should be unavailable
	status := result["status"].(string)
	if status != "unavailable" {
		t.Errorf("API runner should return unavailable, got %q", status)
	}
}

func TestExecuteDispatchChildUnknownRunner(t *testing.T) {
	ctx := &DispatchContext{
		RoleID:  "test-role",
		Model:   "claude-sonnet-5",
		Sandbox: SandboxReadOnly,
		Prompt:  "test prompt",
	}

	result := ExecuteDispatchChild(ctx, "unknown-runner", nil, 10.0)

	status := result["status"].(string)
	if status != "error" {
		t.Errorf("unknown runner should return error status, got %q", status)
	}

	if reason, ok := result["reason"].(string); !ok || reason == "" {
		t.Errorf("error result missing reason for unknown runner")
	}
}

func TestValidateClaudeCodeExecutionValidModels(t *testing.T) {
	models := []string{
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-haiku-4.5",
		"claude-opus-4.1",
		"claude-sonnet-4",
		"claude-haiku-4",
	}

	for _, model := range models {
		err := validateClaudeCodeExecution(model, "")
		if err != nil {
			t.Errorf("valid model %q failed validation: %v", model, err)
		}
	}
}

func TestValidateClaudeCodeExecutionInvalidModel(t *testing.T) {
	err := validateClaudeCodeExecution("invalid-model", "")
	if err == nil {
		t.Errorf("invalid model should fail validation")
	}
}

func TestValidateClaudeCodeExecutionValidSandbox(t *testing.T) {
	sandboxes := []string{
		SandboxReadOnly,
		SandboxWorkspaceWrite,
		SandboxDangerFullAccess,
	}

	for _, sandbox := range sandboxes {
		err := validateClaudeCodeExecution("claude-sonnet-5", sandbox)
		if err != nil {
			t.Errorf("valid sandbox %q failed validation: %v", sandbox, err)
		}
	}
}

func TestValidateClaudeCodeExecutionInvalidSandbox(t *testing.T) {
	err := validateClaudeCodeExecution("claude-sonnet-5", "invalid-sandbox")
	if err == nil {
		t.Errorf("invalid sandbox should fail validation")
	}
}

func TestValidateClaudeCodeExecutionEmptySandbox(t *testing.T) {
	// Empty sandbox mode should be allowed (uses default)
	err := validateClaudeCodeExecution("claude-sonnet-5", "")
	if err != nil {
		t.Errorf("empty sandbox mode should be valid: %v", err)
	}
}

func TestParseCommandOutputSuccess(t *testing.T) {
	output := []byte("test output\n")

	result := parseCommandOutput(nil, output, nil)

	if status, ok := result["status"].(string); !ok || status != "success" {
		t.Errorf("status = %v, want 'success'", result["status"])
	}

	if exitCode, ok := result["exit_code"].(int); !ok || exitCode != 0 {
		t.Errorf("exit_code = %v, want 0", result["exit_code"])
	}

	if outputStr, ok := result["output"].(string); !ok || outputStr != "test output\n" {
		t.Errorf("output mismatch: %q", result["output"])
	}
}

func TestParseCommandOutputError(t *testing.T) {
	output := []byte("error output")
	err := exec.Command("false").Run() // Will fail

	result := parseCommandOutput(nil, output, err)

	if status, ok := result["status"].(string); !ok || status == "success" {
		t.Errorf("error should not have success status, got %q", status)
	}

	if errStr, ok := result["error"].(string); !ok || errStr == "" {
		t.Errorf("result missing error field")
	}
}

func TestEnvironmentMapToSlice(t *testing.T) {
	env := map[string]string{
		"PATH": "/usr/bin",
		"HOME": "/home/user",
	}

	slice := environmentMapToSlice(env)

	if slice == nil {
		t.Errorf("slice is nil")
		return
	}

	if len(slice) != 2 {
		t.Errorf("slice length = %d, want 2", len(slice))
	}

	// Check both env vars are in the slice
	found := 0
	for _, s := range slice {
		if containsStr5(s, "PATH=/usr/bin") || containsStr5(s, "HOME=/home/user") {
			found++
		}
	}

	if found != 2 {
		t.Errorf("expected both env vars in slice, found %d", found)
	}
}

func TestEnvironmentMapToSliceNil(t *testing.T) {
	slice := environmentMapToSlice(nil)
	if slice != nil {
		t.Errorf("nil map should return nil slice")
	}
}

func TestEnvironmentMapToSliceEmpty(t *testing.T) {
	env := make(map[string]string)
	slice := environmentMapToSlice(env)

	if slice == nil {
		t.Errorf("empty map should return empty slice, not nil")
	}

	if len(slice) != 0 {
		t.Errorf("slice length = %d, want 0", len(slice))
	}
}

// Helper function to check string containment
func containsStr5(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
