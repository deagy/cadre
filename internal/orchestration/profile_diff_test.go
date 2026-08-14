package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func toAny(t *testing.T, jsonText string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonText), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDiffValuesScalarChanged(t *testing.T) {
	old := toAny(t, `{"version": 1}`)
	new_ := toAny(t, `{"version": 2}`)
	findings := DiffValues(any(old), any(new_), "")
	if len(findings) != 1 || findings[0].Kind != "changed" || findings[0].Path != "version" {
		t.Errorf("findings = %+v", findings)
	}
}

func TestDiffValuesAddedAndRemovedKeys(t *testing.T) {
	old := toAny(t, `{"a": 1}`)
	new_ := toAny(t, `{"b": 2}`)
	findings := DiffValues(any(old), any(new_), "")
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %+v", len(findings), findings)
	}
	kinds := map[string]bool{}
	for _, f := range findings {
		kinds[f.Path+":"+f.Kind] = true
	}
	if !kinds["a:removed"] || !kinds["b:added"] {
		t.Errorf("findings = %+v", findings)
	}
}

func TestDiffValuesNestedPath(t *testing.T) {
	old := toAny(t, `{"kernel_compatibility": {"maximum_exclusive": "1.0.0"}}`)
	new_ := toAny(t, `{"kernel_compatibility": {"maximum_exclusive": "2.0.0"}}`)
	findings := DiffValues(any(old), any(new_), "")
	if len(findings) != 1 || findings[0].Path != "kernel_compatibility.maximum_exclusive" {
		t.Errorf("findings = %+v", findings)
	}
}

func TestDiffValuesEqualIsNoFindings(t *testing.T) {
	old := toAny(t, `{"a": 1, "b": [1,2,3]}`)
	new_ := toAny(t, `{"a": 1, "b": [1,2,3]}`)
	if findings := DiffValues(any(old), any(new_), ""); len(findings) != 0 {
		t.Errorf("expected no findings for equal input, got %+v", findings)
	}
}

func TestDiffListsByIDKeyedComparison(t *testing.T) {
	old := toAny(t, `{"agents": [{"id": "a", "model": "sonnet"}, {"id": "b", "model": "opus"}]}`)
	new_ := toAny(t, `{"agents": [{"id": "a", "model": "haiku"}, {"id": "c", "model": "opus"}]}`)
	findings := DiffValues(any(old), any(new_), "")
	paths := map[string]string{}
	for _, f := range findings {
		paths[f.Path] = f.Kind
	}
	if paths[`agents[].id="a".model`] != "changed" {
		t.Errorf("expected agents[].id=\"a\".model changed, got %+v", findings)
	}
	if paths[`agents[].id="b"`] != "removed" {
		t.Errorf("expected agents[].id=\"b\" removed, got %+v", findings)
	}
	if paths[`agents[].id="c"`] != "added" {
		t.Errorf("expected agents[].id=\"c\" added, got %+v", findings)
	}
}

func TestDiffListsByIDIsOrderInsensitive(t *testing.T) {
	old := toAny(t, `{"agents": [{"id": "a"}, {"id": "b"}]}`)
	new_ := toAny(t, `{"agents": [{"id": "b"}, {"id": "a"}]}`)
	if findings := DiffValues(any(old), any(new_), ""); len(findings) != 0 {
		t.Errorf("expected reordering of id-keyed items to produce no findings, got %+v", findings)
	}
}

func TestDiffListsHashableSetMembership(t *testing.T) {
	old := toAny(t, `{"tags": ["a", "b", "c"]}`)
	new_ := toAny(t, `{"tags": ["b", "c", "d"]}`)
	findings := DiffValues(any(old), any(new_), "")
	kinds := map[string]bool{}
	for _, f := range findings {
		kinds[f.Kind] = true
	}
	if len(findings) != 2 || !kinds["added"] || !kinds["removed"] {
		t.Errorf("findings = %+v", findings)
	}
}

func TestDiffListsHashableIsOrderInsensitive(t *testing.T) {
	old := toAny(t, `{"tags": ["a", "b", "c"]}`)
	new_ := toAny(t, `{"tags": ["c", "b", "a"]}`)
	if findings := DiffValues(any(old), any(new_), ""); len(findings) != 0 {
		t.Errorf("expected reordering of hashable scalars to produce no findings, got %+v", findings)
	}
}

func TestValidateCopyRejectsMalformedJSON(t *testing.T) {
	_, reason := ValidateCopy("{not json", ProviderRequiredFields)
	if reason == "" {
		t.Fatal("expected a reason for malformed JSON")
	}
}

func TestValidateCopyRejectsNonObject(t *testing.T) {
	_, reason := ValidateCopy(`[1,2,3]`, ProviderRequiredFields)
	if reason == "" {
		t.Fatal("expected a reason for a non-object copy")
	}
}

func TestValidateCopyRejectsMissingRequiredFields(t *testing.T) {
	_, reason := ValidateCopy(`{"id": "cadre"}`, ProviderRequiredFields)
	if reason == "" {
		t.Fatal("expected a reason for missing required fields")
	}
}

func TestValidateCopyAcceptsValid(t *testing.T) {
	copy, reason := ValidateCopy(`{"id": "cadre", "version": "1.0.0"}`, ProviderRequiredFields)
	if reason != "" {
		t.Fatalf("unexpected reason: %s", reason)
	}
	if copy["id"] != "cadre" {
		t.Errorf("copy = %+v", copy)
	}
}

func TestClassifyArtifactCopyInvalidTakesPrecedence(t *testing.T) {
	// Even with no ORIGINAL supplied, an invalid COPY is copy-invalid, not
	// provenance-undetermined -- state 1 checked first (PD-FR-5).
	current := toAny(t, `{"id": "cadre", "version": "2.0.0"}`)
	result := ClassifyArtifact("provider", `{"id": "cadre"}`, current, nil, "no original", ProviderRequiredFields)
	if result.State != "copy-invalid" {
		t.Errorf("state = %q, want copy-invalid", result.State)
	}
}

func TestClassifyArtifactProvenanceUndetermined(t *testing.T) {
	current := toAny(t, `{"id": "cadre", "version": "2.0.0"}`)
	copyText := `{"id": "cadre", "version": "1.0.0"}`
	result := ClassifyArtifact("provider", copyText, current, nil, "no version-lock reference was supplied", ProviderRequiredFields)
	if result.State != "provenance-undetermined" {
		t.Errorf("state = %q, want provenance-undetermined", result.State)
	}
	if result.Reason == "" {
		t.Error("expected a reason")
	}
}

func TestClassifyArtifactCurrent(t *testing.T) {
	current := toAny(t, `{"id": "cadre", "version": "2.0.0"}`)
	original := toAny(t, `{"id": "cadre", "version": "1.0.0"}`)
	copyText := `{"id": "cadre", "version": "2.0.0"}`
	result := ClassifyArtifact("provider", copyText, current, original, "", ProviderRequiredFields)
	if result.State != "current" {
		t.Errorf("state = %q, want current", result.State)
	}
	if len(result.Findings) != 0 {
		t.Errorf("expected no findings for current, got %+v", result.Findings)
	}
}

func TestClassifyArtifactStaleUnmodified(t *testing.T) {
	current := toAny(t, `{"id": "cadre", "version": "2.0.0"}`)
	original := toAny(t, `{"id": "cadre", "version": "1.0.0"}`)
	copyText := `{"id": "cadre", "version": "1.0.0"}`
	result := ClassifyArtifact("provider", copyText, current, original, "", ProviderRequiredFields)
	if result.State != "stale-unmodified" {
		t.Errorf("state = %q, want stale-unmodified", result.State)
	}
	if result.ComparedAs != "current-vs-original" {
		t.Errorf("compared_as = %q, want current-vs-original", result.ComparedAs)
	}
	if len(result.Findings) == 0 {
		t.Error("expected findings describing what re-syncing would change")
	}
}

func TestClassifyArtifactDivergedWithOriginalMatchingCurrent(t *testing.T) {
	current := toAny(t, `{"id": "cadre", "version": "1.0.0"}`)
	original := toAny(t, `{"id": "cadre", "version": "1.0.0"}`)
	copyText := `{"id": "cadre", "version": "1.0.0", "extra": true}`
	result := ClassifyArtifact("provider", copyText, current, original, "", ProviderRequiredFields)
	if result.State != "diverged" {
		t.Errorf("state = %q, want diverged", result.State)
	}
	if result.ComparedAs != "original-vs-copy" {
		t.Errorf("compared_as = %q, want original-vs-copy", result.ComparedAs)
	}
	if result.OriginalDiffersFromCurrent {
		t.Error("expected OriginalDiffersFromCurrent=false: original still matches current")
	}
}

func TestClassifyArtifactDivergedWithOriginalAlsoDiffersFromCurrent(t *testing.T) {
	current := toAny(t, `{"id": "cadre", "version": "3.0.0"}`)
	original := toAny(t, `{"id": "cadre", "version": "1.0.0"}`)
	copyText := `{"id": "cadre", "version": "1.0.0", "extra": true}`
	result := ClassifyArtifact("provider", copyText, current, original, "", ProviderRequiredFields)
	if result.State != "diverged" {
		t.Errorf("state = %q, want diverged", result.State)
	}
	if !result.OriginalDiffersFromCurrent {
		t.Error("expected OriginalDiffersFromCurrent=true: this suite's release also moved since capture")
	}
}

func TestClassifyArtifactProvenanceUndeterminedChecksBeforeCurrent(t *testing.T) {
	// State 2 (provenance-undetermined) is checked before state 3
	// (current): even a COPY that is byte-for-byte identical to CURRENT
	// resolves to provenance-undetermined when no ORIGINAL was supplied,
	// because "current" and "stale-unmodified" cannot be told apart
	// without knowing what COPY was captured from.
	current := toAny(t, `{"id": "cadre", "version": "2.0.0"}`)
	copyText := `{"id": "cadre", "version": "2.0.0"}`
	result := ClassifyArtifact("provider", copyText, current, nil, "no original", ProviderRequiredFields)
	if result.State != "provenance-undetermined" {
		t.Errorf("state = %q, want provenance-undetermined even though copy == current", result.State)
	}
}

func TestRunProfileDiffEndToEnd(t *testing.T) {
	dir := t.TempDir()
	currentProvider := filepath.Join(dir, "current-provider.json")
	currentProfile := filepath.Join(dir, "current-profile.json")
	copyProvider := filepath.Join(dir, "copy-provider.json")
	copyProfile := filepath.Join(dir, "copy-profile.json")
	originalProvider := filepath.Join(dir, "original-provider.json")
	originalProfile := filepath.Join(dir, "original-profile.json")

	writeJSONText(t, currentProvider, `{"id": "cadre", "version": "2.0.0"}`)
	writeJSONText(t, currentProfile, `{"id": "secure-cloud", "version": "2.0.0", "agents": []}`)
	writeJSONText(t, copyProvider, `{"id": "cadre", "version": "2.0.0"}`)
	writeJSONText(t, copyProfile, `{"id": "secure-cloud", "version": "2.0.0", "agents": []}`)
	writeJSONText(t, originalProvider, `{"id": "cadre", "version": "2.0.0"}`)
	writeJSONText(t, originalProfile, `{"id": "secure-cloud", "version": "2.0.0", "agents": []}`)

	results, err := RunProfileDiff(ProfileDiffRequest{
		CopyProviderPath: copyProvider, CopyProfilePath: copyProfile,
		CurrentProviderPath: currentProvider, CurrentProfilePath: currentProfile,
		OriginalProviderPath: originalProvider, OriginalProfilePath: originalProfile,
	})
	if err != nil {
		t.Fatalf("RunProfileDiff: %v", err)
	}
	if !AllCurrent(results) {
		t.Errorf("expected all-current for matching copies, got %+v", results)
	}
}

func TestRunProfileDiffMissingCopyIsCLIError(t *testing.T) {
	dir := t.TempDir()
	currentProvider := filepath.Join(dir, "current-provider.json")
	currentProfile := filepath.Join(dir, "current-profile.json")
	writeJSONText(t, currentProvider, `{"id": "cadre", "version": "2.0.0"}`)
	writeJSONText(t, currentProfile, `{"id": "secure-cloud", "version": "2.0.0", "agents": []}`)

	_, err := RunProfileDiff(ProfileDiffRequest{
		CopyProviderPath: filepath.Join(dir, "does-not-exist.json"), CopyProfilePath: currentProfile,
		CurrentProviderPath: currentProvider, CurrentProfilePath: currentProfile,
	})
	if err == nil {
		t.Fatal("expected an error for a missing --copy-provider path")
	}
}

func writeJSONText(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestToJSONableOverallReflectsAnyNonCurrent(t *testing.T) {
	results := map[string]ArtifactResult{
		"provider": {Artifact: "provider", State: "current"},
		"profile":  {Artifact: "profile", State: "diverged"},
	}
	payload := ToJSONable(results)
	if payload.Overall != "drift" {
		t.Errorf("overall = %q, want drift", payload.Overall)
	}
}

func TestToJSONableAllCurrentIsCurrent(t *testing.T) {
	results := map[string]ArtifactResult{
		"provider": {Artifact: "provider", State: "current"},
		"profile":  {Artifact: "profile", State: "current"},
	}
	payload := ToJSONable(results)
	if payload.Overall != "current" {
		t.Errorf("overall = %q, want current", payload.Overall)
	}
}

func TestRenderArtifactIncludesFindings(t *testing.T) {
	result := ArtifactResult{
		Artifact: "provider", State: "diverged", ComparedAs: "original-vs-copy",
		Findings: []ProfileFinding{{Path: "version", Kind: "changed", Old: "1.0.0", New: "1.1.0"}},
	}
	lines := RenderArtifact("provider", result)
	found := false
	for _, l := range lines {
		if l == `  - version : "1.0.0" -> "1.1.0"` {
			found = true
		}
	}
	if !found {
		t.Errorf("lines = %v", lines)
	}
}
