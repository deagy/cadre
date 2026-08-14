package orchestration

import (
	"context"
	"testing"
	"time"
)

func TestNewOrchestrationWorkflow(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	retriever := &NoOpRetriever{}
	routing := &RoutingConfig{}

	workflow := NewOrchestrationWorkflow(routing, executor, retriever)

	if workflow == nil {
		t.Fatalf("workflow is nil")
	}

	if workflow.Routing != routing {
		t.Errorf("routing mismatch")
	}

	if workflow.Executor != executor {
		t.Errorf("executor mismatch")
	}
}

func TestNewOrchestrationWorkflowNilRetriever(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	routing := &RoutingConfig{}

	workflow := NewOrchestrationWorkflow(routing, executor, nil)

	if workflow == nil {
		t.Fatalf("workflow is nil")
	}

	_, ok := workflow.KnowledgeRetriever.(*NoOpRetriever)
	if !ok {
		t.Errorf("expected NoOpRetriever, got %T", workflow.KnowledgeRetriever)
	}
}

func TestWorkflowExecuteNilInput(t *testing.T) {
	workflow := NewOrchestrationWorkflow(&RoutingConfig{}, &Executor{}, nil)

	_, err := workflow.Execute(context.Background(), nil)
	if err == nil {
		t.Errorf("expected error for nil input")
	}
}

func TestWorkflowExecuteCompleteFlow(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	routing := &RoutingConfig{
		Routes:    []Route{},
		RiskRules: []Risk{},
	}

	workflow := NewOrchestrationWorkflow(routing, executor, &NoOpRetriever{})

	matches := []RouteMatch{
		{
			RouteID:   "route-1",
			Primary:   []string{"primary-1"},
			Reviewers: []string{"reviewer-1"},
		},
	}

	input := &WorkflowInput{
		TaskID:         "TASK-001",
		Task:           "Test task",
		ChangedFiles:   []string{"file.go"},
		Classification: "internal",
		Matches:        matches,
	}

	output, err := workflow.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if output == nil {
		t.Fatalf("output is nil")
	}

	if output.DispatchPlan == nil {
		t.Errorf("dispatch plan is nil")
	}

	if output.ExecutionResult == nil {
		t.Errorf("execution result is nil")
	}

	if output.Formatter == nil {
		t.Errorf("formatter is nil")
	}

	if output.ReportCard == nil {
		t.Errorf("report card is nil")
	}

	if output.Duration == 0 {
		t.Errorf("duration should be non-zero")
	}
}

func TestWorkflowNoRoutesMatched(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	routing := &RoutingConfig{
		Routes: []Route{
			{
				ID:       "route-never-matches",
				Keywords: []string{"kubernetes", "helm"},
				Paths:    []string{"k8s/**"},
			},
		},
		RiskRules: []Risk{},
	}

	workflow := NewOrchestrationWorkflow(routing, executor, nil)

	input := &WorkflowInput{
		TaskID:         "TASK-001",
		Task:           "Fix a bug in the main application",
		ChangedFiles:   []string{"src/main.go"},
		Classification: "internal",
		Matches:        []RouteMatch{}, // Empty - force route matching attempt
	}

	output, err := workflow.Execute(context.Background(), input)
	if err == nil {
		t.Errorf("expected error for no routes matching")
	}

	// When MatchTaskToRoutes fails (no routes match), status is "failed"
	// The "no-routes-matched" status only occurs if we reach the len(matches) == 0 check
	if output.Status != "failed" {
		t.Errorf("expected status failed, got %q", output.Status)
	}
}

func TestWorkflowGetters(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	routing := &RoutingConfig{}
	workflow := NewOrchestrationWorkflow(routing, executor, nil)

	matches := []RouteMatch{
		{RouteID: "r1", Primary: []string{"p1"}},
	}

	input := &WorkflowInput{
		TaskID:         "TASK-001",
		Task:           "Test",
		ChangedFiles:   []string{},
		Classification: "internal",
		Matches:        matches,
	}

	output, err := workflow.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Test getters
	plan := workflow.GetDispatchPlan()
	if plan == nil {
		t.Errorf("dispatch plan getter returned nil")
	}

	result := workflow.GetExecutionResult()
	if result == nil {
		t.Errorf("execution result getter returned nil")
	}

	ctx := workflow.GetExecutionContext()
	if ctx == nil {
		t.Errorf("execution context getter returned nil")
	}

	// Verify they match output
	if plan != output.DispatchPlan {
		t.Errorf("dispatch plan mismatch")
	}

	if result != output.ExecutionResult {
		t.Errorf("execution result mismatch")
	}
}

func TestWorkflowFormatOutput(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	routing := &RoutingConfig{}
	workflow := NewOrchestrationWorkflow(routing, executor, nil)

	matches := []RouteMatch{
		{RouteID: "r1", Primary: []string{"p1"}},
	}

	input := &WorkflowInput{
		TaskID:         "TASK-001",
		Task:           "Test",
		ChangedFiles:   []string{},
		Classification: "internal",
		Matches:        matches,
	}

	_, err := workflow.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Test various formats
	formats := []string{"json", "markdown", "text", "summary"}
	for _, format := range formats {
		output, err := workflow.FormatOutput(format)
		if err != nil {
			t.Errorf("FormatOutput(%q) error: %v", format, err)
		}

		if output == "" {
			t.Errorf("FormatOutput(%q) is empty", format)
		}
	}
}

func TestWorkflowFormatOutputNotExecuted(t *testing.T) {
	workflow := NewOrchestrationWorkflow(&RoutingConfig{}, &Executor{}, nil)

	_, err := workflow.FormatOutput("json")
	if err == nil {
		t.Errorf("expected error when workflow not executed")
	}
}

func TestWorkflowGetStatus(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	routing := &RoutingConfig{}
	workflow := NewOrchestrationWorkflow(routing, executor, nil)

	// Before execution
	status := workflow.GetStatus()
	if status.OverallStatus != "not-executed" {
		t.Errorf("expected status not-executed, got %q", status.OverallStatus)
	}

	// After execution
	matches := []RouteMatch{
		{
			RouteID:   "r1",
			Primary:   []string{"p1"},
			Reviewers: []string{"r1"},
		},
	}

	input := &WorkflowInput{
		TaskID:         "TASK-001",
		Task:           "Test",
		ChangedFiles:   []string{},
		Classification: "internal",
		Matches:        matches,
	}

	_, err := workflow.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status = workflow.GetStatus()
	if status.OverallStatus == "not-executed" {
		t.Errorf("status should not be not-executed after execution")
	}

	if status.Phase != "complete" {
		t.Errorf("expected phase complete, got %q", status.Phase)
	}

	if status.AgentsDispatched != 2 {
		t.Errorf("expected 2 agents dispatched, got %d", status.AgentsDispatched)
	}

	if status.Duration == "" {
		t.Errorf("duration should not be empty")
	}
}

func TestWorkflowOutputFields(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	routing := &RoutingConfig{}
	workflow := NewOrchestrationWorkflow(routing, executor, nil)

	matches := []RouteMatch{
		{RouteID: "r1", Primary: []string{"p1"}},
	}

	input := &WorkflowInput{
		TaskID:         "TASK-001",
		Task:           "Test",
		ChangedFiles:   []string{},
		Classification: "internal",
		Matches:        matches,
	}

	output, err := workflow.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if output.StartedAt.IsZero() {
		t.Errorf("started_at should not be zero")
	}

	if output.CompletedAt.IsZero() {
		t.Errorf("completed_at should not be zero")
	}

	if output.CompletedAt.Before(output.StartedAt) {
		t.Errorf("completed_at should be after started_at")
	}

	if output.Duration == 0 {
		t.Errorf("duration should be non-zero")
	}

	if len(output.Status) < 8 || output.Status[:8] != "complete" {
		t.Errorf("status should start with 'complete', got %q", output.Status)
	}
}

func TestWorkflowNoAgentsSelected(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	routing := &RoutingConfig{}
	workflow := NewOrchestrationWorkflow(routing, executor, nil)

	matches := []RouteMatch{
		{
			RouteID:   "r1",
			Primary:   []string{}, // No agents
			Reviewers: []string{},
		},
	}

	input := &WorkflowInput{
		TaskID:         "TASK-001",
		Task:           "Test",
		ChangedFiles:   []string{},
		Classification: "internal",
		Matches:        matches,
	}

	output, err := workflow.Execute(context.Background(), input)
	if err == nil {
		t.Errorf("expected error for no agents selected")
	}

	if output.Status != "no-agents-selected" {
		t.Errorf("expected status no-agents-selected, got %q", output.Status)
	}
}
