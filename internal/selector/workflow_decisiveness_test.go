package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Every declared workflow_shape must be pinned by at least one fixture.
//
// Another test asserts that all of routing.json's routes declare a shape. That
// is a completeness check on the *file*, and on its own it measures the wrong
// thing: a route can declare a shape that no test would notice being changed,
// because every input matching it also matches a broad route already
// contributing its own shape. The declaration is then decorative -- freely
// editable to any legal value with a fully green suite -- which is the
// silent-editing problem shapes were introduced to remove, one level up from
// where it was fixed.
//
// Appearing in a fixture is not the same as being pinned by one. This measures
// the difference the only way it can be measured: substitute each route's
// declared shape for every other legal value, re-run the fixtures that match
// that route, and require at least one substitution to move some fixture's
// workflow.
//
// Ported from roster/orchestration/test/test_workflow_shape_coverage.py.

// structurallyUnpinnable maps a route that no input can isolate to the broad
// route that always co-matches it. Exempted by name rather than by weakening
// the check for everyone -- and the exemption is itself tested below, so it
// stops being valid the moment the subsumption stops holding.
var structurallyUnpinnable = map[string]string{
	"prompt-artifact-execution": "ai-feature",
}

type shapeFixture struct {
	Task           string
	ChangedFiles   []string
	Classification string
}

// everyFixtureInput reads both fixture files, deduplicated. A pin may live in
// either: the corpus for a regression baseline, the fitness table for an
// independently reasoned one.
func everyFixtureInput(t *testing.T) []shapeFixture {
	t.Helper()
	directory := filepath.Join(selectorRepoRoot(t), "roster", "orchestration", "test", "fixtures")
	seen := map[string]bool{}
	var inputs []shapeFixture
	for _, name := range []string{"selection_golden_corpus.json", "workflow_fitness_table.json"} {
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		var document struct {
			Cases []struct {
				Task           string   `json:"task"`
				ChangedFiles   []string `json:"changed_files"`
				Classification string   `json:"classification"`
			} `json:"cases"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, testCase := range document.Cases {
			classification := testCase.Classification
			if classification == "" {
				classification = "internal"
			}
			key := classification + "\x00" + testCase.Task + "\x00" +
				filepath.Join(testCase.ChangedFiles...)
			if seen[key] {
				continue
			}
			seen[key] = true
			inputs = append(inputs, shapeFixture{
				Task: testCase.Task, ChangedFiles: testCase.ChangedFiles,
				Classification: classification,
			})
		}
	}
	if len(inputs) < 100 {
		t.Fatalf("read %d fixture inputs; the fixtures failed to load", len(inputs))
	}
	return inputs
}

func legalShapes() []string {
	shapes := make([]string, 0, len(workflowShapes))
	for shape := range workflowShapes {
		shapes = append(shapes, shape)
	}
	sort.Strings(shapes)
	return shapes
}

// withRouteShape returns a copy of config in which one route declares a
// different shape. Only the one route's entry is rebuilt; nothing else moves.
func withRouteShape(config map[string]any, routeID, shape string) map[string]any {
	routes := objectList(config["routes"])
	replaced := make([]any, 0, len(routes))
	for _, route := range routes {
		if id, _ := route["id"].(string); id != routeID {
			replaced = append(replaced, route)
			continue
		}
		copied := make(map[string]any, len(route))
		for key, value := range route {
			copied[key] = value
		}
		copied["workflow_shape"] = shape
		replaced = append(replaced, copied)
	}
	swapped := make(map[string]any, len(config))
	for key, value := range config {
		swapped[key] = value
	}
	swapped["routes"] = replaced
	return swapped
}

func TestEveryDeclaredShapeChangesSomeFixtureWhenSubstituted(t *testing.T) {
	config := loadRoutingConfig(t)
	catalog := loadCatalogIDs(t)
	rosterRoot := filepath.Join(selectorRepoRoot(t), "roster")
	inputs := everyFixtureInput(t)
	shapes := legalShapes()

	workflowFor := func(routing map[string]any, input shapeFixture) string {
		t.Helper()
		// Standalone: no lifecycle contract is supplied, matching both fixture
		// harnesses. With a kernel resolvable, extra gate agents join the plan
		// and the comparison stops being about the shape.
		plan, err := BuildDispatchPlan(routing, PlanInput{
			Task: input.Task, TaskID: "SHAPE-DECISIVENESS",
			ChangedFiles: input.ChangedFiles, Classification: input.Classification,
			RepositoryRoot: "<REPO_ROOT>", ChangedFileSource: "explicit",
		}, PlanOptions{Catalog: catalog, RosterRoot: rosterRoot})
		if err != nil {
			t.Fatalf("BuildDispatchPlan: %v", err)
		}
		workflow, _ := plan["workflow"].(string)
		return workflow
	}

	var unpinned []string
	inScope := 0
	for _, route := range objectList(config["routes"]) {
		id, _ := route["id"].(string)
		declared, _ := route["workflow_shape"].(string)
		// Scope: routes declaring a delivery shape. A route declaring
		// "unclassified" contributes nothing by definition, so most
		// substitutions of it are unobservable, and requiring a pin for all of
		// them costs several times the runtime to guard declarations carrying
		// no behaviour. A route moving *off* unclassified lands in scope
		// automatically.
		if declared == "" || declared == "unclassified" {
			continue
		}
		if _, exempt := structurallyUnpinnable[id]; exempt {
			continue
		}
		inScope++

		// Substituting a route's shape can only move an input whose plan
		// matched that route, so resolve matching first and build plans only
		// for the inputs that survive. This is what keeps the cross-product
		// affordable.
		var covering []shapeFixture
		for _, input := range inputs {
			if MatchRule(route, input.Task, input.ChangedFiles).Matched {
				covering = append(covering, input)
			}
		}
		if len(covering) == 0 {
			unpinned = append(unpinned, id+" (no fixture matches it at all)")
			continue
		}

		pinned := false
		for _, input := range covering {
			baseline := workflowFor(config, input)
			for _, candidate := range shapes {
				if candidate == declared {
					continue
				}
				if workflowFor(withRouteShape(config, id, candidate), input) != baseline {
					pinned = true
					break
				}
			}
			if pinned {
				break
			}
		}
		if !pinned {
			unpinned = append(unpinned, id+" (matched by fixtures, but no "+
				"substitution moves any of their workflows)")
		}
	}

	if inScope == 0 {
		t.Fatal("no route declares a delivery shape; this test would prove nothing")
	}
	for _, name := range unpinned {
		t.Errorf("%s declares a shape nothing pins: it can be edited to any legal "+
			"value with a green suite. Add a fixture that isolates it, or exempt "+
			"it by name with a subsumption argument.", name)
	}
	t.Logf("%d routes in scope, %d fixture inputs, %d legal shapes",
		inScope, len(inputs), len(shapes))
}

func TestEachExemptRouteIsWhollySubsumedByItsBroadRoute(t *testing.T) {
	// The exemption above is a claim -- "no input can isolate this route" --
	// and claims rot. If the broad route stops contributing a shape, or stops
	// matching one of the exempt route's keywords, the exempt route becomes
	// isolable and the exemption becomes a hole.
	config := loadRoutingConfig(t)
	routes := map[string]map[string]any{}
	for _, route := range objectList(config["routes"]) {
		if id, _ := route["id"].(string); id != "" {
			routes[id] = route
		}
	}
	if len(structurallyUnpinnable) == 0 {
		t.Skip("no exemptions to check")
	}

	for exemptID, broadID := range structurallyUnpinnable {
		t.Run(exemptID, func(t *testing.T) {
			exempt, ok := routes[exemptID]
			if !ok {
				t.Fatalf("%s is exempted but no longer exists; drop the exemption", exemptID)
			}
			broad, ok := routes[broadID]
			if !ok {
				t.Fatalf("%s is exempted against %s, which no longer exists",
					exemptID, broadID)
			}
			if shape, _ := broad["workflow_shape"].(string); shape == "unclassified" || shape == "" {
				t.Errorf("%s no longer contributes a shape, so %s can now be pinned "+
					"-- remove the exemption and add a fixture", broadID, exemptID)
			}
			keywords := stringSlice(exempt["keywords"])
			if len(keywords) == 0 {
				t.Fatalf("%s declares no keywords, so the subsumption argument "+
					"cannot be checked", exemptID)
			}
			for _, keyword := range keywords {
				if !MatchRule(broad, keyword, nil).Matched {
					t.Errorf("%s keyword %q no longer matches %s; it can now be "+
						"isolated, so remove the exemption and add a pin",
						exemptID, keyword, broadID)
				}
			}
		})
	}
}
