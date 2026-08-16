package kernel

import (
	"errors"
	"fmt"
	"strings"
)

// Creating and reusing an approval issue.
//
// Separate from the gate issue because two things differ, and both matter.
//
// An approval issue is **assigned to somebody**. That means resolving a
// username to a GitLab account before creating it, and a username that
// resolves to nothing or to two accounts is refused rather than guessed --
// assigning an approval to the wrong person is the failure the gates exist to
// prevent, and it is exactly what picking the first match would risk.
//
// And on reuse it checks **assignee drift**. GitLab's assignee can be changed
// by anyone with access, and a mismatch against the project's authority map is
// reported rather than corrected: overwriting it would erase a deliberate
// reassignment somebody made for a reason this kernel cannot see.
// `--reconcile-assignees` is how an operator asks for the correction.

// processApprovalIssue creates or reuses one authority's approval issue.
//
// Returns the result, whether unreconciled drift was found, and an error. An
// unresolvable username comes back as an *approvalRefusal, which the caller
// folds into the run's refusals rather than aborting on -- one bad binding
// should not stop the rest of a task being tracked.
func processApprovalIssue(
	client *GitLabClient, ledger *issueLedger, taskID, projectPath string,
	approval ApprovalCandidate, gateIID int, botUsername string, mocked bool,
	linkType string, reconcileAssignees bool,
) (*orderedObject, bool, error) {
	entryKey := approval.GateID + "/" + approval.AuthorityID
	context := entryKey

	existing, err := client.SearchIssuesByLabels(projectPath, []string{FixedLabel, approval.Label})
	if err != nil {
		return nil, false, approvalStructuralError(approval, err)
	}
	if len(existing) > 1 {
		return nil, false, &GateIssuesBlocked{Message: fmt.Sprintf(
			"%s: %d issues matched labels [%s, %s] -- ambiguous identity, needs human resolution",
			context, len(existing), FixedLabel, approval.Label)}
	}

	if len(existing) == 1 {
		return reuseApprovalIssue(client, ledger, projectPath, approval, existing[0],
			botUsername, entryKey, context, reconcileAssignees)
	}

	// Resolved before creating, so the issue is never created unassigned and
	// then fixed up -- an unassigned approval issue looks like one nobody has
	// been asked for.
	active, err := resolveActiveUsernames(client, approval.Login)
	if err != nil {
		return nil, false, approvalStructuralError(approval, err)
	}
	switch {
	case len(active) == 0:
		return nil, false, &approvalRefusal{approval.GateID, approval.AuthorityID,
			"gitlab-user-unresolved", fmt.Sprintf(
				"username %s resolved to 0 active GitLab users", pythonRepr(approval.Login))}
	case len(active) > 1:
		return nil, false, &approvalRefusal{approval.GateID, approval.AuthorityID,
			"gitlab-user-ambiguous", fmt.Sprintf(
				"username %s resolved to %d active GitLab users",
				pythonRepr(approval.Login), len(active))}
	}
	resolvedID, _ := jsonNumber(active[0]["id"])

	ledger.setEntry(entryKey, approvalLedgerEntry(approval, "creating", nil, nil, nil))
	if err := ledger.write(); err != nil {
		return nil, false, &GateIssuesError{Message: err.Error()}
	}

	description, err := RenderApprovalDescription(taskID, approval.GateID, approval.Marker,
		projectPath, gateIID, approval.Rationale)
	if err != nil {
		return nil, false, err
	}
	iid, err := client.CreateIssue(projectPath, approval.Title, description,
		[]string{FixedLabel, approval.Label}, []int{resolvedID})
	if err != nil {
		return nil, false, approvalStructuralError(approval, err)
	}
	verification, err := client.FetchIssueAssignmentVerification(projectPath, iid)
	if err != nil {
		return nil, false, approvalStructuralError(approval, err)
	}

	failures := verifyCreatedIssue(verification, approval.Title, projectPath, botUsername,
		[]string{FixedLabel, approval.Label}, []string{approval.Login})
	if len(failures) > 0 {
		ledger.setEntry(entryKey, approvalLedgerEntry(approval, "suspect", iid, verification.State,
			"post-creation verification failed: "+strings.Join(failures, ", ")))
		if err := ledger.write(); err != nil {
			return nil, false, &GateIssuesError{Message: err.Error()}
		}
		return nil, false, &GateIssuesBlocked{Message: fmt.Sprintf(
			"%s: post-creation verification failed (%s) -- aborting the entire run immediately",
			context, strings.Join(failures, ", "))}
	}

	if linkType != "" {
		// The link is an enhancement over the description's parent line, but
		// an operator who asked for it must not get a run that quietly did not
		// make one -- so an unavailable API aborts rather than degrading.
		if _, err := client.CreateIssueLink(projectPath, iid, projectPath, gateIID, linkType); err != nil {
			ledger.setEntry(entryKey, approvalLedgerEntry(approval, "suspect", iid,
				verification.State, "issue link creation failed: "+err.Error()))
			if writeErr := ledger.write(); writeErr != nil {
				return nil, false, &GateIssuesError{Message: writeErr.Error()}
			}
			return nil, false, &GateIssuesBlocked{Message: fmt.Sprintf(
				"%s: GitLab Issue Links API unavailable (%s) -- re-run without --link-type to rely "+
					"on the description cross-reference floor only", context, err.Error())}
		}
	}

	ledger.setEntry(entryKey, approvalLedgerEntry(approval, "created", iid, verification.State, nil))
	if err := ledger.write(); err != nil {
		return nil, false, &GateIssuesError{Message: err.Error()}
	}
	return ordered(
		"gate_id", approval.GateID, "authority_id", approval.AuthorityID,
		"status", "created", "issue_iid", iid, "issue_state", verification.State,
		"drift", nil), false, nil
}

// reuseApprovalIssue takes over an issue that already exists.
func reuseApprovalIssue(
	client *GitLabClient, ledger *issueLedger, projectPath string,
	approval ApprovalCandidate, raw any, botUsername, entryKey, context string,
	reconcileAssignees bool,
) (*orderedObject, bool, error) {
	issue, _ := raw.(map[string]any)
	if err := validateMatchedIssue(issue, approval.Label, approvalLabelPrefix, context); err != nil {
		return nil, false, err
	}
	iid, _ := jsonNumber(issue["iid"])

	// The assignment-reading variant, named for the one caller allowed to look
	// at who an issue is assigned to -- this is the drift check, and it is the
	// only reason that read exists.
	verification, err := client.FetchIssueAssignmentVerification(projectPath, iid)
	if err != nil {
		return nil, false, approvalStructuralError(approval, err)
	}
	if !strings.EqualFold(toStringOrEmpty(verification.AuthorUsername), botUsername) {
		ledger.setEntry(entryKey, approvalLedgerEntry(approval, "suspect", iid, verification.State,
			"matched issue author does not match the verified bot identity"))
		if err := ledger.write(); err != nil {
			return nil, false, &GateIssuesError{Message: err.Error()}
		}
		return nil, false, &GateIssuesBlocked{Message: fmt.Sprintf(
			"%s: matched issue's author does not match the verified bot identity -- refusing to "+
				"reuse, needs human resolution", context)}
	}

	var drift any
	unreconciled := false
	current := lowercased(verification.AssigneeUsernames)
	if !sameStrings(current, []string{strings.ToLower(approval.Login)}) {
		drift = "assignee_changed"
		unreconciled = true
		if reconcileAssignees {
			active, err := resolveActiveUsernames(client, approval.Login)
			if err != nil {
				return nil, false, approvalStructuralError(approval, err)
			}
			if len(active) != 1 {
				return nil, false, &GateIssuesBlocked{Message: fmt.Sprintf(
					"%s: cannot reconcile assignee -- username %s did not resolve to exactly "+
						"one active GitLab user", context, pythonRepr(approval.Login))}
			}
			resolvedID, _ := jsonNumber(active[0]["id"])
			if err := client.UpdateIssueAssignee(projectPath, iid, []int{resolvedID}); err != nil {
				return nil, false, approvalStructuralError(approval, err)
			}
			drift = "assignee_changed (reconciled)"
			unreconciled = false
		}
	}

	ledger.setEntry(entryKey, approvalLedgerEntry(approval, "reused", iid, verification.State, drift))
	if err := ledger.write(); err != nil {
		return nil, false, &GateIssuesError{Message: err.Error()}
	}
	return ordered(
		"gate_id", approval.GateID, "authority_id", approval.AuthorityID,
		"status", "reused", "issue_iid", iid, "issue_state", verification.State,
		"drift", drift), unreconciled, nil
}

// approvalStructuralError wraps a forge failure with which item it happened on.
//
// A failed call is structural -- exit 1 -- not a human-resolvable ambiguity.
// The distinction is what tells an operator whether to look at the project or
// at their credentials.
func approvalStructuralError(approval ApprovalCandidate, err error) error {
	var blocked *GateIssuesBlocked
	if errors.As(err, &blocked) {
		return err
	}
	return &GateIssuesError{Message: fmt.Sprintf("gate %s authority %s: %s",
		pythonRepr(approval.GateID), pythonRepr(approval.AuthorityID), err.Error())}
}

func approvalLedgerEntry(
	approval ApprovalCandidate, status string, iid, state, detail any,
) *orderedObject {
	recordedAt := any(nowRFC3339())
	if status == "creating" {
		recordedAt = nil
	}
	return ordered(
		"kind", "approval", "gate_id", approval.GateID, "authority_id", approval.AuthorityID,
		"marker", approval.Marker, "status", status,
		"issue_iid", iid, "issue_state", state,
		"attempted_at", nowRFC3339(), "recorded_at", recordedAt, "detail", detail)
}
