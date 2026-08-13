package orchestration

import (
	"fmt"
	"sort"
	"strings"
)

// ConsolidatedResult represents a unified view of execution results.
type ConsolidatedResult struct {
	ExecutionResult *ExecutionResult
	Summary         *ExecutionSummary
	Findings        []Finding
	Errors          []ExecutionError
}

// ExecutionSummary provides high-level statistics about execution.
type ExecutionSummary struct {
	TotalAgents     int
	SuccessfulCount int
	FailedCount     int
	SkippedCount    int
	WarningCount    int
	CriticalCount   int
	ExecutionTime   string
	Status          string // "completed", "partial", "failed"
}

// Finding represents a consolidated finding from agent execution.
type Finding struct {
	AgentID        string
	Role           string
	Severity       string // "critical", "high", "medium", "low", "info"
	Title          string
	Description    string
	Recommendation string
	Evidence       string
}

// ExecutionError represents an error from execution.
type ExecutionError struct {
	AgentID   string
	ErrorType string // "timeout", "failure", "panic", "invalid_output"
	Message   string
}

// ConsolidateResults creates a unified view of execution results.
func ConsolidateResults(execResult *ExecutionResult) *ConsolidatedResult {
	if execResult == nil {
		return nil
	}

	cr := &ConsolidatedResult{
		ExecutionResult: execResult,
		Findings:        []Finding{},
		Errors:          []ExecutionError{},
	}

	// Build summary
	cr.Summary = &ExecutionSummary{
		TotalAgents:     len(execResult.AgentResults),
		SuccessfulCount: execResult.SuccessCount,
		FailedCount:     execResult.TotalErrors,
		ExecutionTime:   execResult.Duration.String(),
	}

	// Count skipped agents
	for _, res := range execResult.AgentResults {
		if res.Status == "skipped" {
			cr.Summary.SkippedCount++
		}
	}

	// Determine overall status
	switch {
	case execResult.TotalErrors == 0:
		cr.Summary.Status = "completed"
	case execResult.SuccessCount > 0:
		cr.Summary.Status = "partial"
	default:
		cr.Summary.Status = "failed"
	}

	// Extract findings from agent results
	for agentID, res := range execResult.AgentResults {
		if res == nil {
			continue
		}

		// Parse findings from output (placeholder)
		findings := cr.extractFindings(agentID, res)
		cr.Findings = append(cr.Findings, findings...)

		// Collect execution errors
		if res.Status == "failed" || res.Status == "timeout" {
			cr.Errors = append(cr.Errors, ExecutionError{
				AgentID:   agentID,
				ErrorType: res.Status,
				Message:   res.Error,
			})
		}
	}

	// Sort findings by severity
	sort.Slice(cr.Findings, func(i, j int) bool {
		return severityRank(cr.Findings[i].Severity) > severityRank(cr.Findings[j].Severity)
	})

	return cr
}

// extractFindings parses agent output to extract structured findings.
// This is a placeholder that would parse agent-specific output formats.
func (cr *ConsolidatedResult) extractFindings(agentID string, res *AgentResult) []Finding {
	var findings []Finding

	// Placeholder: just create a finding if the agent produced output
	if res.Output != "" {
		findings = append(findings, Finding{
			AgentID:     agentID,
			Role:        res.Role,
			Severity:    "info",
			Title:       fmt.Sprintf("Output from %s", agentID),
			Description: truncateString(res.Output, 200),
		})
	}

	return findings
}

// TextSummary renders the execution summary as human-readable text.
func (cr *ConsolidatedResult) TextSummary() string {
	if cr.Summary == nil {
		return ""
	}

	var sb strings.Builder

	sb.WriteString("═══════════════════════════════════════════════════════════════\n")
	sb.WriteString("Dispatch Execution Summary\n")
	sb.WriteString("═══════════════════════════════════════════════════════════════\n\n")

	fmt.Fprintf(&sb, "Status:        %s\n", cr.Summary.Status)
	fmt.Fprintf(&sb, "Total Agents:  %d\n", cr.Summary.TotalAgents)
	fmt.Fprintf(&sb, "Successful:    %d\n", cr.Summary.SuccessfulCount)
	fmt.Fprintf(&sb, "Failed:        %d\n", cr.Summary.FailedCount)
	fmt.Fprintf(&sb, "Skipped:       %d\n", cr.Summary.SkippedCount)
	fmt.Fprintf(&sb, "Duration:      %s\n\n", cr.Summary.ExecutionTime)

	if len(cr.Errors) > 0 {
		sb.WriteString("Errors:\n")
		for _, err := range cr.Errors {
			fmt.Fprintf(&sb, "  - %s (%s): %s\n", err.AgentID, err.ErrorType, err.Message)
		}
		sb.WriteString("\n")
	}

	if len(cr.Findings) > 0 {
		sb.WriteString("Findings:\n")
		for _, finding := range cr.Findings {
			fmt.Fprintf(&sb, "  [%s] %s (%s)\n", finding.Severity, finding.Title, finding.AgentID)
			if finding.Description != "" {
				fmt.Fprintf(&sb, "      %s\n", finding.Description)
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("═══════════════════════════════════════════════════════════════\n")

	return sb.String()
}

// severityRank returns a numeric rank for severity ordering.
func severityRank(severity string) int {
	switch strings.ToLower(severity) {
	case "critical":
		return 5
	case "high":
		return 4
	case "medium":
		return 3
	case "low":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
}

// truncateString limits a string to a maximum length.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
