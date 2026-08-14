package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestDumpWorkflowsForParityProbe enumerates the workflow-selection decision
// space far beyond what the 25-case corpus reaches, so the precedence chain
// itself is compared rather than the handful of shapes real tasks happen to
// produce. Not an assertion; see TestDumpMatchesForParityProbe.
func TestDumpWorkflowsForParityProbe(t *testing.T) {
	destination := os.Getenv("CADRE_SELECTOR_WORKFLOW_PROBE_OUT")
	if destination == "" {
		t.Skip("set CADRE_SELECTOR_WORKFLOW_PROBE_OUT to dump workflow decisions")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	config := readJSONMap(t, filepath.Join(repoRoot, "roster", "orchestration", "routing.json"))
	rawRoutes, _ := config["routes"].([]any)

	byID := map[string]map[string]any{}
	var routeIDs []string
	for _, raw := range rawRoutes {
		rule, _ := raw.(map[string]any)
		id, _ := rule["id"].(string)
		byID[id] = rule
		routeIDs = append(routeIDs, id)
	}
	sort.Strings(routeIDs)

	riskSets := [][]string{nil, {"production"}, {"architecture-change"}, {"production", "architecture-change"}}
	results := map[string]string{}

	record := func(key string, ids []string, risks []string, debugKeyword bool) {
		matches := make([]Match, 0, len(ids))
		for _, id := range ids {
			reasons := RuleMatch{}
			if id == "debugging" && debugKeyword {
				reasons.Keywords = []string{"debug"}
			}
			matches = append(matches, Match{ID: id, Reasons: reasons, Rule: byID[id]})
		}
		results[key] = SelectWorkflow(matches, risks, true)
	}

	for _, risks := range riskSets {
		riskKey := "risks=" + join(risks)
		for _, id := range routeIDs {
			for _, debugKeyword := range []bool{false, true} {
				record(riskKey+"|kw="+boolText(debugKeyword)+"|"+id, []string{id}, risks, debugKeyword)
			}
		}
		for i := 0; i < len(routeIDs); i++ {
			for j := i + 1; j < len(routeIDs); j++ {
				pair := []string{routeIDs[i], routeIDs[j]}
				record(riskKey+"|kw=f|"+pair[0]+"+"+pair[1], pair, risks, false)
			}
		}
	}
	// needs-triage is decided before anything else.
	results["no-agents"] = SelectWorkflow([]Match{{ID: "backend", Rule: byID["backend"]}}, nil, false)

	encoded, err := json.MarshalIndent(results, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d workflow decisions", len(results))
}

func join(values []string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

func boolText(b bool) string {
	if b {
		return "t"
	}
	return "f"
}
