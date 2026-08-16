package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The one workflow test that is not self-referential.
//
// selection_golden_corpus.json pins the selector's current output as a
// regression baseline -- but it is derived FROM the code, so a bug in the
// workflow decision and a fixture copied from that same buggy output will
// always agree. A corpus can tell you the behaviour changed. It cannot tell
// you the behaviour is wrong.
//
// workflow_fitness_table.json is the other kind: a small hand-authored table
// whose expected_workflow values were each reasoned independently from
// roster/workflows/*.md and routing.json, not from the selector's behaviour.
// Disagreement between the two is the signal a self-referential pinning test
// can never produce.
//
// The table carries a `known_mismatch` flag for a case that disagrees with the
// code on purpose -- the mechanism is dormant, not gone. Two cases once used
// it: maintenance mislabelled as debugging, and an unreachable rollback
// workflow. Both were fixed in the code rather than bent in the table, which
// is the direction that flag exists to make available.
//
// Ported from roster/orchestration/test/test_workflow_fitness_table.py.

type fitnessCase struct {
	ID               string   `json:"id"`
	Description      string   `json:"description"`
	Task             string   `json:"task"`
	ChangedFiles     []string `json:"changed_files"`
	Classification   string   `json:"classification"`
	TaskID           string   `json:"task_id"`
	ExpectedWorkflow string   `json:"expected_workflow"`
	KnownMismatch    bool     `json:"known_mismatch"`
}

func loadFitnessTable(t *testing.T) []fitnessCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(selectorRepoRoot(t), "roster", "orchestration",
		"test", "fixtures", "workflow_fitness_table.json"))
	if err != nil {
		t.Fatalf("reading the fitness table: %v", err)
	}
	var document struct {
		Cases []fitnessCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parsing the fitness table: %v", err)
	}
	if len(document.Cases) == 0 {
		t.Fatal("the fitness table is empty")
	}
	return document.Cases
}

// planWorkflowEnum reads the values a plan's workflow field may take, from the
// schema rather than from a list here -- a second copy would drift, and the
// point of the coverage check below is that the table tracks the schema.
func planWorkflowEnum(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(selectorRepoRoot(t), "roster", "orchestration",
		"selection.schema.json"))
	if err != nil {
		t.Fatalf("reading the selection schema: %v", err)
	}
	var schema struct {
		Properties struct {
			Workflow struct {
				Enum []string `json:"enum"`
			} `json:"workflow"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parsing the selection schema: %v", err)
	}
	if len(schema.Properties.Workflow.Enum) == 0 {
		t.Fatal("the schema declares no workflow enum")
	}
	return schema.Properties.Workflow.Enum
}

func TestTheFitnessTableIsWellFormed(t *testing.T) {
	cases := loadFitnessTable(t)
	seen := map[string]bool{}
	for _, testCase := range cases {
		if testCase.ID == "" {
			t.Errorf("a case has no id: %+v", testCase)
			continue
		}
		if seen[testCase.ID] {
			t.Errorf("duplicate case id %q -- a failure would name the wrong case", testCase.ID)
		}
		seen[testCase.ID] = true
		if testCase.ExpectedWorkflow == "" {
			t.Errorf("%s asserts no expected_workflow", testCase.ID)
		}
		if strings.TrimSpace(testCase.Description) == "" {
			t.Errorf("%s has no description; an independently reasoned expectation "+
				"is only reviewable if it says what it is reasoning from", testCase.ID)
		}
	}
}

func TestEveryExpectedWorkflowIsAValueAPlanMayCarry(t *testing.T) {
	// A table asserting a workflow the schema does not allow is asserting
	// something no plan can satisfy -- it would fail forever, or worse, pass
	// because the comparison never ran.
	permitted := setOf(planWorkflowEnum(t))
	for _, testCase := range loadFitnessTable(t) {
		if !permitted[testCase.ExpectedWorkflow] {
			t.Errorf("%s expects workflow %q, which selection.schema.json does not permit",
				testCase.ID, testCase.ExpectedWorkflow)
		}
	}
}

func TestTheTableCoversEveryWorkflowTheSchemaPermits(t *testing.T) {
	// The completeness half. A workflow value with no case is a branch of the
	// selector that no independently reasoned expectation covers -- only the
	// corpus, which was derived from the code and therefore agrees with it by
	// construction.
	covered := map[string]bool{}
	for _, testCase := range loadFitnessTable(t) {
		covered[testCase.ExpectedWorkflow] = true
	}
	var missing []string
	for _, workflow := range planWorkflowEnum(t) {
		if !covered[workflow] {
			missing = append(missing, workflow)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("no fitness case asserts these workflows: %v\n"+
			"Each is a selector branch checked only against fixtures derived "+
			"from the selector.", missing)
	}
}

func TestTheSelectorAgreesWithIndependentJudgment(t *testing.T) {
	// The real check. Every case whose expectation was reasoned from the
	// workflow docs rather than read off the selector must still be what the
	// selector produces.
	//
	// A case marked known_mismatch is excluded and reported, so a deliberate
	// disagreement stays visible rather than becoming a silent exemption. None
	// is marked today; the flag exists so that a future case that disagrees is
	// added as a disagreement rather than bent to match current behaviour.
	config := loadRoutingConfig(t)
	catalog := loadCatalogIDs(t)
	rosterRoot := filepath.Join(selectorRepoRoot(t), "roster")

	checked, deferred := 0, 0
	for _, testCase := range loadFitnessTable(t) {
		if testCase.KnownMismatch {
			deferred++
			t.Logf("%s is marked known_mismatch and is not compared: %s",
				testCase.ID, testCase.Description)
			continue
		}
		classification := testCase.Classification
		if classification == "" {
			classification = "internal"
		}
		// Standalone, matching the table's own harness: with a lifecycle
		// kernel resolvable, extra gate agents join the plan and can move the
		// workflow for reasons the table is not reasoning about.
		plan, err := BuildDispatchPlan(config, PlanInput{
			Task: testCase.Task, TaskID: testCase.TaskID,
			ChangedFiles: testCase.ChangedFiles, Classification: classification,
			RepositoryRoot: "<REPO_ROOT>", ChangedFileSource: "explicit",
		}, PlanOptions{Catalog: catalog, RosterRoot: rosterRoot})
		if err != nil {
			t.Errorf("%s: BuildDispatchPlan: %v", testCase.ID, err)
			continue
		}
		checked++
		if got, _ := plan["workflow"].(string); got != testCase.ExpectedWorkflow {
			t.Errorf("%s (%s):\n  independently reasoned: %s\n  selector produced:      %s\n"+
				"  One of the two is wrong. If the selector is right, the table entry "+
				"should be corrected with its reasoning updated; if the table is right, "+
				"this is a selector bug. Bending the table to match is the one response "+
				"that makes this test worthless.",
				testCase.ID, testCase.Description, testCase.ExpectedWorkflow, got)
		}
	}
	if checked == 0 {
		t.Fatal("every case is marked known_mismatch; the table now compares nothing")
	}
	t.Logf("compared %d cases against independent judgment, %d deferred", checked, deferred)
}
