// Package contracts loads the declarative Agentic SDLC contracts and answers
// the small phrase-matching questions the orchestration graph asks of them.
//
// Read-only, against `kernel/contracts/` and a provider's profile. Nothing
// here writes to those trees, and nothing here links the kernel: the contracts
// are consumed as data, which is the coupling internal/kernel's boundary test
// permits. Ported from engine/agentic_sdlc_langgraph/contracts.py.
package contracts

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Gate is one lifecycle gate from lifecycle-gates.json.
type Gate struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Phase                 string   `json:"phase"`
	Prerequisites         []string `json:"prerequisites"`
	RequiredContributions []string `json:"required_contributions"`
	AuthorityRequirements []string `json:"authority_requirements"`
}

// MutationGate is one human-only gate from mutation-gates.json.
type MutationGate struct {
	ID      string   `json:"id"`
	Phrases []string `json:"phrases"`
}

// AgentCatalogEntry describes one agent in agent-catalog.json.
type AgentCatalogEntry struct {
	Kind         string   `json:"kind"`
	Capabilities []string `json:"capabilities"`
}

// Contribution is what one binding slot supplies to a gate.
type Contribution struct {
	Agents    []string `json:"agents"`
	Tasks     []string `json:"tasks"`
	Artifacts []string `json:"artifacts"`
}

// GateBinding binds contribution slots for a single gate.
type GateBinding struct {
	Contributions map[string]Contribution `json:"contributions"`
}

// Route is one entry of a profile's routing array.
type Route struct {
	ID        string   `json:"id"`
	Phrases   []string `json:"phrases"`
	Agents    []string `json:"agents"`
	Reviewers []string `json:"reviewers"`
	Support   []string `json:"support"`
	Gates     []string `json:"gates"`
}

// Profile is a provider profile document.
//
// No `extends`-chain merge, matching the Python: it loads a single profile
// directly, and the profiles that exist declare no `extends`. If one ever
// does, this must grow the merge rather than silently ignore the field.
type Profile struct {
	ID           string                 `json:"id"`
	Version      string                 `json:"version"`
	GateBindings map[string]GateBinding `json:"gate_bindings"`
	Routing      []Route                `json:"routing"`
}

func loadJSON(path string, into any) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(contents, into); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// LoadLifecycleGates returns the `gates` array from lifecycle-gates.json.
func LoadLifecycleGates(path string) ([]Gate, error) {
	var document struct {
		Gates []Gate `json:"gates"`
	}
	if err := loadJSON(path, &document); err != nil {
		return nil, err
	}
	return document.Gates, nil
}

// LoadMutationGates returns the `human_only` array from mutation-gates.json.
func LoadMutationGates(path string) ([]MutationGate, error) {
	var document struct {
		HumanOnly []MutationGate `json:"human_only"`
	}
	if err := loadJSON(path, &document); err != nil {
		return nil, err
	}
	return document.HumanOnly, nil
}

// LoadAgentCatalog returns the `agents` mapping from agent-catalog.json.
func LoadAgentCatalog(path string) (map[string]AgentCatalogEntry, error) {
	var document struct {
		Agents map[string]AgentCatalogEntry `json:"agents"`
	}
	if err := loadJSON(path, &document); err != nil {
		return nil, err
	}
	return document.Agents, nil
}

// LoadProfile returns a provider profile document.
func LoadProfile(path string) (Profile, error) {
	var profile Profile
	err := loadJSON(path, &profile)
	return profile, err
}

// Dispatch is what a gate's bound contributions resolve to.
type Dispatch struct {
	Agents    []string
	Tasks     []string
	Artifacts []string
}

// GateDispatchBinding resolves the agents, tasks and artifacts bound to a
// gate's required_contributions slots.
//
// Order is the order slots are declared on the gate, then the order values
// appear within a slot; duplicates are dropped, keeping the first occurrence.
// Callers render this into plans a human reads, so a stable order is part of
// the contract rather than an accident of iteration.
func GateDispatchBinding(gate Gate, bindings map[string]GateBinding) Dispatch {
	var dispatch Dispatch
	binding, bound := bindings[gate.ID]
	if !bound {
		return dispatch
	}

	appendUnique := func(into []string, values []string) []string {
		for _, value := range values {
			seen := false
			for _, existing := range into {
				if existing == value {
					seen = true
					break
				}
			}
			if !seen {
				into = append(into, value)
			}
		}
		return into
	}

	for _, slot := range gate.RequiredContributions {
		contribution, ok := binding.Contributions[slot]
		if !ok {
			continue
		}
		dispatch.Agents = appendUnique(dispatch.Agents, contribution.Agents)
		dispatch.Tasks = appendUnique(dispatch.Tasks, contribution.Tasks)
		dispatch.Artifacts = appendUnique(dispatch.Artifacts, contribution.Artifacts)
	}
	return dispatch
}

// ChooseRoute returns the routes whose phrases appear in the task text.
func ChooseRoute(taskText string, routes []Route) []Route {
	lowered := strings.ToLower(taskText)
	var matched []Route
	for _, route := range routes {
		for _, phrase := range route.Phrases {
			if strings.Contains(lowered, strings.ToLower(phrase)) {
				matched = append(matched, route)
				break
			}
		}
	}
	return matched
}

// MutationGateMatch is one human-only gate a task text tripped.
type MutationGateMatch struct {
	ID     string `json:"id"`
	Phrase string `json:"phrase"`
	Reason string `json:"reason"`
}

// MutationGateGuard reports the human-only gates a task text trips.
//
// Returns nil when nothing matches, mirroring the Python's None.
//
// Both sides are lowercased. The Python lowered the task text but compared
// the phrase verbatim, so any phrase carrying a capital letter could never
// match — a human-only gate that silently never fires, which is the worst
// possible direction for this particular check to fail in. Every phrase in
// mutation-gates.json is lowercase today, so the defect is latent rather
// than live, and porting it faithfully would carry a trap forward for
// whoever writes the first phrase with a capital in it.
//
// At most one match is recorded per gate: the first phrase that hits.
func MutationGateGuard(taskText string, gates []MutationGate) []MutationGateMatch {
	lowered := strings.ToLower(taskText)
	var matched []MutationGateMatch
	for _, gate := range gates {
		for _, phrase := range gate.Phrases {
			if strings.Contains(lowered, strings.ToLower(phrase)) {
				matched = append(matched, MutationGateMatch{
					ID:     gate.ID,
					Phrase: phrase,
					Reason: "Matched human-only phrase: " + phrase,
				})
				break
			}
		}
	}
	if len(matched) == 0 {
		return nil
	}
	return matched
}
