package selector

import (
	"testing"
)

// The compile-avoidance gate, and the one place it is not equivalent.
//
// KeywordMatches does a cheap check before touching a regex: every
// whitespace-separated token of the keyword must appear in the lowercased
// text. Only then is the compiled pattern consulted for word boundaries.
//
// The gate exists for cost. `MatchRule` runs every route's and every risk
// rule's keywords against every selection -- 830 keywords across the shipped
// ruleset -- and the pre-filter rejects almost all of them without a regex.
//
// Its correctness condition is one-directional: the gate may only skip work
// that would have returned false. Skipping a keyword that *would* have matched
// does not raise anything. The route simply is not selected, the specialist is
// not dispatched, and the plan looks like a normal plan.
//
// Ported from roster/orchestration/test/test_selection_cost.py's
// GateEquivalenceTests. The rest of that file counts CPython `re._compile`
// calls against the interpreter's 512-entry cache -- the cliff that motivated
// memoizing routing.py -- and does not port: Go memoizes in unbounded package
// maps, so the same cliff cannot recur for the same reason.

// ungatedKeywordMatches is KeywordMatches with the pre-filter removed: the
// behaviour the gate is an optimization of, and the reference it must agree
// with.
func ungatedKeywordMatches(text, keyword string) bool {
	for _, span := range keywordBody(keyword).FindAllStringIndex(text, -1) {
		start, end := span[0], span[1]
		if start > 0 && isKeywordBoundaryChar(text[start-1]) {
			continue
		}
		if end < len(text) && isKeywordBoundaryChar(text[end]) {
			continue
		}
		return true
	}
	return false
}

// everyShippedKeyword collects the keywords the gate actually runs against.
func everyShippedKeyword(t *testing.T) []string {
	t.Helper()
	config := loadRoutingConfig(t)
	seen := map[string]bool{}
	var keywords []string
	add := func(keyword string) {
		if keyword != "" && !seen[keyword] {
			seen[keyword] = true
			keywords = append(keywords, keyword)
		}
	}
	for _, section := range []string{"routes", "risk_rules"} {
		for _, rule := range objectList(config[section]) {
			for _, keyword := range stringSlice(rule["keywords"]) {
				add(keyword)
			}
			for _, group := range anyList(rule["keyword_groups"]) {
				for _, keyword := range stringSlice(group) {
					add(keyword)
				}
			}
		}
	}
	return keywords
}

func TestTheGateAgreesWithTheRegexOnEveryShippedPair(t *testing.T) {
	// The full cross-product, not a sample: every keyword in the shipped
	// ruleset against every task in the golden corpus. A gate that is wrong
	// for one keyword is wrong silently, and the only way to find which is to
	// try them all -- which is cheap here and was the point of the Python
	// original.
	keywords := everyShippedKeyword(t)
	corpus := loadGoldenCorpus(t)
	if len(keywords) < 100 || len(corpus) < 100 {
		t.Fatalf("%d keywords and %d tasks; the ruleset or corpus failed to load "+
			"and this test would prove almost nothing", len(keywords), len(corpus))
	}

	disagreements := 0
	for _, testCase := range corpus {
		for _, keyword := range keywords {
			gated := KeywordMatches(testCase.Task, keyword)
			if gated == ungatedKeywordMatches(testCase.Task, keyword) {
				continue
			}
			disagreements++
			if disagreements <= 5 {
				t.Errorf("the gate disagrees with the regex.\nkeyword: %q\ntask: %q\n"+
					"gated: %v, ungated: %v", keyword, testCase.Task,
					gated, !gated)
			}
		}
	}
	if disagreements > 5 {
		t.Errorf("...and %d more disagreements", disagreements-5)
	}
	t.Logf("checked %d keyword/task pairs", len(keywords)*len(corpus))
}

func TestTheGateIsCaseInsensitiveTheSameWayThePatternIs(t *testing.T) {
	// The gate lowercases; the pattern carries (?i). For ASCII these are the
	// same operation, and the ruleset's keywords are ASCII.
	for _, probe := range []struct{ text, keyword string }{
		{"Update the TERRAFORM module", "terraform"},
		{"update the terraform module", "TERRAFORM"},
		{"UPDATE THE TERRAFORM MODULE", "TeRrAfOrM"},
	} {
		if !KeywordMatches(probe.text, probe.keyword) {
			t.Errorf("case folding failed: keyword %q against %q", probe.keyword, probe.text)
		}
		if KeywordMatches(probe.text, probe.keyword) != ungatedKeywordMatches(probe.text, probe.keyword) {
			t.Errorf("gate and regex disagree on case: %q / %q", probe.keyword, probe.text)
		}
	}
}

func TestAMultiWordKeywordStillRequiresAdjacency(t *testing.T) {
	// The gate checks each token independently -- "toshiba" here, "qkms"
	// there -- so on its own it would accept text containing both words far
	// apart. The regex joins them with \s+, and that is what decides. A gate
	// that were allowed to answer on its own would match a task mentioning
	// both words in unrelated sentences.
	const keyword = "toshiba qkms"
	if !KeywordMatches("integrate toshiba qkms today", keyword) {
		t.Error("an adjacent occurrence must match")
	}
	if !KeywordMatches("integrate toshiba   qkms today", keyword) {
		t.Error("a run of whitespace between the words must still match")
	}
	for _, text := range []string{
		"toshiba hardware and a separate qkms deployment",
		"qkms first, toshiba second",
		"toshiba only",
	} {
		if KeywordMatches(text, keyword) {
			t.Errorf("non-adjacent tokens matched: %q", text)
		}
		if KeywordMatches(text, keyword) != ungatedKeywordMatches(text, keyword) {
			t.Errorf("gate and regex disagree on adjacency: %q", text)
		}
	}
}

func TestTheGateAndTheRegexPartCompanyOnUnicodeCaseFolding(t *testing.T) {
	// A recorded exception, not an aspiration.
	//
	// The gate lowercases with strings.ToLower; the pattern folds case with
	// Go's (?i). Those are different operations, and they disagree on
	// characters that fold to a letter without lowercasing to it. U+017F
	// LATIN SMALL LETTER LONG S is the reachable example: it is already
	// lowercase, so ToLower leaves it alone and the gate's Contains fails,
	// while (?i)s matches it.
	//
	// The gate therefore skips a case the regex would have matched -- the one
	// direction its contract forbids. Pinned here because:
	//
	//   - the Python selector does exactly the same thing, checked directly:
	//     _keyword_matches returns False and _keyword_regex().search returns
	//     True for this pair. So this is a property of the design, not
	//     something the port introduced, and "fix" would mean changing routing
	//     behaviour rather than restoring it.
	//   - the cross-product test above passes only because no shipped keyword
	//     and no corpus task contains such a character. A test asserting total
	//     equivalence while sampling only ASCII would be the kind that passes
	//     without measuring anything.
	//
	// If this ever needs closing, the cheap fix is to skip the gate when
	// either side is not ASCII, which keeps the optimization for every real
	// task and costs one regex for the rest. That is a routing-behaviour
	// decision, so it is recorded rather than taken here.
	const text, keyword = "the ſtandard form", "standard"

	if KeywordMatches(text, keyword) {
		t.Error("the gate now matches a long s; if that was deliberate, this " +
			"test and the comment above should go, and the cross-product test " +
			"is the one that guarantees the rest")
	}
	if !ungatedKeywordMatches(text, keyword) {
		t.Error("the regex no longer folds U+017F to s, so this exception has " +
			"stopped being an exception and the gate is now equivalent")
	}
}
