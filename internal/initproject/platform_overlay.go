// platform_overlay.go ports init_project.py's RG-C
// platform-impact-profile.yaml builder -- C-002 applicable-requires-
// citation, C-004 template immutability (only per-key overrides on
// existing entries; the category/BOM list itself is never touched here).
package initproject

import (
	"github.com/deagy/cadre/cli/internal/config"
	"gopkg.in/yaml.v3"
)

func platformApplicabilityValid(v string) bool {
	for _, allowed := range PlatformApplicabilityValues {
		if v == allowed {
			return true
		}
	}
	return false
}

// ValidatePlatformFragment enforces C-002: an entry marked
// applicability=applicable requires both a definition_reference and an
// owner.
func ValidatePlatformFragment(fragment map[string]any) error {
	for _, section := range []string{"impact_categories", "specialized_boms"} {
		entriesRaw, ok := fragment[section]
		if !ok || entriesRaw == nil {
			continue
		}
		entries, ok := entriesRaw.(map[string]any)
		if !ok {
			continue
		}
		for key, entryRaw := range entries {
			entry, ok := entryRaw.(map[string]any)
			if !ok {
				return initErrorf("platform-impact-profile.yaml %s.%s override must be a mapping", section, key)
			}
			applicabilityRaw, present := entry["applicability"]
			if present && applicabilityRaw != nil {
				applicability, _ := applicabilityRaw.(string)
				if !platformApplicabilityValid(applicability) {
					return initErrorf("platform-impact-profile.yaml %s.%s: applicability must be one of %v, got %v (C-002)",
						section, key, PlatformApplicabilityValues, applicabilityRaw)
				}
			}
			if s, _ := entry["applicability"].(string); s == "applicable" {
				if ref, _ := entry["definition_reference"].(string); ref == "" {
					return initErrorf("platform-impact-profile.yaml %s.%s: applicability=applicable requires a definition_reference (C-002)", section, key)
				}
				if owner, _ := entry["owner"].(string); owner == "" {
					return initErrorf("platform-impact-profile.yaml %s.%s: applicability=applicable requires an owner (C-002)", section, key)
				}
			}
		}
	}
	return nil
}

// BuildPlatformOverlay merges fragment's per-entry overrides over the
// shipped impact_categories/specialized_boms template. The template list
// itself (which entries exist) is never touched here (C-004) -- the
// overlay always carries the COMPLETE list (shipped defaults plus this
// project's overlay plus this run's overrides), never only the entries a
// fragment happens to mention, because config.DeepMergeJSON replaces lists
// wholesale rather than merging them by id: an overlay listing only the
// overridden entries would otherwise silently delete every other
// category/BOM from the resolved profile, including the "unknown" ones
// that are supposed to block gates.
func BuildPlatformOverlay(targetRoot, sharedDefaultsDir string, fragment map[string]any) (content string, merged map[string]any, ok bool, err error) {
	if err := ValidatePlatformFragment(fragment); err != nil {
		return "", nil, false, err
	}
	existingText, hasExisting := readExistingOverlayText(targetRoot, PlatformFilename)
	existing := map[string]any{}
	if hasExisting {
		if m, err := parseYAMLMap(existingText); err == nil {
			existing = m
		}
	}
	if len(fragment) == 0 && !hasExisting {
		return "", nil, false, nil
	}
	base, err := config.LoadStructured(sharedDefaultsPath(sharedDefaultsDir, PlatformFilename))
	if err != nil {
		return "", nil, false, err
	}

	merged = map[string]any{}
	for k, v := range existing {
		merged[k] = v
	}

	for _, section := range []string{"impact_categories", "specialized_boms"} {
		overridesRaw := fragment[section]
		overrides, _ := overridesRaw.(map[string]any)
		_, existingHasSection := existing[section]
		if len(overrides) == 0 && !existingHasSection {
			continue
		}
		idKey := "id"
		if section == "specialized_boms" {
			idKey = "type"
		}

		baseEntries, _ := base[section].([]any)
		entriesByID := map[string]map[string]any{}
		var baseIDs []string
		for _, raw := range baseEntries {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id, _ := entry[idKey].(string)
			if id == "" {
				continue
			}
			copied := map[string]any{}
			for k, v := range entry {
				copied[k] = v
			}
			entriesByID[id] = copied
			baseIDs = append(baseIDs, id)
		}

		existingEntries, _ := existing[section].([]any)
		for _, raw := range existingEntries {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id, _ := entry[idKey].(string)
			if base, ok := entriesByID[id]; ok {
				entriesByID[id] = config.DeepMergeJSON(base, entry)
			}
		}

		for entryID, entryFragmentRaw := range overrides {
			entryFragment, _ := entryFragmentRaw.(map[string]any)
			base, ok := entriesByID[entryID]
			if !ok {
				return "", nil, false, initErrorf("platform-impact-profile.yaml %s has no entry %q to override", section, entryID)
			}
			entriesByID[entryID] = config.DeepMergeJSON(base, entryFragment)
		}

		orderedEntries := make([]any, 0, len(baseIDs))
		for _, id := range baseIDs {
			orderedEntries = append(orderedEntries, entriesByID[id])
		}
		merged[section] = orderedEntries
	}

	content, err = dumpYAML(merged)
	if err != nil {
		return "", nil, false, err
	}
	return content, merged, true, nil
}

func parseYAMLMap(text string) (map[string]any, error) {
	var m map[string]any
	if err := yaml.Unmarshal([]byte(text), &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}
