package orchestration

import (
	"testing"

	"github.com/deagy/cadre/cli/internal/selector"
)

func TestGlobContainsIdenticalPatternIsContained(t *testing.T) {
	verdict := GlobContains("**/*.txt", []string{"**/*.txt"})
	if verdict != Contained {
		t.Fatalf("verdict = %q, want %q", verdict, Contained)
	}
}

func TestGlobContainsRosterStarStarNotShadowedByTxtExclude(t *testing.T) {
	// The exact false-positive case from the Python original's docstring:
	// paths: ["roster/**"], exclude_paths: ["**/*.txt"] must NOT be
	// reported as fully shadowed -- roster/main.go matches the include and
	// no exclude.
	verdict, witness := GlobContainsWithWitness("roster/**", []string{"**/*.txt"})
	if verdict != NotContained {
		t.Fatalf("verdict = %q, want %q (this is the motivating false-positive regression case)", verdict, NotContained)
	}
	if witness == "" {
		t.Fatal("expected a non-empty witness for NotContained")
	}
	assertWitnessIsValid(t, witness, "roster/**", []string{"**/*.txt"})
}

func TestGlobContainsNoExcludes(t *testing.T) {
	verdict, witness := GlobContainsWithWitness("src/**/*.go", nil)
	if verdict != NotContained {
		t.Fatalf("verdict = %q, want %q", verdict, NotContained)
	}
	if witness == "" {
		t.Fatal("expected a non-empty witness with no excludes at all")
	}
	assertWitnessIsValid(t, witness, "src/**/*.go", nil)
}

func TestGlobContainsCharacterClassIsUndetermined(t *testing.T) {
	verdict := GlobContains("src/[ab].go", []string{"src/*.go"})
	if verdict != Undetermined {
		t.Fatalf("verdict = %q, want %q for a character-class pattern", verdict, Undetermined)
	}
}

func TestGlobContainsExcludeCharacterClassIsUndetermined(t *testing.T) {
	verdict := GlobContains("src/*.go", []string{"src/[ab].go"})
	if verdict != Undetermined {
		t.Fatalf("verdict = %q, want %q for a character-class exclude", verdict, Undetermined)
	}
}

func TestGlobContainsExactLiteralFullyShadowed(t *testing.T) {
	verdict := GlobContains("README.md", []string{"README.md", "OTHER.md"})
	if verdict != Contained {
		t.Fatalf("verdict = %q, want %q", verdict, Contained)
	}
}

func TestGlobContainsFoldsCaseLikeTheMatcher(t *testing.T) {
	// This asserted the opposite, on the strength of a comment pointing at
	// this file's own globToRegex -- which has no callers outside tests. The
	// matcher that decides whether a route's paths fire is
	// selector.GlobToRegex, and it sets (?i).
	//
	// The consequence was not academic. A rule with paths **/README.md and
	// exclude_paths **/readme.md has its include swallowed whole by the
	// exclude at runtime: it keeps its reviewers and its human_gate and
	// matches on keywords alone. The analyzer said not-contained, so
	// TestNoRuleExcludesAwayItsOwnPathCoverage -- which exists to catch
	// exactly that -- stayed silent.
	verdict := GlobContains("README.md", []string{"readme.md"})
	if verdict != Contained {
		t.Errorf("verdict = %q, want %q. The live matcher is case-insensitive, "+
			"so an analyzer that is not answers a question nobody asks.",
			verdict, Contained)
	}
}

func TestCaseFoldingDoesNotCollapseUnrelatedLiterals(t *testing.T) {
	// The check above is satisfied vacuously by an analyzer whose alphabet has
	// desynced from its literals -- everything becomes Contained. This is what
	// pins the folding rather than the collapse.
	for _, testCase := range []struct{ include, exclude string }{
		{"Foo/**", "bar/**"},
		{"A/**", "b/**"},
		{"src/Main.go", "src/other.go"},
	} {
		if verdict := GlobContains(testCase.include, []string{testCase.exclude}); verdict != NotContained {
			t.Errorf("GlobContains(%q, [%q]) = %q, want %q -- unrelated literals "+
				"must stay unrelated after folding",
				testCase.include, testCase.exclude, verdict, NotContained)
		}
	}
}

func TestGlobContainsQuestionMark(t *testing.T) {
	// "src/?.go" ⊆ "src/*.go" -- ? is one non-separator char, a subset of
	// [^/]* -- so fully contained.
	verdict := GlobContains("src/?.go", []string{"src/*.go"})
	if verdict != Contained {
		t.Fatalf("verdict = %q, want %q", verdict, Contained)
	}
}

func TestGlobContainsStarNotContainedByNarrowerExclude(t *testing.T) {
	verdict, witness := GlobContainsWithWitness("src/*.go", []string{"src/main.go"})
	if verdict != NotContained {
		t.Fatalf("verdict = %q, want %q", verdict, NotContained)
	}
	assertWitnessIsValid(t, witness, "src/*.go", []string{"src/main.go"})
}

func TestGlobContainsMultipleExcludesUnionCovers(t *testing.T) {
	// "src/*.go" is contained in the union of {"src/main.go", "src/*.go"}
	// (the second exclude alone already covers everything).
	verdict := GlobContains("src/*.go", []string{"src/main.go", "src/*.go"})
	if verdict != Contained {
		t.Fatalf("verdict = %q, want %q", verdict, Contained)
	}
}

func TestGlobContainsDoubleStarSlashSemantics(t *testing.T) {
	// "**/*.go" ⊆ "**/*.go" (identical).
	if verdict := GlobContains("**/*.go", []string{"**/*.go"}); verdict != Contained {
		t.Errorf("identical **/ pattern verdict = %q, want %q", verdict, Contained)
	}
	// "internal/**/*.go" is NOT contained in "internal/cli/**/*.go" (a
	// narrower prefix) -- internal/orchestration/x.go matches the include
	// and not the exclude.
	verdict, witness := GlobContainsWithWitness("internal/**/*.go", []string{"internal/cli/**/*.go"})
	if verdict != NotContained {
		t.Fatalf("verdict = %q, want %q", verdict, NotContained)
	}
	assertWitnessIsValid(t, witness, "internal/**/*.go", []string{"internal/cli/**/*.go"})
}

func TestCheckRouteExcludeShadowing(t *testing.T) {
	route := Route{
		ID:           "example",
		Paths:        []string{"roster/**", "README.md"},
		ExcludePaths: []string{"README.md"},
	}
	results := CheckRouteExcludeShadowing(route)
	if results["README.md"] != Contained {
		t.Errorf("README.md verdict = %q, want %q (fully shadowed by its own exclude)", results["README.md"], Contained)
	}
	if results["roster/**"] != NotContained {
		t.Errorf("roster/** verdict = %q, want %q (README.md exclude does not shadow roster/**)", results["roster/**"], NotContained)
	}
}

func TestCheckRouteExcludeShadowingNoExcludes(t *testing.T) {
	route := Route{ID: "example", Paths: []string{"src/**"}}
	results := CheckRouteExcludeShadowing(route)
	if results["src/**"] != NotContained {
		t.Errorf("verdict = %q, want %q with no exclude_paths at all", results["src/**"], NotContained)
	}
}

// assertWitnessIsValid independently checks a NotContained witness against
// the real route_matching.go globToRegex: the witness must match include's
// compiled regex and must not match any exclude's compiled regex. This is
// exactly the verification property the Python original's design docstring
// calls out -- a NOT_CONTAINED verdict can be checked, not just trusted.
func assertWitnessIsValid(t *testing.T, witness, include string, excludes []string) {
	t.Helper()
	includeRe := selector.GlobToRegex(include)
	if !includeRe.MatchString(witness) {
		t.Fatalf("witness %q does not match include pattern %q (regex %s)", witness, include, includeRe.String())
	}
	for _, exclude := range excludes {
		excludeRe := selector.GlobToRegex(exclude)
		if excludeRe.MatchString(witness) {
			t.Fatalf("witness %q unexpectedly matches exclude pattern %q (regex %s)", witness, exclude, excludeRe.String())
		}
	}
}
