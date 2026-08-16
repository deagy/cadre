package kernel

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// `request-gate-reviewers-gitlab` -- the same report against a merge request.
//
// The policy is shared with the GitHub side: eligibility, independence, and
// the request-wide poisoning rule are the same regardless of forge, so both
// call one planner. What differs is only how a login is resolved and what two
// of the reason codes are called.
//
// Two things GitLab does not offer, and neither is worked around:
//
//   - There is **no review-stale classification**. GitLab's approvals endpoint
//     exposes no per-approval commit, so there is no way to tell an approval
//     of this head from an approval of an older one. Inventing one from the
//     merge request's own SHA would attribute every approval to the current
//     commit, which is exactly the wrong answer when it matters.
//   - A username lookup is a search, so **gitlab-user-ambiguous** exists here
//     and has no GitHub counterpart. Two accounts matching one username is a
//     question for a human, not something to resolve by picking the first.

// gitlabProblemClassifications are the states that mean somebody has to act
// before these reviewers can be requested. Two of the three have no GitHub
// counterpart, which is why this is a separate set rather than a shared one.
var gitlabProblemClassifications = map[string]bool{
	"withheld-conflict": true, "gitlab-user-unresolved": true, "gitlab-user-ambiguous": true,
}

// GateReviewersGitlabError is the GitLab report's structural failure.
//
// Its own type rather than a re-export, so a caller can tell which forge's
// report failed without reading the message.
type GateReviewersGitlabError struct{ Message string }

func (e *GateReviewersGitlabError) Error() string { return e.Message }

// GitLabReviewerReport is what `request-gate-reviewers-gitlab` prints.
type GitLabReviewerReport struct {
	ProjectPath    string                `json:"project_path"`
	MergeRequest   int                   `json:"mr_iid"`
	HeadSHA        string                `json:"mr_head_sha"`
	Draft          bool                  `json:"mr_draft"`
	AuthorUsername any                   `json:"mr_author_username"`
	BotUsername    string                `json:"as_bot_username"`
	GateIDs        []string              `json:"gate_ids"`
	Reviewers      []GitLabReviewerEntry `json:"reviewers"`
	Skipped        []SkippedEntry        `json:"skipped"`
	Refusals       []RefusalEntry        `json:"refusals"`
	Summary        *orderedObject        `json:"summary"`
}

// GitLabReviewerEntry is one username and what is true about it.
type GitLabReviewerEntry struct {
	Username       string       `json:"username"`
	Classification string       `json:"classification"`
	Motivations    []Motivation `json:"motivations"`
	WithheldCause  *PoisonCause `json:"withheld_cause"`
}

// GitLabReviewerRequest is one invocation.
type GitLabReviewerRequest struct {
	Root                string
	TaskID              string
	ProjectPath         string
	MergeRequestIID     int
	AsBot               string
	Gates               []string
	AllowClassification string
}

// RequestGateReviewersGitLab builds the merge-request reviewer report.
func (r *Registry) RequestGateReviewersGitLab(
	request GitLabReviewerRequest,
) (*GitLabReviewerReport, error) {
	root, err := resolveExisting(request.Root)
	if err != nil {
		return nil, &GateReviewersGitlabError{Message: err.Error()}
	}
	taskID, err := SafeTaskID(request.TaskID)
	if err != nil {
		return nil, &GateReviewersGitlabError{Message: err.Error()}
	}
	record, dispatchPlan, authorities, err := loadTaskContext(root, taskID)
	if err != nil {
		return nil, &GateReviewersGitlabError{Message: err.Error()}
	}
	if _, err := lifecycleGateContracts(); err != nil {
		return nil, &GateReviewersGitlabError{Message: err.Error()}
	}

	gateByID := map[string]map[string]any{}
	for _, raw := range listOf(record["lifecycle_gates"]) {
		if gate, ok := raw.(map[string]any); ok {
			id, _ := gate["gate_id"].(string)
			gateByID[id] = gate
		}
	}

	if request.AllowClassification == "" ||
		request.AllowClassification != toStringOrEmpty(record["classification"]) {
		return nil, &GateReviewersGitlabError{Message: fmt.Sprintf(
			"--allow-classification must be supplied and exactly match the task's classification "+
				"(got %s, task classification is %s)",
			pythonRepr(nonEmptyOrNil(request.AllowClassification)), pythonRepr(record["classification"]))}
	}

	var gateIDs []string
	if len(request.Gates) > 0 {
		for _, gateID := range request.Gates {
			if err := CheckGateEligibility(gateID, dispatchPlan, gateByID[gateID]); err != nil {
				return nil, &GateReviewersGitlabError{Message: err.Error()}
			}
			gateIDs = append(gateIDs, gateID)
		}
	} else {
		gateIDs = defaultGateIDs(dispatchPlan, gateByID)
	}

	client, err := NewGitLabClient()
	if err != nil {
		return nil, &GateReviewersGitlabError{Message: err.Error()}
	}
	verifiedUsername, err := client.VerifyIdentity(request.AsBot)
	if err != nil {
		return nil, &GateReviewersGitlabError{Message: err.Error()}
	}

	mergeRequest, err := client.FetchMergeRequest(request.ProjectPath, request.MergeRequestIID)
	if err != nil {
		return nil, &GateReviewersGitlabError{Message: err.Error()}
	}
	headSHA, authorUsername, draft, err := inspectMergeRequest(
		request.ProjectPath, request.MergeRequestIID, mergeRequest)
	if err != nil {
		return nil, err
	}

	plan, err := buildReviewerPlan(gateIDs, record, authorities,
		toStringOrEmpty(authorUsername), verifiedUsername,
		func(authority map[string]any) string { return AuthorityForgeLogin(authority, "gitlab") },
		"no-gitlab-binding", "mr-author-conflict")
	if err != nil {
		return nil, &GateReviewersGitlabError{Message: err.Error()}
	}

	assignedReviewers := map[string]bool{}
	for _, raw := range listOf(mergeRequest["reviewers"]) {
		reviewer, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if username, ok := reviewer["username"].(string); ok && username != "" {
			assignedReviewers[strings.ToLower(username)] = true
		}
	}

	approvals, err := fetchMergeRequestApprovals(request.ProjectPath, request.MergeRequestIID)
	if err != nil {
		return nil, &GateReviewersGitlabError{Message: err.Error()}
	}
	approvedUsernames := map[string]bool{}
	for _, approval := range approvals {
		if username, ok := approval["username"].(string); ok {
			approvedUsernames[strings.ToLower(username)] = true
		}
	}

	reviewers := []GitLabReviewerEntry{}
	for _, key := range plan.order {
		username := plan.display[key]
		motivations := plan.motivations[key]

		if cause, poisoned := plan.poisoned[key]; poisoned {
			withheld := cause
			reviewers = append(reviewers, GitLabReviewerEntry{
				username, "withheld-conflict", motivations, &withheld})
			continue
		}

		matches, err := resolveActiveUsernames(client, username)
		if err != nil {
			return nil, &GateReviewersGitlabError{Message: err.Error()}
		}
		switch {
		case len(matches) == 0:
			reviewers = append(reviewers, GitLabReviewerEntry{
				username, "gitlab-user-unresolved", motivations, nil})
			continue
		case len(matches) > 1:
			// Two accounts answering to one username is a question for a
			// human. Picking either would assign a gate's review to somebody
			// nobody named.
			reviewers = append(reviewers, GitLabReviewerEntry{
				username, "gitlab-user-ambiguous", motivations, nil})
			continue
		}

		reviewers = append(reviewers, GitLabReviewerEntry{
			username,
			ClassifyReviewerUsername(username, assignedReviewers, approvedUsernames),
			motivations, nil})
	}
	sort.SliceStable(reviewers, func(a, b int) bool {
		return strings.ToLower(reviewers[a].Username) < strings.ToLower(reviewers[b].Username)
	})

	summary := &orderedObject{values: map[string]any{}}
	for _, entry := range reviewers {
		count, _ := summary.values[entry.Classification].(int)
		summary.set(entry.Classification, count+1)
	}
	if gateIDs == nil {
		gateIDs = []string{}
	}

	return &GitLabReviewerReport{
		ProjectPath: request.ProjectPath, MergeRequest: request.MergeRequestIID,
		HeadSHA: headSHA, Draft: draft, AuthorUsername: authorUsername,
		BotUsername: verifiedUsername, GateIDs: gateIDs,
		Reviewers: reviewers, Skipped: plan.skipped, Refusals: plan.refusals,
		Summary: summary,
	}, nil
}

// ClassifyReviewerUsername decides what state a username is in.
//
// Priority: already-approved, then already-reviewer, then to-request. No
// stale case, because GitLab does not say which commit an approval was for --
// see the file header.
func ClassifyReviewerUsername(
	username string, assignedReviewers, approvedUsernames map[string]bool,
) string {
	normalized := strings.ToLower(username)
	switch {
	case approvedUsernames[normalized]:
		return "already-approved"
	case assignedReviewers[normalized]:
		return "already-reviewer"
	default:
		return "to-request"
	}
}

// resolveActiveUsernames keeps the matches that are real, active accounts.
func resolveActiveUsernames(client *GitLabClient, username string) ([]map[string]any, error) {
	matches, err := client.ResolveUsername(username)
	if err != nil {
		return nil, err
	}
	var active []map[string]any
	for _, raw := range matches {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, hasID := jsonNumber(entry["id"]); !hasID {
			continue
		}
		// A missing state means active, which is what GitLab omits it for. A
		// blocked or deactivated account is not somebody who can review.
		state, present := entry["state"]
		if present && state != "active" {
			continue
		}
		active = append(active, entry)
	}
	return active, nil
}

// inspectMergeRequest checks the merge request is one this report can describe.
func inspectMergeRequest(
	projectPath string, iid int, mergeRequest map[string]any,
) (headSHA string, authorUsername any, draft bool, err error) {
	state := mergeRequest["state"]
	if state == "closed" || state == "merged" {
		return "", nil, false, &GateReviewersGitlabError{Message: fmt.Sprintf(
			"GitLab MR %s!%d is closed or merged (state=%s)",
			projectPath, iid, pythonRepr(state))}
	}

	mergeRequestPath := mergeRequestProjectPath(mergeRequest)
	if mergeRequestPath == nil || !strings.EqualFold(toStringOrEmpty(mergeRequestPath), projectPath) {
		return "", nil, false, &GateReviewersGitlabError{Message: fmt.Sprintf(
			"GitLab MR %s!%d's project (%s) does not match --project-path %s",
			projectPath, iid, pythonRepr(mergeRequestPath), pythonRepr(projectPath))}
	}

	headSHA, _ = mergeRequest["sha"].(string)
	if headSHA == "" {
		return "", nil, false, &GateReviewersGitlabError{Message: fmt.Sprintf(
			"GitLab MR %s!%d response is missing sha", projectPath, iid)}
	}

	draft = mergeRequestIsDraft(mergeRequest)
	if author, ok := mergeRequest["author"].(map[string]any); ok {
		if username, ok := author["username"].(string); ok {
			authorUsername = username
		}
	}
	return headSHA, authorUsername, draft, nil
}

// mergeRequestProjectPath reads the project from the full reference.
func mergeRequestProjectPath(mergeRequest map[string]any) any {
	references, ok := mergeRequest["references"].(map[string]any)
	if !ok {
		return nil
	}
	full, ok := references["full"].(string)
	if !ok || !strings.Contains(full, "!") {
		return nil
	}
	return full[:strings.LastIndex(full, "!")]
}

// mergeRequestIsDraft prefers the field, then the legacy field, then the title.
//
// Three sources because instances differ: `draft` is current, but
// `work_in_progress` predates it and some self-hosted instances still expose
// only the title convention.
func mergeRequestIsDraft(mergeRequest map[string]any) bool {
	if draft, ok := mergeRequest["draft"].(bool); ok {
		return draft
	}
	if workInProgress, ok := mergeRequest["work_in_progress"].(bool); ok {
		return workInProgress
	}
	if title, ok := mergeRequest["title"].(string); ok {
		normalized := strings.ToLower(strings.TrimSpace(title))
		return strings.HasPrefix(normalized, "draft:") || strings.HasPrefix(normalized, "wip:")
	}
	return false
}

// fetchMergeRequestApprovals reads who has approved, normalised.
func fetchMergeRequestApprovals(projectPath string, iid int) ([]map[string]any, error) {
	var raw any
	if mockPath := os.Getenv("AGENTIC_SDLC_TEST_GITLAB_APPROVALS_FILE"); mockPath != "" {
		data, err := os.ReadFile(mockPath)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
	} else {
		result, err := runForgeCLIIn("glab", []string{"glab", "api", fmt.Sprintf(
			"projects/%s/merge_requests/%d/approvals", percentEncode(projectPath), iid)})
		if err != nil {
			return nil, err
		}
		if !result.ok() {
			return nil, fmt.Errorf("unable to fetch GitLab MR approvals for %s MR %d: %s",
				projectPath, iid, result.detail("glab"))
		}
		if raw, err = parseForgeJSON("glab", "fetch_gitlab_mr_approvals", result.stdout); err != nil {
			return nil, err
		}
	}
	// Both branches go through the same normaliser, so a test exercising the
	// mock also exercises the data-minimisation wiring rather than bypassing
	// it.
	return normaliseGitLabApprovals(raw)
}

// normaliseGitLabApprovals turns GitLab's single approvals object into one
// record per approver, shaped like GitHub's per-review list.
//
// Only the fields evidence needs are read: username, an identifier, the state,
// a time, and the commit. Name, email and avatar are deliberately never
// touched -- the kernel has no use for them and reading them would put them in
// a record that gets published.
func normaliseGitLabApprovals(raw any) ([]map[string]any, error) {
	response, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("GitLab approvals API response must be a JSON object")
	}
	commitSHA := response["sha"]

	records := []map[string]any{}
	for _, entry := range listOf(response["approved_by"]) {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		user, ok := item["user"].(map[string]any)
		if !ok {
			continue
		}
		username, ok := user["username"].(string)
		if !ok || username == "" {
			continue
		}

		// Presence in approved_by is GitLab's per-user approval signal, and it
		// is independent of the merge request's own `approved` flag -- that
		// one only says whether the overall rule threshold is met. Conflating
		// them would hide an approver who has signed while others have not.
		//
		// Two fields here are approximations, and callers should know it:
		// `decided_at` is the merge request's updated_at, which moves on any
		// update, and `commit_sha` is the merge request's own head applied
		// uniformly. GitLab's approvals endpoint exposes neither per approver.
		identifier := username
		if userID, hasID := jsonNumber(user["id"]); hasID {
			identifier = fmt.Sprint(userID)
		}
		records = append(records, map[string]any{
			"approval_id": identifier,
			"username":    username,
			"state":       "approved",
			"decided_at":  response["updated_at"],
			"commit_sha":  commitSHA,
		})
	}
	return records, nil
}
