package orchestration

import (
	"testing"

	"github.com/deagy/cadre/cli/internal/selector"
)

// The containment analyzer models the matcher that actually runs.
//
// GlobContains decides whether a rule's exclude_paths swallow one of its own
// path globs. That verdict is only worth anything if it is about the same glob
// dialect the selector matches with -- and the selector matches with
// selector.GlobToRegex, in a different package.
//
// The two were coupled by a comment. The comment pointed at this package's own
// globToRegex, which has no callers outside tests, and the two dialects had
// diverged on case: the analyzer was case-sensitive, the matcher sets (?i).
// So the analyzer reported `**/README.md` as not contained by `**/readme.md`
// while at runtime the exclude swallowed the include whole -- and the linter
// built to catch a rule losing its path coverage said nothing.
//
// A comment cannot fail. This can.

// containmentCases are pairs whose verdict is decidable, with a path that
// distinguishes them. Deliberately includes case variants, which is where the
// two implementations had drifted apart.
func containmentCases() []struct {
	include string
	exclude string
	probes  []string
} {
	return []struct {
		include string
		exclude string
		probes  []string
	}{
		{"**/README.md", "**/readme.md", []string{"docs/README.md", "docs/readme.md", "a/b/ReadMe.md"}},
		{"src/*.go", "src/*.go", []string{"src/main.go", "src/Main.go"}},
		{"src/**", "src/**", []string{"src/a", "src/a/b", "src/A/B.go"}},
		{"roster/**/AGENT.md", "roster/**/agent.md", []string{"roster/x/AGENT.md", "roster/x/y/agent.md"}},
		{"**/Dockerfile", "**/dockerfile", []string{"a/Dockerfile", "Dockerfile", "b/c/DOCKERFILE"}},
		{"src/*.go", "src/*.py", []string{"src/main.go", "src/main.py"}},
		{"a/b.md", "a/*", []string{"a/b.md", "a/c.md", "a/b/c.md"}},
		{"Foo/**", "bar/**", []string{"Foo/x", "bar/x", "foo/x"}},
	}
}

func TestTheAnalyzersVerdictAgreesWithTheLiveMatcher(t *testing.T) {
	// For every probe path: if the analyzer says Contained, the live matcher
	// must never let that path through the exclude. A disagreement means the
	// linter is reasoning about a dialect nothing runs.
	checked, contained := 0, 0
	for _, testCase := range containmentCases() {
		verdict := GlobContains(testCase.include, []string{testCase.exclude})
		if verdict == Undetermined {
			continue
		}
		includeMatcher := selector.GlobToRegex(testCase.include)
		excludeMatcher := selector.GlobToRegex(testCase.exclude)

		if verdict == Contained {
			contained++
			for _, path := range testCase.probes {
				checked++
				if includeMatcher.MatchString(path) && !excludeMatcher.MatchString(path) {
					t.Errorf("GlobContains(%q, [%q]) = Contained, but the live matcher "+
						"lets %q through: the include matches it and the exclude does not.",
						testCase.include, testCase.exclude, path)
				}
			}
		}
	}
	if contained == 0 {
		t.Fatal("no case produced Contained; this test would prove nothing")
	}
	t.Logf("checked %d probe paths across %d Contained verdicts", checked, contained)
}

func TestANotContainedVerdictCarriesAWitnessTheLiveMatcherAgreesWith(t *testing.T) {
	// The other direction, and the stronger one: NotContained comes with a
	// concrete path the analyzer claims escapes the excludes. That claim is
	// checkable against the real matcher, so a NotContained reached by faulty
	// reasoning is caught rather than merely being the quiet answer.
	witnessed := 0
	for _, testCase := range containmentCases() {
		verdict, witness := GlobContainsWithWitness(testCase.include, []string{testCase.exclude})
		if verdict != NotContained {
			continue
		}
		if witness == "" {
			t.Errorf("GlobContains(%q, [%q]) = NotContained with no witness",
				testCase.include, testCase.exclude)
			continue
		}
		witnessed++
		if !selector.GlobToRegex(testCase.include).MatchString(witness) {
			t.Errorf("witness %q for include %q is not matched by the live matcher; "+
				"the analyzer's counterexample is not a counterexample",
				witness, testCase.include)
		}
		if selector.GlobToRegex(testCase.exclude).MatchString(witness) {
			t.Errorf("witness %q is matched by exclude %q under the live matcher, "+
				"so it does not demonstrate an escape", witness, testCase.exclude)
		}
	}
	if witnessed == 0 {
		t.Fatal("no case produced NotContained with a witness; this test would prove nothing")
	}
}

func TestBothDialectsFoldCaseTheSameWay(t *testing.T) {
	// The specific divergence, stated directly rather than only through its
	// consequences. If selector.GlobToRegex ever stops setting (?i), this is
	// the test that says the analyzer has to follow -- rather than the linter
	// quietly over-reporting shadowed globs from then on.
	for _, testCase := range []struct {
		pattern string
		path    string
	}{
		{"**/readme.md", "docs/README.md"},
		{"**/dockerfile", "svc/Dockerfile"},
		{"SRC/*.GO", "src/main.go"},
	} {
		if !selector.GlobToRegex(testCase.pattern).MatchString(testCase.path) {
			t.Fatalf("the live matcher no longer folds case: %q does not match %q. "+
				"The analyzer folds case to model it, so it must change too.",
				testCase.pattern, testCase.path)
		}
		if verdict := GlobContains(testCase.path, []string{testCase.pattern}); verdict != Contained {
			t.Errorf("the matcher folds case but the analyzer does not: "+
				"GlobContains(%q, [%q]) = %q", testCase.path, testCase.pattern, verdict)
		}
	}
}
