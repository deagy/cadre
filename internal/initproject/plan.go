// plan.go ports init_project.py's plan_writes: builds every planned
// overlay write, validating everything first (A-005 fail-closed). If the
// returned errors slice is non-empty, the returned InitResult.Planned must
// be treated as invalid and nothing may be written.
package initproject

import (
	"errors"

	"github.com/deagy/cadre/cli/internal/config"
)

func sectionEnabled(sections []string, name string) bool {
	for _, s := range sections {
		if s == name {
			return true
		}
	}
	return false
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// PlanWrites builds every planned overlay write for this run, mirroring
// init_project.py's plan_writes exactly section-by-section.
func PlanWrites(targetRoot, sharedDefaultsDir string, answers map[string]any, sections []string) (*InitResult, []string) {
	result := &InitResult{GovernanceTouchedPaths: map[string]bool{}}
	var errs []string
	var touched []TouchedPath

	decisions, err := ParseFieldDecisions(asMap(answers["field_decisions"]))
	if err != nil {
		errs = append(errs, err.Error())
		decisions = map[string]FieldDecision{}
	}

	if sectionEnabled(sections, "rg-a-stack") {
		stackFragment := asMap(answers["rg_a_stack"])
		librariesFragment := asMap(answers["rg_a_libraries"])
		for _, p := range leafPaths(stackFragment, "") {
			touched = append(touched, TouchedPath{p, "stack"})
		}
		for _, p := range leafPaths(librariesFragment, "") {
			touched = append(touched, TouchedPath{p, "stack"})
		}

		if content, _, ok, err := BuildStructuredOverlay(targetRoot, TeamProfileFilename, stackFragment); err != nil {
			errs = append(errs, TeamProfileFilename+": "+err.Error())
		} else if ok {
			if err := ValidateOverlayContent(sharedDefaultsDir, TeamProfileFilename, content); err != nil {
				errs = append(errs, TeamProfileFilename+": "+err.Error())
			} else {
				result.Planned = append(result.Planned, PlannedWrite{TeamProfileFilename, "rg-a-stack", "stack", content})
			}
		}

		if content, _, ok, err := BuildStructuredOverlay(targetRoot, LibraryStandardsFilename, librariesFragment); err != nil {
			errs = append(errs, LibraryStandardsFilename+": "+err.Error())
		} else if ok {
			if err := ValidateOverlayContent(sharedDefaultsDir, LibraryStandardsFilename, content); err != nil {
				errs = append(errs, LibraryStandardsFilename+": "+err.Error())
			} else {
				result.Planned = append(result.Planned, PlannedWrite{LibraryStandardsFilename, "rg-a-stack", "stack", content})
			}
		}

		addendum := asString(asMap(answers["rg_a_prose_addenda"])[TechnologyStandardsFilename])
		if content, ok := BuildProseAddendumOverlay(targetRoot, TechnologyStandardsFilename, addendum); ok {
			if err := ValidateOverlayContent(sharedDefaultsDir, TechnologyStandardsFilename, content); err != nil {
				errs = append(errs, TechnologyStandardsFilename+": "+err.Error())
			} else {
				result.Planned = append(result.Planned, PlannedWrite{TechnologyStandardsFilename, "rg-a-stack", "stack", content})
			}
		}
	}

	if sectionEnabled(sections, "rg-b-governance") {
		autonomyFragment := asMap(answers["rg_b_autonomy"])
		for _, leaf := range autonomyLeafPathStrings(autonomyFragment) {
			touched = append(touched, TouchedPath{leaf, "governance"})
			result.GovernanceTouchedPaths[leaf] = true
		}

		content, merged, ok, err := BuildAutonomyOverlay(targetRoot, sharedDefaultsDir, autonomyFragment)
		if err != nil {
			var rejected *AutonomyOverlayRejected
			if errors.As(err, &rejected) {
				result.RejectedAutonomy = append(result.RejectedAutonomy, RejectedAutonomy{rejected.FieldPath, rejected.Value})
				errs = append(errs, AutonomyFilename+": "+rejected.Error())
			} else {
				errs = append(errs, AutonomyFilename+": "+err.Error())
			}
		} else if ok {
			if verr := ValidateAutonomyOverlayContent(sharedDefaultsDir, content, autonomyFragment); verr != nil {
				var rejected *AutonomyOverlayRejected
				if errors.As(verr, &rejected) {
					result.RejectedAutonomy = append(result.RejectedAutonomy, RejectedAutonomy{rejected.FieldPath, rejected.Value})
					errs = append(errs, AutonomyFilename+": "+rejected.Error())
				} else {
					errs = append(errs, AutonomyFilename+": "+verr.Error())
				}
			} else {
				_ = merged
				result.Planned = append(result.Planned, PlannedWrite{AutonomyFilename, "rg-b-governance", "governance", content})
			}
		}

		guardrailBulletsRaw, hasBullets := answers["rg_b_guardrails_addendum"]
		if hasBullets {
			bulletsList, isList := guardrailBulletsRaw.([]any)
			if !isList {
				errs = append(errs, "rg_b_guardrails_addendum must be a list of additive bullet strings (B-004)")
			} else {
				bullets := make([]string, len(bulletsList))
				for i, b := range bulletsList {
					bullets[i] = asString(b)
				}
				content, ok, rejected := BuildGuardrailsOverlay(targetRoot, bullets)
				result.RejectedGuardrails = append(result.RejectedGuardrails, rejected...)
				if len(rejected) > 0 {
					for _, r := range rejected {
						errs = append(errs, GuardrailsFilename+" bullet rejected: "+r.Reason)
					}
				} else if ok {
					if err := ValidateOverlayContent(sharedDefaultsDir, GuardrailsFilename, content); err != nil {
						errs = append(errs, GuardrailsFilename+": "+err.Error())
					} else {
						result.Planned = append(result.Planned, PlannedWrite{GuardrailsFilename, "rg-b-governance", "governance", content})
					}
				}
			}
		}
	}

	if sectionEnabled(sections, "rg-c-platform") {
		platformFragment := asMap(answers["rg_c_platform"])
		for _, section := range []string{"impact_categories", "specialized_boms"} {
			for entryID := range asMap(platformFragment[section]) {
				touched = append(touched, TouchedPath{"rg_c_platform." + section + "." + entryID, "stack"})
			}
		}
		if content, _, ok, err := BuildPlatformOverlay(targetRoot, sharedDefaultsDir, platformFragment); err != nil {
			errs = append(errs, PlatformFilename+": "+err.Error())
		} else if ok {
			if err := ValidateOverlayContent(sharedDefaultsDir, PlatformFilename, content); err != nil {
				errs = append(errs, PlatformFilename+": "+err.Error())
			} else {
				result.Planned = append(result.Planned, PlannedWrite{PlatformFilename, "rg-c-platform", "stack", content})
			}
		}
	}

	if err := RequireFieldDecisionsCover(touched, decisions); err != nil {
		errs = append(errs, err.Error())
	}

	return result, errs
}

func autonomyLeafPathStrings(fragment map[string]any) []string {
	if len(fragment) == 0 {
		return nil
	}
	var out []string
	for _, leaf := range config.AutonomyLeafPaths(fragment) {
		out = append(out, leaf.Path)
	}
	return out
}
