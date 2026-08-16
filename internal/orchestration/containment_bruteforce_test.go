package orchestration

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/selector"
)

// The containment engine, checked against exhaustive enumeration.
//
// GlobContains decides language containment with an NFA product construction.
// Every test above it asserts a hand-chosen pair, which means the cases it is
// checked on are the cases somebody thought of. This decides the same question
// by brute force -- enumerate every path up to a bound over a small alphabet,
// match each with the real matcher -- and compares.
//
// Ported from test_glob_containment.py's DifferentialAgainstBruteForceTests,
// which is where the `**` consuming a trailing `/` divergence was caught.

// oracleAlphabet includes a newline and both cases of a letter on purpose: the
// newline because `*` and `**` translate to regex fragments that treat it
// differently, and the case pair because the engine folds literals and an
// oracle that never varied case could not tell correct folding from none.
const (
	oracleAlphabet  = "aAb/.z\n"
	oracleMaxLength = 5
)

// containmentPatterns are small enough that the oracle's bound is rarely the
// limiting factor, and shaped to hit the constructs that carry the semantics:
// `**` at either end, a bare `**/`, adjacent separators, a leading `./`.
var containmentPatterns = []string{
	"*", "**", "?", "a", "b", "a/b", "**/a", "a/**", "*.a", "**/*.a", "a/*/b",
	"**/a/**", "a?b", "*/*", "a/**/b", "**/*", "./a", "a.b", "*a*", "**/",
	"/a", "a/", "**/a/b", "?/?", "a/*", "a/*/**",
	// Mixed case, so the oracle exercises the literal case-folding path.
	"A", "A/**", "**/A.a", "*.A", "Ab/*",
}

// bruteForceOracle decides containment by enumerating candidates, using the
// matcher the selector actually runs.
//
// Bounded by oracleMaxLength, so it can only *miss* a witness -- it never
// invents one. A disagreement where the oracle says NotContained is therefore
// always a real engine defect, while the other direction may just be a witness
// longer than the bound.
func bruteForceOracle(include string, excludes []string) (string, string) {
	includeMatcher := selector.GlobToRegex(include)
	excludeMatchers := make([]*regexp.Regexp, 0, len(excludes))
	for _, pattern := range excludes {
		excludeMatchers = append(excludeMatchers, selector.GlobToRegex(pattern))
	}

	alphabet := []rune(oracleAlphabet)
	var candidate []rune
	var walk func(depth int) (bool, string)
	walk = func(depth int) (bool, string) {
		text := string(candidate)
		if includeMatcher.MatchString(text) {
			excluded := false
			for _, matcher := range excludeMatchers {
				if matcher.MatchString(text) {
					excluded = true
					break
				}
			}
			if !excluded {
				return true, text
			}
		}
		if depth == oracleMaxLength {
			return false, ""
		}
		for _, letter := range alphabet {
			candidate = append(candidate, letter)
			if found, witness := walk(depth + 1); found {
				return true, witness
			}
			candidate = candidate[:len(candidate)-1]
		}
		return false, ""
	}
	if found, witness := walk(0); found {
		return NotContained, witness
	}
	return Contained, ""
}

func TestTheEngineAgreesWithExhaustiveEnumeration(t *testing.T) {
	// Both directions, and neither on trust.
	//
	// A NotContained verdict is checked twice: against the oracle, and by
	// validating the engine's own witness with the real matcher. Without that
	// second check the direction is unverifiable -- the oracle is
	// length-bounded, so an engine that returned NotContained for everything
	// would pass with the engine fully disabled.
	generator := rand.New(rand.NewSource(20260816))
	checked, containedChecked, witnessed := 0, 0, 0

	for iteration := 0; iteration < 300; iteration++ {
		include := containmentPatterns[generator.Intn(len(containmentPatterns))]
		excludes := make([]string, 0, 3)
		for _, index := range generator.Perm(len(containmentPatterns))[:1+generator.Intn(3)] {
			excludes = append(excludes, containmentPatterns[index])
		}

		verdict, witness := GlobContainsWithWitness(include, excludes)
		if verdict == Undetermined {
			continue
		}

		if verdict == NotContained {
			// Independently checkable, regardless of the oracle's bound.
			//
			// An empty witness is not "no witness": the empty path is a real
			// path, and for include `*` against exclude `?/?` it is the
			// counterexample -- `[^/]*` matches it and `?/?` does not. Treating
			// "" as a failure reported 31 defects that were not there.
			//
			// So the witness is validated rather than inspected. If the engine
			// genuinely produced nothing, "" fails the include check below,
			// which is the same signal without the false positives.
			witnessed++
			if !selector.GlobToRegex(include).MatchString(witness) {
				t.Errorf("witness %q does not match its own include %q",
					witness, include)
			}
			for _, exclude := range excludes {
				if selector.GlobToRegex(exclude).MatchString(witness) {
					t.Errorf("witness %q is excluded by %q, so it proves nothing",
						witness, exclude)
				}
			}
		}

		expected, oracleWitness := bruteForceOracle(include, excludes)
		if expected == Contained && verdict == NotContained {
			// The oracle is length-bounded; a witness longer than its bound is
			// a legitimate disagreement in this direction only, and the
			// engine's witness was validated above regardless.
			continue
		}
		checked++
		if expected == Contained {
			containedChecked++
		}
		if expected != verdict {
			t.Errorf("include=%q excludes=%v: engine said %s, exhaustive enumeration "+
				"said %s (oracle witness %q)",
				include, excludes, verdict, expected, oracleWitness)
		}
	}

	// Self-vacuity. An engine returning one verdict for everything, or a
	// generator producing only Undetermined pairs, agrees with the oracle
	// about nothing -- and would otherwise pass.
	if checked < 50 {
		t.Fatalf("only %d pairs were decided; the differential is not exercising "+
			"the engine", checked)
	}
	if containedChecked == 0 {
		t.Error("no pair was Contained; the engine returning NotContained for " +
			"everything would pass this test")
	}
	if witnessed == 0 {
		t.Error("no NotContained verdict carried a witness; the independent half " +
			"of this check never ran")
	}
	t.Logf("%d pairs decided against exhaustive enumeration (%d Contained), "+
		"%d witnesses independently validated", checked, containedChecked, witnessed)
}

func TestTheOracleAndTheEngineDisagreeWhenTheEngineIsWrong(t *testing.T) {
	// Guards the differential. It passes over an engine that is correct, which
	// is also what it would do if the oracle were broken -- if the oracle
	// matched nothing, every pair would come back Contained and only agree by
	// luck.
	//
	// So: a pair whose answer is known independently, checked through the
	// oracle alone.
	for _, testCase := range []struct {
		include  string
		excludes []string
		want     string
	}{
		{"a/b", []string{"a/b"}, Contained},
		{"a/b", []string{"a/*"}, Contained},
		{"a/*", []string{"a/b"}, NotContained},
		{"A", []string{"a"}, Contained},
		{"a", []string{"b"}, NotContained},
	} {
		got, witness := bruteForceOracle(testCase.include, testCase.excludes)
		if got != testCase.want {
			t.Errorf("oracle(%q, %v) = %s (witness %q), want %s -- the oracle is "+
				"not deciding correctly, so agreeing with it proves nothing",
				testCase.include, testCase.excludes, got, witness, testCase.want)
		}
		if got == NotContained && witness == "" {
			t.Errorf("oracle(%q, %v) = NotContained with no witness",
				testCase.include, testCase.excludes)
		}
	}
}

func TestTheOraclesAlphabetReachesEveryConstructThePatternsUse(t *testing.T) {
	// An alphabet missing a character that appears in the patterns makes whole
	// literals unreachable, and the oracle then agrees with anything about
	// them. Cheap to state, and the kind of thing that rots when a pattern is
	// added later.
	for _, pattern := range containmentPatterns {
		for _, char := range pattern {
			if char == '*' || char == '?' {
				continue
			}
			if !strings.ContainsRune(oracleAlphabet, char) {
				t.Errorf("pattern %q contains %q, which the oracle's alphabet cannot "+
					"produce -- no enumerated candidate can exercise that literal",
					pattern, string(char))
			}
		}
	}
}

func TestATrailingDoubleStarIsNotRequiredToEndOnASeparator(t *testing.T) {
	// The defect the differential found, stated directly so it is legible
	// without running 300 random pairs.
	//
	// `**` compiles to `.*` and matches "a". `**/` compiles to `(?:.*/)?` and
	// matches only the empty path or one ending in `/`. Modelling both as the
	// latter understated every trailing `**`, and an understated include is
	// easier to contain -- so `**` came back contained by `**/`.
	for _, testCase := range []struct {
		include string
		exclude string
		want    string
		why     string
	}{
		{"**", "**/", NotContained,
			"`**` matches \"a\"; `**/` does not"},
		{"a/**", "a/", NotContained,
			"`a/**` matches \"a/b\"; `a/` matches only \"a/\""},
		{"**/a/**", "**/", NotContained,
			"`**/a/**` matches \"a/a\"; `**/` does not"},
		{"**/", "**", Contained,
			"the other direction still holds: everything `**/` matches, `**` matches"},
	} {
		got := GlobContains(testCase.include, []string{testCase.exclude})
		if got != testCase.want {
			t.Errorf("GlobContains(%q, [%q]) = %s, want %s -- %s",
				testCase.include, testCase.exclude, got, testCase.want, testCase.why)
		}
	}
}
