package orchestration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Phase 3.3: Real Child Process Spawning
// Replaces echo stubs with actual subprocess execution (claude code CLI, Codex API future)

// SpawnClaudeCodeChild executes a Claude Code subprocess with the given prompt
func SpawnClaudeCodeChild(
	prompt string,
	model string,
	sandboxMode string,
	env map[string]string,
	timeout float64,
) map[string]any {
	if prompt == "" {
		return map[string]any{
			"status": "error",
			"reason": "prompt cannot be empty",
		}
	}

	if model == "" {
		return map[string]any{
			"status": "error",
			"reason": "model cannot be empty",
		}
	}

	// Validate Claude Code subprocess invocation
	if err := validateClaudeCodeExecution(model, sandboxMode); err != nil {
		return map[string]any{
			"status": "error",
			"reason": err.Error(),
		}
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	// Build claude code command
	// Format: claude code --agent <model> --brief <prompt>
	args := []string{
		"code",
		"--agent", model,
		"--brief", prompt,
	}

	// Add sandbox mode if specified
	if sandboxMode != "" {
		args = append(args, "--sandbox", sandboxMode)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)

	// Set environment
	cmd.Env = environmentMapToSlice(env)

	// Capture combined output
	output, err := cmd.CombinedOutput()

	// Handle execution results
	result := parseCommandOutput(cmd.ProcessState, output, err)

	// Wrap output as untrusted
	if outputStr, ok := result["output"].(string); ok {
		result["output"] = WrapUntrustedOutput(outputStr)
	}

	return result
}

// SpawnCodexChild executes a Codex API request (future implementation)
func SpawnCodexChild(
	prompt string,
	model string,
	env map[string]string,
	timeout float64,
) map[string]any {
	// Codex API integration is not yet implemented
	// Future: use Codex OpenAI client to execute prompt
	return map[string]any{
		"status": "unavailable",
		"reason": fmt.Sprintf("Codex runner not yet implemented (model: %s)", model),
	}
}

// ExecuteDispatchChild dispatches to appropriate spawner based on runner type
func ExecuteDispatchChild(
	ctx *DispatchContext,
	runner string,
	env map[string]string,
	timeout float64,
) map[string]any {
	if ctx == nil {
		return map[string]any{
			"status": "error",
			"reason": "dispatch context is nil",
		}
	}

	if err := ValidateDispatchContext(ctx); err != nil {
		return map[string]any{
			"status": "error",
			"reason": fmt.Sprintf("invalid dispatch context: %v", err),
		}
	}

	if runner == "" {
		runner = DefaultRunner
	}

	switch runner {
	case RunnerClaudeCode:
		return SpawnClaudeCodeChild(ctx.Prompt, ctx.Model, ctx.Sandbox, env, timeout)

	case RunnerCodex:
		return SpawnCodexChild(ctx.Prompt, ctx.Model, env, timeout)

	case RunnerAPI:
		// Spawns no child process: it drives a chat endpoint and executes the
		// tool calls itself, inside the sandbox in api_runner_sandbox.go.
		// That is the point of runner="api" -- it serves deployments where
		// there is no coding CLI to spawn.
		//
		// Configuration arrives from the caller rather than being resolved
		// here, so the runner stays testable without a settings tree.
		apiConfig := ResolveAPIRunnerConfig(apiProjectRoot())
		if apiConfig.Model == "" {
			apiConfig.Model = modelForTier(ctx.ModelTier)
		}
		return SpawnAPIChild(*ctx, apiConfig, time.Duration(timeout*float64(time.Second)))

	default:
		return map[string]any{
			"status": "error",
			"reason": fmt.Sprintf("unknown runner: %q", runner),
		}
	}
}

// SafelyKillProcess attempts graceful termination with timeout fallback
func SafelyKillProcess(pid int, timeout time.Duration) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process %d: %w", pid, err)
	}

	// Attempt graceful SIGTERM
	if err := process.Signal(os.Interrupt); err != nil {
		return fmt.Errorf("failed to send SIGTERM to %d: %w", pid, err)
	}

	// Wait with timeout, then force SIGKILL if needed
	done := make(chan error, 1)
	go func() {
		_, err := process.Wait()
		done <- err
	}()

	select {
	case <-time.After(timeout):
		// Process didn't exit gracefully, force kill
		if err := process.Kill(); err != nil {
			return fmt.Errorf("failed to force kill process %d: %w", pid, err)
		}
		<-done
		return nil
	case err := <-done:
		return err
	}
}

// Helper: validate Claude Code can be executed
func validateClaudeCodeExecution(model, sandboxMode string) error {
	// Check model is valid
	validModels := map[string]bool{
		"claude-opus-5":    true,
		"claude-sonnet-5":  true,
		"claude-haiku-4.5": true,
		"claude-opus-4.1":  true,
		"claude-sonnet-4":  true,
		"claude-haiku-4":   true,
	}

	if !validModels[model] {
		return fmt.Errorf("invalid model for Claude Code execution: %q", model)
	}

	// Check sandbox mode is valid if specified
	if sandboxMode != "" && !KnownSandboxModes[sandboxMode] {
		return fmt.Errorf("invalid sandbox mode: %q", sandboxMode)
	}

	return nil
}

// Helper: parse command execution results
func parseCommandOutput(state *os.ProcessState, output []byte, err error) map[string]any {
	exitCode := 0
	status := "success"
	errorMsg := ""

	if err != nil {
		status = "error"
		errorMsg = err.Error()

		// Extract exit code if available
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			status = "failed"
		}
	}

	if state != nil && !state.Success() {
		status = "failed"
		exitCode = state.ExitCode()
	}

	result := map[string]any{
		"status":    status,
		"exit_code": exitCode,
		"output":    string(output),
	}

	if errorMsg != "" {
		result["error"] = errorMsg
	}

	return result
}

// Helper: convert environment map to []string for exec.Cmd
func environmentMapToSlice(env map[string]string) []string {
	if env == nil {
		return nil
	}

	envSlice := make([]string, 0, len(env))
	for key, value := range env {
		envSlice = append(envSlice, key+"="+value)
	}
	return envSlice
}

// SpawnWithContextTimeout is a wrapper for spawning processes with context-based timeout
func SpawnWithContextTimeout(
	cmd *exec.Cmd,
	timeout time.Duration,
) map[string]any {
	if cmd == nil {
		return map[string]any{
			"status": "error",
			"reason": "command is nil",
		}
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Attach the context by rebuilding the command: exec.Cmd has no way to
	// bind a context after construction. CommandContext returns a *fresh*
	// Cmd, so every caller-supplied field has to be carried across
	// explicitly or the timeout wrapper silently discards it. Stdout/Stderr
	// are deliberately *not* copied -- CombinedOutput below rejects a Cmd
	// that already has them set.
	original := cmd
	cmd = exec.CommandContext(ctx, original.Path)
	cmd.Args = original.Args // preserves argv[0] even when it differs from Path
	cmd.Env = original.Env
	cmd.Dir = original.Dir
	cmd.Stdin = original.Stdin
	cmd.ExtraFiles = original.ExtraFiles
	cmd.SysProcAttr = original.SysProcAttr

	// Execute and capture output
	output, err := cmd.CombinedOutput()

	// Check for context timeout
	if ctx.Err() == context.DeadlineExceeded {
		return map[string]any{
			"status": "timeout",
			"reason": fmt.Sprintf("process exceeded timeout of %v", timeout),
			"output": string(output),
		}
	}

	return parseCommandOutput(cmd.ProcessState, output, err)
}
