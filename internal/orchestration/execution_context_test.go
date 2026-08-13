package orchestration

import (
	"context"
	"testing"
	"time"
)

// mockKnowledgeRetriever returns configured knowledge for testing.
type mockKnowledgeRetriever struct {
	knowledge *RetrievedKnowledge
	err       error
}

func (m *mockKnowledgeRetriever) Retrieve(ctx context.Context, config KnowledgeContext, task string, classification string) (*RetrievedKnowledge, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.knowledge == nil {
		return &RetrievedKnowledge{
			Status:   "disabled",
			Passages: []KnowledgePassage{},
		}, nil
	}
	return m.knowledge, nil
}

func TestNewExecutionContext(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	retriever := &NoOpRetriever{}

	ec := NewExecutionContext(executor, retriever)

	if ec == nil {
		t.Fatalf("execution context is nil")
	}

	if ec.Executor != executor {
		t.Errorf("executor mismatch")
	}

	if ec.KnowledgeRetriever != retriever {
		t.Errorf("retriever mismatch")
	}
}

func TestNewExecutionContextNilRetriever(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)

	ec := NewExecutionContext(executor, nil)

	if ec == nil {
		t.Fatalf("execution context is nil")
	}

	// Should default to NoOpRetriever
	_, ok := ec.KnowledgeRetriever.(*NoOpRetriever)
	if !ok {
		t.Errorf("expected NoOpRetriever, got %T", ec.KnowledgeRetriever)
	}
}

func TestExecuteWithKnowledgeDisabled(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	retriever := &NoOpRetriever{}
	ec := NewExecutionContext(executor, retriever)

	plan := &DispatchPlan{
		TaskID:         "TASK-001",
		Task:           "Test task",
		Classification: "internal",
		Agents: AgentGroups{
			Primary: []string{"primary-1"},
		},
		KnowledgeContext: KnowledgeContext{
			Enabled: false,
		},
	}

	result, err := ec.ExecuteWithKnowledge(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatalf("result is nil")
	}

	if ec.RetrievedKnowledge.Status != "disabled" {
		t.Errorf("expected status disabled, got %q", ec.RetrievedKnowledge.Status)
	}
}

func TestExecuteWithKnowledgeEnabled(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)

	knowledge := &RetrievedKnowledge{
		Status:         "success",
		Classification: "high",
		TotalCount:     2,
		Passages: []KnowledgePassage{
			{ID: "p1", Source: "docs", Content: "Some doc"},
			{ID: "p2", Source: "chat", Content: "Some chat"},
		},
	}

	retriever := &mockKnowledgeRetriever{knowledge: knowledge}
	ec := NewExecutionContext(executor, retriever)

	plan := &DispatchPlan{
		TaskID:         "TASK-001",
		Task:           "Test task",
		Classification: "internal",
		Agents: AgentGroups{
			Primary: []string{"primary-1"},
		},
		KnowledgeContext: KnowledgeContext{
			Enabled:         true,
			Sources:         []string{"docs", "chat"},
			Classifications: []string{"high"},
			TopK:            10,
		},
	}

	result, err := ec.ExecuteWithKnowledge(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatalf("result is nil")
	}

	if ec.RetrievedKnowledge.Status != "success" {
		t.Errorf("expected status success, got %q", ec.RetrievedKnowledge.Status)
	}

	if ec.RetrievedKnowledge.TotalCount != 2 {
		t.Errorf("expected 2 passages, got %d", ec.RetrievedKnowledge.TotalCount)
	}
}

func TestBuildAgentInjections(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)

	knowledge := &RetrievedKnowledge{
		Status:   "success",
		Passages: []KnowledgePassage{{ID: "p1", Source: "docs"}},
	}

	retriever := &mockKnowledgeRetriever{knowledge: knowledge}
	ec := NewExecutionContext(executor, retriever)

	plan := &DispatchPlan{
		Agents: AgentGroups{
			Primary:   []string{"primary-1", "primary-2"},
			Reviewers: []string{"reviewer-1"},
			Support:   []string{"support-1"},
		},
	}

	ec.RetrievedKnowledge = knowledge
	ec.buildAgentInjections(plan)

	if len(ec.AgentInjections) != 4 {
		t.Errorf("expected 4 injections, got %d", len(ec.AgentInjections))
	}

	// Check each agent got an injection with correct role
	if inj, ok := ec.AgentInjections["primary-1"]; !ok || inj.Role != "primary" {
		t.Errorf("primary-1 injection missing or role incorrect")
	}

	if inj, ok := ec.AgentInjections["reviewer-1"]; !ok || inj.Role != "reviewer" {
		t.Errorf("reviewer-1 injection missing or role incorrect")
	}

	if inj, ok := ec.AgentInjections["support-1"]; !ok || inj.Role != "support" {
		t.Errorf("support-1 injection missing or role incorrect")
	}
}

func TestGetAgentContext(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	ec := NewExecutionContext(executor, &NoOpRetriever{})

	injection := &KnowledgeInjection{
		AgentID: "agent-1",
		Role:    "primary",
	}

	ec.AgentInjections["agent-1"] = injection

	retrieved := ec.GetAgentContext("agent-1")
	if retrieved != injection {
		t.Errorf("injection mismatch")
	}

	notFound := ec.GetAgentContext("agent-2")
	if notFound != nil {
		t.Errorf("expected nil for missing agent, got %v", notFound)
	}
}

func TestSummarizeAgentContext(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	ec := NewExecutionContext(executor, &NoOpRetriever{})

	knowledge := &RetrievedKnowledge{
		Status: "success",
		Passages: []KnowledgePassage{
			{Source: "docs", Classification: "high"},
			{Source: "chat", Classification: "medium"},
			{Source: "docs", Classification: "high"},
		},
	}

	injection := &KnowledgeInjection{
		AgentID:   "agent-1",
		Role:      "primary",
		Knowledge: knowledge,
	}

	ec.AgentInjections["agent-1"] = injection

	summary := ec.SummarizeAgentContext("agent-1")

	if !summary.KnowledgeAvailable {
		t.Errorf("expected knowledge to be available")
	}

	if summary.PassageCount != 3 {
		t.Errorf("expected 3 passages, got %d", summary.PassageCount)
	}

	if len(summary.Sources) != 2 {
		t.Errorf("expected 2 unique sources, got %d", len(summary.Sources))
	}

	if len(summary.Classifications) != 2 {
		t.Errorf("expected 2 unique classifications, got %d", len(summary.Classifications))
	}
}

func TestSummarizeAgentContextMissing(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	ec := NewExecutionContext(executor, &NoOpRetriever{})

	summary := ec.SummarizeAgentContext("agent-1")

	if summary == nil {
		t.Fatalf("summary is nil")
	}

	if summary.KnowledgeAvailable {
		t.Errorf("expected knowledge to be unavailable for missing agent")
	}
}

func TestExecuteWithKnowledgeNilPlan(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	ec := NewExecutionContext(executor, &NoOpRetriever{})

	_, err := ec.ExecuteWithKnowledge(context.Background(), nil)
	if err == nil {
		t.Errorf("expected error for nil plan")
	}
}

func TestExecuteWithKnowledgeInvalidConfig(t *testing.T) {
	executor := NewExecutor(&mockAgentRunner{}, 4, 5*time.Second)
	ec := NewExecutionContext(executor, &NoOpRetriever{})

	plan := &DispatchPlan{
		KnowledgeContext: KnowledgeContext{
			Enabled: true,
			TopK:    100, // Invalid: > 20
		},
	}

	_, err := ec.ExecuteWithKnowledge(context.Background(), plan)
	if err == nil {
		t.Errorf("expected error for invalid knowledge config")
	}
}
