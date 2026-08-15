package selector

import (
	"encoding/json"
	"strings"
	"testing"
)

// These pin what probe_overlay_parity.py measured against Python across 69
// overlay documents (24 accepted, 45 refused). The probe is the evidence;
// these keep the rules that carry gating semantics from quietly relaxing.

func overlayFrom(t *testing.T, text string) map[string]any {
	t.Helper()
	var loaded any
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if err := decoder.Decode(&loaded); err != nil {
		t.Fatal(err)
	}
	object, ok := loaded.(map[string]any)
	if !ok {
		t.Fatalf("overlay is not an object: %s", text)
	}
	return object
}

func baseFrom(t *testing.T, text string) map[string]any {
	t.Helper()
	var base map[string]any
	if err := json.Unmarshal([]byte(text), &base); err != nil {
		t.Fatal(err)
	}
	return base
}

const minimalBase = `{
  "version": 1,
  "routes": [{"id": "backend", "keywords": ["api", "handler"], "paths": ["src/**"],
              "primary": ["backend-engineer"], "reviewers": ["code-reviewer"], "human_gate": true}],
  "risk_rules": [],
  "team_recipes": [{"id": "review-team", "type": "fixed", "route_ids": ["backend"],
                    "minimum_matches": 2, "members": ["code-reviewer", "security-reviewer"],
                    "communication_mode": "peer", "fallback": "orchestrator-relayed",
                    "description": "d"}],
  "change_intake": {"keywords": ["add"], "agents": ["product-intent-agent"]},
  "cross_stack": {"route_ids": ["backend"], "support": ["observability-sre"], "minimum_matches": 2},
  "knowledge_focus": {"backend-engineer": "prior defects"},
  "ignored_gates": ["G7"]
}`

func mergeForTest(t *testing.T, overlayText string) (map[string]any, error) {
	t.Helper()
	return MergeRouting(baseFrom(t, minimalBase), overlayFrom(t, overlayText))
}

func TestOverlayMayWidenButNeverNarrowAMatchingCondition(t *testing.T) {
	// Narrowing a base route's matching conditions has the same effect as
	// deleting its reviewers -- the route stops firing on changes it used to
	// cover -- so it is refused just as firmly, and refused whether or not
	// this entry declares a human_gate at all.
	merged, err := mergeForTest(t, `{"routes": [{"id": "backend", "keywords": ["api", "handler", "rpc"]}]}`)
	if err != nil {
		t.Fatalf("appending a keyword must be permitted: %v", err)
	}
	route := objectList(merged["routes"])[0]
	if len(anyList(route["keywords"])) != 3 {
		t.Errorf("keywords = %v, want the widened list", route["keywords"])
	}
	// The rest of the base entry survives the patch untouched.
	if len(anyList(route["primary"])) != 1 || route["human_gate"] != true {
		t.Errorf("widening dropped base fields: %v", route)
	}

	for _, overlay := range []string{
		`{"routes": [{"id": "backend", "keywords": ["api"]}]}`,
		`{"routes": [{"id": "backend", "keywords": []}]}`,
		`{"routes": [{"id": "backend", "keywords": ["something-else"]}]}`,
		`{"routes": [{"id": "backend", "paths": []}]}`,
	} {
		if _, err := mergeForTest(t, overlay); err == nil {
			t.Errorf("narrowing must be refused: %s", overlay)
		}
	}
}

func TestOverlayMayNotTouchTheFieldsThatDecideReview(t *testing.T) {
	// This is what the mechanism is for. An overlay that could edit these
	// would be a way for a project to review its own high-risk changes.
	for _, overlay := range []string{
		`{"routes": [{"id": "backend", "reviewers": []}]}`,
		`{"routes": [{"id": "backend", "human_gate": false}]}`,
		`{"routes": [{"id": "backend", "primary": ["someone-else"]}]}`,
		// exclude_paths is not a widen field, and its polarity is inverted:
		// a superset of it *narrows* the effective match, so treating it like
		// keywords would enforce exactly the wrong direction.
		`{"routes": [{"id": "backend", "exclude_paths": ["src/vendor/**"]}]}`,
	} {
		if _, err := mergeForTest(t, overlay); err == nil {
			t.Errorf("must be refused: %s", overlay)
		}
	}

	// Restating a field with its exact base value is a permitted no-op, so an
	// overlay author may include context around the field being widened.
	if _, err := mergeForTest(t, `{"routes": [{"id": "backend", "human_gate": true,
	    "reviewers": ["code-reviewer"], "keywords": ["api", "handler", "rpc"]}]}`); err != nil {
		t.Errorf("an exact restatement must be permitted: %v", err)
	}
}

func TestKeywordGroupsWidenInTheOppositeDirectionFromAFlatList(t *testing.T) {
	// keyword_groups is an AND-of-ORs. Appending an outer group adds a
	// mandatory condition and so *narrows* matching, even though a plain
	// superset check reads it as additive -- the single place where the
	// obvious implementation is wrong in the dangerous direction.
	base := baseFrom(t, `{"version": 1, "risk_rules": [], "routes": [
	    {"id": "backend", "keyword_groups": [["deploy", "release"], ["prod", "production"]]}]}`)

	widened := overlayFrom(t, `{"routes": [{"id": "backend",
	    "keyword_groups": [["deploy", "release", "ship"], ["prod", "production"]]}]}`)
	if _, err := MergeRouting(base, widened); err != nil {
		t.Errorf("adding a keyword to an existing group is the one real widen: %v", err)
	}

	for _, overlay := range []string{
		// Looks additive. Adds an AND-condition. Narrows.
		`{"routes": [{"id": "backend", "keyword_groups": [["deploy", "release"], ["prod", "production"], ["urgent"]]}]}`,
		// Dropping an outer group changes which combinations remain reachable.
		`{"routes": [{"id": "backend", "keyword_groups": [["deploy", "release"]]}]}`,
		// Removing a keyword from an inner OR-list narrows that clause.
		`{"routes": [{"id": "backend", "keyword_groups": [["deploy"], ["prod", "production"]]}]}`,
		`{"routes": [{"id": "backend", "keyword_groups": ["not-a-list", ["prod", "production"]]}]}`,
	} {
		if _, err := MergeRouting(base, overlayFrom(t, overlay)); err == nil {
			t.Errorf("must be refused: %s", overlay)
		}
	}
}

func TestOverlayMayAddEntriesButNotCollideAcrossSections(t *testing.T) {
	// Routes, risk rules and team recipes share one id namespace, because a
	// plan puts their ids side by side.
	if _, err := mergeForTest(t, `{"routes": [{"id": "frontend", "keywords": ["ui"]}]}`); err != nil {
		t.Errorf("a fresh id must be addable: %v", err)
	}
	for _, overlay := range []string{
		`{"risk_rules": [{"id": "backend", "keywords": ["x"]}]}`,
		`{"team_recipes": [{"id": "backend", "type": "fixed"}]}`,
		`{"routes": [{"id": "review-team", "keywords": ["x"]}]}`,
	} {
		if _, err := mergeForTest(t, overlay); err == nil {
			t.Errorf("a colliding id must be refused: %s", overlay)
		}
	}
}

func TestBaseTeamRecipesAreFullyImmutable(t *testing.T) {
	// No widen exception at all: a team recipe names who collaborates on a
	// change, so there is no field an overlay has business adjusting.
	if _, err := mergeForTest(t, `{"team_recipes": [{"id": "review-team", "description": "changed"}]}`); err == nil {
		t.Error("modifying a base team recipe must be refused")
	}
	if _, err := mergeForTest(t, `{"team_recipes": [{"id": "new-team", "type": "fixed"}]}`); err != nil {
		t.Errorf("adding a new team recipe must be permitted: %v", err)
	}
}

func TestIgnoredGatesMayOnlyShrink(t *testing.T) {
	// Each entry suppresses a gate, so growth is how an overlay would switch
	// off a check the base insists on.
	merged, err := mergeForTest(t, `{"ignored_gates": []}`)
	if err != nil {
		t.Fatalf("removing a suppression must be permitted: %v", err)
	}
	if len(anyList(merged["ignored_gates"])) != 0 {
		t.Errorf("ignored_gates = %v, want the shrunk list", merged["ignored_gates"])
	}
	if _, err := mergeForTest(t, `{"ignored_gates": ["G7", "G9"]}`); err == nil {
		t.Error("adding a suppression must be refused")
	}
}

func TestCrossStackMinimumMatchesMayOnlyDecrease(t *testing.T) {
	// Raising it means more routes must match before cross-stack support
	// engages, which reduces coverage.
	if _, err := mergeForTest(t, `{"cross_stack": {"minimum_matches": 1}}`); err != nil {
		t.Errorf("decreasing must be permitted: %v", err)
	}
	if _, err := mergeForTest(t, `{"cross_stack": {"minimum_matches": 3}}`); err == nil {
		t.Error("increasing must be refused")
	}
	// Python's json decoder yields int for `2` and float for `2.0`, and only
	// the first satisfies its isinstance check. Go sees float64 for both
	// unless the overlay is decoded with json.Number -- so this asserts the
	// decoder choice, not just the comparison.
	for _, overlay := range []string{
		`{"cross_stack": {"minimum_matches": 1.0}}`,
		`{"cross_stack": {"minimum_matches": "1"}}`,
		`{"cross_stack": {"minimum_matches": true}}`,
	} {
		if _, err := mergeForTest(t, overlay); err == nil {
			t.Errorf("a non-integer minimum_matches must be refused: %s", overlay)
		}
	}
}

func TestAdditiveSectionsAppendWithoutDuplicating(t *testing.T) {
	merged, err := mergeForTest(t, `{"change_intake": {"keywords": ["add", "introduce"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	intake := objectOf(merged["change_intake"])
	if got := anyList(intake["keywords"]); len(got) != 2 {
		t.Errorf("keywords = %v, want the already-present value not duplicated", got)
	}
	// An untouched additive field keeps its base value.
	if got := anyList(intake["agents"]); len(got) != 1 {
		t.Errorf("agents = %v, want the base value preserved", got)
	}

	for _, overlay := range []string{
		`{"change_intake": {"nonsense": []}}`,
		`{"change_intake": {"keywords": "add"}}`,
		`{"cross_stack": {"nonsense": 1}}`,
	} {
		if _, err := mergeForTest(t, overlay); err == nil {
			t.Errorf("must be refused: %s", overlay)
		}
	}
}

func TestKnowledgeFocusIsAnOrdinaryDeepMerge(t *testing.T) {
	// The one section with no narrowing rule, because it carries no gating,
	// dispatch or review-separation semantics -- overlay simply wins.
	merged, err := mergeForTest(t, `{"knowledge_focus": {"backend-engineer": "replaced", "new-agent": "focus"}}`)
	if err != nil {
		t.Fatal(err)
	}
	focus := objectOf(merged["knowledge_focus"])
	if focus["backend-engineer"] != "replaced" || focus["new-agent"] != "focus" {
		t.Errorf("knowledge_focus = %v, want overlay to win per key", focus)
	}
}

func TestVersionIsAContractFieldNotADial(t *testing.T) {
	if _, err := mergeForTest(t, `{"version": 1}`); err != nil {
		t.Errorf("restating the version must be a permitted no-op: %v", err)
	}
	if _, err := mergeForTest(t, `{"version": 2}`); err == nil {
		t.Error("changing the version must be refused")
	}
}

func TestOverlayRejectsUnrecognizedTopLevelFields(t *testing.T) {
	// A typo'd section name would otherwise be silently ignored, leaving the
	// author believing a customisation is in force when it is not.
	_, err := mergeForTest(t, `{"zebra": 1, "alpha": 2}`)
	if err == nil {
		t.Fatal("an unrecognized top-level field must be refused")
	}
	if !strings.Contains(err.Error(), "['alpha', 'zebra']") {
		t.Errorf("error = %q, want the unknown fields listed in sorted order", err)
	}
}

func TestEffectiveConfigIsValidatedNotJustTheEdit(t *testing.T) {
	// A merge can obey every rule and still produce a config the selector
	// cannot run, so validation happens on the result.
	base := baseFrom(t, minimalBase)
	overlay := overlayFrom(t, `{"routes": [{"id": "frontend", "keywords": ["ui"], "workflow_shape": "not-a-shape"}]}`)

	merged, err := MergeRouting(base, overlay)
	if err != nil {
		t.Fatalf("the merge itself is legal: %v", err)
	}
	if err := ValidateRoutingConfig(merged); err == nil {
		t.Error("a misspelled workflow_shape must fail validation of the effective config")
	}

	// Omitting workflow_shape entirely is permitted -- it contributes no
	// delivery shape and is reported in the plan rather than rejected, so
	// that existing overlays adding a route keep working.
	merged, err = MergeRouting(base, overlayFrom(t, `{"routes": [{"id": "frontend", "keywords": ["ui"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRoutingConfig(merged); err != nil {
		t.Errorf("a route without workflow_shape must validate: %v", err)
	}
}

func TestResolveEffectiveRoutingReturnsTheBaseUntouchedWithNoOverlay(t *testing.T) {
	base := baseFrom(t, minimalBase)
	resolved, err := ResolveEffectiveRouting(base, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(objectList(resolved["routes"])) != 1 {
		t.Errorf("routes = %v, want the base returned as-is", resolved["routes"])
	}
}
