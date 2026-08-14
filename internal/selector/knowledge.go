package selector

import (
	"fmt"
	"strconv"
	"strings"
)

// ClassificationOrder is the containment ladder: material may not exceed the
// classification asserted for the work it is supplied to. Order is meaning,
// not presentation -- ClassificationRank compares by position.
var ClassificationOrder = []string{"public", "internal", "confidential", "restricted"}

// ClassificationRank maps a classification to its position in the ladder.
var ClassificationRank = func() map[string]int {
	rank := make(map[string]int, len(ClassificationOrder))
	for index, name := range ClassificationOrder {
		rank[name] = index
	}
	return rank
}()

// MaximumKnowledgeTop bounds retrieval breadth.
const MaximumKnowledgeTop = 20

// KnowledgeLauncher describes how to run the retrieval CLI.
type KnowledgeLauncher struct {
	Runtime        string `json:"runtime"`
	MinimumVersion string `json:"minimum_version"`
	Resolution     string `json:"resolution"`
}

// KnowledgeInvocation is a planned retrieval call.
type KnowledgeInvocation struct {
	Launcher KnowledgeLauncher `json:"launcher"`
	Args     []string          `json:"args"`
}

// KnowledgeRequest is one agent's planned retrieval.
type KnowledgeRequest struct {
	Agent      string              `json:"agent"`
	Query      string              `json:"query"`
	Invocation KnowledgeInvocation `json:"invocation"`
}

// KnowledgeContext is the plan's knowledge_context object.
type KnowledgeContext struct {
	Status         string             `json:"status"`
	Reason         string             `json:"reason,omitempty"`
	Classification string             `json:"classification,omitempty"`
	SourceFilter   []string           `json:"source_filter,omitempty"`
	Requests       []KnowledgeRequest `json:"requests"`
}

// KnowledgeInput is what BuildKnowledgeContext needs from the plan inputs.
type KnowledgeInput struct {
	Task           string
	TaskID         string
	Classification string
	Sources        []string
	Top            int
	KnowledgeCLI   string
}

// BuildKnowledgeContext ports _build_knowledge_context. The selector plans
// retrieval; it never performs it.
func BuildKnowledgeContext(knowledgeFocus map[string]any, selectedAgents []string, input KnowledgeInput) (KnowledgeContext, error) {
	if len(selectedAgents) == 0 {
		return KnowledgeContext{Status: "not-applicable", Requests: []KnowledgeRequest{}}, nil
	}
	if input.Classification == "" {
		// Fails closed rather than defaulting: an unasserted classification
		// is not a licence to retrieve.
		return KnowledgeContext{
			Status:   "authorization-required",
			Reason:   "Provide an authorized classification and scope before retrieval.",
			Requests: []KnowledgeRequest{},
		}, nil
	}
	if _, known := ClassificationRank[input.Classification]; !known {
		return KnowledgeContext{}, fmt.Errorf("Invalid classification: %s", input.Classification) //nolint:staticcheck // ST1005: ported verbatim.
	}
	top := input.Top
	if top == 0 {
		top = 5
	}
	if top < 1 || top > MaximumKnowledgeTop {
		return KnowledgeContext{}, fmt.Errorf("Knowledge top must be an integer from 1 through 20") //nolint:staticcheck // ST1005: ported verbatim.
	}

	normalizedTask := strings.Join(strings.Fields(input.Task), " ")
	requests := make([]KnowledgeRequest, 0, len(selectedAgents))
	for _, agent := range selectedAgents {
		focus, _ := knowledgeFocus[agent].(string)
		if focus == "" {
			return KnowledgeContext{}, fmt.Errorf("Missing knowledge focus for selected agent: %s", agent) //nolint:staticcheck // ST1005: ported verbatim.
		}
		query := fmt.Sprintf("Task: %s. Retrieve %s.", normalizedTask, focus)

		args := []string{
			input.KnowledgeCLI,
			"knowledge", "search",
			"--agent", agent,
			"--task-id", input.TaskID,
			"--classification", input.Classification,
			"--top", strconv.Itoa(top),
			"--json",
		}
		// One --source per entry: the store's flag is repeatable, and naming
		// each source keeps retrieval scoped. Never --all-sources, which on
		// the shared global store would read other projects' corpora.
		for _, source := range input.Sources {
			args = append(args, "--source", source)
		}
		// The query is a trailing positional, not --query. Go's flag package
		// stops parsing at the first non-flag argument, so it must come after
		// every flag above -- appending it earlier would silently turn the
		// remaining --source scoping into positional junk, which is the one
		// direction retrieval must never fail in.
		args = append(args, query)

		requests = append(requests, KnowledgeRequest{
			Agent: agent,
			Query: query,
			Invocation: KnowledgeInvocation{
				// args[0] is an executable wrapper, run directly. It was a
				// .py path plus a probed interpreter until the knowledge
				// store moved to Go. minimum_version is the `cadre --version`
				// floor that first carries `knowledge search --json`.
				Launcher: KnowledgeLauncher{
					Runtime:        "cadre",
					MinimumVersion: "0.5.0",
					Resolution:     "platform-anchored",
				},
				Args: args,
			},
		})
	}

	sources := input.Sources
	if sources == nil {
		sources = []string{}
	}
	return KnowledgeContext{
		Status:         "planned",
		Classification: input.Classification,
		SourceFilter:   sources,
		Requests:       requests,
	}, nil
}
