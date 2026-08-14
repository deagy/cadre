package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDumpMatchesForParityProbe is not an assertion. It exists so the Go
// matcher can be diffed against the Python one over the real ruleset, which
// is the only way to know this port reproduces routing.py rather than
// something that merely looks similar.
//
// Skipped unless CADRE_SELECTOR_PROBE_OUT names a file to write.
//
//	CADRE_SELECTOR_PROBE_OUT=/tmp/go.json go test ./internal/selector/ -run Probe
//
// The Python side of the comparison lives in the same scratch workflow, not
// in the repository: this is a development aid for the port, and the durable
// gate is roster/orchestration/test/test_select_differential.py.
func TestDumpMatchesForParityProbe(t *testing.T) {
	destination := os.Getenv("CADRE_SELECTOR_PROBE_OUT")
	if destination == "" {
		t.Skip("set CADRE_SELECTOR_PROBE_OUT to dump matcher output for the parity probe")
	}

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	config := readJSONMap(t, filepath.Join(repoRoot, "roster", "orchestration", "routing.json"))
	corpus := readJSONMap(t, filepath.Join(repoRoot, "roster", "orchestration", "test", "select_corpus.json"))

	cases, _ := corpus["cases"].([]any)
	results := map[string]any{}

	for _, kind := range []string{"routes", "risk_rules", "context_packs"} {
		rules, _ := config[kind].([]any)
		for _, raw := range rules {
			rule, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id, _ := rule["id"].(string)
			for _, rawCase := range cases {
				testCase, ok := rawCase.(map[string]any)
				if !ok {
					continue
				}
				caseID, _ := testCase["id"].(string)
				task, _ := testCase["task"].(string)
				files, _ := testCase["files"].(string)
				var changed []string
				if files != "" {
					changed = strings.Split(files, ",")
				}
				match := MatchRule(rule, task, changed)
				if !match.Matched {
					continue
				}
				results[kind+"|"+id+"|"+caseID] = map[string]any{
					"keywords":       match.Keywords,
					"keyword_groups": match.KeywordGroups,
					"paths":          match.Paths,
				}
			}
		}
	}

	encoded, err := json.MarshalIndent(results, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d matches to %s", len(results), destination)
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
