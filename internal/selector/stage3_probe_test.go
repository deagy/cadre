package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDumpStage3ForParityProbe covers teams, change intake, knowledge context
// and context packs. Not an assertion; see TestDumpMatchesForParityProbe.
func TestDumpStage3ForParityProbe(t *testing.T) {
	destination := os.Getenv("CADRE_SELECTOR_STAGE3_PROBE_OUT")
	if destination == "" {
		t.Skip("set CADRE_SELECTOR_STAGE3_PROBE_OUT to dump stage-3 output")
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
	focus, _ := config["knowledge_focus"].(map[string]any)
	rosterRoot := filepath.Join(repoRoot, "roster")
	knowledgeCLI := os.Getenv("CADRE_SELECTOR_PROBE_CLI")

	results := map[string]any{}
	cases, _ := corpus["cases"].([]any)
	for _, rawCase := range cases {
		testCase, _ := rawCase.(map[string]any)
		caseID, _ := testCase["id"].(string)
		task, _ := testCase["task"].(string)
		files, _ := testCase["files"].(string)
		classification, _ := testCase["classification"].(string)
		var changed []string
		if files != "" {
			changed = strings.Split(files, ",")
		}

		routes := MatchRoutes(config, task, changed)
		risks := ClassifyRisks(config, task, changed)
		packs := MatchContextPacks(config, task, changed)

		var primary, reviewers, support []string
		for _, match := range append(append([]Match{}, routes...), risks...) {
			primary = append(primary, stringSlice(match.Rule["primary"])...)
			reviewers = append(reviewers, stringSlice(match.Rule["reviewers"])...)
			support = append(support, stringSlice(match.Rule["support"])...)
		}
		support = append(support, ApplyCrossStack(config, routes)...)
		groups := AgentGroups{
			Primary:   Ordered(primary, catalog),
			Reviewers: Ordered(reviewers, catalog),
			Support:   Ordered(support, catalog),
		}
		groups.Reviewers = without(groups.Reviewers, groups.Primary)
		groups.Support = without(groups.Support, groups.Primary, groups.Reviewers)
		selected := Ordered(append(append(append([]string{}, groups.Primary...), groups.Reviewers...), groups.Support...), catalog)

		knowledge, err := BuildKnowledgeContext(focus, selected, KnowledgeInput{
			Task: task, TaskID: strings.ToUpper(caseID), Classification: classification,
			Sources: []string{"deagy/cadre", "proposed-knowledge"}, Top: knowledgeTop(5), KnowledgeCLI: knowledgeCLI,
		})
		if err != nil {
			t.Fatalf("BuildKnowledgeContext(%s): %v", caseID, err)
		}
		built, err := BuildContextPacks(packs, classification, rosterRoot)
		if err != nil {
			t.Fatalf("BuildContextPacks(%s): %v", caseID, err)
		}

		results["_stage3|"+caseID] = map[string]any{
			"teams":         BuildTeams(config, routes, selected, task),
			"change_intake": MatchesChangeIntake(config, task),
			"knowledge":     knowledge,
			"packs":         built,
		}
	}

	encoded, _ := json.MarshalIndent(results, "", " ")
	if err := os.WriteFile(destination, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d stage-3 entries", len(results))
}
