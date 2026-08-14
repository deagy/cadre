// field_decisions.go ports init_project.py's A-006 rev 2 field-decision
// tracking and the --print-answers redaction logic (finding A / round-4
// fail-safe union).
package initproject

import (
	"sort"

	"github.com/deagy/cadre/cli/internal/config"
)

func leafPaths(node map[string]any, prefix string) []string {
	var paths []string
	keys := sortedKeys(node)
	for _, key := range keys {
		value := node[key]
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if m, ok := value.(map[string]any); ok {
			paths = append(paths, leafPaths(m, path)...)
		} else {
			paths = append(paths, path)
		}
	}
	return paths
}

// leafValues is leafPaths, but keeping each leaf's value -- used to show a
// --set override the shipped default it is replacing (its source value).
func leafValues(node map[string]any, prefix string) map[string]any {
	values := map[string]any{}
	for key, value := range node {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if m, ok := value.(map[string]any); ok {
			for k, v := range leafValues(m, path) {
				values[k] = v
			}
		} else {
			values[path] = value
		}
	}
	return values
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func setByPath(node map[string]any, path string, value any) {
	parts := splitDots(path)
	current := node
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func splitDots(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// ParseFieldDecisions validates a raw field_decisions map from an answer
// set into FieldDecision values.
func ParseFieldDecisions(raw map[string]any) (map[string]FieldDecision, error) {
	decisions := map[string]FieldDecision{}
	for path, entryRaw := range raw {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			return nil, initErrorf("field_decisions[%q] must be a mapping", path)
		}
		status, _ := entry["status"].(string)
		category, _ := entry["category"].(string)
		if !stringInSlice(status, FieldDecisionStatuses) {
			return nil, initErrorf("field_decisions[%q].status must be one of %v, got %q", path, FieldDecisionStatuses, status)
		}
		if !stringInSlice(category, FieldDecisionCategories) {
			return nil, initErrorf("field_decisions[%q].category must be one of %v, got %q", path, FieldDecisionCategories, category)
		}
		decisions[path] = FieldDecision{
			Path: path, Status: status, Category: category,
			SourceValue: entry["source_value"], NewValue: entry["new_value"],
		}
	}
	return decisions, nil
}

func stringInSlice(s string, list []string) bool {
	for _, item := range list {
		if s == item {
			return true
		}
	}
	return false
}

// TouchedPath is a (path, expectedCategory) pair drawn from the fragments
// an answer set actually supplies values for.
type TouchedPath struct {
	Path     string
	Category string
}

// RequireFieldDecisionsCover enforces A-006 rev 2: no field may reach flow
// output without a recorded kept/overridden/deferred decision.
func RequireFieldDecisionsCover(touched []TouchedPath, decisions map[string]FieldDecision) error {
	var missing []string
	for _, t := range touched {
		if _, ok := decisions[t.Path]; !ok {
			missing = append(missing, t.Path)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return initErrorf("field_decisions is missing an entry for: %s (A-006 rev 2)", joinStrings(missing, ", "))
	}
	var mismatched []string
	for _, t := range touched {
		if d, ok := decisions[t.Path]; ok && d.Category != t.Category {
			mismatched = append(mismatched, t.Path)
		}
	}
	if len(mismatched) > 0 {
		sort.Strings(mismatched)
		return initErrorf("field_decisions category mismatch (stack vs governance, B-005) for: %s", joinStrings(mismatched, ", "))
	}
	return nil
}

func joinStrings(items []string, sep string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

// ---------------------------------------------------------------------
// --print-answers redaction (finding A / round-4 fail-safe union).
// ---------------------------------------------------------------------

// RedactAnswersForEcho builds a copy of answers safe to echo via
// --print-answers. Must only ever be called AFTER PlanWrites has run, so
// the accepted/rejected status this redaction reports is the real,
// post-validation outcome, not a guess made from the raw, unvalidated
// answer set. See package doc for the full B-005/B-006 fail-safe-union
// reasoning.
func RedactAnswersForEcho(answers map[string]any, result *InitResult) map[string]any {
	redacted := map[string]any{}
	for k, v := range answers {
		redacted[k] = v
	}

	autonomyFragment, _ := answers["rg_b_autonomy"].(map[string]any)
	rejectedPaths := map[string]bool{}
	for _, r := range result.RejectedAutonomy {
		rejectedPaths[r.FieldPath] = true
	}
	fragmentFailed := len(result.RejectedAutonomy) > 0

	statusLabel := func(path string, value any) string {
		valueHash := sha256Hex(anyToString(value))
		if rejectedPaths[path] || fragmentFailed {
			return "rejected (hash " + valueHash + ")"
		}
		return "accepted (hash " + valueHash + ")"
	}

	if len(autonomyFragment) > 0 {
		redactedAutonomy := map[string]any{}
		for _, leaf := range config.AutonomyLeafPaths(autonomyFragment) {
			redactedAutonomy[leaf.Path] = statusLabel(leaf.Path, leaf.Value)
		}
		redacted["rg_b_autonomy"] = redactedAutonomy
	}

	if fieldDecisionsRaw, ok := answers["field_decisions"].(map[string]any); ok && len(fieldDecisionsRaw) > 0 {
		redactedFieldDecisions := map[string]any{}
		for path, entryRaw := range fieldDecisionsRaw {
			entry, ok := entryRaw.(map[string]any)
			if !ok {
				redactedFieldDecisions[path] = entryRaw
				continue
			}
			declaredGovernance := entry["category"] == "governance"
			groundTruthGovernance := result.GovernanceTouchedPaths[path]
			if !declaredGovernance && !groundTruthGovernance {
				redactedFieldDecisions[path] = entry
				continue
			}
			entryCopy := map[string]any{}
			for k, v := range entry {
				entryCopy[k] = v
			}
			for _, valueKey := range []string{"new_value", "source_value"} {
				if v, present := entryCopy[valueKey]; present && v != nil {
					entryCopy[valueKey] = statusLabel(path, v)
				}
			}
			redactedFieldDecisions[path] = entryCopy
		}
		redacted["field_decisions"] = redactedFieldDecisions
	}

	if guardrailBullets, ok := answers["rg_b_guardrails_addendum"].([]any); ok && len(guardrailBullets) > 0 {
		rejectedTexts := map[string]bool{}
		for _, r := range result.RejectedGuardrails {
			rejectedTexts[r.Bullet] = true
		}
		redactedBullets := make([]any, 0, len(guardrailBullets))
		for _, b := range guardrailBullets {
			bullet, _ := b.(string)
			if rejectedTexts[bullet] {
				redactedBullets = append(redactedBullets, "<rejected, hash "+sha256Hex(bullet)+">")
			} else {
				redactedBullets = append(redactedBullets, b)
			}
		}
		redacted["rg_b_guardrails_addendum"] = redactedBullets
	}

	return redacted
}
