package orchestration

import (
	"errors"
	"testing"
)

func TestResolveRoleForDispatchInvalidRoleID(t *testing.T) {
	tests := []string{"InvalidRole", "role_name", "role@123"}
	for _, roleID := range tests {
		_, err := ResolveRoleForDispatch(roleID, RunnerCodex, "/tmp/project", "/tmp/global", "/tmp/plugin", ModePlanningOnly)
		if err == nil {
			t.Errorf("ResolveRoleForDispatch(%q) should fail with invalid role ID", roleID)
		}
	}
}

func TestResolveRoleForDispatchDefaultRunner(t *testing.T) {
	// Without explicit runner, should default to Codex
	_, err := ResolveRoleForDispatch("test-role", "", "/tmp/project", "/tmp/global", "/tmp/plugin", ModePlanningOnly)
	// Error is expected because files don't exist, but it should try Codex resolver
	if err == nil {
		t.Errorf("expected error for non-existent role files")
	}
	// Should be an "unavailable" error, not a validation error
	var unavailable *DispatchUnavailable
	if !errors.As(err, &unavailable) {
		t.Errorf("expected DispatchUnavailable error for missing role, got %T", err)
	}
}

func TestResolveRoleForDispatchUnknownRunner(t *testing.T) {
	_, err := ResolveRoleForDispatch("test-role", "unknown-runner", "/tmp/project", "/tmp/global", "/tmp/plugin", ModePlanningOnly)
	if err == nil {
		t.Errorf("expected error for unknown runner")
	}
	var denied *DispatchDenied
	if !errors.As(err, &denied) {
		t.Errorf("expected DispatchDenied error, got %T", err)
	}
}

func TestResolveRoleForDispatchAPIRunner(t *testing.T) {
	_, err := ResolveRoleForDispatch("test-role", RunnerAPI, "/tmp/project", "/tmp/global", "/tmp/plugin", ModePlanningOnly)
	if err == nil {
		t.Errorf("expected error for unimplemented API runner")
	}
	var unavailable *DispatchUnavailable
	if !errors.As(err, &unavailable) {
		t.Errorf("expected DispatchUnavailable error, got %T", err)
	}
}

func TestValidateResolvedRoleNil(t *testing.T) {
	err := ValidateResolvedRole(nil, "", ModePlanningOnly)
	if err == nil {
		t.Errorf("expected error for nil role")
	}
}

func TestValidateResolvedRoleEmpty(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test-role.toml",
		DeveloperInstructs: "",
		Model:              "claude-sonnet-5",
	}

	err := ValidateResolvedRole(role, "", ModePlanningOnly)
	if err == nil {
		t.Errorf("expected error for empty developer_instructions")
	}
}

func TestValidateResolvedRoleNoModel(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test-role.toml",
		DeveloperInstructs: "You are a helpful assistant",
		Model:              "",
	}

	err := ValidateResolvedRole(role, "", ModePlanningOnly)
	if err == nil {
		t.Errorf("expected error for missing model")
	}
}

func TestValidateResolvedRoleInvalidModel(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test-role.toml",
		DeveloperInstructs: "You are a helpful assistant",
		Model:              "invalid-model-xyz",
	}

	err := ValidateResolvedRole(role, "", ModePlanningOnly)
	if err == nil {
		t.Errorf("expected error for invalid model")
	}
}

func TestValidateResolvedRoleValidModels(t *testing.T) {
	models := []string{
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-haiku-4.5",
		"claude-opus-4.1",
		"claude-sonnet-4",
		"claude-haiku-4",
	}

	for _, model := range models {
		role := &ResolvedRole{
			ID:                 "test-role",
			FilePath:           "/tmp/test.toml",
			DeveloperInstructs: "You are a helpful assistant",
			Model:              model,
		}

		err := ValidateResolvedRole(role, "", ModePlanningOnly)
		if err != nil {
			t.Errorf("ValidateResolvedRole with model %q failed: %v", model, err)
		}
	}
}

func TestValidateResolvedRoleInvalidSandboxMode(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "You are a helpful assistant",
		Model:              "claude-sonnet-5",
		SandboxMode:        "invalid-sandbox-xyz",
	}

	err := ValidateResolvedRole(role, "", ModePlanningOnly)
	if err == nil {
		t.Errorf("expected error for invalid sandbox_mode")
	}
}

func TestValidateResolvedRoleWriteInPlanningMode(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "You are a helpful assistant",
		Model:              "claude-sonnet-5",
		SandboxMode:        SandboxWorkspaceWrite,
	}

	// Write-capable sandbox in planning mode is allowed (forced to read-only)
	// ComputeEffectiveSandbox handles the forcing, not ValidateResolvedRole
	err := ValidateResolvedRole(role, "", ModePlanningOnly)
	if err != nil {
		t.Errorf("ValidateResolvedRole should allow write sandbox in planning mode (forced to read-only): %v", err)
	}
}

func TestValidateResolvedRoleWriteInRepositoryMode(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "You are a helpful assistant",
		Model:              "claude-sonnet-5",
		SandboxMode:        SandboxWorkspaceWrite,
	}

	// Write-capable sandbox in repository mode is allowed
	err := ValidateResolvedRole(role, "", ModeRepositoryEdit)
	if err != nil {
		t.Errorf("ValidateResolvedRole with write sandbox in repo mode failed: %v", err)
	}
}

func TestBuildDispatchPromptNilRole(t *testing.T) {
	_, err := BuildDispatchPrompt(nil, "test brief")
	if err == nil {
		t.Errorf("expected error for nil role")
	}
}

func TestBuildDispatchPromptEmptyInstructions(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "",
	}

	_, err := BuildDispatchPrompt(role, "test brief")
	if err == nil {
		t.Errorf("expected error for empty instructions")
	}
}

func TestBuildDispatchPromptEmptyBrief(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "You are a helpful assistant",
	}

	_, err := BuildDispatchPrompt(role, "")
	if err == nil {
		t.Errorf("expected error for empty brief")
	}
}

func TestBuildDispatchPromptBriefTooLarge(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "You are a helpful assistant",
	}

	// Create brief exceeding MaxBriefBytes
	largeBrief := ""
	for i := 0; i < MaxBriefBytes+1000; i++ {
		largeBrief += "x"
	}

	_, err := BuildDispatchPrompt(role, largeBrief)
	if err == nil {
		t.Errorf("expected error for brief exceeding max size")
	}
}

func TestBuildDispatchPromptSuccess(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "You are a helpful assistant",
	}

	prompt, err := BuildDispatchPrompt(role, "test brief")
	if err != nil {
		t.Errorf("BuildDispatchPrompt failed: %v", err)
		return
	}

	if prompt == "" {
		t.Errorf("prompt is empty")
	}

	if !containsStr3(prompt, "You are a helpful assistant") {
		t.Errorf("prompt missing developer instructions")
	}

	if !containsStr3(prompt, "test brief") {
		t.Errorf("prompt missing brief")
	}
}

func TestExtractModelTier(t *testing.T) {
	tests := []struct {
		model string
		tier  string
	}{
		{"claude-opus-5", "opus"},
		{"claude-sonnet-5", "sonnet"},
		{"claude-haiku-4.5", "haiku"},
		{"", "sonnet"}, // default
	}

	for _, test := range tests {
		role := &ResolvedRole{Model: test.model}
		tier := ExtractModelTierFromRole(role)
		if tier != test.tier {
			t.Errorf("ExtractModelTier(%q) = %q, want %q", test.model, tier, test.tier)
		}
	}
}

func TestBuildDispatchContext(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "You are a helpful assistant",
		Model:              "claude-sonnet-5",
		SandboxMode:        SandboxReadOnly,
	}

	ctx, err := BuildDispatchContext("test-role", role, "test brief", ModePlanningOnly)
	if err != nil {
		t.Errorf("BuildDispatchContext failed: %v", err)
		return
	}

	if ctx.RoleID != "test-role" {
		t.Errorf("RoleID = %q, want 'test-role'", ctx.RoleID)
	}

	if ctx.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q, want 'claude-sonnet-5'", ctx.Model)
	}

	if ctx.ModelTier != "sonnet" {
		t.Errorf("ModelTier = %q, want 'sonnet'", ctx.ModelTier)
	}

	if ctx.Sandbox != SandboxReadOnly {
		t.Errorf("Sandbox = %q, want %q", ctx.Sandbox, SandboxReadOnly)
	}

	if ctx.IsWriteCapable {
		t.Errorf("IsWriteCapable should be false for read-only sandbox")
	}
}

func TestBuildDispatchContextWriteCapable(t *testing.T) {
	role := &ResolvedRole{
		ID:                 "test-role",
		FilePath:           "/tmp/test.toml",
		DeveloperInstructs: "You are a helpful assistant",
		Model:              "claude-sonnet-5",
		SandboxMode:        SandboxWorkspaceWrite,
	}

	ctx, err := BuildDispatchContext("test-role", role, "test brief", ModeRepositoryEdit)
	if err != nil {
		t.Errorf("BuildDispatchContext failed: %v", err)
		return
	}

	if !ctx.IsWriteCapable {
		t.Errorf("IsWriteCapable should be true for workspace-write sandbox in repo mode")
	}
}

func TestValidateDispatchContextNil(t *testing.T) {
	err := ValidateDispatchContext(nil)
	if err == nil {
		t.Errorf("expected error for nil context")
	}
}

func TestValidateDispatchContextValid(t *testing.T) {
	ctx := &DispatchContext{
		RoleID:    "test-role",
		Model:     "claude-sonnet-5",
		Sandbox:   SandboxReadOnly,
		Prompt:    "test prompt",
		ModelTier: "sonnet",
	}

	err := ValidateDispatchContext(ctx)
	if err != nil {
		t.Errorf("ValidateDispatchContext failed: %v", err)
	}
}

func TestEffectiveSandboxForDispatch(t *testing.T) {
	tests := []struct {
		mode     string
		fileSand string
		expected string
	}{
		{ModePlanningOnly, "", SandboxReadOnly},
		{ModePlanningOnly, SandboxWorkspaceWrite, SandboxReadOnly},
		{ModeRepositoryEdit, "", SandboxWorkspaceWrite},
		{ModeRepositoryEdit, SandboxWorkspaceWrite, SandboxWorkspaceWrite},
	}

	for _, test := range tests {
		role := &ResolvedRole{
			ID:                 "test-role",
			FilePath:           "/tmp/test.toml",
			DeveloperInstructs: "test",
			Model:              "claude-sonnet-5",
			SandboxMode:        test.fileSand,
		}

		sandbox, err := EffectiveSandboxForDispatch(role, test.mode)
		if err != nil {
			t.Errorf("EffectiveSandboxForDispatch failed: %v", err)
			continue
		}

		if sandbox != test.expected {
			t.Errorf("sandbox = %q, want %q (mode=%s, fileSand=%s)", sandbox, test.expected, test.mode, test.fileSand)
		}
	}
}

// Helper to check string containment
func containsStr3(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
