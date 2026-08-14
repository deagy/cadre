package initproject

import "testing"

func TestParseSetOverrideSimplePathEqualsValue(t *testing.T) {
	region, path, value, err := ParseSetOverride("platform.hosting_model=cloud")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if region != "" || path != "platform.hosting_model" || value != "cloud" {
		t.Errorf("region=%q path=%q value=%q", region, path, value)
	}
}

func TestParseSetOverrideExplicitRegion(t *testing.T) {
	region, path, value, err := ParseSetOverride("stack:platform.hosting_model=cloud")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if region != "stack" || path != "platform.hosting_model" || value != "cloud" {
		t.Errorf("region=%q path=%q value=%q", region, path, value)
	}
}

func TestParseSetOverrideRejectsUnknownRegion(t *testing.T) {
	if _, _, _, err := ParseSetOverride("bogus:path=value"); err == nil {
		t.Fatal("expected rejection of an unknown region")
	}
}

func TestParseSetOverrideRejectsMissingEquals(t *testing.T) {
	if _, _, _, err := ParseSetOverride("no-equals-sign"); err == nil {
		t.Fatal("expected rejection of malformed --set with no '='")
	}
}

func TestResolveSetRegionDerivesRegionFromShippedDefault(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	region, err := ResolveSetRegion(sharedDir, "platform.hosting_model", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if region != "stack" {
		t.Errorf("region = %q, want stack", region)
	}
}

func TestResolveSetRegionFailsClosedOnUndefinedPath(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	if _, err := ResolveSetRegion(sharedDir, "no.such.field.anywhere", ""); err == nil {
		t.Fatal("expected fail-closed rejection of a path no shipped default defines")
	}
}

func TestResolveSetRegionRejectsWrongExplicitRegion(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	if _, err := ResolveSetRegion(sharedDir, "platform.hosting_model", "autonomy"); err == nil {
		t.Fatal("expected rejection: platform.hosting_model does not exist under the autonomy region")
	}
}

func TestApplySetOverridesRecordsFieldDecision(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	answers := map[string]any{}
	sections := []string{"rg-a-stack", "rg-b-governance", "rg-c-platform"}
	result, err := ApplySetOverrides(sharedDir, answers, []string{"platform.hosting_model=cloud"}, sections)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stack := result["rg_a_stack"].(map[string]any)
	platform := stack["platform"].(map[string]any)
	if platform["hosting_model"] != "cloud" {
		t.Errorf("expected the override applied to rg_a_stack.platform.hosting_model, got %v", platform)
	}
	decisions := result["field_decisions"].(map[string]any)
	decision, ok := decisions["platform.hosting_model"].(map[string]any)
	if !ok {
		t.Fatalf("expected a synthesized field_decisions entry, got %v", decisions)
	}
	if decision["status"] != "overridden" || decision["category"] != "stack" {
		t.Errorf("decision = %v", decision)
	}
}

func TestApplySetOverridesRejectsWhenSectionExcluded(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	answers := map[string]any{}
	// rg-a-stack deliberately excluded.
	sections := []string{"rg-b-governance", "rg-c-platform"}
	if _, err := ApplySetOverrides(sharedDir, answers, []string{"platform.hosting_model=cloud"}, sections); err == nil {
		t.Fatal("expected rejection: --set targets a section excluded by --sections")
	}
}

func TestApplySetOverridesRejectsMappingValue(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	answers := map[string]any{}
	sections := []string{"rg-a-stack", "rg-b-governance", "rg-c-platform"}
	_, err := ApplySetOverrides(sharedDir, answers, []string{"platform.hosting_model={a: b}"}, sections)
	if err == nil {
		t.Fatal("expected rejection of a mapping-shaped --set value")
	}
}
