package selector

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDumpGatesForParityProbe enumerates gate sequencing, gate agents,
// quality gates and human gates for comparison against Python. Not an
// assertion; see TestDumpMatchesForParityProbe.
func TestDumpGatesForParityProbe(t *testing.T) {
	destination := os.Getenv("CADRE_SELECTOR_GATES_PROBE_OUT")
	if destination == "" {
		t.Skip("set CADRE_SELECTOR_GATES_PROBE_OUT to dump gate decisions")
	}
	contract, err := FetchLifecycleContract(context.Background())
	if err != nil {
		t.Fatalf("FetchLifecycleContract: %v", err)
	}
	if contract == nil {
		t.Skip("no lifecycle contract available")
		return
	}
	gates := contract.Gates
	order := GateOrder(gates)
	results := map[string]any{
		"_version": contract.Version,
		"_order":   order,
	}

	// Every single gate and every ordered pair as `configured`, with and
	// without an ignored gate -- the sequence rule is "imply everything up to
	// the furthest configured", so pairs are what exercise it.
	ignoredSets := [][]string{nil, {order[0]}, {order[len(order)-1]}}
	for _, ignored := range ignoredSets {
		key := "ignored=" + join(ignored)
		for i := range order {
			configured := []string{order[i]}
			record(t, results, key+"|"+order[i], configured, ignored, gates)
			for j := range order {
				pair := []string{order[i], order[j]}
				record(t, results, key+"|"+order[i]+"+"+order[j], pair, ignored, gates)
			}
		}
		// And the same with no contract at all: the standalone branch.
		effective, ignoredOut, err := GateSequence([]string{"G3", "G1"}, ignored, nil)
		if err != nil {
			t.Fatal(err)
		}
		results["nocontract|"+key] = map[string]any{"effective": effective, "ignored": ignoredOut}
	}

	// Quality and human gates over the corpus, where real route/risk rules
	// supply the quality_gates and human_gate declarations.
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	config := readJSONMap(t, filepath.Join(repoRoot, "roster", "orchestration", "routing.json"))
	corpus := readJSONMap(t, filepath.Join(repoRoot, "roster", "orchestration", "test", "select_corpus.json"))
	cases, _ := corpus["cases"].([]any)
	for _, rawCase := range cases {
		testCase, _ := rawCase.(map[string]any)
		caseID, _ := testCase["id"].(string)
		task, _ := testCase["task"].(string)
		files, _ := testCase["files"].(string)
		var changed []string
		if files != "" {
			changed = strings.Split(files, ",")
		}
		routes := MatchRoutes(config, task, changed)
		risks := ClassifyRisks(config, task, changed)
		quality, err := BuildQualityGates(routes, risks, gates)
		if err != nil {
			t.Fatalf("BuildQualityGates(%s): %v", caseID, err)
		}
		results["_gates|"+caseID] = map[string]any{
			"quality": quality,
			"human":   BuildHumanGates(risks),
		}
	}

	encoded, _ := json.MarshalIndent(results, "", " ")
	if err := os.WriteFile(destination, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d gate decisions", len(results))
}

func record(t *testing.T, out map[string]any, key string, configured, ignored []string, gates []LifecycleGate) {
	t.Helper()
	effective, ignoredOut, err := GateSequence(configured, ignored, gates)
	if err != nil {
		t.Fatalf("GateSequence(%v): %v", configured, err)
	}
	agents, err := GateAgents(configured, ignored, gates, []string{"code-reviewer"})
	if err != nil {
		t.Fatalf("GateAgents(%v): %v", configured, err)
	}
	out[key] = map[string]any{"effective": effective, "ignored": ignoredOut, "agents": agents}
}
