package orchestration

import (
	"testing"
	"time"
)

func TestConsolidateResultsNilInput(t *testing.T) {
	result := ConsolidateResults(nil)
	if result != nil {
		t.Errorf("ConsolidateResults(nil) should return nil, got %v", result)
	}
}

func TestConsolidateResultsBasic(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{
			TaskID:    "TASK-001",
			Task:      "Test task",
			Agents:    AgentGroups{Primary: []string{"agent1", "agent2"}},
			CreatedAt: time.Now(),
		},
		AgentResults: map[string]*AgentResult{
			"agent1": {
				AgentID:     "agent1",
				Status:      "success",
				Findings:    []string{"Finding 1", "Finding 2"},
				StartedAt:   time.Now(),
				CompletedAt: time.Now().Add(1 * time.Second),
				Duration:    1 * time.Second,
			},
			"agent2": {
				AgentID:     "agent2",
				Status:      "success",
				Findings:    []string{"Finding 1"},
				StartedAt:   time.Now(),
				CompletedAt: time.Now().Add(1 * time.Second),
				Duration:    1 * time.Second,
			},
		},
		ExecutedAt:  time.Now(),
		CompletedAt: time.Now().Add(2 * time.Second),
		Duration:    2 * time.Second,
	}

	result := ConsolidateResults(execResult)
	if result == nil {
		t.Fatalf("ConsolidateResults returned nil")
	}

	if result.TaskID != "TASK-001" {
		t.Errorf("TaskID = %q, want TASK-001", result.TaskID)
	}

	if result.TotalAgents != 2 {
		t.Errorf("TotalAgents = %d, want 2", result.TotalAgents)
	}

	if result.SuccessfulAgents != 2 {
		t.Errorf("SuccessfulAgents = %d, want 2", result.SuccessfulAgents)
	}

	if len(result.Findings) == 0 {
		t.Errorf("Findings should not be empty")
	}

	if result.QualityScore < 0 || result.QualityScore > 1 {
		t.Errorf("QualityScore = %f, want 0-1", result.QualityScore)
	}
}

func TestBuildAgentMetrics(t *testing.T) {
	result := &AgentResult{
		AgentID:     "test-agent",
		Status:      "success",
		Findings:    []string{"Critical issue", "High priority finding"},
		Duration:    5 * time.Second,
		StartedAt:   time.Now(),
		CompletedAt: time.Now().Add(5 * time.Second),
	}

	metrics := buildAgentMetrics("test-agent", result)
	if metrics.AgentID != "test-agent" {
		t.Errorf("AgentID = %q, want test-agent", metrics.AgentID)
	}

	if metrics.ExecutionTime != 5*time.Second {
		t.Errorf("ExecutionTime = %v, want 5s", metrics.ExecutionTime)
	}

	if metrics.FindingCount != 2 {
		t.Errorf("FindingCount = %d, want 2", metrics.FindingCount)
	}

	if metrics.QualityScore < 0.9 {
		t.Errorf("QualityScore = %f, want >= 0.9", metrics.QualityScore)
	}
}

func TestExtractFindings(t *testing.T) {
	result := &AgentResult{
		AgentID: "agent1",
		Status:  "success",
		Findings: []string{
			"Critical: SQL injection vulnerability",
			"High: Unvalidated input",
			"Medium: Missing error handling",
		},
	}

	findings := extractFindings("agent1", result)
	if len(findings) != 3 {
		t.Errorf("extractFindings returned %d findings, want 3", len(findings))
	}

	if findings[0].Severity != "critical" {
		t.Errorf("First finding severity = %q, want critical", findings[0].Severity)
	}
}

func TestConsolidateFindings(t *testing.T) {
	findings := []Finding{
		{ID: "1", Severity: "critical", Description: "Issue A", Count: 1, AgentIDs: []string{"agent1"}},
		{ID: "2", Severity: "critical", Description: "Issue A", Count: 1, AgentIDs: []string{"agent2"}},
		{ID: "3", Severity: "high", Description: "Issue B", Count: 1, AgentIDs: []string{"agent1"}},
	}

	consolidated := consolidateFindings(findings)

	// Should merge the two "Issue A" findings
	if len(consolidated) != 2 {
		t.Errorf("consolidateFindings returned %d findings, want 2", len(consolidated))
	}

	// First should be critical with both agents
	if consolidated[0].Severity != "critical" {
		t.Errorf("First consolidated finding severity = %q, want critical", consolidated[0].Severity)
	}

	if len(consolidated[0].AgentIDs) != 2 {
		t.Errorf("Merged finding agent count = %d, want 2", len(consolidated[0].AgentIDs))
	}
}

func TestDetectConflicts(t *testing.T) {
	agentMetrics := map[string]*AgentMetrics{
		"agent1": {FindingCount: 1},
		"agent2": {FindingCount: 10},
	}

	conflicts := detectConflicts(agentMetrics, []Finding{})
	if len(conflicts) == 0 {
		t.Errorf("Should detect conflict between agents with different finding counts")
	}

	if !stringContains(conflicts[0].Description, "mismatch") {
		t.Errorf("Conflict description should mention mismatch")
	}
}

func TestCalculateCoverage(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{
			ChangedFiles: []string{"file1.go", "file2.go", "file3.go"},
			Agents:       AgentGroups{Primary: []string{"agent1"}},
		},
	}

	findings := []Finding{
		{ID: "1", Severity: "high", Description: "Issue 1"},
		{ID: "2", Severity: "medium", Description: "Issue 2"},
	}

	coverage := calculateCoverage(findings, execResult)
	if coverage.FilesAnalyzed != 3 {
		t.Errorf("FilesAnalyzed = %d, want 3", coverage.FilesAnalyzed)
	}

	if coverage.CompletionPercent != 100.0 {
		t.Errorf("CompletionPercent = %f, want 100", coverage.CompletionPercent)
	}

	if coverage.DepthScore < 0 || coverage.DepthScore > 1 {
		t.Errorf("DepthScore = %f, want 0-1", coverage.DepthScore)
	}
}

func TestCalculateQualityScore(t *testing.T) {
	result := &ConsolidatedResult{
		TotalAgents:      4,
		SuccessfulAgents: 3,
		Findings:         make([]Finding, 5),
		Conflicts:        make([]Conflict, 1),
		Coverage: CoverageMetrics{
			DepthScore: 0.8,
		},
	}

	score := calculateQualityScore(result)
	if score < 0 || score > 1 {
		t.Errorf("calculateQualityScore returned %f, want 0-1", score)
	}

	if score < 0.5 {
		t.Errorf("Quality score too low: %f", score)
	}
}

func TestConsolidatedResultSummary(t *testing.T) {
	result := &ConsolidatedResult{
		TaskID:           "TASK-001",
		Classification:   "high",
		TotalAgents:      3,
		SuccessfulAgents: 3,
		FailedAgents:     0,
		SkippedAgents:    0,
		Findings:         make([]Finding, 5),
		Conflicts:        make([]Conflict, 0),
		QualityScore:     0.85,
		Duration:         2 * time.Second,
		Coverage: CoverageMetrics{
			CompletionPercent: 100.0,
			DepthScore:        0.75,
		},
	}

	summary := result.Summary()
	if len(summary) == 0 {
		t.Errorf("Summary should not be empty")
	}

	if !stringContains(summary, "TASK-001") {
		t.Errorf("Summary should contain task ID")
	}

	if !stringContains(summary, "3 executed") {
		t.Errorf("Summary should show agent count")
	}
}

// stringContains checks if a string stringContains a substring
func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
