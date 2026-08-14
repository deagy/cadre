package orchestration

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ExecutionResult holds the outcome of executing a dispatch plan.
type ExecutionResult struct {
	Plan          *DispatchPlan           `json:"plan"`
	AgentResults  map[string]*AgentResult `json:"agent_results"`
	Waves         [][]string              `json:"waves"`
	ExecutedAt    time.Time               `json:"executed_at"`
	CompletedAt   time.Time               `json:"completed_at"`
	Duration      time.Duration           `json:"duration"`
	TotalErrors   int                     `json:"total_errors"`
	SuccessCount  int                     `json:"success_count"`
	ErrorMessages map[string]string       `json:"error_messages,omitempty"`
}

// AgentResult holds the outcome of running a single agent.
type AgentResult struct {
	AgentID     string        `json:"agent_id"`
	Role        string        `json:"role"`
	Status      string        `json:"status"` // "success", "failed", "timeout", "skipped"
	ExitCode    int           `json:"exit_code"`
	Duration    time.Duration `json:"duration"`
	Output      string        `json:"output,omitempty"`
	Stderr      string        `json:"stderr,omitempty"`
	Error       string        `json:"error,omitempty"`
	Findings    []string      `json:"findings,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt time.Time     `json:"completed_at"`
}

// AgentRunner is the interface for executing a single agent.
// Implementations can vary by runner type (subprocess, Claude Code, Cline, etc.).
type AgentRunner interface {
	RunAgent(ctx context.Context, agentID string, task string, plan *DispatchPlan) (*AgentResult, error)
}

// Executor orchestrates dispatch plan execution across agents and waves.
type Executor struct {
	runner   AgentRunner
	maxWaves int // maximum concurrent agents per wave
	timeout  time.Duration
}

// NewExecutor creates a new executor with a given agent runner.
func NewExecutor(runner AgentRunner, maxWaves int, timeout time.Duration) *Executor {
	if maxWaves <= 0 {
		maxWaves = 4 // sensible default
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute // sensible default
	}
	return &Executor{
		runner:   runner,
		maxWaves: maxWaves,
		timeout:  timeout,
	}
}

// Execute runs the dispatch plan through its configured agents.
// It coordinates execution waves: primary agents first, then reviewers, then support.
func (e *Executor) Execute(ctx context.Context, plan *DispatchPlan) (*ExecutionResult, error) {
	if plan == nil {
		return nil, fmt.Errorf("dispatch plan cannot be nil")
	}

	startTime := time.Now()
	result := &ExecutionResult{
		Plan:          plan,
		AgentResults:  make(map[string]*AgentResult),
		ExecutedAt:    startTime,
		ErrorMessages: make(map[string]string),
	}

	// Coordinate execution waves based on dispatch plan
	waves := e.coordinateWaves(plan)
	result.Waves = waves

	// Execute each wave sequentially (wave N+1 starts after wave N completes)
	for waveNum, agentIDs := range waves {
		err := e.executeWave(ctx, waveNum, agentIDs, plan, result)
		if err != nil {
			result.ErrorMessages[fmt.Sprintf("wave_%d", waveNum)] = err.Error()
			// Continue with next wave even if current wave has errors
		}
	}

	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(startTime)

	// Summarize results (count errors from agent results, not wave errors)
	for _, res := range result.AgentResults {
		if res.Status == "success" {
			result.SuccessCount++
		} else if res.Status != "skipped" {
			result.TotalErrors++
		}
	}

	return result, nil
}

// executeWave runs all agents in a single wave in parallel.
func (e *Executor) executeWave(ctx context.Context, waveNum int, agentIDs []string, plan *DispatchPlan, result *ExecutionResult) error {
	if len(agentIDs) == 0 {
		return nil
	}

	// Create a context with timeout for this wave
	waveCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex // Protect concurrent writes to result.AgentResults
	errChan := make(chan error, len(agentIDs))

	// Launch all agents in this wave concurrently
	for _, agentID := range agentIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			// Determine the role for this agent
			role := e.determineRole(id, plan)

			// Run the agent
			agentResult, err := e.runner.RunAgent(waveCtx, id, plan.Task, plan)
			if err != nil {
				errChan <- fmt.Errorf("agent %s failed: %w", id, err)
				mu.Lock()
				result.AgentResults[id] = &AgentResult{
					AgentID:     id,
					Role:        role,
					Status:      "failed",
					Error:       err.Error(),
					StartedAt:   time.Now(), // approximate
					CompletedAt: time.Now(),
				}
				mu.Unlock()
				return
			}

			// Store successful result
			if agentResult == nil {
				agentResult = &AgentResult{
					AgentID: id,
					Role:    role,
					Status:  "skipped",
				}
			}
			agentResult.Role = role
			mu.Lock()
			result.AgentResults[id] = agentResult
			mu.Unlock()
		}(agentID)
	}

	// Wait for all agents to complete
	wg.Wait()
	close(errChan)

	// Collect any errors from the wave
	var waveErr error
	for err := range errChan {
		if waveErr == nil {
			waveErr = err
		} else {
			waveErr = fmt.Errorf("%w; %w", waveErr, err)
		}
	}

	return waveErr
}

// coordinateWaves determines execution order based on dispatch plan.
// Returns ordered slices: [primary agents] [reviewers] [support agents]
// Primary and reviewers can be dispatched before support (not strictly dependent),
// but we order them for logical clarity: authors first, then reviewers, then support.
func (e *Executor) coordinateWaves(plan *DispatchPlan) [][]string {
	var waves [][]string

	// Wave 1: Primary agents (authors/executors)
	if len(plan.Agents.Primary) > 0 {
		waves = append(waves, append([]string{}, plan.Agents.Primary...))
	}

	// Wave 2: Reviewers (independent review, can run in parallel with primary but after for dependency clarity)
	if len(plan.Agents.Reviewers) > 0 {
		waves = append(waves, append([]string{}, plan.Agents.Reviewers...))
	}

	// Wave 3: Support agents (advisory, runs after main work)
	if len(plan.Agents.Support) > 0 {
		waves = append(waves, append([]string{}, plan.Agents.Support...))
	}

	return waves
}

// determineRole looks up which role an agent belongs to in the dispatch plan.
func (e *Executor) determineRole(agentID string, plan *DispatchPlan) string {
	for _, a := range plan.Agents.Primary {
		if a == agentID {
			return "primary"
		}
	}
	for _, a := range plan.Agents.Reviewers {
		if a == agentID {
			return "reviewer"
		}
	}
	for _, a := range plan.Agents.Support {
		if a == agentID {
			return "support"
		}
	}
	return "unknown"
}
