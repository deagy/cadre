package orchestration

import (
	"context"
	"fmt"
	"time"
)

// OrchestrationWorkflow orchestrates the complete end-to-end process:
// route selection → dispatch planning → knowledge retrieval → agent execution → result reporting
type OrchestrationWorkflow struct {
	Routing            *RoutingConfig
	Executor           *Executor
	KnowledgeRetriever KnowledgeRetriever
	ResultFormatter    *ResultFormatter
	dispatchPlan       *DispatchPlan
	executionResult    *ExecutionResult
	executionContext   *ExecutionContext
	consolidatedResult *ConsolidatedResult
}

// WorkflowInput defines the input to an orchestration workflow.
type WorkflowInput struct {
	TaskID         string
	Task           string
	ChangedFiles   []string
	Classification string
	Matches        []RouteMatch // Pre-computed route matches (optional)
}

// WorkflowOutput defines the complete output of an orchestration workflow.
type WorkflowOutput struct {
	DispatchPlan       *DispatchPlan
	ExecutionResult    *ExecutionResult
	ConsolidatedResult *ConsolidatedResult
	Formatter          *ResultFormatter
	ReportCard         *ReportCard
	Status             string
	Error              string
	StartedAt          time.Time
	CompletedAt        time.Time
	Duration           time.Duration
}

// NewOrchestrationWorkflow creates a new orchestration workflow.
func NewOrchestrationWorkflow(routing *RoutingConfig, executor *Executor, retriever KnowledgeRetriever) *OrchestrationWorkflow {
	if retriever == nil {
		retriever = &NoOpRetriever{}
	}

	return &OrchestrationWorkflow{
		Routing:            routing,
		Executor:           executor,
		KnowledgeRetriever: retriever,
	}
}

// Execute runs the complete orchestration workflow end-to-end.
func (ow *OrchestrationWorkflow) Execute(ctx context.Context, input *WorkflowInput) (*WorkflowOutput, error) {
	if input == nil {
		return nil, fmt.Errorf("workflow input cannot be nil")
	}

	output := &WorkflowOutput{
		StartedAt: time.Now(),
		Status:    "running",
	}

	// Step 1: Route matching (or use pre-computed matches)
	var matches []RouteMatch
	if len(input.Matches) > 0 {
		matches = input.Matches
	} else {
		var err error
		matches, err = MatchTaskToRoutes(input.Task, input.ChangedFiles, input.Classification, ow.Routing)
		if err != nil {
			output.Status = "failed"
			output.Error = fmt.Sprintf("route matching failed: %v", err)
			output.CompletedAt = time.Now()
			output.Duration = output.CompletedAt.Sub(output.StartedAt)
			return output, err
		}

		if len(matches) == 0 {
			output.Status = "no-routes-matched"
			output.Error = "no routes matched for this task"
			output.CompletedAt = time.Now()
			output.Duration = output.CompletedAt.Sub(output.StartedAt)
			return output, fmt.Errorf("needs-triage: %s", output.Error)
		}
	}

	// Step 2: Build dispatch plan
	plan, err := BuildDispatchPlan(
		input.TaskID,
		input.Task,
		input.ChangedFiles,
		input.Classification,
		matches,
		ow.Routing,
	)
	if err != nil {
		output.Status = "failed"
		output.Error = fmt.Sprintf("dispatch plan building failed: %v", err)
		output.CompletedAt = time.Now()
		output.Duration = output.CompletedAt.Sub(output.StartedAt)
		return output, err
	}

	ow.dispatchPlan = plan
	output.DispatchPlan = plan

	// Step 3: Check dispatch disposition before proceeding
	if plan.DispatchDisposition.Status == "no-agents-selected" {
		output.Status = "no-agents-selected"
		output.Error = plan.DispatchDisposition.Reason
		output.CompletedAt = time.Now()
		output.Duration = output.CompletedAt.Sub(output.StartedAt)
		return output, fmt.Errorf("needs-triage: %s", output.Error)
	}

	// Step 4: Execute with knowledge retrieval
	execCtx := NewExecutionContext(ow.Executor, ow.KnowledgeRetriever)
	ow.executionContext = execCtx

	execResult, err := execCtx.ExecuteWithKnowledge(ctx, plan)
	if err != nil {
		output.Status = "execution-failed"
		output.Error = fmt.Sprintf("execution failed: %v", err)
		output.CompletedAt = time.Now()
		output.Duration = output.CompletedAt.Sub(output.StartedAt)
		return output, err
	}

	ow.executionResult = execResult
	output.ExecutionResult = execResult

	// Step 5: Consolidate results from multiple agents
	consolidated := ConsolidateResults(execResult)
	ow.consolidatedResult = consolidated
	output.ConsolidatedResult = consolidated

	// Step 6: Format results
	formatter := NewResultFormatter(execResult)
	ow.ResultFormatter = formatter
	output.Formatter = formatter

	// Step 7: Generate report card
	reportCard := formatter.GenerateReportCard()
	output.ReportCard = reportCard

	// Step 8: Determine final status
	if consolidated == nil {
		output.Status = "complete-no-consolidation"
	} else {
		output.Status = fmt.Sprintf("complete-quality-%.0f", consolidated.QualityScore*100)
	}

	output.CompletedAt = time.Now()
	output.Duration = output.CompletedAt.Sub(output.StartedAt)

	return output, nil
}

// GetDispatchPlan returns the computed dispatch plan.
func (ow *OrchestrationWorkflow) GetDispatchPlan() *DispatchPlan {
	return ow.dispatchPlan
}

// GetExecutionResult returns the execution result.
func (ow *OrchestrationWorkflow) GetExecutionResult() *ExecutionResult {
	return ow.executionResult
}

// GetExecutionContext returns the execution context with knowledge.
func (ow *OrchestrationWorkflow) GetExecutionContext() *ExecutionContext {
	return ow.executionContext
}

// FormatOutput exports the execution result in the specified format.
func (ow *OrchestrationWorkflow) FormatOutput(format string) (string, error) {
	if ow.ResultFormatter == nil {
		return "", fmt.Errorf("workflow has not been executed")
	}

	return ow.ResultFormatter.ExportResults(format)
}

// WorkflowStatus provides a summary status of the workflow.
type WorkflowStatus struct {
	OverallStatus      string
	Phase              string
	AgentsDispatched   int
	AgentsFailed       int
	AgentsSuccessful   int
	FindingsCount      int
	CriticalFindings   int
	HighFindings       int
	Duration           string
	KnowledgeRetrieved bool
}

// GetConsolidatedResult returns the consolidated analysis results.
func (ow *OrchestrationWorkflow) GetConsolidatedResult() *ConsolidatedResult {
	return ow.consolidatedResult
}

// GetStatus returns a status summary of the workflow execution.
func (ow *OrchestrationWorkflow) GetStatus() *WorkflowStatus {
	status := &WorkflowStatus{}

	if ow.executionResult == nil {
		status.OverallStatus = "not-executed"
		status.Phase = "initialization"
		return status
	}

	if ow.executionContext == nil {
		status.OverallStatus = "incomplete"
		status.Phase = "dispatch"
		return status
	}

	consolidated := ow.consolidatedResult
	if consolidated == nil {
		status.OverallStatus = "incomplete"
		status.Phase = "consolidation"
		return status
	}

	status.Phase = "complete"
	status.AgentsDispatched = consolidated.TotalAgents
	status.AgentsFailed = consolidated.FailedAgents
	status.AgentsSuccessful = consolidated.SuccessfulAgents
	status.FindingsCount = len(consolidated.Findings)
	status.Duration = fmt.Sprintf("%v", consolidated.Duration)
	status.KnowledgeRetrieved = ow.executionContext != nil &&
		ow.executionContext.RetrievedKnowledge != nil &&
		ow.executionContext.RetrievedKnowledge.Status == "success"

	// Count finding severity
	for _, finding := range consolidated.Findings {
		switch finding.Severity {
		case "critical":
			status.CriticalFindings++
		case "high":
			status.HighFindings++
		}
	}

	// Overall status based on quality score
	switch {
	case consolidated.QualityScore >= 0.8:
		status.OverallStatus = "high-quality"
	case consolidated.QualityScore >= 0.6:
		status.OverallStatus = "medium-quality"
	default:
		status.OverallStatus = "low-quality"
	}

	return status
}
