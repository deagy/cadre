package kernel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Profile resolution: turning a profile id into the merged document `init`
// writes a project's routing from.
//
// Profiles inherit, and the merge rules are asymmetric on purpose. Scalars and
// most lists replace the parent's; `agents` and `routing` accumulate; gate
// bindings merge per gate, with the child's binding for a gate replacing the
// parent's outright rather than being merged into it. A partially-merged gate
// binding would be a set of contributions no profile author ever wrote.
//
// Everything the merged result names is then checked to exist: unknown gates,
// unknown contribution slots, unknown agents. A profile that routes work to an
// agent no catalog supplies produces a plan naming somebody who cannot be
// dispatched -- and it would be written to disk looking executable.

// MergeProfile resolves a profile and its ancestry.
//
// Order-preserving throughout. The merged document is copied into a project's
// routing.json verbatim, so a route's key order is part of what init writes --
// and Go's maps would alphabetise it. That is also why the gate bindings are
// walked in document order rather than sorted: the agent list this builds ends
// up in the profile digest, and a different order is a different digest.
func (r *Registry) MergeProfile(profileID string) (*orderedObject, error) {
	return r.mergeProfileWithin(profileID, map[string]bool{})
}

// mergeProfileWithin carries the ids already being resolved, so a profile that
// extends itself -- directly or through a chain -- is refused rather than
// recursed into until the stack gives out.
func (r *Registry) mergeProfileWithin(
	profileID string, resolving map[string]bool,
) (*orderedObject, error) {
	if resolving[profileID] {
		return nil, fmt.Errorf("profile %s extends itself", profileID)
	}
	resolving[profileID] = true
	defer delete(resolving, profileID)

	var candidates []string
	for _, root := range r.ProfileRoots {
		path := filepath.Join(root, profileID, "profile.json")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			candidates = append(candidates, path)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("unknown profile: %s", profileID)
	}
	// Two providers supplying the same profile id is ambiguous, and picking
	// either would make which one wins depend on load order.
	if len(candidates) > 1 {
		return nil, fmt.Errorf("duplicate profile: %s", profileID)
	}

	data, err := os.ReadFile(candidates[0])
	if err != nil {
		return nil, err
	}
	decoded, err := DecodeOrdered(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", candidates[0], err)
	}
	child, ok := decoded.(*orderedObject)
	if !ok {
		return nil, fmt.Errorf("profile %s is not a JSON object", profileID)
	}

	version, _ := child.values["version"].(string)
	if child.values["id"] != profileID || version == "" {
		return nil, fmt.Errorf(
			"profile %s has malformed metadata; id and version are required", profileID)
	}
	if _, ok := child.values["gate_bindings"].(*orderedObject); !ok {
		return nil, fmt.Errorf("profile %s must define gate_bindings", profileID)
	}

	result := child
	if parentID, _ := child.values["extends"].(string); parentID != "" {
		parent, err := r.mergeProfileWithin(parentID, resolving)
		if err != nil {
			return nil, err
		}
		result = &orderedObject{values: map[string]any{}}
		for _, key := range parent.keys {
			result.set(key, parent.values[key])
		}
		for _, key := range child.keys {
			// The three that do not simply replace are merged below.
			if key == "agents" || key == "routing" || key == "gate_bindings" {
				continue
			}
			result.set(key, child.values[key])
		}
		result.set("agents", asJSONList(uniqueStrings(append(
			stringsIn(parent.values["agents"]), stringsIn(child.values["agents"])...))))
		result.set("routing", append(append([]any{}, listOf(parent.values["routing"])...),
			listOf(child.values["routing"])...))

		// A child's binding for a gate replaces the parent's outright rather
		// than being merged into it: a half-merged set of contributions is one
		// no profile author ever wrote.
		bindings := &orderedObject{values: map[string]any{}}
		for _, gateID := range orderedKeys(parent.values["gate_bindings"]) {
			bindings.set(gateID, fieldOf(parent.values["gate_bindings"], gateID))
		}
		for _, gateID := range orderedKeys(child.values["gate_bindings"]) {
			bindings.set(gateID, fieldOf(child.values["gate_bindings"], gateID))
		}
		result.set("gate_bindings", bindings)
	}
	result.set("id", profileID)
	if _, present := result.values["gate_bindings"]; !present {
		result.set("gate_bindings", &orderedObject{values: map[string]any{}})
	}

	if err := r.validateMergedProfile(profileID, result); err != nil {
		return nil, err
	}
	return result, nil
}

// validateMergedProfile checks that everything the merged profile names exists,
// and folds the gate bindings' agents into the profile's agent list.
func (r *Registry) validateMergedProfile(profileID string, profile *orderedObject) error {
	knownGates := map[string]bool{}
	for _, id := range GateIDs {
		knownGates[id] = true
	}
	bindings := profile.values["gate_bindings"]
	gateIDs := orderedKeys(bindings)
	var unknownGates []string
	for _, gateID := range gateIDs {
		if !knownGates[gateID] {
			unknownGates = append(unknownGates, gateID)
		}
	}
	if len(unknownGates) > 0 {
		sort.Strings(unknownGates)
		return fmt.Errorf("profile %s references unknown lifecycle gates: %s",
			profileID, pythonList(unknownGates))
	}

	knownSlots, err := contributionSlots()
	if err != nil {
		return err
	}
	catalog, err := r.LoadAgentCatalog()
	if err != nil {
		return err
	}

	// Walked in document order, not sorted: the agent list built below feeds
	// the profile digest, so a different order is a different digest -- and
	// the order the profile's author wrote is the one the Python kernel used.
	var boundAgents []string
	for _, gateID := range gateIDs {
		binding := fieldOf(bindings, gateID)
		contributions := fieldOf(binding, "contributions")
		if _, ok := binding.(*orderedObject); !ok {
			return fmt.Errorf("profile %s has malformed gate contribution binding", profileID)
		}
		if _, ok := contributions.(*orderedObject); !ok {
			return fmt.Errorf("profile %s has malformed gate contribution binding", profileID)
		}

		slotNames := orderedKeys(contributions)
		var unknownSlots []string
		for _, slot := range slotNames {
			if !knownSlots[slot] {
				unknownSlots = append(unknownSlots, slot)
			}
		}
		if len(unknownSlots) > 0 {
			sort.Strings(unknownSlots)
			return fmt.Errorf("profile %s references unknown contribution slots: %s",
				profileID, pythonList(unknownSlots))
		}

		var unknownAgents []string
		for _, slot := range slotNames {
			contribution := fieldOf(contributions, slot)
			if _, ok := contribution.(*orderedObject); !ok {
				return fmt.Errorf("profile %s has malformed contribution metadata", profileID)
			}
			for _, field := range []string{"agents", "tasks", "artifacts"} {
				if _, ok := fieldOf(contribution, field).([]any); !ok {
					return fmt.Errorf("profile %s has malformed contribution metadata", profileID)
				}
			}
			for _, agent := range stringsIn(fieldOf(contribution, "agents")) {
				boundAgents = append(boundAgents, agent)
				if _, known := catalog[agent]; !known {
					unknownAgents = append(unknownAgents, agent)
				}
			}
		}
		if len(unknownAgents) > 0 {
			unknownAgents = uniqueStrings(unknownAgents)
			sort.Strings(unknownAgents)
			return fmt.Errorf("profile %s references unknown agents: %s",
				profileID, pythonList(unknownAgents))
		}
	}

	// An agent bound to a gate is an agent this profile uses, whether or not
	// its own list says so -- otherwise init would write no wrapper for it.
	profile.set("agents", asJSONList(uniqueStrings(
		append(stringsIn(profile.values["agents"]), boundAgents...))))

	for _, raw := range listOf(profile.values["routing"]) {
		route, ok := raw.(*orderedObject)
		if !ok {
			continue
		}
		var unknown []string
		for _, group := range []string{"agents", "reviewers", "support"} {
			for _, agent := range stringsIn(route.values[group]) {
				if _, known := catalog[agent]; !known {
					unknown = append(unknown, agent)
				}
			}
		}
		if len(unknown) > 0 {
			unknown = uniqueStrings(unknown)
			sort.Strings(unknown)
			return fmt.Errorf("profile %s route %v references unknown agents: %s",
				profileID, route.values["id"], pythonList(unknown))
		}
	}
	return nil
}

// contributionSlots is every contribution slot the lifecycle contract names.
func contributionSlots() (map[string]bool, error) {
	gates, err := lifecycleGateList()
	if err != nil {
		return nil, err
	}
	slots := map[string]bool{}
	for _, gate := range gates {
		for _, raw := range listOf(gate["required_contributions"]) {
			if slot, ok := raw.(string); ok {
				slots[slot] = true
			}
		}
	}
	return slots, nil
}

// orderedKeys lists an object's keys in document order, for a value that may
// be an ordered object or an ordinary map.
func orderedKeys(value any) []string {
	if object, ok := value.(*orderedObject); ok {
		return object.keys
	}
	plain, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(plain))
	for key := range plain {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// fieldOf reads one field from either shape -- an ordered object as decoded
// from disk, or a plain map as built in code.
//
// Both occur in the same call paths: a document read order-preserving carries
// ordered objects all the way down, while one constructed by a caller carries
// maps. A helper that handled only one would not fail, it would silently
// return nothing, which reads downstream as "the field is absent".
func fieldOf(value any, key string) any {
	if object, ok := value.(*orderedObject); ok {
		return object.values[key]
	}
	plain, _ := value.(map[string]any)
	return plain[key]
}
