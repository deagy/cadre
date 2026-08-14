package orchestration

import (
	"encoding/json"
	"testing"
)

func TestBuildDispatchPlan(t *testing.T) {
	matches := []RouteMatch{
		{
			RouteID:   "backend-route",
			Primary:   []string{"backend-engineer"},
			Reviewers: []string{"code-reviewer"},
			Support:   []string{},
			Reasons: map[string]string{
				"keyword_match": "task contains 'API'",
			},
		},
	}

	routing := &RoutingConfig{
		Routes: []Route{},
		RiskRules: []Risk{
			{
				ID:    "risk-1",
				Level: "high",
			},
		},
	}

	plan, err := BuildDispatchPlan(
		"TASK-001",
		"Implement REST API endpoint for user authentication",
		[]string{"src/api/auth.go"},
		"internal",
		matches,
		routing,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan == nil {
		t.Fatalf("plan is nil")
	}

	if plan.TaskID != "TASK-001" {
		t.Errorf("task ID mismatch: got %q", plan.TaskID)
	}

	if len(plan.Agents.Primary) != 1 {
		t.Errorf("expected 1 primary agent, got %d", len(plan.Agents.Primary))
	}

	if plan.Agents.Primary[0] != "backend-engineer" {
		t.Errorf("expected backend-engineer, got %q", plan.Agents.Primary[0])
	}

	if len(plan.Agents.Reviewers) != 1 {
		t.Errorf("expected 1 reviewer, got %d", len(plan.Agents.Reviewers))
	}

	if plan.DispatchDisposition.Status != "staffed" {
		t.Errorf("expected staffed status, got %q", plan.DispatchDisposition.Status)
	}

	if plan.DispatchDisposition.HasPrimary == false {
		t.Errorf("expected HasPrimary to be true")
	}

	if plan.Workflow == "" {
		t.Errorf("workflow is empty")
	}
}

func TestBuildDispatchPlanCritical(t *testing.T) {
	matches := []RouteMatch{
		{
			RouteID:   "critical-route",
			Primary:   []string{"chief-architect"},
			Reviewers: []string{"security-reviewer", "compliance-reviewer"},
		},
	}

	routing := &RoutingConfig{
		Routes:    []Route{},
		RiskRules: []Risk{},
	}

	plan, err := BuildDispatchPlan(
		"CRIT-001",
		"Security vulnerability in production",
		[]string{"src/auth/session.go"},
		"critical",
		matches,
		routing,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.Workflow != "critical-path" {
		t.Errorf("expected critical-path workflow, got %q", plan.Workflow)
	}

	if len(plan.HumanGates) == 0 {
		t.Errorf("expected human gates for critical classification")
	}

	if plan.KnowledgeContext.TopK != 10 {
		t.Errorf("expected TopK=10 for critical, got %d", plan.KnowledgeContext.TopK)
	}
}

func TestBuildDispatchPlanNoMatches(t *testing.T) {
	routing := &RoutingConfig{
		Routes:    []Route{},
		RiskRules: []Risk{},
	}

	_, err := BuildDispatchPlan(
		"TASK-001",
		"Some task",
		[]string{},
		"internal",
		[]RouteMatch{},
		routing,
	)

	if err == nil {
		t.Fatalf("expected error for no matches")
	}
}

func TestBuildDispatchDisposition(t *testing.T) {
	tests := []struct {
		name              string
		agents            AgentGroups
		expectedStatus    string
		shouldHavePrimary bool
	}{
		{
			name: "fully staffed",
			agents: AgentGroups{
				Primary:   []string{"agent-1"},
				Reviewers: []string{"agent-2"},
			},
			expectedStatus:    "staffed",
			shouldHavePrimary: true,
		},
		{
			name: "only primary",
			agents: AgentGroups{
				Primary: []string{"agent-1"},
			},
			expectedStatus:    "staffed",
			shouldHavePrimary: true,
		},
		{
			name: "advisory only",
			agents: AgentGroups{
				Support: []string{"agent-1"},
			},
			expectedStatus:    "advisory-only",
			shouldHavePrimary: false,
		},
		{
			name:              "no agents",
			agents:            AgentGroups{},
			expectedStatus:    "no-agents-selected",
			shouldHavePrimary: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			disp := buildDispatchDisposition(tt.agents)
			if disp.Status != tt.expectedStatus {
				t.Errorf("expected %q, got %q", tt.expectedStatus, disp.Status)
			}
			if disp.HasPrimary != tt.shouldHavePrimary {
				t.Errorf("HasPrimary: expected %v, got %v", tt.shouldHavePrimary, disp.HasPrimary)
			}
		})
	}
}

func TestPlanJSONMarshal(t *testing.T) {
	plan := &DispatchPlan{
		TaskID:         "TASK-001",
		Task:           "Implement feature",
		Classification: "internal",
		Agents: AgentGroups{
			Primary:   []string{"backend-engineer"},
			Reviewers: []string{"code-reviewer"},
		},
		Workflow: "standard",
		DispatchDisposition: Disposition{
			Status:     "staffed",
			HasPrimary: true,
		},
	}

	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("cannot marshal: %v", err)
	}

	if len(data) == 0 {
		t.Fatalf("marshaled data is empty")
	}

	var unmarshaled DispatchPlan
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("cannot unmarshal: %v", err)
	}

	if unmarshaled.TaskID != plan.TaskID {
		t.Errorf("task ID mismatch after round-trip")
	}
}

func TestPlanText(t *testing.T) {
	plan := &DispatchPlan{
		TaskID:         "TASK-001",
		Task:           "Implement feature",
		Classification: "high",
		Agents: AgentGroups{
			Primary:   []string{"backend-engineer", "api-engineer"},
			Reviewers: []string{"code-reviewer"},
			Support:   []string{"documentation-writer"},
		},
		Workflow: "standard",
		DispatchDisposition: Disposition{
			Status:     "staffed",
			HasPrimary: true,
		},
		QualityGates: []Gate{
			{
				ID:    "code-review",
				Phase: "review",
			},
		},
	}

	text := plan.PlanText()

	if text == "" {
		t.Fatalf("plan text is empty")
	}

	// Verify key sections are present
	expectedContent := []string{
		"Agent Selection Plan",
		"TASK-001",
		"Implement feature",
		"high",
		"backend-engineer",
		"code-reviewer",
		"documentation-writer",
		"staffed",
	}

	for _, expected := range expectedContent {
		if !contains(text, expected) {
			t.Errorf("plan text missing expected content: %q", expected)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
