package selector

import "testing"

// MatchedRouteIDs -- the input to near-miss reasoning, and the one place a
// diagnostic flag can take down the run it was meant to explain.
//
// It was at 0%. Its own comment says why that matters:
//
//	It round-trips through JSON rather than asserting on the plan's in-memory
//	shape. A plan assembled in-process carries typed slices, so a direct
//	`plan["matched_routes"].([]any)` panics -- and a panic in a diagnostic
//	flag takes down the run that was working a moment earlier without it.
//
// The failure is asymmetric in the worst way: `cadre select` succeeds, and
// `cadre select --explain` on the same input crashes. The person who reaches
// for the diagnostic is the one who loses their plan.

func TestMatchedRouteIDsReadsAPlanAssembledInProcess(t *testing.T) {
	// The shape BuildDispatchPlan actually produces, rather than the []any
	// shape a plan read back from stdout carries. A reader written against
	// the second panics on the first.
	type routeEntry struct {
		ID      string         `json:"id"`
		Reasons map[string]any `json:"reasons"`
	}
	plan := map[string]any{
		"task_id": "T-1",
		"status":  "ready",
		"matched_routes": []routeEntry{
			{ID: "infrastructure", Reasons: map[string]any{"keywords": []string{"terraform"}}},
			{ID: "security", Reasons: map[string]any{"keywords": []string{"secrets"}}},
		},
		"agents": map[string]any{"primary": []string{"backend-engineer"}},
	}

	matched := MatchedRouteIDs(plan)
	if len(matched) != 2 || !matched["infrastructure"] || !matched["security"] {
		t.Errorf("matched = %v, want both typed entries read", matched)
	}
}

func TestMatchedRouteIDsReadsAPlanParsedFromStdout(t *testing.T) {
	// The other representation, so the round-trip is not merely tolerated for
	// typed plans but correct for both. This is the shape `cadre select |
	// cadre something-else` produces.
	matched := MatchedRouteIDs(map[string]any{
		"matched_routes": []any{
			map[string]any{"id": "frontend"},
			map[string]any{"id": "testing"},
		},
	})
	if len(matched) != 2 || !matched["frontend"] || !matched["testing"] {
		t.Errorf("matched = %v, want both entries read", matched)
	}
}

func TestMatchedRouteIDsReturnsAnEmptySetRatherThanFailing(t *testing.T) {
	// Every one of these is a plan the selector can legitimately produce, or
	// one a caller can hand in. None of them justifies losing the run: with no
	// matched routes the correct near-miss report is "every route is a near
	// miss", which an empty set produces, and reporting nothing is better than
	// a crash in either case.
	for name, plan := range map[string]map[string]any{
		"a plan with no matched_routes key": {"task_id": "T", "status": "needs-triage"},
		"an explicitly empty list":          {"matched_routes": []any{}},
		"a null":                            {"matched_routes": nil},
		"the wrong type entirely":           {"matched_routes": "infrastructure"},
		"a list of strings, not objects":    {"matched_routes": []any{"infrastructure"}},
		"an entry with no id":               {"matched_routes": []any{map[string]any{"reasons": nil}}},
		"an id that is not a string":        {"matched_routes": []any{map[string]any{"id": 7}}},
		"an entirely empty plan":            {},
		// A value encoding/json cannot marshal, which is what makes the
		// round-trip itself fail rather than the reading after it.
		"a plan that cannot be serialized": {"matched_routes": []any{map[string]any{"id": "x"}},
			"unserializable": make(chan int)},
	} {
		t.Run(name, func(t *testing.T) {
			// A panic here fails the test by itself; the assertion covers the
			// quieter failure of returning something wrong.
			if matched := MatchedRouteIDs(plan); len(matched) != 0 {
				t.Errorf("matched = %v, want an empty set", matched)
			}
		})
	}
}
