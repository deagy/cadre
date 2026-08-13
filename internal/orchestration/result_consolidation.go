package orchestration

import (
	"fmt"
	"sort"
	"time"
)

// ConsolidatedResult represents aggregated findings from all agents.
type ConsolidatedResult struct {
	TaskID              string
	Classification      string
	ExecutedAt          time.Time
	CompletedAt         time.Time
	Duration            time.Duration
	TotalAgents         int
	SuccessfulAgents    int
	FailedAgents        int
	SkippedAgents       int
	Findings            []Finding
	Conflicts           []Conflict
	Coverage            CoverageMetrics
	QualityScore        float64
	AgentMetrics        map[string]*AgentMetrics
	ConsolidationErrors []string
}

// Finding represents a consolidated finding across agents.
type Finding struct {
	ID          string   `json:"id"`
	Severity    string   `json:"severity"` // critical, high, medium, low, info
	Category    string   `json:"category"`
	Description string   `json:"description"`
	AgentIDs    []string `json:"agent_ids"`  // which agents reported this
	Confidence  float64  `json:"confidence"` // 0-1 based on agent agreement
	FirstSeen   string   `json:"first_seen_by"`
	Count       int      `json:"count"` // how many agents reported similar
}

// Conflict represents a disagreement between agents.
type Conflict struct {
	AgentA      string
	AgentB      string
	Finding1    string
	Finding2    string
	Description string
}

// CoverageMetrics represents how comprehensively agents covered the task.
type CoverageMetrics struct {
	FilesAnalyzed       int     `json:"files_analyzed"`
	LinesOfCodeReviewed int     `json:"lines_of_code_reviewed"`
	CompletionPercent   float64 `json:"completion_percent"`
	DepthScore          float64 `json:"depth_score"` // 0-1 how deep analysis went
}

// AgentMetrics represents metrics for a single agent's execution.
type AgentMetrics struct {
	AgentID         string
	ExecutionTime   time.Duration
	FindingCount    int
	CriticalCount   int
	HighCount       int
	MediumCount     int
	LowCount        int
	QualityScore    float64
	ReliabilityRate float64 // agreement with other agents
	ExecutionStatus string  // success, failed, timeout, skipped
}

// ConsolidateResults aggregates findings from multiple agent executions.
func ConsolidateResults(result *ExecutionResult) *ConsolidatedResult {
	if result == nil {
		return nil
	}

	consolidated := &ConsolidatedResult{
		TaskID:              result.Plan.TaskID,
		Classification:      result.Plan.Classification,
		ExecutedAt:          result.ExecutedAt,
		CompletedAt:         result.CompletedAt,
		Duration:            result.Duration,
		TotalAgents:         len(result.AgentResults),
		AgentMetrics:        make(map[string]*AgentMetrics),
		ConsolidationErrors: []string{},
	}

	// Process agent results
	var allFindings []Finding
	for agentID, agentResult := range result.AgentResults {
		metrics := buildAgentMetrics(agentID, agentResult)
		consolidated.AgentMetrics[agentID] = metrics

		// Count agent execution statuses
		switch agentResult.Status {
		case "success":
			consolidated.SuccessfulAgents++
		case "failed":
			consolidated.FailedAgents++
		case "skipped", "timeout":
			consolidated.SkippedAgents++
		}

		// Extract findings from agent output
		findings := extractFindings(agentID, agentResult)
		allFindings = append(allFindings, findings...)
	}

	// Consolidate findings by deduplication and confidence scoring
	consolidated.Findings = consolidateFindings(allFindings)

	// Detect conflicts between agent findings
	consolidated.Conflicts = detectConflicts(consolidated.AgentMetrics, allFindings)

	// Calculate coverage metrics
	consolidated.Coverage = calculateCoverage(consolidated.Findings, result)

	// Calculate overall quality score
	consolidated.QualityScore = calculateQualityScore(consolidated)

	return consolidated
}

// buildAgentMetrics creates metrics for a single agent execution.
func buildAgentMetrics(agentID string, result *AgentResult) *AgentMetrics {
	metrics := &AgentMetrics{
		AgentID:         agentID,
		ExecutionTime:   result.Duration,
		FindingCount:    len(result.Findings),
		ExecutionStatus: result.Status,
	}

	// Count findings by severity (if structured)
	for _, finding := range result.Findings {
		switch finding[0:1] {
		case "C":
			metrics.CriticalCount++
		case "H":
			metrics.HighCount++
		case "M":
			metrics.MediumCount++
		case "L":
			metrics.LowCount++
		}
	}

	// Quality score based on findings and execution
	switch result.Status {
	case "success":
		metrics.QualityScore = 1.0
		if metrics.FindingCount > 0 {
			metrics.QualityScore = 0.95
		}
	case "failed":
		metrics.QualityScore = 0.5
	default:
		metrics.QualityScore = 0.0
	}

	return metrics
}

// extractFindings parses findings from agent output.
func extractFindings(agentID string, result *AgentResult) []Finding {
	var findings []Finding

	// Extract from structured findings array
	for i, finding := range result.Findings {
		f := Finding{
			ID:          fmt.Sprintf("%s-%d", agentID, i),
			Description: finding,
			AgentIDs:    []string{agentID},
			Confidence:  0.7,
			FirstSeen:   agentID,
			Count:       1,
		}

		// Infer severity from finding format
		if len(finding) > 0 {
			switch finding[0:1] {
			case "C":
				f.Severity = "critical"
			case "H":
				f.Severity = "high"
			case "M":
				f.Severity = "medium"
			case "L":
				f.Severity = "low"
			default:
				f.Severity = "info"
			}
		}

		findings = append(findings, f)
	}

	return findings
}

// consolidateFindings deduplicates and merges findings across agents.
func consolidateFindings(findings []Finding) []Finding {
	if len(findings) == 0 {
		return []Finding{}
	}

	// Group similar findings
	findingMap := make(map[string]*Finding)

	for _, f := range findings {
		// Use finding description as grouping key
		key := fmt.Sprintf("%s:%s", f.Severity, f.Description)

		if existing, found := findingMap[key]; found {
			// Merge with existing finding
			existing.Count++
			existing.AgentIDs = append(existing.AgentIDs, f.AgentIDs...)
			// Increase confidence based on agreement
			existing.Confidence = min(1.0, existing.Confidence+0.1)
		} else {
			// New finding
			findingMap[key] = &f
		}
	}

	// Convert map to slice and sort by severity
	var consolidated []Finding
	for _, f := range findingMap {
		consolidated = append(consolidated, *f)
	}

	// Sort by severity (critical first) then by confidence
	sort.Slice(consolidated, func(i, j int) bool {
		severityOrder := map[string]int{
			"critical": 0,
			"high":     1,
			"medium":   2,
			"low":      3,
			"info":     4,
		}

		if severityOrder[consolidated[i].Severity] != severityOrder[consolidated[j].Severity] {
			return severityOrder[consolidated[i].Severity] < severityOrder[consolidated[j].Severity]
		}

		return consolidated[i].Confidence > consolidated[j].Confidence
	})

	return consolidated
}

// detectConflicts finds disagreements between agents.
func detectConflicts(agentMetrics map[string]*AgentMetrics, findings []Finding) []Conflict {
	var conflicts []Conflict

	// Detect if agents reported opposite findings
	agentIDs := make([]string, 0, len(agentMetrics))
	for id := range agentMetrics {
		agentIDs = append(agentIDs, id)
	}

	// Simple conflict detection: agents with very different finding counts
	if len(agentIDs) >= 2 {
		sort.Strings(agentIDs)
		for i := 0; i < len(agentIDs)-1; i++ {
			m1 := agentMetrics[agentIDs[i]]
			m2 := agentMetrics[agentIDs[i+1]]

			// Significant difference in findings
			diff := abs(m1.FindingCount - m2.FindingCount)
			if diff > 5 {
				conflicts = append(conflicts, Conflict{
					AgentA:      agentIDs[i],
					AgentB:      agentIDs[i+1],
					Description: fmt.Sprintf("Finding count mismatch: %d vs %d", m1.FindingCount, m2.FindingCount),
				})
			}
		}
	}

	return conflicts
}

// calculateCoverage determines how comprehensively the task was analyzed.
func calculateCoverage(findings []Finding, result *ExecutionResult) CoverageMetrics {
	coverage := CoverageMetrics{
		FilesAnalyzed:     len(result.Plan.ChangedFiles),
		CompletionPercent: 100.0,
		DepthScore:        0.5,
	}

	// Estimate depth based on finding density
	if len(findings) > 0 {
		coverage.DepthScore = min(1.0, float64(len(findings))/20.0) // Normalize by 20 findings
	}

	// Completion percent based on successful agents
	if len(result.Plan.Agents.Primary) > 0 {
		coverage.CompletionPercent = 100.0
	}

	return coverage
}

// calculateQualityScore computes an overall quality metric (0-1).
func calculateQualityScore(result *ConsolidatedResult) float64 {
	if result.TotalAgents == 0 {
		return 0.0
	}

	// Components of quality score
	successRate := float64(result.SuccessfulAgents) / float64(result.TotalAgents)
	findingRate := min(1.0, float64(len(result.Findings))/10.0)
	lowConflictRate := 1.0 - min(1.0, float64(len(result.Conflicts))/5.0)
	coverageScore := result.Coverage.DepthScore

	// Weighted average
	quality := (successRate * 0.4) + (findingRate * 0.2) + (lowConflictRate * 0.2) + (coverageScore * 0.2)

	return min(1.0, max(0.0, quality))
}

// Helper functions
func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Summary returns a human-readable summary of consolidation results.
func (c *ConsolidatedResult) Summary() string {
	return fmt.Sprintf(
		"Consolidation Summary\n"+
			"Task: %s (%s)\n"+
			"Agents: %d executed, %d successful, %d failed, %d skipped\n"+
			"Findings: %d consolidated, %d conflicts detected\n"+
			"Quality Score: %.2f (0-1)\n"+
			"Coverage: %.1f%% completion, %.2f depth\n"+
			"Duration: %v",
		c.TaskID,
		c.Classification,
		c.TotalAgents,
		c.SuccessfulAgents,
		c.FailedAgents,
		c.SkippedAgents,
		len(c.Findings),
		len(c.Conflicts),
		c.QualityScore,
		c.Coverage.CompletionPercent,
		c.Coverage.DepthScore,
		c.Duration,
	)
}
