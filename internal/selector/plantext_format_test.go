package selector

import (
	"encoding/json"
	"strings"
	"testing"
)

// What the text rendering promises about the plan it is showing.
//
// plantext_test.go pins the wrapping primitive and the blocks that were
// measured against Python. This covers the properties stated at the top of
// roster/orchestration/test/test_plan_text_format.py, which the Go side never
// had -- most of all the headline one: **the renderer invents nothing.** It is
// a pure function of the plan it is handed. A formatter that re-read
// routing.json, or re-derived a field, could disagree with the JSON it claims
// to be showing, which is the worst available failure for a command whose
// entire value is being reproducible.
//
// routeSummary, sortedUnique and containsString were at 0% before this file.

// fullPlan is a plan with something in every block the renderer draws, so a
// block quietly disappearing shows up here rather than in nobody's test.
func fullPlan() map[string]any {
	return map[string]any{
		"schema_version": SchemaVersion,
		"task_id":        "TASK-7",
		"status":         "ready",
		"workflow":       "production-release",
		"inputs": map[string]any{
			"task":          "rotate the signing keys",
			"changed_files": []any{"terraform/prod/main.tf"},
		},
		"matched_routes": []any{map[string]any{
			"id": "infrastructure",
			"reasons": map[string]any{
				"keywords":       []any{"terraform", "rotate", "signing", "keys"},
				"keyword_groups": []any{},
				"paths": []any{
					map[string]any{"pattern": "terraform/**", "file": "terraform/prod/main.tf"},
					map[string]any{"pattern": "**/*.tf", "file": "terraform/prod/main.tf"},
					map[string]any{"pattern": "infra/**", "file": "terraform/prod/main.tf"},
				},
			},
		}},
		"matched_risks": []any{map[string]any{"id": "production"}},
		"agents": map[string]any{
			"primary":   []any{"kubernetes-manifest-implementer"},
			"reviewers": []any{"security-reviewer"},
			"support":   []any{"release-engineer"},
		},
		"dispatch_disposition": map[string]any{
			"status": "staffed", "reason": "A primary was selected.",
		},
		"teams": []any{map[string]any{
			"id": "parallel-review", "type": "fixed",
			"members":            []any{"code-reviewer", "security-reviewer"},
			"communication_mode": "peer",
		}},
		"human_gates": []any{map[string]any{
			"id": "production-change", "required": true,
			"reason": "An authorized human must approve the target.",
		}},
		"dispatch_fingerprint": "sha256:abc123",
	}
}

func TestTheTextRenderingDoesNotMutateThePlan(t *testing.T) {
	// The renderer normalizes the plan through JSON before reading it, which
	// is the kind of step that mutates in place if it is written slightly
	// differently. `cadre select --format text --output f` renders and then
	// writes, so a mutation here would corrupt the file the caller keeps.
	plan := fullPlan()
	before, err := json.Marshal(sortedPlanFor(t, plan))
	if err != nil {
		t.Fatal(err)
	}
	FormatPlanText(plan)
	after, err := json.Marshal(sortedPlanFor(t, plan))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("rendering changed the plan.\nbefore: %s\nafter:  %s", before, after)
	}
}

func sortedPlanFor(t *testing.T, plan map[string]any) any {
	t.Helper()
	encoded, err := CanonicalJSON(plan)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestTheTextRenderingNamesEveryAgentAndNeverSplitsAnID(t *testing.T) {
	// plantext_test.go asserts this of textwrapFill. That leaves the way
	// FormatPlanText *calls* it untested: passing a width that ignores the
	// indent, or reaching for a different wrapper for one block, breaks the
	// property with the primitive's own test still green.
	//
	// Asserted on a deliberately long roster, since short ones never reach
	// the margin and would make this pass without wrapping anything.
	plan := fullPlan()
	roster := []any{
		"kubernetes-manifest-implementer",
		"supply-chain-security-reviewer",
		"opentofu-module-implementer",
		"network-management-automation-implementer",
		"embedded-linux-platform-implementer",
		"quantum-network-integration-implementer",
	}
	plan["agents"] = map[string]any{
		"primary": roster, "reviewers": []any{}, "support": []any{},
	}
	rendered := FormatPlanText(plan)

	if !strings.Contains(rendered, "\n") || len(strings.Split(rendered, "\n")) < 8 {
		t.Fatalf("the rendering is too short to have wrapped anything:\n%s", rendered)
	}
	wrapped := false
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasSuffix(strings.TrimRight(line, " "), "-") {
			t.Errorf("a hyphenated id was split across lines: %q", line)
		}
		if len(line) > 60 {
			wrapped = true
		}
	}
	for _, agent := range roster {
		if !strings.Contains(rendered, agent.(string)) {
			t.Errorf("%v did not survive wrapping:\n%s", agent, rendered)
		}
	}
	if !wrapped {
		t.Errorf("no line came near the margin, so the split-id check above "+
			"passed without the wrapper running at all:\n%s", rendered)
	}
}

func TestTheTextRenderingShowsWhyEachRouteMatched(t *testing.T) {
	// The ROUTES block is the answer to "why these agents?". Rendering only
	// the route id turns a reproducible decision back into an assertion.
	rendered := FormatPlanText(fullPlan())
	if !strings.Contains(rendered, "infrastructure") {
		t.Fatalf("the matched route is not named:\n%s", rendered)
	}
	for _, keyword := range []string{`"keys"`, `"rotate"`, `"signing"`} {
		if !strings.Contains(rendered, keyword) {
			t.Errorf("keyword %s is not shown; the first three sort first "+
				"and must appear:\n%s", keyword, rendered)
		}
	}
	// Four keywords, the first three by sort order shown, "terraform" dropped.
	// Checked by what is *absent*: asserting only on "+1 more" would be
	// satisfied by either cap alone, so a broken keyword cap would pass on the
	// pattern cap's message.
	if strings.Contains(rendered, `"terraform"`) {
		t.Errorf("the keyword list is capped at three and showed a fourth:\n%s", rendered)
	}
	// Three patterns, two shown, "infra/**" dropped.
	if !strings.Contains(rendered, "terraform/**") || !strings.Contains(rendered, "**/*.tf") {
		t.Errorf("the matched path patterns are not shown:\n%s", rendered)
	}
	if strings.Contains(rendered, "infra/**") {
		t.Errorf("the pattern list is capped at two and showed a third:\n%s", rendered)
	}
	// And both caps say how many they dropped, rather than truncating silently.
	if got := strings.Count(rendered, "+1 more"); got != 2 {
		t.Errorf("counted %d \"+1 more\" markers, want one per capped list:\n%s",
			got, rendered)
	}
}

func TestARouteThatMatchedOnNothingRendersAsItsIDAlone(t *testing.T) {
	// A route can be selected by a rule that carries no keywords or paths.
	// Rendering "id ()" for it would read as a summary that failed to load.
	rendered := FormatPlanText(map[string]any{
		"status": "ready", "task_id": "T-3",
		"agents":         map[string]any{"primary": []any{"backend-engineer"}},
		"matched_routes": []any{map[string]any{"id": "risk-only-route"}},
	})
	if !strings.Contains(rendered, "risk-only-route") {
		t.Fatalf("the route is not named:\n%s", rendered)
	}
	if strings.Contains(rendered, "risk-only-route (") {
		t.Errorf("a route that matched on nothing rendered an empty reason "+
			"list:\n%s", rendered)
	}
}

func TestADuplicatedPathPatternIsShownOnce(t *testing.T) {
	// Several changed files hitting one pattern is the common case, not an
	// edge one: the reasons list carries an entry per file. Repeating the
	// pattern would spend the two-pattern budget saying the same thing twice.
	rendered := FormatPlanText(map[string]any{
		"status": "ready", "task_id": "T-4",
		"agents": map[string]any{"primary": []any{"frontend-engineer"}},
		"matched_routes": []any{map[string]any{
			"id": "frontend",
			"reasons": map[string]any{"paths": []any{
				map[string]any{"pattern": "frontend/**", "file": "frontend/a.tsx"},
				map[string]any{"pattern": "frontend/**", "file": "frontend/b.tsx"},
				map[string]any{"pattern": "**/*.tsx", "file": "frontend/a.tsx"},
			}},
		}},
	})
	if got := strings.Count(rendered, "frontend/**"); got != 1 {
		t.Errorf("the pattern appears %d times, want 1:\n%s", got, rendered)
	}
	if !strings.Contains(rendered, "**/*.tsx") {
		t.Errorf("de-duplication dropped a distinct pattern:\n%s", rendered)
	}
}

func TestAHumanGateIsRenderedWithItsReasonNotJustItsID(t *testing.T) {
	// An id alone tells a reader that something is blocked without telling
	// them what to do about it, which is the whole content of the block.
	rendered := FormatPlanText(fullPlan())
	for _, want := range []string{
		"HUMAN APPROVAL REQUIRED", "production-change",
		"An authorized human must approve",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the gate block does not carry %q:\n%s", want, rendered)
		}
	}
}

func TestASparsePlanRendersRatherThanFailing(t *testing.T) {
	// A formatter is the wrong place to discover a schema change: erroring
	// here hides the whole plan rather than showing what it does have. An
	// older plan, or one truncated by a partial write, reaches this.
	for _, plan := range []map[string]any{
		{},
		{"status": "ready"},
		{"task_id": "X", "agents": map[string]any{}},
		{"matched_routes": []any{map[string]any{}}},
		// Wrong types where objects are expected -- what a hand-edited or
		// half-migrated plan looks like.
		{"agents": "not-an-object", "human_gates": "not-a-list"},
	} {
		rendered := FormatPlanText(plan)
		if !strings.HasSuffix(rendered, "\n") {
			t.Errorf("plan %v rendered without a trailing newline: %q", plan, rendered)
		}
		if strings.TrimSpace(rendered) == "" {
			t.Errorf("plan %v rendered as nothing at all", plan)
		}
	}
}
