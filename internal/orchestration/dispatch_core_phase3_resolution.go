package orchestration

import (
	"fmt"
	"strings"
)

// Phase 3.2: Real Role Resolution Integration
// Connects Phase 1 role file resolution to Phase 2 dispatch functions

// ResolveRoleForDispatch resolves a role for dispatch, selecting appropriate resolver based on runner
func ResolveRoleForDispatch(
	roleID string,
	runner string,
	projectRoot string,
	globalRoot string,
	pluginRoot string,
	mode string,
) (*ResolvedRole, error) {
	if err := ValidateRoleID(roleID); err != nil {
		return nil, err
	}

	if runner == "" {
		runner = DefaultRunner
	}

	// Route to appropriate resolver based on runner
	switch runner {
	case RunnerCodex:
		return ResolveRoleFileCodex(roleID, projectRoot, globalRoot, pluginRoot, mode)
	case RunnerClaudeCode:
		return ResolveClaudeCodeRoleFile(roleID, projectRoot, pluginRoot, mode)
	case RunnerAPI:
		// API runner doesn't use local role files (future: resolve from remote registry)
		return nil, &DispatchUnavailable{Reason: fmt.Sprintf("runner %q not yet implemented", RunnerAPI)}
	default:
		return nil, &DispatchDenied{Reason: fmt.Sprintf("unknown runner: %q", runner)}
	}
}

// ValidateResolvedRole validates a resolved role for dispatch
func ValidateResolvedRole(role *ResolvedRole, classification, mode string) error {
	if role == nil {
		return fmt.Errorf("resolved role is nil")
	}

	if role.DeveloperInstructs == "" {
		return fmt.Errorf("role has no developer_instructions")
	}

	if role.Model == "" {
		return fmt.Errorf("role has no model specified")
	}

	// Validate model tier (opus/sonnet/haiku)
	validModels := map[string]bool{
		"claude-opus-5":    true,
		"claude-sonnet-5":  true,
		"claude-haiku-4.5": true,
		"claude-opus-4.1":  true,
		"claude-sonnet-4":  true,
		"claude-haiku-4":   true,
	}

	if !validModels[role.Model] {
		return fmt.Errorf("invalid model %q: must be opus/sonnet/haiku tier", role.Model)
	}

	// Validate sandbox mode consistency
	if role.SandboxMode != "" {
		if !KnownSandboxModes[role.SandboxMode] {
			return fmt.Errorf("invalid sandbox_mode %q in role file", role.SandboxMode)
		}
	}

	// Verify mode can compute effective sandbox (validates mode itself)
	_, _, err := ComputeEffectiveSandbox(mode, role.SandboxMode)
	if err != nil {
		return err
	}

	// Validate classification doesn't exceed parent
	if classification != "" {
		_, err := ValidateClassification(classification, "restricted")
		if err != nil {
			return err
		}
	}

	return nil
}

// BuildDispatchPrompt combines developer instructions with untrusted brief
func BuildDispatchPrompt(role *ResolvedRole, brief string) (string, error) {
	if role == nil {
		return "", fmt.Errorf("role is nil")
	}

	if role.DeveloperInstructs == "" {
		return "", fmt.Errorf("role has no developer_instructions")
	}

	if brief == "" {
		return "", fmt.Errorf("brief cannot be empty")
	}

	// Size validation
	if len(brief) > MaxBriefBytes {
		return "", fmt.Errorf("brief exceeds max size %d bytes", MaxBriefBytes)
	}

	instructions := strings.TrimSpace(role.DeveloperInstructs)
	if len(instructions) == 0 {
		return "", fmt.Errorf("developer_instructions are empty after trimming")
	}

	// Compose prompt: instructions + untrusted brief fence
	prompt := ComposePrompt(instructions, brief)

	// Size cap on final prompt
	if len(prompt) > MaxChildOutputBytes {
		return "", fmt.Errorf("composed prompt exceeds max size %d bytes", MaxChildOutputBytes)
	}

	return prompt, nil
}

// EffectiveSandboxForDispatch computes the effective sandbox mode for a dispatch operation
func EffectiveSandboxForDispatch(role *ResolvedRole, mode string) (string, error) {
	if role == nil {
		return "", fmt.Errorf("role is nil")
	}

	effective, _, err := ComputeEffectiveSandbox(mode, role.SandboxMode)
	if err != nil {
		return "", err
	}

	return effective, nil
}

// ExtractModelTierFromRole extracts the model tier level (opus/sonnet/haiku)
func ExtractModelTierFromRole(role *ResolvedRole) string {
	if role == nil {
		return "sonnet" // default
	}

	model := role.Model
	if model == "" {
		return "sonnet"
	}

	// Extract tier from model name
	if strings.Contains(model, "opus") {
		return "opus"
	}
	if strings.Contains(model, "sonnet") {
		return "sonnet"
	}
	if strings.Contains(model, "haiku") {
		return "haiku"
	}

	return "sonnet" // default
}

// RoleToDispatchContext converts a resolved role to dispatch context for child execution
type DispatchContext struct {
	RoleID             string
	Model              string
	ModelTier          string
	Sandbox            string
	DeveloperInstructs string
	Prompt             string
	// Brief is the caller's raw, unfenced brief.
	//
	// Kept alongside Prompt for the api runner, which addresses a chat API
	// with separate system and user slots and so must fence the brief on its
	// own rather than reusing Prompt -- Prompt already has the role's trusted
	// instructions concatenated into it, and sending that as the user message
	// puts trusted policy inside the untrusted slot.
	Brief          string
	IsWriteCapable bool
}

// BuildDispatchContext prepares a complete dispatch context from a resolved role
func BuildDispatchContext(
	roleID string,
	role *ResolvedRole,
	brief string,
	mode string,
) (*DispatchContext, error) {
	if role == nil {
		return nil, fmt.Errorf("role is nil")
	}

	// Validate role
	if err := ValidateResolvedRole(role, "", mode); err != nil {
		return nil, fmt.Errorf("role validation failed: %w", err)
	}

	// Build prompt
	prompt, err := BuildDispatchPrompt(role, brief)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	// Compute effective sandbox
	sandbox, err := EffectiveSandboxForDispatch(role, mode)
	if err != nil {
		return nil, fmt.Errorf("failed to compute sandbox: %w", err)
	}

	// Extract model tier
	tier := ExtractModelTierFromRole(role)

	ctx := &DispatchContext{
		RoleID:             roleID,
		Model:              role.Model,
		ModelTier:          tier,
		Sandbox:            sandbox,
		DeveloperInstructs: role.DeveloperInstructs,
		Prompt:             prompt,
		Brief:              brief,
		IsWriteCapable:     WriteCarpableSandboxes[sandbox],
	}

	return ctx, nil
}

// ValidateDispatchContext validates a dispatch context before execution
func ValidateDispatchContext(ctx *DispatchContext) error {
	if ctx == nil {
		return fmt.Errorf("dispatch context is nil")
	}

	if ctx.RoleID == "" {
		return fmt.Errorf("role_id is empty")
	}

	if ctx.Model == "" {
		return fmt.Errorf("model is empty")
	}

	if ctx.Sandbox == "" {
		return fmt.Errorf("sandbox is empty")
	}

	if ctx.Prompt == "" {
		return fmt.Errorf("prompt is empty")
	}

	if len(ctx.Prompt) > MaxChildOutputBytes {
		return fmt.Errorf("prompt exceeds max size")
	}

	return nil
}
