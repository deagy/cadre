package kernel

import (
	"fmt"
	"sort"
	"strings"
)

// `create-gate-issues` -- back a task's lifecycle gates with real GitLab
// issues, idempotently.
//
// **The forge is the source of truth, never the ledger.** Whether an issue
// already exists is answered by searching GitLab for its label pair, on every
// run. The sidecar ledger is diagnostics: it can be stale, deleted, or from
// another machine, and trusting it would create a second issue for a gate that
// already has one -- which is the exact failure this command exists to avoid.
//
// Two levels of granularity, per gate: one tracking issue for the gate, and
// one approval issue per applicable authority requirement, assigned to that
// authority's resolved GitLab user.
//
// Three things that look like details and are not:
//
//   - **A task id never appears in an issue.** Only its hash does. The id is
//     operator-chosen and may name something the project does not want on a
//     forge.
//   - **The description carries a `> parent` line**, which GitLab renders as a
//     bidirectional cross-reference with no API tier required. That is the
//     floor. `--link-type` additionally calls the Issue Links API, and if that
//     API is unavailable the run aborts naming the missing capability rather
//     than silently falling back to the floor -- an operator who asked for a
//     link should not get a run that quietly did not make one.
//   - **Assignee drift is reported, not fixed.** If GitLab's assignee no
//     longer matches the project's authority map, that is recorded and forces
//     a non-zero exit. Overwriting it would erase somebody's deliberate
//     reassignment; `--reconcile-assignees` is how an operator asks for that
//     explicitly.

// maxIssuesPerRun bounds what one invocation will create.
//
// Aborts rather than truncating. A run that quietly created the first forty of
// sixty issues would leave a project half-tracked, and the missing twenty are
// the ones nobody notices.
const maxIssuesPerRun = 40

// Label prefixes. The fixed anchor label marks everything this kernel created;
// the per-kind prefix distinguishes a gate issue from an approval issue.
const (
	gateLabelPrefix     = "agentic-sdlc-gate-"
	approvalLabelPrefix = "agentic-sdlc-approval-"
)

// advisoryTemplate is what every approval issue says about itself.
const advisoryTemplate = "%s Tracking artifact only — closing this issue is not approval evidence " +
	"and does not approve %s. The approver must not be a preparer or the independent " +
	"verifier of this gate. Record approval via `agentic-sdlc approve-from-gitlab-mr` or " +
	"`agentic-sdlc decide`."

// GateIssuesError is a structural or policy failure -- exit 1.
type GateIssuesError struct{ Message string }

func (e *GateIssuesError) Error() string { return e.Message }

// GateIssuesBlocked needs a human -- exit 2.
//
// Ambiguous identity, assignee drift, a held lock, a digest that no longer
// matches, or a capability the instance does not offer. Each means "stop and
// look", not "this is broken".
type GateIssuesBlocked struct{ Message string }

func (e *GateIssuesBlocked) Error() string { return e.Message }

// approvalRefusal is internal control flow: a candidate that turned out to be
// unresolvable once GitLab was actually asked. Folded into the run's refusals
// rather than aborting it -- one unresolvable username should not stop the
// other issues being created.
type approvalRefusal struct {
	GateID      string
	AuthorityID string
	Reason      string
	Detail      string
}

func (e *approvalRefusal) Error() string { return e.Detail }

// ComputeGateMarker identifies a gate's tracking issue.
func ComputeGateMarker(taskID, gateID string) string {
	return hexSHA256([]byte("gate\x00" + taskID + "\x00" + gateID))[:16]
}

// ComputeApprovalMarker identifies one authority's approval issue.
func ComputeApprovalMarker(taskID, gateID, authorityID string) string {
	return hexSHA256([]byte("approval\x00" + taskID + "\x00" + gateID + "\x00" + authorityID))[:16]
}

// GateLabel is the label a gate issue is found by.
//
// Checked against the label charset rather than assumed: a label GitLab would
// reject or normalise is one the next run's search will not find, which turns
// idempotency into duplication.
func GateLabel(marker string) (string, error) {
	return checkedLabel(gateLabelPrefix + marker)
}

// ApprovalLabel is the label an approval issue is found by.
func ApprovalLabel(marker string) (string, error) {
	return checkedLabel(approvalLabelPrefix + marker)
}

func checkedLabel(label string) (string, error) {
	if !IsLabelCharset(label) {
		return "", &GateIssuesError{Message: fmt.Sprintf(
			"computed label %s violates the [a-z0-9-] label charset", pythonRepr(label))}
	}
	return label, nil
}

// GateIssueTitle names a gate's tracking issue.
func GateIssueTitle(taskID, gateID, gateName string) (string, error) {
	raw := fmt.Sprintf("[agentic-sdlc] %s %s (%s)", gateID, gateName, TaskHash(taskID))
	title, err := SanitizeTitleText(raw, gateID+" gate issue title")
	if err != nil {
		return "", &GateIssuesError{Message: err.Error()}
	}
	return title, nil
}

// ApprovalIssueTitle names one authority's approval issue.
func ApprovalIssueTitle(taskID, gateID, gateName, role string) (string, error) {
	raw := fmt.Sprintf("[agentic-sdlc] Approve %s %s - %s (%s)",
		gateID, gateName, role, TaskHash(taskID))
	title, err := SanitizeTitleText(raw, gateID+" approval issue title")
	if err != nil {
		return "", &GateIssuesError{Message: err.Error()}
	}
	return title, nil
}

// RenderGateDescription is a gate issue's body.
//
// The rationale and the scope are the only project-supplied text here, and
// both go through the sanitizer -- unlike the gate-status comment, this module
// genuinely does render free text, which is why it needs the machinery that
// one deliberately lacks.
func RenderGateDescription(
	taskID, gateID, gateName, phase string, humanOnly bool, marker string,
	rationale, scopeText any,
) (string, error) {
	lines := []string{
		provenanceLine + " Not a human-authored artifact. Not approval evidence.",
		fmt.Sprintf("%s%s/%s/%s", refLinePrefix, TaskHash(taskID), gateID, marker),
		"",
		fmt.Sprintf("Gate: %s %s (phase: %s)", gateID, gateName, phase),
	}
	if text, ok := rationale.(string); ok && text != "" {
		sanitized, err := SanitizeFreeText(text, gateID+" applicability_rationale")
		if err != nil {
			return "", &GateIssuesError{Message: err.Error()}
		}
		lines = append(lines, "Applicability rationale: "+sanitized)
	}
	if humanOnly {
		lines = append(lines, "This is a human-only gate -- automation cannot grant it "+
			"(see contracts/lifecycle-gates.json human_only).")
	}
	if text, ok := scopeText.(string); ok && text != "" {
		sanitized, err := SanitizeFreeText(text, gateID+" scope")
		if err != nil {
			return "", &GateIssuesError{Message: err.Error()}
		}
		lines = append(lines, "Scope: "+sanitized)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// RenderApprovalDescription is an approval issue's body.
func RenderApprovalDescription(
	taskID, gateID, marker, projectPath string, gateIID int, rationale any,
) (string, error) {
	lines := []string{
		fmt.Sprintf(advisoryTemplate, provenanceLine, gateID),
		fmt.Sprintf("%s%s/%s/%s", refLinePrefix, TaskHash(taskID), gateID, marker),
		// The cross-reference floor. GitLab renders this as a working
		// bidirectional link with no API call and no tier requirement.
		fmt.Sprintf("%s%s#%d", parentLinePrefix, projectPath, gateIID),
		"",
	}
	if text, ok := rationale.(string); ok && text != "" {
		sanitized, err := SanitizeFreeText(text, gateID+" authority rationale")
		if err != nil {
			return "", &GateIssuesError{Message: err.Error()}
		}
		lines = append(lines, sanitized)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// GatePlan is one gate's tracking issue, as planned.
type GatePlan struct {
	GateID      string
	GateName    string
	Phase       string
	HumanOnly   bool
	Marker      string
	Label       string
	Title       string
	Description string
}

// ApprovalCandidate is one authority's approval issue, as planned.
type ApprovalCandidate struct {
	GateID      string
	AuthorityID string
	Role        string
	Marker      string
	Label       string
	Title       string
	Username    string
	Rationale   any
}

// issuePlan is everything one run intends to do, before any forge call.
type issuePlan struct {
	gates     []GatePlan
	approvals []ApprovalCandidate
	skipped   []SkippedEntry
	refusals  []RefusalEntry
	perGate   *orderedObject
	gateOrder []string
}

// buildIssuePlan works out what issues a task needs.
//
// Pure: it reads the run record, the authority map and the contracts, and
// touches no network. That is what makes the plan digest meaningful -- it is a
// hash of a decision made from local state, so a change in that state between
// planning and applying is detectable.
func buildIssuePlan(
	taskID, projectPath string, gateIDs []string,
	record, authorities map[string]any,
	contracts map[string]map[string]any,
	includeScope bool, scopeText any,
) (*issuePlan, error) {
	gateByID := map[string]map[string]any{}
	for _, raw := range listOf(record["lifecycle_gates"]) {
		if gate, ok := raw.(map[string]any); ok {
			id, _ := gate["gate_id"].(string)
			gateByID[id] = gate
		}
	}

	plan := &issuePlan{
		skipped:  []SkippedEntry{},
		refusals: []RefusalEntry{},
		perGate:  &orderedObject{values: map[string]any{}},
	}

	for _, gateID := range gateIDs {
		gateRecord, present := gateByID[gateID]
		if !present {
			return nil, &GateIssuesError{Message: fmt.Sprintf(
				"gate %s not found in the run record's lifecycle_gates array", gateID)}
		}
		contract := contracts[gateID]
		gateName, _ := contract["name"].(string)
		if gateName == "" {
			gateName = gateID
		}
		phase, _ := contract["phase"].(string)
		humanOnly := contract["human_only"] == true

		marker := ComputeGateMarker(taskID, gateID)
		label, err := GateLabel(marker)
		if err != nil {
			return nil, err
		}
		title, err := GateIssueTitle(taskID, gateID, gateName)
		if err != nil {
			return nil, err
		}
		var scope any
		if includeScope {
			scope = scopeText
		}
		description, err := RenderGateDescription(taskID, gateID, gateName, phase, humanOnly,
			marker, gateRecord["applicability_rationale"], scope)
		if err != nil {
			return nil, err
		}
		plan.gates = append(plan.gates, GatePlan{
			gateID, gateName, phase, humanOnly, marker, label, title, description})

		requirements := listOf(gateRecord["authority_requirements"])
		resolved := &orderedObject{values: map[string]any{}}
		for _, raw := range requirements {
			requirement, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			authorityID, _ := requirement["authority_id"].(string)
			var username any
			if authority, ok := authorities[authorityID].(map[string]any); ok {
				if resolvedName := AuthorityForgeLogin(authority, "gitlab"); resolvedName != "" {
					username = resolvedName
				}
			}
			resolved.set(authorityID, username)
		}

		if err := plan.addApprovalCandidates(taskID, gateID, gateName, gateRecord,
			requirements, authorities, resolved); err != nil {
			return nil, err
		}

		plan.perGate.set(gateID, ordered(
			"applicability", gateRecord["applicability"],
			"applicability_rationale", gateRecord["applicability_rationale"],
			"status", gateRecord["status"],
			"required_reentry_gate", gateRecord["required_reentry_gate"],
			"authority_requirements", requirementDigests(requirements),
			"resolved_usernames", resolved,
		))
		plan.gateOrder = append(plan.gateOrder, gateID)
	}

	if total := len(plan.gates) + len(plan.approvals); total > maxIssuesPerRun {
		return nil, &GateIssuesError{Message: fmt.Sprintf(
			"planned issue count %d exceeds MAX_ISSUES_PER_RUN=%d -- aborting rather than truncating",
			total, maxIssuesPerRun)}
	}
	return plan, nil
}

// addApprovalCandidates decides which authorities get an approval issue.
func (p *issuePlan) addApprovalCandidates(
	taskID, gateID, gateName string, gateRecord map[string]any,
	requirements []any, authorities map[string]any, resolved *orderedObject,
) error {
	for _, raw := range requirements {
		requirement, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		authorityID, _ := requirement["authority_id"].(string)
		authority, isAuthority := authorities[authorityID].(map[string]any)
		roleLabel, hasRole := requirement["role"].(string)
		if !hasRole {
			roleLabel = authorityID
		}

		switch requirement["applicability"] {
		case "not-applicable":
			p.skipped = append(p.skipped, SkippedEntry{
				gateID, authorityID, "not-applicable", requirement["rationale"]})
			continue
		case "unknown":
			if isAuthority && authority["applicability"] == "not-applicable" {
				p.skipped = append(p.skipped, SkippedEntry{
					gateID, authorityID, "authorities-not-applicable", authority["rationale"]})
				continue
			}
			p.refusals = append(p.refusals, RefusalEntry{
				gateID, authorityID, "applicability-unknown",
				"authority_requirements applicability is 'unknown' and authorities.json does not " +
					"mark this authority not-applicable"})
			continue
		case "applicable":
		default:
			p.refusals = append(p.refusals, RefusalEntry{
				gateID, authorityID, "applicability-unknown", fmt.Sprintf(
					"unrecognized applicability %s", pythonRepr(requirement["applicability"]))})
			continue
		}

		if !isAuthority {
			p.refusals = append(p.refusals, RefusalEntry{
				gateID, authorityID, "authority-unknown",
				fmt.Sprintf("role %s missing from authorities.json", authorityID)})
			continue
		}
		assignee, _ := authority["assignee"].(string)
		if authority["status"] != "assigned" || assignee == "" {
			p.refusals = append(p.refusals, RefusalEntry{
				gateID, authorityID, "authority-unassigned",
				fmt.Sprintf("authority %s is not assigned", authorityID)})
			continue
		}
		username, _ := resolved.values[authorityID].(string)
		if username == "" {
			p.refusals = append(p.refusals, RefusalEntry{
				gateID, authorityID, "no-gitlab-binding",
				fmt.Sprintf("authority %s has no GitLab username binding", authorityID)})
			continue
		}
		// Enforced here because GitLab enforces nothing about who an issue is
		// assigned to. Compared against the run record's identities, not the
		// GitLab username -- the record is where "who prepared this" lives.
		if isGateSelfApproval(authority["assignee"], gateRecord) {
			p.refusals = append(p.refusals, RefusalEntry{
				gateID, authorityID, "self-approval", fmt.Sprintf(
					"authority %s's assignee is a preparer or the independent verifier of %s",
					authorityID, gateID)})
			continue
		}

		marker := ComputeApprovalMarker(taskID, gateID, authorityID)
		label, err := ApprovalLabel(marker)
		if err != nil {
			return err
		}
		title, err := ApprovalIssueTitle(taskID, gateID, gateName, roleLabel)
		if err != nil {
			return err
		}
		p.approvals = append(p.approvals, ApprovalCandidate{
			GateID: gateID, AuthorityID: authorityID, Role: roleLabel,
			Marker: marker, Label: label, Title: title, Username: username,
			Rationale: requirement["rationale"],
		})
	}
	return nil
}

func requirementDigests(requirements []any) []any {
	digests := []any{}
	for _, raw := range requirements {
		requirement, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		digests = append(digests, ordered(
			"authority_id", requirement["authority_id"],
			"applicability", requirement["applicability"],
			"rationale", requirement["rationale"],
		))
	}
	return digests
}

// ComputePlanDigest hashes everything the plan was derived from.
//
// This is what `--apply` is handed back from a `--dry-run`, and what is
// re-checked before every single issue. Its job is to notice that the project
// changed between the operator looking and the run acting -- an authority
// reassigned, a gate invalidated, a rationale edited. Creating issues from a
// plan somebody has since invalidated is the failure it prevents.
func ComputePlanDigest(
	taskID, projectPath string, gateIDs []string,
	dispatchFingerprint any, perGate *orderedObject,
	disposition, classification any, reEntryCount int,
) (string, error) {
	payload := ordered(
		"task_id", taskID,
		"project_path", projectPath,
		"gate_ids", asJSONList(gateIDs),
		"dispatch_fingerprint", dispatchFingerprint,
		"per_gate", perGate,
		"eligibility", ordered(
			"disposition", disposition,
			"classification", classification,
			"re_entry_count", reEntryCount,
		),
	)
	return Fingerprint(payload)
}

// validateMatchedIssue checks an issue found by label really is ours.
//
// Three separate checks, because a label search alone is not proof. The anchor
// label says this kernel created it; the own label says it is this artifact;
// and the absence of a foreign label of the same family says nobody has
// attached a second identity to it. An issue carrying two gate labels is one
// that would be reused for two different gates.
func validateMatchedIssue(issue map[string]any, ownLabel, foreignPrefix, context string) error {
	labels := map[string]bool{}
	for _, raw := range listOf(issue["labels"]) {
		if label, ok := raw.(string); ok {
			labels[label] = true
		}
	}
	if !labels[FixedLabel] {
		return &GateIssuesBlocked{Message: fmt.Sprintf(
			"%s: matched issue is missing the %s anchor label", context, pythonRepr(FixedLabel))}
	}
	if !labels[ownLabel] {
		return &GateIssuesBlocked{Message: fmt.Sprintf(
			"%s: matched issue is missing its own label %s", context, pythonRepr(ownLabel))}
	}
	var foreign []string
	for label := range labels {
		if strings.HasPrefix(label, foreignPrefix) && label != ownLabel {
			foreign = append(foreign, label)
		}
	}
	if len(foreign) > 0 {
		sort.Strings(foreign)
		return &GateIssuesBlocked{Message: fmt.Sprintf(
			"%s: matched issue carries a foreign label %s -- possible mismatch/poisoned issue",
			context, pythonList(foreign))}
	}
	return nil
}
