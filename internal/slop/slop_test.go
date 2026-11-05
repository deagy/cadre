package slop

import (
	"bytes"
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

const shippedRules = "../../roster/shared/slop-rules.yaml"

func loadShipped(t *testing.T) *RuleSet {
	t.Helper()
	data, err := os.ReadFile(shippedRules)
	if err != nil {
		t.Fatalf("reading the shipped rules: %v", err)
	}
	set, err := ParseRules(data)
	if err != nil {
		t.Fatalf("shipped rules do not parse: %v", err)
	}
	return set
}

func scanText(t *testing.T, set *RuleSet, surface Surface, text string) UnitResult {
	t.Helper()
	report, err := Scan(set, []Unit{{Name: "t.md", Surface: surface, Text: text}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return report.Units[0]
}

func allHits(result UnitResult) []Hit {
	return append(append([]Hit{}, result.Hits...), result.CountedHits...)
}

func hasRule(result UnitResult, id string) bool {
	for _, hit := range allHits(result) {
		if hit.RuleID == id {
			return true
		}
	}
	return false
}

// AC-1: every rule has a positive and a negative test. The positive and the
// near-miss live in the rules file beside the pattern, so a new rule cannot
// land without both; this checks them through the real scanner.
func TestEveryRuleHasWorkingPositiveAndNegativeExamples(t *testing.T) {
	set := loadShipped(t)
	if len(set.Rules) < 15 {
		t.Fatalf("only %d rules loaded; the rules file was not read", len(set.Rules))
	}
	for _, rule := range set.Rules {
		t.Run(rule.ID, func(t *testing.T) {
			if !rule.Matches(rule.Example) {
				t.Errorf("pattern does not match its own example %q", rule.Example)
			}
			if rule.Matches(rule.Clean) {
				t.Errorf("pattern matches its clean near-miss %q", rule.Clean)
			}
			if !hasRule(scanText(t, set, SurfaceMarkdown, rule.Example+"\n"), rule.ID) {
				t.Errorf("scan did not report %s on its example", rule.ID)
			}
			if hasRule(scanText(t, set, SurfaceMarkdown, rule.Clean+"\n"), rule.ID) {
				t.Errorf("scan reported %s on its clean near-miss", rule.ID)
			}
		})
	}
}

// AC-1: a fixture for each rule fails; the clean corpus does not.
func TestGoldenCorpusSlopFailsAndCleanPasses(t *testing.T) {
	set := loadShipped(t)
	for _, rule := range set.Rules {
		path := filepath.Join("testdata", "slop", rule.ID+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("rule %s has no slop fixture: %v", rule.ID, err)
			continue
		}
		result := scanText(t, set, SurfaceMarkdown, string(data))
		if !hasRule(result, rule.ID) {
			t.Errorf("%s: expected a %s hit, got %+v", path, rule.ID, allHits(result))
		}
		if rule.Class == ClassHard && !result.Failed() {
			t.Errorf("%s: a hard tell must fail the piece", path)
		}
	}

	clean, err := filepath.Glob(filepath.Join("testdata", "clean", "*.md"))
	if err != nil || len(clean) < 3 {
		t.Fatalf("clean corpus not found (%v, %d files)", err, len(clean))
	}
	for _, path := range clean {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if result := scanText(t, set, SurfaceMarkdown, string(data)); result.Failed() {
			t.Errorf("%s: clean text failed: %+v", path, result.Hits)
		}
	}
}

// AC-2: nothing inside code fences, inline code, link targets or quotes hits.
func TestMaskedRegionsNeverHit(t *testing.T) {
	set := loadShipped(t)
	tell := "It is not a cache, it is a ledger \u2014 honestly."
	cases := map[string]string{
		"backtick fence":    "Intro.\n\n```\n" + tell + "\n```\n\nOutro.\n",
		"tilde fence":       "Intro.\n\n~~~go\n" + tell + "\n~~~\n",
		"longer fence":      "````\n```\n" + tell + "\n```\n````\n",
		"unclosed fence":    "Intro.\n\n```\n" + tell + "\n",
		"inline code":       "Use `" + tell + "` as written.\n",
		"double backticks":  "Use ``a ` " + tell + "`` as written.\n",
		"straight quotes":   "The draft said \"" + tell + "\" and was cut.\n",
		"curly quotes":      "The draft said \u201c" + tell + "\u201d and was cut.\n",
		"link target":       "See [the guide](https://example.test/\u2014/honestly).\n",
		"bare url":          "See https://example.test/a--b\u2014c for details.\n",
		"autolink":          "See <https://example.test/\u2014> for details.\n",
		"reference link":    "[ref]: https://example.test/\u2014\n",
		"exhibit in a list": "- the phrase \"great question\" is a tell\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if hits := allHits(scanText(t, set, SurfaceMarkdown, text)); len(hits) != 0 {
				t.Errorf("expected no hits, got %+v", hits)
			}
		})
	}

	// The same sentence in plain prose does hit, so the cases above prove
	// masking and not a pattern that never fires.
	if result := scanText(t, set, SurfaceMarkdown, tell+"\n"); !result.Failed() {
		t.Error("the unmasked sentence should fail")
	}
}

func TestMaskKeepsLengthAndLineNumbers(t *testing.T) {
	text := "one\n```\nfenced \u2014 text\n```\nfour `\u2014` five \"q\u2014q\" six\nseven \u2014 here\n"
	masked := Mask(text, SurfaceMarkdown)
	if len(masked) != len(text) {
		t.Fatalf("mask changed length: %d -> %d", len(text), len(masked))
	}
	if strings.Count(masked, "\n") != strings.Count(text, "\n") {
		t.Fatal("mask changed the number of lines")
	}
	set := loadShipped(t)
	result := scanText(t, set, SurfaceMarkdown, text)
	if len(result.Hits) != 1 || result.Hits[0].Line != 6 || result.Hits[0].RuleID != "em-dash" {
		t.Fatalf("expected one em-dash hit on line 6, got %+v", result.Hits)
	}
	if got := result.Hits[0].Context; got != "seven \u2014 here" {
		t.Errorf("context = %q", got)
	}
}

func TestHeadingHitsReportTheHeadingLine(t *testing.T) {
	set := loadShipped(t)
	result := scanText(t, set, SurfaceMarkdown, "Intro.\n\n\n## Why this matters\n")
	if len(result.Hits) != 1 || result.Hits[0].Line != 4 {
		t.Fatalf("expected one hit on line 4, got %+v", result.Hits)
	}
}

func TestCountedTellsFailOnlyAtTheLimit(t *testing.T) {
	set := loadShipped(t)
	two := scanText(t, set, SurfaceMarkdown, "A simple and plain design.\n")
	if two.Failed() || two.CountedTotal != 2 || two.CountedExceeded {
		t.Fatalf("two counted hits must not fail: %+v", two)
	}
	three := scanText(t, set, SurfaceMarkdown, "A simple, plain and elegant design.\n")
	if !three.Failed() || !three.CountedExceeded || len(three.Hits) != 3 {
		t.Fatalf("three counted hits must fail with all listed: %+v", three)
	}
	// Counted hits from different rules are tallied together.
	mixed := scanText(t, set, SurfaceMarkdown, "We leverage a simple, robust design.\n")
	if !mixed.Failed() {
		t.Fatalf("counted hits across rules must add up: %+v", mixed)
	}

	report, err := Scan(set, []Unit{{Name: "t.md", Surface: SurfaceMarkdown, Text: "A simple and plain design.\n"}},
		Options{CountedLimit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Failed || report.CountedLimit != 2 {
		t.Fatalf("--counted-limit 2 should fail two hits: %+v", report)
	}
}

func TestExemptionNeedsAReasonAndIsListed(t *testing.T) {
	set := loadShipped(t)
	body := "Fast \u2014 and small.\n"

	exempt := scanText(t, set, SurfaceMarkdown, "<!-- slop-ok: quoting a style guide exhibit -->\n"+body)
	if exempt.Failed() || exempt.Exempt == nil {
		t.Fatalf("a reasoned exemption should pass: %+v", exempt)
	}
	if exempt.Exempt.Reason != "quoting a style guide exhibit" || exempt.Exempt.Line != 1 {
		t.Errorf("exemption = %+v", exempt.Exempt)
	}

	for _, marker := range []string{"slop-ok:", "<!-- slop-ok: -->", "slop-ok:    "} {
		result := scanText(t, set, SurfaceMarkdown, marker+"\n"+body)
		if !result.Failed() || !hasRule(result, ExemptionRuleID) || result.Exempt != nil {
			t.Errorf("%q: a reasonless marker must fail, not exempt: %+v", marker, result)
		}
	}

	// A marker discussed in code is not a marker.
	result := scanText(t, set, SurfaceMarkdown, "Write `slop-ok: <reason>` to exempt.\n\n"+body)
	if !result.Failed() || result.Exempt != nil {
		t.Errorf("a marker inside inline code must not exempt: %+v", result)
	}

	report, err := Scan(set, []Unit{{Name: "a.md", Surface: SurfaceMarkdown, Text: "slop-ok: exhibit\n" + body}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WriteText(&out, report, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Exemptions (slop-ok):") || !strings.Contains(out.String(), "a.md line 1: exhibit") {
		t.Errorf("the report must list the exemption and its reason:\n%s", out.String())
	}
}

func TestDisabledRulesAreSkippedAndListed(t *testing.T) {
	set := loadShipped(t)
	units := []Unit{{Name: "a.md", Surface: SurfaceMarkdown, Text: "Fast \u2014 and small.\n"}}

	report, err := Scan(set, units, Options{Disabled: []Disabled{{ID: "em-dash", Reason: "legal appendix style"}}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed || len(report.Disabled) != 1 || report.Disabled[0].Reason != "legal appendix style" {
		t.Fatalf("disabled rule should not hit and should be recorded: %+v", report)
	}
	var out bytes.Buffer
	if err := WriteText(&out, report, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Disabled rules:") || !strings.Contains(out.String(), "em-dash: legal appendix style") {
		t.Errorf("the report must show the disabled rule:\n%s", out.String())
	}

	bad := map[string][]Disabled{
		"unknown id":      {{ID: "no-such-rule", Reason: "x"}},
		"missing reason":  {{ID: "em-dash", Reason: "  "}},
		"disabled twice":  {{ID: "em-dash", Reason: "a"}, {ID: "em-dash", Reason: "b"}},
		"exemption guard": {{ID: ExemptionRuleID, Reason: "x"}},
	}
	for name, disabled := range bad {
		if _, err := Scan(set, units, Options{Disabled: disabled}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestCommitSurface(t *testing.T) {
	set := loadShipped(t)

	masked := Mask("feat(slop)!: add scan\n\nBody.\n\nSigned-off-by: A B <a@b.test>\nCo-authored-by: C D <c@d.test>", SurfaceCommit)
	if strings.Contains(masked, "feat") || strings.Contains(masked, "Signed-off-by") || strings.Contains(masked, "Co-authored") {
		t.Errorf("prefix and trailers should be masked: %q", masked)
	}
	if !strings.Contains(masked, "add scan") || !strings.Contains(masked, "Body.") {
		t.Errorf("subject and body must stay: %q", masked)
	}

	// A single-paragraph message keeps its subject even if it looks like Key: value.
	if got := Mask("Note: fast", SurfaceCommit); strings.TrimSpace(got) != "fast" {
		t.Errorf("only the conventional prefix should go: %q", got)
	}

	// The subject paragraph is never a trailer block.
	if got := Mask("Refs: #1", SurfaceCommit); strings.Contains(got, "#1") == false {
		t.Errorf("subject-only message lost its text: %q", got)
	}

	result := scanText(t, set, SurfaceCommit, "fix: tidy parser\n\nIt is not a rewrite, it is a cleanup.\n\nSigned-off-by: A <a@b.test>\n")
	if !hasRule(result, "contrast-it-is-not-x-it-is-y") || result.Hits[0].Line != 3 {
		t.Errorf("body should be scanned with message-relative lines: %+v", result.Hits)
	}
	if hasRule(scanText(t, set, SurfaceCommit, "fix: tidy parser\n\nSmall change to the retry loop.\n"), "contrast-it-is-not-x-it-is-y") {
		t.Error("clean commit flagged")
	}
}

// AC-4: --git-range reports commit-message hits with the commit SHA.
func TestGitRangeReportsHitsWithCommitSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{
			"-c", "user.name=Test", "-c", "user.email=test@example.test",
			"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main",
		}, args...)...)
		command.Dir = dir
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "chore: start")
	run("tag", "base")
	run("commit", "-q", "--allow-empty", "-m", "fix: parser", "-m", "A short and plain body.")
	run("commit", "-q", "--allow-empty", "-m", "feat: importer \u2014 now faster")
	slopSHA := run("rev-parse", "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "docs: update notes", "-m", "Great question about retries.")
	secondSHA := run("rev-parse", "HEAD")

	units, err := CommitUnits(context.Background(), dir, "base..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 3 {
		t.Fatalf("expected 3 commits in the range, got %d", len(units))
	}
	set := loadShipped(t)
	report, err := Scan(set, units, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Failed {
		t.Fatal("the range has two slop commits and must fail")
	}
	found := map[string]string{}
	for _, unit := range report.Units {
		for _, hit := range unit.Hits {
			if hit.Commit != unit.Commit || hit.File != CommitMessageName {
				t.Errorf("hit does not carry its commit: %+v", hit)
			}
			found[hit.Commit] = hit.RuleID
		}
	}
	if found[slopSHA] != "em-dash" || found[secondSHA] != "sycophancy" || len(found) != 2 {
		t.Errorf("hits by commit = %v, want em-dash on %s and sycophancy on %s", found, slopSHA, secondSHA)
	}

	var out bytes.Buffer
	if err := WriteText(&out, report, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "commit "+slopSHA[:12]) {
		t.Errorf("text report must name the commit:\n%s", out.String())
	}
}

func TestGitRangeIsValidatedBeforeGitSeesIt(t *testing.T) {
	for _, bad := range []string{"", "--output=/tmp/x", "-n1", "a b..c", "main", "a..b\nc"} {
		if _, err := CommitUnits(context.Background(), t.TempDir(), bad); err == nil {
			t.Errorf("range %q should be rejected", bad)
		}
	}
}

func TestParseCommitLog(t *testing.T) {
	units, err := parseCommitLog("abc123\x00fix: one\n\nbody\n\x01\ndef456\x00feat: two\n\x01")
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 || units[0].Commit != "abc123" || units[0].Text != "fix: one\n\nbody" || units[1].Commit != "def456" {
		t.Fatalf("units = %+v", units)
	}
	if _, err := parseCommitLog("no separator here\x01"); err == nil {
		t.Error("a record without a SHA must be an error")
	}
}

// AC-10 (package half): a malformed rules file is an error, never a fallback.
func TestMalformedRulesFailClosed(t *testing.T) {
	valid := `version: 1
rules:
  - id: demo
    class: hard
    pattern: 'foo'
    rationale: r
    example: foo
    clean: bar
`
	if _, err := ParseRules([]byte(valid)); err != nil {
		t.Fatalf("the baseline fixture must parse: %v", err)
	}
	cases := map[string]string{
		"empty file":      "",
		"not yaml":        "version: [1\n",
		"no rules":        "version: 1\nrules: []\n",
		"no version":      strings.Replace(valid, "version: 1\n", "", 1),
		"unknown field":   strings.Replace(valid, "clean: bar", "clean: bar\n    extra: 1", 1),
		"bad class":       strings.Replace(valid, "class: hard", "class: soft", 1),
		"bad id":          strings.Replace(valid, "id: demo", "id: Demo_Rule", 1),
		"duplicate id":    valid + strings.SplitN(valid, "rules:\n", 2)[1],
		"lookahead":       strings.Replace(valid, "'foo'", `'foo(?=bar)'`, 1),
		"backreference":   strings.Replace(valid, "'foo'", `'(a)\1'`, 1),
		"empty match":     strings.Replace(valid, "'foo'", `'x*'`, 1),
		"missing example": strings.Replace(valid, "example: foo\n", "", 1),
		"missing clean":   strings.Replace(valid, "clean: bar\n", "", 1),
		"bad limit":       strings.Replace(valid, "version: 1\n", "version: 1\ncounted_limit: -2\n", 1),
	}
	for name, content := range cases {
		if _, err := ParseRules([]byte(content)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestReadUnitsInputs(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "one\n")
	write("sub/b.txt", "two\n")
	write("sub/code.go", "package x\n")
	write("node_modules/c.md", "skip\n")
	write("blob.bin", "a\x00b")

	units, skipped, err := ReadUnits([]string{dir, filepath.Join(dir, "blob.bin"), filepath.Join(dir, "sub", "code.go")},
		SurfaceMarkdown, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, unit := range units {
		names = append(names, filepath.Base(unit.Name))
	}
	if strings.Join(names, ",") != "a.md,b.txt,code.go" {
		t.Errorf("units = %v (a directory walk takes prose files and skips node_modules; an explicit file is read as given)", names)
	}
	if len(skipped) != 1 || skipped[0].Reason != "binary file" {
		t.Errorf("skipped = %+v", skipped)
	}

	glob, _, err := ReadUnits([]string{filepath.Join(dir, "*.md")}, SurfaceMarkdown, strings.NewReader(""))
	if err != nil || len(glob) != 1 {
		t.Errorf("glob = %v, %v", glob, err)
	}
	if _, _, err := ReadUnits([]string{filepath.Join(dir, "*.nothing")}, SurfaceMarkdown, strings.NewReader("")); err == nil {
		t.Error("a glob matching nothing must be an error")
	}
	if _, _, err := ReadUnits([]string{filepath.Join(dir, "missing.md")}, SurfaceMarkdown, strings.NewReader("")); err == nil {
		t.Error("a missing file must be an error")
	}
	stdin, _, err := ReadUnits([]string{"-"}, SurfaceMarkdown, strings.NewReader("from stdin\n"))
	if err != nil || len(stdin) != 1 || stdin[0].Name != StdinName || stdin[0].Text != "from stdin\n" {
		t.Errorf("stdin = %+v, %v", stdin, err)
	}
}

func TestJSONReportGolden(t *testing.T) {
	set := loadShipped(t)
	text := "# Report\n\nFast \u2014 and small. We leverage it.\n\nIt is not a cache, it is a ledger.\n\n```\nskip \u2014 this\n```\n"
	report, err := Scan(set, []Unit{{Name: "mixed.md", Surface: SurfaceMarkdown, Text: text}},
		Options{Disabled: []Disabled{{ID: "recap-ending", Reason: "release notes recap on purpose"}}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WriteJSON(&out, report, nil); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "mixed.golden.json")
	if *update {
		if err := os.WriteFile(golden, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("missing golden file (run with -update): %v", err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("JSON report changed; run `go test ./internal/slop -update` if intended.\n got:\n%s\nwant:\n%s", out.Bytes(), want)
	}
}

func TestTextReportShape(t *testing.T) {
	set := loadShipped(t)
	report, err := Scan(set, []Unit{
		{Name: "bad.md", Surface: SurfaceMarkdown, Text: "Fast \u2014 and small.\n"},
		{Name: "ok.md", Surface: SurfaceMarkdown, Text: "Fast and small.\n"},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WriteText(&out, report, []Skipped{{Path: "x.bin", Reason: "binary file"}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"bad.md: 1 tell(s)", "bad.md:1:", "[hard/em-dash]", "ok.md: clear",
		"x.bin: binary file", "1 tell(s) found", "not evidence of authorship",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("text report missing %q:\n%s", want, out.String())
		}
	}
}
