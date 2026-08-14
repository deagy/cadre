// schema_validate.go ports roster/orchestration/src/schema_validate.py:
// strict, standalone JSON Schema validation for roster/catalog.yaml and
// roster/orchestration/routing.json.
//
// This is a distinct, complementary check from CheckRouteExcludeShadowing
// (glob_containment.go, reachability/orphan coverage) and the role-metadata
// generators (generation-drift detection). It instead asks a third,
// independent question -- "is this file's own shape/type/enum content
// valid" -- answerable standalone, without invoking any generator. It
// reports every finding in one pass, location-precise (JSON pointer per
// jsonschema's error-path convention), not just the first.
//
// Two JSON Schema documents (Draft 2020-12) hold the bulk of the contract:
// roster/catalog.schema.json and roster/orchestration/routing.schema.json.
// A small number of cross-field consistency checks a JSON Schema document
// cannot cleanly express (a duplicate YAML/JSON object key, a filesystem
// existence check, an integer property compared against a sibling array's
// length) are implemented here as supplementary Go checks, run in addition
// to -- never instead of -- the schema validation.
//
// Regenerates nothing; this file never mutates catalog.yaml or
// routing.json.
package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

// idLineRe matches routing.py-equivalent parse_keyed_entries's own id-line
// character class exactly, so the two line-oriented parsers can't silently
// diverge.
var idLineRe = regexp.MustCompile(`^  ([a-z0-9-]+):\s*$`)

// loadCatalogYAML reads catalog.yaml and returns a JSON-native value
// (map[string]any / []any / string / float64 / bool / nil only): parsed via
// yaml.v3 first, then round-tripped through encoding/json so numeric types
// match what the jsonschema validator expects (yaml.v3 decodes integers as
// Go `int`, not `float64`; a raw JSON Schema validator needs the latter).
func loadCatalogYAML(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var yamlValue any
	if err := yaml.Unmarshal(data, &yamlValue); err != nil {
		return nil, err
	}
	jsonBytes, err := json.Marshal(yamlValue)
	if err != nil {
		return nil, err
	}
	var jsonValue any
	if err := json.Unmarshal(jsonBytes, &jsonValue); err != nil {
		return nil, err
	}
	return jsonValue, nil
}

func loadRoutingJSONRaw(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// schemaErrors compiles schemaPath and validates instance against it,
// returning every leaf finding as a location-precise, sorted string.
func schemaErrors(instance any, schemaPath string) ([]string, error) {
	schema, err := jsonschema.Compile(schemaPath)
	if err != nil {
		return nil, err
	}
	err = schema.Validate(instance)
	if err == nil {
		return nil, nil
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []string{err.Error()}, nil
	}
	var findings []string
	flattenValidationError(ve, &findings)
	sort.Strings(findings)
	return findings, nil
}

// flattenValidationError walks the library's validation error tree down to
// its leaves: an aggregate node (oneOf/allOf/anyOf failing, or the schema
// root) carries Causes and no independently-useful message of its own, so
// only leaves (no Causes) are reported -- mirroring the Python original's
// "surface the most specific sub-error" behavior for oneOf/allOf failures.
func flattenValidationError(ve *jsonschema.ValidationError, out *[]string) {
	if len(ve.Causes) == 0 {
		pointer := ve.InstanceLocation
		if pointer == "" {
			pointer = "$"
		} else {
			pointer = "$/" + pointer
		}
		*out = append(*out, fmt.Sprintf("%s: %s", pointer, ve.Message))
		return
	}
	for _, cause := range ve.Causes {
		flattenValidationError(cause, out)
	}
}

// findDuplicateCatalogIDs detects duplicate `agents:` block role ids in
// catalog.yaml's raw text.
//
// A supplementary check because YAML parsing (and schema validation, which
// operates on the already-parsed value) can no longer see a duplicate key
// by the time a plain map comes back -- the later occurrence has already
// silently overwritten the earlier one. This line-oriented raw-text scan is
// the sole layer that detects it, and it reports every duplicate found
// (not just the first) so a duplicate id and an unrelated schema defect
// elsewhere in the same file can both surface in one run.
func findDuplicateCatalogIDs(catalogText string) []string {
	seen := map[string]int{}
	var findings []string
	inAgentsBlock := false
	for lineNumber, line := range strings.Split(catalogText, "\n") {
		lineNumber++ // 1-indexed, matching the Python original.
		if strings.TrimRight(line, " \t\r") == "agents:" {
			inAgentsBlock = true
			continue
		}
		if !inAgentsBlock {
			continue
		}
		match := idLineRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		roleID := match[1]
		if firstLine, exists := seen[roleID]; exists {
			findings = append(findings, fmt.Sprintf(
				"$.agents[%q]: duplicate role id (first seen at line %d, again at line %d)",
				roleID, firstLine, lineNumber))
		} else {
			seen[roleID] = lineNumber
		}
	}
	return findings
}

// findMissingDefinitions checks that every agents[].definition resolves,
// relative to agentsRoot, to a file that exists on disk -- a filesystem
// check, not expressible in JSON Schema.
func findMissingDefinitions(catalog any, agentsRoot string) []string {
	var findings []string
	catalogMap, ok := catalog.(map[string]any)
	if !ok {
		return findings
	}
	agents, ok := catalogMap["agents"].(map[string]any)
	if !ok {
		return findings
	}
	roleIDs := make([]string, 0, len(agents))
	for id := range agents {
		roleIDs = append(roleIDs, id)
	}
	sort.Strings(roleIDs)
	for _, roleID := range roleIDs {
		record, ok := agents[roleID].(map[string]any)
		if !ok {
			continue
		}
		definition, ok := record["definition"].(string)
		if !ok || definition == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(agentsRoot, definition)); err != nil || info.IsDir() {
			findings = append(findings, fmt.Sprintf(
				"$.agents[%q].definition: %q does not resolve to a file under %s", roleID, definition, agentsRoot))
		}
	}
	return findings
}

// ValidateCatalogSchema runs schema validation plus supplementary checks on
// an already-read catalog.yaml (catalogText, for the duplicate-id text
// scan) and its parsed form (catalog).
func ValidateCatalogSchema(catalogText string, catalog any, schemaPath, agentsRoot string) ([]string, error) {
	var findings []string
	schemaFindings, err := schemaErrors(catalog, schemaPath)
	if err != nil {
		return nil, err
	}
	findings = append(findings, schemaFindings...)
	findings = append(findings, findDuplicateCatalogIDs(catalogText)...)
	findings = append(findings, findMissingDefinitions(catalog, agentsRoot)...)
	return findings, nil
}

// findDuplicateArrayIDs reports every duplicate id within a single
// routes[]/risk_rules[]/team_recipes[]/context_packs[] array, per-array
// rather than only the combined-arrays uniqueness ValidateRouting already
// enforces (and stops at the first violation of). Not expressible as a JSON
// Schema uniqueItems constraint because uniqueItems compares whole array
// elements, not one field of an object element.
func findDuplicateArrayIDs(items any, arrayName string) []string {
	list, ok := items.([]any)
	if !ok {
		return nil
	}
	var findings []string
	seen := map[string]int{}
	for index, raw := range list {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		itemID, ok := item["id"].(string)
		if !ok || itemID == "" {
			continue
		}
		if firstIndex, exists := seen[itemID]; exists {
			findings = append(findings, fmt.Sprintf(
				"$.%s[%d].id: duplicate id %q (also present at %s[%d])", arrayName, index, itemID, arrayName, firstIndex))
		} else {
			seen[itemID] = index
		}
	}
	return findings
}

// findCrossStackInconsistency checks that cross_stack.minimum_matches does
// not exceed len(cross_stack.route_ids) -- a cross-field numeric comparison
// between two sibling properties JSON Schema cannot express cleanly.
func findCrossStackInconsistency(routing map[string]any) []string {
	crossStack, ok := routing["cross_stack"].(map[string]any)
	if !ok {
		return nil
	}
	routeIDs, routeIDsOK := crossStack["route_ids"].([]any)
	minimumMatches, minOK := crossStack["minimum_matches"].(float64)
	if routeIDsOK && minOK && minimumMatches > float64(len(routeIDs)) {
		return []string{fmt.Sprintf(
			"$.cross_stack.minimum_matches: %v exceeds len(cross_stack.route_ids) == %d", minimumMatches, len(routeIDs))}
	}
	return nil
}

// findTeamRecipeInconsistencies is the same class of check as
// findCrossStackInconsistency, for each type:"fixed" team_recipes[] entry's
// minimum_matches vs. route_ids and minimum_members_selected vs. members.
func findTeamRecipeInconsistencies(routing map[string]any) []string {
	recipes, ok := routing["team_recipes"].([]any)
	if !ok {
		return nil
	}
	var findings []string
	for index, raw := range recipes {
		recipe, ok := raw.(map[string]any)
		if !ok || recipe["type"] != "fixed" {
			continue
		}
		recipeID, ok := recipe["id"].(string)
		if !ok {
			recipeID = fmt.Sprintf("index %d", index)
		}
		if routeIDs, ok := recipe["route_ids"].([]any); ok {
			if minimumMatches, ok := recipe["minimum_matches"].(float64); ok && minimumMatches > float64(len(routeIDs)) {
				findings = append(findings, fmt.Sprintf(
					"$.team_recipes[%d] (id=%q).minimum_matches: %v exceeds len(route_ids) == %d",
					index, recipeID, minimumMatches, len(routeIDs)))
			}
		}
		if members, ok := recipe["members"].([]any); ok {
			if minimumMembersSelected, ok := recipe["minimum_members_selected"].(float64); ok && minimumMembersSelected > float64(len(members)) {
				findings = append(findings, fmt.Sprintf(
					"$.team_recipes[%d] (id=%q).minimum_members_selected: %v exceeds len(members) == %d",
					index, recipeID, minimumMembersSelected, len(members)))
			}
		}
	}
	return findings
}

// ValidateRoutingSchema runs schema validation plus supplementary checks on
// an already-parsed routing.json value.
func ValidateRoutingSchema(routing any, schemaPath string) ([]string, error) {
	var findings []string
	schemaFindings, err := schemaErrors(routing, schemaPath)
	if err != nil {
		return nil, err
	}
	findings = append(findings, schemaFindings...)
	if routingMap, ok := routing.(map[string]any); ok {
		findings = append(findings, findDuplicateArrayIDs(routingMap["routes"], "routes")...)
		findings = append(findings, findDuplicateArrayIDs(routingMap["risk_rules"], "risk_rules")...)
		findings = append(findings, findDuplicateArrayIDs(routingMap["team_recipes"], "team_recipes")...)
		findings = append(findings, findDuplicateArrayIDs(routingMap["context_packs"], "context_packs")...)
		findings = append(findings, findCrossStackInconsistency(routingMap)...)
		findings = append(findings, findTeamRecipeInconsistencies(routingMap)...)
	}
	return findings, nil
}

// SchemaValidationPaths collects every file path RunSchemaValidation needs.
// Zero values fall back to the standard roster/ layout relative to
// repoRoot (see DefaultSchemaValidationPaths).
type SchemaValidationPaths struct {
	CatalogPath        string
	RoutingPath        string
	CatalogSchemaPath  string
	RoutingSchemaPath  string
	AgentsRoot         string // Base directory catalog.yaml's `definition` fields resolve against.
	RosterManifestPath string
	RosterSchemaPath   string
}

// DefaultSchemaValidationPaths returns the standard roster/ layout's paths,
// relative to repoRoot.
func DefaultSchemaValidationPaths(repoRoot string) SchemaValidationPaths {
	rosterRoot := filepath.Join(repoRoot, "roster")
	return SchemaValidationPaths{
		CatalogPath:        filepath.Join(rosterRoot, "catalog.yaml"),
		RoutingPath:        filepath.Join(rosterRoot, "orchestration", "routing.json"),
		CatalogSchemaPath:  filepath.Join(rosterRoot, "catalog.schema.json"),
		RoutingSchemaPath:  filepath.Join(rosterRoot, "orchestration", "routing.schema.json"),
		AgentsRoot:         rosterRoot,
		RosterManifestPath: filepath.Join(rosterRoot, "roster.json"),
		RosterSchemaPath:   filepath.Join(rosterRoot, "orchestration", "roster.schema.json"),
	}
}

// RunSchemaValidation returns a deterministic, ordered list of finding
// strings, each prefixed with the file it applies to. Empty means every
// checked document is schema-valid. A structurally-invalid file (malformed
// YAML/JSON) is itself reported as a finding, not returned as an error.
func RunSchemaValidation(paths SchemaValidationPaths) ([]string, error) {
	agentsRoot := paths.AgentsRoot
	if agentsRoot == "" {
		agentsRoot = filepath.Dir(paths.CatalogPath)
	}

	var findings []string

	// A roster manifest is optional: absent is not itself a finding (a
	// roster package predating the manifest is the selector's error to
	// raise by name, not this validator's), but a present-and-invalid one
	// is checked.
	if paths.RosterManifestPath != "" {
		if info, err := os.Stat(paths.RosterManifestPath); err == nil && !info.IsDir() {
			data, err := os.ReadFile(paths.RosterManifestPath)
			if err != nil {
				return nil, err
			}
			var manifest any
			if err := json.Unmarshal(data, &manifest); err != nil {
				findings = append(findings, fmt.Sprintf("%s: invalid JSON: %v", paths.RosterManifestPath, err))
			} else {
				manifestFindings, err := schemaErrors(manifest, paths.RosterSchemaPath)
				if err != nil {
					return nil, err
				}
				for _, f := range manifestFindings {
					findings = append(findings, fmt.Sprintf("%s: %s", paths.RosterManifestPath, f))
				}
			}
		}
	}

	catalogTextBytes, err := os.ReadFile(paths.CatalogPath)
	if err != nil {
		return nil, err
	}
	catalogText := string(catalogTextBytes)
	catalog, err := loadCatalogYAML(paths.CatalogPath)
	if err != nil {
		findings = append(findings, fmt.Sprintf("%s: invalid YAML: %v", paths.CatalogPath, err))
	} else {
		catalogFindings, err := ValidateCatalogSchema(catalogText, catalog, paths.CatalogSchemaPath, agentsRoot)
		if err != nil {
			return nil, err
		}
		for _, f := range catalogFindings {
			findings = append(findings, fmt.Sprintf("%s %s", paths.CatalogPath, f))
		}
	}

	routing, err := loadRoutingJSONRaw(paths.RoutingPath)
	if err != nil {
		findings = append(findings, fmt.Sprintf("%s: invalid JSON: %v", paths.RoutingPath, err))
	} else {
		routingFindings, err := ValidateRoutingSchema(routing, paths.RoutingSchemaPath)
		if err != nil {
			return nil, err
		}
		for _, f := range routingFindings {
			findings = append(findings, fmt.Sprintf("%s %s", paths.RoutingPath, f))
		}
	}

	return findings, nil
}
