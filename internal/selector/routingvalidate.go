package selector

import (
	"fmt"
	"sort"
)

// ValidateRoutingConfig asserts routing.json's structural invariants on an
// already-assembled configuration, mirroring routing.validate_routing_config.
//
// The merge rules in overlay.go and these invariants answer different
// questions. The merge rules ask "was this edit permitted"; this asks "is the
// result usable at all". An overlay can pass every merge rule and still
// produce a config the selector cannot run -- adding a route whose id
// duplicates a context pack's, say -- so the effective config is validated
// before it is ever returned, never merely on its way in from disk.
func ValidateRoutingConfig(config map[string]any) error {
	version, _ := jsonInteger(config["version"])
	_, routesAreList := config["routes"].([]any)
	_, risksAreList := config["risk_rules"].([]any)
	if version != 1 || !routesAreList || !risksAreList {
		return fmt.Errorf("routing.json must contain version 1 routes and risk_rules")
	}

	// Routes, risk rules and team recipes share one id namespace: a dispatch
	// plan puts matched_routes[].id and matched_risks[].id side by side, so a
	// duplicate is ambiguous for any consumer keying on plan ids.
	var ids []any
	for _, section := range []string{"routes", "risk_rules", "team_recipes"} {
		for _, entry := range objectList(config[section]) {
			ids = append(ids, entry["id"])
		}
	}
	if len(dedupeJSON(ids)) != len(ids) {
		return fmt.Errorf("Routing, risk rule, and team recipe IDs must be unique") //nolint:staticcheck // ported message
	}

	for _, section := range []string{"routes", "risk_rules"} {
		for _, rule := range objectList(config[section]) {
			groups, present := rule["keyword_groups"]
			if !present || groups == nil {
				continue
			}
			groupList, ok := groups.([]any)
			if !ok {
				return keywordGroupsError(rule)
			}
			if len(groupList) == 0 {
				continue
			}
			for _, group := range groupList {
				inner, ok := group.([]any)
				if !ok || len(inner) == 0 {
					return keywordGroupsError(rule)
				}
				for _, keyword := range inner {
					text, ok := keyword.(string)
					if !ok || text == "" {
						return keywordGroupsError(rule)
					}
				}
			}
		}
	}

	// workflow_shape is validated by value, not required by presence: an
	// overlay may add a route without one, which contributes no delivery
	// shape and is reported in the plan rather than rejected here. A
	// *misspelled* shape is the case worth failing on, because it would
	// contribute nothing while looking declared.
	for _, route := range objectList(config["routes"]) {
		shape, present := route["workflow_shape"]
		if !present || shape == nil {
			continue
		}
		text, ok := shape.(string)
		if !ok || !workflowShapes[text] {
			return fmt.Errorf("%s workflow_shape must be one of %s, got %s",
				idOrDefault(route, "route"), pythonList(sortedWorkflowShapes()), pythonRepr(shape))
		}
	}

	packs, present := config["context_packs"]
	if present && packs != nil {
		packList, ok := packs.([]any)
		if !ok {
			return fmt.Errorf("routing.json context_packs must be a list")
		}
		claimed := append([]any{}, ids...)
		for _, raw := range packList {
			pack, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("routing.json context_packs entries must be objects")
			}
			packID, idOK := pack["id"].(string)
			definition, definitionOK := pack["definition"].(string)
			if !idOK || packID == "" || !definitionOK || definition == "" {
				return fmt.Errorf("routing.json context_packs entries require non-empty id and definition")
			}
			version, versionOK := jsonInteger(pack["version"])
			if _, isBool := pack["version"].(bool); !versionOK || isBool || version < 1 {
				return fmt.Errorf("%s context pack version must be a positive integer", packID)
			}
			if containsJSON(claimed, packID) {
				return fmt.Errorf("duplicate context pack id: %s", packID)
			}
			claimed = append(claimed, packID)
		}
	}

	for _, recipe := range objectList(config["team_recipes"]) {
		if recipe["type"] != "dynamic" {
			continue
		}
		instances := objectOf(recipe["instances"])
		minimum, minimumOK := jsonInteger(instances["min"])
		maximum, maximumOK := jsonInteger(instances["max"])
		_, minimumIsBool := instances["min"].(bool)
		_, maximumIsBool := instances["max"].(bool)
		if !minimumOK || !maximumOK || minimumIsBool || maximumIsBool || minimum < 1 || maximum < minimum {
			return fmt.Errorf("%s instances must satisfy 1 <= min <= max", idOrDefault(recipe, "team recipe"))
		}
	}
	return nil
}

var workflowShapes = map[string]bool{
	"new-service": true, "infrastructure-change": true,
	"pipeline-change": true, "unclassified": true,
}

func sortedWorkflowShapes() []string {
	shapes := make([]string, 0, len(workflowShapes))
	for shape := range workflowShapes {
		shapes = append(shapes, shape)
	}
	sort.Strings(shapes)
	return shapes
}

func keywordGroupsError(rule map[string]any) error {
	return fmt.Errorf("%s keyword_groups must contain non-empty string groups", idOrDefault(rule, "rule"))
}

func idOrDefault(entry map[string]any, fallback string) string {
	if id, ok := entry["id"].(string); ok && id != "" {
		return id
	}
	return fallback
}
