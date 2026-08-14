// routing_overlay.go ports roster/orchestration/src/routing_overlay.py:
// resolves a project-local overlay of roster/orchestration/routing.json.
//
// A project expresses its overlay as a single JSON file at
// .agents/orchestration/routing-overlay.json, found by walking up from the
// current directory to the nearest .git boundary.
//
// Per-section merge rules (direct port of the Python module's RO-FR-3..
// RO-FR-15 semantics):
//
//   - routes[] / risk_rules[]: an overlay may add a new id-keyed entry (must
//     not collide with any id already present across routes + risk_rules +
//     team_recipes combined), and may widen an *existing* base entry's
//     keywords / keyword_groups / paths by supplying a superset of the base
//     value; any other field present on a widen-patch entry must equal the
//     base value exactly, or resolution fails closed. exclude_paths is
//     deliberately not a widen field: its polarity is inverted (a superset
//     narrows the effective match), so it stays fully immutable on an
//     existing base entry, like human_gate.
//   - team_recipes[]: purely additive. A new, non-colliding id may be added;
//     an existing base id is fully immutable (no widen exception).
//   - change_intake: keywords / agents / quality_gates are additive-only.
//   - cross_stack: route_ids / support are additive-only; minimum_matches
//     may only decrease from the base value, never increase.
//   - knowledge_focus: ordinary structured-file deep-merge, overlay wins per
//     key -- no narrowing restriction.
//   - ignored_gates: may only shrink (remove an already-present entry),
//     never grow.
//   - version: fixed; an overlay may repeat the base value as a no-op but
//     may not change it.
//
// Fails closed on: a malformed/unparsable overlay file, an unrecognized
// top-level or per-section field, an id collision between an overlay-added
// entry and any base routes/risk_rules/team_recipes entry, any attempt to
// add to ignored_gates or change version, any attempt to change a base
// routes[]/risk_rules[] entry's primary/reviewers/support/quality_gates/
// human_gate field, and any attempt to narrow a base entry's
// keywords/keyword_groups/paths.
//
// When no project-local overlay is found, the effective configuration is
// the base file's own bytes, unchanged -- this module never reformats
// routing.json when there is nothing to merge.
package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	cadreconfig "github.com/deagy/cadre/cli/internal/config"
	"github.com/deagy/cadre/cli/internal/platform"
)

// RoutingOverlayError is a malformed overlay or a merge-rule violation.
type RoutingOverlayError struct {
	msg string
}

func (e *RoutingOverlayError) Error() string { return e.msg }

func overlayErrorf(format string, args ...any) error {
	return &RoutingOverlayError{msg: fmt.Sprintf(format, args...)}
}

// OverlayRelativePath is the project-local overlay's fixed location,
// relative to the project root.
var OverlayRelativePath = filepath.Join(".agents", "orchestration", "routing-overlay.json")

var routeRiskWidenFields = []string{"keywords", "keyword_groups", "paths"}
var changeIntakeAdditiveFields = []string{"keywords", "agents", "quality_gates"}
var crossStackAdditiveFields = []string{"route_ids", "support"}
var knownTopLevelKeys = map[string]bool{
	"version": true, "ignored_gates": true, "change_intake": true,
	"routes": true, "risk_rules": true, "cross_stack": true,
	"team_recipes": true, "knowledge_focus": true,
}

// FindRoutingOverlay discovers .agents/orchestration/routing-overlay.json by
// walking up from start (empty string means cwd) to the nearest .git
// boundary. Returns ("", false) if none is found before the boundary (or
// the filesystem root).
//
// Delegates to platform.FindFileAtProjectRoot, the single shared
// implementation of this walk-up-to-.git convention (see that function's
// doc comment) -- this used to be a local reimplementation of the same
// algorithm before internal/platform grew a canonical version.
func FindRoutingOverlay(start string) (string, bool) {
	return platform.FindFileAtProjectRoot(OverlayRelativePath, start)
}

func loadOverlayFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var loaded any
	if err := json.Unmarshal(data, &loaded); err != nil {
		return nil, overlayErrorf("%s: malformed overlay JSON: %v", path, err)
	}
	obj, ok := loaded.(map[string]any)
	if !ok {
		return nil, overlayErrorf("%s: overlay root must be a JSON object", path)
	}
	return obj, nil
}

func getOptionalList(container map[string]any, key, label string) ([]any, error) {
	value, present := container[key]
	if !present || value == nil {
		return nil, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, overlayErrorf("%s must be a list", label)
	}
	return list, nil
}

func entryByID(entries []any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(entries))
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := entry["id"].(string); ok {
			out[id] = entry
		}
	}
	return out
}

func jsonEqual(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

func containsJSON(list []any, item any) bool {
	for _, existing := range list {
		if jsonEqual(existing, item) {
			return true
		}
	}
	return false
}

// widenKeywordGroups implements keyword_groups' AND-of-ORs widen semantics:
// only adding a keyword to an EXISTING group's inner OR-list is a safe
// widen; adding/removing/reordering outer groups changes which base match
// combinations remain reachable.
func widenKeywordGroups(section, entryID string, baseValue, overlayValue []any) ([]any, error) {
	if len(overlayValue) != len(baseValue) {
		return nil, overlayErrorf(
			"%s overlay entry %q changes the number of 'keyword_groups' outer groups (base has %d, "+
				"overlay has %d); each outer group is a mandatory AND-condition, so adding or removing "+
				"one changes which task/file combinations match -- an overlay may only add keywords to "+
				"an EXISTING group's inner OR-list, never add, remove, or reorder outer groups",
			section, entryID, len(baseValue), len(overlayValue))
	}
	result := make([]any, 0, len(overlayValue))
	for index := range overlayValue {
		overlayGroup, ok := overlayValue[index].([]any)
		if !ok {
			return nil, overlayErrorf("%s overlay entry %q field 'keyword_groups'[%d] must be a list", section, entryID, index)
		}
		baseGroup, _ := baseValue[index].([]any)
		var missing []any
		for _, item := range baseGroup {
			if !containsJSON(overlayGroup, item) {
				missing = append(missing, item)
			}
		}
		if len(missing) > 0 {
			return nil, overlayErrorf(
				"%s overlay entry %q narrows base 'keyword_groups'[%d] by omitting already-present "+
					"value(s) %v; an overlay may only widen an existing group's inner OR-list (append), "+
					"never remove or replace an element already present",
				section, entryID, index, missing)
		}
		merged := make([]any, 0, len(overlayGroup))
		for _, item := range overlayGroup {
			if !containsJSON(merged, item) {
				merged = append(merged, item)
			}
		}
		result = append(result, merged)
	}
	return result, nil
}

// widenFieldSuperset requires overlayValue to be a superset of baseValue:
// every element already present in the base entry's field must still be
// present in the overlay-supplied value, or this is a narrowing attempt
// (functionally equivalent to weakening human_gate/reviewers) and rejected.
func widenFieldSuperset(section, entryID, field string, baseValueRaw, overlayValueRaw any) (any, error) {
	overlayValue, ok := overlayValueRaw.([]any)
	if !ok {
		return nil, overlayErrorf("%s overlay entry %q field %q must be a list", section, entryID, field)
	}
	baseValue, _ := baseValueRaw.([]any)

	if field == "keyword_groups" {
		return widenKeywordGroups(section, entryID, baseValue, overlayValue)
	}

	var missing []any
	for _, item := range baseValue {
		if !containsJSON(overlayValue, item) {
			missing = append(missing, item)
		}
	}
	if len(missing) > 0 {
		return nil, overlayErrorf(
			"%s overlay entry %q narrows base %q by omitting already-present value(s) %v; an overlay "+
				"may only widen a base entry's matching conditions (append), never remove or replace an "+
				"element already present",
			section, entryID, field, missing)
	}
	result := make([]any, 0, len(overlayValue))
	for _, item := range overlayValue {
		if !containsJSON(result, item) {
			result = append(result, item)
		}
	}
	return result, nil
}

// applyWidenPatch requires every field on overlayEntry other than id and the
// widen fields to equal the base entry's value exactly (a no-op restatement
// is allowed; any actual change fails closed, naming the entry id and
// field).
func applyWidenPatch(section string, baseEntry, overlayEntry map[string]any) (map[string]any, error) {
	entryID, _ := overlayEntry["id"].(string)
	patched := make(map[string]any, len(baseEntry))
	for k, v := range baseEntry {
		patched[k] = v
	}

	isWidenField := func(key string) bool {
		for _, f := range routeRiskWidenFields {
			if f == key {
				return true
			}
		}
		return false
	}

	for key, value := range overlayEntry {
		if key == "id" || isWidenField(key) {
			continue
		}
		if !jsonEqual(baseEntry[key], value) {
			return nil, overlayErrorf(
				"%s overlay entry %q may not change field %q (base value: %v, overlay value: %v); "+
					"only %v may be widened on a base entry",
				section, entryID, key, baseEntry[key], value, routeRiskWidenFields)
		}
	}
	for _, field := range routeRiskWidenFields {
		overlayValue, present := overlayEntry[field]
		if !present {
			continue
		}
		widened, err := widenFieldSuperset(section, entryID, field, baseEntry[field], overlayValue)
		if err != nil {
			return nil, err
		}
		patched[field] = widened
	}
	return patched, nil
}

func mergeRouteOrRiskRuleSection(section string, baseEntries, overlayEntries []any, combinedIDsSeen map[string]bool) ([]any, error) {
	baseByID := entryByID(baseEntries)
	effective := make([]any, len(baseEntries))
	positionByID := make(map[string]int, len(baseEntries))
	for i, raw := range baseEntries {
		entry, _ := raw.(map[string]any)
		copied := make(map[string]any, len(entry))
		for k, v := range entry {
			copied[k] = v
		}
		effective[i] = copied
		if id, ok := entry["id"].(string); ok {
			positionByID[id] = i
		}
	}

	for _, rawOverlay := range overlayEntries {
		overlayEntry, ok := rawOverlay.(map[string]any)
		if !ok {
			return nil, overlayErrorf("%s overlay entries must be objects", section)
		}
		entryID, ok := overlayEntry["id"].(string)
		if !ok || entryID == "" {
			return nil, overlayErrorf("%s overlay entry is missing a non-empty string 'id'", section)
		}
		if baseEntry, exists := baseByID[entryID]; exists {
			patched, err := applyWidenPatch(section, baseEntry, overlayEntry)
			if err != nil {
				return nil, err
			}
			effective[positionByID[entryID]] = patched
			continue
		}
		if combinedIDsSeen[entryID] {
			return nil, overlayErrorf("%s overlay entry id %q collides with an existing routes/risk_rules/team_recipes id", section, entryID)
		}
		copied := make(map[string]any, len(overlayEntry))
		for k, v := range overlayEntry {
			copied[k] = v
		}
		effective = append(effective, copied)
		combinedIDsSeen[entryID] = true
	}
	return effective, nil
}

// mergeTeamRecipes is purely additive: a base team_recipes[] entry is fully
// immutable to an overlay, with no field-level widen exception.
func mergeTeamRecipes(baseEntries, overlayEntries []any, combinedIDsSeen map[string]bool) ([]any, error) {
	baseByID := entryByID(baseEntries)
	effective := make([]any, len(baseEntries))
	for i, raw := range baseEntries {
		entry, _ := raw.(map[string]any)
		copied := make(map[string]any, len(entry))
		for k, v := range entry {
			copied[k] = v
		}
		effective[i] = copied
	}

	for _, rawOverlay := range overlayEntries {
		overlayEntry, ok := rawOverlay.(map[string]any)
		if !ok {
			return nil, overlayErrorf("team_recipes overlay entries must be objects")
		}
		entryID, ok := overlayEntry["id"].(string)
		if !ok || entryID == "" {
			return nil, overlayErrorf("team_recipes overlay entry is missing a non-empty string 'id'")
		}
		if _, exists := baseByID[entryID]; exists {
			return nil, overlayErrorf(
				"team_recipes overlay may not modify base entry %q; base team recipes are fully "+
					"immutable, only new team_recipes entries may be added", entryID)
		}
		if combinedIDsSeen[entryID] {
			return nil, overlayErrorf("team_recipes overlay entry id %q collides with an existing routes/risk_rules/team_recipes id", entryID)
		}
		copied := make(map[string]any, len(overlayEntry))
		for k, v := range overlayEntry {
			copied[k] = v
		}
		effective = append(effective, copied)
		combinedIDsSeen[entryID] = true
	}
	return effective, nil
}

// mergeChangeIntake: keywords/agents/quality_gates are additive-only.
func mergeChangeIntake(baseCI map[string]any, overlayCIRaw any) (map[string]any, error) {
	merged := make(map[string]any, len(baseCI))
	for k, v := range baseCI {
		merged[k] = v
	}
	if overlayCIRaw == nil {
		return merged, nil
	}
	overlayCI, ok := overlayCIRaw.(map[string]any)
	if !ok {
		return nil, overlayErrorf("change_intake overlay must be an object")
	}
	if unknown := unknownKeys(overlayCI, changeIntakeAdditiveFields); len(unknown) > 0 {
		return nil, overlayErrorf("change_intake overlay has unrecognized field(s): %v", unknown)
	}
	for _, field := range changeIntakeAdditiveFields {
		additionRaw, present := overlayCI[field]
		if !present {
			continue
		}
		addition, ok := additionRaw.([]any)
		if !ok {
			return nil, overlayErrorf("change_intake.%s overlay must be a list", field)
		}
		baseValues, _ := baseCI[field].([]any)
		result := append([]any{}, baseValues...)
		for _, item := range addition {
			if !containsJSON(baseValues, item) {
				result = append(result, item)
			}
		}
		merged[field] = result
	}
	return merged, nil
}

// mergeCrossStack: route_ids/support are additive-only; minimum_matches may
// only decrease from the base value, never increase.
func mergeCrossStack(baseCS map[string]any, overlayCSRaw any) (map[string]any, error) {
	merged := make(map[string]any, len(baseCS))
	for k, v := range baseCS {
		merged[k] = v
	}
	if overlayCSRaw == nil {
		return merged, nil
	}
	overlayCS, ok := overlayCSRaw.(map[string]any)
	if !ok {
		return nil, overlayErrorf("cross_stack overlay must be an object")
	}
	allowed := append(append([]string{}, crossStackAdditiveFields...), "minimum_matches")
	if unknown := unknownKeys(overlayCS, allowed); len(unknown) > 0 {
		return nil, overlayErrorf("cross_stack overlay has unrecognized field(s): %v", unknown)
	}
	for _, field := range crossStackAdditiveFields {
		additionRaw, present := overlayCS[field]
		if !present {
			continue
		}
		addition, ok := additionRaw.([]any)
		if !ok {
			return nil, overlayErrorf("cross_stack.%s overlay must be a list", field)
		}
		baseValues, _ := baseCS[field].([]any)
		result := append([]any{}, baseValues...)
		for _, item := range addition {
			if !containsJSON(baseValues, item) {
				result = append(result, item)
			}
		}
		merged[field] = result
	}
	if overlayValueRaw, present := overlayCS["minimum_matches"]; present {
		overlayValue, ok := overlayValueRaw.(float64)
		if !ok {
			return nil, overlayErrorf("cross_stack.minimum_matches overlay must be an integer")
		}
		if baseValue, ok := baseCS["minimum_matches"].(float64); ok && overlayValue > baseValue {
			return nil, overlayErrorf(
				"cross_stack.minimum_matches overlay may only decrease the base value (%v); overlay "+
					"supplied %v, which would require more matches to trigger cross-stack support and "+
					"would reduce coverage", baseValue, overlayValue)
		}
		merged["minimum_matches"] = overlayValue
	}
	return merged, nil
}

// mergeKnowledgeFocus is an ordinary structured-file deep-merge, no
// narrowing restriction.
func mergeKnowledgeFocus(baseKF map[string]any, overlayKFRaw any) (map[string]any, error) {
	if overlayKFRaw == nil {
		merged := make(map[string]any, len(baseKF))
		for k, v := range baseKF {
			merged[k] = v
		}
		return merged, nil
	}
	overlayKF, ok := overlayKFRaw.(map[string]any)
	if !ok {
		return nil, overlayErrorf("knowledge_focus overlay must be an object")
	}
	return cadreconfig.DeepMergeJSON(baseKF, overlayKF), nil
}

// mergeIgnoredGates may only shrink, never grow.
func mergeIgnoredGates(baseGates []any, overlayGatesRaw any) ([]any, error) {
	if overlayGatesRaw == nil {
		return append([]any{}, baseGates...), nil
	}
	overlayGates, ok := overlayGatesRaw.([]any)
	if !ok {
		return nil, overlayErrorf("ignored_gates overlay must be a list")
	}
	var added []any
	for _, gate := range overlayGates {
		if !containsJSON(baseGates, gate) {
			added = append(added, gate)
		}
	}
	if len(added) > 0 {
		return nil, overlayErrorf(
			"ignored_gates overlay may not add new suppression(s) %v not present in the base "+
				"ignored_gates; an overlay may only remove already-present entries", added)
	}
	return append([]any{}, overlayGates...), nil
}

func checkVersion(baseVersion any, overlay map[string]any) error {
	if overlayVersion, present := overlay["version"]; present {
		if !jsonEqual(overlayVersion, baseVersion) {
			return overlayErrorf(
				"overlay may not change 'version' from %v to %v; it is a fixed schema-version contract "+
					"field, not a per-project dial", baseVersion, overlayVersion)
		}
	}
	return nil
}

func unknownKeys(m map[string]any, allowed []string) []string {
	allowedSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allowedSet[a] = true
	}
	var unknown []string
	for k := range m {
		if !allowedSet[k] {
			unknown = append(unknown, k)
		}
	}
	return unknown
}

// MergeRouting applies every per-section merge rule and returns the
// effective configuration. Returns a *RoutingOverlayError on any violation.
func MergeRouting(base, overlay map[string]any) (map[string]any, error) {
	if unknown := unknownKeys(overlay, topLevelKeySlice()); len(unknown) > 0 {
		return nil, overlayErrorf("overlay has unrecognized top-level field(s): %v", unknown)
	}
	if err := checkVersion(base["version"], overlay); err != nil {
		return nil, err
	}

	baseRoutes, _ := base["routes"].([]any)
	baseRiskRules, _ := base["risk_rules"].([]any)
	baseTeamRecipes, _ := base["team_recipes"].([]any)

	combinedIDsSeen := make(map[string]bool)
	for _, entries := range [][]any{baseRoutes, baseRiskRules, baseTeamRecipes} {
		for id := range entryByID(entries) {
			combinedIDsSeen[id] = true
		}
	}

	effective := make(map[string]any, len(base))
	for k, v := range base {
		effective[k] = v
	}

	overlayRoutes, err := getOptionalList(overlay, "routes", "routes overlay")
	if err != nil {
		return nil, err
	}
	if effective["routes"], err = mergeRouteOrRiskRuleSection("routes", baseRoutes, overlayRoutes, combinedIDsSeen); err != nil {
		return nil, err
	}

	overlayRiskRules, err := getOptionalList(overlay, "risk_rules", "risk_rules overlay")
	if err != nil {
		return nil, err
	}
	if effective["risk_rules"], err = mergeRouteOrRiskRuleSection("risk_rules", baseRiskRules, overlayRiskRules, combinedIDsSeen); err != nil {
		return nil, err
	}

	overlayTeamRecipes, err := getOptionalList(overlay, "team_recipes", "team_recipes overlay")
	if err != nil {
		return nil, err
	}
	if effective["team_recipes"], err = mergeTeamRecipes(baseTeamRecipes, overlayTeamRecipes, combinedIDsSeen); err != nil {
		return nil, err
	}

	baseChangeIntake, _ := base["change_intake"].(map[string]any)
	if effective["change_intake"], err = mergeChangeIntake(baseChangeIntake, overlay["change_intake"]); err != nil {
		return nil, err
	}

	baseCrossStack, _ := base["cross_stack"].(map[string]any)
	if effective["cross_stack"], err = mergeCrossStack(baseCrossStack, overlay["cross_stack"]); err != nil {
		return nil, err
	}

	baseKnowledgeFocus, _ := base["knowledge_focus"].(map[string]any)
	if effective["knowledge_focus"], err = mergeKnowledgeFocus(baseKnowledgeFocus, overlay["knowledge_focus"]); err != nil {
		return nil, err
	}

	baseIgnoredGates, _ := base["ignored_gates"].([]any)
	if effective["ignored_gates"], err = mergeIgnoredGates(baseIgnoredGates, overlay["ignored_gates"]); err != nil {
		return nil, err
	}

	return effective, nil
}

func topLevelKeySlice() []string {
	keys := make([]string, 0, len(knownTopLevelKeys))
	for k := range knownTopLevelKeys {
		keys = append(keys, k)
	}
	return keys
}

// validateEffective round-trips the merged configuration through this
// package's own RoutingConfig unmarshal + ValidateRouting, reusing existing
// uniqueness/shape invariants rather than duplicating that validation.
func validateEffective(effective map[string]any) error {
	data, err := json.Marshal(effective)
	if err != nil {
		return overlayErrorf("effective configuration failed validation: %v", err)
	}
	var config RoutingConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return overlayErrorf("effective configuration failed validation: %v", err)
	}
	if err := ValidateRouting(&config); err != nil {
		return overlayErrorf("effective configuration failed validation: %v", err)
	}
	return nil
}

type resolvedOverlayResult struct {
	effective    map[string]any
	baseText     string
	overlayPath  string
	overlayFound bool
}

func resolveOverlay(basePath, start, overlayPathOverride string) (*resolvedOverlayResult, error) {
	baseBytes, err := os.ReadFile(basePath)
	if err != nil {
		return nil, err
	}
	var base map[string]any
	if err := json.Unmarshal(baseBytes, &base); err != nil {
		return nil, overlayErrorf("%s: root must be a JSON object: %v", basePath, err)
	}

	resolvedOverlayPath := overlayPathOverride
	found := resolvedOverlayPath != ""
	if !found {
		resolvedOverlayPath, found = FindRoutingOverlay(start)
	}
	if !found {
		return &resolvedOverlayResult{effective: base, baseText: string(baseBytes)}, nil
	}

	overlay, err := loadOverlayFile(resolvedOverlayPath)
	if err != nil {
		return nil, err
	}
	effective, err := MergeRouting(base, overlay)
	if err != nil {
		return nil, err
	}
	if err := validateEffective(effective); err != nil {
		return nil, err
	}
	return &resolvedOverlayResult{
		effective:    effective,
		baseText:     string(baseBytes),
		overlayPath:  resolvedOverlayPath,
		overlayFound: true,
	}, nil
}

// ResolveEffectiveRouting resolves the effective routing configuration: with
// no project-local overlay, returns the base configuration exactly (parsed,
// content-identical). With an overlay, returns the merged effective
// configuration. Returns (config, overlayPath, overlayFound).
func ResolveEffectiveRouting(basePath, start, overlayPathOverride string) (map[string]any, string, bool, error) {
	result, err := resolveOverlay(basePath, start, overlayPathOverride)
	if err != nil {
		return nil, "", false, err
	}
	return result.effective, result.overlayPath, result.overlayFound, nil
}

// MaterializeEffectiveRouting writes the effective configuration to
// outPath. With no overlay, outPath receives the base file's own bytes
// verbatim -- never a re-serialized round-trip -- so the no-overlay case is
// byte-for-byte identical to routing.json itself. Returns (overlayPath,
// overlayFound).
func MaterializeEffectiveRouting(outPath, basePath, start, overlayPathOverride string) (string, bool, error) {
	result, err := resolveOverlay(basePath, start, overlayPathOverride)
	if err != nil {
		return "", false, err
	}
	if !result.overlayFound {
		if err := os.WriteFile(outPath, []byte(result.baseText), 0o644); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	data, err := json.MarshalIndent(result.effective, "", "  ")
	if err != nil {
		return "", false, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return "", false, err
	}
	return result.overlayPath, true, nil
}

// EffectiveRoutingConfig converts a resolved effective configuration map
// (from ResolveEffectiveRouting) into a typed *RoutingConfig, for direct use
// by the select pipeline without a temp-file round trip.
func EffectiveRoutingConfig(effective map[string]any) (*RoutingConfig, error) {
	data, err := json.Marshal(effective)
	if err != nil {
		return nil, err
	}
	var config RoutingConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}
