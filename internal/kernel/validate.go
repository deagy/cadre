package kernel

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// `agentic-sdlc validate` -- does this project's configuration hold together,
// and is it ready to proceed?
//
// Two answers, not one, and the distinction is the whole design:
//
//   errors   something is wrong. The configuration contradicts itself, or a
//            run record claims something that cannot be true.
//   blockers nothing is wrong, but something is undecided. An authority
//            nobody has assigned; an environment whose production status is
//            "unknown".
//
// A blocker is not a failure to fix in code -- it is a decision a human has
// not made yet, and the kernel refuses to make it for them. That is why
// `ready` is separate from `valid`, and why unresolved applicability blocks
// rather than defaulting to "probably not applicable".
//
// This file is the configuration half; validate_runrecord.go is the other.
// ValidateProject runs both, and the subcommand calls that rather than either
// alone -- a validate that skipped run records would answer "valid" for a
// repository it never looked at properly.

// ValidationReport is what `validate` prints.
type ValidationReport struct {
	Valid    bool     `json:"valid"`
	Ready    bool     `json:"ready"`
	Errors   []string `json:"errors"`
	Blockers []string `json:"blockers"`
}

// validationAccumulator collects both lists in the order they were found.
type validationAccumulator struct {
	errors   []string
	blockers []string
}

func (a *validationAccumulator) errorf(format string, args ...any) {
	a.errors = append(a.errors, fmt.Sprintf(format, args...))
}

func (a *validationAccumulator) blockf(format string, args ...any) {
	a.blockers = append(a.blockers, fmt.Sprintf(format, args...))
}

func (a *validationAccumulator) report() ValidationReport {
	errors := a.errors
	if errors == nil {
		errors = []string{}
	}
	blockers := a.blockers
	if blockers == nil {
		blockers = []string{}
	}
	return ValidationReport{
		Valid:    len(errors) == 0,
		Ready:    len(errors) == 0 && len(blockers) == 0,
		Errors:   errors,
		Blockers: blockers,
	}
}

// ValidateProject is the whole answer: configuration and run records.
//
// `validate` reports both together because either alone would be misleading.
// A project whose configuration is impeccable can still hold a run record
// claiming a gate was approved by its own author, and a project with a
// spotless record history can still be pointed at a profile nobody installed.
func (r *Registry) ValidateProject(root string, overlay *ProjectOverlay) ValidationReport {
	accumulator := &validationAccumulator{}
	policy := r.validateConfigurationInto(accumulator, overlay)
	r.validateRunRecords(accumulator, root, overlay, policy)
	return accumulator.report()
}

// ValidateConfiguration checks a project's overlay: its profile, approval
// policy, environments, command confirmation, version lock, authorities,
// impact profile and routing.
//
// Exported so the two halves can be tested apart -- not so either can be run
// alone as an answer. `validate` calls ValidateProject.
func (r *Registry) ValidateConfiguration(root string, overlay *ProjectOverlay) ValidationReport {
	accumulator := &validationAccumulator{}
	r.validateConfigurationInto(accumulator, overlay)
	return accumulator.report()
}

// validateConfigurationInto runs the configuration checks and hands back the
// approval policy, which the run-record half needs: whether an approval must
// be backed by a forge review is a property of the project, not of the gate.
func (r *Registry) validateConfigurationInto(
	accumulator *validationAccumulator, overlay *ProjectOverlay,
) ApprovalPolicy {

	// The profile must be one that is actually installed. A project naming a
	// profile no provider supplies has gate bindings nobody can resolve.
	profile, _ := overlay.Project["profile"].(string)
	if !r.ProfileIDs()[profile] {
		accumulator.errorf("project profile is not installed")
	}

	policy, err := ApprovalSourcePolicy(overlay.Project)
	if err != nil {
		accumulator.errorf("%s", err.Error())
		// Continue with the permissive default so the remaining checks still
		// run: one malformed field should not hide every other problem in the
		// project, which is the whole reason this collects rather than raises.
		policy = ApprovalPolicy{HumanGateDefault: "manual", AllowManualFallback: true}
	}

	// An environment whose persistence or production status is "unknown"
	// blocks rather than erring: nobody has written the wrong thing, they
	// have not yet written anything, and guessing "not production" is exactly
	// the guess that gets something deployed.
	if environments, ok := overlay.Project["environments"].([]any); ok {
		for _, raw := range environments {
			environment, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := environment["name"].(string)
			if name == "" {
				name = "unnamed"
			}
			if environment["persistence"] == "unknown" {
				accumulator.blockf("environment persistence is unknown: %s", name)
			}
			if environment["production"] == "unknown" {
				accumulator.blockf("environment production status is unknown: %s", name)
			}
		}
	}

	commands, commandsErr := loadJSONObject(filepath.Join(overlay.Root, "commands.json"))
	lock, lockErr := loadJSONObject(filepath.Join(overlay.Root, "version.lock"))
	if commandsErr != nil {
		accumulator.errorf("%s", commandsErr.Error())
		commands = map[string]any{}
	}
	if lockErr != nil {
		accumulator.errorf("%s", lockErr.Error())
		lock = map[string]any{}
	}
	// Detected commands are a proposal until a human confirms them. Running
	// an unconfirmed command is running something the kernel guessed.
	if len(commands) > 0 && commands["confirmed"] != true {
		accumulator.blockf("detected project commands are not confirmed")
	}
	if len(lock) > 0 {
		if lockVersion, _ := lock["kernel_version"].(string); lockVersion != Version {
			accumulator.errorf(
				"project kernel lock %v does not match installed version %s",
				lock["kernel_version"], Version)
		}
		if !lockedProvidersMatch(lock["providers"], r.Providers) {
			accumulator.errorf("loaded providers do not match the project provider lock")
		}
	}

	r.validateAuthorities(accumulator, overlay.Authorities, policy)
	validateImpact(accumulator, overlay.Impact)
	r.validateRouting(accumulator, overlay.Routing)

	return policy
}

// validateAuthorities checks that every role a gate can require is resolved.
func (r *Registry) validateAuthorities(
	accumulator *validationAccumulator, authorities map[string]any, policy ApprovalPolicy,
) {
	// Sorted so two runs over the same project report in the same order --
	// Go map iteration is randomised, and a diffable report is worth more
	// than the microsecond.
	roles := make([]string, 0, len(AuthorityRoles))
	for role := range AuthorityRoles {
		roles = append(roles, role)
	}
	sort.Strings(roles)

	for _, role := range roles {
		value, ok := authorities[role].(map[string]any)
		if !ok {
			accumulator.errorf("missing authority role: %s", role)
			continue
		}

		if _, conditional := ConditionalAuthorityRoles[role]; conditional {
			switch value["applicability"] {
			case "unknown":
				accumulator.blockf("conditional authority applicability %s is unresolved", role)
			case "applicable":
				if value["status"] != "assigned" || emptyString(value["assignee"]) {
					accumulator.blockf("applicable conditional authority %s is unassigned", role)
				}
			case "not-applicable":
				// A role declared not-applicable needs a reason. Otherwise
				// "not-applicable" is indistinguishable from "we did not want
				// to find someone", which is the decision this is meant to
				// make visible.
				if emptyString(value["rationale"]) {
					accumulator.errorf(
						"conditional authority %s not-applicable requires a rationale", role)
				}
			}
		} else if value["status"] != "assigned" || emptyString(value["assignee"]) {
			accumulator.blockf("authority %s is unresolved", role)
		}

		// An authority expected to approve through GitHub or GitLab needs the
		// binding that makes that possible. Blocked only when manual fallback
		// is off: with fallback available the approval can still happen, so
		// the missing binding is an inconvenience rather than a dead end.
		applicability := "applicable"
		if declared, ok := value["applicability"].(string); ok {
			applicability = declared
		}
		if value["status"] == "assigned" && applicability == "applicable" && !policy.AllowManualFallback {
			if policy.HumanGateDefault == "github-review" && AuthorityForgeLogin(value, "github") == "" {
				accumulator.blockf(
					"authority %s is missing a GitHub login binding required for GitHub review approvals",
					role)
			}
			if policy.HumanGateDefault == "gitlab-mr" && AuthorityForgeLogin(value, "gitlab") == "" {
				accumulator.blockf(
					"authority %s is missing a GitLab username binding required for GitLab MR approvals",
					role)
			}
		}
	}
}

// validateImpact reports unresolved impact applicability.
func validateImpact(accumulator *validationAccumulator, impact map[string]any) {
	var unknown []string
	for _, key := range []string{"impact_categories", "specialized_boms"} {
		items, _ := impact[key].([]any)
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if item["applicability"] == "unknown" {
				id, _ := item["id"].(string)
				if id == "" {
					id = "unnamed"
				}
				unknown = append(unknown, id)
			}
		}
	}
	for _, id := range unknown {
		accumulator.blockf("impact applicability is unknown: %s", id)
	}

	blocking, _ := impact["blocking_unknowns"].([]any)
	for _, raw := range blocking {
		accumulator.blockf("impact profile blocker: %v", raw)
	}
}

// validateRouting checks the project's own routes.
func (r *Registry) validateRouting(accumulator *validationAccumulator, routing map[string]any) {
	knownGates := map[string]bool{}
	for _, id := range GateIDs {
		knownGates[id] = true
	}
	var unknownIgnored []string
	ignored, _ := routing["ignored_gates"].([]any)
	for _, raw := range ignored {
		id, ok := raw.(string)
		if !ok || !knownGates[id] {
			unknownIgnored = append(unknownIgnored, fmt.Sprint(raw))
		}
	}
	if len(unknownIgnored) > 0 {
		sort.Strings(unknownIgnored)
		accumulator.errorf("routing ignored_gates contains unknown lifecycle gates: %s", pythonList(unknownIgnored))
	}

	catalog, err := r.LoadAgentCatalog()
	if err != nil {
		accumulator.errorf("%s", err.Error())
		catalog = map[string]any{}
	}

	seen := map[string]bool{}
	routes, _ := routing["routes"].([]any)
	for _, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		routeID, _ := route["id"].(string)
		if routeID == "" || seen[routeID] {
			accumulator.errorf("duplicate or missing route ID: %v", route["id"])
		}
		seen[routeID] = true

		agents := stringsIn(route["agents"])
		reviewers := stringsIn(route["reviewers"])
		support := stringsIn(route["support"])

		// The invariant this whole system is built around, at the routing
		// layer: an agent cannot be both author and reviewer of the same
		// route. Everything downstream assumes the two sets are disjoint.
		var overlap []string
		reviewerSet := map[string]bool{}
		for _, reviewer := range reviewers {
			reviewerSet[reviewer] = true
		}
		for _, agent := range agents {
			if reviewerSet[agent] {
				overlap = append(overlap, agent)
			}
		}
		if len(overlap) > 0 {
			sort.Strings(overlap)
			accumulator.errorf("route %s assigns author and reviewer roles to: %s", routeID, pythonList(overlap))
		}

		var unknownAgents []string
		for _, group := range [][]string{agents, reviewers, support} {
			for _, agent := range group {
				if _, known := catalog[agent]; !known {
					unknownAgents = append(unknownAgents, agent)
				}
			}
		}
		if len(unknownAgents) > 0 {
			unknownAgents = uniqueStrings(unknownAgents)
			sort.Strings(unknownAgents)
			accumulator.errorf("route %s references unknown agents: %s", routeID, pythonList(unknownAgents))
		}
	}
}

// lockedProvidersMatch compares a project's recorded provider lock against
// what is loaded now.
//
// A lock exists so a project notices when the providers underneath it change.
// Comparing the derived records -- id, version, and both digests -- is what
// makes it a real check rather than a name match.
func lockedProvidersMatch(locked any, loaded []LoadedProvider) bool {
	list, ok := locked.([]any)
	if !ok {
		return len(loaded) == 0
	}
	if len(list) != len(loaded) {
		return false
	}
	for index, raw := range list {
		entry, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		current := loaded[index]
		if entry["id"] != current.ID ||
			entry["version"] != current.Version ||
			entry["manifest_sha256"] != current.ManifestSHA256 ||
			entry["catalog_sha256"] != current.CatalogSHA256 {
			return false
		}
	}
	return true
}

// pythonList renders a string slice the way Python renders a list of
// strings: ['a', 'b'].
//
// The messages are what an operator reads and greps for, and this kernel is
// meant to replace the Python one rather than sit beside it -- so a message
// that differs only in bracket style is still a message that breaks someone's
// alert or runbook. Go's %v prints [a b], which the differential caught on
// two cases before this existed.
func pythonList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "'"+value+"'")
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func stringsIn(value any) []string {
	list, _ := value.([]any)
	out := make([]string, 0, len(list))
	for _, raw := range list {
		if text, ok := raw.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func emptyString(value any) bool {
	text, ok := value.(string)
	return !ok || text == ""
}

// AuthorityForgeLogin reads an authority's login for a forge, or "" when it
// has none.
//
// Two sources, in Python's order: an explicit `github_login` /
// `gitlab_username` field, then the assignee parsed as a forge URL.
//
// The assignee fallback is why this is not a field lookup. An authority is
// normally recorded as "github.com/alice", and that string *is* the binding --
// requiring a separate field would report every ordinary project as missing
// one. My first attempt read a nested "identity" object that does not exist,
// which would have reported every authority as unbound and blocked every
// project configured for GitHub review.
func AuthorityForgeLogin(authority map[string]any, forge string) string {
	explicitField, prefix := "github_login", "github.com/"
	if forge == "gitlab" {
		explicitField, prefix = "gitlab_username", "gitlab.com/"
	}

	if explicit, ok := authority[explicitField].(string); ok && explicit != "" {
		return explicit
	}

	assignee, ok := authority["assignee"].(string)
	if !ok || !strings.HasPrefix(assignee, prefix) {
		return ""
	}
	return strings.Trim(strings.TrimPrefix(assignee, prefix), "/")
}
