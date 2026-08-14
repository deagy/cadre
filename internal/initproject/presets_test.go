package initproject

import "testing"

func TestLoadStackPresetLoadsRealPreset(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	preset, err := LoadStackPreset(sharedDir, "golang-postgres-k8s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := preset["rg_a_stack"]; !ok {
		t.Errorf("expected rg_a_stack in preset, got %v", preset)
	}
}

func TestLoadStackPresetRejectsUnknownID(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	if _, err := LoadStackPreset(sharedDir, "does-not-exist"); err == nil {
		t.Fatal("expected an error for an unknown preset id")
	}
}

func TestMergeAnswersWithPresetAnswersWinPerKey(t *testing.T) {
	preset := map[string]any{
		"rg_a_stack": map[string]any{"backend": map[string]any{"database": "postgresql"}},
	}
	answers := map[string]any{
		"rg_a_stack": map[string]any{"backend": map[string]any{"database": "mysql"}},
	}
	merged := MergeAnswersWithPreset(answers, preset)
	stack := merged["rg_a_stack"].(map[string]any)
	backend := stack["backend"].(map[string]any)
	if backend["database"] != "mysql" {
		t.Errorf("expected answers to win over preset, got %v", backend["database"])
	}
}

func TestMergeAnswersWithPresetNoPresetIsNoOp(t *testing.T) {
	answers := map[string]any{"rg_a_stack": map[string]any{"backend": map[string]any{}}}
	merged := MergeAnswersWithPreset(answers, nil)
	if len(merged) != len(answers) {
		t.Errorf("expected an unchanged answers map, got %v", merged)
	}
}
