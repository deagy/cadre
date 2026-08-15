package selector

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Python's repr(), for the values that reach an overlay's error messages.
//
// These messages quote the offending value back at the author -- "may not
// change field 'reviewers' (base value: ['security-reviewer'], overlay value:
// [])" -- and that quoting is most of what makes a refusal actionable. Go's
// %v renders the same value as `[security-reviewer]`, losing the quotes that
// distinguish a one-element list from a string, and `map[]` for an empty
// object.
//
// So this renders decoded JSON the way Python would, which keeps the two
// implementations' refusals comparable rather than merely equivalent in
// spirit.

func pythonRepr(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case string:
		return pythonStringRepr(typed)
	case json.Number:
		return typed.String()
	case float64:
		// Python renders an integral float as 1.0 but an int as 1. A config
		// decoded to float64 has lost which one it was; routing.json's
		// numbers are all integers, so render integral values as integers.
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, pythonRepr(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		// Python preserves insertion order; a decoded map has none to
		// preserve, so sort for a stable message rather than a per-run one.
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, pythonStringRepr(key)+": "+pythonRepr(typed[key]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", value)
	}
}

// pythonStringRepr prefers single quotes, as Python's repr does, switching to
// double quotes only for a string that itself contains a single quote.
func pythonStringRepr(value string) string {
	if strings.Contains(value, "'") && !strings.Contains(value, `"`) {
		return `"` + value + `"`
	}
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, "'", `\'`)
	return "'" + escaped + "'"
}

// pythonList renders a sorted []string the way an f-string interpolating
// sorted(...) does.
func pythonList(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, pythonStringRepr(value))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
