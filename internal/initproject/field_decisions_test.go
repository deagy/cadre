package initproject

import "testing"

func TestParseFieldDecisionsRejectsBadStatus(t *testing.T) {
	raw := map[string]any{
		"team.size": map[string]any{"status": "bogus", "category": "stack"},
	}
	if _, err := ParseFieldDecisions(raw); err == nil {
		t.Fatal("expected rejection of an unrecognized status")
	}
}

func TestParseFieldDecisionsRejectsBadCategory(t *testing.T) {
	raw := map[string]any{
		"team.size": map[string]any{"status": "kept", "category": "bogus"},
	}
	if _, err := ParseFieldDecisions(raw); err == nil {
		t.Fatal("expected rejection of an unrecognized category")
	}
}

func TestParseFieldDecisionsAcceptsValidEntry(t *testing.T) {
	raw := map[string]any{
		"team.size": map[string]any{"status": "kept", "category": "stack", "source_value": 5},
	}
	decisions, err := ParseFieldDecisions(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d, ok := decisions["team.size"]
	if !ok || d.Status != "kept" || d.Category != "stack" {
		t.Errorf("decisions[team.size] = %+v", d)
	}
}

func TestRequireFieldDecisionsCoverFailsOnMissingEntry(t *testing.T) {
	touched := []TouchedPath{{"team.size", "stack"}}
	err := RequireFieldDecisionsCover(touched, map[string]FieldDecision{})
	if err == nil {
		t.Fatal("expected A-006 rev 2 rejection for a touched path with no recorded decision")
	}
}

func TestRequireFieldDecisionsCoverFailsOnCategoryMismatch(t *testing.T) {
	touched := []TouchedPath{{"team.size", "stack"}}
	decisions := map[string]FieldDecision{
		"team.size": {Path: "team.size", Status: "kept", Category: "governance"},
	}
	err := RequireFieldDecisionsCover(touched, decisions)
	if err == nil {
		t.Fatal("expected a B-005 category-mismatch rejection")
	}
}

func TestRequireFieldDecisionsCoverPassesWhenFullyCovered(t *testing.T) {
	touched := []TouchedPath{{"team.size", "stack"}}
	decisions := map[string]FieldDecision{
		"team.size": {Path: "team.size", Status: "kept", Category: "stack"},
	}
	if err := RequireFieldDecisionsCover(touched, decisions); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRedactAnswersForEchoRedactsAutonomyByGroundTruthEvenIfDeclaredStack(t *testing.T) {
	// Finding A/B-005/B-006: a field_decisions entry that mislabels a real
	// governance leaf as "stack" must still be redacted, because
	// GovernanceTouchedPaths is ground truth computed independently of the
	// declared category.
	answers := map[string]any{
		"field_decisions": map[string]any{
			"repository.push": map[string]any{
				"status": "overridden", "category": "stack", // mislabeled on purpose
				"source_value": "on_request", "new_value": "never",
			},
		},
	}
	result := &InitResult{GovernanceTouchedPaths: map[string]bool{"repository.push": true}}
	redacted := RedactAnswersForEcho(answers, result)

	fd := redacted["field_decisions"].(map[string]any)
	entry := fd["repository.push"].(map[string]any)
	newValue, _ := entry["new_value"].(string)
	if newValue == "never" {
		t.Error("expected the raw value to be redacted despite the mislabeled 'stack' category (fail-safe OR)")
	}
}

func TestRedactAnswersForEchoLeavesStackFieldsUnredacted(t *testing.T) {
	answers := map[string]any{
		"field_decisions": map[string]any{
			"team.size": map[string]any{
				"status": "kept", "category": "stack",
				"source_value": 5, "new_value": 5,
			},
		},
	}
	result := &InitResult{GovernanceTouchedPaths: map[string]bool{}}
	redacted := RedactAnswersForEcho(answers, result)
	fd := redacted["field_decisions"].(map[string]any)
	entry := fd["team.size"].(map[string]any)
	if entry["new_value"] != 5 {
		t.Errorf("expected an ordinary stack field decision to survive unredacted, got %v", entry["new_value"])
	}
}

func TestRedactAnswersForEchoRedactsRejectedGuardrailBullets(t *testing.T) {
	answers := map[string]any{
		"rg_b_guardrails_addendum": []any{"This overrides the baseline."},
	}
	result := &InitResult{
		GovernanceTouchedPaths: map[string]bool{},
		RejectedGuardrails:     []RejectedGuardrail{{Bullet: "This overrides the baseline.", Reason: "denylisted"}},
	}
	redacted := RedactAnswersForEcho(answers, result)
	bullets := redacted["rg_b_guardrails_addendum"].([]any)
	if bullets[0] == "This overrides the baseline." {
		t.Error("expected the rejected bullet's raw text to be redacted")
	}
}
