// Package agents builds role prompts and dispatches a gate's contribution to
// a model.
//
// Ported from engine/agentic_sdlc_langgraph/agents.py. This file carries the
// part with no network in it: the prompt a dispatched agent is given, the
// structured contribution it must return, and a deterministic stand-in client.
package agents

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/deagy/cadre/cli/internal/engine/provider"
)

// AskHumanRule is appended to every role prompt.
//
// A dispatched subagent has no channel to the human, so the only correct move
// when it reaches a decision it cannot make is to stop and say so. Without
// this, the alternative is guessing, and a guess at a gate is indistinguishable
// from a judgement.
const AskHumanRule = "You are a dispatched subagent: you cannot ask the human directly. " +
	"If you reach a decision only a human can make, stop and return a clearly labeled " +
	"blocking question in your result instead of guessing or proceeding."

// RichContentAdaptationNote is appended when a provider supplies its own role
// definition, because that definition was written for another repository.
const RichContentAdaptationNote = "Adapted from a role definition bundled with a provider's " +
	"agent catalog. Review and tailor this role for this project's own stack, policies, and " +
	"gates before relying on it -- shared-policy references in the source repository it came " +
	"from will not resolve here."

// AgentContribution is what a dispatched agent returns.
type AgentContribution struct {
	ArtifactID       string  `json:"artifact_id"`
	Revision         string  `json:"revision"`
	Summary          string  `json:"summary"`
	BlockingQuestion *string `json:"blocking_question"`
}

// ModelClient is what a dispatch node calls.
type ModelClient interface {
	Complete(request CompletionRequest) (AgentContribution, error)
}

// CompletionRequest is one dispatch.
type CompletionRequest struct {
	AgentID    string
	Kind       string
	GateID     string
	RolePrompt string
	TaskText   string
}

// SubmitContributionToolName and its schema are the structured reply every
// real client asks a model for, so prose never has to be parsed.
//
// Shared so the Anthropic and OpenAI-compatible clients cannot drift on the
// contract. Each still wraps this in its own tool-declaration envelope --
// input_schema against parameters -- because those envelopes differ.
const (
	SubmitContributionToolName    = "submit_contribution"
	SubmitContributionDescription = "Submit this agent's structured contribution for the gate."
)

// SubmitContributionSchema is the JSON Schema for the tool's input.
func SubmitContributionSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"artifact_id", "revision", "summary"},
		"properties": map[string]any{
			"artifact_id":       map[string]any{"type": "string"},
			"revision":          map[string]any{"type": "string"},
			"summary":           map[string]any{"type": "string"},
			"blocking_question": map[string]any{"type": []any{"string", "null"}},
		},
	}
}

// agentWrapperInstructions is the generic role instruction used when no
// provider-supplied definition is available or opted into.
func agentWrapperInstructions(agentID string, reviewer bool) string {
	separation := "Prepare artifacts for independent review; do not self-review."
	if reviewer {
		separation = "Remain independent and do not modify the artifact under review."
	}
	return "Act as the portable Agentic SDLC role " + agentID + ". " +
		"Bind work to the task revision and lifecycle gate. " +
		"Never approve a lifecycle or mutation gate. " +
		separation + " " + AskHumanRule
}

// richAgentContent reads a provider-supplied role definition, or returns empty.
//
// Never errors: a definition that is missing, escapes its provider root, or is
// not a real file falls back to the generic instruction rather than failing a
// dispatch.
//
// With no providerRoot, only an absolute path is trusted -- which is the shape
// a definition already has once provider.LoadProvider has resolved and
// confined it. A relative path with nothing to confine it against is treated
// as unresolved rather than resolved against the working directory, because
// the latter is an implicit escape hatch: it would read whatever happens to
// sit at that relative path next to whoever ran the process.
func richAgentContent(definition string, providerRoot string) string {
	if definition == "" {
		return ""
	}

	var path string
	if providerRoot != "" {
		resolved, err := provider.ProviderResource(providerRoot, definition, "definition", false)
		if err != nil {
			return ""
		}
		path = resolved
	} else {
		if !filepath.IsAbs(definition) {
			return ""
		}
		path = definition
	}

	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ""
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contents))
}

// RolePromptRequest carries what ResolveRolePrompt needs.
type RolePromptRequest struct {
	AgentID  string
	Kind     string
	Metadata map[string]any
	Profile  map[string]any

	// ProviderRoot is normally empty: LoadProvider already resolved and
	// confined any definition once, at catalog-load time. It exists so a
	// caller holding an unresolved catalog can still confine safely.
	ProviderRoot string
}

// ResolveRolePrompt returns the prompt a dispatched agent is given.
//
// A provider's own role definition is used only when the profile opts in via
// rich_content_source *and* the definition resolves to a real, confined file.
// None of the three shipped profiles opts in, so the generic instruction is
// what runs today.
func ResolveRolePrompt(request RolePromptRequest) string {
	if optedIn := request.Profile["rich_content_source"]; truthy(optedIn) {
		definition, _ := request.Metadata["definition"].(string)
		if rich := richAgentContent(definition, request.ProviderRoot); rich != "" {
			return strings.Join([]string{rich, RichContentAdaptationNote, AskHumanRule}, "\n\n")
		}
	}
	return agentWrapperInstructions(request.AgentID, request.Kind == "reviewer")
}

// truthy mirrors Python's notion of a truthy value for the opt-in flag, where
// the field may be a bool, a string, or absent.
func truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0
	default:
		return true
	}
}

// FakeModelClient is a deterministic, no-network stand-in.
//
// Its output is derived entirely from the agent, kind and gate, so a run is
// reproducible.
type FakeModelClient struct {
	// BlockingAgents name agents that should return a blocking question.
	BlockingAgents map[string]bool
}

// Complete returns a deterministic contribution.
func (f FakeModelClient) Complete(request CompletionRequest) (AgentContribution, error) {
	contribution := AgentContribution{
		ArtifactID: request.GateID + "-" + request.AgentID + "-artifact",
		Revision:   "rev-1",
		Summary:    request.AgentID + " completed its " + request.Kind + " contribution for " + request.GateID,
	}
	if f.BlockingAgents[request.AgentID] {
		question := request.AgentID + " needs clarification before proceeding"
		contribution.BlockingQuestion = &question
	}
	return contribution, nil
}
