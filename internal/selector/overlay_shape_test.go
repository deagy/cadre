package selector

import (
	"strings"
	"testing"
)

// workflow_shape across the overlay boundary.
//
// This is where the null-shape defect actually entered. The unit-level test in
// workflow_signal_test.go pins the predicate; these pin the path a consuming
// project takes to reach it -- writing a route into
// .agents/orchestration/routing-overlay.json.
//
// The shape decides which workflow a plan claims, and the workflow decides
// which lifecycle gates a change is held to. So the three rules below are not
// bookkeeping: a base route's shape is fixed, a new route's shape is the
// author's to choose, and a new route that chooses nothing is named in the
// plan rather than silently contributing nothing.
//
// Ported from test_undeclared_workflow_shape.py's overlay cases.

const shapedBase = `{
  "version": 1,
  "routes": [{"id": "backend", "keywords": ["api", "handler"], "paths": ["src/**"],
              "primary": ["backend-engineer"], "reviewers": ["code-reviewer"],
              "human_gate": true, "workflow_shape": "new-service"}],
  "risk_rules": [],
  "knowledge_focus": {"backend-engineer": "prior defects", "code-reviewer": "review history"}
}`

func mergeOntoShapedBase(t *testing.T, overlayText string) (map[string]any, error) {
	t.Helper()
	return MergeRouting(baseFrom(t, shapedBase), overlayFrom(t, overlayText))
}

func routeByID(t *testing.T, config map[string]any, id string) map[string]any {
	t.Helper()
	for _, route := range objectList(config["routes"]) {
		if got, _ := route["id"].(string); got == id {
			return route
		}
	}
	t.Fatalf("no route %q in the merged configuration", id)
	return nil
}

func TestAnOverlayMayNotChangeABaseRoutesWorkflowShape(t *testing.T) {
	// workflow_shape is not in the widen list, so the generic immutability
	// rule already covers it -- overlay_test.go proves that rule with
	// reviewers, human_gate, primary and exclude_paths. This adds the field
	// whose consequence is a different lifecycle: re-shaping a base route from
	// "new-service" to something narrower changes which gates the change is held
	// to, without touching a single reviewer.
	_, err := mergeOntoShapedBase(t,
		`{"routes": [{"id": "backend", "workflow_shape": "unclassified"}]}`)
	if err == nil {
		t.Fatal("an overlay re-shaped a base route")
	}
	if !strings.Contains(err.Error(), "workflow_shape") {
		t.Errorf("the refusal does not name the field: %v", err)
	}

	// Restating the base value exactly is a permitted no-op, so an author may
	// include surrounding context in the entry they are widening.
	merged, err := mergeOntoShapedBase(t, `{"routes": [{"id": "backend",
		"workflow_shape": "new-service", "keywords": ["api", "handler", "rpc"]}]}`)
	if err != nil {
		t.Fatalf("an exact restatement must be permitted: %v", err)
	}
	if got := routeByID(t, merged, "backend")["workflow_shape"]; got != "new-service" {
		t.Errorf("the base shape came out as %v", got)
	}
}

func TestANewOverlayRouteChoosesItsOwnShapeOrIsNamedForHavingNone(t *testing.T) {
	// The additive path, and the whole reason the signal exists. Requiring the
	// field would have been the stronger guarantee; it was rejected because it
	// breaks every overlay that already adds a route. So the contract is:
	// declare and be believed, or omit and be named.
	declared, err := mergeOntoShapedBase(t, `{"routes": [{"id": "added-declared",
		"keywords": ["zzz"], "workflow_shape": "unclassified"}]}`)
	if err != nil {
		t.Fatalf("a new route declaring its own shape must be permitted: %v", err)
	}
	if got := routeByID(t, declared, "added-declared")["workflow_shape"]; got != "unclassified" {
		t.Errorf("the declared shape came out as %v", got)
	}

	omitted, err := mergeOntoShapedBase(t,
		`{"routes": [{"id": "added-silent", "keywords": ["zzz"]}]}`)
	if err != nil {
		t.Fatalf("a new route omitting its shape must still be permitted: %v", err)
	}
	if _, present := routeByID(t, omitted, "added-silent")["workflow_shape"]; present {
		t.Error("a shape was invented for a route that declared none")
	}
}

func TestAnOverlayRouteWithoutAShapeIsNamedInThePlan(t *testing.T) {
	// End to end, because the merge and the signal are separate mechanisms and
	// the defect this file exists for lived in the join between them: the
	// merge accepted the route, and the signal failed to name it.
	//
	// Three shapes, one merged configuration each, driven through the same
	// path a real invocation takes.
	for _, probe := range []struct {
		name     string
		entry    string
		reported bool
	}{
		{"declaring a shape", `"workflow_shape": "unclassified",`, false},
		{"omitting the field", ``, true},
		// The one that regressed. Validation accepts a null because the value
		// check skips it, so the document is legal and the signal is the only
		// thing that can say anything.
		{"declaring null", `"workflow_shape": null,`, true},
	} {
		t.Run(probe.name, func(t *testing.T) {
			merged, err := mergeOntoShapedBase(t, `{"routes": [{"id": "overlay-added",
				`+probe.entry+` "keywords": ["zzprobetoken"],
				"primary": ["backend-engineer"]}]}`)
			if err != nil {
				t.Fatalf("the overlay was refused: %v", err)
			}
			if err := ValidateRoutingConfig(merged); err != nil {
				t.Fatalf("the merged configuration is invalid, so this case never "+
					"reaches the selector: %v", err)
			}

			plan, err := BuildDispatchPlan(merged, PlanInput{
				Task: "zzprobetoken work", TaskID: "OVERLAY-SHAPE",
				Classification: "internal", RepositoryRoot: "<REPO_ROOT>",
				ChangedFileSource: "explicit",
			}, PlanOptions{Catalog: []string{"backend-engineer"}})
			if err != nil {
				t.Fatalf("BuildDispatchPlan: %v", err)
			}
			if ids := planRouteIDs(plan); len(ids) != 1 || ids[0] != "overlay-added" {
				t.Fatalf("the overlay route did not match (%v), so this case "+
					"proves nothing", ids)
			}

			signal, present := plan["undeclared_workflow_shape_routes"]
			if probe.reported {
				if !present {
					t.Fatalf("the route contributes no delivery shape and the plan "+
						"says nothing; workflow = %v", plan["workflow"])
				}
				named, _ := signal.([]string)
				if len(named) != 1 || named[0] != "overlay-added" {
					t.Errorf("signal = %v, want [overlay-added]", signal)
				}
			} else if present {
				t.Errorf("a route that declared its shape was reported: %v", signal)
			}
		})
	}
}

func TestWithNoOverlayThePlanShapeIsWhateverTheBaseDeclared(t *testing.T) {
	// The control. Every case above changes the configuration before building
	// a plan; this one changes nothing, so a signal appearing here would mean
	// the base itself is being misread rather than the overlay mishandled.
	base := baseFrom(t, shapedBase)
	resolved, err := ResolveEffectiveRouting(base, "")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildDispatchPlan(resolved, PlanInput{
		Task: "api handler work", TaskID: "NO-OVERLAY",
		Classification: "internal", RepositoryRoot: "<REPO_ROOT>",
		ChangedFileSource: "explicit",
	}, PlanOptions{Catalog: []string{"backend-engineer", "code-reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	if ids := planRouteIDs(plan); len(ids) != 1 || ids[0] != "backend" {
		t.Fatalf("the base route did not match (%v)", ids)
	}
	if signal, present := plan["undeclared_workflow_shape_routes"]; present {
		t.Errorf("a base route declaring \"new-service\" was reported as undeclared: %v", signal)
	}
}
