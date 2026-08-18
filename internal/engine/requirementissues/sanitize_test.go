package requirementissues

import (
	"strings"
	"testing"
)

// The raw task id never reaches GitLab.
//
// A task id is operator-chosen and may name a customer, an incident or an
// embargoed project, while the target GitLab project is often visible far more
// widely than the repository the task came from.
func TestIdentifiersNeverCarryTheRawTaskID(t *testing.T) {
	const secret = "acme-breach-2026-embargoed"

	marker := ComputeMarker(secret, "G2", "req-1")
	if strings.Contains(marker, secret) || len(marker) != 16 {
		t.Errorf("marker = %q", marker)
	}
	hash := TaskHash(secret)
	if strings.Contains(hash, secret) || len(hash) != 16 {
		t.Errorf("task hash = %q", hash)
	}

	label, err := ItemLabel(marker)
	if err != nil {
		t.Fatalf("ItemLabel: %v", err)
	}
	if strings.Contains(label, secret) {
		t.Errorf("label = %q", label)
	}

	body := RenderBody(secret, "G2", marker, "a description")
	if strings.Contains(body, secret) {
		t.Error("the rendered body carries the raw task id")
	}
	if !strings.Contains(body, hash) {
		t.Error("the rendered body carries no task reference at all")
	}
}

// Markers are per item, so two items cannot collide onto one issue.
func TestMarkersDistinguishItems(t *testing.T) {
	first := ComputeMarker("task-1", "G2", "req-1")
	for _, other := range []string{
		ComputeMarker("task-2", "G2", "req-1"),
		ComputeMarker("task-1", "G3", "req-1"),
		ComputeMarker("task-1", "G2", "req-2"),
	} {
		if first == other {
			t.Errorf("two different items share the marker %q", first)
		}
	}
	if ComputeMarker("task-1", "G2", "req-1") != first {
		t.Error("the same item produced two markers")
	}
}

// A label GitLab would mangle breaks idempotency and publishes duplicates.
func TestItemLabelRejectsAnUnusableCharset(t *testing.T) {
	if _, err := ItemLabel("Not_A_Hex_Marker"); err == nil {
		t.Error("a marker outside [a-z0-9-] produced a label")
	}
}

func TestValidateItemKey(t *testing.T) {
	for _, valid := range []string{"req-1", "REQ.1", "a", strings.Repeat("a", 64)} {
		if err := ValidateItemKey(valid); err != nil {
			t.Errorf("ValidateItemKey(%q) = %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "-leading", ".leading", "has space", "has/slash",
		strings.Repeat("a", 65), "unicodeé"} {
		if err := ValidateItemKey(invalid); err == nil {
			t.Errorf("ValidateItemKey(%q) was accepted", invalid)
		}
	}
}

// A title is printable ASCII after NFKC, or it is refused.
//
// Titles appear in listings and notifications, where a homoglyph or a
// bidirectional control is a spoofing tool and has no legitimate use.
func TestSanitizeTitleRefusesEverythingButPrintableASCII(t *testing.T) {
	if got, err := SanitizeTitle("  Add rate limiting  ", "k"); err != nil || got != "Add rate limiting" {
		t.Errorf("SanitizeTitle = (%q, %v)", got, err)
	}

	// NFKC folds a compatibility character into ASCII, which is allowed --
	// the point is that it is normalised *before* the check, so a lookalike
	// cannot pass by decomposing into acceptable pieces.
	if got, err := SanitizeTitle("ＡＢＣ", "k"); err != nil || got != "ABC" {
		t.Errorf("fullwidth title = (%q, %v), want ABC", got, err)
	}

	refused := map[string]string{
		"right-to-left override": "safe‮txt.exe",
		"zero-width space":       "safe​title",
		"newline":                "line one\nline two",
		"emoji":                  "ship it \U0001F680",
		"empty":                  "   ",
		"leading dash":           "-rf /",
		"leading slash":          "/assign @someone",
		"too long":               strings.Repeat("a", 201),
	}
	for name, title := range refused {
		if _, err := SanitizeTitle(title, "k"); err == nil {
			t.Errorf("%s: title %q was accepted", name, title)
		}
	}
}

// GitLab quick actions execute rather than render.
//
// Without this refusal an issue body is an instruction channel: "/assign", or
// "/confidential", or worse, written by whatever produced the description.
func TestQuickActionLinesAreRefused(t *testing.T) {
	for _, description := range []string{
		"/assign @someone",
		"  /confidential",
		"fine line\n/close\nanother line",
		"\t/spend 10h",
	} {
		if _, err := SanitizeDescription(description, "k"); err == nil {
			t.Errorf("a quick action was accepted: %q", description)
		}
	}
	if _, err := SanitizeDescription("a path like /usr/bin is fine mid-line", "k"); err != nil {
		t.Errorf("a slash mid-line was refused: %v", err)
	}
}

// The separators that make two line-splitters disagree are refused outright.
//
// Every check here is line-oriented. If the splitter used for checking sees
// fewer lines than the renderer does, a quick action can hide on a line the
// check never looked at.
func TestSeparatorsThatHideLinesAreRefused(t *testing.T) {
	hidden := map[string]string{
		"vertical tab":     "visible\v/assign @someone",
		"form feed":        "visible\f/assign @someone",
		"file separator":   "visible\x1c/assign @someone",
		"group separator":  "visible\x1d/assign @someone",
		"record separator": "visible\x1e/assign @someone",
		"next line":        "visible/assign @someone",
		"line separator":   "visible /assign @someone",
		"para separator":   "visible /assign @someone",
	}
	for name, description := range hidden {
		if _, err := SanitizeDescription(description, "k"); err == nil {
			t.Errorf("%s: a quick action hidden behind a separator was accepted", name)
		}
	}
}

func TestLoneSurrogatesAreRefused(t *testing.T) {
	if _, err := SanitizeDescription(string([]byte{0xED, 0xA0, 0x80}), "k"); err == nil {
		t.Error("a lone surrogate was accepted")
	}
	if _, err := SanitizeDescription("\xff\xfe invalid utf8", "k"); err == nil {
		t.Error("invalid UTF-8 was accepted")
	}
}

// Content may not impersonate this module's own output.
func TestReservedPrefixesAreRefused(t *testing.T) {
	for _, description := range []string{
		ProvenanceBanner + " but forged",
		RefLinePrefix + "task:0000 gate:G2 item:dead",
		"first line\n" + RefLinePrefix + "forged",
	} {
		if _, err := SanitizeDescription(description, "k"); err == nil {
			t.Errorf("content forging this module's output was accepted: %q", description)
		}
	}
}

// Mentions and cross-references are defused, not deleted.
func TestReferencesAreNeutralisedButReadable(t *testing.T) {
	sanitized, err := SanitizeDescription("cc @alice and see #42 plus group/proj#7", "k")
	if err != nil {
		t.Fatalf("SanitizeDescription: %v", err)
	}

	if strings.Contains(sanitized, "@alice") {
		t.Error("an @mention survived intact and would notify a real user")
	}
	if strings.Contains(sanitized, "#42") {
		t.Error("a bare cross-reference survived intact")
	}
	if strings.Contains(sanitized, "proj#7") {
		t.Error("a project cross-reference survived intact")
	}
	// Still readable: the words remain, only the sigil is broken.
	for _, fragment := range []string{"alice", "42", "group/proj"} {
		if !strings.Contains(sanitized, fragment) {
			t.Errorf("neutralisation removed %q rather than defusing it", fragment)
		}
	}
	if !strings.Contains(sanitized, zeroWidthSpace) {
		t.Error("nothing was neutralised at all")
	}
}

// An email-like string is not a mention.
func TestAnEmailIsNotAMention(t *testing.T) {
	sanitized, err := SanitizeDescription("write to alice@example.com", "k")
	if err != nil {
		t.Fatalf("SanitizeDescription: %v", err)
	}
	if strings.Contains(sanitized, zeroWidthSpace) {
		t.Errorf("an email address was mangled as a mention: %q", sanitized)
	}
}

func TestDescriptionLengthCap(t *testing.T) {
	if _, err := SanitizeDescription(strings.Repeat("a", MaxDescriptionLength+1), "k"); err == nil {
		t.Error("an over-long description was accepted")
	}
	if _, err := SanitizeDescription(strings.Repeat("a", MaxDescriptionLength), "k"); err != nil {
		t.Errorf("a description at the cap was refused: %v", err)
	}
}

// A description containing a fence must not break out of ours.
//
// Otherwise everything after the inner fence renders as markdown, and content
// that was carefully sanitised becomes active formatting again.
func TestTheFenceOutlivesAnyFenceInsideIt(t *testing.T) {
	description := "before\n```\nnot really code\n```\nafter"
	body := RenderBody("task-1", "G2", "abc123", description)

	longest := 0
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") && strings.Trim(trimmed, "`") == "" {
			if len(trimmed) > longest {
				longest = len(trimmed)
			}
		}
	}
	if longest <= 3 {
		t.Errorf("the outer fence is %d backticks; the description contains a 3-backtick fence", longest)
	}

	// And a longer run inside pushes it further still.
	deeper := RenderBody("task-1", "G2", "abc123", "````\ncontent\n````")
	if !strings.Contains(deeper, "`````") {
		t.Error("a four-backtick run inside did not widen the outer fence")
	}
}

func TestFenceLength(t *testing.T) {
	cases := map[string]int{
		"no backticks":     3,
		"one ` here":       3,
		"``` a fence":      4,
		"```` deeper":      5,
		"a ``` and a ````": 5,
	}
	for text, want := range cases {
		if got := fenceLength(text); got != want {
			t.Errorf("fenceLength(%q) = %d, want %d", text, got, want)
		}
	}
}

// Publishing is refused from a run whose gate content cannot be trusted to
// represent an agreed requirement.
func TestPublishEligibility(t *testing.T) {
	if err := CheckPublishEligibility(false, "", "ready"); err != nil {
		t.Errorf("a ready gate was refused: %v", err)
	}

	blocked := map[string]struct {
		halted  bool
		reentry string
		status  string
	}{
		"halted run":      {true, "", "ready"},
		"pending reentry": {false, "G2", "ready"},
		"blocked gate":    {false, "", "blocked"},
		"invalidated":     {false, "", "invalidated"},
	}
	for name, scenario := range blocked {
		err := CheckPublishEligibility(scenario.halted, scenario.reentry, scenario.status)
		if err == nil {
			t.Errorf("%s: publishing was permitted", name)
			continue
		}
		if _, isBlocked := err.(Blocked); !isBlocked {
			t.Errorf("%s: error is %T, want Blocked so a caller can tell it from a defect", name, err)
		}
	}
}
