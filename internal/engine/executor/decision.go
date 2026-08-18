package executor

import (
	"strings"

	"github.com/deagy/cadre/cli/internal/engine/contracts"
	"github.com/deagy/cadre/cli/internal/engine/state"
)

// roleLabel renders an authority id as a human-readable role.
func roleLabel(authorityID string) string {
	words := strings.Split(strings.ReplaceAll(authorityID, "_", " "), " ")
	for index, word := range words {
		if word == "" {
			continue
		}
		words[index] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
	}
	return strings.Join(words, " ")
}

// authorityRequirementsFor resolves a gate's authorities against the project's
// authority map.
//
// An authority nobody has been assigned to is "unknown" rather than absent.
// That is what lets validate report an approved gate with an unresolved
// authority as a *blocker* -- a decision nobody has made -- instead of either
// passing it silently or calling it a defect.
func authorityRequirementsFor(gate contracts.Gate, authorities map[string]map[string]any) []state.AuthorityRequirement {
	requirements := make([]state.AuthorityRequirement, 0, len(gate.AuthorityRequirements))
	for _, authorityID := range gate.AuthorityRequirements {
		assigned := false
		if entry, present := authorities[authorityID]; present {
			status, _ := entry["status"].(string)
			assigned = status == "assigned"
		}
		applicability, rationale := "unknown", "Authority is not assigned"
		if assigned {
			applicability, rationale = "applicable", "Assigned in project authority map"
		}
		requirements = append(requirements, state.AuthorityRequirement{
			AuthorityID:   authorityID,
			AuthorityType: "human-approver",
			Role:          roleLabel(authorityID),
			Applicability: applicability,
			Rationale:     &rationale,
		})
	}
	return requirements
}

func emptyGateState(gate contracts.Gate) state.GateState {
	rationale := "Lifecycle gate applies by default"
	return state.GateState{
		Tier: "lifecycle", GateID: gate.ID, Name: gate.Name,
		Applicability: "applicable", ApplicabilityRationale: &rationale,
		Status:                  "pending",
		ArtifactBindings:        []state.ArtifactBinding{},
		Preparers:               []state.Identity{},
		IndependenceDeclaration: state.IndependenceDeclaration{},
		AuthorityRequirements:   []state.AuthorityRequirement{},
		HumanApprovals:          []state.Approval{},
		EvidenceRefs:            []state.Evidence{},
		KnowledgeStatus:         "unavailable",
		Findings:                []state.Finding{},
		Exceptions:              []state.Exception{},
		InvalidationHistory:     []state.Invalidation{},
	}
}

// decideGate merges a gate's agent outputs and enforces separation of duties.
//
// The verifier check is the point of the node: if the agent recorded as the
// independent verifier is also one of the preparers, the gate is blocked
// rather than merely noted, and the declaration says so. An agent reviewing
// its own work is the failure this whole model exists to prevent.
func (e *Executor) decideGate(current state.SDLCState, gate contracts.Gate) state.GateState {
	var preparers []state.Identity
	var reviewerIdentities []state.Identity
	var artifactBindings []state.ArtifactBinding
	var evidenceRefs []state.Evidence

	for _, slot := range sortedSlots(current.AgentOutputs) {
		output := current.AgentOutputs[slot]
		if asString(output["gate_id"]) != gate.ID {
			continue
		}
		identity := decodeIdentity(output["identity"])
		switch asString(output["kind"]) {
		case "author":
			preparers = append(preparers, identity)
		case "reviewer":
			reviewerIdentities = append(reviewerIdentities, identity)
		}
		if binding, ok := decodeArtifactBinding(output["artifact_binding"]); ok {
			artifactBindings = append(artifactBindings, binding)
		}
		if evidence, ok := decodeEvidence(output["evidence_ref"]); ok {
			evidenceRefs = append(evidenceRefs, evidence)
		}
	}

	// One verifier is modelled, matching the schema's single field.
	var verifier *state.Identity
	if len(reviewerIdentities) > 0 {
		first := reviewerIdentities[0]
		verifier = &first
	}

	preparerIDs := map[string]bool{}
	for _, preparer := range preparers {
		preparerIDs[preparer.ID] = true
	}
	violation := verifier != nil && preparerIDs[verifier.ID]

	updated, present := current.LifecycleGates[gate.ID]
	if !present {
		updated = emptyGateState(gate)
	}
	updated.Preparers = orEmptyIdentities(preparers)
	updated.IndependentVerifier = verifier
	updated.IndependenceDeclaration = state.IndependenceDeclaration{
		VerifierConfirmedNotPreparer: !violation,
	}
	updated.ArtifactBindings = orEmptyBindings(artifactBindings)
	updated.EvidenceRefs = orEmptyEvidence(evidenceRefs)
	updated.AuthorityRequirements = authorityRequirementsFor(gate, current.Authorities)
	updated.Status = "ready"
	if violation {
		updated.Status = "blocked"
		rationale := "Blocked: independent verifier matches a preparer id " +
			"(author_cannot_review_or_approve_same_revision)"
		updated.ApplicabilityRationale = &rationale
	}
	return updated
}

// applyApproval records a human decision on a gate.
//
// Fails closed, in four separate ways, because every one of them is a route by
// which an unapproved change could be recorded as approved:
//
//   - a blocked gate is rejected outright; no decision can approve a gate whose
//     verifier was also its preparer;
//   - "approved" without complete evidence is downgraded to rejected, so an
//     approval always carries something checkable;
//   - "approved" by someone who prepared the artifact, or by the verifier, is
//     downgraded -- an approver must be neither;
//   - any status that is not one of the four recognised values is rejected
//     rather than passed through.
func (e *Executor) applyApproval(current state.SDLCState, gateID string, decision map[string]any) state.SDLCState {
	gate, present := current.LifecycleGates[gateID]
	if !present {
		return current
	}

	violation := gate.Status == "blocked"
	evidenceRefs := decodeEvidenceList(decision["evidence_refs"])
	approver := decodeIdentityPointer(decision["approver"])

	preparerIDs := map[string]bool{}
	for _, preparer := range gate.Preparers {
		preparerIDs[preparer.ID] = true
	}
	verifierID := ""
	if gate.IndependentVerifier != nil {
		verifierID = gate.IndependentVerifier.ID
	}

	approvalStatus := "rejected"
	gateStatus := "blocked"
	if !violation {
		raw := asString(decision["status"])
		if raw == "" {
			raw = "pending"
		}
		if raw == "approved" && !hasValidEvidence(evidenceRefs) {
			raw = "rejected"
		}
		if raw == "approved" && approver != nil && (preparerIDs[approver.ID] || approver.ID == verifierID) {
			raw = "rejected"
		}
		switch raw {
		case "approved", "rejected", "pending", "not-required":
			approvalStatus = raw
		default:
			approvalStatus = "rejected"
		}
		switch approvalStatus {
		case "approved":
			gateStatus = "approved"
		case "pending":
			gateStatus = "pending"
		default:
			gateStatus = "request-changes"
		}
	}

	decidedAt := e.now()
	gate.HumanApprovals = append(gate.HumanApprovals, state.Approval{
		Status: approvalStatus, Approver: approver,
		DecidedAt: &decidedAt, EvidenceRefs: orEmptyEvidence(evidenceRefs),
	})
	gate.DecidedAt = &decidedAt
	gate.Status = gateStatus

	current.LifecycleGates = state.MergeGateUpdates(current.LifecycleGates,
		map[string]state.GateState{gateID: gate})
	return current
}

// hasValidEvidence requires every reference to carry all five fields.
//
// A partially filled reference is worse than none: it looks like evidence in a
// record while pointing at nothing verifiable.
func hasValidEvidence(refs []state.Evidence) bool {
	if len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		if ref.EvidenceID == "" || ref.URI == "" || ref.HashAlgorithm == "" ||
			ref.Hash == "" || ref.Classification == "" {
			return false
		}
	}
	return true
}

// applyMutationDecision records a human's authorisation for a mutation gate.
//
// Anything other than an explicit true halts the run: an absent, malformed or
// merely truthy-looking answer is not authorisation.
func (e *Executor) applyMutationDecision(current state.SDLCState, decision map[string]any) state.SDLCState {
	authorized, _ := decision["authorized"].(bool)
	current.MutationGateDecision = map[string]any{
		"authorized": authorized,
		"approver":   decision["approver"],
		"reason":     decision["reason"],
		"decided_at": e.now(),
	}
	current.RunHalted = !authorized
	return current
}
