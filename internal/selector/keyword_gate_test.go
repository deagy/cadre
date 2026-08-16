package selector

import (
	"testing"
)

// The compile-avoidance gate, and the equivalence it owes the regex.
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
// The gate holds that condition only for ASCII, which is why it is applied only
// there -- see TestTheGateIsSkippedWhereItWouldNotAgreeWithTheRegex.
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

func TestTheGateIsSkippedWhereItWouldNotAgreeWithTheRegex(t *testing.T) {
	// The gate lowercases with strings.ToLower; the pattern folds case with
	// (?i). Those are different operations, and they disagree on characters
	// that fold to a letter without lowercasing to it. U+017F LATIN SMALL
	// LETTER LONG S is the reachable example: already lowercase, so ToLower
	// leaves it alone and Contains fails, while (?i)s folds it and matches.
	//
	// That is the one direction the gate's contract forbids -- skipping work
	// that would have returned true, which surfaces as a route silently not
	// selected. So the gate is applied only when both sides are ASCII, where
	// the two foldings coincide.
	//
	// The Python selector does not do this: _keyword_matches returns False for
	// the pair below while _keyword_regex().search returns True. Go is
	// deliberately the stricter of the two here. The corpus contains no such
	// character, so test_select_differential.py is unaffected -- verified, not
	// assumed.
	for _, probe := range []struct{ text, keyword string }{
		{"the ſtandard form", "standard"}, // U+017F folds to s
		{"the Kelvin sign", "kelvin"},     // U+212A folds to k
		{"a ſimple ſtring", "simple"},     // more than one in the text
		{"plain ascii text", "ascii"},     // the gated path still works
	} {
		gated := KeywordMatches(probe.text, probe.keyword)
		if ungated := ungatedKeywordMatches(probe.text, probe.keyword); gated != ungated {
			t.Errorf("the gate still disagrees with the regex.\ntext: %q\nkeyword: %q\n"+
				"gated: %v, ungated: %v", probe.text, probe.keyword, gated, ungated)
		}
	}
}

func TestEveryShippedKeywordAndCorpusTaskQualifiesForTheGate(t *testing.T) {
	// Named for what it checks. Whether the gate *runs* is not observable from
	// behaviour -- it is a pure optimization, so removing it entirely changes
	// cost and nothing else, and no correctness test can catch that. (Confirmed:
	// forcing the skip for all input fails nothing here.)
	//
	// What is observable, and what matters, is that the skip stays the
	// exception: if a shipped keyword or a corpus task were non-ASCII it would
	// bypass the pre-filter, and the optimization would quietly stop applying
	// to the workload it exists for.
	for _, keyword := range everyShippedKeyword(t) {
		if !isASCIIText(keyword) {
			t.Errorf("shipped keyword %q is not ASCII, so the gate no longer "+
				"applies to it", keyword)
		}
	}
	nonASCII := 0
	for _, testCase := range loadGoldenCorpus(t) {
		if !isASCIIText(testCase.Task) {
			nonASCII++
		}
	}
	if nonASCII > 0 {
		t.Errorf("%d corpus tasks are not ASCII and now bypass the gate", nonASCII)
	}
}
