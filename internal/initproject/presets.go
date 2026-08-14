// presets.go ports init_project.py's --stack preset loading.
package initproject

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deagy/cadre/cli/internal/config"
)

var stackPresetAllowedKeys = map[string]bool{
	"rg_a_stack": true, "rg_a_libraries": true, "rg_a_prose_addenda": true,
}

// LoadStackPreset loads a named --stack preset. Presets are a static,
// reviewed RG-A-only starter fragment, never a detection heuristic over
// target-repo content, and are structurally forbidden from touching
// governance fields (THREAT-MODEL-HARDENING-5).
func LoadStackPreset(sharedDefaultsDir, presetID string) (map[string]any, error) {
	presetsDir := filepath.Join(sharedDefaultsDir, "init-presets")
	path := filepath.Join(presetsDir, presetID+".yaml")
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		var available []string
		if entries, err := os.ReadDir(presetsDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
					available = append(available, strings.TrimSuffix(e.Name(), ".yaml"))
				}
			}
		}
		sort.Strings(available)
		return nil, initErrorf("unknown --stack preset %q; available: %v", presetID, available)
	}
	preset, err := config.LoadStructured(path)
	if err != nil {
		return nil, err
	}
	var forbidden []string
	for key := range preset {
		if !stackPresetAllowedKeys[key] {
			forbidden = append(forbidden, key)
		}
	}
	if len(forbidden) > 0 {
		sort.Strings(forbidden)
		return nil, initErrorf(
			"stack preset %q may only contain rg_a_stack/rg_a_libraries/rg_a_prose_addenda, found: %v",
			presetID, forbidden)
	}
	return preset, nil
}

// MergeAnswersWithPreset folds preset's rg_a_stack/rg_a_libraries/
// rg_a_prose_addenda into answers, with answers winning per key
// (deep-merged so a preset's suggestion is only overridden field-by-field,
// not wholesale).
func MergeAnswersWithPreset(answers map[string]any, preset map[string]any) map[string]any {
	if len(preset) == 0 {
		return answers
	}
	merged := map[string]any{}
	for k, v := range answers {
		merged[k] = v
	}
	for _, key := range []string{"rg_a_stack", "rg_a_libraries"} {
		presetValue, _ := preset[key].(map[string]any)
		answersValue, _ := answers[key].(map[string]any)
		merged[key] = config.DeepMergeJSON(presetValue, answersValue)
	}
	if _, presetHas := preset["rg_a_prose_addenda"]; presetHas {
		if _, answersHas := answers["rg_a_prose_addenda"]; !answersHas {
			merged["rg_a_prose_addenda"] = preset["rg_a_prose_addenda"]
		}
	}
	return merged
}
