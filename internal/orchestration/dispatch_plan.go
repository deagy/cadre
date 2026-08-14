package orchestration

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// DispatchPlan represents the complete output of agent selection.
// It describes which agents should handle a task, in what roles, with what gates.
type DispatchPlan struct {
	// Task identification
	TaskID         string    `json:"task_id"`
	Task           string    `json:"task"`
	Classification string    `json:"classification"`
	ChangedFiles   []string  `json:"changed_files"`
	CreatedAt      time.Time `json:"created_at"`

	// Agent selection
	Agents AgentGroups `json:"agents"`

	// Workflow and gates
	Workflow       string   `json:"workflow"`
	QualityGates   []Gate   `json:"quality_gates"`
	HumanGates     []Gate   `json:"human_gates"`
	LifecycleGates []Gate   `json:"lifecycle_gates,omitempty"`
	RequiredGates  []string `json:"required_gates,omitempty"`
	IgnoredGates   []string `json:"ignored_gates,omitempty"`

	// Teams and communication
	Teams []Team `json:"teams,omitempty"`

	// Dispatch metadata
	DispatchDisposition Disposition       `json:"dispatch_disposition"`
	MatchedRoutes       []string          `json:"matched_routes"`
	Reasons             map[string]string `json:"reasons,omitempty"`

	// Knowledge context
	KnowledgeContext KnowledgeContext `json:"knowledge_context,omitempty"`

	// Provenance binds this plan to the exact catalog.yaml/routing.json
	// content (and, best-effort, git commit) that produced it. Set by the
	// caller (SelectAgents) after the plan is built, since it needs the
	// filesystem paths that were loaded rather than anything BuildDispatchPlan
	// itself has access to. Absent entirely when it could not be computed
	// (e.g. catalog.yaml unreadable) -- never fabricated.
	Provenance *Provenance `json:"provenance,omitempty"`
}

// AgentGroups organizes agents by role.
type AgentGroups struct {
	Primary   []string `json:"primary"`
	Reviewers []string `json:"reviewers"`
	Support   []string `json:"support"`
}

// Gate represents a lifecycle or quality gate.
type Gate struct {
	ID       string   `json:"id"`
	Phase    string   `json:"phase"`
	Agents   []string `json:"agents"`
	Required bool     `json:"required"`
}

// Team represents a group of agents working together.
type Team struct {
	Name              string   `json:"name"`
	Agents            []string `json:"agents"`
	CommunicationMode string   `json:"communication_mode"` // "peer" or "orchestrator-relayed"
}

// Disposition describes the dispatch outcome.
type Disposition struct {
	Status       string `json:"status"` // "staffed", "advisory-only", "no-agents-selected"
	Reason       string `json:"reason,omitempty"`
	HasPrimary   bool   `json:"has_primary"`
	HasReviewers bool   `json:"has_reviewers"`
}

// KnowledgeContext holds knowledge store retrieval configuration.
type KnowledgeContext struct {
	Enabled         bool     `json:"enabled"`
	Sources         []string `json:"sources,omitempty"`
	Classifications []string `json:"classifications,omitempty"`
	TopK            int      `json:"top_k,omitempty"`
}

// BuildDispatchPlan creates a complete dispatch plan from route matches.
// This orchestrates all agent selection logic and gate building.
func BuildDispatchPlan(
	taskID string,
	task string,
	files []string,
	classification string,
	matches []RouteMatch,
	routing *RoutingConfig,
) (*DispatchPlan, error) {
	if len(matches) == 0 {
		return nil, fmt.Errorf("no routes matched for task %q", task)
	}

	plan := &DispatchPlan{
		TaskID:         taskID,
		Task:           task,
		Classification: classification,
		ChangedFiles:   append([]string{}, files...), // Copy to avoid mutation
		CreatedAt:      time.Now(),
		MatchedRoutes:  make([]string, len(matches)),
		Reasons:        make(map[string]string),
	}

	// Record matched routes
	for i, match := range matches {
		plan.MatchedRoutes[i] = match.RouteID
		// Merge reasons
		for k, v := range match.Reasons {
			plan.Reasons[k] = v
		}
	}

	// Select agents from matches
	primary, reviewers, support := SelectAgents(matches)
	plan.Agents = AgentGroups{
		Primary:   primary,
		Reviewers: reviewers,
		Support:   support,
	}

	// Determine dispatch disposition
	plan.DispatchDisposition = buildDispatchDisposition(plan.Agents)

	// Build quality gates from matched routes
	plan.QualityGates = buildQualityGates(matches)

	// Build human gates from routing risk rules
	plan.HumanGates = buildHumanGates(routing.RiskRules, classification)

	// Set default workflow
	plan.Workflow = "standard"
	if classification == "critical" {
		plan.Workflow = "critical-path"
	}

	// Build teams if recipe information is available
	// (Deferred: team recipes require more complex logic)

	// Build knowledge context
	plan.KnowledgeContext = buildKnowledgeContext(classification, routing)

	return plan, nil
}

// buildDispatchDisposition determines the overall dispatch status.
func buildDispatchDisposition(agents AgentGroups) Disposition {
	disp := Disposition{
		HasPrimary:   len(agents.Primary) > 0,
		HasReviewers: len(agents.Reviewers) > 0,
	}

	switch {
	case disp.HasPrimary || disp.HasReviewers:
		disp.Status = "staffed"
	case len(agents.Support) > 0:
		disp.Status = "advisory-only"
		disp.Reason = "only support agents matched; no primary or reviewer authority"
	default:
		disp.Status = "no-agents-selected"
		disp.Reason = "no agents matched for this task"
	}

	return disp
}

// buildQualityGates extracts quality gates from matched routes.
func buildQualityGates(matches []RouteMatch) []Gate {
	gateMap := make(map[string]Gate)

	for _, match := range matches {
		// In a full implementation, this would read quality_gates from the route
		// For now, provide a basic set based on workflow
		if match.Primary != nil {
			gate := Gate{
				ID:       "code-review",
				Phase:    "review",
				Agents:   match.Reviewers,
				Required: true,
			}
			if _, exists := gateMap["code-review"]; !exists {
				gateMap["code-review"] = gate
			}
		}
	}

	// Convert map to sorted slice
	var gates []Gate
	for _, gate := range gateMap {
		gates = append(gates, gate)
	}
	sort.Slice(gates, func(i, j int) bool {
		return gates[i].ID < gates[j].ID
	})

	return gates
}

// buildHumanGates creates human approval gates based on risk rules.
func buildHumanGates(risks []Risk, classification string) []Gate {
	var gates []Gate

	// High-risk classifications require human approval
	if classification == "critical" || classification == "high" {
		gates = append(gates, Gate{
			ID:       "human-approval",
			Phase:    "review",
			Required: true,
		})
	}

	// Check if any risk rule explicitly requires human gates
	for _, risk := range risks {
		if risk.Level == "critical" {
			gates = append(gates, Gate{
				ID:       fmt.Sprintf("risk-approval-%s", risk.ID),
				Phase:    "review",
				Required: true,
			})
		}
	}

	return gates
}

// buildKnowledgeContext creates knowledge retrieval configuration.
func buildKnowledgeContext(classification string, routing *RoutingConfig) KnowledgeContext {
	ctx := KnowledgeContext{
		Enabled: true,
		TopK:    5,
	}

	// Default sources
	ctx.Sources = []string{
		"proposed-knowledge",
		"cadre-agents",
	}

	// Classification-based retrieval
	switch classification {
	case "critical":
		ctx.Classifications = []string{"critical", "high", "medium"}
		ctx.TopK = 10
	case "high":
		ctx.Classifications = []string{"high", "medium"}
		ctx.TopK = 7
	default:
		ctx.Classifications = []string{"medium"}
		ctx.TopK = 5
	}

	return ctx
}

// PlanText renders the dispatch plan as human-readable text.
func (p *DispatchPlan) PlanText() string {
	var text strings.Builder

	text.WriteString("═══════════════════════════════════════════════════════════════\n")
	text.WriteString("Cadre Agent Selection Plan\n")
	text.WriteString("═══════════════════════════════════════════════════════════════\n\n")

	fmt.Fprintf(&text, "Task: %s (ID: %s)\n", p.Task, p.TaskID)
	fmt.Fprintf(&text, "Classification: %s\n", p.Classification)
	fmt.Fprintf(&text, "Workflow: %s\n", p.Workflow)
	fmt.Fprintf(&text, "Matched Routes: %v\n\n", p.MatchedRoutes)

	text.WriteString("Primary Agents (Execute & Author):\n")
	for _, agent := range p.Agents.Primary {
		fmt.Fprintf(&text, "  - %s\n", agent)
	}
	if len(p.Agents.Primary) == 0 {
		text.WriteString("  (none)\n")
	}

	text.WriteString("\nReviewers (Independent Review):\n")
	for _, agent := range p.Agents.Reviewers {
		fmt.Fprintf(&text, "  - %s\n", agent)
	}
	if len(p.Agents.Reviewers) == 0 {
		text.WriteString("  (none)\n")
	}

	text.WriteString("\nSupport (Advisory):\n")
	for _, agent := range p.Agents.Support {
		fmt.Fprintf(&text, "  - %s\n", agent)
	}
	if len(p.Agents.Support) == 0 {
		text.WriteString("  (none)\n")
	}

	fmt.Fprintf(&text, "\nDispatch Disposition: %s\n", p.DispatchDisposition.Status)
	if p.DispatchDisposition.Reason != "" {
		fmt.Fprintf(&text, "  Reason: %s\n", p.DispatchDisposition.Reason)
	}

	if len(p.QualityGates) > 0 {
		text.WriteString("\nQuality Gates:\n")
		for _, gate := range p.QualityGates {
			fmt.Fprintf(&text, "  - %s (%s)\n", gate.ID, gate.Phase)
		}
	}

	if len(p.HumanGates) > 0 {
		text.WriteString("\nHuman Approval Gates:\n")
		for _, gate := range p.HumanGates {
			fmt.Fprintf(&text, "  - %s (%s)\n", gate.ID, gate.Phase)
		}
	}

	text.WriteString("\n═══════════════════════════════════════════════════════════════\n")

	return text.String()
}
