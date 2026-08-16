package kernel

import (
	"regexp"
	"time"
)

// The lifecycle's fixed vocabulary: which gates exist, who may approve each,
// and what the URIs that carry evidence look like.
//
// These are constants rather than configuration on purpose. A project chooses
// which gates apply to it and who fills each authority role; it does not get
// to invent a gate, rename an authority, or decide that G5 needs no security
// lead. That the set is fixed here is what makes a run record from one project
// mean the same thing as a run record from another.

// GateIDs are the ten lifecycle gates, in order. Order is load-bearing:
// approval is checked against prerequisites and against lexical position.
var GateIDs = []string{"G1", "G2", "G3", "G4", "G5", "G6", "G7", "G8", "G9", "G10"}

// RequiredAuthorityRoles hold authority over gates unconditionally.
var RequiredAuthorityRoles = map[string][]string{
	"product_owner":     {"G1", "G2", "G6"},
	"engineering_lead":  {"G2", "G6"},
	"system_architect":  {"G3"},
	"governance_lead":   {"G4"},
	"security_lead":     {"G5"},
	"release_owner":     {"G7", "G8"},
	"release_authority": {"G9"},
	"service_owner":     {"G10"},
}

// ConditionalAuthorityRoles apply only when a project says they do -- but
// "unknown" is not one of the answers. An unresolved applicability blocks,
// because a gate whose authority nobody has decided about is a gate nobody is
// accountable for.
var ConditionalAuthorityRoles = map[string][]string{
	"data_control_owner":         {"G4"},
	"human_key_owner":            {"G5"},
	"uat_product_owner":          {"G6"},
	"implicated_security_lead":   {"G10"},
	"implicated_governance_lead": {"G10"},
}

// AuthorityRoles is every role, required and conditional.
var AuthorityRoles = mergeRoleMaps(RequiredAuthorityRoles, ConditionalAuthorityRoles)

// AuthorityRoleOrder is the order these roles are written in, required before
// conditional and each in lifecycle order.
//
// Not alphabetical, and not a Go map's iteration order. It is the order a
// reader of authorities.json sees, and it groups the roles that always apply
// ahead of the ones a project has to decide about -- which is the distinction
// somebody filling the file in is working through.
var AuthorityRoleOrder = []string{
	"product_owner", "engineering_lead", "system_architect", "governance_lead",
	"security_lead", "release_owner", "release_authority", "service_owner",
	"data_control_owner", "human_key_owner", "uat_product_owner",
	"implicated_security_lead", "implicated_governance_lead",
}

// RoleLabels are the human-facing names. A run record that relabels an
// authority is refused: the label is what a reader sees when deciding whether
// the right person approved, and two roles sharing a label (uat_product_owner
// and product_owner both read "Product Owner") is deliberate, so a record
// swapping one for the other would look correct.
var RoleLabels = map[string]string{
	"product_owner": "Product Owner", "engineering_lead": "Engineering Lead",
	"system_architect": "System Architect", "governance_lead": "Governance Lead",
	"data_control_owner": "Data/Control Owner", "security_lead": "Security Lead",
	"human_key_owner": "Human Key Owner", "uat_product_owner": "Product Owner",
	"implicated_security_lead":   "Security Lead",
	"implicated_governance_lead": "Governance Lead",
	"release_owner":              "Release Owner", "release_authority": "Release Authority",
	"service_owner": "Service Owner",
}

// Evidence URI shapes. Each is anchored: a URI that merely starts with the
// scheme is not evidence, and a run record citing a malformed one is citing
// something nobody can resolve.
var (
	gitlabIssueURI = regexp.MustCompile(`^gitlab-issue:([A-Za-z0-9_./-]+):issues/(\d+)$`)
	githubIssueURI = regexp.MustCompile(`^github-issue:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+):issues/(\d+)$`)
)

// IsGitLabIssueURI reports whether value is a well-formed GitLab issue URI.
func IsGitLabIssueURI(value string) bool { return gitlabIssueURI.MatchString(value) }

// IsGitHubIssueURI reports whether value is a well-formed GitHub issue URI.
func IsGitHubIssueURI(value string) bool { return githubIssueURI.MatchString(value) }

// ValidException reports whether a risk exception is one the kernel will
// honour.
//
// The rule that carries the weight is owner != approver. An exception is a
// decision to ship a known finding, and letting one identity both request and
// approve it is the same self-approval the gates exist to prevent -- reached
// through the escape hatch instead of the front door.
//
// An expired exception is also not one: the point of an expiry is that the
// acceptance stops, and honouring it afterwards would make the date decorative.
func ValidException(exception map[string]any) bool {
	for _, field := range []string{
		"exception_id", "finding_id", "justification", "compensating_controls",
		"owner", "approver", "expires_at", "remediation_plan",
	} {
		if _, present := exception[field]; !present {
			return false
		}
	}
	if controls, ok := exception["compensating_controls"].([]any); !ok || len(controls) == 0 {
		// Present but empty is the same as absent: an exception whose
		// compensating controls are "none" is an unmitigated acceptance
		// wearing the paperwork of a mitigated one.
		if text, ok := exception["compensating_controls"].(string); !ok || text == "" {
			return false
		}
	}

	owner, ownerOK := exception["owner"].(map[string]any)
	approver, approverOK := exception["approver"].(map[string]any)
	if !ownerOK || !approverOK {
		return false
	}
	if owner["kind"] != "human" || approver["kind"] != "human" {
		return false
	}
	if owner["id"] == approver["id"] {
		return false
	}

	expiry, ok := exception["expires_at"].(string)
	if !ok {
		return false
	}
	parsed, err := parseTimestamp(expiry)
	if err != nil {
		return false
	}
	return parsed.After(time.Now())
}

func parseTimestamp(value string) (time.Time, error) {
	if len(value) > 10 && value[10] == ' ' {
		value = value[:10] + "T" + value[11:]
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errInvalidTimestamp
}

var errInvalidTimestamp = &timestampError{}

type timestampError struct{}

func (*timestampError) Error() string { return "invalid timestamp" }

func mergeRoleMaps(maps ...map[string][]string) map[string][]string {
	merged := map[string][]string{}
	for _, source := range maps {
		for key, value := range source {
			merged[key] = value
		}
	}
	return merged
}

// GateDispatchBinding resolves what a gate requires from a project's routing.
//
// Returns the agents, tasks and artifacts the project's own gate_bindings
// declare for the contributions this gate's contract asks for. A contribution
// the project has not bound contributes nothing rather than failing here --
// `validate` is what reports the consequences, so this stays a lookup.
func GateDispatchBinding(gate map[string]any, routing map[string]any) map[string][]string {
	result := map[string][]string{"agents": {}, "tasks": {}, "artifacts": {}}

	gateID, _ := gate["id"].(string)
	bindings, _ := routing["gate_bindings"].(map[string]any)
	binding, _ := bindings[gateID].(map[string]any)
	contributions, _ := binding["contributions"].(map[string]any)

	slots, _ := gate["required_contributions"].([]any)
	for _, rawSlot := range slots {
		slot, ok := rawSlot.(string)
		if !ok {
			continue
		}
		contribution, ok := contributions[slot].(map[string]any)
		if !ok {
			continue
		}
		for field := range result {
			values, _ := contribution[field].([]any)
			for _, raw := range values {
				if text, ok := raw.(string); ok {
					result[field] = append(result[field], text)
				}
			}
		}
	}
	for field, values := range result {
		result[field] = uniqueStrings(values)
	}
	return result
}

// uniqueStrings preserves first-seen order, matching Python's
// list(dict.fromkeys(values)).
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// GateAgentArtifacts is the per-gate artifact requirement.
//
// Empty, matching the Python kernel, where this function is a stub returning
// []. Ported as-is rather than "improved": the checks that consume it compare
// against what it returns, so inventing requirements here would fail every
// existing run record. Left as the seam it is.
func GateAgentArtifacts(map[string]any) []map[string]string { return nil }
