package selector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"time"

	"github.com/deagy/cadre/cli/internal/config"
)

// The lifecycle contract this selector understands. A different version is an
// error, not a degraded mode: gate ids and their order are what gate
// sequencing is computed from, so silently accepting an unknown shape would
// produce a confidently wrong plan.
const supportedLifecycleContractVersion = 2

const contractTimeout = 10 * time.Second

// LifecycleGate is one gate as the kernel declares it.
type LifecycleGate struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Phase        string   `json:"phase"`
	AuthorAgents []string `json:"author_agents"`
	ReviewAgents []string `json:"review_agents"`
	// reviewAgentsDeclared distinguishes "declared an empty list" from "did
	// not declare the key". No gate in any shipped contract has ever declared
	// review_agents, and that distinction is what makes
	// default_gate_review_agents a real default rather than an unconditional
	// hardcode -- see GateAgents.
	reviewAgentsDeclared bool
}

// UnmarshalJSON records whether review_agents was present at all.
func (g *LifecycleGate) UnmarshalJSON(data []byte) error {
	type plain LifecycleGate
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*g = LifecycleGate(decoded)
	_, g.reviewAgentsDeclared = raw["review_agents"]
	return nil
}

// LifecycleContract is the kernel's lifecycle-gates contract.
type LifecycleContract struct {
	Version int             `json:"version"`
	Gates   []LifecycleGate `json:"gates"`
}

// GateOrder is the contract's declared gate order. Nil when no contract is
// available, which is the signal every gate function branches on.
func GateOrder(gates []LifecycleGate) []string {
	if gates == nil {
		return nil
	}
	order := make([]string, 0, len(gates))
	for _, gate := range gates {
		order = append(order, gate.ID)
	}
	return order
}

// FetchLifecycleContract shells out to the kernel, porting
// agentic_sdlc_contracts.py.
//
// Returns (nil, nil) when no executable resolves: that is the ordinary
// standalone mode, not a failure. A resolution *error* is returned as-is and
// must not be flattened into "unavailable" -- agentic_sdlc.bin_path is a
// global-only setting because it selects an executable to spawn, so a
// project-local file trying to set it is a security event rather than a
// missing-tool outcome.
func FetchLifecycleContract(ctx context.Context) (*LifecycleContract, error) {
	executable, err := config.ResolveString(ctx, "agentic_sdlc.bin_path")
	if err != nil && !errors.Is(err, config.ErrSettingNotFound) {
		// Only a global_only scope violation reaches here -- an untrusted
		// project-local file setting agentic_sdlc.bin_path. That is a
		// security event and must not be flattened into "unavailable".
		return nil, err
	}
	if errors.Is(err, config.ErrSettingNotFound) || executable == "" {
		return nil, nil
	}

	commandContext, cancel := context.WithTimeout(ctx, contractTimeout)
	defer cancel()
	output, err := exec.CommandContext(commandContext, executable, "show-contract", "lifecycle-gates").Output()
	if err != nil {
		return nil, fmt.Errorf("Agentic SDLC contract lookup failed: %w", err) //nolint:staticcheck // ST1005: message text is ported verbatim from agentic_sdlc_contracts.py / build_dispatch_plan.py; rewording it during a fidelity port is the drift this port exists to avoid.
	}

	var contract LifecycleContract
	if err := json.Unmarshal(output, &contract); err != nil {
		return nil, fmt.Errorf("Agentic SDLC returned malformed JSON for lifecycle-gates") //nolint:staticcheck // ST1005: message text is ported verbatim from agentic_sdlc_contracts.py / build_dispatch_plan.py; rewording it during a fidelity port is the drift this port exists to avoid.
	}
	if contract.Gates == nil {
		return nil, fmt.Errorf("Agentic SDLC returned an invalid lifecycle-gates contract") //nolint:staticcheck // ST1005: message text is ported verbatim from agentic_sdlc_contracts.py / build_dispatch_plan.py; rewording it during a fidelity port is the drift this port exists to avoid.
	}
	if contract.Version != supportedLifecycleContractVersion {
		return nil, fmt.Errorf( //nolint:staticcheck // ST1005: ported verbatim; see above.
			"Agentic SDLC returned an incompatible lifecycle-gates contract "+
				"(expected version %d, got %d)",
			supportedLifecycleContractVersion, contract.Version)
	}
	for _, gate := range contract.Gates {
		if gate.ID == "" {
			return nil, fmt.Errorf("Agentic SDLC lifecycle contract must contain identified gates") //nolint:staticcheck // ST1005: message text is ported verbatim from agentic_sdlc_contracts.py / build_dispatch_plan.py; rewording it during a fidelity port is the drift this port exists to avoid.
		}
	}
	if len(contract.Gates) == 0 {
		return nil, fmt.Errorf("Agentic SDLC lifecycle contract must contain identified gates") //nolint:staticcheck // ST1005: message text is ported verbatim from agentic_sdlc_contracts.py / build_dispatch_plan.py; rewording it during a fidelity port is the drift this port exists to avoid.
	}
	return &contract, nil
}

// GateSequence ports _gate_sequence: the effective gate run, and the ones
// suppressed by ignored_gates.
//
// With a contract, a configured gate implies every earlier gate in the
// contract's order -- the sequence runs from the first gate up to the
// latest one anything configured. Without a contract there is no order to
// imply anything, so the configured set is used as-is.
func GateSequence(configured, ignored []string, gates []LifecycleGate) (effective, ignoredOut []string, err error) {
	if len(configured) == 0 {
		return []string{}, []string{}, nil
	}
	if gates == nil {
		ignoredSet := setOf(ignored)
		for _, gateID := range configured {
			if ignoredSet[gateID] {
				ignoredOut = append(ignoredOut, gateID)
				continue
			}
			effective = append(effective, gateID)
		}
		return orEmpty(effective), orEmpty(ignoredOut), nil
	}

	gateIDs := GateOrder(gates)
	if err := rejectUnknownGates(ignored, gateIDs, "ignored_gates contains unknown lifecycle gates"); err != nil {
		return nil, nil, err
	}
	if err := rejectUnknownGates(configured, gateIDs, "routing references unknown lifecycle gates"); err != nil {
		return nil, nil, err
	}

	index := indexOf(gateIDs)
	furthest := 0
	for _, gateID := range configured {
		if index[gateID] > furthest {
			furthest = index[gateID]
		}
	}
	sequence := gateIDs[:furthest+1]

	ignoredSet := setOf(ignored)
	for _, gateID := range sequence {
		if ignoredSet[gateID] {
			ignoredOut = append(ignoredOut, gateID)
			continue
		}
		effective = append(effective, gateID)
	}
	// Python sorts the ignored list by contract order; `sequence` is already
	// in that order, so appending as we walk it produces the same result.
	return orEmpty(effective), orEmpty(ignoredOut), nil
}

// GateAgents ports _gate_agents: agents contributed by the configured gates.
//
// defaultReviewAgents comes from the roster's default_gate_review_agents, and
// it is emphatically not a fallback in the usual sense. No gate in any shipped
// lifecycle contract has ever declared review_agents or author_agents, so a
// `.get(key, default)` against it is an unconditional hardcode wearing a
// fallback's clothes. Making it roster-supplied is what lets a foreign roster
// produce a plan at all: agent validation rejects any id absent from the
// catalog, so a roster without a `code-reviewer` role and any route declaring
// quality_gates previously emitted nothing whatsoever.
func GateAgents(configured, ignored []string, gates []LifecycleGate, defaultReviewAgents []string) ([]string, error) {
	if gates == nil || len(configured) == 0 {
		return []string{}, nil
	}
	gateIDs := GateOrder(gates)
	if err := rejectUnknownGates(ignored, gateIDs, "ignored_gates contains unknown lifecycle gates"); err != nil {
		return nil, err
	}
	if err := rejectUnknownGates(configured, gateIDs, "routing references unknown lifecycle gates"); err != nil {
		return nil, err
	}

	index := indexOf(gateIDs)
	furthest := 0
	for _, gateID := range configured {
		if index[gateID] > furthest {
			furthest = index[gateID]
		}
	}
	sequence := gateIDs[:furthest+1]
	ignoredSet := setOf(ignored)
	byID := map[string]LifecycleGate{}
	for _, gate := range gates {
		byID[gate.ID] = gate
	}

	var agents []string
	for _, gateID := range sequence {
		if ignoredSet[gateID] {
			continue
		}
		gate := byID[gateID]
		agents = append(agents, gate.AuthorAgents...)
		if gate.reviewAgentsDeclared {
			agents = append(agents, gate.ReviewAgents...)
			continue
		}
		agents = append(agents, defaultReviewAgents...)
	}
	return uniqueStrings(agents), nil
}

// QualityGate is one entry of the plan's required_quality_gates.
type QualityGate struct {
	ID                 string   `json:"id"`
	Required           bool     `json:"required"`
	Reason             string   `json:"reason"`
	ContributingRoutes []string `json:"contributing_routes"`
}

const gateDetailOmitted = "Required by routing configuration (Agentic SDLC unavailable; gate detail omitted)."

// BuildQualityGates ports _build_quality_gates: it aggregates provider
// applicability without defining lifecycle semantics.
func BuildQualityGates(routes, risks []Match, gates []LifecycleGate) ([]QualityGate, error) {
	byID := map[string]LifecycleGate{}
	for _, gate := range gates {
		byID[gate.ID] = gate
	}
	contributors := map[string][]string{}
	var order []string
	for _, match := range append(append([]Match{}, routes...), risks...) {
		for _, gateID := range stringSlice(match.Rule["quality_gates"]) {
			if gates != nil {
				if _, known := byID[gateID]; !known {
					return nil, fmt.Errorf("Routing references an unknown lifecycle gate: %s", gateID) //nolint:staticcheck // ST1005: message text is ported verbatim from agentic_sdlc_contracts.py / build_dispatch_plan.py; rewording it during a fidelity port is the drift this port exists to avoid.
				}
			}
			if _, seen := contributors[gateID]; !seen {
				order = append(order, gateID)
			}
			contributors[gateID] = append(contributors[gateID], match.ID)
		}
	}

	gateIDs := order
	if gates != nil {
		gateIDs = nil
		for _, gateID := range GateOrder(gates) {
			if _, contributed := contributors[gateID]; contributed {
				gateIDs = append(gateIDs, gateID)
			}
		}
	}

	built := make([]QualityGate, 0, len(gateIDs))
	for _, gateID := range gateIDs {
		reason := gateDetailOmitted
		if gates != nil {
			gate := byID[gateID]
			name := gate.Name
			if name == "" {
				name = gateID
			}
			phase := gate.Phase
			if phase == "" {
				phase = "unspecified"
			}
			reason = fmt.Sprintf("%s lifecycle gate (%s phase).", name, phase)
		}
		built = append(built, QualityGate{
			ID:                 gateID,
			Required:           true,
			Reason:             reason,
			ContributingRoutes: uniqueStrings(contributors[gateID]),
		})
	}
	return built, nil
}

// humanGateDescriptions and kernelMutationGateIDs are contract text and
// identifiers, kept verbatim from build_dispatch_plan.py.
var humanGateDescriptions = map[string]string{
	"persistent-database-migration":   "An authorized human must approve persistent database migrations.",
	"production-change":               "An authorized human must approve the exact production change and target.",
	"destructive-action":              "An authorized human must approve the exact destructive action and recovery plan.",
	"accountable-human-escalation":    "An accountable human owner or approval group must make the requested decision.",
	"privileged-identity-change":      "An authorized human must approve privileged identity, credential, or break-glass changes.",
	"halt-authority-determination":    "An accountable human must confirm or lift the halt determination before affected work resumes.",
	"architecture-boundary-violation": "An authorized human must approve any infrastructure boundary crossing that architecture review found missing a required element.",
	"classification-and-marking":      "An authorized human must approve an artifact's classification/marking before it may leave the environment.",
	"retention-deletion-execution":    "An authorized human must confirm the retention/deletion obligation and scope before execution.",
}

// kernelMutationGateIDs maps a selector human gate onto the kernel's own
// mutation gate. A present-but-nil entry is meaningful: it says this gate
// deliberately has no kernel counterpart, which is different from a gate
// nobody has mapped yet.
var kernelMutationGateIDs = map[string]*string{
	"persistent-database-migration": stringPointer("persistent-migration"),
	"production-change":             stringPointer("production-deployment"),
	"destructive-action":            stringPointer("destructive-operation"),
	"privileged-identity-change":    stringPointer("privileged-identity-change"),
	"accountable-human-escalation":  nil,
}

// HumanGate is one entry of the plan's human_gates.
type HumanGate struct {
	ID                 string  `json:"id"`
	Required           bool    `json:"required"`
	Reason             string  `json:"reason"`
	KernelMutationGate *string `json:"kernel_mutation_gate_id"`
}

// BuildHumanGates ports _build_human_gates.
func BuildHumanGates(risks []Match) []HumanGate {
	var ids []string
	for _, risk := range risks {
		if gateID, ok := risk.Rule["human_gate"].(string); ok && gateID != "" {
			ids = append(ids, gateID)
		}
	}
	built := make([]HumanGate, 0, len(ids))
	for _, gateID := range uniqueStrings(ids) {
		reason, ok := humanGateDescriptions[gateID]
		if !ok {
			reason = "An authorized human decision is required."
		}
		built = append(built, HumanGate{
			ID:                 gateID,
			Required:           true,
			Reason:             reason,
			KernelMutationGate: kernelMutationGateIDs[gateID],
		})
	}
	return built
}

func rejectUnknownGates(candidates, known []string, message string) error {
	knownSet := setOf(known)
	var unknown []string
	for _, candidate := range candidates {
		if !knownSet[candidate] {
			unknown = append(unknown, candidate)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("%s: %v", message, unknown)
}

func indexOf(values []string) map[string]int {
	out := make(map[string]int, len(values))
	for index, value := range values {
		out[value] = index
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func stringPointer(value string) *string { return &value }
