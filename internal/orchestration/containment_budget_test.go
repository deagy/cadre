package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// What the containment search does when it runs out of room, and whether that
// ever happens to a real pattern.
//
// GlobContains bounds the product states it explores. Reaching the bound has
// to yield Undetermined, because the only caller reports on Contained: a
// pattern the analyzer declines to decide is a finding nobody makes, while a
// pattern it guesses Contained is a correct routing.json failing its own
// check.
//
// Both halves matter and neither was asserted. The bound was a const, so the
// behaviour at it was untestable, and the shadowing linter counted the globs
// it looked at without ever checking that any of them came back decided --
// 46 Undetermined verdicts would have read exactly like 46 clean ones.

func TestExhaustingTheBudgetYieldsUndeterminedNeverContained(t *testing.T) {
	original := maxContainmentProductStates
	t.Cleanup(func() { maxContainmentProductStates = original })

	// A pattern pair whose product is comfortably larger than one state.
	include := "**/a/**/b/**/c.md"
	excludes := []string{"x/**", "y/**"}

	// Decided at the real bound, so the case below is about the budget and not
	// about a pattern this engine cannot handle anyway.
	if verdict := GlobContains(include, excludes); verdict == Undetermined {
		t.Fatalf("%q is Undetermined at the real bound; this case cannot show "+
			"anything about budget exhaustion", include)
	}

	maxContainmentProductStates = 1
	if verdict := GlobContains(include, excludes); verdict != Undetermined {
		t.Errorf("verdict = %q at a budget of one state, want %q. Contained is the "+
			"dangerous answer here -- the caller reports on it, so a guess becomes "+
			"a correct routing.json failing its own check.", verdict, Undetermined)
	}
}

func TestABudgetedRunNeverInventsAWitness(t *testing.T) {
	// A witness is a claim that a concrete path escapes every exclude. A run
	// that gave up should not be producing one, and a caller that logged it
	// would be reporting a counterexample the analyzer never actually found.
	original := maxContainmentProductStates
	t.Cleanup(func() { maxContainmentProductStates = original })

	maxContainmentProductStates = 1
	verdict, witness := GlobContainsWithWitness("**/a/**/b/**/c.md", []string{"x/**", "y/**"})
	if verdict == Undetermined && witness != "" {
		t.Errorf("Undetermined carried witness %q; the search gave up without "+
			"deciding, so it has no counterexample to offer", witness)
	}
}

// routingExcludePairs yields every (include, excludes) pair the shadowing
// linter actually evaluates, from the shipped routing.json.
func routingExcludePairs(t *testing.T) []struct {
	rule     string
	include  string
	excludes []string
} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(checkoutRoot(t), "roster", "orchestration", "routing.json"))
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("routing.json does not parse: %v", err)
	}
	var pairs []struct {
		rule     string
		include  string
		excludes []string
	}
	for _, section := range []string{"routes", "risk_rules"} {
		for _, rule := range objectListOf(document[section]) {
			excludes := stringsAt(rule["exclude_paths"])
			if len(excludes) == 0 {
				continue
			}
			id, _ := rule["id"].(string)
			for _, include := range stringsAt(rule["paths"]) {
				pairs = append(pairs, struct {
					rule     string
					include  string
					excludes []string
				}{section + "/" + id, include, excludes})
			}
		}
	}
	return pairs
}

func TestEveryRealRoutingPatternIsActuallyDecided(t *testing.T) {
	// The shipped patterns must stay far enough inside the budget that the
	// analyzer reaches a verdict on all of them. An Undetermined here is not a
	// failure of routing.json -- it is the linter going quiet about one rule,
	// which reads identically to that rule being fine.
	pairs := routingExcludePairs(t)
	if len(pairs) == 0 {
		t.Skip("no rule declares both paths and exclude_paths")
	}

	var undecided []string
	tally := map[string]int{}
	for _, pair := range pairs {
		verdict := GlobContains(pair.include, pair.excludes)
		tally[verdict]++
		if verdict == Undetermined {
			undecided = append(undecided, pair.rule+": "+pair.include)
		}
	}
	sort.Strings(undecided)
	if len(undecided) > 0 {
		t.Errorf("%d of %d real include globs could not be decided: %v\n"+
			"The shadowing check reports only on Contained, so each of these is a "+
			"rule it silently skips.", len(undecided), len(pairs), undecided)
	}
	t.Logf("%d include globs, verdicts: %v", len(pairs), tally)
}
