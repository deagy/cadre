package orchestration

import "testing"

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

func TestGlobContainsCaseSensitive(t *testing.T) {
	// route_matching.go's globToRegex never sets an IGNORECASE-equivalent
	// flag -- this must be case-sensitive, unlike the Python original.
	verdict, witness := GlobContainsWithWitness("README.md", []string{"readme.md"})
	if verdict != NotContained {
		t.Fatalf("verdict = %q, want %q -- containment must be case-sensitive to match globToRegex", verdict, NotContained)
	}
	assertWitnessIsValid(t, witness, "README.md", []string{"readme.md"})
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
	includeRe := globToRegex(include)
	if !includeRe.MatchString(witness) {
		t.Fatalf("witness %q does not match include pattern %q (regex %s)", witness, include, includeRe.String())
	}
	for _, exclude := range excludes {
		excludeRe := globToRegex(exclude)
		if excludeRe.MatchString(witness) {
			t.Fatalf("witness %q unexpectedly matches exclude pattern %q (regex %s)", witness, exclude, excludeRe.String())
		}
	}
}
