package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AgentRequest is the structured input sent to an agent.
type AgentRequest struct {
	AgentID        string         `json:"agent_id"`
	TaskID         string         `json:"task_id"`
	Task           string         `json:"task"`
	Classification string         `json:"classification"`
	ChangedFiles   []string       `json:"changed_files"`
	DispatchPlan   *DispatchPlan  `json:"dispatch_plan"`
	Knowledge      *KnowledgeData `json:"knowledge,omitempty"`
}

// AgentResponse is the structured output from an agent.
type AgentResponse struct {
	AgentID  string   `json:"agent_id"`
	Status   string   `json:"status"` // "success", "failed", "error"
	Findings []string `json:"findings,omitempty"`
	Output   string   `json:"output,omitempty"`
	ExitCode int      `json:"exit_code"`
	Error    string   `json:"error,omitempty"`
	Duration string   `json:"duration,omitempty"`
}

// KnowledgeData holds knowledge context passed to agents.
type KnowledgeData struct {
	Passages []string `json:"passages,omitempty"`
	Source   string   `json:"source,omitempty"`
	Status   string   `json:"status,omitempty"`
}

// SubprocessAgentRunner executes agents via subprocess invocation.
// It supports Python scripts, Go binaries, and shell commands.
type SubprocessAgentRunner struct {
	repoRoot    string
	timeout     time.Duration
	strategy    ExecutionStrategy
	pythonPath  string
	environment []string
}

// ExecutionStrategy determines how agents are invoked.
type ExecutionStrategy string

const (
	// StrategySubprocess invokes agents as real subprocesses
	StrategySubprocess ExecutionStrategy = "subprocess"
	// StrategyMock returns mock success responses without invoking anything
	StrategyMock ExecutionStrategy = "mock"
	// StrategyDry validates the execution plan without running agents
	StrategyDry ExecutionStrategy = "dry"
)

// NewSubprocessAgentRunner creates an agent runner that invokes subprocess commands.
func NewSubprocessAgentRunner(repoRoot string, timeout time.Duration, strategy ExecutionStrategy) *SubprocessAgentRunner {
	env := os.Environ()
	if repoRoot != "" {
		env = append(env, fmt.Sprintf("CADRE_REPO_ROOT=%s", repoRoot))
	}

	return &SubprocessAgentRunner{
		repoRoot:    repoRoot,
		timeout:     timeout,
		strategy:    strategy,
		environment: env,
	}
}

// RunAgent executes a single agent via subprocess.
func (s *SubprocessAgentRunner) RunAgent(ctx context.Context, agentID string, task string, plan *DispatchPlan) (*AgentResult, error) {
	startedAt := time.Now()

	// Apply execution strategy
	switch s.strategy {
	case StrategyMock:
		return s.runMock(agentID, startedAt)
	case StrategyDry:
		return s.runDry(agentID, startedAt)
	case StrategySubprocess:
		return s.runSubprocess(ctx, agentID, task, plan, startedAt)
	default:
		return s.runMock(agentID, startedAt)
	}
}

// runMock returns a mock success result without invoking anything.
func (s *SubprocessAgentRunner) runMock(agentID string, startedAt time.Time) (*AgentResult, error) {
	completedAt := time.Now()
	return &AgentResult{
		AgentID:     agentID,
		Status:      "success",
		ExitCode:    0,
		Output:      fmt.Sprintf("Agent %s completed successfully (mock execution)", agentID),
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		Duration:    completedAt.Sub(startedAt),
	}, nil
}

// runDry validates the request without executing anything.
func (s *SubprocessAgentRunner) runDry(agentID string, startedAt time.Time) (*AgentResult, error) {
	completedAt := time.Now()
	return &AgentResult{
		AgentID:     agentID,
		Status:      "success",
		ExitCode:    0,
		Output:      fmt.Sprintf("Agent %s validated (dry run, no execution)", agentID),
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		Duration:    completedAt.Sub(startedAt),
	}, nil
}

// runSubprocess executes the agent as a real subprocess.
func (s *SubprocessAgentRunner) runSubprocess(ctx context.Context, agentID string, task string, plan *DispatchPlan, startedAt time.Time) (*AgentResult, error) {
	result := &AgentResult{
		AgentID:   agentID,
		StartedAt: startedAt,
	}

	// Apply timeout to context
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	// Build agent request
	req := &AgentRequest{
		AgentID:        agentID,
		TaskID:         plan.TaskID,
		Task:           task,
		Classification: plan.Classification,
		ChangedFiles:   plan.ChangedFiles,
		DispatchPlan:   plan,
	}

	// Serialize request to JSON
	reqJSON, err := json.Marshal(req)
	if err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("failed to serialize request: %v", err)
		result.ExitCode = 1
		result.CompletedAt = time.Now()
		result.Duration = result.CompletedAt.Sub(startedAt)
		return result, err
	}

	// Determine how to invoke the agent
	// For now, we'll create a placeholder that can be extended to invoke actual agents
	agentPath := s.locateAgentScript(agentID)
	if agentPath == "" {
		result.Status = "skipped"
		result.Output = fmt.Sprintf("Agent %s not found in repository; would invoke externally", agentID)
		result.CompletedAt = time.Now()
		result.Duration = result.CompletedAt.Sub(startedAt)
		return result, nil
	}

	// Execute the agent script
	return s.executeAgentScript(ctx, agentPath, reqJSON, result, startedAt)
}

// locateAgentScript finds an agent script in the repository.
// Looks for Python scripts under roster/{phase}/{role}/agent.py or similar patterns.
func (s *SubprocessAgentRunner) locateAgentScript(agentID string) string {
	if s.repoRoot == "" {
		return ""
	}

	// Common agent script locations to search
	searchPaths := []string{
		// Direct agent script
		filepath.Join(s.repoRoot, "roster", agentID, "agent.py"),
		// Under a phase directory
		filepath.Join(s.repoRoot, "roster", "*", agentID, "agent.py"),
		// Under src directory
		filepath.Join(s.repoRoot, "roster", agentID, "src", "agent.py"),
	}

	for _, pattern := range searchPaths {
		// For patterns with wildcards, we'd need glob matching
		// For now, check exact paths only
		if !strings.Contains(pattern, "*") {
			if _, err := os.Stat(pattern); err == nil {
				return pattern
			}
		}
	}

	return ""
}

// executeAgentScript runs the agent script via subprocess.
func (s *SubprocessAgentRunner) executeAgentScript(ctx context.Context, scriptPath string, reqJSON []byte, result *AgentResult, startedAt time.Time) (*AgentResult, error) {
	var stdout, stderr bytes.Buffer

	// Determine script type and build command
	var cmd *exec.Cmd
	switch {
	case strings.HasSuffix(scriptPath, ".py"):
		// Python script - use python3
		python := s.pythonPath
		if python == "" {
			python = "python3"
		}
		cmd = exec.CommandContext(ctx, python, scriptPath)
	case strings.HasSuffix(scriptPath, ".sh"):
		// Shell script
		cmd = exec.CommandContext(ctx, "bash", scriptPath)
	default:
		// Assume it's a binary
		cmd = exec.CommandContext(ctx, scriptPath)
	}

	// Pass request via stdin
	cmd.Stdin = bytes.NewReader(reqJSON)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = s.environment

	// Run the command
	err := cmd.Run()
	result.Output = stdout.String()
	result.Stderr = stderr.String()

	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
		result.CompletedAt = time.Now()
		result.Duration = result.CompletedAt.Sub(startedAt)
		return result, nil
	}

	// Try to parse response JSON if agent script produced it
	var resp AgentResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err == nil {
		// Successfully parsed agent response
		result.Status = resp.Status
		result.ExitCode = resp.ExitCode
		result.Findings = resp.Findings
		if resp.Error != "" {
			result.Error = resp.Error
		}
	} else {
		// Agent didn't return JSON - assume success with text output
		result.Status = "success"
		result.ExitCode = 0
	}

	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(startedAt)

	return result, nil
}

// SetPythonPath sets the explicit Python interpreter path to use.
func (s *SubprocessAgentRunner) SetPythonPath(path string) {
	s.pythonPath = path
}

// SetEnvironment overrides the environment variables for agent execution.
func (s *SubprocessAgentRunner) SetEnvironment(env []string) {
	s.environment = env
}
