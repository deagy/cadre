package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// APIAgentRunner executes agents via LLM API calls (Claude, OpenAI, etc).
// It sends structured task descriptions to LLMs and parses responses into findings.
type APIAgentRunner struct {
	provider    APIProvider
	modelID     string
	timeout     time.Duration
	maxTokens   int
	temperature float32
}

// APIProvider defines the interface for LLM API backends.
type APIProvider interface {
	// Name returns the provider name (e.g., "claude", "openai")
	Name() string
	// CallAgent sends a request to the LLM and returns the response
	CallAgent(ctx context.Context, request *AgentRequest, modelID string, maxTokens int, temperature float32) (string, error)
}

// NewAPIAgentRunner creates an agent runner using an LLM API provider.
func NewAPIAgentRunner(provider APIProvider, modelID string, timeout time.Duration) *APIAgentRunner {
	return &APIAgentRunner{
		provider:    provider,
		modelID:     modelID,
		timeout:     timeout,
		maxTokens:   4096,
		temperature: 0.7,
	}
}

// RunAgent executes an agent via LLM API call.
func (a *APIAgentRunner) RunAgent(ctx context.Context, agentID string, task string, plan *DispatchPlan) (*AgentResult, error) {
	startedAt := time.Now()
	result := &AgentResult{
		AgentID:   agentID,
		StartedAt: startedAt,
	}

	// Apply timeout
	if a.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.timeout)
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

	// Call the LLM API
	response, err := a.provider.CallAgent(ctx, req, a.modelID, a.maxTokens, a.temperature)
	if err != nil {
		result.Status = "failed"
		result.Error = fmt.Sprintf("API call failed: %v", err)
		result.ExitCode = 1
		result.CompletedAt = time.Now()
		result.Duration = result.CompletedAt.Sub(startedAt)
		return result, nil
	}

	// Try to parse JSON response
	var resp AgentResponse
	if err := json.Unmarshal([]byte(response), &resp); err == nil {
		// Successfully parsed structured response
		result.Status = resp.Status
		result.ExitCode = resp.ExitCode
		result.Findings = resp.Findings
		result.Output = resp.Output
		if resp.Error != "" {
			result.Error = resp.Error
		}
	} else {
		// Treat raw text response as successful output
		result.Status = "success"
		result.ExitCode = 0
		result.Output = response
	}

	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(startedAt)

	return result, nil
}

// SetMaxTokens sets the maximum tokens for API responses.
func (a *APIAgentRunner) SetMaxTokens(maxTokens int) {
	a.maxTokens = maxTokens
}

// SetTemperature sets the temperature for API calls.
func (a *APIAgentRunner) SetTemperature(temperature float32) {
	a.temperature = temperature
}
