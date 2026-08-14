package orchestration

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// Phase 3.4: Interactive Confirmation Flow
// Adds real user prompts for write-mode confirmations with timeout support

const (
	// Confirmation prompt timeout (seconds)
	ConfirmationPromptTimeout = 30
)

// PromptForConfirmation displays a confirmation prompt and reads user response
func PromptForConfirmation(
	ctx context.Context,
	data map[string]any,
	timeout time.Duration,
) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("context is nil")
	}

	if data == nil {
		return false, fmt.Errorf("confirmation data is nil")
	}

	// Display the prompt
	prompt := DisplayConfirmationPrompt(data)
	if _, err := fmt.Fprint(os.Stderr, prompt); err != nil {
		return false, fmt.Errorf("failed to write prompt: %w", err)
	}

	// Set up timeout for reading response
	responseChan := make(chan string, 1)
	errorChan := make(chan error, 1)

	// Read response in goroutine
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			responseChan <- strings.ToLower(strings.TrimSpace(scanner.Text()))
		}
		if err := scanner.Err(); err != nil {
			errorChan <- fmt.Errorf("failed to read response: %w", err)
		}
	}()

	// Wait for response with timeout
	select {
	case <-ctx.Done():
		return false, fmt.Errorf("confirmation prompt cancelled")

	case err := <-errorChan:
		return false, err

	case response := <-responseChan:
		approved := response == "y" || response == "yes"
		return approved, nil

	case <-time.After(timeout):
		return false, fmt.Errorf("confirmation prompt timeout after %v", timeout)
	}
}

// DisplayConfirmationPrompt formats a human-readable confirmation prompt
func DisplayConfirmationPrompt(data map[string]any) string {
	var prompt strings.Builder

	prompt.WriteString("\n")
	prompt.WriteString("╔═══════════════════════════════════════════════════════════════╗\n")
	prompt.WriteString("║                   WRITE-MODE CONFIRMATION                     ║\n")
	prompt.WriteString("╚═══════════════════════════════════════════════════════════════╝\n")
	prompt.WriteString("\n")

	// Display operation details
	if roleID, ok := data["role_id"].(string); ok && roleID != "" {
		fmt.Fprintf(&prompt, "Role:             %s\n", roleID)
	}

	if mode, ok := data["mode"].(string); ok && mode != "" {
		fmt.Fprintf(&prompt, "Dispatch Mode:    %s\n", mode)
	}

	if sandboxMode, ok := data["sandbox_mode"].(string); ok && sandboxMode != "" {
		fmt.Fprintf(&prompt, "Sandbox:          %s\n", sandboxMode)
	}

	if classification, ok := data["classification"].(string); ok && classification != "" {
		fmt.Fprintf(&prompt, "Classification:   %s\n", classification)
	}

	if taskID, ok := data["task_id"].(string); ok && taskID != "" {
		fmt.Fprintf(&prompt, "Task ID:          %s\n", taskID)
	}

	// Display sandbox implications
	prompt.WriteString("\n")
	prompt.WriteString("⚠️  SANDBOX IMPLICATIONS:\n")
	if sandboxMode, ok := data["sandbox_mode"].(string); ok {
		switch sandboxMode {
		case SandboxWorkspaceWrite:
			prompt.WriteString("    • Can read/write workspace files\n")
			prompt.WriteString("    • Cannot access system resources outside workspace\n")
		case SandboxDangerFullAccess:
			prompt.WriteString("    • FULL access to system resources\n")
			prompt.WriteString("    • Can read/write any accessible files\n")
			prompt.WriteString("    • Danger: potential for destructive operations\n")
		}
	}

	// Display audit logging notice
	prompt.WriteString("\n")
	prompt.WriteString("📋 AUDIT LOG:\n")
	prompt.WriteString("    • All operations are logged and audited\n")
	prompt.WriteString("    • Decisions are recorded with timestamp\n")

	prompt.WriteString("\n")
	prompt.WriteString("Do you approve this dispatch? (yes/no): ")

	return prompt.String()
}

// RecordConfirmationDecision logs a confirmation decision to audit trail
func RecordConfirmationDecision(
	jobID string,
	approved bool,
	decider string,
	data map[string]any,
) error {
	if jobID == "" {
		return fmt.Errorf("job_id is required")
	}

	if decider == "" {
		decider = "interactive_user"
	}

	// Build audit record for confirmation decision
	decision := "denied"
	if approved {
		decision = "approved"
	}

	record, err := BuildAuditRecord(map[string]any{
		"event_type": "confirmation_decision",
		"job_id":     jobID,
		"decision":   decision,
		"decider":    decider,
		"role_id":    extractString(data, "role_id"),
		"mode":       extractString(data, "mode"),
		"task_id":    extractString(data, "task_id"),
	})

	if err != nil {
		return fmt.Errorf("failed to build audit record: %w", err)
	}

	return WriteAuditLog(record)
}

// PromptWithContext creates a context with timeout and runs PromptForConfirmation
func PromptWithContext(
	data map[string]any,
	timeout time.Duration,
) (bool, error) {
	if timeout == 0 {
		timeout = time.Duration(ConfirmationPromptTimeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return PromptForConfirmation(ctx, data, timeout)
}

// IsInteractiveMode detects if stdin/stdout are real terminals
func IsInteractiveMode() bool {
	// Check if stdin is a terminal
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	// Check if stdout is a terminal
	statOut, err := os.Stdout.Stat()
	if err != nil {
		return false
	}

	// Both should be character devices (terminals)
	isModeIn := (stat.Mode() & os.ModeCharDevice) != 0
	isModeOut := (statOut.Mode() & os.ModeCharDevice) != 0

	return isModeIn && isModeOut
}

// DefaultConfirmationBehavior determines how to handle confirmation in non-interactive mode
func DefaultConfirmationBehavior(approveByDefault bool) bool {
	// In automation/CI, default to denied unless explicitly configured
	return approveByDefault
}

// EnsureConfirmationApproved validates confirmation before proceeding
func EnsureConfirmationApproved(
	approved bool,
	approveByDefault bool,
	data map[string]any,
) error {
	if approved {
		return nil // Approved, proceed
	}

	// Confirmation denied
	mode := "unknown"
	if m, ok := data["mode"].(string); ok {
		mode = m
	}

	return fmt.Errorf("dispatch denied: confirmation not approved for mode %s", mode)
}

// Helper: safely extract string from data map
func extractString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}

	if val, ok := data[key].(string); ok {
		return val
	}

	return ""
}

// InteractiveConfirmationFlow coordinates the full confirmation workflow
type InteractiveConfirmationFlow struct {
	data           map[string]any
	timeout        time.Duration
	approveDefault bool
}

// NewInteractiveConfirmationFlow creates a new confirmation flow coordinator
func NewInteractiveConfirmationFlow(data map[string]any, timeout time.Duration, approveDefault bool) *InteractiveConfirmationFlow {
	if timeout == 0 {
		timeout = time.Duration(ConfirmationPromptTimeout) * time.Second
	}

	return &InteractiveConfirmationFlow{
		data:           data,
		timeout:        timeout,
		approveDefault: approveDefault,
	}
}

// Execute runs the confirmation flow
func (icf *InteractiveConfirmationFlow) Execute() (bool, error) {
	if icf.data == nil {
		return false, fmt.Errorf("confirmation data is nil")
	}

	// Check if interactive mode is available
	if !IsInteractiveMode() {
		// Non-interactive: use default behavior
		return icf.approveDefault, nil
	}

	// Interactive mode: prompt user
	approved, err := PromptWithContext(icf.data, icf.timeout)
	if err != nil {
		// On timeout or error, use default
		if icf.approveDefault {
			return true, nil
		}
		return false, err
	}

	return approved, nil
}
