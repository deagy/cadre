package selector

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
)

// Project-local routing overlays: the merge half of routing_overlay.py.
//
// An overlay is a project's customisation surface over routing.json. It is
// not a general-purpose config merge, because most of routing.json's sections
// carry gating and review-separation semantics: a route's `reviewers` and
// `human_gate` are who must look at a change before it ships. So every rule
// below is asymmetric, and every one fails closed.
//
// The single organising idea is that an overlay may *widen* what matches and
// may *add* new entries, but may never narrow or weaken what the base already
// declares. Narrowing a base route's `paths` is not a smaller edit than
// deleting its `reviewers` -- it has the same effect, reached indirectly, and
// is rejected the same way.

// OverlayRelativePath is where a project declares its overlay. It is found by
// walking up to the nearest .git boundary, the same convention every other
// project-local override in this repository uses.
var OverlayRelativePath = []string{".agents", "orchestration", "routing-overlay.json"}

var (
	routeRiskWidenFields      = []string{"keywords", "keyword_groups", "paths"}
	changeIntakeAdditiveField = []string{"keywords", "agents", "quality_gates"}
	crossStackAdditiveFields  = []string{"route_ids", "support"}
	knownTopLevelKeys         = map[string]bool{
		"version": true, "ignored_gates": true, "change_intake": true,
		"routes": true, "risk_rules": true, "cross_stack": true,
		"team_recipes": true, "knowledge_focus": true,
	}
)

// OverlayError is a malformed overlay, or one that violates a merge rule.
type OverlayError struct{ message string }

func (e *OverlayError) Error() string { return e.message }

func overlayErrorf(format string, args ...any) error {
	return &OverlayError{message: fmt.Sprintf(format, args...)}
}

// LoadOverlay reads and shape-checks an overlay document.
func LoadOverlay(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var loaded any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&loaded); err != nil {
		return nil, overlayErrorf("%s: malformed overlay JSON: %s", path, err)
	}
	object, ok := loaded.(map[string]any)
	if !ok {
		return nil, overlayErrorf("%s: overlay root must be a JSON object", path)
	}
	return object, nil
}

// MergeRouting applies every per-section rule and returns the effective
// configuration, or refuses.
//
// `base` is decoded with float64 numbers (as the rest of the selector reads
// it); `overlay` is decoded with json.Number, because one rule -- see
// mergeCrossStack -- has to tell an integer literal from a float one, a
// distinction float64 has already lost by the time it is observable.
func MergeRouting(base, overlay map[string]any) (map[string]any, error) {
	var unknown []string
	for key := range overlay {
		if !knownTopLevelKeys[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, overlayErrorf("overlay has unrecognized top-level field(s): %s", pythonList(unknown))
	}

	if err := checkVersion(base["version"], overlay); err != nil {
		return nil, err
	}

	// Ids are one namespace across all three sections, so a new entry has to
	// clear every existing id, not just its own section's.
	seenIDs := map[string]bool{}
	for _, section := range []string{"routes", "risk_rules", "team_recipes"} {
		for _, entry := range objectList(base[section]) {
			if id, ok := entry["id"].(string); ok {
				seenIDs[id] = true
			}
		}
	}

	effective := make(map[string]any, len(base))
	for key, value := range base {
		effective[key] = value
	}

	routesOverlay, err := optionalList(overlay, "routes", "routes overlay")
	if err != nil {
		return nil, err
	}
	merged, err := mergeRouteOrRiskSection("routes", objectList(base["routes"]), routesOverlay, seenIDs)
	if err != nil {
		return nil, err
	}
	effective["routes"] = merged

	risksOverlay, err := optionalList(overlay, "risk_rules", "risk_rules overlay")
	if err != nil {
		return nil, err
	}
	merged, err = mergeRouteOrRiskSection("risk_rules", objectList(base["risk_rules"]), risksOverlay, seenIDs)
	if err != nil {
		return nil, err
	}
	effective["risk_rules"] = merged

	recipesOverlay, err := optionalList(overlay, "team_recipes", "team_recipes overlay")
	if err != nil {
		return nil, err
	}
	mergedRecipes, err := mergeTeamRecipes(objectList(base["team_recipes"]), recipesOverlay, seenIDs)
	if err != nil {
		return nil, err
	}
	effective["team_recipes"] = mergedRecipes

	intake, err := mergeChangeIntake(objectOf(base["change_intake"]), overlay["change_intake"])
	if err != nil {
		return nil, err
	}
	effective["change_intake"] = intake

	crossStack, err := mergeCrossStack(objectOf(base["cross_stack"]), overlay["cross_stack"])
	if err != nil {
		return nil, err
	}
	effective["cross_stack"] = crossStack

	focus, err := mergeKnowledgeFocus(objectOf(base["knowledge_focus"]), overlay["knowledge_focus"])
	if err != nil {
		return nil, err
	}
	effective["knowledge_focus"] = focus

	gates, err := mergeIgnoredGates(anyList(base["ignored_gates"]), overlay["ignored_gates"])
	if err != nil {
		return nil, err
	}
	effective["ignored_gates"] = gates

	return effective, nil
}

// checkVersion keeps `version` a fixed schema-version contract field rather
// than a per-project dial. Restating the base value is a permitted no-op.
func checkVersion(baseVersion any, overlay map[string]any) error {
	value, present := overlay["version"]
	if !present {
		return nil
	}
	if !jsonEqual(value, baseVersion) {
		return overlayErrorf(
			"overlay may not change 'version' from %s to %s; "+
				"it is a fixed schema-version contract field, not a per-project dial",
			pythonRepr(baseVersion), pythonRepr(value))
	}
	return nil
}

// mergeRouteOrRiskSection handles routes[] and risk_rules[]: an overlay entry
// whose id already exists is a widen-patch against that entry; one whose id is
// new is an addition that must not collide.
func mergeRouteOrRiskSection(
	section string, baseEntries []map[string]any, overlayEntries []any, seenIDs map[string]bool,
) ([]any, error) {
	baseByID := map[string]map[string]any{}
	positionByID := map[string]int{}
	effective := make([]any, 0, len(baseEntries))
	for index, entry := range baseEntries {
		effective = append(effective, copyObject(entry))
		if id, ok := entry["id"].(string); ok {
			baseByID[id] = entry
			positionByID[id] = index
		}
	}

	for _, raw := range overlayEntries {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, overlayErrorf("%s overlay entries must be objects", section)
		}
		id, ok := entry["id"].(string)
		if !ok || id == "" {
			return nil, overlayErrorf("%s overlay entry is missing a non-empty string 'id'", section)
		}
		if baseEntry, exists := baseByID[id]; exists {
			patched, err := applyWidenPatch(section, baseEntry, entry)
			if err != nil {
				return nil, err
			}
			effective[positionByID[id]] = patched
			continue
		}
		if seenIDs[id] {
			return nil, overlayErrorf(
				"%s overlay entry id %s collides with an existing routes/"+
					"risk_rules/team_recipes id", section, pythonRepr(id))
		}
		effective = append(effective, copyObject(entry))
		seenIDs[id] = true
	}
	return effective, nil
}

// applyWidenPatch permits only the widen fields to change on an existing base
// entry. Everything else must be restated exactly or omitted.
//
// This is the rule that makes the whole mechanism safe: `reviewers`,
// `human_gate`, `primary`, `support` and `quality_gates` are all reachable
// only by exact restatement, so an overlay cannot quietly drop a reviewer
// from a route it is nominally just adding a keyword to.
func applyWidenPatch(section string, baseEntry, overlayEntry map[string]any) (map[string]any, error) {
	id, _ := overlayEntry["id"].(string)
	widen := map[string]bool{}
	for _, field := range routeRiskWidenFields {
		widen[field] = true
	}

	patched := copyObject(baseEntry)
	// Sorted so a document with several illegal fields always names the same
	// one first; Go map iteration would otherwise report a different field
	// per run for one unchanged overlay.
	for _, key := range sortedKeys(overlayEntry) {
		if key == "id" || widen[key] {
			continue
		}
		if !jsonEqual(baseEntry[key], overlayEntry[key]) {
			return nil, overlayErrorf(
				"%s overlay entry %s may not change field %s "+
					"(base value: %s, overlay value: %s); only "+
					"%s may be widened on a base entry",
				section, pythonRepr(id), pythonRepr(key),
				pythonRepr(baseEntry[key]), pythonRepr(overlayEntry[key]),
				strings.Join(routeRiskWidenFields, ", "))
		}
	}
	for _, field := range routeRiskWidenFields {
		value, present := overlayEntry[field]
		if !present {
			continue
		}
		widened, err := widenFieldSuperset(section, id, field, baseEntry[field], value)
		if err != nil {
			return nil, err
		}
		patched[field] = widened
	}
	return patched, nil
}

// widenFieldSuperset requires the overlay's value to be a superset of the
// base's: every element already present must still be present. Removing one
// is a narrowing attempt -- functionally the same as weakening the entry's
// human_gate or reviewers without ever naming those fields -- and is rejected
// whether or not this entry happens to declare a human_gate at all.
func widenFieldSuperset(section, id, field string, baseValue, overlayValue any) ([]any, error) {
	overlayList, ok := overlayValue.([]any)
	if !ok {
		return nil, overlayErrorf("%s overlay entry %s field %s must be a list",
			section, pythonRepr(id), pythonRepr(field))
	}
	baseList := anyList(baseValue)

	if field == "keyword_groups" {
		return widenKeywordGroups(section, id, baseList, overlayList)
	}

	var missing []any
	for _, item := range baseList {
		if !containsJSON(overlayList, item) {
			missing = append(missing, item)
		}
	}
	if len(missing) > 0 {
		return nil, overlayErrorf(
			"%s overlay entry %s narrows base %s by omitting "+
				"already-present value(s) %s; an overlay may only widen a base entry's "+
				"matching conditions (append), never remove or replace an element already present",
			section, pythonRepr(id), pythonRepr(field), pythonRepr(missing))
	}
	return dedupeJSON(overlayList), nil
}

// widenKeywordGroups is the case where "widen" means the opposite of what it
// looks like.
//
// keyword_groups is an AND-of-ORs: match_rule requires every outer group to
// have at least one matching keyword, while each group's inner list is an OR.
// So appending a brand-new outer group adds a mandatory condition and
// *narrows* overall matching -- some base matches are lost -- even though a
// plain list-superset check would wave it through as additive.
//
// The only genuinely widening operation is adding a keyword to an existing
// group's inner OR-list.
func widenKeywordGroups(section, id string, baseValue, overlayValue []any) ([]any, error) {
	if len(overlayValue) != len(baseValue) {
		return nil, overlayErrorf(
			"%s overlay entry %s changes the number of 'keyword_groups' outer "+
				"groups (base has %d, overlay has %d); each outer "+
				"group is a mandatory AND-condition, so adding or removing one changes which task/file "+
				"combinations match -- an overlay may only add keywords to an EXISTING group's inner "+
				"OR-list, never add, remove, or reorder outer groups",
			section, pythonRepr(id), len(baseValue), len(overlayValue))
	}
	result := make([]any, 0, len(overlayValue))
	for index := range overlayValue {
		overlayGroup, ok := overlayValue[index].([]any)
		if !ok {
			return nil, overlayErrorf("%s overlay entry %s field 'keyword_groups'[%d] must be a list",
				section, pythonRepr(id), index)
		}
		var missing []any
		for _, item := range anyList(baseValue[index]) {
			if !containsJSON(overlayGroup, item) {
				missing = append(missing, item)
			}
		}
		if len(missing) > 0 {
			return nil, overlayErrorf(
				"%s overlay entry %s narrows base 'keyword_groups'[%d] by "+
					"omitting already-present value(s) %s; an overlay may only widen an "+
					"existing group's inner OR-list (append), never remove or replace an element "+
					"already present",
				section, pythonRepr(id), index, pythonRepr(missing))
		}
		result = append(result, dedupeJSON(overlayGroup))
	}
	return result, nil
}

// mergeTeamRecipes is purely additive: a base recipe is fully immutable, with
// no widen exception at all. A team recipe names who collaborates on a change,
// so there is no field on one an overlay has any business adjusting.
func mergeTeamRecipes(baseEntries []map[string]any, overlayEntries []any, seenIDs map[string]bool) ([]any, error) {
	baseByID := map[string]bool{}
	effective := make([]any, 0, len(baseEntries))
	for _, entry := range baseEntries {
		effective = append(effective, copyObject(entry))
		if id, ok := entry["id"].(string); ok {
			baseByID[id] = true
		}
	}

	for _, raw := range overlayEntries {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, overlayErrorf("team_recipes overlay entries must be objects")
		}
		id, ok := entry["id"].(string)
		if !ok || id == "" {
			return nil, overlayErrorf("team_recipes overlay entry is missing a non-empty string 'id'")
		}
		if baseByID[id] {
			return nil, overlayErrorf(
				"team_recipes overlay may not modify base entry %s; base team "+
					"recipes are fully immutable, only new team_recipes entries may be added",
				pythonRepr(id))
		}
		if seenIDs[id] {
			return nil, overlayErrorf(
				"team_recipes overlay entry id %s collides with an existing "+
					"routes/risk_rules/team_recipes id", pythonRepr(id))
		}
		effective = append(effective, copyObject(entry))
		seenIDs[id] = true
	}
	return effective, nil
}

// mergeChangeIntake: keywords/agents/quality_gates are additive-only.
func mergeChangeIntake(base map[string]any, overlayValue any) (map[string]any, error) {
	if overlayValue == nil {
		return copyObject(base), nil
	}
	overlay, ok := overlayValue.(map[string]any)
	if !ok {
		return nil, overlayErrorf("change_intake overlay must be an object")
	}
	if err := rejectUnknown("change_intake", overlay, changeIntakeAdditiveField); err != nil {
		return nil, err
	}
	merged := copyObject(base)
	for _, field := range changeIntakeAdditiveField {
		addition, present := overlay[field]
		if !present {
			continue
		}
		additionList, ok := addition.([]any)
		if !ok {
			return nil, overlayErrorf("change_intake.%s overlay must be a list", field)
		}
		merged[field] = appendNew(anyList(base[field]), additionList)
	}
	return merged, nil
}

// mergeCrossStack: route_ids/support are additive-only, and minimum_matches
// may only decrease -- raising it would require more matches before
// cross-stack support engages, which reduces coverage.
func mergeCrossStack(base map[string]any, overlayValue any) (map[string]any, error) {
	if overlayValue == nil {
		return copyObject(base), nil
	}
	overlay, ok := overlayValue.(map[string]any)
	if !ok {
		return nil, overlayErrorf("cross_stack overlay must be an object")
	}
	if err := rejectUnknown("cross_stack", overlay, append(append([]string{}, crossStackAdditiveFields...), "minimum_matches")); err != nil {
		return nil, err
	}
	merged := copyObject(base)
	for _, field := range crossStackAdditiveFields {
		addition, present := overlay[field]
		if !present {
			continue
		}
		additionList, ok := addition.([]any)
		if !ok {
			return nil, overlayErrorf("cross_stack.%s overlay must be a list", field)
		}
		merged[field] = appendNew(anyList(base[field]), additionList)
	}

	if value, present := overlay["minimum_matches"]; present {
		// Python's json decoder yields int for `2` and float for `2.0`, and
		// its isinstance(..., int) check rejects the second. Decoding the
		// overlay with json.Number is what preserves that distinction here --
		// float64 alone cannot tell the two literals apart.
		overlayInt, ok := jsonInteger(value)
		if !ok {
			return nil, overlayErrorf("cross_stack.minimum_matches overlay must be an integer")
		}
		if baseInt, ok := jsonInteger(base["minimum_matches"]); ok && overlayInt > baseInt {
			return nil, overlayErrorf(
				"cross_stack.minimum_matches overlay may only decrease the base value "+
					"(%d); overlay supplied %d, which would require more "+
					"matches to trigger cross-stack support and would reduce coverage",
				baseInt, overlayInt)
		}
		merged["minimum_matches"] = value
	}
	return merged, nil
}

// mergeKnowledgeFocus is an ordinary deep merge with no narrowing rule --
// unlike every other section, it carries no gating, dispatch, or
// review-separation semantics, so overlay simply wins per key.
func mergeKnowledgeFocus(base map[string]any, overlayValue any) (map[string]any, error) {
	if overlayValue == nil {
		return copyObject(base), nil
	}
	overlay, ok := overlayValue.(map[string]any)
	if !ok {
		return nil, overlayErrorf("knowledge_focus overlay must be an object")
	}
	return deepMerge(base, overlay), nil
}

// mergeIgnoredGates may only shrink. Each entry suppresses a gate, so adding
// one is how an overlay would switch off a check the base insists on.
func mergeIgnoredGates(base []any, overlayValue any) ([]any, error) {
	if overlayValue == nil {
		return append([]any{}, base...), nil
	}
	overlay, ok := overlayValue.([]any)
	if !ok {
		return nil, overlayErrorf("ignored_gates overlay must be a list")
	}
	var added []any
	for _, gate := range overlay {
		if !containsJSON(base, gate) {
			added = append(added, gate)
		}
	}
	if len(added) > 0 {
		return nil, overlayErrorf(
			"ignored_gates overlay may not add new suppression(s) %s not present in the "+
				"base ignored_gates; an overlay may only remove already-present entries",
			pythonRepr(added))
	}
	return append([]any{}, overlay...), nil
}

// deepMerge recurses into dict values and replaces everything else wholesale,
// lists included.
func deepMerge(base, overlay map[string]any) map[string]any {
	result := copyObject(base)
	for key, value := range overlay {
		overlayObject, overlayIsObject := value.(map[string]any)
		baseObject, baseIsObject := result[key].(map[string]any)
		if overlayIsObject && baseIsObject {
			result[key] = deepMerge(baseObject, overlayObject)
			continue
		}
		result[key] = value
	}
	return result
}

func rejectUnknown(section string, overlay map[string]any, allowed []string) error {
	permitted := map[string]bool{}
	for _, field := range allowed {
		permitted[field] = true
	}
	var unknown []string
	for key := range overlay {
		if !permitted[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return overlayErrorf("%s overlay has unrecognized field(s): %s", section, pythonList(unknown))
	}
	return nil
}

// appendNew is the additive-only merge: base order first, then whatever the
// overlay adds that was not already there.
func appendNew(base, addition []any) []any {
	result := append([]any{}, base...)
	for _, item := range addition {
		if !containsJSON(base, item) {
			result = append(result, item)
		}
	}
	return result
}

func optionalList(container map[string]any, key, label string) ([]any, error) {
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

func objectList(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}

func objectOf(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return map[string]any{}
}

func anyList(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	return nil
}

func copyObject(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func sortedKeys(source map[string]any) []string {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// jsonEqual compares two decoded JSON values across the two number
// representations in play: the base is decoded to float64, the overlay to
// json.Number, so a bare reflect.DeepEqual would call every restated number
// a change.
func jsonEqual(left, right any) bool {
	leftNumber, leftOK := jsonFloat(left)
	rightNumber, rightOK := jsonFloat(right)
	if leftOK && rightOK {
		return leftNumber == rightNumber
	}
	if leftOK != rightOK {
		return false
	}

	switch typedLeft := left.(type) {
	case map[string]any:
		typedRight, ok := right.(map[string]any)
		if !ok || len(typedLeft) != len(typedRight) {
			return false
		}
		for key, value := range typedLeft {
			other, present := typedRight[key]
			if !present || !jsonEqual(value, other) {
				return false
			}
		}
		return true
	case []any:
		typedRight, ok := right.([]any)
		if !ok || len(typedLeft) != len(typedRight) {
			return false
		}
		for index := range typedLeft {
			if !jsonEqual(typedLeft[index], typedRight[index]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(left, right)
	}
}

func containsJSON(list []any, needle any) bool {
	for _, item := range list {
		if jsonEqual(item, needle) {
			return true
		}
	}
	return false
}

func dedupeJSON(list []any) []any {
	result := make([]any, 0, len(list))
	for _, item := range list {
		if !containsJSON(result, item) {
			result = append(result, item)
		}
	}
	return result
}

func jsonFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

// jsonInteger reports whether a value is an integer *literal*, matching
// Python's isinstance(value, int) on a json-decoded document -- so `2`
// qualifies and `2.0` does not.
func jsonInteger(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case float64:
		// The base config is decoded to float64, where the distinction is
		// already gone. Treat an integral value as the integer it was
		// written as, which is what routing.json always contains.
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
		return 0, false
	default:
		return 0, false
	}
}

// ResolveEffectiveRouting is the whole mechanism: with no project-local
// overlay the base configuration is returned exactly as parsed; with one, the
// merged and validated effective configuration is returned along with the
// overlay's path.
//
// Validation runs on the merged result rather than on the way in, because the
// merge rules and the structural invariants answer different questions -- see
// ValidateRoutingConfig. The no-overlay path deliberately does not validate,
// matching Python: the base file is this repository's own, already guarded,
// and revalidating it here would make a selection run fail on a problem no
// overlay introduced.
func ResolveEffectiveRouting(base map[string]any, overlayPath string) (map[string]any, error) {
	if overlayPath == "" {
		return base, nil
	}
	overlay, err := LoadOverlay(overlayPath)
	if err != nil {
		return nil, err
	}
	effective, err := MergeRouting(base, overlay)
	if err != nil {
		return nil, err
	}
	if err := ValidateRoutingConfig(effective); err != nil {
		return nil, overlayErrorf("effective configuration failed validation: %s", err)
	}
	return effective, nil
}
