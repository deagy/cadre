package orchestration

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ResultFormatter formats execution results in different output formats.
type ResultFormatter struct {
	execution    *ExecutionResult
	consolidated *ConsolidatedResult
}

// NewResultFormatter creates a formatter for execution results.
func NewResultFormatter(execution *ExecutionResult) *ResultFormatter {
	consolidated := ConsolidateResults(execution)
	return &ResultFormatter{
		execution:    execution,
		consolidated: consolidated,
	}
}

// FormatJSON serializes the execution result as JSON.
func (rf *ResultFormatter) FormatJSON(pretty bool) (string, error) {
	var data []byte
	var err error

	if pretty {
		data, err = json.MarshalIndent(rf.execution, "", "  ")
	} else {
		data, err = json.Marshal(rf.execution)
	}

	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return string(data), nil
}

// FormatMarkdown generates a markdown report of execution results.
func (rf *ResultFormatter) FormatMarkdown() string {
	if rf.consolidated == nil {
		return "# Execution Report\n\nNo results available.\n"
	}

	var sb strings.Builder

	sb.WriteString("# Cadre Orchestration Report\n\n")

	// Execution summary
	fmt.Fprintf(&sb, "**Quality Score:** %.2f%%  \n", rf.consolidated.QualityScore*100)
	fmt.Fprintf(&sb, "**Executed:** %s  \n", rf.consolidated.ExecutedAt.Format(time.RFC3339))
	fmt.Fprintf(&sb, "**Duration:** %v  \n", rf.consolidated.Duration)
	sb.WriteString("\n")

	// Statistics
	sb.WriteString("## Statistics\n\n")
	sb.WriteString("| Metric | Count |\n")
	sb.WriteString("|--------|-------|\n")
	fmt.Fprintf(&sb, "| Total Agents | %d |\n", rf.consolidated.TotalAgents)
	fmt.Fprintf(&sb, "| Successful | %d |\n", rf.consolidated.SuccessfulAgents)
	fmt.Fprintf(&sb, "| Failed | %d |\n", rf.consolidated.FailedAgents)
	fmt.Fprintf(&sb, "| Skipped | %d |\n", rf.consolidated.SkippedAgents)
	sb.WriteString("\n")

	// Agent results
	sb.WriteString("## Agent Execution Results\n\n")
	rf.formatAgentResults(&sb)

	// Findings
	if len(rf.consolidated.Findings) > 0 {
		sb.WriteString("## Findings\n\n")
		rf.formatFindings(&sb)
	}

	// Consolidation Errors
	if len(rf.consolidated.ConsolidationErrors) > 0 {
		sb.WriteString("## Consolidation Errors\n\n")
		for _, err := range rf.consolidated.ConsolidationErrors {
			fmt.Fprintf(&sb, "- %s\n", err)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// formatAgentResults formats agent execution details as a markdown table.
func (rf *ResultFormatter) formatAgentResults(sb *strings.Builder) {
	sb.WriteString("| Agent | Role | Status | Duration |\n")
	sb.WriteString("|-------|------|--------|----------|\n")

	// Sort agents for consistent output
	agents := make([]string, 0, len(rf.execution.AgentResults))
	for id := range rf.execution.AgentResults {
		agents = append(agents, id)
	}
	sort.Strings(agents)

	for _, agentID := range agents {
		result := rf.execution.AgentResults[agentID]
		if result == nil {
			continue
		}

		duration := ""
		if result.Duration > 0 {
			duration = result.Duration.String()
		}

		fmt.Fprintf(sb, "| `%s` | %s | %s | %s |\n",
			agentID, result.Role, result.Status, duration)
	}
	sb.WriteString("\n")
}

// formatFindings formats consolidated findings as markdown sections.
func (rf *ResultFormatter) formatFindings(sb *strings.Builder) {
	bySeverity := make(map[string][]Finding)
	for _, finding := range rf.consolidated.Findings {
		bySeverity[finding.Severity] = append(bySeverity[finding.Severity], finding)
	}

	severityOrder := []string{"critical", "high", "medium", "low", "info"}
	for _, severity := range severityOrder {
		findings, ok := bySeverity[severity]
		if !ok || len(findings) == 0 {
			continue
		}

		fmt.Fprintf(sb, "### %s Findings (%d)\n\n", strings.ToUpper(severity), len(findings))
		for _, finding := range findings {
			fmt.Fprintf(sb, "#### %s\n", finding.Description)
			fmt.Fprintf(sb, "**ID:** `%s`  \n", finding.ID)
			fmt.Fprintf(sb, "**Reported by:** %v  \n", finding.AgentIDs)
			fmt.Fprintf(sb, "**Confidence:** %.0f%%  \n", finding.Confidence*100)
			fmt.Fprintf(sb, "**Agreement:** %d agent(s)  \n", finding.Count)
			sb.WriteString("\n")
		}
	}
}

// FormatText generates a human-readable text report.
func (rf *ResultFormatter) FormatText() string {
	if rf.consolidated == nil {
		return "No results available.\n"
	}

	return rf.consolidated.Summary()
}

// FormatSummary generates a brief summary suitable for logs or status messages.
func (rf *ResultFormatter) FormatSummary() string {
	if rf.consolidated == nil {
		return "Execution completed with no results."
	}

	return fmt.Sprintf(
		"Quality Score: %.1f%% | Agents: %d/%d successful | Findings: %d | Duration: %v",
		rf.consolidated.QualityScore*100,
		rf.consolidated.SuccessfulAgents,
		rf.consolidated.TotalAgents,
		len(rf.consolidated.Findings),
		rf.consolidated.Duration,
	)
}

// ReportCard generates a detailed execution report card.
type ReportCard struct {
	TaskID         string
	Status         string
	AgentCount     int
	SuccessCount   int
	FailedCount    int
	FindingCount   int
	CriticalCount  int
	HighCount      int
	Duration       string
	ExecutedAt     string
	CompletedAt    string
	PrimaryAgents  []string
	ReviewerAgents []string
	SupportAgents  []string
}

// GenerateReportCard creates a structured execution report card.
func (rf *ResultFormatter) GenerateReportCard() *ReportCard {
	if rf.execution == nil || rf.consolidated == nil {
		return nil
	}

	card := &ReportCard{
		TaskID:         rf.consolidated.TaskID,
		Status:         fmt.Sprintf("Quality: %.0f%%", rf.consolidated.QualityScore*100),
		AgentCount:     rf.consolidated.TotalAgents,
		SuccessCount:   rf.consolidated.SuccessfulAgents,
		FailedCount:    rf.consolidated.FailedAgents,
		FindingCount:   len(rf.consolidated.Findings),
		Duration:       fmt.Sprintf("%v", rf.consolidated.Duration),
		ExecutedAt:     rf.consolidated.ExecutedAt.Format(time.RFC3339),
		CompletedAt:    rf.consolidated.CompletedAt.Format(time.RFC3339),
		PrimaryAgents:  append([]string{}, rf.execution.Plan.Agents.Primary...),
		ReviewerAgents: append([]string{}, rf.execution.Plan.Agents.Reviewers...),
		SupportAgents:  append([]string{}, rf.execution.Plan.Agents.Support...),
	}

	// Count critical and high findings
	for _, finding := range rf.consolidated.Findings {
		switch finding.Severity {
		case "critical":
			card.CriticalCount++
		case "high":
			card.HighCount++
		}
	}

	return card
}

// truncateForTable truncates a string for table display without breaking formatting.
func truncateForTable(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// ExportResults exports results in a specified format.
func (rf *ResultFormatter) ExportResults(format string) (string, error) {
	switch strings.ToLower(format) {
	case "json":
		return rf.FormatJSON(false)
	case "json-pretty", "json-indent":
		return rf.FormatJSON(true)
	case "markdown", "md":
		return rf.FormatMarkdown(), nil
	case "text", "txt":
		return rf.FormatText(), nil
	case "summary":
		return rf.FormatSummary(), nil
	default:
		return "", fmt.Errorf("unsupported format: %q (supported: json, markdown, text, summary)", format)
	}
}
