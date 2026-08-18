// Package planning derives which lifecycle gates a task must pass.
//
// Ported from engine/agentic_sdlc_langgraph/planning.py.
package planning

import (
	"fmt"
	"sort"

	"github.com/deagy/cadre/cli/internal/engine/contracts"
)

// DeriveGateSequence returns the cumulative slice of allGates a task needs, in
// lifecycle order.
//
// The sequence is a *prefix*, not the matched set: if any matched route
// references G5, every gate up to G5 is included, because a later gate cannot
// be approved while an earlier applicable one is not. Ignored gates are then
// removed from that prefix.
//
// allGates is expected already in ascending lifecycle order -- the contract's
// own array order. This does not re-sort it; it slices and filters.
//
// An empty result means no route matched, which is a real answer: the caller
// decides what to do with a task that references no gate at all.
func DeriveGateSequence(
	taskText string,
	routes []contracts.Route,
	ignoredGateIDs []string,
	allGates []contracts.Gate,
) ([]contracts.Gate, error) {
	allGateIDs := make([]string, 0, len(allGates))
	indexByID := make(map[string]int, len(allGates))
	gatesByID := make(map[string]contracts.Gate, len(allGates))
	for index, gate := range allGates {
		allGateIDs = append(allGateIDs, gate.ID)
		indexByID[gate.ID] = index
		gatesByID[gate.ID] = gate
	}

	var unknownIgnored []string
	for _, gateID := range ignoredGateIDs {
		if _, known := gatesByID[gateID]; !known {
			unknownIgnored = append(unknownIgnored, gateID)
		}
	}
	if len(unknownIgnored) > 0 {
		sort.Strings(unknownIgnored)
		return nil, fmt.Errorf("ignored_gate_ids contains unknown lifecycle gates: %v", unknownIgnored)
	}

	highestIndex := -1
	for _, route := range contracts.ChooseRoute(taskText, routes) {
		for _, gateID := range route.Gates {
			index, known := indexByID[gateID]
			if !known {
				// A route naming a gate the contract does not have is
				// skipped rather than fatal, matching the Python: profiles
				// and contracts version independently.
				continue
			}
			if index > highestIndex {
				highestIndex = index
			}
		}
	}
	if highestIndex < 0 {
		return nil, nil
	}

	ignored := make(map[string]bool, len(ignoredGateIDs))
	for _, gateID := range ignoredGateIDs {
		ignored[gateID] = true
	}

	sequence := make([]contracts.Gate, 0, highestIndex+1)
	for _, gateID := range allGateIDs[:highestIndex+1] {
		if ignored[gateID] {
			continue
		}
		sequence = append(sequence, gatesByID[gateID])
	}
	return sequence, nil
}
