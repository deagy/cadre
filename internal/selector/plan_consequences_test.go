package selector

import (
	"path/filepath"
	"testing"
)

// Properties of the assembled plan, as distinct from the units that build it.
//
// The Go suite tests the mechanisms well: BuildContextPacks enforces
// classification containment, MatchRule subtracts exclude_paths per file. What
// it does not test is the *consequence* -- what the plan a caller receives
// looks like when those mechanisms interact.
//
// That distinction is the whole of the gap found by auditing
// roster/orchestration/test/test_selector.py against this package. A unit test
// answers "does the filter work?"; these answer "does using the filter change
// anything it was not supposed to?", which is the question a caller actually
// has and the one a refactor is most likely to get wrong.

func planWith(t *testing.T, input PlanInput) map[string]any {
	t.Helper()
	plan, err := BuildDispatchPlan(loadRoutingConfig(t), input, PlanOptions{
		Catalog:    loadCatalogIDs(t),
		RosterRoot: filepath.Join(selectorRepoRoot(t), "roster"),
	})
	if err != nil {
		t.Fatalf("BuildDispatchPlan: %v", err)
	}
	return plan
}

// planPackIDs reads the plan's context packs.
//
// The field is a typed []ContextPack, not []any. Reading it as []any yields
// nil from a failed assertion, which is indistinguishable from "no packs were
// emitted" -- and "no packs" is exactly what the withholding case below
// expects, so getting this wrong makes the test pass while checking nothing.
func planPackIDs(t *testing.T, plan map[string]any) []string {
	t.Helper()
	packs, ok := plan["context_packs"].([]ContextPack)
	if !ok {
		if plan["context_packs"] == nil {
			return nil
		}
		t.Fatalf("context_packs is %T, not []ContextPack; a reader expecting "+
			"[]any here would report an empty list for a full one",
			plan["context_packs"])
	}
	ids := make([]string, 0, len(packs))
	for _, pack := range packs {
		ids = append(ids, pack.ID)
	}
	return ids
}

func TestALowerClassificationWithholdsThePackAndNothingElse(t *testing.T) {
	// An internal-classified pack must not ride along in a plan the caller
	// asserted as public -- that much BuildContextPacks already enforces, and
	// stage3_test.go already checks.
	//
	// What is untested is the other half: *only* the pack is withheld. Routing
	// and staffing have to be identical to the internal run. If they were not,
	// a caller could change who gets dispatched by relabelling the
	// classification -- silently, and in the direction of claiming less
	// sensitivity rather than more, which is the direction nobody audits.
	arguments := PlanInput{
		Task:              "Integrate Toshiba Q-KMS with our QKD gateway",
		TaskID:            "PACK-1",
		RepositoryRoot:    "<REPO_ROOT>",
		ChangedFileSource: "explicit",
	}

	internalArgs, publicArgs := arguments, arguments
	internalArgs.Classification = "internal"
	publicArgs.Classification = "public"

	internal := planWith(t, internalArgs)
	public := planWith(t, publicArgs)

	if got := planPackIDs(t, internal); len(got) != 1 || got[0] != "toshiba-qkms-context" {
		t.Fatalf("the internal run emitted %v; without a pack to withhold this "+
			"test proves nothing", got)
	}
	if got := planPackIDs(t, public); len(got) != 0 {
		t.Errorf("an internal pack rode along in a public plan: %v", got)
	}

	// And everything else is the same decision.
	if got, want := joinIDs(planRouteIDs(public)), joinIDs(planRouteIDs(internal)); got != want {
		t.Errorf("the downgrade changed which routes matched.\ninternal: %s\npublic:   %s",
			want, got)
	}
	internalAgents, publicAgents := planAgents(t, internal), planAgents(t, public)
	for _, group := range []struct {
		name             string
		public, internal []string
	}{
		{"primary", publicAgents.Primary, internalAgents.Primary},
		{"reviewers", publicAgents.Reviewers, internalAgents.Reviewers},
		{"support", publicAgents.Support, internalAgents.Support},
	} {
		if joinIDs(group.public) != joinIDs(group.internal) {
			t.Errorf("the downgrade changed %s.\ninternal: %v\npublic:   %v",
				group.name, group.internal, group.public)
		}
	}
}

func TestAnUnassertedClassificationWithholdsTheSamePack(t *testing.T) {
	// Fails closed: an absent classification is not read as "public enough for
	// an internal pack". The distinction matters because omitting the flag is
	// the easy path, and treating it as permissive would make the containment
	// control opt-in.
	plan := planWith(t, PlanInput{
		Task:              "Integrate Toshiba Q-KMS with our QKD gateway",
		TaskID:            "PACK-2",
		RepositoryRoot:    "<REPO_ROOT>",
		ChangedFileSource: "explicit",
	})
	if got := planPackIDs(t, plan); len(got) != 0 {
		t.Errorf("a pack was emitted with no classification asserted: %v", got)
	}
}

func joinIDs(values []string) string {
	out := ""
	for index, value := range values {
		if index > 0 {
			out += ","
		}
		out += value
	}
	return out
}

// The exclude_paths cases match_test.go leaves.
//
// It covers three of the seven the Python suite has: an excluded file not
// matching, a non-excluded file still matching, and the subtraction being
// per-file rather than per-rule. The four below are the ones where a plausible
// implementation passes those three and still gets it wrong.

func TestEveryPatternInAnExcludeListIsApplied(t *testing.T) {
	// A loop that returns on its first match applies only the first pattern.
	// With one pattern in the list -- which is what the existing tests use --
	// that bug is invisible.
	rule := map[string]any{
		"paths": []any{"**/*.py"},
		"exclude_paths": []any{
			"**/test/**", "**/vendor/**", "**/generated/**",
		},
	}
	for _, excluded := range []string{
		"roster/test/thing.py", "roster/vendor/thing.py", "roster/generated/thing.py",
	} {
		if MatchRule(rule, "", []string{excluded}).Matched {
			t.Errorf("%q was not excluded; only some patterns in the list are applied",
				excluded)
		}
	}
	if !MatchRule(rule, "", []string{"roster/src/thing.py"}).Matched {
		t.Error("a file matched by no exclude pattern must still match")
	}
}

func TestAnExcludePatternMatchingNothingIsANoOp(t *testing.T) {
	// The common case in a real config: most rules carry exclusions that do
	// not apply to most changes. An exclusion list that suppressed a match
	// merely by being present would break far more than it filtered.
	withExclusion := map[string]any{
		"paths":         []any{"**/*.py"},
		"exclude_paths": []any{"**/nothing-here/**"},
	}
	without := map[string]any{"paths": []any{"**/*.py"}}

	files := []string{"roster/src/thing.py", "roster/src/other.py"}
	got, want := MatchRule(withExclusion, "", files), MatchRule(without, "", files)
	if !got.Matched {
		t.Fatal("an exclusion matching nothing suppressed the match")
	}
	if len(got.Paths) != len(want.Paths) {
		t.Errorf("an exclusion matching nothing changed the reported paths: %v vs %v",
			got.Paths, want.Paths)
	}
}

func TestAnExcludeThatShadowsItsWholeIncludeMatchesNothing(t *testing.T) {
	// A rule whose exclusion covers its own include is dead: it can never
	// fire. Worth pinning as behaviour rather than left to chance, because the
	// alternative -- treating a fully-shadowed exclusion as a mistake and
	// ignoring it -- would silently re-enable a rule somebody disabled this
	// way on purpose.
	rule := map[string]any{
		"paths":         []any{"roster/**"},
		"exclude_paths": []any{"roster/**"},
	}
	for _, file := range []string{
		"roster/catalog.yaml", "roster/orchestration/routing.json", "roster/a/b/c/deep.md",
	} {
		if MatchRule(rule, "", []string{file}).Matched {
			t.Errorf("%q matched a rule whose exclusion shadows its whole include", file)
		}
	}
}

func TestAbsentExcludePathsChangesNothing(t *testing.T) {
	// Most rules have no exclude_paths at all. The regression this guards is a
	// reader that treats a missing key as an empty pattern, or as a pattern
	// matching everything -- the second of which silently disables every rule
	// in the file.
	rule := map[string]any{"paths": []any{"**/*.go", "docs/**"}}
	files := []string{"internal/selector/plan.go", "docs/api.md", "README.md"}

	got := MatchRule(rule, "", files)
	if !got.Matched {
		t.Fatal("a rule with no exclude_paths stopped matching")
	}
	if len(got.Paths) != 2 {
		t.Errorf("reported %d matched paths, want the two that match: %v",
			len(got.Paths), got.Paths)
	}
	// Explicitly empty is the same as absent, since a config generator may
	// emit the key with nothing in it.
	withEmpty := map[string]any{
		"paths": []any{"**/*.go", "docs/**"}, "exclude_paths": []any{},
	}
	if empty := MatchRule(withEmpty, "", files); len(empty.Paths) != len(got.Paths) {
		t.Errorf("an empty exclude list behaved differently from an absent one: %v vs %v",
			empty.Paths, got.Paths)
	}
}
