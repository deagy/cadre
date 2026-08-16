package kernel

import (
	"fmt"
	"strings"
)

// Creating and reusing a GitHub approval issue.
//
// Two things here have no counterpart on the GitLab side, and both exist
// because GitHub will accept an assignment it does not make.
//
// **The pre-check.** Before creating, the login is confirmed to be a real
// GitHub user and a collaborator on the repository. GitHub silently drops an
// assignment to a non-collaborator -- no error, just an issue with nobody on
// it -- so an approval issue that looks assigned and is not would sit there
// waiting for somebody who was never asked. A failure here is one authority's
// refusal, not the run's: the rest of the task still gets tracked.
//
// **The re-verify.** The pre-check has a window. Somebody can be removed from
// the repository between the check and the write, and the write will still
// report success. So every create and every reconciling PATCH is followed by a
// re-read, and a mismatch blocks the run rather than reporting drift --
// reporting an assignment that did not happen is the one outcome worse than
// refusing to report anything.
//
// Drift itself, on reuse, is still report-only. Somebody may have reassigned
// an issue deliberately for a reason this kernel cannot see, and overwriting
// it would erase that. `--reconcile-assignees` is how an operator asks.

// processGithubApprovalIssue creates or reuses one authority's approval issue.
//
// Returns the result, whether unreconciled drift was found, and an error. An
// unresolvable or non-collaborating login comes back as a
// *githubApprovalRefusal, which the caller folds into the run's refusals.
func processGithubApprovalIssue(
	issues *GitHubIssueClient, reads *GitHubClient, ledger *issueLedger,
	taskID, repo string, approval ApprovalCandidate, gateNumber int,
	botLogin string, mocked bool, reconcileAssignees bool, mutations *githubMutations,
) (*orderedObject, bool, error) {
	entryKey := approval.GateID + "/" + approval.AuthorityID
	context := entryKey
	wrap := fmt.Sprintf("gate %s authority %s",
		pythonRepr(approval.GateID), pythonRepr(approval.AuthorityID))

	matches, err := issues.SearchIssuesByLabel(repo, approval.Label)
	if err != nil {
		return nil, false, githubStructuralError(wrap, err)
	}
	if err := checkSearchResults(matches, context); err != nil {
		return nil, false, err
	}

	if len(matches) == 1 {
		return reuseGithubApprovalIssue(issues, ledger, repo, approval, matches[0],
			botLogin, entryKey, context, wrap, reconcileAssignees, mutations)
	}

	// The pre-check, before anything is created. An issue created and then
	// found to be unassignable would have to be closed again, and closing an
	// issue this command created is not something it will do.
	exists, err := reads.UserExists(approval.Login)
	if err != nil {
		return nil, false, githubStructuralError(wrap, err)
	}
	if !exists {
		return nil, false, &githubApprovalRefusal{approval.GateID, approval.AuthorityID,
			"github-user-unresolved", fmt.Sprintf(
				"login %s does not resolve to an existing GitHub user", pythonRepr(approval.Login))}
	}
	collaborator, err := reads.IsCollaborator(repo, approval.Login)
	if err != nil {
		return nil, false, githubStructuralError(wrap, err)
	}
	if !collaborator {
		return nil, false, &githubApprovalRefusal{approval.GateID, approval.AuthorityID,
			"not-a-collaborator", fmt.Sprintf(
				"login %s is not a collaborator on %s", pythonRepr(approval.Login), repo)}
	}

	ledger.setEntry(entryKey, githubApprovalLedgerEntry(approval, "creating", nil, nil, nil))
	if err := githubLedgerWrite(ledger); err != nil {
		return nil, false, &GateIssuesGithubError{Message: err.Error()}
	}

	description, err := RenderGithubApprovalDescription(taskID, approval.GateID, approval.Marker,
		repo, gateNumber, approval.Rationale)
	if err != nil {
		return nil, false, err
	}
	mutations.next()
	if err := issues.EnsureLabel(repo, approval.Label); err != nil {
		return nil, false, githubStructuralError(wrap, err)
	}
	mutations.next()
	number, err := issues.CreateIssue(repo, approval.Title, description,
		[]string{FixedLabel, approval.Label}, []string{approval.Login})
	if err != nil {
		return nil, false, githubStructuralError(wrap, err)
	}
	verification, err := issues.FetchIssueVerification(repo, number)
	if err != nil {
		return nil, false, githubStructuralError(wrap, err)
	}

	failures := verifyCreatedGithubIssue(verification, approval.Title, repo, botLogin,
		[]string{FixedLabel, approval.Label}, []string{approval.Login})
	if len(failures) > 0 {
		ledger.setEntry(entryKey, githubApprovalLedgerEntry(approval, "suspect", number,
			verification.State, "post-creation verification failed: "+strings.Join(failures, ", ")))
		if err := githubLedgerWrite(ledger); err != nil {
			return nil, false, &GateIssuesGithubError{Message: err.Error()}
		}
		return nil, false, &GateIssuesGithubBlocked{Message: fmt.Sprintf(
			"%s: post-creation verification failed (%s) -- aborting the entire run immediately",
			context, strings.Join(failures, ", "))}
	}

	ledger.setEntry(entryKey, githubApprovalLedgerEntry(approval, "created", number,
		verification.State, nil))
	if err := githubLedgerWrite(ledger); err != nil {
		return nil, false, &GateIssuesGithubError{Message: err.Error()}
	}
	return ordered(
		"gate_id", approval.GateID, "authority_id", approval.AuthorityID,
		"status", "created", "issue_number", number, "issue_state", verification.State,
		"drift", nil), false, nil
}

// reuseGithubApprovalIssue takes over an issue that already exists.
func reuseGithubApprovalIssue(
	issues *GitHubIssueClient, ledger *issueLedger, repo string,
	approval ApprovalCandidate, raw any, botLogin, entryKey, context, wrap string,
	reconcileAssignees bool, mutations *githubMutations,
) (*orderedObject, bool, error) {
	entry, _ := raw.(map[string]any)
	if err := validateMatchedGithubIssue(entry, approval.Label,
		githubApprovalLabelPrefix, context); err != nil {
		return nil, false, err
	}
	number, _ := jsonNumber(entry["number"])

	verification, err := issues.FetchIssueVerification(repo, number)
	if err != nil {
		return nil, false, githubStructuralError(wrap, err)
	}
	if !strings.EqualFold(toStringOrEmpty(verification.AuthorLogin), botLogin) {
		ledger.setEntry(entryKey, githubApprovalLedgerEntry(approval, "suspect", number,
			verification.State, "matched issue author does not match the verified bot identity"))
		if err := githubLedgerWrite(ledger); err != nil {
			return nil, false, &GateIssuesGithubError{Message: err.Error()}
		}
		return nil, false, &GateIssuesGithubBlocked{Message: fmt.Sprintf(
			"%s: matched issue's author does not match the verified bot identity -- refusing to "+
				"reuse, needs human resolution", context)}
	}

	var drift any
	unreconciled := false
	if !sameStrings(lowercased(verification.Assignees),
		[]string{strings.ToLower(approval.Login)}) {
		drift = "assignee_changed"
		unreconciled = true
		if reconcileAssignees {
			mutations.next()
			if err := issues.UpdateIssueAssignees(repo, number, []string{approval.Login}); err != nil {
				return nil, false, githubStructuralError(wrap, err)
			}
			// The PATCH reporting success proves nothing: GitHub accepts an
			// assignment to somebody who has lost access and does not make it.
			refetch, err := issues.FetchIssueVerification(repo, number)
			if err != nil {
				return nil, false, githubStructuralError(wrap, err)
			}
			if !sameStrings(lowercased(refetch.Assignees),
				[]string{strings.ToLower(approval.Login)}) {
				ledger.setEntry(entryKey, githubApprovalLedgerEntry(approval, "suspect", number,
					refetch.State, "PATCH assignees silently dropped the assignee on re-verification"))
				if err := githubLedgerWrite(ledger); err != nil {
					return nil, false, &GateIssuesGithubError{Message: err.Error()}
				}
				return nil, false, &GateIssuesGithubBlocked{Message: fmt.Sprintf(
					"%s: PATCH assignees silently dropped the assignee -- refusing to report "+
						"success, needs human resolution", context)}
			}
			drift = "assignee_changed (reconciled)"
			unreconciled = false
		}
	}

	ledger.setEntry(entryKey, githubApprovalLedgerEntry(approval, "reused", number,
		verification.State, drift))
	if err := githubLedgerWrite(ledger); err != nil {
		return nil, false, &GateIssuesGithubError{Message: err.Error()}
	}
	return ordered(
		"gate_id", approval.GateID, "authority_id", approval.AuthorityID,
		"status", "reused", "issue_number", number, "issue_state", verification.State,
		"drift", drift), unreconciled, nil
}
