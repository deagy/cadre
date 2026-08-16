package kernel

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Assembling the reviewer report.
//
// The order of what follows is deliberate, and it is roughly "refuse before
// asking the forge anything". Classification, identity, and the pull request's
// own state are all checked before a single login is looked up, because each
// of those failures makes the whole report meaningless -- and a report that
// half-answers a question about who may approve a gate is worse than one that
// declines to.

// ReviewerReport is what `request-gate-reviewers` prints.
type ReviewerReport struct {
	Repo        string          `json:"repo"`
	PullRequest int             `json:"pr"`
	HeadSHA     string          `json:"pr_head_sha"`
	Draft       bool            `json:"pr_draft"`
	AuthorLogin any             `json:"pr_author_login"`
	BotLogin    string          `json:"as_bot_login"`
	GateIDs     []string        `json:"gate_ids"`
	Reviewers   []ReviewerEntry `json:"reviewers"`
	Skipped     []SkippedEntry  `json:"skipped"`
	Refusals    []RefusalEntry  `json:"refusals"`
	// Ordered by first appearance, not sorted: the Python kernel builds this
	// by walking the reviewer list, and a sorted map renders every summary
	// line in a different order for no reason a reader could name.
	Summary *orderedObject `json:"summary"`
}

// ReviewerEntry is one login and what is true about it.
type ReviewerEntry struct {
	Login          string       `json:"login"`
	Classification string       `json:"classification"`
	Motivations    []Motivation `json:"motivations"`
	WithheldCause  *PoisonCause `json:"withheld_cause"`
}

// ReviewerRequest is one `request-gate-reviewers` invocation.
type ReviewerRequest struct {
	Root                string
	TaskID              string
	Repo                string
	PullRequest         int
	AsBot               string
	Gates               []string
	AllowClassification string
}

// RequestGateReviewers builds the report.
func (r *Registry) RequestGateReviewers(request ReviewerRequest) (*ReviewerReport, error) {
	root, err := resolveExisting(request.Root)
	if err != nil {
		return nil, &GateReviewersError{Message: err.Error()}
	}
	taskID, err := SafeTaskID(request.TaskID)
	if err != nil {
		return nil, &GateReviewersError{Message: err.Error()}
	}

	record, dispatchPlan, authorities, err := loadTaskContext(root, taskID)
	if err != nil {
		return nil, err
	}
	// Loaded to fail closed on a corrupted bundled contract, even though this
	// report renders no gate name or phase. Parity with the issue publishers:
	// a kernel whose contract will not load should not answer questions about
	// gates at all.
	if _, err := lifecycleGateContracts(); err != nil {
		return nil, &GateReviewersError{Message: err.Error()}
	}

	gateByID := map[string]map[string]any{}
	for _, raw := range listOf(record["lifecycle_gates"]) {
		if gate, ok := raw.(map[string]any); ok {
			id, _ := gate["gate_id"].(string)
			gateByID[id] = gate
		}
	}

	// The classification handshake. An operator has to state what they expect
	// the task's classification to be, and be right, before this reads a
	// project's authority map and puts names in a report -- so a command
	// pointed at the wrong task cannot quietly disclose who approves what.
	if request.AllowClassification == "" ||
		request.AllowClassification != toStringOrEmpty(record["classification"]) {
		return nil, &GateReviewersError{Message: fmt.Sprintf(
			"--allow-classification must be supplied and exactly match the task's classification "+
				"(got %s, task classification is %s)",
			pythonRepr(nonEmptyOrNil(request.AllowClassification)), pythonRepr(record["classification"]))}
	}

	var gateIDs []string
	if len(request.Gates) > 0 {
		for _, gateID := range request.Gates {
			if err := CheckGateEligibility(gateID, dispatchPlan, gateByID[gateID]); err != nil {
				return nil, err
			}
			gateIDs = append(gateIDs, gateID)
		}
	} else {
		gateIDs = defaultGateIDs(dispatchPlan, gateByID)
	}

	client, err := NewGitHubClient()
	if err != nil {
		return nil, &GateReviewersError{Message: err.Error()}
	}
	verifiedLogin, err := client.VerifyIdentity(request.AsBot)
	if err != nil {
		return nil, &GateReviewersError{Message: err.Error()}
	}

	pullRequest, err := client.FetchPullRequest(request.Repo, request.PullRequest)
	if err != nil {
		return nil, &GateReviewersError{Message: err.Error()}
	}
	headSHA, authorLogin, draft, err := inspectPullRequest(
		request.Repo, request.PullRequest, pullRequest)
	if err != nil {
		return nil, err
	}

	plan, err := buildReviewerPlan(gateIDs, record, authorities,
		toStringOrEmpty(authorLogin), verifiedLogin,
		func(authority map[string]any) string { return AuthorityForgeLogin(authority, "github") },
		"no-github-binding", "pr-author-conflict")
	if err != nil {
		return nil, err
	}

	requestedLogins := map[string]bool{}
	requested, err := client.FetchRequestedReviewers(request.Repo, request.PullRequest)
	if err != nil {
		return nil, &GateReviewersError{Message: err.Error()}
	}
	for _, login := range requested {
		requestedLogins[strings.ToLower(login)] = true
	}
	reviews, err := fetchPullRequestReviews(request.Repo, request.PullRequest)
	if err != nil {
		return nil, &GateReviewersError{Message: err.Error()}
	}

	reviewers := []ReviewerEntry{}
	for _, key := range plan.order {
		login := plan.display[key]
		motivations := plan.motivations[key]

		// Poisoned first, before any forge lookup: a login that cannot be
		// asked does not need its existence confirmed, and asking would spend
		// a round trip to learn something that changes nothing.
		if cause, poisoned := plan.poisoned[key]; poisoned {
			withheld := cause
			reviewers = append(reviewers, ReviewerEntry{
				login, "withheld-conflict", motivations, &withheld})
			continue
		}

		exists, err := client.UserExists(login)
		if err != nil {
			return nil, &GateReviewersError{Message: err.Error()}
		}
		if !exists {
			reviewers = append(reviewers, ReviewerEntry{
				login, "github-user-unresolved", motivations, nil})
			continue
		}
		collaborator, err := client.IsCollaborator(request.Repo, login)
		if err != nil {
			return nil, &GateReviewersError{Message: err.Error()}
		}
		if !collaborator {
			reviewers = append(reviewers, ReviewerEntry{
				login, "not-a-collaborator", motivations, nil})
			continue
		}

		reviewers = append(reviewers, ReviewerEntry{
			login,
			ClassifyReviewerLogin(login, requestedLogins, reviews, headSHA),
			motivations, nil})
	}
	sort.SliceStable(reviewers, func(a, b int) bool {
		return strings.ToLower(reviewers[a].Login) < strings.ToLower(reviewers[b].Login)
	})

	summary := &orderedObject{values: map[string]any{}}
	for _, entry := range reviewers {
		count, _ := summary.values[entry.Classification].(int)
		summary.set(entry.Classification, count+1)
	}
	if gateIDs == nil {
		gateIDs = []string{}
	}

	return &ReviewerReport{
		Repo: request.Repo, PullRequest: request.PullRequest,
		HeadSHA: headSHA, Draft: draft, AuthorLogin: authorLogin,
		BotLogin: verifiedLogin, GateIDs: gateIDs,
		Reviewers: reviewers, Skipped: plan.skipped, Refusals: plan.refusals,
		Summary: summary,
	}, nil
}

// inspectPullRequest checks the pull request is one this report can describe.
func inspectPullRequest(
	repo string, number int, pullRequest map[string]any,
) (headSHA string, authorLogin any, draft bool, err error) {
	// Closed or merged: the question "who should review this" has no useful
	// answer, and reporting one invites somebody to review work that has
	// already landed.
	state := pullRequest["state"]
	merged := pullRequest["merged"] == true
	if state == "closed" || merged {
		return "", nil, false, &GateReviewersError{Message: fmt.Sprintf(
			"GitHub PR %s#%d is closed or merged (state=%s, merged=%s)",
			repo, number, pythonRepr(state), pythonBool(merged))}
	}

	// The base repository must be the one that was asked about. A pull request
	// number is only meaningful within a repository, and reporting on a fork's
	// PR as though it were this repository's would attribute the wrong
	// reviewers to a gate.
	base, _ := pullRequest["base"].(map[string]any)
	baseRepo, _ := base["repo"].(map[string]any)
	baseFullName, isString := baseRepo["full_name"].(string)
	if !isString || !strings.EqualFold(baseFullName, repo) {
		return "", nil, false, &GateReviewersError{Message: fmt.Sprintf(
			"GitHub PR %s#%d's base repository (%s) does not match --repo %s",
			repo, number, pythonRepr(baseRepo["full_name"]), pythonRepr(repo))}
	}

	head, _ := pullRequest["head"].(map[string]any)
	headSHA, _ = head["sha"].(string)
	if headSHA == "" {
		return "", nil, false, &GateReviewersError{Message: fmt.Sprintf(
			"GitHub PR %s#%d response is missing head.sha", repo, number)}
	}

	draft = pullRequest["draft"] == true
	if user, ok := pullRequest["user"].(map[string]any); ok {
		if login, ok := user["login"].(string); ok {
			authorLogin = login
		}
	}
	return headSHA, authorLogin, draft, nil
}

// fetchPullRequestReviews reads a pull request's reviews.
//
// Its own mock variable rather than the read client's: this endpoint predates
// that client in the Python kernel and existing fixtures name it, so folding
// it in would break every one of them for no gain.
func fetchPullRequestReviews(repo string, number int) ([]any, error) {
	mockPath := os.Getenv("AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE")
	var payload any
	if mockPath != "" {
		data, err := os.ReadFile(mockPath)
		if err != nil {
			return nil, err
		}
		var decoded any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil, err
		}
		payload = decoded
	} else {
		result, err := runForgeCLIIn("gh", []string{"gh", "api",
			fmt.Sprintf("repos/%s/pulls/%d/reviews", encodeRepoPath(repo), number)})
		if err != nil {
			return nil, err
		}
		if !result.ok() {
			return nil, fmt.Errorf("unable to fetch GitHub reviews for %s PR %d: %s",
				repo, number, result.detail("gh"))
		}
		decoded, err := parseForgeJSON("gh", "fetch_github_pr_reviews", result.stdout)
		if err != nil {
			return nil, err
		}
		payload = decoded
	}

	reviews, ok := payload.([]any)
	if !ok {
		return nil, fmt.Errorf("GitHub reviews response must be a JSON array")
	}
	// Every entry, or none. A response with a non-object in it is one this
	// kernel does not understand, and skipping the entry it cannot read would
	// silently drop a review that might be the approving one.
	for _, entry := range reviews {
		if _, ok := entry.(map[string]any); !ok {
			return nil, fmt.Errorf("GitHub reviews response contains non-object entries")
		}
	}
	return reviews, nil
}

// loadTaskContext reads the three documents every forge command starts from.
func loadTaskContext(
	root, taskID string,
) (record, dispatchPlan, authorities map[string]any, err error) {
	recordPath, err := ConfinedPath(root, Overlay, "runs", taskID, "run-record.json")
	if err != nil {
		return nil, nil, nil, &GateReviewersError{Message: err.Error()}
	}
	dispatchPath, err := ConfinedPath(root, Overlay, "runs", taskID, "dispatch-plan.json")
	if err != nil {
		return nil, nil, nil, &GateReviewersError{Message: err.Error()}
	}
	authoritiesPath, err := ConfinedPath(root, Overlay, "authorities.json")
	if err != nil {
		return nil, nil, nil, &GateReviewersError{Message: err.Error()}
	}

	if record, err = loadJSONObject(recordPath); err != nil {
		return nil, nil, nil, &GateReviewersError{Message: err.Error()}
	}
	if dispatchPlan, err = loadJSONObject(dispatchPath); err != nil {
		return nil, nil, nil, &GateReviewersError{Message: err.Error()}
	}
	if authorities, err = loadJSONObject(authoritiesPath); err != nil {
		return nil, nil, nil, &GateReviewersError{Message: err.Error()}
	}
	return record, dispatchPlan, authorities, nil
}

// pythonBool renders a Go bool the way Python prints one.
func pythonBool(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

// nonEmptyOrNil turns "" into nil, so pythonRepr prints None for an absent
// flag rather than an empty string.
func nonEmptyOrNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
