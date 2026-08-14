// set_overrides.go ports init_project.py's --set flag-driven overrides:
// the defaults-first counterpart to --interactive. The region a path
// belongs to is DERIVED by looking the path up in the shipped default
// files; it is never supplied by the operator -- that is what keeps a
// field_decisions[...].category label honest, since a --set can no more
// mislabel a governance field as "stack" than an answer file can.
package initproject

import (
	"sort"
	"strings"

	"github.com/deagy/cadre/cli/internal/config"
	"gopkg.in/yaml.v3"
)

// SetRegion is one --set region: (answers key, shipped default filename,
// decision category, section).
type SetRegion struct {
	AnswersKey string
	Filename   string
	Category   string
	Section    string
}

// SetRegions maps a --set region name to its definition.
var SetRegions = map[string]SetRegion{
	"stack":     {"rg_a_stack", TeamProfileFilename, "stack", "rg-a-stack"},
	"libraries": {"rg_a_libraries", LibraryStandardsFilename, "stack", "rg-a-stack"},
	"autonomy":  {"rg_b_autonomy", AutonomyFilename, "governance", "rg-b-governance"},
	"platform":  {"rg_c_platform", PlatformFilename, "stack", "rg-c-platform"},
}

// PlatformEntryFields are the per-entry fields an RG-C --set may address.
var PlatformEntryFields = []string{"applicability", "definition_reference", "owner", "rationale"}

func sortedRegionNames() []string {
	names := make([]string, 0, len(SetRegions))
	for name := range SetRegions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func shippedDefault(sharedDefaultsDir, filename string) map[string]any {
	base, err := config.LoadStructured(sharedDefaultsPath(sharedDefaultsDir, filename))
	if err != nil {
		return map[string]any{}
	}
	return base
}

// RegionLeafValues returns every field --set may address in region, mapped
// to its shipped default value. cadre init only ever overrides fields that
// already exist in a shipped default, so this doubles as the validity
// check for a --set path.
func RegionLeafValues(sharedDefaultsDir, region string) map[string]any {
	def := SetRegions[region]
	base := shippedDefault(sharedDefaultsDir, def.Filename)
	if region == "autonomy" {
		values := map[string]any{}
		for _, leaf := range config.AutonomyLeafPaths(base) {
			values[leaf.Path] = leaf.Value
		}
		return values
	}
	if region == "platform" {
		values := map[string]any{}
		for _, sectionDef := range []struct{ section, idKey string }{
			{"impact_categories", "id"}, {"specialized_boms", "type"},
		} {
			entries, _ := base[sectionDef.section].([]any)
			for _, raw := range entries {
				entry, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				entryID, _ := entry[sectionDef.idKey].(string)
				if entryID == "" {
					continue
				}
				for _, field := range PlatformEntryFields {
					values[sectionDef.section+"."+entryID+"."+field] = entry[field]
				}
			}
		}
		return values
	}
	return leafValues(base, "")
}

// ParseSetOverride splits one --set argument into (explicit region or "",
// path, raw value text). Accepts PATH=VALUE and REGION:PATH=VALUE.
func ParseSetOverride(raw string) (region, path, valueText string, err error) {
	eq := strings.Index(raw, "=")
	if eq < 0 {
		return "", "", "", initErrorf("--set %q must be of the form [REGION:]PATH=VALUE", raw)
	}
	lhs := strings.TrimSpace(raw[:eq])
	valueText = raw[eq+1:]
	if colon := strings.Index(lhs, ":"); colon >= 0 {
		regionText := strings.TrimSpace(lhs[:colon])
		lhs = strings.TrimSpace(lhs[colon+1:])
		if _, ok := SetRegions[regionText]; !ok {
			return "", "", "", initErrorf("--set %q: unknown region %q; known regions: %v", raw, regionText, sortedRegionNames())
		}
		region = regionText
	}
	if lhs == "" {
		return "", "", "", initErrorf("--set %q must name a field path before '='", raw)
	}
	return region, lhs, valueText, nil
}

// ResolveSetRegion derives which shipped default file owns path, failing
// closed on a field no shipped default defines and on a path that is
// ambiguous across regions.
func ResolveSetRegion(sharedDefaultsDir, path, explicit string) (string, error) {
	if explicit != "" {
		if _, ok := RegionLeafValues(sharedDefaultsDir, explicit)[path]; !ok {
			return "", initErrorf("--set %s:%s: no such field in shipped %s", explicit, path, SetRegions[explicit].Filename)
		}
		return explicit, nil
	}
	var matches []string
	for _, region := range sortedRegionNames() {
		if _, ok := RegionLeafValues(sharedDefaultsDir, region)[path]; ok {
			matches = append(matches, region)
		}
	}
	if len(matches) == 0 {
		return "", initErrorf("--set %s: no shipped default under roster/shared/ defines this field; `cadre init` only overrides fields that already exist", path)
	}
	if len(matches) > 1 {
		return "", initErrorf("--set %s: ambiguous across regions %v; qualify it as <region>:%s", path, matches, path)
	}
	return matches[0], nil
}

// parseSetValue is the same permissive scalar parse the interactive
// collector uses: a YAML scalar if it parses, otherwise the raw string.
// Mappings are refused -- every field --set can address is a scalar or a
// list, so a mapping value would graft new leaf paths below it that no
// shipped default defines.
func parseSetValue(text string) (any, error) {
	var parsed any
	if err := yaml.Unmarshal([]byte(text), &parsed); err != nil {
		return text, nil
	}
	if _, ok := parsed.(map[string]any); ok {
		return nil, initErrorf("--set values must be scalars or lists; a mapping would add leaf paths below the named field that no shipped default defines")
	}
	return parsed, nil
}

func setDecisionPath(region, path string) string {
	if region == "platform" {
		parts := strings.SplitN(path, ".", 2)
		section := parts[0]
		remainder := ""
		if len(parts) > 1 {
			remainder = parts[1]
		}
		entryID := strings.SplitN(remainder, ".", 2)[0]
		return "rg_c_platform." + section + "." + entryID
	}
	return path
}

// ApplySetOverrides folds each --set into answers, recording the
// field_decisions entry the answer schema requires. Applied last, so a
// --set wins over both an answer file and a --stack preset.
func ApplySetOverrides(sharedDefaultsDir string, answers map[string]any, rawOverrides []string, sections []string) (map[string]any, error) {
	sectionSet := map[string]bool{}
	for _, s := range sections {
		sectionSet[s] = true
	}
	for _, raw := range rawOverrides {
		explicit, path, valueText, err := ParseSetOverride(raw)
		if err != nil {
			return nil, err
		}
		region, err := ResolveSetRegion(sharedDefaultsDir, path, explicit)
		if err != nil {
			return nil, err
		}
		def := SetRegions[region]
		if !sectionSet[def.Section] {
			return nil, initErrorf("--set %s targets %s, which is excluded by --sections", path, def.Section)
		}
		value, err := parseSetValue(valueText)
		if err != nil {
			return nil, err
		}
		fragmentRaw, ok := answers[def.AnswersKey]
		var fragment map[string]any
		if !ok || fragmentRaw == nil {
			fragment = map[string]any{}
			answers[def.AnswersKey] = fragment
		} else {
			fragment, ok = fragmentRaw.(map[string]any)
			if !ok {
				return nil, initErrorf("cannot apply --set %s: %s is not a mapping", path, def.AnswersKey)
			}
		}
		setByPath(fragment, path, value)

		decisionsRaw, ok := answers["field_decisions"]
		var decisions map[string]any
		if !ok || decisionsRaw == nil {
			decisions = map[string]any{}
			answers["field_decisions"] = decisions
		} else {
			decisions, _ = decisionsRaw.(map[string]any)
		}
		decisionPath := setDecisionPath(region, path)
		if region == "platform" {
			previous, _ := decisions[decisionPath].(map[string]any)
			var newValue any
			if strings.HasSuffix(path, ".applicability") {
				newValue = value
			} else if previous != nil {
				newValue = previous["new_value"]
			}
			decisions[decisionPath] = map[string]any{
				"status": "overridden", "category": def.Category,
				"source_value": "unknown", "new_value": newValue,
			}
		} else {
			decisions[decisionPath] = map[string]any{
				"status": "overridden", "category": def.Category,
				"source_value": RegionLeafValues(sharedDefaultsDir, region)[path],
				"new_value":    value,
			}
		}
	}
	return answers, nil
}

// SynthesizePresetFieldDecisions records an "overridden" decision for every
// RG-A leaf that reached the answer set without one. Only ever called on a
// defaults-mode run, where the sole way an unrecorded leaf can be present
// is a --stack preset (LoadStackPreset structurally forbids a preset from
// touching anything but RG-A, so this can never fabricate a governance
// decision).
func SynthesizePresetFieldDecisions(sharedDefaultsDir string, answers map[string]any) map[string]any {
	decisionsRaw, ok := answers["field_decisions"]
	var decisions map[string]any
	if !ok || decisionsRaw == nil {
		decisions = map[string]any{}
		answers["field_decisions"] = decisions
	} else {
		decisions, _ = decisionsRaw.(map[string]any)
	}
	for _, region := range []string{"stack", "libraries"} {
		def := SetRegions[region]
		shipped := RegionLeafValues(sharedDefaultsDir, region)
		fragment, _ := answers[def.AnswersKey].(map[string]any)
		for path, value := range leafValues(fragment, "") {
			if _, exists := decisions[path]; exists {
				continue
			}
			decisions[path] = map[string]any{
				"status": "overridden", "category": def.Category,
				"source_value": shipped[path], "new_value": value,
			}
		}
	}
	return answers
}
