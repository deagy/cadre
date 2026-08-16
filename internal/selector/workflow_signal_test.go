package selector

import (
	"strings"
	"testing"
)

// undeclared_workflow_shape_routes -- the signal that turns a silent workflow
// fallback into a visible one.
//
// Every route in this repository's own routing.json declares a workflow_shape,
// and a test keeps it that way. That guarantee stops at the overlay boundary:
// routing.schema.json leaves the field optional so an overlay written before
// shapes existed still validates, and validation checks the *value* against
// the enum, never its presence. A consuming project adding a route to
// .agents/orchestration/routing-overlay.json can therefore reproduce the exact
// defect shapes were introduced to fix -- the route matches, contributes no
// delivery shape, and the plan falls back to "unclassified" by omission with
// nothing to notice short of reading the plan.
//
// Reporting rather than rejecting was the deliberate trade: requiring the
// field would break every overlay that already adds a route.
//
// Ported from roster/orchestration/test/test_undeclared_workflow_shape.py.

func shapeRule(id string, shape any) Match {
	rule := map[string]any{"id": id}
	if shape != nil {
		rule["workflow_shape"] = shape
	}
	return Match{ID: id, Rule: rule}
}

// nullShapeRule is a route whose workflow_shape key is present and null --
// the shape an overlay can legally write and validation accepts.
func nullShapeRule(id string) Match {
	return Match{ID: id, Rule: map[string]any{"id": id, "workflow_shape": nil}}
}

func TestAPresentButEmptyShapeIsStillUndeclared(t *testing.T) {
	// The bug this test was written for. Keying on the key's *presence* let a
	// route with "workflow_shape": null count as declared -- so it matched,
	// contributed nothing to the workflow decision, and the plan fell back to
	// unclassified with an empty signal.
	//
	// That is reachable from an overlay today: validation skips a null rather
	// than rejecting it, so the document is accepted and the signal was the
	// only thing that would have said anything.
	for _, probe := range []struct {
		name  string
		route Match
	}{
		{"a key that is not there at all", shapeRule("absent", nil)},
		{"a key present and null", nullShapeRule("null-shape")},
		{"a key present and empty", shapeRule("empty-shape", "")},
	} {
		t.Run(probe.name, func(t *testing.T) {
			got := UndeclaredWorkflowShapeRoutes([]Match{probe.route})
			if len(got) != 1 || got[0] != probe.route.ID {
				t.Errorf("undeclared = %v, want [%s]", got, probe.route.ID)
			}
		})
	}
}

func TestDeclaringUnclassifiedIsADeclaration(t *testing.T) {
	// The other side, and the reason the test above cannot simply report
	// everything: a route stating "unclassified" claims no delivery shape on
	// purpose and is doing the right thing. Reporting it would make the signal
	// noise, and a signal that fires on correct configuration gets ignored.
	got := UndeclaredWorkflowShapeRoutes([]Match{
		shapeRule("declared-unclassified", "unclassified"),
		shapeRule("declared-execution", "execution"),
	})
	if len(got) != 0 {
		t.Errorf("undeclared = %v, want nothing reported for declared routes", got)
	}
}

func TestTheSignalNamesOnlyTheRoutesThatMatchedInMatchOrder(t *testing.T) {
	// Two contractual properties, both easy to lose to a tidy-up.
	//
	// Match order, never sorted: the list lines up positionally with the
	// plan's own matched_routes, which is the only way a reader can correlate
	// the two. Sorting would still produce "the right ids" and quietly break
	// that.
	//
	// And only matched routes: an unmatched route declaring nothing is not a
	// problem with this plan, and naming it would train readers to skim past
	// the ones that are.
	mixed := []Match{
		shapeRule("zeta-undeclared", nil),
		shapeRule("alpha-declared", "execution"),
		nullShapeRule("mid-null"),
		shapeRule("beta-declared", "unclassified"),
		shapeRule("aaa-undeclared", nil),
	}
	got := UndeclaredWorkflowShapeRoutes(mixed)
	want := []string{"zeta-undeclared", "mid-null", "aaa-undeclared"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("undeclared = %v, want %v -- in match order, interleaved with the "+
			"declared routes rather than sorted", got, want)
	}
	if len(UndeclaredWorkflowShapeRoutes(nil)) != 0 {
		t.Error("a plan that matched nothing must produce no signal")
	}
}

func TestTheSignalIsOmittedWhenEmptyButHashedWhenPresent(t *testing.T) {
	// The key is absent from a plan with nothing to report -- optional, so a
	// consumer written before it existed still reads the plan.
	//
	// It is *not* excluded from the fingerprint, though, and that asymmetry is
	// deliberate: matched_routes emits only id and reasons, never the shape,
	// so a route flipping between declaring "unclassified" and declaring
	// nothing at all would otherwise produce a byte-identical plan with a
	// byte-identical fingerprint. The signal is the only thing that
	// distinguishes them.
	config := loadRoutingConfig(t)
	catalog := loadCatalogIDs(t)

	clean, err := BuildDispatchPlan(config, PlanInput{
		Task:   "Update the React navigation for keyboard accessibility",
		TaskID: "SIGNAL-1", ChangedFiles: []string{"frontend/src/Nav.tsx"},
		Classification: "internal", RepositoryRoot: "<REPO_ROOT>",
		ChangedFileSource: "explicit",
	}, PlanOptions{Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := clean["undeclared_workflow_shape_routes"]; present {
		t.Errorf("the signal is present with nothing to report: %v",
			clean["undeclared_workflow_shape_routes"])
	}

	// Two plans differing only in the signal must not share a fingerprint.
	withSignal := map[string]any{
		"schema_version": SchemaVersion, "task_id": "T", "status": "ready",
		"matched_routes":                   []any{map[string]any{"id": "r"}},
		"undeclared_workflow_shape_routes": []any{"r"},
	}
	without := map[string]any{
		"schema_version": SchemaVersion, "task_id": "T", "status": "ready",
		"matched_routes": []any{map[string]any{"id": "r"}},
	}
	first, err := DispatchFingerprint(withSignal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DispatchFingerprint(without)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Error("a plan carrying the signal fingerprints identically to one " +
			"without it, so a route that stopped declaring its shape would be " +
			"indistinguishable from one that never had to")
	}
	// And it is stable: the same plan twice is the same fingerprint.
	repeat, err := DispatchFingerprint(withSignal)
	if err != nil {
		t.Fatal(err)
	}
	if repeat != first {
		t.Errorf("the fingerprint is not deterministic with the signal present: %s vs %s",
			first, repeat)
	}
}

func TestThisRepositorysOwnRoutingNeverTriggersTheSignal(t *testing.T) {
	// The guarantee the signal exists to backstop, checked against the shipped
	// file rather than a fixture. Every base route declares a shape; a new one
	// added without would make this repository's own plans carry the warning it
	// ships to tell consumers about.
	config := loadRoutingConfig(t)
	routes := objectList(config["routes"])
	if len(routes) == 0 {
		t.Fatal("no routes were read; this test would prove nothing")
	}
	all := make([]Match, 0, len(routes))
	for _, rule := range routes {
		id, _ := rule["id"].(string)
		all = append(all, Match{ID: id, Rule: rule})
	}
	if got := UndeclaredWorkflowShapeRoutes(all); len(got) != 0 {
		t.Errorf("%d of this repository's own routes declare no workflow_shape: %v",
			len(got), got)
	}
}
