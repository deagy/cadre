package knowledge

// Contract tests: parse, validate, serialize, round trip.
//
// Ported from roster/knowledge-store/test/test_staged_records.py and
// test_staged_store.py on main. The round trip is the load-bearing property,
// because an export that silently drops a field turns the durability backup
// into a corruption vector -- and that would only be discovered when the store
// was lost.

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func mustSerialize(t *testing.T, frontmatter map[string]any, body string) string {
	t.Helper()
	text, err := SerializeStagedRecord(frontmatter, body)
	if err != nil {
		t.Fatalf("cannot serialise: %v", err)
	}
	return text
}

func TestARecordRoundTripsThroughSerialiseAndParse(t *testing.T) {
	frontmatter := testStagedFrontmatter("KS-20260101-round-trip")
	text := mustSerialize(t, frontmatter, testStagedBody)

	parsed, body, err := ParseStagedRecord(text)
	if err != nil {
		t.Fatalf("cannot parse serialised record: %v", err)
	}
	if !reflect.DeepEqual(parsed, frontmatter) {
		t.Fatalf("frontmatter changed across the round trip:\n got %#v\nwant %#v", parsed, frontmatter)
	}
	if strings.TrimSpace(body) != strings.TrimSpace(testStagedBody) {
		t.Fatalf("body changed across the round trip: %q", body)
	}
	if findings := ValidateStagedRecord(parsed, body); len(findings) > 0 {
		t.Fatalf("a round-tripped record no longer validates: %v", findings)
	}
}

func TestRoundTripIsStableUnderRepetition(t *testing.T) {
	frontmatter := testStagedFrontmatter("KS-20260101-stable")
	first := mustSerialize(t, frontmatter, testStagedBody)
	parsed, body, err := ParseStagedRecord(first)
	if err != nil {
		t.Fatalf("cannot parse: %v", err)
	}
	second := mustSerialize(t, parsed, body)
	if first != second {
		t.Fatalf("serialisation is not stable:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestBooleansSurviveAsBooleans(t *testing.T) {
	frontmatter := testStagedFrontmatter("KS-20260101-booleans")
	frontmatter["untrusted_instruction_risk"] = "unknown"
	frontmatter["status"] = "deferred"
	frontmatter["disposition"] = map[string]any{
		"action": "deferred", "reason": "escalated", "classification_used": "internal",
		"diverged_from_proposal": true, "decided_by": testStagedSteward,
	}
	text := mustSerialize(t, frontmatter, testStagedBody)
	parsed, _, err := ParseStagedRecord(text)
	if err != nil {
		t.Fatalf("cannot parse: %v", err)
	}
	disposition, ok := parsed["disposition"].(map[string]any)
	if !ok {
		t.Fatalf("disposition did not survive as a mapping: %#v", parsed["disposition"])
	}
	if diverged, ok := disposition["diverged_from_proposal"].(bool); !ok || !diverged {
		t.Fatalf("a boolean round-tripped as %#v", disposition["diverged_from_proposal"])
	}
	if risk, ok := parsed["untrusted_instruction_risk"].(string); !ok || risk != "unknown" {
		t.Fatalf("the string 'unknown' round-tripped as %#v", parsed["untrusted_instruction_risk"])
	}
}

func TestAValueContainingQuotesAndColonsRoundTrips(t *testing.T) {
	frontmatter := testStagedFrontmatter("KS-20260101-quoting")
	frontmatter["title"] = `He said: "no" \ and left`
	text := mustSerialize(t, frontmatter, testStagedBody)
	parsed, _, err := ParseStagedRecord(text)
	if err != nil {
		t.Fatalf("cannot parse: %v", err)
	}
	if parsed["title"] != frontmatter["title"] {
		t.Fatalf("title round-tripped as %#v", parsed["title"])
	}
}

// A value that looks like a YAML keyword must stay a string, or the contract's
// booleans stop being distinguishable from prose that happens to say "true".
func TestAValueThatLooksLikeAYAMLKeywordStaysAString(t *testing.T) {
	frontmatter := testStagedFrontmatter("KS-20260101-keyword")
	frontmatter["sensitivity_notes"] = "null"
	text := mustSerialize(t, frontmatter, testStagedBody)
	parsed, _, err := ParseStagedRecord(text)
	if err != nil {
		t.Fatalf("cannot parse: %v", err)
	}
	if notes, ok := parsed["sensitivity_notes"].(string); !ok || notes != "null" {
		t.Fatalf(`the string "null" round-tripped as %#v`, parsed["sensitivity_notes"])
	}
}

func TestABodyEditedWithoutRecomputingTheDigestIsRejected(t *testing.T) {
	frontmatter := testStagedFrontmatter("KS-20260101-tampered")
	findings := ValidateStagedRecord(frontmatter, testStagedBody+"\nan unrecorded amendment\n")
	if len(findings) != 1 || !strings.Contains(findings[0], "content_digest does not match") {
		t.Fatalf("expected exactly one digest finding, got %v", findings)
	}
}

func TestADroppedRequiredKeyIsReportedByName(t *testing.T) {
	frontmatter := testStagedFrontmatter("KS-20260101-incomplete")
	delete(frontmatter, "source_scope")
	findings := ValidateStagedRecord(frontmatter, testStagedBody)
	if !containsFinding(findings, `missing required frontmatter key "source_scope"`) {
		t.Fatalf("expected a named missing-key finding, got %v", findings)
	}
}

func TestAnUnknownTopLevelKeyIsRefused(t *testing.T) {
	frontmatter := testStagedFrontmatter("KS-20260101-extra")
	frontmatter["ingested"] = true
	findings := ValidateStagedRecord(frontmatter, testStagedBody)
	if !containsFinding(findings, `unknown top-level frontmatter key "ingested"`) {
		t.Fatalf("expected the closed contract to refuse an unknown key, got %v", findings)
	}
}

func TestRecommendedActionDeleteIsRefusedWithItsOwnReason(t *testing.T) {
	frontmatter := testStagedFrontmatter("KS-20260101-delete-action")
	frontmatter["recommended_action"] = "delete"
	findings := ValidateStagedRecord(frontmatter, testStagedBody)
	if !containsFinding(findings, "recommended_action 'delete' is not an available action") {
		t.Fatalf("expected the delete-action refusal, got %v", findings)
	}
}

// The automatic-defer rule: an injection-risk candidate is deferred, never
// accepted on a steward's discretion alone.
func TestAutomaticDeferRefusesAnAcceptedRiskyRecord(t *testing.T) {
	for _, risk := range []any{true, "unknown"} {
		frontmatter := testStagedFrontmatter("KS-20260101-auto-defer")
		frontmatter["untrusted_instruction_risk"] = risk
		frontmatter["status"] = "accepted"
		frontmatter["disposition"] = map[string]any{
			"action": "accepted", "reason": "looks fine", "classification_used": "internal",
			"diverged_from_proposal": false, "decided_by": testStagedSteward,
		}
		findings := ValidateStagedRecord(frontmatter, testStagedBody)
		if !containsFinding(findings, "so status must not be 'accepted'") {
			t.Fatalf("risk %#v: expected the automatic-defer refusal, got %v", risk, findings)
		}
		if !containsFinding(findings, "disposition.action must be 'deferred'") {
			t.Fatalf("risk %#v: expected the disposition half of the rule, got %v", risk, findings)
		}
	}
}

func TestAProposedRecordMayNotCarryADisposition(t *testing.T) {
	frontmatter := testStagedFrontmatter("KS-20260101-incoherent")
	frontmatter["disposition"] = map[string]any{
		"action": "accepted", "reason": "r", "classification_used": "internal",
		"diverged_from_proposal": false, "decided_by": testStagedSteward,
	}
	findings := ValidateStagedRecord(frontmatter, testStagedBody)
	if !containsFinding(findings, "status 'proposed' requires 'disposition' to be absent") {
		t.Fatalf("expected the coherence finding, got %v", findings)
	}
}

func TestAbsoluteLocalPathsAreRefusedInEvidenceAndOrigin(t *testing.T) {
	cases := map[string]string{
		"posix home":    "/home/someone/notes.md:3",
		"macos home":    "/Users/someone/notes.md:3",
		"tilde":         "~/notes.md:3",
		"windows drive": `C:\Users\someone\notes.md`,
	}
	for name, leaked := range cases {
		t.Run(name, func(t *testing.T) {
			frontmatter := testStagedFrontmatter("KS-20260101-leaky")
			frontmatter["evidence"] = []any{leaked}
			findings := ValidateStagedRecord(frontmatter, testStagedBody)
			if !containsFinding(findings, "contains an absolute local path") {
				t.Fatalf("expected a redaction finding for %q, got %v", leaked, findings)
			}
		})
	}
}

// The lookbehind these patterns need is emulated by hand in Go, so the
// no-false-positive half of the rule needs its own test: a repository-relative
// reference and a URL must both pass.
func TestOrdinaryReferencesAreNotMistakenForAbsolutePaths(t *testing.T) {
	for _, safe := range []string{
		"internal/knowledge/staged_store.go:12",
		"https://example.invalid/Users/page",
		"docs/homes/index.md",
		"see file.py:12",
	} {
		frontmatter := testStagedFrontmatter("KS-20260101-safe")
		frontmatter["evidence"] = []any{safe}
		findings := ValidateStagedRecord(frontmatter, testStagedBody)
		if containsFinding(findings, "absolute local path") {
			t.Fatalf("%q was wrongly flagged as an absolute path: %v", safe, findings)
		}
	}
}

func TestUnsupportedFrontmatterConstructsFailLoudly(t *testing.T) {
	cases := map[string]string{
		"block scalar":     "---\ntitle: |\n  long prose\n---\n\nbody\n",
		"flow collection":  "---\nevidence: [a, b]\n---\n\nbody\n",
		"anchor":           "---\ntitle: &anchor\n---\n\nbody\n",
		"tab indentation":  "---\norigin:\n\ttask: T\n---\n\nbody\n",
		"no delimiter":     "title: nope\n",
		"unclosed block":   "---\ntitle: \"x\"\n",
		"orphan indent":    "---\n  task: T\n---\n\nbody\n",
		"duplicate key":    "---\ntitle: \"a\"\ntitle: \"b\"\n---\n\nbody\n",
		"two-level nested": "---\norigin:\n  task:\n---\n\nbody\n",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := ParseStagedRecord(text)
			if err == nil {
				t.Fatal("expected an unsupported construct to be refused")
			}
			var formatErr *RecordFormatError
			if !errors.As(err, &formatErr) {
				t.Fatalf("expected a RecordFormatError, got %T: %v", err, err)
			}
		})
	}
}

func TestDigestIgnoresLineEndingsAndSurroundingWhitespace(t *testing.T) {
	base := ComputeStagedDigest("a\nb\n")
	for _, variant := range []string{"a\r\nb\r\n", "\n\na\nb\n\n  ", "a\rb"} {
		if got := ComputeStagedDigest(variant); got != base {
			t.Fatalf("digest of %q is %s, want %s", variant, got, base)
		}
	}
	if ComputeStagedDigest("a\n\nb\n") == base {
		t.Fatal("interior blank lines must remain significant")
	}
}

func containsFinding(findings []string, substring string) bool {
	for _, finding := range findings {
		if strings.Contains(finding, substring) {
			return true
		}
	}
	return false
}
