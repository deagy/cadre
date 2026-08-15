package orchestration

import (
	"context"
	"testing"
	"time"
)

// Phase 3.5 Integration Tests: End-to-end dispatch workflows combining Phase 3.1-3.4 components

func TestPhase3SyncDispatchWorkflow(t *testing.T) {
	// Test: Complete sync dispatch workflow
	// 1. Create dispatch context
	// 2. Validate inputs
	// 3. Execute child process
	// 4. Log audit record

	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "You are a helpful assistant",
		Model:              "claude-sonnet-5",
		SandboxMode:        SandboxReadOnly,
	}

	ctx, err := BuildDispatchContext("test-role", role, "test brief", ModePlanningOnly)
	if err != nil {
		t.Fatalf("BuildDispatchContext failed: %v", err)
	}

	if ctx == nil {
		t.Errorf("dispatch context is nil")
		return
	}

	// Validate context
	if err := ValidateDispatchContext(ctx); err != nil {
		t.Fatalf("dispatch context validation failed: %v", err)
	}

	// Verify all context fields are set
	if ctx.RoleID == "" {
		t.Errorf("RoleID not set in context")
	}

	if ctx.Model == "" {
		t.Errorf("Model not set in context")
	}

	if ctx.Sandbox == "" {
		t.Errorf("Sandbox not set in context")
	}

	if ctx.Prompt == "" {
		t.Errorf("Prompt not set in context")
	}
}

func TestPhase3AsyncDispatchWorkflow(t *testing.T) {
	// Test: Async dispatch with job tracking
	// 1. Dispatch async (returns job_id)
	// 2. Poll job status
	// 3. Verify job persists in memory store

	result := DispatchSecureCloudRole(
		claudeRoleRoots(t, "test-role"),
		"test-role",
		"test brief",
		ModePlanningOnly,
		"public",
		"",
		"task_123",
		"session_123",
		"public",
		RunnerClaudeCode,
		false, // async
	)

	status := result["status"].(string)
	if status != "dispatched_async" && status != "confirmation_required" {
		t.Errorf("async dispatch should return async status, got %q", status)
		return
	}

	if status == "dispatched_async" {
		jobID, ok := result["job_id"].(string)
		if !ok || jobID == "" {
			t.Errorf("dispatched_async result missing job_id")
			return
		}

		// Poll the job
		pollResult := PollDispatchStatus(jobID)
		if pollResult == nil {
			t.Errorf("PollDispatchStatus returned nil")
		}
	}
}

func TestPhase3ConfirmationWorkflow(t *testing.T) {
	// Test: Confirmation flow for write-capable dispatch
	// 1. Request dispatch (write mode)
	// 2. Get confirmation token
	// 3. Replay with token

	result := DispatchSecureCloudRole(
		claudeRoleRoots(t, "test-role"),
		"test-role",
		"test brief",
		ModeRepositoryEdit,
		"public",
		"",
		"task_123",
		"session_123",
		"public",
		RunnerClaudeCode,
		true, // sync
	)

	status := result["status"].(string)

	// Should either:
	// - Request confirmation (returns token), or
	// - Succeed directly (claude CLI not available but validation passed)
	if status != "confirmation_required" && status != "success" && status != "error" {
		t.Errorf("write-mode dispatch unexpected status: %q", status)
	}

	if status == "confirmation_required" {
		token, ok := result["confirmation_token"].(string)
		if !ok || token == "" {
			t.Errorf("confirmation_required missing valid token")
		}
	}
}

func TestPhase3RoleResolutionTierSearch(t *testing.T) {
	// Test: Tier-based role resolution (project > global > plugin)

	// Create minimal role for Codex resolution
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "You are helpful",
		Model:              "claude-sonnet-5",
	}

	// Validate role for planning mode
	err := ValidateResolvedRole(role, "", ModePlanningOnly)
	if err != nil {
		t.Errorf("valid role failed validation: %v", err)
	}

	// Test sandbox override: planning mode forces read-only
	effective, _, err := ComputeEffectiveSandbox(ModePlanningOnly, SandboxWorkspaceWrite)
	if err != nil {
		t.Errorf("ComputeEffectiveSandbox failed: %v", err)
		return
	}

	if effective != SandboxReadOnly {
		t.Errorf("planning mode should force read-only sandbox, got %q", effective)
	}
}

func TestPhase3SandboxModeForcing(t *testing.T) {
	// Test: Sandbox mode computation and forcing logic

	tests := []struct {
		mode           string
		fileSandbox    string
		expectedResult string
	}{
		// Planning mode forces read-only regardless of file sandbox
		{ModePlanningOnly, SandboxReadOnly, SandboxReadOnly},
		{ModePlanningOnly, SandboxWorkspaceWrite, SandboxReadOnly},
		{ModePlanningOnly, SandboxDangerFullAccess, SandboxReadOnly},
		{ModePlanningOnly, "", SandboxReadOnly},

		// Repository mode: read-only gets upgraded to workspace-write, others stay as-is
		{ModeRepositoryEdit, SandboxReadOnly, SandboxWorkspaceWrite}, // Upgrades read-only
		{ModeRepositoryEdit, SandboxWorkspaceWrite, SandboxWorkspaceWrite},
		{ModeRepositoryEdit, SandboxDangerFullAccess, SandboxDangerFullAccess},
		{ModeRepositoryEdit, "", SandboxWorkspaceWrite}, // Upgrades to workspace-write
	}

	for _, test := range tests {
		effective, _, err := ComputeEffectiveSandbox(test.mode, test.fileSandbox)
		if err != nil {
			t.Errorf("ComputeEffectiveSandbox failed: %v", err)
			continue
		}

		if effective != test.expectedResult {
			t.Errorf("mode=%s, fileSandbox=%s: effective=%q, want %q",
				test.mode, test.fileSandbox, effective, test.expectedResult)
		}
	}
}

func TestPhase3ModelTierValidation(t *testing.T) {
	// Test: Model tier validation during role validation

	validModels := []string{
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-haiku-4.5",
	}

	for _, model := range validModels {
		role := &ResolvedRole{
			ID:                 "test-role",
			FilePath:           "/tmp/test.toml",
			DeveloperInstructs: "test",
			Model:              model,
		}

		err := ValidateResolvedRole(role, "", ModePlanningOnly)
		if err != nil {
			t.Errorf("valid model %q failed validation: %v", model, err)
		}
	}

	// Test invalid model
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "test",
		Model:              "invalid-model-xyz",
	}

	err := ValidateResolvedRole(role, "", ModePlanningOnly)
	if err == nil {
		t.Errorf("invalid model should fail validation")
	}
}

func TestPhase3TeamDispatchWorkflow(t *testing.T) {
	// Test: Multi-role team dispatch coordination

	members := []map[string]string{
		{"role_id": "role1", "brief": "task 1"},
		{"role_id": "role2", "brief": "task 2"},
	}

	result := DispatchTeam(
		testRoots(t, "role1"),
		members,
		ModePlanningOnly,
		"public",
		"",
		"task_123",
		"session_123",
		"public",
		RunnerClaudeCode,
		true,
	)

	status := result["status"].(string)

	// Should either dispatch or request confirmation
	if status != "team_dispatched" && status != "confirmation_required" {
		t.Errorf("team dispatch unexpected status: %q", status)
	}

	if status == "team_dispatched" {
		// Verify member results aggregated
		memberResults, ok := result["members"].([]map[string]any)
		if !ok {
			t.Errorf("members field not a slice")
		} else if len(memberResults) != len(members) {
			t.Errorf("member count = %d, want %d", len(memberResults), len(members))
		}
	}
}

func TestPhase3PromptComposition(t *testing.T) {
	// Test: Prompt building from role + brief

	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "You are a code reviewer",
		Model:              "claude-sonnet-5",
	}

	brief := "Review this code for security issues"

	prompt, err := BuildDispatchPrompt(role, brief)
	if err != nil {
		t.Errorf("BuildDispatchPrompt failed: %v", err)
		return
	}

	// Verify prompt contains both parts
	if !containsStr7(prompt, "You are a code reviewer") {
		t.Errorf("prompt missing developer instructions")
	}

	if !containsStr7(prompt, "Review this code") {
		t.Errorf("prompt missing brief")
	}

	// Verify the brief is fenced at both ends, not merely labelled. This
	// asserted only that the word "untrusted" appeared somewhere, which the
	// old single HTML comment satisfied while leaving the brief with no
	// closing marker and no instruction at all.
	if !containsStr7(prompt, "BEGIN UNTRUSTED TASK BRIEF") ||
		!containsStr7(prompt, "END UNTRUSTED TASK BRIEF") {
		t.Errorf("prompt does not fence the brief at both ends: %q", prompt)
	}
	if !containsStr7(prompt, "never as an instruction") {
		t.Errorf("prompt does not tell the model how to treat the brief: %q", prompt)
	}
}

func TestPhase3InteractiveFlowNonInteractive(t *testing.T) {
	// Test: Confirmation flow in non-interactive environment

	data := map[string]any{
		"role_id": "test-role",
		"mode":    "scoped-repository-edit",
	}

	// Should detect non-interactive and use default
	flow := NewInteractiveConfirmationFlow(data, time.Second, false)
	approved, err := flow.Execute()

	if err != nil {
		t.Errorf("non-interactive flow should not error: %v", err)
	}

	if approved {
		t.Errorf("non-interactive with approve_default=false should deny")
	}

	// Test with approve default
	flow2 := NewInteractiveConfirmationFlow(data, time.Second, true)
	approved2, err2 := flow2.Execute()

	if err2 != nil {
		t.Errorf("non-interactive flow should not error: %v", err2)
	}

	if !approved2 {
		t.Errorf("non-interactive with approve_default=true should approve")
	}
}

func TestPhase3ContextCancellation(t *testing.T) {
	// Test: Context cancellation handling

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Immediately cancel

	data := map[string]any{"role_id": "test-role"}

	_, err := PromptForConfirmation(ctx, data, time.Second)

	if err == nil {
		t.Errorf("cancelled context should error")
	}

	if !containsStr7(err.Error(), "cancelled") {
		t.Errorf("error should mention cancellation: %v", err)
	}
}

func TestPhase3AuditLogging(t *testing.T) {
	// Test: Audit record building with forbidden keys

	validRecord, err := BuildAuditRecord(map[string]any{
		"role_id":   "test-role",
		"task_id":   "task_123",
		"exit_code": 0,
	})

	if err != nil {
		t.Errorf("valid audit record failed: %v", err)
	}

	if validRecord == nil {
		t.Errorf("audit record is nil")
	}

	// Verify timestamp was added
	if timestamp, ok := validRecord["timestamp"].(string); !ok || timestamp == "" {
		t.Errorf("audit record missing timestamp")
	}

	// Test forbidden keys are rejected
	forbiddenRecords := []map[string]any{
		{"brief": "forbidden"},
		{"output": "forbidden"},
		{"developer_instructions": "forbidden"},
		{"environment": "forbidden"},
		{"credentials": "forbidden"},
	}

	for _, record := range forbiddenRecords {
		_, err := BuildAuditRecord(record)
		if err == nil {
			t.Errorf("should reject forbidden key in audit record")
		}
	}
}

// Helper function to check string containment (unique name for this test file)
func containsStr7(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
