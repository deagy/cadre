package generators

import (
	"sort"
	"strings"
	"testing"
)

// The packaged selector routes cadre's own packaging paths, and only those.
//
// Some routing rules exist to catch changes to this repository's own
// distribution -- its version marker, its agent briefs. Those rules travel
// inside the packaged plugin, where they are evaluated against *a consumer's*
// files.
//
// So the rule set has to be specific enough that a consumer project does not
// trip it. A path like pyproject.toml is packaging in any Python project;
// routing it would hand a consumer cadre's governance reviewers for a change
// that has nothing to do with cadre. The positive case proves the rule still
// fires; the negative cases prove it does not fire on ordinary packaging.
//
// Both are checked through the packaged wrapper in an unrelated git
// repository, because that is the only place the question is real -- the rules
// behave differently when the selector is standing inside cadre itself.
//
// Ported from test_repository_health.py's
// test_packaged_selector_routes_cadre_specific_packaging_paths_only.

func matchedRouteIDs(t *testing.T, plan map[string]any) []string {
	t.Helper()
	routes, ok := plan["matched_routes"].([]any)
	if !ok {
		return nil
	}
	var ids []string
	for _, entry := range routes {
		route, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := route["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func TestThePackagedSelectorStillRoutesCadresOwnVersionMarker(t *testing.T) {
	// VERSION is the distribution's version marker. It was cadre_cli/_version.py
	// until the Python elimination made it a plain text file, and the route had
	// to follow it -- otherwise a version bump silently stops reaching the
	// governance reviewers.
	wrapper, binary := packagedWrapper(t)
	consumer, _ := consumerRepository(t)

	plan := runPackagedSelect(t, wrapper, binary, consumer,
		"--task", "Inspect selected file",
		"--files", "VERSION",
		"--classification", "internal",
		"--task-id", "PACKAGED-ROUTING-POSITIVE")

	ids := matchedRouteIDs(t, plan)
	if len(ids) != 1 || ids[0] != "agent-suite-governance" {
		t.Fatalf("matched routes = %v, want [agent-suite-governance]", ids)
	}

	// The reason names the pattern and the file, so a reader can tell *why* it
	// fired rather than being told that it did.
	routes, _ := plan["matched_routes"].([]any)
	route, _ := routes[0].(map[string]any)
	reasons, _ := route["reasons"].(map[string]any)
	paths, _ := reasons["paths"].([]any)
	if len(paths) != 1 {
		t.Fatalf("the match reason names %d paths, want 1: %v", len(paths), reasons)
	}
	reason, _ := paths[0].(map[string]any)
	if reason["pattern"] != "VERSION" || reason["file"] != "VERSION" {
		t.Errorf("the match reason is %v, want pattern and file both VERSION", reason)
	}

	if status, _ := plan["status"].(string); status == "needs-triage" {
		t.Error("a path the rules cover came back needs-triage")
	}
}

func TestThePackagedSelectorDoesNotRouteOrdinaryPackagingFiles(t *testing.T) {
	// The half that protects consumers. Each of these is packaging in any
	// Python project; routing them would hand a consumer cadre's own
	// governance roles for a change that has nothing to do with cadre.
	//
	// needs-triage is the right answer, not a fallback: when no rule matches,
	// the selector says so rather than guessing.
	wrapper, binary := packagedWrapper(t)
	consumer, _ := consumerRepository(t)

	for _, path := range []string{
		"pyproject.toml",
		"kernel/pyproject.toml",
		"engine/pyproject.toml",
	} {
		t.Run(path, func(t *testing.T) {
			plan := runPackagedSelect(t, wrapper, binary, consumer,
				"--task", "Inspect selected file",
				"--files", path,
				"--classification", "internal",
				"--task-id", "PACKAGED-ROUTING-NEGATIVE")

			if ids := matchedRouteIDs(t, plan); len(ids) != 0 {
				t.Errorf("%s matched %v in a consumer project.\n"+
					"These rules ship inside the plugin and are evaluated against "+
					"the consumer's files; a rule broad enough to catch ordinary "+
					"packaging hands them cadre's reviewers for unrelated work.",
					path, ids)
			}
			for _, field := range []string{"status", "workflow"} {
				if value, _ := plan[field].(string); value != "needs-triage" {
					t.Errorf("%s = %q, want needs-triage", field, value)
				}
			}
		})
	}
}

func TestTheRoutingProbeDistinguishesItsTwoOutcomes(t *testing.T) {
	// Guards the pair. Both tests above would pass over a selector that
	// returned the same thing for everything -- one asserts a match, the other
	// asserts none, so they only mean something together.
	//
	// Stated here so the pairing is a check rather than a coincidence of two
	// files being next to each other.
	wrapper, binary := packagedWrapper(t)
	consumer, _ := consumerRepository(t)

	routed := runPackagedSelect(t, wrapper, binary, consumer,
		"--task", "Inspect selected file", "--files", "VERSION",
		"--classification", "internal", "--task-id", "PACKAGED-ROUTING-PAIR-A")
	unrouted := runPackagedSelect(t, wrapper, binary, consumer,
		"--task", "Inspect selected file", "--files", "pyproject.toml",
		"--classification", "internal", "--task-id", "PACKAGED-ROUTING-PAIR-B")

	routedIDs := strings.Join(matchedRouteIDs(t, routed), ",")
	unroutedIDs := strings.Join(matchedRouteIDs(t, unrouted), ",")
	if routedIDs == unroutedIDs {
		t.Fatalf("both inputs produced the same routes (%q); the selector is not "+
			"distinguishing them, so neither test above means anything", routedIDs)
	}
}
