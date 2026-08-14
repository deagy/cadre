package initproject

import "testing"

func TestValidatePlatformFragmentRequiresReferenceAndOwnerWhenApplicable(t *testing.T) {
	fragment := map[string]any{
		"impact_categories": map[string]any{
			"platform-phase": map[string]any{"applicability": "applicable"},
		},
	}
	if err := ValidatePlatformFragment(fragment); err == nil {
		t.Fatal("expected C-002 rejection: applicable with no definition_reference/owner")
	}

	fragment["impact_categories"].(map[string]any)["platform-phase"] = map[string]any{
		"applicability": "applicable", "definition_reference": "doc.md#phase", "owner": "platform-team",
	}
	if err := ValidatePlatformFragment(fragment); err != nil {
		t.Fatalf("expected a fully-cited applicable entry to pass: %v", err)
	}
}

func TestValidatePlatformFragmentRejectsUnknownApplicabilityValue(t *testing.T) {
	fragment := map[string]any{
		"impact_categories": map[string]any{
			"platform-phase": map[string]any{"applicability": "sort-of"},
		},
	}
	if err := ValidatePlatformFragment(fragment); err == nil {
		t.Fatal("expected rejection of an unrecognized applicability value")
	}
}

func TestBuildPlatformOverlayPreservesFullTemplateList(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	dir := makeGitProject(t)

	fragment := map[string]any{
		"impact_categories": map[string]any{
			"platform-phase": map[string]any{
				"applicability": "applicable", "definition_reference": "doc.md#phase", "owner": "platform-team",
			},
		},
	}
	content, merged, ok, err := BuildPlatformOverlay(dir, sharedDir, fragment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected a write")
	}
	entries, _ := merged["impact_categories"].([]any)
	if len(entries) < 2 {
		t.Fatalf("expected the complete shipped impact_categories list to survive (C-004), got %d entries", len(entries))
	}
	foundOverridden := false
	foundUntouchedUnknown := false
	for _, raw := range entries {
		entry := raw.(map[string]any)
		if entry["id"] == "platform-phase" {
			foundOverridden = true
			if entry["applicability"] != "applicable" {
				t.Errorf("expected platform-phase to be overridden to applicable, got %v", entry["applicability"])
			}
		} else if entry["applicability"] == "unknown" {
			foundUntouchedUnknown = true
		}
	}
	if !foundOverridden {
		t.Error("expected the overridden entry to be present")
	}
	if !foundUntouchedUnknown {
		t.Error("expected untouched entries to keep their shipped 'unknown' applicability (gate-blocking must not be silently dropped)")
	}
	if content == "" {
		t.Error("expected non-empty content")
	}
}

func TestBuildPlatformOverlayRejectsOverrideOfNonexistentEntry(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	dir := makeGitProject(t)

	fragment := map[string]any{
		"impact_categories": map[string]any{
			"no-such-entry": map[string]any{"applicability": "not-applicable"},
		},
	}
	_, _, _, err := BuildPlatformOverlay(dir, sharedDir, fragment)
	if err == nil {
		t.Fatal("expected an error overriding an entry the shipped template doesn't define")
	}
}

func TestBuildPlatformOverlayNoFragmentNoExistingIsNoOp(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	dir := makeGitProject(t)
	_, _, ok, err := BuildPlatformOverlay(dir, sharedDir, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected no write when there's no fragment and no existing overlay")
	}
}
