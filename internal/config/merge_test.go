package config

import "testing"

func TestDeepMergeJSON(t *testing.T) {
	base := map[string]any{
		"a": "base-a",
		"nested": map[string]any{
			"x": "base-x",
			"y": "base-y",
		},
		"list": []any{"base-item"},
	}
	overlay := map[string]any{
		"a": "overlay-a",
		"nested": map[string]any{
			"y": "overlay-y",
			"z": "overlay-z",
		},
		"list": []any{"overlay-item"}, // lists replace wholesale, never merge.
	}

	result := DeepMergeJSON(base, overlay)

	if result["a"] != "overlay-a" {
		t.Errorf("a = %v, want overlay-a", result["a"])
	}
	nested := result["nested"].(map[string]any)
	if nested["x"] != "base-x" {
		t.Errorf("nested.x = %v, want base-x (preserved)", nested["x"])
	}
	if nested["y"] != "overlay-y" {
		t.Errorf("nested.y = %v, want overlay-y", nested["y"])
	}
	if nested["z"] != "overlay-z" {
		t.Errorf("nested.z = %v, want overlay-z", nested["z"])
	}
	list := result["list"].([]any)
	if len(list) != 1 || list[0] != "overlay-item" {
		t.Errorf("list = %v, want wholesale replacement with [overlay-item]", list)
	}
}

func TestDeepMergeJSONDoesNotMutateInputs(t *testing.T) {
	base := map[string]any{"nested": map[string]any{"x": "base"}}
	overlay := map[string]any{"nested": map[string]any{"x": "overlay"}}

	DeepMergeJSON(base, overlay)

	if base["nested"].(map[string]any)["x"] != "base" {
		t.Error("DeepMergeJSON must not mutate its base argument")
	}
	if overlay["nested"].(map[string]any)["x"] != "overlay" {
		t.Error("DeepMergeJSON must not mutate its overlay argument")
	}
}
