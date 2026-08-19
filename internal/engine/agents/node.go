package agents

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/deagy/cadre/cli/internal/engine/provider"
	"github.com/deagy/cadre/cli/internal/engine/state"
)

// AgentOutput is one dispatched agent's contribution to a gate.
type AgentOutput struct {
	AgentID          string                `json:"agent_id"`
	Kind             string                `json:"kind"`
	GateID           string                `json:"gate_id"`
	Identity         state.Identity        `json:"identity"`
	ArtifactBinding  state.ArtifactBinding `json:"artifact_binding"`
	EvidenceRef      state.Evidence        `json:"evidence_ref"`
	BlockingQuestion *string               `json:"blocking_question"`
}

// Dispatch is one agent to run for a gate.
type Dispatch struct {
	AgentID      string
	Kind         string // author | reviewer
	Metadata     map[string]any
	Profile      map[string]any
	ProviderRoot string
}

// DispatchRequest is the per-run context an agent node needs.
type DispatchRequest struct {
	GateID         string
	TaskText       string
	Classification string
}

// sanitizedArtifactField refuses anything a model cannot be trusted to assert
// as real provenance, falling back to a default rather than failing.
//
// Rejected: a non-string, an empty string, one longer than 200 characters, and
// any string carrying a control or format character. The last is the
// interesting one -- a zero-width space or a right-to-left override renders as
// nothing, or as something else entirely, once it reaches a run record a human
// reads, so an artifact id could claim to be one thing and display as another.
func sanitizedArtifactField(value, fallback string) string {
	if value == "" || len(value) > 200 {
		return fallback
	}
	for _, character := range value {
		if unicode.In(character, unicode.Cc, unicode.Cf) {
			return fallback
		}
	}
	return value
}

// Run dispatches one agent and returns its output.
//
// The digest binds the artifact id to its revision, so a later claim about
// either is checkable against the evidence the gate recorded.
func Run(dispatch Dispatch, request DispatchRequest, client ModelClient) (AgentOutput, error) {
	var output AgentOutput

	// Written field by field rather than as a RolePromptRequest(dispatch)
	// conversion, which staticcheck suggests because the two structs are
	// identical today. That identity is incidental: a Dispatch carries
	// execution context, while this names the fields a *prompt* is entitled to
	// read. Spelling them out keeps that list a decision rather than a
	// consequence of the two types happening to still match.
	//nolint:staticcheck // S1016: see above -- the explicit list is the point.
	rolePrompt := ResolveRolePrompt(RolePromptRequest{
		AgentID:      dispatch.AgentID,
		Kind:         dispatch.Kind,
		Metadata:     dispatch.Metadata,
		Profile:      dispatch.Profile,
		ProviderRoot: dispatch.ProviderRoot,
	})

	contribution, err := client.Complete(CompletionRequest{
		AgentID:    dispatch.AgentID,
		Kind:       dispatch.Kind,
		GateID:     request.GateID,
		RolePrompt: rolePrompt,
		TaskText:   request.TaskText,
	})
	if err != nil {
		return output, err
	}

	artifactID := sanitizedArtifactField(contribution.ArtifactID,
		fmt.Sprintf("%s-%s-artifact", request.GateID, dispatch.AgentID))
	revision := sanitizedArtifactField(contribution.Revision, "rev-1")

	digest, err := provider.Fingerprint(map[string]any{"artifact_id": artifactID, "revision": revision})
	if err != nil {
		return output, err
	}

	classification := request.Classification
	if classification == "" {
		classification = "internal"
	}

	return AgentOutput{
		AgentID:  dispatch.AgentID,
		Kind:     dispatch.Kind,
		GateID:   request.GateID,
		Identity: state.Identity{ID: dispatch.AgentID, Role: dispatch.Kind + ":" + dispatch.AgentID, Kind: "agent"},
		ArtifactBinding: state.ArtifactBinding{
			ArtifactID: artifactID, Revision: revision, Digest: digest,
		},
		EvidenceRef: state.Evidence{
			EvidenceID:     fmt.Sprintf("%s-%s-evidence", request.GateID, dispatch.AgentID),
			URI:            fmt.Sprintf("agent-dispatch://%s/%s", request.GateID, dispatch.AgentID),
			HashAlgorithm:  "sha256",
			Hash:           strings.TrimPrefix(digest, "sha256:"),
			Classification: classification,
		},
		BlockingQuestion: contribution.BlockingQuestion,
	}, nil
}

// Slot is the key an output is merged under.
func Slot(gateID, kind, agentID string) string {
	return state.AgentOutputSlot(gateID, kind, agentID)
}
