package selector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The golden corpus, run against this selector.
//
// 176 fixtures pinning what `cadre select` resolves for a given task and file
// set: which agent is primary, who reviews, who supports, which routes matched
// and which workflow was chosen. A routing change that moves any of them shows
// up here as a data diff rather than as a surprise in somebody's dispatch.
//
// This existed only in Python, calling the *Python* selector in-process. That
// selector is being retired, and the corpus would have gone with it -- which
// would have left `BuildDispatchPlan` and `RenderPlanJSON` at zero coverage
// across the entire tree. They were at zero already: the plan builder at the
// centre of this package had no Go test, because the only thing exercising it
// end to end was a Python harness.
//
// Two properties of the harness are load-bearing and carried over:
//
//   - **Standalone mode, forced.** The lifecycle contract is not consulted, so
//     the corpus resolves the same way on a machine that happens to have a
//     kernel installed as on one that does not. Without this, `_gate_agents`
//     pulls extra quality-gate agents into `support` and the same fixture
//     passes or fails depending on the environment.
//   - **needs-triage is its own failure.** A fixture that stops matching
//     anything would otherwise pass against a copy-pasted all-empty expected
//     block. Only a fixture that opts in may resolve that way.

type goldenCase struct {
	ID                string   `json:"id"`
	Description       string   `json:"description"`
	Task              string   `json:"task"`
	ChangedFiles      []string `json:"changed_files"`
	Classification    string   `json:"classification"`
	TaskID            string   `json:"task_id"`
	ExpectNeedsTriage bool     `json:"expect_needs_triage"`
	TeamIDs           []string `json:"team_ids"`
	Expected          struct {
		Primary       []string `json:"primary"`
		Reviewers     []string `json:"reviewers"`
		Support       []string `json:"support"`
		MatchedRoutes []string `json:"matched_routes"`
		Workflow      string   `json:"workflow"`
	} `json:"expected"`
}

// selectorRepoRoot is this repository, from the package directory.
func selectorRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func loadGoldenCorpus(t *testing.T) []goldenCase {
	t.Helper()
	path := filepath.Join(selectorRepoRoot(t), "roster", "orchestration", "test",
		"fixtures", "selection_golden_corpus.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the corpus: %v", err)
	}
	var corpus struct {
		Cases []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("parsing the corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("the corpus is empty; this test would prove nothing")
	}
	return corpus.Cases
}

// loadCatalogIDs is the agent inventory the plan builder validates against.
//
// Without it every fixture fails with "routing selected an unknown agent",
// which is the check doing its job: a plan naming an agent the catalog does
// not carry is a plan nothing can dispatch.
func loadCatalogIDs(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(selectorRepoRoot(t), "roster", "catalog.yaml"))
	if err != nil {
		t.Fatalf("reading the catalog: %v", err)
	}
	ids, err := ParseCatalogIDs(string(data))
	if err != nil {
		t.Fatalf("parsing the catalog: %v", err)
	}
	return ids
}

func loadRoutingConfig(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join(selectorRepoRoot(t), "roster", "orchestration", "routing.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading routing.json: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parsing routing.json: %v", err)
	}
	return config
}

// planAgents reads the plan's agent groups.
//
// BuildDispatchPlan returns typed values, not the decoded JSON a caller sees
// -- `agents` is an AgentGroups and `matched_routes` a slice of maps. Reading
// them as `[]any` silently yields nothing, which is how an earlier version of
// this harness reported all 176 fixtures as resolving to empty.
func planAgents(t *testing.T, plan map[string]any) AgentGroups {
	t.Helper()
	groups, ok := plan["agents"].(AgentGroups)
	if !ok {
		t.Fatalf("the plan's agents field is %T, not AgentGroups", plan["agents"])
	}
	return groups
}

// planRouteIDs reads the ids of the routes that matched.
func planRouteIDs(plan map[string]any) []string {
	routes, ok := plan["matched_routes"].([]map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(routes))
	for _, route := range routes {
		if id, ok := route["id"].(string); ok {
			out = append(out, id)
		}
	}
	return out
}

// difference reports what one list has and the other does not, both ways.
func difference(want, got []string) string {
	present := map[string]bool{}
	for _, value := range got {
		present[value] = true
	}
	expected := map[string]bool{}
	for _, value := range want {
		expected[value] = true
	}
	var missing, extra []string
	for _, value := range want {
		if !present[value] {
			missing = append(missing, value)
		}
	}
	for _, value := range got {
		if !expected[value] {
			extra = append(extra, value)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return ""
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return fmt.Sprintf("missing %v, unexpected %v", missing, extra)
}

func TestTheGoldenCorpusResolvesAsRecorded(t *testing.T) {
	corpus := loadGoldenCorpus(t)
	config := loadRoutingConfig(t)
	catalog := loadCatalogIDs(t)
	rosterRoot := filepath.Join(selectorRepoRoot(t), "roster")

	matched := 0
	for _, probe := range corpus {
		t.Run(probe.ID, func(t *testing.T) {
			plan, err := BuildDispatchPlan(config, PlanInput{
				Task:           probe.Task,
				TaskID:         probe.TaskID,
				ChangedFiles:   probe.ChangedFiles,
				Classification: probe.Classification,
			}, PlanOptions{
				Catalog: catalog,
				// No Gates and no ContractVer: standalone mode, so a machine
				// with a kernel installed resolves the corpus identically to
				// one without.
				RosterRoot: rosterRoot,
			})
			if err != nil {
				t.Fatalf("%s: %v", probe.Description, err)
			}

			status, _ := plan["status"].(string)
			if probe.ExpectNeedsTriage {
				if status != "needs-triage" {
					t.Errorf("expected needs-triage, resolved %q", status)
				}
				return
			}
			// Reported on its own rather than folded into the field
			// comparisons: a fixture that stopped matching anything would
			// otherwise agree with an all-empty expected block.
			if status == "needs-triage" {
				t.Fatalf("resolved to needs-triage, which this fixture does not "+
					"opt into -- a routing edit has made %q match nothing", probe.Task)
			}

			agents := planAgents(t, plan)
			for _, field := range []struct {
				name string
				want []string
				got  []string
			}{
				{"primary", probe.Expected.Primary, agents.Primary},
				{"reviewers", probe.Expected.Reviewers, agents.Reviewers},
				{"support", probe.Expected.Support, agents.Support},
				{"matched_routes", probe.Expected.MatchedRoutes, planRouteIDs(plan)},
			} {
				if delta := difference(field.want, field.got); delta != "" {
					t.Errorf("%s: %s\n  expected %v\n  got      %v",
						field.name, delta, field.want, field.got)
				}
			}
			if probe.Expected.Workflow != "" {
				if got, _ := plan["workflow"].(string); got != probe.Expected.Workflow {
					t.Errorf("workflow: expected %q, got %q", probe.Expected.Workflow, got)
				}
			}
			if len(probe.TeamIDs) > 0 {
				teams := stringsOfTeams(plan)
				if delta := difference(probe.TeamIDs, teams); delta != "" {
					t.Errorf("team_ids: %s\n  expected %v\n  got      %v",
						delta, probe.TeamIDs, teams)
				}
			}
			matched++
		})
	}
	if matched == 0 {
		t.Error("no fixture resolved at all; the corpus proved nothing")
	}
}

// stringsOfTeams reads the plan's teams array, which carries objects rather
// than plain strings.
func stringsOfTeams(plan map[string]any) []string {
	teams, ok := plan["teams"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(teams))
	for _, raw := range teams {
		team, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := team["team_id"].(string); ok {
			out = append(out, id)
		}
	}
	return out
}

func TestTheCorpusCoversMoreThanOneOutcome(t *testing.T) {
	// A corpus where every fixture expected the same thing would agree with a
	// selector that ignored its inputs. These count what the corpus actually
	// exercises, so a future edit cannot quietly narrow it to one shape.
	corpus := loadGoldenCorpus(t)

	routes := map[string]bool{}
	primaries := map[string]bool{}
	workflows := map[string]bool{}
	triage := 0
	for _, probe := range corpus {
		if probe.ExpectNeedsTriage {
			triage++
			continue
		}
		for _, route := range probe.Expected.MatchedRoutes {
			routes[route] = true
		}
		for _, agent := range probe.Expected.Primary {
			primaries[agent] = true
		}
		if probe.Expected.Workflow != "" {
			workflows[probe.Expected.Workflow] = true
		}
	}
	if len(routes) < 10 {
		t.Errorf("the corpus exercises only %d routes", len(routes))
	}
	if len(primaries) < 10 {
		t.Errorf("the corpus exercises only %d primary agents", len(primaries))
	}
	if len(workflows) < 2 {
		t.Errorf("the corpus exercises only %d workflows", len(workflows))
	}
	// Both outcomes: a corpus with no needs-triage case never checks that the
	// selector refuses to guess.
	if triage == 0 {
		t.Error("no fixture expects needs-triage, so nothing checks that the " +
			"selector declines to guess rather than routing arbitrarily")
	}
	t.Logf("%d fixtures: %d routes, %d primaries, %d workflows, %d needs-triage",
		len(corpus), len(routes), len(primaries), len(workflows), triage)
}

func TestEveryCorpusFixtureNamesWhatItPins(t *testing.T) {
	// The corpus is reviewed as a data file. A fixture with no description is
	// one a reviewer cannot judge the intent of, and an id collision makes a
	// failure report ambiguous about which case moved.
	seen := map[string]bool{}
	for _, probe := range loadGoldenCorpus(t) {
		if probe.ID == "" {
			t.Errorf("a fixture has no id: %q", probe.Task)
			continue
		}
		if seen[probe.ID] {
			t.Errorf("%s appears twice", probe.ID)
		}
		seen[probe.ID] = true
		if strings.TrimSpace(probe.Description) == "" {
			t.Errorf("%s has no description", probe.ID)
		}
		if strings.TrimSpace(probe.Task) == "" {
			t.Errorf("%s has no task text", probe.ID)
		}
	}
}

// Not covered here, deliberately, and it is the remaining blocker on deleting
// the Python selector: `RenderPlanJSON`.
//
// Its gate today is test_select_differential.py, which runs both selectors on
// one machine and compares bytes. The obvious replacement is
// `select_golden.json`, which stores 25 plans in their fingerprint basis --
// but those were recorded with a lifecycle kernel present
// (`lifecycle_tracking: {"status": "integrated"}`), where this corpus forces
// standalone mode so it resolves the same everywhere. Rebuilding them means
// supplying the same lifecycle gates, which is real work rather than a
// missing parameter. Until that lands, the Python differential is still the
// only thing checking that the rendered bytes have not moved.
