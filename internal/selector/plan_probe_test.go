package selector

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDumpPlansForParityProbe builds a complete plan for every corpus case
// and writes its canonical form, for comparison against Python's. Not an
// assertion; the durable gate is test_select_differential.py.
func TestDumpPlansForParityProbe(t *testing.T) {
	destination := os.Getenv("CADRE_SELECTOR_PLAN_PROBE_OUT")
	if destination == "" {
		t.Skip("set CADRE_SELECTOR_PLAN_PROBE_OUT to dump full plans")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	config := readJSONMap(t, filepath.Join(repoRoot, "roster", "orchestration", "routing.json"))
	corpus := readJSONMap(t, filepath.Join(repoRoot, "roster", "orchestration", "test", "select_corpus.json"))
	catalogRaw, err := os.ReadFile(filepath.Join(repoRoot, "roster", "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := ParseCatalogIDs(string(catalogRaw))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := FetchLifecycleContract(context.Background())
	if err != nil {
		t.Fatalf("FetchLifecycleContract: %v", err)
	}
	var gates []LifecycleGate
	if contract != nil {
		gates = contract.Gates
	}

	results := map[string]string{}
	cases, _ := corpus["cases"].([]any)
	for _, rawCase := range cases {
		testCase, _ := rawCase.(map[string]any)
		id, _ := testCase["id"].(string)
		task, _ := testCase["task"].(string)
		files, _ := testCase["files"].(string)
		classification, _ := testCase["classification"].(string)
		var changed []string
		if files != "" {
			changed = strings.Split(files, ",")
		}

		plan, err := BuildDispatchPlan(config, PlanInput{
			Task: task, TaskID: strings.ToUpper(id), RepositoryRoot: repoRoot,
			ChangedFileSource: "explicit", ChangedFiles: changed,
			Classification: classification,
			Sources:        []string{"deagy/cadre", "proposed-knowledge"}, Top: 5,
		}, PlanOptions{
			Catalog: catalog, Gates: gates,
			RosterRoot:   filepath.Join(repoRoot, "roster"),
			KnowledgeCLI: os.Getenv("CADRE_SELECTOR_PROBE_CLI"),
		})
		if err != nil {
			t.Fatalf("BuildDispatchPlan(%s): %v", id, err)
		}
		payload := map[string]any{}
		excluded := setOf(FingerprintExcludedKeys)
		for key, value := range plan {
			if !excluded[key] {
				payload[key] = value
			}
		}
		canonical, err := CanonicalJSON(payload)
		if err != nil {
			t.Fatal(err)
		}
		results[id] = string(canonical)
		results["fp|"+id], _ = plan["dispatch_fingerprint"].(string)
	}

	encoded, _ := json.MarshalIndent(results, "", " ")
	if err := os.WriteFile(destination, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d plans", len(results))
}
