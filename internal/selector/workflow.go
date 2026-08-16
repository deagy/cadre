package selector

// SelectWorkflow ports build_dispatch_plan.py's _select_workflow.
//
// The order of these checks is behaviour, not style. Each comment records why
// a check sits where it does, because the ordering encodes bugs that were
// found the hard way and a "tidy" reordering would reintroduce them.
func SelectWorkflow(matchedRoutes []Match, riskIDs []string, hasAgents bool) string {
	routeIDs := make([]string, 0, len(matchedRoutes))
	for _, route := range matchedRoutes {
		routeIDs = append(routeIDs, route.ID)
	}
	hasRoute := setOf(routeIDs)
	hasRisk := setOf(riskIDs)

	if !hasAgents {
		return "needs-triage"
	}

	// Before the production check, deliberately. A rollback is
	// production-shaped and almost always trips the `production` risk rule
	// too ("roll back the production release"), so checking production first
	// swallowed every rollback and labelled it production-release -- which is
	// why `rollback` was a documented, enumerated workflow no plan could ever
	// be assigned. Ordering decides only the *label*: production's reviewers
	// and its human gate come from the risk rules, not from here.
	//
	// ...but not when the frame is incident coordination. incident-response
	// carries its own "rollback coordination" keyword, so a task that merely
	// mentions a rollback while describing escalation is support-escalation.
	if hasRoute["rollback"] && !hasRoute["incident-response"] && !hasRoute["support"] {
		return "rollback"
	}
	if hasRisk["production"] {
		return "production-release"
	}
	if hasRoute["support"] || hasRoute["incident-response"] {
		return "support-escalation"
	}
	if hasRoute["runtime-assurance"] {
		return "runtime-assurance"
	}
	if hasRoute["knowledge-store"] && allIn(routeIDs, "knowledge-store", "documentation", "testing") {
		return "knowledge-ingestion"
	}

	// "debugging" and "agent-suite-governance"/"orchestration" share paths by
	// design -- editing a role or routing rule is simultaneously roster
	// self-maintenance and something debugging's broad agent-tune-up paths
	// also cover -- so path overlap alone cannot decide which applies. What
	// can: whether debugging actually fired on a debugging-shaped *keyword*
	// rather than merely a shared path. This must run before the plain
	// `debugging` check below and before agent-suite-* is asserted, so a
	// keyword-driven debugging match always wins the tie.
	for _, route := range matchedRoutes {
		if route.ID == "debugging" && len(route.Reasons.Keywords) > 0 {
			return "debugging"
		}
	}
	if hasRoute["agent-suite-governance"] || hasRoute["orchestration"] {
		return "agent-suite-maintenance"
	}
	if hasRoute["debugging"] {
		return "debugging"
	}

	if (hasRoute["product-intent"] || hasRoute["requirements-baseline"]) &&
		allIn(routeIDs, "product-intent", "requirements-baseline", "documentation", "testing") &&
		!hasRisk["architecture-change"] {
		return "product-intake"
	}

	// Everything above decides a shape from a *condition* -- a route id plus
	// an exclusion, a keyword having actually fired, a risk rule. Those are
	// not expressible as a per-route constant and stay here.
	//
	// What follows is the delivery-shape fallback, reading each matched
	// route's own declared workflow_shape. A narrow shape wins only when
	// nothing contradicts it: any *other* narrow shape among the matched
	// routes makes the work generic delivery, which is what new-service's
	// workflow doc covers. Both checks stay ahead of the architecture-change
	// risk check, because an infrastructure change that also trips
	// architecture-change is still an infrastructure change.
	//
	// "unclassified" stays reachable and meaningful: it is what a plan gets
	// when it matched something (so not needs-triage) but no matched route
	// claims a delivery shape -- advisory, assessment, review, governance,
	// support, documentation and evidence routes all declare it deliberately.
	shapes := map[string]bool{}
	for _, route := range matchedRoutes {
		shape, _ := route.Rule["workflow_shape"].(string)
		if shape == "" || shape == "unclassified" {
			continue
		}
		shapes[shape] = true
	}
	if len(shapes) == 1 && shapes["infrastructure-change"] {
		return "infrastructure-change"
	}
	if len(shapes) == 1 && shapes["pipeline-change"] {
		return "pipeline-change"
	}
	if len(shapes) > 0 || hasRisk["architecture-change"] {
		return "new-service"
	}
	return "unclassified"
}

// UndeclaredWorkflowShapeRoutes names every matched route that declared no
// workflow_shape at all, ported from _undeclared_workflow_shape_routes. A
// route that declares "unclassified" has made a choice; one that declares
// nothing has not, and the plan reports the difference so the omission is
// visible rather than silently behaving like "unclassified".
func UndeclaredWorkflowShapeRoutes(matchedRoutes []Match) []string {
	undeclared := []string{}
	for _, route := range matchedRoutes {
		// A shape counts as declared only when it is a non-empty string.
		//
		// Presence alone is not enough, and the difference is reachable: an
		// overlay may write "workflow_shape": null, which validation accepts
		// (the value check skips a null) while the workflow selector reads no
		// shape off it. Keying on presence let exactly that case fall back to
		// "unclassified" with nothing in the plan to say so -- the defect this
		// signal exists to make visible.
		//
		// Python spells this as falsiness, so null, "" and any other empty
		// value all report. Anything that is not a non-empty string reports
		// here for the same reason, which is also the conservative direction
		// for a signal whose whole purpose is visibility.
		shape, _ := route.Rule["workflow_shape"].(string)
		if shape == "" {
			undeclared = append(undeclared, route.ID)
		}
	}
	return undeclared
}

func setOf(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

// allIn reports whether every value is one of permitted. An empty values
// slice is vacuously true, matching Python's all().
func allIn(values []string, permitted ...string) bool {
	allowed := setOf(permitted)
	for _, value := range values {
		if !allowed[value] {
			return false
		}
	}
	return true
}
