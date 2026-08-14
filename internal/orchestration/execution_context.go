package orchestration

import (
	"context"
	"fmt"
	"time"
)

// ExecutionContext wraps execution with knowledge retrieval and agent context.
type ExecutionContext struct {
	Executor           *Executor
	KnowledgeRetriever KnowledgeRetriever
	Plan               *DispatchPlan
	RetrievedKnowledge *RetrievedKnowledge
	AgentInjections    map[string]*KnowledgeInjection
	ExecutedAt         time.Time
	CompletedAt        time.Time
	Duration           time.Duration
}

// NewExecutionContext creates a new execution context with executor and knowledge retriever.
func NewExecutionContext(executor *Executor, retriever KnowledgeRetriever) *ExecutionContext {
	if retriever == nil {
		retriever = &NoOpRetriever{}
	}
	return &ExecutionContext{
		Executor:           executor,
		KnowledgeRetriever: retriever,
		AgentInjections:    make(map[string]*KnowledgeInjection),
	}
}

// ExecuteWithKnowledge runs a dispatch plan with knowledge retrieval.
// It retrieves knowledge first, then executes agents with knowledge context.
func (ec *ExecutionContext) ExecuteWithKnowledge(ctx context.Context, plan *DispatchPlan) (*ExecutionResult, error) {
	if plan == nil {
		return nil, fmt.Errorf("dispatch plan cannot be nil")
	}

	startTime := time.Now()
	ec.Plan = plan
	ec.ExecutedAt = startTime

	// Step 1: Validate knowledge configuration
	if err := ValidateKnowledgeConfig(plan.KnowledgeContext); err != nil {
		return nil, fmt.Errorf("invalid knowledge context: %w", err)
	}

	// Step 2: Retrieve authorized knowledge
	if plan.KnowledgeContext.Enabled {
		knowledge, err := ec.KnowledgeRetriever.Retrieve(ctx, plan.KnowledgeContext, plan.Task, plan.Classification)

		if err != nil {
			// Log error but don't fail execution (knowledge retrieval is advisory)
			knowledge = &RetrievedKnowledge{
				Status: "error",
				Error:  err.Error(),
			}
		}
		ec.RetrievedKnowledge = knowledge
	} else {
		ec.RetrievedKnowledge = &RetrievedKnowledge{
			Status: "disabled",
		}
	}

	// Step 3: Build knowledge injections for each agent
	ec.buildAgentInjections(plan)

	// Step 4: Execute dispatch plan
	result, err := ec.Executor.Execute(ctx, plan)
	if err != nil {
		return nil, err
	}

	// Step 5: Annotate results with knowledge context
	result.Plan = plan
	if ec.RetrievedKnowledge != nil {
		result.Plan.KnowledgeContext.TopK = ec.RetrievedKnowledge.TotalCount
	}

	ec.CompletedAt = time.Now()
	ec.Duration = ec.CompletedAt.Sub(startTime)

	return result, nil
}

// buildAgentInjections creates knowledge context for each agent.
func (ec *ExecutionContext) buildAgentInjections(plan *DispatchPlan) {
	allAgents := append(append([]string{}, plan.Agents.Primary...), plan.Agents.Reviewers...)
	allAgents = append(allAgents, plan.Agents.Support...)

	for _, agentID := range allAgents {
		role := ec.determineRole(agentID, plan)
		injection := FormatKnowledgeForAgent(ec.RetrievedKnowledge, agentID, role)
		ec.AgentInjections[agentID] = injection
	}
}

// determineRole looks up an agent's role in the dispatch plan.
func (ec *ExecutionContext) determineRole(agentID string, plan *DispatchPlan) string {
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

// GetAgentContext retrieves the knowledge injection for a specific agent.
func (ec *ExecutionContext) GetAgentContext(agentID string) *KnowledgeInjection {
	return ec.AgentInjections[agentID]
}

// AgentContextSummary provides a summary of knowledge available to an agent.
type AgentContextSummary struct {
	AgentID            string
	Role               string
	KnowledgeAvailable bool
	PassageCount       int
	Sources            []string
	Classifications    []string
}

// SummarizeAgentContext creates a summary of what knowledge an agent has access to.
func (ec *ExecutionContext) SummarizeAgentContext(agentID string) *AgentContextSummary {
	injection := ec.GetAgentContext(agentID)
	if injection == nil {
		return &AgentContextSummary{
			AgentID: agentID,
		}
	}

	summary := &AgentContextSummary{
		AgentID: agentID,
		Role:    injection.Role,
	}

	if injection.Knowledge != nil && injection.Knowledge.Status == "success" {
		summary.KnowledgeAvailable = true
		summary.PassageCount = len(injection.Knowledge.Passages)

		// Extract unique sources and classifications
		sourceMap := make(map[string]bool)
		classMap := make(map[string]bool)
		for _, p := range injection.Knowledge.Passages {
			if p.Source != "" {
				sourceMap[p.Source] = true
			}
			if p.Classification != "" {
				classMap[p.Classification] = true
			}
		}

		for source := range sourceMap {
			summary.Sources = append(summary.Sources, source)
		}
		for class := range classMap {
			summary.Classifications = append(summary.Classifications, class)
		}
	}

	return summary
}
