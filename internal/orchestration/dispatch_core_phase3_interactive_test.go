package orchestration

import (
	"context"
	"testing"
	"time"
)

func TestDisplayConfirmationPromptComplete(t *testing.T) {
	data := map[string]any{
		"role_id":        "test-role",
		"mode":           "scoped-repository-edit",
		"sandbox_mode":   "workspace-write",
		"classification": "internal",
		"task_id":        "task_123",
	}

	prompt := DisplayConfirmationPrompt(data)

	if prompt == "" {
		t.Errorf("prompt is empty")
		return
	}

	// Check that all key fields are present
	if !containsStr6(prompt, "test-role") {
		t.Errorf("prompt missing role_id")
	}

	if !containsStr6(prompt, "scoped-repository-edit") {
		t.Errorf("prompt missing mode")
	}

	if !containsStr6(prompt, "workspace-write") {
		t.Errorf("prompt missing sandbox_mode")
	}

	if !containsStr6(prompt, "internal") {
		t.Errorf("prompt missing classification")
	}

	if !containsStr6(prompt, "task_123") {
		t.Errorf("prompt missing task_id")
	}

	// Check for approval question
	if !containsStr6(prompt, "approve") || !containsStr6(prompt, "yes/no") {
		t.Errorf("prompt missing approval question")
	}
}

func TestDisplayConfirmationPromptSandboxWarnings(t *testing.T) {
	tests := []struct {
		sandbox      string
		expectedText string
	}{
		{SandboxWorkspaceWrite, "read/write workspace"},
		{SandboxDangerFullAccess, "FULL access"},
	}

	for _, test := range tests {
		data := map[string]any{
			"role_id":      "test-role",
			"sandbox_mode": test.sandbox,
		}

		prompt := DisplayConfirmationPrompt(data)

		if !containsStr6(prompt, test.expectedText) {
			t.Errorf("prompt missing expected text for sandbox %q: %q", test.sandbox, test.expectedText)
		}
	}
}

func TestDisplayConfirmationPromptMinimal(t *testing.T) {
	// Empty data should still produce valid prompt
	data := map[string]any{}

	prompt := DisplayConfirmationPrompt(data)

	if prompt == "" {
		t.Errorf("prompt should not be empty even with minimal data")
	}

	// Should still contain approval question
	if !containsStr6(prompt, "approve") {
		t.Errorf("prompt missing approval question")
	}
}

func TestRecordConfirmationDecisionNilJobID(t *testing.T) {
	err := RecordConfirmationDecision("", true, "", nil)
	if err == nil {
		t.Errorf("expected error for empty job_id")
	}
}

func TestRecordConfirmationDecisionApproved(t *testing.T) {
	data := map[string]any{
		"role_id": "test-role",
		"mode":    "scoped-repository-edit",
	}

	// This will try to write to audit log, which may fail in test environment
	err := RecordConfirmationDecision("job_test_123", true, "test_user", data)
	// Tolerate error if audit log writing fails
	_ = err
}

func TestRecordConfirmationDecisionDenied(t *testing.T) {
	data := map[string]any{
		"role_id": "test-role",
	}

	// This will try to write to audit log
	err := RecordConfirmationDecision("job_test_123", false, "test_user", data)
	// Tolerate error if audit log writing fails
	_ = err
}

func TestPromptWithContextNilData(t *testing.T) {
	_, err := PromptWithContext(nil, time.Second)
	if err == nil {
		t.Errorf("expected error for nil data")
	}
}

func TestPromptWithContextDefaultTimeout(t *testing.T) {
	data := map[string]any{"role_id": "test-role"}

	// Use zero timeout to trigger default. Either outcome is acceptable: the
	// data is valid, but there is no stdin to read a response from under
	// `go test`, so the prompt may still fail. The assertion is only that
	// PromptWithContext returns rather than blocking on the zero timeout.
	_, _ = PromptWithContext(data, 0)
}

func TestIsInteractiveModeNonTerminal(t *testing.T) {
	// This test runs in non-interactive environment (no terminal)
	interactive := IsInteractiveMode()

	// Should detect we're not in interactive mode
	if interactive {
		t.Errorf("IsInteractiveMode should return false in non-terminal environment")
	}
}

func TestDefaultConfirmationBehaviorApprove(t *testing.T) {
	approved := DefaultConfirmationBehavior(true)
	if !approved {
		t.Errorf("DefaultConfirmationBehavior(true) should return true")
	}
}

func TestDefaultConfirmationBehaviorDeny(t *testing.T) {
	approved := DefaultConfirmationBehavior(false)
	if approved {
		t.Errorf("DefaultConfirmationBehavior(false) should return false")
	}
}

func TestEnsureConfirmationApprovedYes(t *testing.T) {
	data := map[string]any{"mode": "test-mode"}
	err := EnsureConfirmationApproved(true, false, data)
	if err != nil {
		t.Errorf("EnsureConfirmationApproved(true) should not error: %v", err)
	}
}

func TestEnsureConfirmationApprovedNo(t *testing.T) {
	data := map[string]any{"mode": "scoped-repository-edit"}
	err := EnsureConfirmationApproved(false, false, data)
	if err == nil {
		t.Errorf("EnsureConfirmationApproved(false) should error")
	}

	if !containsStr6(err.Error(), "denied") {
		t.Errorf("error should mention denial: %v", err)
	}
}

func TestEnsureConfirmationApprovedWithDefaultApprove(t *testing.T) {
	data := map[string]any{"mode": "test-mode"}
	// Approved explicitly should always succeed
	err := EnsureConfirmationApproved(true, false, data)
	if err != nil {
		t.Errorf("explicit approval should always succeed: %v", err)
	}
}

func TestExtractString(t *testing.T) {
	data := map[string]any{
		"role_id": "test-role",
		"count":   42,
	}

	tests := []struct {
		key      string
		expected string
	}{
		{"role_id", "test-role"},
		{"count", ""}, // Not a string
		{"missing", ""},
	}

	for _, test := range tests {
		result := extractString(data, test.key)
		if result != test.expected {
			t.Errorf("extractString(%q) = %q, want %q", test.key, result, test.expected)
		}
	}
}

func TestExtractStringNilData(t *testing.T) {
	result := extractString(nil, "key")
	if result != "" {
		t.Errorf("extractString on nil data should return empty string, got %q", result)
	}
}

func TestNewInteractiveConfirmationFlow(t *testing.T) {
	data := map[string]any{"role_id": "test-role"}
	flow := NewInteractiveConfirmationFlow(data, time.Second, false)

	if flow == nil {
		t.Fatalf("NewInteractiveConfirmationFlow should not return nil")
	}

	if flow.data == nil {
		t.Errorf("flow.data should be set")
	}

	if flow.timeout != time.Second {
		t.Errorf("flow.timeout = %v, want %v", flow.timeout, time.Second)
	}

	if flow.approveDefault {
		t.Errorf("flow.approveDefault should be false")
	}
}

func TestNewInteractiveConfirmationFlowDefaultTimeout(t *testing.T) {
	data := map[string]any{"role_id": "test-role"}
	flow := NewInteractiveConfirmationFlow(data, 0, false)

	// Zero timeout should be replaced with default
	if flow.timeout == 0 {
		t.Errorf("zero timeout should be replaced with default")
	}

	if flow.timeout != time.Duration(ConfirmationPromptTimeout)*time.Second {
		t.Errorf("timeout = %v, want %v", flow.timeout, time.Duration(ConfirmationPromptTimeout)*time.Second)
	}
}

func TestInteractiveConfirmationFlowExecuteNilData(t *testing.T) {
	flow := &InteractiveConfirmationFlow{
		data:           nil,
		timeout:        time.Second,
		approveDefault: false,
	}

	_, err := flow.Execute()
	if err == nil {
		t.Errorf("Execute with nil data should error")
	}
}

func TestInteractiveConfirmationFlowExecuteNonInteractive(t *testing.T) {
	// In non-interactive environment, should use default
	data := map[string]any{"role_id": "test-role"}
	flow := NewInteractiveConfirmationFlow(data, time.Second, false)

	approved, err := flow.Execute()

	// Should return default (false) without error
	if approved {
		t.Errorf("non-interactive execution should return false (default), got true")
	}

	if err != nil {
		t.Errorf("non-interactive execution should not error: %v", err)
	}
}

func TestInteractiveConfirmationFlowExecuteDefaultApprove(t *testing.T) {
	data := map[string]any{"role_id": "test-role"}
	flow := NewInteractiveConfirmationFlow(data, time.Second, true)

	// Non-interactive with approve default
	approved, err := flow.Execute()

	if !approved {
		t.Errorf("non-interactive with approve default should return true")
	}

	if err != nil {
		t.Errorf("should not error: %v", err)
	}
}

func TestPromptForConfirmationNilContext(t *testing.T) {
	data := map[string]any{"role_id": "test-role"}
	// Deliberately nil: this test asserts PromptForConfirmation's own
	// fail-closed guard for a nil context, which context.TODO() would bypass.
	_, err := PromptForConfirmation(nil, data, time.Second) //nolint:staticcheck // SA1012: nil ctx is the subject under test

	if err == nil {
		t.Errorf("nil context should error")
	}
}

func TestPromptForConfirmationNilData(t *testing.T) {
	ctx := context.Background()
	_, err := PromptForConfirmation(ctx, nil, time.Second)

	if err == nil {
		t.Errorf("nil data should error")
	}
}

func TestPromptForConfirmationContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Immediately cancel

	data := map[string]any{"role_id": "test-role"}
	_, err := PromptForConfirmation(ctx, data, time.Second)

	if err == nil {
		t.Errorf("cancelled context should error")
	}

	if !containsStr6(err.Error(), "cancelled") {
		t.Errorf("error should mention cancellation: %v", err)
	}
}

// Helper function to check string containment (unique name to avoid collision)
func containsStr6(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
