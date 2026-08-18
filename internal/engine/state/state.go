// Package state holds the graph's state model.
//
// GateState is a field-for-field port of run-record.schema.json's `gate`
// definition: the checkpointed state for a task *is* its run record, so a
// field that drifts from the schema produces a record the kernel rejects.
// Every one of the schema's twenty gate properties is required, which is why
// nothing here carries `omitempty` and why the nullable fields are pointers --
// they must appear as `null`, not vanish.
//
// Ported from engine/agentic_sdlc_langgraph/state.py.
package state

// Identity is a person, agent or service acting on a gate.
type Identity struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	Kind string `json:"kind"` // human | agent | service
}

// ArtifactBinding pins an artifact revision to a gate.
type ArtifactBinding struct {
	ArtifactID string `json:"artifact_id"`
	Revision   string `json:"revision"`
	Digest     string `json:"digest"`
}

// Evidence is a hashed reference to supporting material.
type Evidence struct {
	EvidenceID     string `json:"evidence_id"`
	URI            string `json:"uri"`
	HashAlgorithm  string `json:"hash_algorithm"`
	Hash           string `json:"hash"`
	Classification string `json:"classification"`
}

// AuthorityRequirement is one authority a gate needs.
type AuthorityRequirement struct {
	AuthorityID   string  `json:"authority_id"`
	AuthorityType string  `json:"authority_type"` // always human-approver in this contract
	Role          string  `json:"role"`
	Applicability string  `json:"applicability"` // applicable | not-applicable | unknown
	Rationale     *string `json:"rationale"`
}

// Approval is one human decision on a gate.
type Approval struct {
	Status       string     `json:"status"` // pending | approved | rejected | not-required
	Approver     *Identity  `json:"approver"`
	DecidedAt    *string    `json:"decided_at"`
	EvidenceRefs []Evidence `json:"evidence_refs"`
}

// IndependenceDeclaration records verifier separation.
type IndependenceDeclaration struct {
	VerifierConfirmedNotPreparer   bool `json:"verifier_confirmed_not_preparer"`
	VerifierMadeMaterialCorrection bool `json:"verifier_made_material_correction"` // schema pins this false
}

// Finding is an open issue raised against a gate.
type Finding struct {
	FindingID string `json:"finding_id"`
	Severity  string `json:"severity"`
	Status    string `json:"status"`
	Owner     string `json:"owner"`
}

// Exception is an accepted deviation, with its compensating controls.
type Exception struct {
	ExceptionID          string   `json:"exception_id"`
	FindingID            string   `json:"finding_id"`
	Justification        string   `json:"justification"`
	CompensatingControls []string `json:"compensating_controls"`
	Owner                Identity `json:"owner"`
	Approver             Identity `json:"approver"`
	ExpiresAt            string   `json:"expires_at"`
	RemediationPlan      string   `json:"remediation_plan"`
}

// Invalidation records a gate being reopened.
type Invalidation struct {
	InvalidatedAt            string            `json:"invalidated_at"`
	Actor                    string            `json:"actor"`
	Reason                   string            `json:"reason"`
	EarliestGate             string            `json:"earliest_gate"`
	InvalidatedGateIDs       []string          `json:"invalidated_gate_ids"`
	AffectedArtifactBindings []ArtifactBinding `json:"affected_artifact_bindings"`
	SupersedingArtifactID    *string           `json:"superseding_artifact_id"`
}

// GateState is one gate's entry in the run record.
type GateState struct {
	Tier                    string                  `json:"tier"` // lifecycle | specialist
	GateID                  string                  `json:"gate_id"`
	Name                    string                  `json:"name"`
	Applicability           string                  `json:"applicability"`
	ApplicabilityRationale  *string                 `json:"applicability_rationale"`
	Status                  string                  `json:"status"`
	ArtifactBindings        []ArtifactBinding       `json:"artifact_bindings"`
	Preparers               []Identity              `json:"preparers"`
	IndependentVerifier     *Identity               `json:"independent_verifier"`
	IndependenceDeclaration IndependenceDeclaration `json:"independence_declaration"`
	AuthorityRequirements   []AuthorityRequirement  `json:"authority_requirements"`
	HumanApprovals          []Approval              `json:"human_approvals"`
	DecidedAt               *string                 `json:"decided_at"`
	EvidenceRefs            []Evidence              `json:"evidence_refs"`
	KnowledgeStatus         string                  `json:"knowledge_status"`
	Findings                []Finding               `json:"findings"`
	Exceptions              []Exception             `json:"exceptions"`
	InvalidationHistory     []Invalidation          `json:"invalidation_history"`
	RequiredReentryGate     *string                 `json:"required_reentry_gate"`
}

// SDLCState is the graph-level state. Its checkpointed value for a task_id is
// that task's run record.
//
// Several run-record fields are deliberately absent: provider_bindings,
// dispatch_binding_digest, contract_digest, knowledge_retrieval,
// impact_profile, mode, baseline_revision and disposition are synthesised at
// export time rather than carried through the graph, because nothing in the
// gate slice computes them.
type SDLCState struct {
	TaskID                string `json:"task_id"`
	Classification        string `json:"classification"`
	Scope                 string `json:"scope"` // task text; routing and mutation matching read this
	CurrentLifecyclePhase string `json:"current_lifecycle_phase"`

	// Set once at plan time from --intent-gitlab-issue /
	// --requirements-gitlab-issue, then read back unchanged at export. Never
	// approval evidence: gate approval status is unaffected by either.
	IntentRecordID         *string `json:"intent_record_id"`
	RequirementsBaselineID *string `json:"requirements_baseline_id"`

	LifecycleGates map[string]GateState `json:"lifecycle_gates"`
	ReEntryHistory []Invalidation       `json:"re_entry_history"`

	// Authority assignments fed in at invoke time. Not part of the exported
	// run record; mirrors the project authorities.json overlay.
	Authorities map[string]map[string]any `json:"authorities"`

	// Fan-in scratch, keyed "gate_id:kind:agent_id". Never exported.
	AgentOutputs map[string]map[string]any `json:"agent_outputs"`

	// Set by the mutation-gate guard at graph entry, independent of gate and
	// authority approval: non-nil whenever a human-only phrase matched scope,
	// whether or not it was subsequently authorised.
	MutationGatePending  map[string]any `json:"mutation_gate_pending"`
	MutationGateDecision map[string]any `json:"mutation_gate_decision"`

	// Hard stop. While true no gate dispatch node runs and the graph routes
	// straight to END. Independent of, and preceding, any per-gate approval
	// interrupt.
	RunHalted bool `json:"run_halted"`
}

// MergeGateUpdates is the per-gate-id reducer for lifecycle_gates.
//
// Parallel branches touching different gate ids must not clobber each other.
// Each key in update replaces that key wholesale -- nodes return a complete
// replacement for a gate, never a sparse patch -- and every other key in
// current survives.
//
// Returns a new map: mutating current in place would be visible to a parallel
// branch holding the same reference, which is the clobbering this prevents.
func MergeGateUpdates(current, update map[string]GateState) map[string]GateState {
	merged := make(map[string]GateState, len(current)+len(update))
	for id, gate := range current {
		merged[id] = gate
	}
	for id, gate := range update {
		merged[id] = gate
	}
	return merged
}

// MergeAgentOutputs is the per-dispatch-slot reducer for agent_outputs.
//
// Keyed "gate_id:kind:agent_id" -- one slot per agent per role per gate --
// with the same later-write-wins semantics as MergeGateUpdates.
//
// The keying is load-bearing, not cosmetic. With an append-only list, a gate
// re-dispatched after reenter_gate kept its stale pre-invalidation outputs
// alongside the fresh ones; the gate decision node filters only by gate_id, so
// it read both and duplicated preparers, artifact_bindings and evidence_refs.
// Keying by slot means a redispatch overwrites its own prior entry, while
// parallel branches for different agents in the same gate still get distinct
// keys.
func MergeAgentOutputs(current, update map[string]map[string]any) map[string]map[string]any {
	merged := make(map[string]map[string]any, len(current)+len(update))
	for slot, output := range current {
		merged[slot] = output
	}
	for slot, output := range update {
		merged[slot] = output
	}
	return merged
}

// AgentOutputSlot builds the key MergeAgentOutputs is keyed by.
func AgentOutputSlot(gateID, kind, agentID string) string {
	return gateID + ":" + kind + ":" + agentID
}
