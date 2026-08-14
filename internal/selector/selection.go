package selector

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Match is one rule that fired, carrying the reasons the plan reports and the
// rule itself for downstream assembly.
type Match struct {
	ID      string
	Reasons RuleMatch
	Rule    map[string]any
}

// catalogIDLine matches catalog.yaml's `  <id>:` block headers, mirroring
// routing.py's parse_keyed_entries.
var catalogIDLine = regexp.MustCompile(`^  ([a-z0-9-]+):\s*$`)

// ParseCatalogIDs returns catalog.yaml's agent ids in file order.
//
// catalog.yaml is parsed line-orientedly rather than with a YAML library --
// that is routing.py's choice, and the *order* it produces is load-bearing:
// Ordered below sorts selected agents by catalog position, so a different
// traversal order silently reorders every agents list in every plan.
//
// Note internal/orchestration's LoadCatalog is not reusable here: it parses
// JSON, a leftover from the selector removed in #269. catalog.yaml is not
// JSON.
//
// A duplicate id is an error rather than last-one-wins, matching Python: two
// blocks claiming one id makes catalog position ambiguous, which is exactly
// the thing this ordering depends on.
func ParseCatalogIDs(content string) ([]string, error) {
	var ids []string
	seen := map[string]int{}
	for number, line := range strings.Split(content, "\n") {
		match := catalogIDLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		id := match[1]
		if previous, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("line %d: duplicate id %q (first seen at line %d)",
				number+1, id, previous)
		}
		seen[id] = number + 1
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no agents found in catalog")
	}
	return ids, nil
}

// matchAll runs MatchRule over one of routing.json's rule collections.
func matchAll(config map[string]any, key, taskText string, changedFiles []string) []Match {
	rules, _ := config[key].([]any)
	matches := make([]Match, 0, len(rules))
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		reasons := MatchRule(rule, taskText, changedFiles)
		if !reasons.Matched {
			continue
		}
		id, _ := rule["id"].(string)
		matches = append(matches, Match{ID: id, Reasons: reasons, Rule: rule})
	}
	return matches
}

// MatchRoutes ports routing.py's match_routes.
func MatchRoutes(config map[string]any, taskText string, changedFiles []string) []Match {
	return matchAll(config, "routes", taskText, changedFiles)
}

// MatchContextPacks ports routing.py's match_context_packs: non-authoring
// context packs selected with the ordinary route grammar.
func MatchContextPacks(config map[string]any, taskText string, changedFiles []string) []Match {
	return matchAll(config, "context_packs", taskText, changedFiles)
}

// ClassifyRisks ports risk_classifier.py's classify_risks.
func ClassifyRisks(config map[string]any, taskText string, changedFiles []string) []Match {
	return matchAll(config, "risk_rules", taskText, changedFiles)
}

// ApplyCrossStack ports risk_classifier.py's apply_cross_stack: support
// agents added when enough of the named routes matched at once.
func ApplyCrossStack(config map[string]any, matchedRoutes []Match) []string {
	crossStack, ok := config["cross_stack"].(map[string]any)
	if !ok || len(crossStack) == 0 {
		return nil
	}
	routeIDs := map[string]bool{}
	for _, id := range stringSlice(crossStack["route_ids"]) {
		routeIDs[id] = true
	}
	relevant := 0
	for _, route := range matchedRoutes {
		if routeIDs[route.ID] {
			relevant++
		}
	}
	minimum, _ := crossStack["minimum_matches"].(float64)
	if float64(relevant) < minimum {
		return nil
	}
	return stringSlice(crossStack["support"])
}

// Ordered de-duplicates values keeping first appearance, then sorts by
// catalog position.
//
// Python is `sorted(_unique(values), key=positions.get(agent, len(catalog)))`.
// Two properties there are easy to lose and both matter:
//
//   - `sorted` is stable, and every agent absent from the catalog shares the
//     key len(catalog) -- so unknown agents keep their first-seen order
//     rather than being reordered among themselves. sort.SliceStable, not
//     sort.Slice.
//   - de-duplication happens before sorting and keeps the *first*
//     occurrence, which is what makes the input order observable at all.
func Ordered(values []string, catalog []string) []string {
	position := make(map[string]int, len(catalog))
	for index, agent := range catalog {
		position[agent] = index
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	rank := func(agent string) int {
		if index, ok := position[agent]; ok {
			return index
		}
		return len(catalog)
	}
	sort.SliceStable(unique, func(i, j int) bool {
		return rank(unique[i]) < rank(unique[j])
	})
	return unique
}

// AgentGroups is the plan's `agents` object.
type AgentGroups struct {
	Primary   []string `json:"primary"`
	Reviewers []string `json:"reviewers"`
	Support   []string `json:"support"`
}

// Disposition is the plan's `dispatch_disposition` object.
type Disposition struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// These strings are contract, not prose: consumers read `status`, and the
// reason text is what an orchestrator surfaces to a human before deciding
// whether to act without a dispatch. Kept verbatim from
// build_dispatch_plan.py.
const (
	staffedReason = "A primary and/or reviewer role was selected and can be dispatched as an accountable executor or independent reviewer."

	advisoryOnlyReasonSuffix = ") were selected; no primary or reviewer role " +
		"matched this task. Support-only selections are advisory input, not an accountable " +
		"executor or independent reviewer. Before performing any destructive or " +
		"persistent-environment action directly, report this disposition and either dispatch " +
		"an available reviewer for independent pre-action verification or state explicitly " +
		"why no dispatch occurred."

	noAgentsReason = "No route or risk rule matched this task; there is nothing to dispatch."
)

// BuildDispatchDisposition ports _build_dispatch_disposition: it makes
// explicit whether a selection can be dispatched as an accountable
// executor/reviewer, or is advisory-only support. Without it, a
// support-only selection is indistinguishable in the plan from a fully
// staffed one.
func BuildDispatchDisposition(groups AgentGroups) Disposition {
	if len(groups.Primary) > 0 || len(groups.Reviewers) > 0 {
		return Disposition{Status: "staffed", Reason: staffedReason}
	}
	if len(groups.Support) > 0 {
		return Disposition{
			Status: "advisory-only",
			Reason: "Only support role(s) (" + strings.Join(groups.Support, ", ") + advisoryOnlyReasonSuffix,
		}
	}
	return Disposition{Status: "no-agents-selected", Reason: noAgentsReason}
}
