package selector

import "testing"

// The checks in SelectWorkflow are ordered, and the order encodes bugs that
// were found in production rather than designed up front. These tests pin the
// orderings specifically, because a reordering looks harmless in review and
// changes published labels.

func routeMatch(id string, keywords ...string) Match {
	return Match{
		ID:      id,
		Reasons: RuleMatch{Keywords: keywords},
		Rule:    map[string]any{"id": id},
	}
}

func shapedRoute(id, shape string) Match {
	return Match{ID: id, Rule: map[string]any{"id": id, "workflow_shape": shape}}
}

func TestWorkflowRollbackIsCheckedBeforeProduction(t *testing.T) {
	// A rollback is production-shaped and almost always trips the production
	// risk rule too ("roll back the production release"). Checking production
	// first swallowed every rollback and labelled it production-release,
	// which made `rollback` a documented workflow no plan could ever be
	// assigned.
	got := SelectWorkflow([]Match{routeMatch("rollback")}, []string{"production"}, true)
	if got != "rollback" {
		t.Errorf("workflow = %q, want %q -- production must not swallow rollback", got, "rollback")
	}
}

func TestWorkflowRollbackDefersToIncidentFraming(t *testing.T) {
	// incident-response carries its own "rollback coordination" keyword, so a
	// task that merely mentions a rollback while describing escalation is
	// support-escalation. The rollback route's roles are still selected --
	// routes drive agents independently of this label.
	for _, companion := range []string{"incident-response", "support"} {
		got := SelectWorkflow([]Match{routeMatch("rollback"), routeMatch(companion)}, nil, true)
		if got != "support-escalation" {
			t.Errorf("rollback + %s = %q, want support-escalation", companion, got)
		}
	}
}

func TestWorkflowDebuggingByKeywordBeatsAgentSuiteMaintenance(t *testing.T) {
	// debugging and agent-suite-governance/orchestration share paths by
	// design, so path overlap alone cannot decide between them. A genuine bug
	// report keeps its keyword hit and must stay "debugging"; a routine
	// catalog edit with no debugging keyword is agent-suite-maintenance.
	withKeyword := SelectWorkflow(
		[]Match{routeMatch("debugging", "debug"), routeMatch("orchestration")}, nil, true)
	if withKeyword != "debugging" {
		t.Errorf("debugging matched by keyword = %q, want debugging", withKeyword)
	}

	pathOnly := SelectWorkflow(
		[]Match{routeMatch("debugging"), routeMatch("orchestration")}, nil, true)
	if pathOnly != "agent-suite-maintenance" {
		t.Errorf("debugging matched by path only = %q, want agent-suite-maintenance", pathOnly)
	}
}

func TestWorkflowNarrowShapeWinsOnlyUncontradicted(t *testing.T) {
	// A narrow shape wins when it is the only narrow shape present. Any other
	// narrow shape makes the work generic delivery, because new-service's
	// workflow doc is the one covering both.
	onlyInfrastructure := SelectWorkflow(
		[]Match{shapedRoute("infrastructure", "infrastructure-change"),
			shapedRoute("review", "unclassified")}, nil, true)
	if onlyInfrastructure != "infrastructure-change" {
		t.Errorf("single narrow shape = %q, want infrastructure-change", onlyInfrastructure)
	}

	contradicted := SelectWorkflow(
		[]Match{shapedRoute("infrastructure", "infrastructure-change"),
			shapedRoute("api", "new-service")}, nil, true)
	if contradicted != "new-service" {
		t.Errorf("two narrow shapes = %q, want new-service", contradicted)
	}
}

func TestWorkflowInfrastructureOutranksArchitectureChangeRisk(t *testing.T) {
	// An infrastructure change that also trips architecture-change is still
	// an infrastructure change.
	got := SelectWorkflow(
		[]Match{shapedRoute("infrastructure", "infrastructure-change")},
		[]string{"architecture-change"}, true)
	if got != "infrastructure-change" {
		t.Errorf("workflow = %q, want infrastructure-change", got)
	}
}

func TestWorkflowUnclassifiedIsReachable(t *testing.T) {
	// A plan that matched something but whose routes all declare
	// "unclassified" is unclassified -- an answer a route author wrote down,
	// not a fallthrough.
	got := SelectWorkflow([]Match{shapedRoute("review", "unclassified")}, nil, true)
	if got != "unclassified" {
		t.Errorf("workflow = %q, want unclassified", got)
	}
}

func TestWorkflowNeedsTriageOutranksEverything(t *testing.T) {
	got := SelectWorkflow([]Match{routeMatch("rollback")}, []string{"production"}, false)
	if got != "needs-triage" {
		t.Errorf("workflow = %q, want needs-triage when no agents were selected", got)
	}
}

func TestUndeclaredWorkflowShapeRoutesDistinguishesAbsenceFromUnclassified(t *testing.T) {
	// Declaring "unclassified" is a choice; declaring nothing is not, and the
	// plan reports the difference so an omission is visible.
	got := UndeclaredWorkflowShapeRoutes([]Match{
		shapedRoute("declared", "unclassified"),
		routeMatch("silent"),
	})
	if len(got) != 1 || got[0] != "silent" {
		t.Errorf("undeclared = %v, want only the route with no workflow_shape key", got)
	}
}
