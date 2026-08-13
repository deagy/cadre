package orchestration

import (
	"context"
	"fmt"
	"time"
)

// SubprocessRunner spawns agents as subprocess executables (Python scripts or Go binaries).
type SubprocessRunner struct {
	repoRoot string
}

// NewSubprocessRunner creates a runner that spawns agent scripts from the repository.
func NewSubprocessRunner(repoRoot string) *SubprocessRunner {
	return &SubprocessRunner{repoRoot: repoRoot}
}

// RunAgent executes an agent as a subprocess and collects its output.
func (s *SubprocessRunner) RunAgent(ctx context.Context, agentID string, task string, plan *DispatchPlan) (*AgentResult, error) {
	if plan == nil {
		return nil, fmt.Errorf("dispatch plan cannot be nil")
	}

	startTime := time.Now()
	result := &AgentResult{
		AgentID:   agentID,
		StartedAt: startTime,
	}

	// For now, this is a framework. Actual agent scripts would be discovered and spawned here.
	// The subprocess would be configured with:
	// - The agent's AGENT.md
	// - The task brief
	// - The dispatch plan (serialized as JSON or YAML)
	// - Any knowledge context

	// Placeholder implementation: just record that the agent would be dispatched
	result.Status = "skipped"
	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(startTime)

	return result, nil
}

// NOTE: Helper methods for agent script location and subprocess execution will be added
// in Phase 4d when actual agent spawning is implemented. For now, RunAgent is a placeholder.
