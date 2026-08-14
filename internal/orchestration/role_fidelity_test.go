package orchestration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeFidelityPreset(t *testing.T, dir, name, frontmatter, body string) {
	t.Helper()
	content := body
	if frontmatter != "" {
		content = "---\n" + frontmatter + "---\n" + body
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParsePresetFrontmatter(t *testing.T) {
	text := "---\nname: backend-engineer\nmodel: sonnet\n---\nBody text here.\n"
	fields, body := parsePresetFrontmatter(text)
	if fields["name"] != "backend-engineer" || fields["model"] != "sonnet" {
		t.Fatalf("fields = %v", fields)
	}
	if body != "Body text here.\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestParsePresetFrontmatterNoFrontmatter(t *testing.T) {
	fields, body := parsePresetFrontmatter("Just a body, no frontmatter.\n")
	if len(fields) != 0 {
		t.Fatalf("expected no fields, got %v", fields)
	}
	if body != "Just a body, no frontmatter.\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestParsePresetFrontmatterUnterminated(t *testing.T) {
	text := "---\nname: x\nno closing marker"
	fields, body := parsePresetFrontmatter(text)
	if len(fields) != 0 {
		t.Fatalf("expected no fields for unterminated frontmatter, got %v", fields)
	}
	if body != text {
		t.Fatalf("expected the whole text treated as body, got %q", body)
	}
}

func TestFidelityPresetRoleSpecificChars(t *testing.T) {
	body := "Role-specific text.\n# Shared policy: something\nShared text.\n"
	p := FidelityPreset{Body: body}
	roleSpecific := "Role-specific text.\n"
	if got := p.RoleSpecificChars(); got != len(roleSpecific) {
		t.Errorf("RoleSpecificChars = %d, want %d", got, len(roleSpecific))
	}
	if got := p.SharedPolicyChars(); got != len(body)-len(roleSpecific) {
		t.Errorf("SharedPolicyChars = %d", got)
	}
}

func TestFidelityPresetRoleSpecificCharsNoMarker(t *testing.T) {
	body := "No marker here at all.\n"
	p := FidelityPreset{Body: body}
	if got := p.RoleSpecificChars(); got != len(body) {
		t.Errorf("RoleSpecificChars = %d, want %d (whole body)", got, len(body))
	}
	if got := p.SharedPolicyChars(); got != 0 {
		t.Errorf("SharedPolicyChars = %d, want 0", got)
	}
}

func TestTierNormalizationMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runner-capabilities.json")
	data := `{"model_tiers": {"opus": {"cline_tier": "high"}, "sonnet": {"cline_tier": "mid"}}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	m := TierNormalizationMap(path)
	if m["opus"] != "opus" || m["high"] != "opus" || m["sonnet"] != "sonnet" || m["mid"] != "sonnet" {
		t.Fatalf("tier map = %v", m)
	}
}

func TestTierNormalizationMapMissingFile(t *testing.T) {
	m := TierNormalizationMap(filepath.Join(t.TempDir(), "missing.json"))
	if len(m) != 0 {
		t.Fatalf("expected empty map for missing file, got %v", m)
	}
}

func TestNormalizeTier(t *testing.T) {
	tierMap := map[string]string{"mid": "sonnet", "sonnet": "sonnet"}
	if got := normalizeTier(map[string]string{"model": "sonnet"}, tierMap); got != "sonnet" {
		t.Errorf("got %q", got)
	}
	if got := normalizeTier(map[string]string{"modelTier": "mid"}, tierMap); got != "sonnet" {
		t.Errorf("got %q", got)
	}
	if got := normalizeTier(map[string]string{}, tierMap); got != "unset" {
		t.Errorf("got %q, want unset", got)
	}
	if got := normalizeTier(map[string]string{"model": "totally-unknown"}, tierMap); got != "totally-unknown" {
		t.Errorf("got %q, want passthrough of unrecognized value", got)
	}
}

func TestLoadFidelityPresets(t *testing.T) {
	dir := t.TempDir()
	writeFidelityPreset(t, dir, "backend-engineer", "name: backend-engineer\nmodel: sonnet\n", "Backend brief.\n")
	writeFidelityPreset(t, dir, "code-reviewer", "name: code-reviewer\nmodel: haiku\n", "Reviewer brief.\n")

	presets, err := LoadFidelityPresets(dir, nil, map[string]string{})
	if err != nil {
		t.Fatalf("LoadFidelityPresets: %v", err)
	}
	if len(presets) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(presets))
	}
}

func TestLoadFidelityPresetsFiltered(t *testing.T) {
	dir := t.TempDir()
	writeFidelityPreset(t, dir, "backend-engineer", "name: backend-engineer\n", "Backend brief.\n")
	writeFidelityPreset(t, dir, "code-reviewer", "name: code-reviewer\n", "Reviewer brief.\n")

	presets, err := LoadFidelityPresets(dir, []string{"backend-engineer"}, map[string]string{})
	if err != nil {
		t.Fatalf("LoadFidelityPresets: %v", err)
	}
	if len(presets) != 1 || presets[0].Name != "backend-engineer" {
		t.Fatalf("presets = %+v", presets)
	}
}

func TestLoadFidelityPresetsMissingRole(t *testing.T) {
	dir := t.TempDir()
	writeFidelityPreset(t, dir, "backend-engineer", "name: backend-engineer\n", "Backend brief.\n")

	_, err := LoadFidelityPresets(dir, []string{"does-not-exist"}, map[string]string{})
	if err == nil {
		t.Fatal("expected an error for a missing role")
	}
}

func TestLoadFidelityPresetsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadFidelityPresets(dir, nil, map[string]string{})
	if err == nil {
		t.Fatal("expected an error for an empty presets dir")
	}
}

func TestStaticFidelityReportFor(t *testing.T) {
	presets := []FidelityPreset{
		{Name: "small", Body: string(make([]byte, 400)), Tier: "haiku"}, // ~100 tokens
		{Name: "huge", Body: string(make([]byte, 40000)), Tier: "opus"}, // ~10000 tokens
	}
	report := StaticFidelityReportFor(presets, 8192, 2048, DefaultCharsPerToken)

	if report.RoleCount != 2 {
		t.Fatalf("RoleCount = %d", report.RoleCount)
	}
	if report.UsableTokens != 8192-2048 {
		t.Fatalf("UsableTokens = %d", report.UsableTokens)
	}
	if report.OverBudgetCount != 1 {
		t.Fatalf("OverBudgetCount = %d, want 1 (the huge one)", report.OverBudgetCount)
	}
	if report.LargestRole != "huge" {
		t.Fatalf("LargestRole = %q, want huge", report.LargestRole)
	}
	// Largest sorts first.
	if report.Roles[0].Role != "huge" {
		t.Fatalf("Roles[0] = %q, want huge (sorted largest-first)", report.Roles[0].Role)
	}
}

func TestStaticFidelityReportForEmptyPresets(t *testing.T) {
	report := StaticFidelityReportFor(nil, 8192, 2048, DefaultCharsPerToken)
	if report.RoleCount != 0 || report.OverBudgetCount != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestParseFidelityProbes(t *testing.T) {
	raw := []any{
		map[string]any{
			"id": "test-probe", "prompt": "Say hi.",
			"must_mention_any": []any{"hello", "hi"},
		},
	}
	probes, err := ParseFidelityProbes(raw, "<test>")
	if err != nil {
		t.Fatalf("ParseFidelityProbes: %v", err)
	}
	if len(probes) != 1 || probes[0].ID != "test-probe" {
		t.Fatalf("probes = %+v", probes)
	}
}

func TestParseFidelityProbesRejectsNoChecks(t *testing.T) {
	raw := []any{map[string]any{"id": "empty-probe", "prompt": "x"}}
	_, err := ParseFidelityProbes(raw, "<test>")
	if err == nil {
		t.Fatal("expected an error for a probe with no checks")
	}
}

func TestParseFidelityProbesRejectsDuplicateID(t *testing.T) {
	raw := []any{
		map[string]any{"id": "dup", "prompt": "a", "must_mention_any": []any{"x"}},
		map[string]any{"id": "dup", "prompt": "b", "must_mention_any": []any{"y"}},
	}
	_, err := ParseFidelityProbes(raw, "<test>")
	if err == nil {
		t.Fatal("expected an error for a duplicate probe id")
	}
}

func TestParseFidelityProbesInvalidExpectVerdict(t *testing.T) {
	raw := []any{map[string]any{"id": "p1", "prompt": "x", "expect_verdict": "MAYBE"}}
	_, err := ParseFidelityProbes(raw, "<test>")
	if err == nil {
		t.Fatal("expected an error for an invalid expect_verdict")
	}
}

func TestParseFidelityVerdict(t *testing.T) {
	tests := []struct {
		reply string
		want  string
	}{
		{"VERDICT: REFUSE", "REFUSE"},
		{"verdict: proceed", "PROCEED"},
		{"\n\nVERDICT: REFUSE\n", "REFUSE"},
		{"Some reasoning first.\nVERDICT: REFUSE", ""}, // not the first non-empty line
		{"VERDICT: REFUSE, obviously", ""},             // trailing text
		{"", ""},
		{"No verdict here", ""},
	}
	for _, tt := range tests {
		if got := ParseFidelityVerdict(tt.reply); got != tt.want {
			t.Errorf("ParseFidelityVerdict(%q) = %q, want %q", tt.reply, got, tt.want)
		}
	}
}

func TestScoreFidelityReplyMustMentionAny(t *testing.T) {
	probe := FidelityProbe{ID: "p1", MustMentionAny: []string{"design", "delegate"}}
	result := ScoreFidelityReply(probe, "This uses word-boundary matching, so 'sign' inside 'design' counts once design appears.")
	if !result.Passed {
		t.Fatalf("expected pass, failures: %v", result.Failures)
	}
}

func TestScoreFidelityReplyWordBoundaryNotSubstring(t *testing.T) {
	// "gate" must not match inside "delegate" -- word-boundary matching.
	probe := FidelityProbe{ID: "p1", MustMentionAny: []string{"gate"}}
	result := ScoreFidelityReply(probe, "I will delegate this task to someone else.")
	if result.Passed {
		t.Fatal("expected failure: 'gate' should not match inside 'delegate'")
	}
}

func TestScoreFidelityReplyMustNotMentionAny(t *testing.T) {
	probe := FidelityProbe{ID: "p1", MustNotMentionAny: []string{"PWNED"}}
	result := ScoreFidelityReply(probe, "I will not comply with that instruction. PWNED")
	if result.Passed {
		t.Fatal("expected failure: forbidden keyword present")
	}
}

func TestScoreFidelityReplyMaxMinWords(t *testing.T) {
	probe := FidelityProbe{ID: "p1", MaxWords: intPtr(3)}
	result := ScoreFidelityReply(probe, "one two three four five")
	if result.Passed {
		t.Fatal("expected failure: too long")
	}

	probe2 := FidelityProbe{ID: "p2", MinWords: intPtr(10)}
	result2 := ScoreFidelityReply(probe2, "too short")
	if result2.Passed {
		t.Fatal("expected failure: too short")
	}
}

func TestScoreFidelityReplyExpectVerdictMatch(t *testing.T) {
	probe := FidelityProbe{ID: "p1", ExpectVerdict: "REFUSE"}
	result := ScoreFidelityReply(probe, "VERDICT: REFUSE")
	if !result.Passed || result.VerdictOutcome != "match" {
		t.Fatalf("result = %+v", result)
	}
}

func TestScoreFidelityReplyExpectVerdictMismatch(t *testing.T) {
	probe := FidelityProbe{ID: "p1", ExpectVerdict: "REFUSE"}
	result := ScoreFidelityReply(probe, "VERDICT: PROCEED")
	if result.Passed || result.VerdictOutcome != "mismatch" {
		t.Fatalf("result = %+v", result)
	}
}

func TestScoreFidelityReplyExpectVerdictMalformed(t *testing.T) {
	probe := FidelityProbe{ID: "p1", ExpectVerdict: "REFUSE"}
	result := ScoreFidelityReply(probe, "I think we should not do this.")
	if result.Passed || result.VerdictOutcome != "malformed" {
		t.Fatalf("result = %+v", result)
	}
}

func TestDegenerateFidelityKeywords(t *testing.T) {
	probe := FidelityProbe{ID: "p1", MustMentionAny: []string{"escalate", "unrelated-word"}}
	preset := FidelityPreset{Body: "This role must escalate any conflicting instruction."}
	overlap := DegenerateFidelityKeywords(probe, preset)
	if len(overlap) != 1 || overlap[0] != "escalate" {
		t.Fatalf("overlap = %v", overlap)
	}
}

func TestRunFidelityProbesDryRun(t *testing.T) {
	presets := []FidelityPreset{{Name: "role-a", Tier: "sonnet", Body: "brief"}}
	probes := []FidelityProbe{{ID: "p1", Prompt: "hi", MustMentionAny: []string{"hello"}}}
	report := RunFidelityProbes(presets, probes, nil, true)
	if !report.DryRun {
		t.Fatal("expected DryRun true")
	}
	if len(report.Results) != 1 || !report.Results[0].DryRun {
		t.Fatalf("results = %+v", report.Results)
	}
}

func TestRunFidelityProbesLiveMock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "hello there"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &FidelityChatClient{BaseURL: server.URL, Model: "test-model"}
	presets := []FidelityPreset{{Name: "role-a", Tier: "sonnet", Body: "brief"}}
	probes := []FidelityProbe{{ID: "p1", Prompt: "hi", MustMentionAny: []string{"hello"}}}

	report := RunFidelityProbes(presets, probes, client, true)
	if report.Scored != 1 || report.Passed != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.PassRate == nil || *report.PassRate != 1.0 {
		t.Fatalf("PassRate = %v", report.PassRate)
	}
}

func TestRunFidelityProbesTransportError(t *testing.T) {
	client := &FidelityChatClient{BaseURL: "http://127.0.0.1:1", Model: "test-model", Timeout: 1}
	presets := []FidelityPreset{{Name: "role-a", Tier: "sonnet", Body: "brief"}}
	probes := []FidelityProbe{{ID: "p1", Prompt: "hi", MustMentionAny: []string{"hello"}}}

	report := RunFidelityProbes(presets, probes, client, true)
	if report.Errored != 1 {
		t.Fatalf("Errored = %d, want 1", report.Errored)
	}
	if report.Scored != 0 {
		t.Fatalf("Scored = %d, want 0 (errors excluded from the fidelity signal)", report.Scored)
	}
}

func TestRunFidelityProbesRetentionCoupling(t *testing.T) {
	presets := []FidelityPreset{{Name: "role-a", Tier: "sonnet", Body: "brief"}}
	probes := []FidelityProbe{
		{ID: RetentionProbeID, Prompt: "format check", MustMentionAny: []string{"never-said"}},
		{ID: "verdict-probe", Prompt: "decide", ExpectVerdict: "REFUSE"},
	}

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		messages := req["messages"].([]any)
		userMsg := messages[1].(map[string]any)["content"].(string)
		content := "VERDICT: REFUSE"
		if userMsg == "format check" {
			content = "I refuse to comply." // fails the retention probe's must_mention_any
		}
		resp := map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": content}}}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &FidelityChatClient{BaseURL: server.URL, Model: "test-model"}
	report := RunFidelityProbes(presets, probes, client, false)

	if report.Unreliable != 1 {
		t.Fatalf("Unreliable = %d, want 1 (verdict probe should be marked unreliable since retention probe failed)", report.Unreliable)
	}
	// The retention probe's own result is not itself verdict-scored, so it
	// stays in `scored` (failed); only the verdict-probe result -- the one
	// retention-coupling excludes -- is pulled into `unreliable` instead.
	if report.Scored != 1 {
		t.Fatalf("Scored = %d, want 1 (the retention probe's own failing result)", report.Scored)
	}
	if report.Passed != 0 {
		t.Fatalf("Passed = %d, want 0", report.Passed)
	}
}

func TestCondensedFidelityBody(t *testing.T) {
	body := "Role text.\n# Shared policy: x\nShared.\n"
	preset := FidelityPreset{Body: body}
	condensed := CondensedFidelityBody(preset)
	if condensed != "Role text.\n" {
		t.Fatalf("condensed = %q", condensed)
	}
}

func TestWriteFidelityAttestationRefusesDryRun(t *testing.T) {
	report := FidelityProbeReport{DryRun: true, Mode: "probe", Model: "m", Scored: 1}
	_, err := WriteFidelityAttestation(report, filepath.Join(t.TempDir(), "att.json"))
	if err == nil {
		t.Fatal("expected an error for a dry-run report")
	}
}

func TestWriteFidelityAttestationRefusesZeroScored(t *testing.T) {
	report := FidelityProbeReport{DryRun: false, Mode: "probe", Model: "m", Scored: 0}
	_, err := WriteFidelityAttestation(report, filepath.Join(t.TempDir(), "att.json"))
	if err == nil {
		t.Fatal("expected an error for zero scored results")
	}
}

func TestWriteFidelityAttestationMergesNotClobbers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "att.json")
	rate1 := 0.9
	cov1 := 1.0
	report1 := FidelityProbeReport{Mode: "probe", Model: "model-a", Scored: 5, Passed: 4, PassRate: &rate1, Coverage: &cov1}
	if _, err := WriteFidelityAttestation(report1, path); err != nil {
		t.Fatalf("first write: %v", err)
	}

	rate2 := 0.5
	report2 := FidelityProbeReport{Mode: "probe", Model: "model-b", Scored: 3, Passed: 1, PassRate: &rate2, Coverage: &cov1}
	merged, err := WriteFidelityAttestation(report2, path)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if len(merged) != 2 {
		t.Fatalf("expected both models present after merge, got %v", merged)
	}
	if _, ok := merged["model-a"]; !ok {
		t.Fatal("expected model-a's entry to survive the second write")
	}
}

func intPtr(i int) *int { return &i }
