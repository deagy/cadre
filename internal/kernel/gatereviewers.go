package kernel

import (
	"fmt"
	"sort"
	"strings"
)

// `request-gate-reviewers` -- report which logins would be asked to review a
// task's pull request, and which already have.
//
// Read-only, and the name is the misleading part. Actually requesting a
// reviewer needs a GitHub token with `Pull requests: write`, which also
// permits editing and closing pull requests and changing labels. Granting that
// is a permission-escalation decision somebody has to make explicitly, so this
// command reports and stops. There is no --apply.
//
// No ledger either, for a reason worth keeping: `create-gate-issues` keeps one
// because something happened that is useful to remember between runs. Nothing
// happens here. A fresh report is always at least as accurate as a cached one,
// since review state changes on the forge between runs -- so a ledger could
// only ever make this less trustworthy.
//
// The rule that shapes the output is **login-level poisoning**. A GitHub
// review request is pull-request-wide, not gate-scoped: inviting somebody to
// review the PR at all lets them approve every gate it touches. So if one
// (gate, authority) pair refuses a login for an *independence* reason -- they
// prepared the work, they authored the PR, they are the bot doing the asking
// -- that login is withheld from all of its motivations, including the ones
// that were clean. Resolution failures are different: a missing binding or an
// unknown account is a property of that pair or that login, not a conflict,
// and it does not poison anything else.

// Reviewer classifications. The first four are states of a login that can be
// asked; the last three are reasons it cannot.
var (
	independenceReasons = map[string]bool{
		"self-approval": true, "pr-author-conflict": true, "actor-is-reviewer": true,
		"mr-author-conflict": true,
	}
	problemClassifications = map[string]bool{
		"withheld-conflict": true, "github-user-unresolved": true, "not-a-collaborator": true,
	}
)

// GateReviewersError is a structural or policy failure: a missing run record,
// a pull request that is closed or belongs to another repository, an identity
// mismatch, or a gate explicitly asked for that is not eligible.
//
// One category, not two. Unlike the issue publishers there is no dry-run/apply
// split here, so there is nothing a "blocked but continue" state would mean:
// anything that stops the report being built stops the command.
type GateReviewersError struct{ Message string }

func (e *GateReviewersError) Error() string { return e.Message }

// Motivation is one reason a login appears in the report.
type Motivation struct {
	GateID        string `json:"gate_id"`
	AuthorityID   string `json:"authority_id"`
	Role          string `json:"role"`
	AuthorityType any    `json:"authority_type"`
}

// SkippedEntry is an authority requirement that does not apply.
type SkippedEntry struct {
	GateID      string `json:"gate_id"`
	AuthorityID string `json:"authority_id"`
	Reason      string `json:"reason"`
	Rationale   any    `json:"rationale"`
}

// RefusalEntry is an authority requirement that applies but cannot be met.
type RefusalEntry struct {
	GateID      string `json:"gate_id"`
	AuthorityID string `json:"authority_id"`
	Reason      string `json:"reason"`
	Detail      string `json:"detail"`
}

// PoisonCause is the independence conflict that withheld a login.
type PoisonCause struct {
	GateID      string `json:"gate_id"`
	AuthorityID string `json:"authority_id"`
	Reason      string `json:"reason"`
}

// reviewerPlan is what build_plan works out before any forge lookup.
type reviewerPlan struct {
	// Keyed by lower-cased login throughout: forge logins are
	// case-insensitive, and grouping by the casing a project happened to
	// write would report one person twice.
	motivations map[string][]Motivation
	display     map[string]string
	poisoned    map[string]PoisonCause
	order       []string
	skipped     []SkippedEntry
	refusals    []RefusalEntry
}

// loginResolver reads a forge login out of an authority record. The two forges
// differ in nothing else, which is why this is a parameter rather than a
// second copy of the planner.
type loginResolver func(authority map[string]any) string

// buildReviewerPlan groups authority requirements by the login that would
// satisfy them.
//
// Forge-agnostic by construction. Eligibility, independence, and the
// request-wide poisoning rule are the same policy on GitHub and GitLab; only
// how a login is resolved and what the two binding-missing reason codes are
// called differ, and those arrive as arguments.
func buildReviewerPlan(
	gateIDs []string,
	record map[string]any,
	authorities map[string]any,
	authorLogin, botLogin string,
	resolve loginResolver,
	noBindingReason, authorConflictReason string,
) (*reviewerPlan, error) {
	gateByID := map[string]map[string]any{}
	for _, raw := range listOf(record["lifecycle_gates"]) {
		if gate, ok := raw.(map[string]any); ok {
			id, _ := gate["gate_id"].(string)
			gateByID[id] = gate
		}
	}

	plan := &reviewerPlan{
		motivations: map[string][]Motivation{},
		display:     map[string]string{},
		poisoned:    map[string]PoisonCause{},
		skipped:     []SkippedEntry{},
		refusals:    []RefusalEntry{},
	}

	for _, gateID := range gateIDs {
		gateRecord, present := gateByID[gateID]
		if !present {
			return nil, &GateReviewersError{Message: fmt.Sprintf(
				"gate %s not found in the run record's lifecycle_gates array", gateID)}
		}

		for _, raw := range listOf(gateRecord["authority_requirements"]) {
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
				plan.skipped = append(plan.skipped, SkippedEntry{
					gateID, authorityID, "not-applicable", requirement["rationale"]})
				continue
			case "unknown":
				// Unknown here is only excusable if the project's own
				// authority map says the role does not apply. Otherwise it is
				// a question nobody has answered, and answering it by
				// assuming would decide who reviews a gate.
				if isAuthority && authority["applicability"] == "not-applicable" {
					plan.skipped = append(plan.skipped, SkippedEntry{
						gateID, authorityID, "authorities-not-applicable", authority["rationale"]})
					continue
				}
				plan.refusals = append(plan.refusals, RefusalEntry{
					gateID, authorityID, "applicability-unknown",
					"authority_requirements applicability is 'unknown' and authorities.json does not " +
						"mark this authority not-applicable"})
				continue
			case "applicable":
			default:
				plan.refusals = append(plan.refusals, RefusalEntry{
					gateID, authorityID, "applicability-unknown", fmt.Sprintf(
						"unrecognized applicability %s", pythonRepr(requirement["applicability"]))})
				continue
			}

			if !isAuthority {
				plan.refusals = append(plan.refusals, RefusalEntry{
					gateID, authorityID, "authority-unknown",
					fmt.Sprintf("role %s missing from authorities.json", authorityID)})
				continue
			}
			assignee, _ := authority["assignee"].(string)
			if authority["status"] != "assigned" || assignee == "" {
				plan.refusals = append(plan.refusals, RefusalEntry{
					gateID, authorityID, "authority-unassigned",
					fmt.Sprintf("authority %s is not assigned", authorityID)})
				continue
			}
			login := resolve(authority)
			if login == "" {
				plan.refusals = append(plan.refusals, RefusalEntry{
					gateID, authorityID, noBindingReason,
					fmt.Sprintf("authority %s has no login binding", authorityID)})
				continue
			}

			// The three independence conflicts, in the order they are
			// checked. Each means the same thing: this person cannot be an
			// independent reviewer of this gate.
			reason := ""
			switch {
			case isGateSelfApproval(authority["assignee"], gateRecord):
				reason = "self-approval"
			case authorLogin != "" && strings.EqualFold(login, authorLogin):
				reason = authorConflictReason
			case botLogin != "" && strings.EqualFold(login, botLogin):
				reason = "actor-is-reviewer"
			}

			key := strings.ToLower(login)
			if _, seen := plan.display[key]; !seen {
				plan.display[key] = login
				plan.order = append(plan.order, key)
			}
			plan.motivations[key] = append(plan.motivations[key],
				Motivation{gateID, authorityID, roleLabel, requirement["authority_type"]})

			if reason != "" {
				plan.refusals = append(plan.refusals, RefusalEntry{
					gateID, authorityID, reason, fmt.Sprintf(
						"authority %s's resolved login %s is withheld from %s (%s)",
						authorityID, pythonRepr(login), gateID, reason)})

				// Only an independence conflict poisons. The guard is
				// defensive rather than reachable today -- every reason the
				// switch above produces is in the set -- and it is here so a
				// fourth reason added later has to be classified deliberately
				// rather than inheriting request-wide withholding by accident.
				if independenceReasons[reason] {
					// First conflict wins as the reported cause. Later ones
					// are still in refusals; replacing the cause would make
					// the report's headline depend on gate iteration order.
					if _, poisoned := plan.poisoned[key]; !poisoned {
						plan.poisoned[key] = PoisonCause{gateID, authorityID, reason}
					}
				}
			}
		}
	}
	return plan, nil
}

// isGateSelfApproval reports whether an identity prepared or verified a gate.
//
// Vacuous today -- the kernel writes no preparers and no verifier -- and
// implemented anyway, because it becomes load-bearing the moment any other
// path populates them, and a check that appears when the data does is a check
// nobody reviewed.
func isGateSelfApproval(assignee any, gateRecord map[string]any) bool {
	for _, raw := range listOf(gateRecord["preparers"]) {
		preparer, ok := raw.(map[string]any)
		if ok && preparer["id"] == assignee {
			return true
		}
	}
	if verifier, ok := gateRecord["independent_verifier"].(map[string]any); ok {
		if id, present := verifier["id"]; present && id != nil && id == assignee {
			return true
		}
	}
	return false
}

// ClassifyReviewerLogin decides what state a login is in on a pull request.
//
// Priority: already-reviewed, then review-stale, then already-requested, then
// to-request. A dismissed review is not a state of its own -- it simply does
// not count, and the login falls through to whichever check applies next.
//
// The distinction that matters is the middle one: a review of an older commit
// is not a review of this one. Treating it as satisfied would let a change
// pushed after an approval inherit that approval.
func ClassifyReviewerLogin(
	login string, requestedReviewers map[string]bool, reviews []any, headSHA string,
) string {
	normalizedLogin := strings.ToLower(login)
	normalizedHead := normalizeCommitSHA(headSHA)

	var effective []map[string]any
	for _, raw := range reviews {
		review, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		user, ok := review["user"].(map[string]any)
		if !ok {
			continue
		}
		reviewLogin, ok := user["login"].(string)
		if !ok || strings.ToLower(reviewLogin) != normalizedLogin {
			continue
		}
		state, _ := review["state"].(string)
		if strings.EqualFold(state, "DISMISSED") {
			continue
		}
		effective = append(effective, review)
	}

	if len(effective) > 0 {
		sort.SliceStable(effective, func(a, b int) bool {
			return submittedAt(effective[a]) < submittedAt(effective[b])
		})
		latest := effective[len(effective)-1]
		reviewCommit := normalizeCommitSHA(toStringOrEmpty(latest["commit_id"]))
		if reviewCommit != "" && reviewCommit == normalizedHead {
			return "already-reviewed"
		}
		return "review-stale"
	}
	if requestedReviewers[normalizedLogin] {
		return "already-requested"
	}
	return "to-request"
}

func submittedAt(review map[string]any) string {
	value, _ := review["submitted_at"].(string)
	return value
}

func toStringOrEmpty(value any) string {
	text, _ := value.(string)
	return text
}

// normalizeCommitSHA lower-cases and trims a commit id, or returns "".
//
// Compared rather than displayed, so the normalisation is what makes a
// comparison between a forge's casing and a record's casing mean anything.
func normalizeCommitSHA(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// configuredGateIDs are the gates a dispatch plan actually requires.
func configuredGateIDs(dispatchPlan map[string]any) map[string]bool {
	configured := map[string]bool{}
	for _, raw := range listOf(dispatchPlan["gate_dispatch"]) {
		item, ok := raw.(map[string]any)
		if ok && item["status"] == "required" {
			id, _ := item["gate_id"].(string)
			configured[id] = true
		}
	}
	return configured
}

// GateEligibilityError is one named gate that cannot be worked on.
//
// NeedsHuman is what separates the two kinds. A gate id that does not exist,
// or one missing from the run record, is a mistake in the invocation -- fixed
// by typing something else. A gate the task is not configured for, or one that
// is not applicable, invalidated, or awaiting re-entry, is a statement about
// the project that somebody has to go and change. The callers map the two to
// different exit codes, and the Python kernel makes the same split with two
// exception classes.
type GateEligibilityError struct {
	Message    string
	NeedsHuman bool
}

func (e *GateEligibilityError) Error() string { return e.Message }

// CheckGateEligibility refuses a gate that cannot be reported on.
//
// Explicitly asked-for gates only. A gate that fails these checks in the
// default set is skipped quietly, because the operator did not name it; one
// that was named is an instruction that cannot be carried out, and saying so
// is more useful than silently reporting on nothing.
func CheckGateEligibility(
	gateID string, dispatchPlan map[string]any, gateRecord map[string]any,
) error {
	known := false
	for _, id := range GateIDs {
		if id == gateID {
			known = true
		}
	}
	if !known {
		return &GateEligibilityError{Message: fmt.Sprintf("unknown gate id: %s", pythonRepr(gateID))}
	}
	if gateRecord == nil {
		return &GateEligibilityError{Message: fmt.Sprintf(
			"gate %s not found in the run record's lifecycle_gates array "+
				"(lookup is by gate_id, not index; the array must contain exactly G1-G10)", gateID)}
	}
	if !configuredGateIDs(dispatchPlan)[gateID] {
		return &GateEligibilityError{NeedsHuman: true, Message: fmt.Sprintf(
			"gate %s is not part of the task's configured (dispatch-plan) gate set", gateID)}
	}
	if gateRecord["applicability"] != "applicable" {
		return &GateEligibilityError{NeedsHuman: true, Message: fmt.Sprintf(
			"gate %s applicability is %s, not 'applicable'",
			gateID, pythonRepr(gateRecord["applicability"]))}
	}
	if gateRecord["status"] == "invalidated" {
		return &GateEligibilityError{NeedsHuman: true, Message: fmt.Sprintf(
			"gate %s status is 'invalidated'", gateID)}
	}
	if reentry, present := gateRecord["required_reentry_gate"]; present && reentry != nil {
		return &GateEligibilityError{NeedsHuman: true, Message: fmt.Sprintf(
			"gate %s has a pending required_reentry_gate=%s", gateID, pythonRepr(reentry))}
	}
	return nil
}

// defaultGateIDs are the gates worth reporting on when none were named.
func defaultGateIDs(dispatchPlan map[string]any, gateByID map[string]map[string]any) []string {
	configured := configuredGateIDs(dispatchPlan)
	var result []string
	for _, gateID := range GateIDs {
		if !configured[gateID] {
			continue
		}
		gateRecord, present := gateByID[gateID]
		if !present {
			continue
		}
		if gateRecord["applicability"] != "applicable" {
			continue
		}
		if gateRecord["status"] == "invalidated" {
			continue
		}
		if reentry, present := gateRecord["required_reentry_gate"]; present && reentry != nil {
			continue
		}
		result = append(result, gateID)
	}
	return result
}
