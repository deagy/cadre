package selector

import (
	"strings"
	"testing"
)

// These pin what probe_text_parity.py measured against Python across 190
// cases (144 textwrap inputs, 35 plans, 11 near-miss configurations).

func TestTextwrapDoesNotSplitAHyphenatedRoleID(t *testing.T) {
	// textwrap's defaults break on hyphens, which wraps one role id across
	// two lines where it reads as two different roles that do not exist. The
	// formatter turns that off, and this is what would notice it coming back.
	roles := "kubernetes-manifest-implementer network-management-automation-implementer " +
		"quantum-network-integration-implementer"
	wrapped := textwrapFill(roles, 40, "", "")

	for _, line := range strings.Split(wrapped, "\n") {
		if strings.HasSuffix(line, "-") {
			t.Errorf("a line ends mid-id: %q\nfull output:\n%s", line, wrapped)
		}
	}
	for _, role := range strings.Fields(roles) {
		if !strings.Contains(wrapped, role) {
			t.Errorf("role %q did not survive wrapping:\n%s", role, wrapped)
		}
	}
}

func TestTextwrapLetsALongTokenOverrunRatherThanCuttingIt(t *testing.T) {
	// break_long_words=False: a token wider than the line goes on its own
	// line and overruns. Cutting it would produce two fragments that each
	// look like a real identifier.
	long := "supercalifragilisticexpialidociousandthensomemoreletters"
	wrapped := textwrapFill("short "+long, 20, "", "")
	if !strings.Contains(wrapped, long) {
		t.Errorf("the long token was broken up:\n%s", wrapped)
	}
}

func TestTextwrapMeasuresWidthInCharactersNotBytes(t *testing.T) {
	// Python counts characters. Measuring bytes would wrap accented or CJK
	// text early by however many continuation bytes it carried -- a
	// difference invisible in any ASCII test.
	ascii := textwrapFill(strings.Repeat("abcd ", 20), 40, "", "")
	accented := textwrapFill(strings.Repeat("café ", 20), 40, "", "")
	if got, want := len(strings.Split(accented, "\n")), len(strings.Split(ascii, "\n")); got != want {
		t.Errorf("accented text wrapped into %d lines, ASCII of the same character width into %d", got, want)
	}
}

func TestTextwrapDropsWhitespaceTheWayPythonDoes(t *testing.T) {
	if got := textwrapFill("", 78, "", ""); got != "" {
		t.Errorf("empty text = %q, want one empty line", got)
	}
	if got := textwrapFill("trailing   ", 78, "", ""); got != "trailing" {
		t.Errorf("= %q, want trailing whitespace dropped", got)
	}
	// A continuation line never begins with the space that separated it from
	// the previous word.
	wrapped := textwrapFill(strings.Repeat("word ", 40), 20, "", "")
	for _, line := range strings.Split(wrapped, "\n")[1:] {
		if strings.HasPrefix(line, " ") {
			t.Errorf("continuation line begins with whitespace: %q", line)
		}
	}
	// An explicit subsequent indent is applied, and is not whitespace-dropped.
	wrapped = textwrapFill(strings.Repeat("word ", 40), 20, "", "    ")
	for _, line := range strings.Split(wrapped, "\n")[1:] {
		if !strings.HasPrefix(line, "    ") {
			t.Errorf("continuation line lost its indent: %q", line)
		}
	}
}

func TestFormatPlanTextSaysWhenNothingWasSelected(t *testing.T) {
	// needs-triage is the case a JSON skim misreads: the plan is structurally
	// valid and every agent list is simply empty, which looks like success.
	rendered := FormatPlanText(map[string]any{
		"status": "needs-triage", "task_id": "T-1",
		"agents":               map[string]any{"primary": []any{}, "reviewers": []any{}, "support": []any{}},
		"dispatch_disposition": map[string]any{"status": "needs-triage", "reason": "Nothing matched."},
	})
	if !strings.Contains(rendered, "NO AGENTS SELECTED") {
		t.Errorf("an unstaffed plan must say so in words:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Nothing matched.") {
		t.Errorf("the plan's own reason must be shown:\n%s", rendered)
	}
	if !strings.Contains(rendered, "--explain") {
		t.Errorf("an unstaffed plan must point at --explain:\n%s", rendered)
	}
}

func TestFormatPlanTextMarksAnEmptyGroupExplicitly(t *testing.T) {
	// "(none)" rather than a bare "-": an empty reviewers slot is worth
	// noticing, and a lone dash at the end of a line reads as a hyphenated id
	// that wrapped.
	rendered := FormatPlanText(map[string]any{
		"status": "ok", "task_id": "T-2",
		"agents": map[string]any{"primary": []any{"backend-engineer"}, "reviewers": []any{}},
	})
	if !strings.Contains(rendered, "(none)") {
		t.Errorf("an empty agent group must render as (none):\n%s", rendered)
	}
}

func TestFormatPlanTextShowsOnlyRequiredGates(t *testing.T) {
	// A gate marked required:false is advisory; printing it under "HUMAN
	// APPROVAL REQUIRED" would claim an approval nobody owes.
	rendered := FormatPlanText(map[string]any{
		"status": "ok", "agents": map[string]any{"primary": []any{"release-engineer"}},
		"human_gates": []any{
			map[string]any{"id": "production-release", "required": true, "reason": "A human must approve."},
			map[string]any{"id": "advisory-only", "required": false, "reason": "not required"},
			// Absent `required` means required, matching the plan's default.
			map[string]any{"id": "defaults-to-required"},
		},
	})
	if !strings.Contains(rendered, "production-release") || !strings.Contains(rendered, "defaults-to-required") {
		t.Errorf("required gates must be shown:\n%s", rendered)
	}
	if strings.Contains(rendered, "advisory-only") {
		t.Errorf("a gate marked required:false must not appear under HUMAN APPROVAL REQUIRED:\n%s", rendered)
	}
}

func TestFormatPlanTextRendersAnEmptyPlanWithoutFailing(t *testing.T) {
	// A formatter is a poor place to discover a schema change, and an error
	// here would hide the plan entirely.
	rendered := FormatPlanText(map[string]any{})
	if rendered == "" || !strings.Contains(rendered, "(no --task-id given)") {
		t.Errorf("an empty plan must still render: %q", rendered)
	}
}

func TestFormatPlanTextTruncatesLongListsWithACount(t *testing.T) {
	files := make([]any, 12)
	for index := range files {
		files[index] = "file.go"
	}
	rendered := FormatPlanText(map[string]any{
		"status": "ok", "inputs": map[string]any{"changed_files": files},
		"agents": map[string]any{"primary": []any{"backend-engineer"}},
	})
	if !strings.Contains(rendered, "(+7 more)") {
		t.Errorf("a long file list must be truncated with a count:\n%s", rendered)
	}
}

func TestNearMissSurfacesOnlyAPartiallySatisfiedGroup(t *testing.T) {
	// The relevance threshold: 1 <= matched < len(group). A group at 0-of-N
	// is noise, and N-of-N would mean the route matched -- a contradiction
	// for a route reaching this code at all.
	config := map[string]any{"routes": []any{
		map[string]any{"id": "partial", "keyword_groups": []any{[]any{"deploy", "production"}}},
		map[string]any{"id": "zero", "keyword_groups": []any{[]any{"nothing", "here"}}},
		map[string]any{"id": "full", "keyword_groups": []any{[]any{"deploy", "service"}}},
		// Plain keywords have no partial state: had one fired, the route
		// would already be matched and would never reach here.
		map[string]any{"id": "plain", "keywords": []any{"deploy"}},
	}}

	got := FindNearMisses(config, "deploy the service", map[string]bool{})
	if len(got) != 1 || got[0].ID != "partial" {
		t.Fatalf("near misses = %+v, want only the partially satisfied route", got)
	}
	if strings.Join(got[0].Groups[0].Matched, ",") != "deploy" {
		t.Errorf("matched = %v", got[0].Groups[0].Matched)
	}
	if strings.Join(got[0].Groups[0].Missing, ",") != "production" {
		t.Errorf("missing = %v", got[0].Groups[0].Missing)
	}

	// An already-matched route is skipped entirely.
	if got := FindNearMisses(config, "deploy the service", map[string]bool{"partial": true}); len(got) != 0 {
		t.Errorf("near misses = %+v, want a matched route skipped", got)
	}
}

func TestNearMissTextIsDescriptiveNeverScored(t *testing.T) {
	// Selection here is deterministic, not judgment: a numeric confidence,
	// score, weight or ranking on a match was explicitly rejected, so the
	// rendering reports which literal keywords are present and nothing else.
	rendered := FormatNearMissesText([]NearMiss{{
		ID:     "partial",
		Groups: []NearMissGroup{{Matched: []string{"deploy"}, Missing: []string{"production"}}},
	}})
	for _, forbidden := range []string{"%", "score", "confidence", "rank", "weight"} {
		if strings.Contains(strings.ToLower(rendered), forbidden) {
			t.Errorf("near-miss text contains %q, which it must never emit:\n%s", forbidden, rendered)
		}
	}
	if !strings.Contains(rendered, "matched 1 of 2 required keywords (deploy); missing: production") {
		t.Errorf("unexpected rendering:\n%s", rendered)
	}

	// The empty case explains why it is empty rather than printing nothing,
	// because "no near misses" is a legitimate answer that otherwise reads
	// as a broken flag.
	empty := FormatNearMissesText(nil)
	if !strings.Contains(empty, "no near-miss routes") {
		t.Errorf("the empty case must explain itself:\n%s", empty)
	}
}
