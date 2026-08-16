package kernel

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// `publish-reviewer-nudge` -- an advisory comment suggesting who a human might
// ask to review a pull request.
//
// This exists because the obvious feature is blocked. Actually requesting a
// reviewer needs `Pull requests: write`, which nobody has signed off; posting
// a comment needs `Issues: write`, which this kernel already has and already
// uses for the gate-status render. So the nudge is a suggestion somebody acts
// on, not an action -- and every design decision below follows from that being
// true rather than merely claimed.
//
// **Nobody is notified.** Logins are rendered as code spans, never as
// `@`-mentions. That is load-bearing, not cosmetic: an `@`-mention in a posted
// comment does trigger a GitHub notification, which would directly contradict
// the advisory paragraph's claim that nobody has been notified.
//
// **Who is named, and who is not.** Only the logins somebody could usefully
// ask -- `to-request` and `review-stale`. A login withheld for an independence
// conflict is counted and never named: saying in a public comment that a
// specific person cannot review because they authored the work is a
// data-exposure regression from the local report, where that reasoning
// already lives. Everything else -- already asked, already reviewed, or a
// resolution failure -- is omitted, because there is nothing to nudge about.
//
// **Only closed-enum data is rendered.** A login that has already been
// existence- and collaborator-checked, a gate id, a bundled contract's gate
// name, and an authority type. The run record's free-text `role` and
// `rationale` are deliberately never rendered, which is why this module needs
// no sanitizer -- `authority_type` stands in as the coarser, closed-enum
// signal instead.

const nudgeTemplateVersion = 1

// nudgeClassifications are the two states worth telling somebody about.
var nudgeClassifications = map[string]bool{"to-request": true, "review-stale": true}

// withheldClassification is counted but never named.
const withheldClassification = "withheld-conflict"

// nudgeClassificationLabels turn a classification into something a reader
// understands without knowing this kernel's vocabulary.
var nudgeClassificationLabels = map[string]string{
	"to-request":   "not yet requested",
	"review-stale": "review is stale (PR has changed since their last review)",
}

// nudgeAdvisory is a different claim from the gate-status advisory, and needed
// its own wording rather than reusing that one. That one says "this is not
// approval evidence"; this one says "nobody has been asked or notified".
const nudgeAdvisory = "**This is a suggestion, not a review request.**\n" +
	"`agentic-sdlc` has not requested a review from anyone, and these people have not been notified by this\n" +
	"comment being posted -- logins above are written as plain code spans, never as GitHub `@`-mentions,\n" +
	"specifically so that posting or updating this comment does not itself trigger a GitHub notification to\n" +
	"anyone. If you want to formally request a review, do so yourself in GitHub's UI (or `@`-mention someone\n" +
	"directly, which does notify them -- this comment deliberately does not). Reacting or replying to this\n" +
	"comment does not request, notify, or approve anything either. `agentic-sdlc` never reads this comment,\n" +
	"its reactions, or its replies back into gate state -- like `publish-gate-status`'s comment, this render\n" +
	"is strictly one-way and is never approval evidence."

// ReviewerNudgeError is a structural or policy failure -- exit 1.
type ReviewerNudgeError struct{ Message string }

func (e *ReviewerNudgeError) Error() string { return e.Message }

// ReviewerNudgeBlocked needs a human -- exit 2.
//
// Its own type even though the page cap it wraps is raised inside reused
// gate-status machinery, so this command's callers never need to know that
// module's exception hierarchy.
type ReviewerNudgeBlocked struct{ Message string }

func (e *ReviewerNudgeBlocked) Error() string { return e.Message }

// ComputeNudgeMarker identifies this comment.
//
// Its own domain tag, disjoint from the gate-status and gate-issue markers.
// Sharing one would let a task's nudge and its status comment match each
// other, and each would then keep overwriting the other.
func ComputeNudgeMarker(taskID string) string {
	return hexSHA256([]byte("reviewer-nudge\x00" + taskID))[:16]
}

func nudgeMarkerPattern(marker string) *regexp.Regexp {
	return regexp.MustCompile(
		`<!-- agentic-sdlc:reviewer-nudge:v\d+:` + regexp.QuoteMeta(marker) + ` -->`)
}

// RenderReviewerNudgeBody builds the comment from a reviewer report.
func RenderReviewerNudgeBody(
	taskID string, report *ReviewerReport,
	contracts map[string]map[string]any, renderedAt string,
) string {
	marker := ComputeNudgeMarker(taskID)

	lines := []string{
		fmt.Sprintf("<!-- agentic-sdlc:reviewer-nudge:v%d:%s -->", nudgeTemplateVersion, marker),
		"> Machine-generated by agentic-sdlc. Not a human-authored artifact. **Not a review request.**",
		"> No one has been asked or notified by this comment being posted.",
		"",
		fmt.Sprintf("**Lifecycle reviewer nudge — task `%s`**", TaskHash(taskID)),
		fmt.Sprintf("PR: %s#%d · rendered %s", report.Repo, report.PullRequest, renderedAt),
		"",
	}

	var toNudge []ReviewerEntry
	withheld := 0
	for _, entry := range report.Reviewers {
		switch {
		case nudgeClassifications[entry.Classification]:
			toNudge = append(toNudge, entry)
		case entry.Classification == withheldClassification:
			withheld++
		}
	}

	if len(toNudge) > 0 {
		lines = append(lines, "Suggested reviewers:", "")
		for _, entry := range toNudge {
			label := nudgeClassificationLabels[entry.Classification]
			if label == "" {
				label = entry.Classification
			}
			// A code span, never an @-mention. See the file header: the
			// difference is whether posting this notifies somebody.
			lines = append(lines, fmt.Sprintf("- `%s` — %s", entry.Login, label))
			for _, motivation := range entry.Motivations {
				contract := contracts[motivation.GateID]
				gateName := motivation.GateID
				if name, ok := contract["name"].(string); ok && name != "" {
					gateName = name
				}
				authorityType := "authority"
				if declared, ok := motivation.AuthorityType.(string); ok && declared != "" {
					authorityType = declared
				}
				lines = append(lines, fmt.Sprintf("  - %s %s (%s)",
					motivation.GateID, gateName, authorityType))
			}
		}
		lines = append(lines, "")
	} else {
		lines = append(lines, "No reviewers to nudge for this PR right now.", "")
	}

	if withheld > 0 {
		// Counted, never named. The reason a specific person is conflicted
		// stays in the local report.
		plural := "s"
		if withheld == 1 {
			plural = ""
		}
		lines = append(lines, fmt.Sprintf(
			"%d additional reviewer%s not shown due to a gate-independence conflict "+
				"— see the full report locally.", withheld, plural))
		lines = append(lines, "")
	}

	lines = append(lines, "---", "")
	lines = append(lines, strings.Split(nudgeAdvisory, "\n")...)
	return strings.Join(lines, "\n") + "\n"
}

// ReviewerNudgeRequest is one `publish-reviewer-nudge` invocation.
type ReviewerNudgeRequest struct {
	Root                string
	TaskID              string
	Repo                string
	PullRequest         int
	AsBot               string
	Gates               []string
	AllowClassification string
	Apply               bool
	BreakLock           bool
	KnowinglyMocked     bool
}

// PublishReviewerNudge renders the nudge and, with Apply, posts it.
//
// The reviewer report is produced by calling the reporting command's own
// entry point rather than rebuilding it here. That call already owns identity
// verification, the pull-request fetch and validation, and the
// requested-reviewers, reviews, user-existence and collaborator checks --
// a second path through any of those is a second thing to keep in step.
func (r *Registry) PublishReviewerNudge(
	request ReviewerNudgeRequest,
) (*orderedObject, error) {
	report, err := r.RequestGateReviewers(ReviewerRequest{
		Root: request.Root, TaskID: request.TaskID, Repo: request.Repo,
		PullRequest: request.PullRequest, AsBot: request.AsBot,
		Gates: request.Gates, AllowClassification: request.AllowClassification,
	})
	if err != nil {
		return nil, err
	}

	root, err := resolveExisting(request.Root)
	if err != nil {
		return nil, &ReviewerNudgeError{Message: err.Error()}
	}
	taskID, err := SafeTaskID(request.TaskID)
	if err != nil {
		return nil, &ReviewerNudgeError{Message: err.Error()}
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		return nil, &ReviewerNudgeError{Message: err.Error()}
	}

	renderedAt := nowRFC3339()
	marker := ComputeNudgeMarker(taskID)
	body := RenderReviewerNudgeBody(taskID, report, contracts, renderedAt)

	client, err := NewGitHubStatusClient()
	if err != nil {
		return nil, &ReviewerNudgeError{Message: err.Error()}
	}
	adapter := &gitHubStatusAdapter{
		client: client, repo: request.Repo, pullRequest: request.PullRequest}

	verifiedUsername, err := adapter.VerifyIdentity(request.AsBot)
	if err != nil {
		return nil, &ReviewerNudgeError{Message: err.Error()}
	}
	comments, err := adapter.ListComments()
	if err != nil {
		// The page cap is raised by the reused adapter as a gate-status
		// refusal; re-typed here so a caller of this command never has to
		// know that.
		return nil, &ReviewerNudgeBlocked{Message: err.Error()}
	}

	pattern := nudgeMarkerPattern(marker)
	var matches []NormalizedComment
	for _, comment := range comments {
		if comment.IsSystem {
			continue
		}
		if pattern.MatchString(comment.Body) {
			matches = append(matches, comment)
		}
	}
	action, reason, matched := ClassifyStatusComment(matches, verifiedUsername, body)

	nudgedLogins := []string{}
	seen := map[string]bool{}
	withheld := 0
	for _, entry := range report.Reviewers {
		switch {
		case nudgeClassifications[entry.Classification]:
			if !seen[entry.Login] {
				seen[entry.Login] = true
				nudgedLogins = append(nudgedLogins, entry.Login)
			}
		case entry.Classification == withheldClassification:
			withheld++
		}
	}
	sort.Strings(nudgedLogins)

	mode := "dry-run"
	if request.Apply {
		mode = "apply"
	}
	var matchedID any
	if matched != nil {
		matchedID = matched.ID
	}
	summary := ordered(
		"mode", mode,
		"task_id", taskID,
		"task_hash", TaskHash(taskID),
		"repo", request.Repo,
		"pr", request.PullRequest,
		"marker", marker,
		"action", action,
		"reason", nonEmptyOrNil(reason),
		"matched_comment_id", matchedID,
		"mocked", adapter.Mocked(),
		"body", body,
		"nudged_logins", asJSONList(nudgedLogins),
		"withheld_count", withheld,
	)

	if !request.Apply {
		return summary, nil
	}
	if adapter.Mocked() && !request.KnowinglyMocked {
		return nil, &ReviewerNudgeError{Message: "a mock backend env var is set but " +
			"--i-know-this-is-mocked was not passed -- refusing to --apply against a " +
			"mocked forge backend"}
	}
	if action == "blocked" {
		return nil, &ReviewerNudgeBlocked{Message: fmt.Sprintf(
			"%s: refusing to create or update a reviewer-nudge comment -- needs human resolution",
			reason)}
	}

	return r.applyReviewerNudge(root, taskID, request, adapter, summary,
		action, verifiedUsername, marker, body, matched)
}

// applyReviewerNudge performs the write and records it.
func (r *Registry) applyReviewerNudge(
	root, taskID string, request ReviewerNudgeRequest, adapter *gitHubStatusAdapter,
	summary *orderedObject, action, verifiedUsername, marker, body string,
	matched *NormalizedComment,
) (*orderedObject, error) {
	lockPath, err := LedgerPath(root, Overlay, taskID, "reviewer-nudge-"+ForgeGitHub+".lock")
	if err != nil {
		return nil, &ReviewerNudgeError{Message: err.Error()}
	}
	if err := AcquireLockFile(lockPath, request.BreakLock); err != nil {
		return nil, &ReviewerNudgeBlocked{Message: err.Error()}
	}
	defer func() { _ = ReleaseLockFile(lockPath) }()

	ledgerPath, err := LedgerPath(root, Overlay, taskID, "reviewer-nudge-"+ForgeGitHub+".json")
	if err != nil {
		return nil, &ReviewerNudgeError{Message: err.Error()}
	}
	ledger, err := r.loadStatusLedger(ledgerPath, taskID, ForgeGitHub)
	if err != nil {
		return nil, &ReviewerNudgeError{Message: err.Error()}
	}
	ledger.set("target", adapter.Target())
	ledger.set("bot_username", verifiedUsername)
	ledger.set("mocked", adapter.Mocked())
	ledger.set("marker", marker)

	if action == "unchanged" {
		var commentID any
		if matched != nil {
			commentID = matched.ID
		}
		ledger.set("entries", append(listOf(ledger.values["entries"]), ordered(
			"action", "unchanged", "comment_id", commentID, "recorded_at", nowRFC3339())))
		if err := WriteLedgerFile(ledgerPath, ledger, ".reviewer-nudge."); err != nil {
			return nil, &ReviewerNudgeError{Message: err.Error()}
		}
		summary.set("comment_id", commentID)
		return summary, nil
	}

	var result NormalizedComment
	if action == "create" {
		result, err = adapter.CreateComment(body)
	} else {
		result, err = adapter.UpdateComment(matched.ID, body)
	}
	if err != nil {
		return nil, &ReviewerNudgeError{Message: err.Error()}
	}

	if !strings.EqualFold(result.Author, verifiedUsername) || result.Body != body {
		ledger.set("entries", append(listOf(ledger.values["entries"]), ordered(
			"action", action, "status", "suspect", "comment_id", result.ID,
			"recorded_at", nowRFC3339(),
			"detail", "post-write verification failed: author or body mismatch after create/update")))
		if err := WriteLedgerFile(ledgerPath, ledger, ".reviewer-nudge."); err != nil {
			return nil, &ReviewerNudgeError{Message: err.Error()}
		}
		return nil, &ReviewerNudgeBlocked{Message: fmt.Sprintf(
			"post-write verification failed for %s on github -- author or body did not match "+
				"after the write; aborting immediately", action)}
	}

	ledger.set("entries", append(listOf(ledger.values["entries"]), ordered(
		"action", action, "status", "verified", "comment_id", result.ID,
		"recorded_at", nowRFC3339())))
	if err := WriteLedgerFile(ledgerPath, ledger, ".reviewer-nudge."); err != nil {
		return nil, &ReviewerNudgeError{Message: err.Error()}
	}
	summary.set("comment_id", result.ID)
	return summary, nil
}
