package orchestration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewResultFormatter(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{TaskID: "TASK-001"},
		AgentResults: map[string]*AgentResult{
			"agent-1": {
				AgentID: "agent-1",
				Status:  "success",
			},
		},
		SuccessCount: 1,
	}

	formatter := NewResultFormatter(execResult)
	if formatter == nil {
		t.Fatalf("formatter is nil")
	}

	if formatter.execution != execResult {
		t.Errorf("execution mismatch")
	}

	if formatter.consolidated == nil {
		t.Errorf("consolidated result is nil")
	}
}

func TestFormatJSON(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{TaskID: "TASK-001"},
		AgentResults: map[string]*AgentResult{
			"agent-1": {AgentID: "agent-1", Status: "success"},
		},
		SuccessCount: 1,
		TotalErrors:  0,
	}

	formatter := NewResultFormatter(execResult)

	// Compact JSON
	jsonStr, err := formatter.FormatJSON(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if jsonStr == "" {
		t.Errorf("JSON output is empty")
	}

	// Verify it's valid JSON
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		t.Errorf("invalid JSON output: %v", err)
	}

	// Pretty JSON
	prettyStr, err := formatter.FormatJSON(true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(prettyStr, "\n") {
		t.Errorf("pretty JSON should contain newlines")
	}

	if !strings.Contains(prettyStr, "  ") {
		t.Errorf("pretty JSON should be indented")
	}
}

func TestFormatMarkdown(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{
			TaskID: "TASK-001",
			Agents: AgentGroups{
				Primary:   []string{"primary-1"},
				Reviewers: []string{"reviewer-1"},
			},
		},
		AgentResults: map[string]*AgentResult{
			"primary-1": {
				AgentID:     "primary-1",
				Role:        "primary",
				Status:      "success",
				Duration:    1 * time.Second,
				StartedAt:   time.Now(),
				CompletedAt: time.Now(),
			},
			"reviewer-1": {
				AgentID:     "reviewer-1",
				Role:        "reviewer",
				Status:      "success",
				Duration:    2 * time.Second,
				StartedAt:   time.Now(),
				CompletedAt: time.Now(),
			},
		},
		ExecutedAt:   time.Now(),
		CompletedAt:  time.Now(),
		SuccessCount: 2,
		TotalErrors:  0,
	}

	formatter := NewResultFormatter(execResult)
	md := formatter.FormatMarkdown()

	if !strings.Contains(md, "# Cadre Orchestration Report") {
		t.Errorf("markdown missing main title")
	}

	if !strings.Contains(md, "## Statistics") {
		t.Errorf("markdown missing statistics section")
	}

	if !strings.Contains(md, "## Agent Execution Results") {
		t.Errorf("markdown missing agent results section")
	}

	if !strings.Contains(md, "primary-1") {
		t.Errorf("markdown missing agent name")
	}
}

func TestFormatText(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{
			TaskID: "TASK-001",
			Agents: AgentGroups{Primary: []string{"agent-1"}},
		},
		AgentResults: map[string]*AgentResult{
			"agent-1": {AgentID: "agent-1", Status: "success"},
		},
		SuccessCount: 1,
	}

	formatter := NewResultFormatter(execResult)
	text := formatter.FormatText()

	if text == "" {
		t.Errorf("text output is empty")
	}

	if !strings.Contains(text, "Consolidation Summary") {
		t.Errorf("text missing expected content")
	}
}

func TestFormatSummary(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{
			TaskID: "TASK-001",
			Agents: AgentGroups{
				Primary:   []string{"p1", "p2"},
				Reviewers: []string{"r1"},
			},
		},
		AgentResults: map[string]*AgentResult{
			"p1": {AgentID: "p1", Status: "success"},
			"p2": {AgentID: "p2", Status: "success"},
			"r1": {AgentID: "r1", Status: "failed"},
		},
		SuccessCount: 2,
		TotalErrors:  1,
	}

	formatter := NewResultFormatter(execResult)
	summary := formatter.FormatSummary()

	if summary == "" {
		t.Errorf("summary is empty")
	}

	if !strings.Contains(summary, "Quality") {
		t.Errorf("summary missing 'Quality'")
	}

	if !strings.Contains(summary, "2") {
		t.Errorf("summary missing success count")
	}
}

func TestGenerateReportCard(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{
			TaskID: "TASK-001",
			Agents: AgentGroups{
				Primary:   []string{"p1"},
				Reviewers: []string{"r1"},
				Support:   []string{"s1"},
			},
		},
		AgentResults: map[string]*AgentResult{
			"p1": {AgentID: "p1", Status: "success"},
			"r1": {AgentID: "r1", Status: "success"},
			"s1": {AgentID: "s1", Status: "failed"},
		},
		ExecutedAt:   time.Now(),
		CompletedAt:  time.Now(),
		SuccessCount: 2,
		TotalErrors:  1,
	}

	formatter := NewResultFormatter(execResult)
	card := formatter.GenerateReportCard()

	if card == nil {
		t.Fatalf("report card is nil")
	}

	if card.TaskID != "TASK-001" {
		t.Errorf("task ID mismatch")
	}

	if card.AgentCount != 3 {
		t.Errorf("expected 3 agents, got %d", card.AgentCount)
	}

	if card.SuccessCount != 2 {
		t.Errorf("expected 2 successful, got %d", card.SuccessCount)
	}

	if card.FailedCount != 1 {
		t.Errorf("expected 1 failed, got %d", card.FailedCount)
	}

	if len(card.PrimaryAgents) != 1 || card.PrimaryAgents[0] != "p1" {
		t.Errorf("primary agents mismatch")
	}
}

func TestExportResults(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{TaskID: "TASK-001"},
		AgentResults: map[string]*AgentResult{
			"agent-1": {AgentID: "agent-1", Status: "success"},
		},
		SuccessCount: 1,
	}

	formatter := NewResultFormatter(execResult)

	tests := []struct {
		format        string
		shouldSucceed bool
		shouldContain string
	}{
		{"json", true, "{"},
		{"json-pretty", true, "{"},
		{"markdown", true, "#"},
		{"text", true, "Agent"},
		{"summary", true, "Quality"},
		{"invalid", false, ""},
	}

	for _, tt := range tests {
		result, err := formatter.ExportResults(tt.format)
		if (err != nil) != !tt.shouldSucceed {
			t.Errorf("ExportResults(%q) error=%v, expected success=%v", tt.format, err, tt.shouldSucceed)
		}

		if tt.shouldSucceed && !strings.Contains(result, tt.shouldContain) {
			t.Errorf("ExportResults(%q) missing %q in output", tt.format, tt.shouldContain)
		}
	}
}

func TestTruncateForTable(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world test", 10, "hello w..."},
		{"test", 4, "test"},
	}

	for _, tt := range tests {
		result := truncateForTable(tt.input, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncateForTable(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
		}
	}
}

func TestFormatMarkdownWithFindings(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{TaskID: "TASK-001"},
		AgentResults: map[string]*AgentResult{
			"agent-1": {AgentID: "agent-1", Status: "success"},
		},
		SuccessCount: 1,
	}

	formatter := NewResultFormatter(execResult)

	// Manually add findings to consolidated result
	formatter.consolidated.Findings = []Finding{
		{
			ID:          "f1",
			Severity:    "critical",
			Description: "Security issue found: Missing input validation",
			AgentIDs:    []string{"agent-1"},
			Confidence:  0.9,
			FirstSeen:   "agent-1",
			Count:       1,
		},
		{
			ID:          "f2",
			Severity:    "high",
			Description: "Code quality issue: Function too complex",
			AgentIDs:    []string{"agent-1"},
			Confidence:  0.8,
			FirstSeen:   "agent-1",
			Count:       1,
		},
	}

	md := formatter.FormatMarkdown()

	if !strings.Contains(md, "## Findings") {
		t.Errorf("markdown missing findings section")
	}

	if !strings.Contains(md, "CRITICAL") {
		t.Errorf("markdown missing critical severity")
	}

	if !strings.Contains(md, "Security issue found") {
		t.Errorf("markdown missing finding description")
	}
}
