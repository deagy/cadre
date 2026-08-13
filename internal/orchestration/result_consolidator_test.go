package orchestration

import (
	"strings"
	"testing"
	"time"
)

func TestConsolidateResults(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{
			TaskID: "TASK-001",
		},
		AgentResults: map[string]*AgentResult{
			"primary-1": {
				AgentID:     "primary-1",
				Role:        "primary",
				Status:      "success",
				ExitCode:    0,
				Duration:    1 * time.Second,
				Output:      "Successful execution",
				StartedAt:   time.Now(),
				CompletedAt: time.Now(),
			},
			"reviewer-1": {
				AgentID:     "reviewer-1",
				Role:        "reviewer",
				Status:      "failed",
				ExitCode:    1,
				Duration:    2 * time.Second,
				Error:       "Review failed",
				StartedAt:   time.Now(),
				CompletedAt: time.Now(),
			},
		},
		SuccessCount: 1,
		TotalErrors:  1,
	}

	cr := ConsolidateResults(execResult)

	if cr == nil {
		t.Fatalf("ConsolidateResults returned nil")
	}

	if cr.Summary == nil {
		t.Fatalf("summary is nil")
	}

	if cr.Summary.TotalAgents != 2 {
		t.Errorf("expected 2 agents, got %d", cr.Summary.TotalAgents)
	}

	if cr.Summary.SuccessfulCount != 1 {
		t.Errorf("expected 1 successful, got %d", cr.Summary.SuccessfulCount)
	}

	if cr.Summary.FailedCount != 1 {
		t.Errorf("expected 1 failed, got %d", cr.Summary.FailedCount)
	}

	if cr.Summary.Status != "partial" {
		t.Errorf("expected status partial, got %q", cr.Summary.Status)
	}
}

func TestConsolidateResultsStatus(t *testing.T) {
	tests := []struct {
		name           string
		successCount   int
		totalErrors    int
		expectedStatus string
	}{
		{"all success", 3, 0, "completed"},
		{"partial success", 2, 1, "partial"},
		{"all failed", 0, 3, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execResult := &ExecutionResult{
				Plan:         &DispatchPlan{TaskID: "TASK-001"},
				AgentResults: make(map[string]*AgentResult),
				SuccessCount: tt.successCount,
				TotalErrors:  tt.totalErrors,
			}

			cr := ConsolidateResults(execResult)
			if cr.Summary.Status != tt.expectedStatus {
				t.Errorf("expected %q, got %q", tt.expectedStatus, cr.Summary.Status)
			}
		})
	}
}

func TestSeverityRank(t *testing.T) {
	tests := []struct {
		severity string
		rank     int
	}{
		{"critical", 5},
		{"high", 4},
		{"medium", 3},
		{"low", 2},
		{"info", 1},
		{"unknown", 0},
	}

	for _, tt := range tests {
		rank := severityRank(tt.severity)
		if rank != tt.rank {
			t.Errorf("severityRank(%q) = %d, want %d", tt.severity, rank, tt.rank)
		}
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"test", 4, "test"},
		{"test", 3, "tes..."},
	}

	for _, tt := range tests {
		result := truncateString(tt.input, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncateString(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
		}
	}
}

func TestTextSummary(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{
			TaskID: "TASK-001",
		},
		AgentResults: map[string]*AgentResult{
			"primary-1": {
				AgentID:     "primary-1",
				Role:        "primary",
				Status:      "success",
				Output:      "Executed successfully",
				StartedAt:   time.Now(),
				CompletedAt: time.Now(),
			},
		},
		SuccessCount: 1,
		TotalErrors:  0,
		Duration:     5 * time.Second,
	}

	cr := ConsolidateResults(execResult)
	text := cr.TextSummary()

	expectedContent := []string{
		"Dispatch Execution Summary",
		"Status:",
		"completed",
		"Total Agents:",
		"1",
		"Successful:",
		"Duration:",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(text, expected) {
			t.Errorf("TextSummary missing %q", expected)
		}
	}
}

func TestTextSummaryWithErrors(t *testing.T) {
	execResult := &ExecutionResult{
		Plan: &DispatchPlan{
			TaskID: "TASK-001",
		},
		AgentResults: map[string]*AgentResult{
			"primary-1": {
				AgentID:   "primary-1",
				Status:    "failed",
				Error:     "Execution failed",
				StartedAt: time.Now(),
			},
		},
		SuccessCount: 0,
		TotalErrors:  1,
	}

	cr := ConsolidateResults(execResult)
	text := cr.TextSummary()

	if !strings.Contains(text, "Errors:") {
		t.Errorf("TextSummary missing Errors section")
	}

	if !strings.Contains(text, "primary-1") {
		t.Errorf("TextSummary missing agent ID")
	}
}

func TestConsolidateResultsNil(t *testing.T) {
	cr := ConsolidateResults(nil)
	if cr != nil {
		t.Errorf("expected nil for nil input, got %v", cr)
	}
}
