package orchestration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	cadreconfig "github.com/deagy/cadre/cli/internal/config"
)

// Phase 3.3: Real Child Process Spawning
// Replaces echo stubs with actual subprocess execution (claude code CLI, Codex API future)

// claudePermissionModes maps this repository's sandbox vocabulary onto the
// Claude Code CLI's own.
//
// Not cosmetic: --permission-mode is how the effective sandbox actually
// reaches the child. The previous invocation passed no permission flag at
// all, so every sandbox decision this package makes -- planning-review-only
// forcing read-only included -- was computed, logged, and then discarded at
// the exec.
var claudePermissionModes = map[string]string{
	SandboxReadOnly:         "plan",
	SandboxWorkspaceWrite:   "acceptEdits",
	SandboxDangerFullAccess: "bypassPermissions",
}

// SpawnClaudeCodeChild runs the Claude Code CLI with the prompt on stdin.
//
// The invocation was `claude code --agent <model> --brief <prompt>`, which is
// not a form the CLI accepts: there is no `code` subcommand, no `--agent`,
// and no `--brief`. It also placed the whole prompt -- role instructions plus
// the caller's untrusted brief -- in an argv element, where it is visible in
// the process table and bounded by ARG_MAX.
func SpawnClaudeCodeChild(
	prompt string,
	model string,
	sandboxMode string,
	env map[string]string,
	timeout float64,
) map[string]any {
	return spawnClaudeCodeChildIn("", prompt, model, sandboxMode, "", env, timeout)
}

func spawnClaudeCodeChildIn(
	projectRoot, prompt, model, sandboxMode, reasoningEffort string,
	env map[string]string,
	timeout float64,
) map[string]any {
	if prompt == "" {
		return map[string]any{"status": "error", "reason": "prompt cannot be empty"}
	}
	if model == "" {
		return map[string]any{"status": "error", "reason": "model cannot be empty"}
	}
	// The model is validated separately from the sandbox: an unrecognised
	// model is this repository's own catalog being wrong, which is a
	// different failure from a role declaring a sandbox nobody supports.
	if err := validateClaudeCodeExecution(model, ""); err != nil {
		return map[string]any{"status": "error", "reason": err.Error()}
	}
	// An empty sandbox is refused rather than defaulted. It cannot arise on
	// the dispatch path -- EffectiveSandboxForDispatch always resolves one --
	// so reaching here with none is a bug upstream, and quietly picking a
	// mode would hide it.
	permissionMode, known := claudePermissionModes[sandboxMode]
	if !known {
		// Refused rather than defaulted. Guessing a permission mode for an
		// unrecognised sandbox is the one direction that can only ever widen
		// what the child may do.
		return map[string]any{
			"status": "denied",
			"reason": fmt.Sprintf("unknown sandbox_mode for the Claude Code runner: %q", sandboxMode),
		}
	}

	argv := []string{
		runnerBinary("runners.claude_bin", "claude", projectRoot),
		"-p",
		"--model", model,
		"--permission-mode", permissionMode,
		"--strict-mcp-config",
	}
	if reasoningEffort != "" {
		argv = append(argv, "--effort", reasoningEffort)
	}

	return spawnChildWithPrompt(spawnChildOptions{
		Argv: argv, Prompt: prompt, Env: env, WorkingDir: projectRoot,
		Timeout: time.Duration(timeout * float64(time.Second)),
	})
}

// SpawnCodexChild runs the Codex CLI with the prompt on stdin.
//
// This returned {"status": "unavailable", "reason": "Codex runner not yet
// implemented"} -- for the default runner, so the default dispatch path could
// not run at all once it was reached.
func SpawnCodexChild(
	prompt string,
	model string,
	env map[string]string,
	timeout float64,
) map[string]any {
	return spawnCodexChildIn("", prompt, model, SandboxReadOnly, "", "", env, timeout)
}

func spawnCodexChildIn(
	projectRoot, prompt, model, sandboxMode, reasoningEffort, modelTier string,
	env map[string]string,
	timeout float64,
) map[string]any {
	if prompt == "" {
		return map[string]any{"status": "error", "reason": "prompt cannot be empty"}
	}
	if !KnownSandboxModes[sandboxMode] {
		return map[string]any{
			"status": "denied",
			"reason": fmt.Sprintf("unknown sandbox_mode for the Codex runner: %q", sandboxMode),
		}
	}

	// The trailing "-" is what makes codex exec read the prompt from stdin.
	argv := []string{
		runnerBinary("runners.codex_bin", "codex", projectRoot),
		"exec",
		"--sandbox", sandboxMode,
	}
	// A profile names a Codex config file the operator owns, carrying the
	// provider's base_url and credential. When one is set it supplies the
	// model too, so the wrapper's vendor identifier is not also passed --
	// a self-hosted endpoint has never heard of it.
	profile := runnerSetting("runners.codex_profile", projectRoot)
	if profile != "" {
		argv = append(argv, "--profile", profile)
	}
	// runners.local_model_<tier> overrides the wrapper's vendor identifier
	// for this role's catalog tier, so tier semantics (opus/sonnet/haiku)
	// survive a switch to a self-hosted model instead of being lost with the
	// vendor name. An override wins over both; with neither, the wrapper's
	// own identifier is used unless a profile already supplies one.
	effectiveModel := localModelForTier(modelTier, projectRoot)
	if effectiveModel == "" && profile == "" {
		effectiveModel = model
	}
	if effectiveModel != "" {
		argv = append(argv, "--model", effectiveModel)
	}
	if reasoningEffort != "" {
		// No dedicated flag exists; the CLI's generic -c override is the
		// documented mechanism.
		argv = append(argv, "-c", "model_reasoning_effort="+reasoningEffort)
	}
	if projectRoot != "" {
		argv = append(argv, "--cd", projectRoot)
	}
	argv = append(argv, "--skip-git-repo-check", "-")

	return spawnChildWithPrompt(spawnChildOptions{
		Argv: argv, Prompt: prompt, Env: env, WorkingDir: projectRoot,
		Timeout: time.Duration(timeout * float64(time.Second)),
	})
}

// runnerBinary resolves an operator-configured executable, falling back to
// the name on PATH. Anchored to the dispatch's project root rather than this
// process's cwd, which for a long-lived MCP server is unrelated to the
// project being dispatched.
func runnerBinary(key, fallback, projectRoot string) string {
	if resolved := runnerSetting(key, projectRoot); resolved != "" {
		return resolved
	}
	return fallback
}

func runnerSetting(key, projectRoot string) string {
	value, err := cadreconfig.ResolveOptional(key, projectRoot)
	if err != nil {
		return ""
	}
	text, _ := value.(string)
	return text
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
	case RunnerClaudeCode, RunnerCodex:
		// Only the CLI runners get a final-handoff channel: they are the ones
		// that speak to this process through a pipe, so a structured result
		// needs somewhere to go that is not the transcript. The api runner
		// already returns structure directly.
		childEnv, prompt, channel := prepareFinalHandoffChannel(env, ctx.Prompt)
		defer channel.cleanup()

		var result map[string]any
		if runner == RunnerClaudeCode {
			result = spawnClaudeCodeChildIn(ctx.ProjectRoot, prompt, ctx.Model, ctx.Sandbox,
				ctx.ReasoningEffort, childEnv, timeout)
		} else {
			result = spawnCodexChildIn(ctx.ProjectRoot, prompt, ctx.Model, ctx.Sandbox,
				ctx.ReasoningEffort, ctx.ModelTier, childEnv, timeout)
		}
		// Read on every outcome, including a failed or timed-out child: a
		// child that wrote its handoff and then crashed still said something,
		// and discarding it because the exit code was non-zero loses the one
		// structured account of what it did.
		channel.read(result)
		return result

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

// localModelForTier resolves runners.local_model_<tier>, or "" for no
// override.
//
// The key is built from the tier only after checking it against the known
// set, so an unexpected tier value cannot reach the settings resolver as an
// attacker-influenced key.
func localModelForTier(modelTier, projectRoot string) string {
	if modelTier == "" {
		return ""
	}
	switch strings.ToLower(modelTier) {
	case "opus", "sonnet", "haiku":
	default:
		return ""
	}
	return runnerSetting("runners.local_model_"+strings.ToLower(modelTier), projectRoot)
}
