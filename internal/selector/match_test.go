package selector

import "testing"

// The cases here are the ones routing.py's own docstrings call out as the
// contract, because those are exactly the behaviours a reimplementation gets
// subtly wrong. Go's regexp is RE2 and has no lookbehind or lookahead, so
// KeywordMatches reimplements the boundary predicate rather than
// transliterating Python's `(?<![a-z0-9-])body(?![a-z0-9-])` -- which is the
// single most likely place for this port to diverge.

func TestKeywordMatchesTreatsHyphenAsAWordCharacter(t *testing.T) {
	// routing.py: `"runner"` matches "the runner failed" but not
	// "cross-runner", "runner-info", or "runners".
	cases := []struct {
		text string
		want bool
	}{
		{"the runner failed", true},
		{"cross-runner", false},
		{"runner-info", false},
		{"runners", false},
		{"RUNNER", true},
		{"a runner.", true},
		{"(runner)", true},
		{"runner_info", true}, // underscore is not a boundary character
	}
	for _, testCase := range cases {
		t.Run(testCase.text, func(t *testing.T) {
			if got := KeywordMatches(testCase.text, "runner"); got != testCase.want {
				t.Errorf("KeywordMatches(%q, \"runner\") = %v, want %v",
					testCase.text, got, testCase.want)
			}
		})
	}
}

func TestKeywordMatchesUnderscoreAndDotAreNotBoundaries(t *testing.T) {
	// The accepted quirk routing.py names explicitly: the boundary class is
	// [a-z0-9-] only, so a keyword containing `_` or `.` CAN match embedded
	// in a longer token. routing.json's `bootstrap_sdlc.py` is the one
	// keyword in the ruleset with this shape. Pinned so a "tightening" of
	// the boundary class shows up here rather than as a silent routing
	// change.
	for _, text := range []string{
		"bootstrap_sdlc.py",
		"legacy_bootstrap_sdlc.py_old",
		"my_bootstrap_sdlc.py_v2",
	} {
		if !KeywordMatches(text, "bootstrap_sdlc.py") {
			t.Errorf("KeywordMatches(%q, \"bootstrap_sdlc.py\") = false, want true", text)
		}
	}
}

func TestKeywordMatchesSpansWhitespaceRuns(t *testing.T) {
	// A single space in a keyword compiles to `\s+`, so any run of
	// whitespace between the tokens matches.
	for _, text := range []string{
		"a container image here",
		"a container  image here",
		"a container\timage here",
		"a container\nimage here",
	} {
		if !KeywordMatches(text, "container image") {
			t.Errorf("KeywordMatches(%q, \"container image\") = false, want true", text)
		}
	}
	if KeywordMatches("container", "container image") {
		t.Error("a multi-word keyword must not match on one token alone")
	}
}

func TestGlobDialect(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		// `**/` is any number of leading segments, including none.
		{"**/go.mod", "go.mod", true},
		{"**/go.mod", "services/api/go.mod", true},
		{"**/go.mod", "go.mod.bak", false},
		// `*` stays within one segment.
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/nested/main.go", false},
		// `**` crosses separators.
		{"src/**", "src/nested/main.go", true},
		// `?` is exactly one non-separator character.
		{"file?.txt", "file1.txt", true},
		{"file?.txt", "file12.txt", false},
		{"file?.txt", "file/.txt", false},
		// Anchored at both ends.
		{"docs/guide.md", "docs/guide.md", true},
		{"docs/guide.md", "x/docs/guide.md", false},
		{"docs/guide.md", "docs/guide.md.bak", false},
		// Case-insensitive, and backslashes normalise to `/`.
		{"docs/GUIDE.md", "docs/guide.md", true},
		{`docs\guide.md`, "docs/guide.md", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.pattern+" vs "+testCase.path, func(t *testing.T) {
			got := GlobToRegex(testCase.pattern).MatchString(testCase.path)
			if got != testCase.want {
				t.Errorf("GlobToRegex(%q).MatchString(%q) = %v, want %v",
					testCase.pattern, testCase.path, got, testCase.want)
			}
		})
	}
}

func TestMatchRuleKeywordGroupsAreConjunctive(t *testing.T) {
	// keyword_groups match only when *every* group has at least one hit;
	// a partial match contributes nothing and is not reported.
	rule := map[string]any{
		"keyword_groups": []any{
			[]any{"go cli"},
			[]any{"add", "extend"},
		},
	}

	both := MatchRule(rule, "add a go cli flag", nil)
	if !both.Matched {
		t.Fatal("both groups satisfied must match")
	}
	if len(both.KeywordGroups) != 2 {
		t.Errorf("KeywordGroups = %v, want both groups reported", both.KeywordGroups)
	}

	partial := MatchRule(rule, "add a python flag", nil)
	if partial.Matched {
		t.Error("one group unsatisfied must not match")
	}
	if len(partial.KeywordGroups) != 0 {
		t.Errorf("an unsatisfied conjunction must report no groups, got %v", partial.KeywordGroups)
	}
}

func TestMatchRuleExcludePathsSubtractPerFile(t *testing.T) {
	// exclude_paths subtracts at the file level, not the rule level: a broad
	// include still matches on any other changed file.
	rule := map[string]any{
		"paths":         []any{"**/*.py"},
		"exclude_paths": []any{"**/test/**"},
	}

	excludedOnly := MatchRule(rule, "", []string{"roster/test/thing.py"})
	if excludedOnly.Matched {
		t.Error("the only changed file was excluded; the rule must not match")
	}

	mixed := MatchRule(rule, "", []string{"roster/test/thing.py", "roster/src/thing.py"})
	if !mixed.Matched {
		t.Fatal("a non-excluded file must still match")
	}
	if len(mixed.Paths) != 1 || mixed.Paths[0].File != "roster/src/thing.py" {
		t.Errorf("only the non-excluded file should be reported, got %v", mixed.Paths)
	}
}
