package selector

import (
	"fmt"
	"strings"
)

// Near-miss route explanations for `cadre select --explain`.
//
// The plan's matched_routes[].reasons answers "why did this route match?".
// This answers the complementary question -- "why did this route NOT match,
// and how close did it come?" -- as a presentation concern printed to stderr,
// never as a plan field and never inside the fingerprint.
//
// Why keyword_groups is the only graded signal: a route matches when
// matched_keywords OR conjunctive_match OR matched_paths is true. Plain
// keywords and paths are disjunctive triggers, so if even one had fired the
// route would already be matched and would never reach here -- for an
// unmatched route their relevance is always exactly zero, and reporting
// "0 of N matched" for every unmatched route on every call would be precisely
// the noise this feature exists to avoid.
//
// keyword_groups is conjunctive, so a group can be genuinely partially
// satisfied. That is the sole relevance threshold applied: a route is
// surfaced only when some group has 1 <= matched < len(group). A group at
// 0-of-N is noise, and N-of-N would mean the route matched -- a contradiction
// for a route reaching this function at all.
//
// Deliberately descriptive, never scored. No percentage, confidence,
// closeness, or cross-route ranking is computed or emitted under any name:
// selection here is deterministic, not judgment, and a numeric score on a
// match was explicitly rejected.

// NearMissGroup is one partially satisfied keyword group.
type NearMissGroup struct {
	Matched []string
	Missing []string
}

// NearMiss is one unmatched route that came close.
type NearMiss struct {
	ID     string
	Groups []NearMissGroup
}

// ExplainRouteNearMiss explains how close an unmatched route came, or reports
// that it does not clear the relevance threshold.
//
// Callers must only pass routes absent from the plan's matched_routes: this
// does not re-check the route's own keywords or paths, because an unmatched
// route's relevance on those is zero by construction.
func ExplainRouteNearMiss(route map[string]any, taskText string) (NearMiss, bool) {
	normalized := strings.ToLower(taskText)
	var partial []NearMissGroup

	for _, rawGroup := range anyList(route["keyword_groups"]) {
		var matched, missing []string
		for _, rawKeyword := range anyList(rawGroup) {
			keyword, ok := rawKeyword.(string)
			if !ok {
				continue
			}
			if KeywordMatches(normalized, keyword) {
				matched = append(matched, keyword)
			} else {
				missing = append(missing, keyword)
			}
		}
		if len(matched) > 0 && len(missing) > 0 {
			partial = append(partial, NearMissGroup{Matched: matched, Missing: missing})
		}
	}
	if len(partial) == 0 {
		return NearMiss{}, false
	}
	return NearMiss{ID: stringOr(route["id"], ""), Groups: partial}, true
}

// FindNearMisses explains every route not in matchedRouteIDs, in routing.json
// declaration order. Routes below the relevance threshold are omitted
// entirely rather than included with an empty reasoning block -- this is a
// filtered "worth looking at" list, not a dump of every unmatched route.
func FindNearMisses(config map[string]any, taskText string, matchedRouteIDs map[string]bool) []NearMiss {
	var nearMisses []NearMiss
	for _, route := range objectList(config["routes"]) {
		id, _ := route["id"].(string)
		if matchedRouteIDs[id] {
			continue
		}
		if explanation, ok := ExplainRouteNearMiss(route, taskText); ok {
			nearMisses = append(nearMisses, explanation)
		}
	}
	return nearMisses
}

// FormatNearMissesText renders the result as a short, scannable block for
// --explain to print to stderr, never to the JSON plan on stdout.
func FormatNearMissesText(nearMisses []NearMiss) string {
	if len(nearMisses) == 0 {
		return "--explain: no near-miss routes for this task -- no unmatched route had a " +
			"partially satisfied keyword_groups entry (see route_near_miss.py's relevance " +
			"threshold; most routes in the current routing.json use plain keywords/paths, " +
			"which have no partial-match state to report).\n"
	}

	lines := []string{"--explain: near-miss routes (did not match, but came close)", ""}
	for _, entry := range nearMisses {
		lines = append(lines, entry.ID+":")
		for index, group := range entry.Groups {
			total := len(group.Matched) + len(group.Missing)
			lines = append(lines, fmt.Sprintf(
				"  keyword_groups[%d]: matched %d of %d required keywords (%s); missing: %s",
				index+1, len(group.Matched), total,
				strings.Join(group.Matched, ", "), strings.Join(group.Missing, ", ")))
		}
		lines = append(lines, "")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\n\v\f\r") + "\n"
}

// MatchedRouteIDs extracts the plan's matched route ids for --explain.
//
// It round-trips through JSON rather than asserting on the plan's in-memory
// shape. A plan assembled in-process carries typed slices, so a direct
// `plan["matched_routes"].([]any)` panics -- and a panic in a diagnostic
// flag takes down the run that was working a moment earlier without it.
func MatchedRouteIDs(plan map[string]any) map[string]bool {
	matched := map[string]bool{}
	normalized, err := normalizePlanForText(plan)
	if err != nil {
		return matched
	}
	for _, route := range objectList(normalized["matched_routes"]) {
		if id, ok := route["id"].(string); ok {
			matched[id] = true
		}
	}
	return matched
}
