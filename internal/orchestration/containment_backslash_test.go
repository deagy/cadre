package orchestration

import (
	"testing"

	"github.com/deagy/cadre/cli/internal/selector"
)

// A Windows-spelled glob is the same glob.
//
// iterGlobTokens replaces `\` with `/` before tokenising, and the matcher does
// the same to the path it is testing. So `foo\bar/**` and `foo/bar/**` are not
// merely equivalent -- they compile to the byte-identical regex
// `(?i)^foo/bar/.*$`.
//
// The containment tokeniser skipped that step and treated `\` as an ordinary
// literal. A pattern was not contained by itself.
//
// That is the silent direction. A rule whose exclude_paths swallow one of its
// own path globs keeps its reviewers and its human_gate and fires on keywords
// alone; the linter exists to catch it, and a false not-contained is the
// answer that produces no output at all.

func TestASpellingDifferenceIsNotALanguageDifference(t *testing.T) {
	// The premise, checked first: if these ever stop compiling to the same
	// regex, the containment expectations below are asserting the wrong thing
	// and should fail here rather than quietly change meaning.
	for _, pair := range []struct{ backslash, forward string }{
		{`foo\bar/**`, "foo/bar/**"},
		{`a\b`, "a/b"},
		{`src\**\*.go`, "src/**/*.go"},
	} {
		got := selector.GlobToRegex(pair.backslash).String()
		want := selector.GlobToRegex(pair.forward).String()
		if got != want {
			t.Fatalf("the matcher no longer normalises backslashes: %q -> %s, %q -> %s",
				pair.backslash, got, pair.forward, want)
		}
	}
}

func TestAPatternIsContainedByItsOwnWindowsSpelling(t *testing.T) {
	// Both directions. Neither is more correct than the other -- they are the
	// same language -- so an implementation that normalised only the include
	// or only the excludes would pass one and fail the other.
	for _, testCase := range []struct{ include, exclude string }{
		{`foo\bar/**`, "foo/bar/**"},
		{"foo/bar/**", `foo\bar/**`},
		{`a\b`, "a/b"},
		{"a/b", `a\b`},
		{`src\**\*.go`, "src/**/*.go"},
		{"src/**/*.go", `src\**\*.go`},
	} {
		if verdict := GlobContains(testCase.include, []string{testCase.exclude}); verdict != Contained {
			t.Errorf("GlobContains(%q, [%q]) = %q, want %q -- these compile to the "+
				"same regex, so the include is contained by definition",
				testCase.include, testCase.exclude, verdict, Contained)
		}
	}
}

func TestNormalisingBackslashesDoesNotCollapseUnrelatedPatterns(t *testing.T) {
	// The check above is satisfied vacuously by a tokeniser that dropped
	// backslashes entirely, or by one that folded every pattern to the same
	// token stream. These stay distinct after normalisation, and a witness
	// proves the verdict rather than only asserting it.
	for _, testCase := range []struct{ include, exclude string }{
		{`foo\bar/**`, "foo/baz/**"},
		{`a\b`, "a/c"},
		{`src\**\*.go`, "src/**/*.py"},
	} {
		verdict, witness := GlobContainsWithWitness(testCase.include, []string{testCase.exclude})
		if verdict != NotContained {
			t.Errorf("GlobContains(%q, [%q]) = %q, want %q -- normalisation must not "+
				"make unrelated patterns interchangeable",
				testCase.include, testCase.exclude, verdict, NotContained)
			continue
		}
		if !selector.GlobToRegex(testCase.include).MatchString(witness) {
			t.Errorf("witness %q does not match its own include %q", witness, testCase.include)
		}
		if selector.GlobToRegex(testCase.exclude).MatchString(witness) {
			t.Errorf("witness %q is matched by exclude %q, so it proves nothing",
				witness, testCase.exclude)
		}
	}
}
