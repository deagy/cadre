package kernel

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Reading approvals and issues off a forge, for the adapters that record them.
//
// These are the reads behind `approve-from-github-pr`, `approve-from-gitlab-mr`
// and the four `link-*-from-*-issue` commands. They are separate from the
// clients in githubclient.go and gitlabclient.go, and deliberately so: those
// serve commands that write to a forge and answer from one mock file each,
// while these are read-only, answer from their own mock files, and are the
// oldest forge code in the kernel -- their error wording and their fallback to
// stdout when stderr is empty are what the Python kernel says, and changing
// either would change what an operator greps for.
//
// **Data minimization runs through all of them.** A GitLab approval carries a
// name, an email and an avatar URL; none is read here. Only the pseudonymous
// username, the approval identifier, the state, the time and the commit reach
// the evidence record. The issue fetchers read no identity at all -- an issue
// link has no approver, so there is nothing to minimize away.

// The mock files these reads answer from.
//
// GitHubIssueMockEnv appears twice in this kernel, and that is the Python
// kernel's collision rather than a mistake here: FetchGitHubIssue reads it as
// a single issue object, while the issue-writing client reads it as a map of
// canned responses. The two commands are never run together, so neither has
// ever seen the other's shape.
const (
	GitHubReviewsMockEnv   = "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE"
	GitLabApprovalsMockEnv = "AGENTIC_SDLC_TEST_GITLAB_APPROVALS_FILE"
	GitLabIssueFetchMock   = "AGENTIC_SDLC_TEST_GITLAB_ISSUE_FILE"
	GitHubIssueFetchMock   = GitHubIssueMockEnv
)

// readForgeMockValue reads a mock file as an arbitrary JSON value.
//
// Unlike loadForgeMock this does not require an object: two of these mocks are
// arrays, because the responses they stand in for are.
func readForgeMockValue(variable string) (any, bool, error) {
	path := os.Getenv(variable)
	if path == "" {
		return nil, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, true, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, true, err
	}
	return value, true, nil
}

// legacyDetail is the failure text these particular reads quote.
//
// Falls back to stdout before the stand-in, which the newer clients do not.
// Kept because it is what the Python kernel prints, and an operator who has
// seen `gh` put its complaint on stdout would otherwise get "unknown gh api
// failure" where they used to get the complaint.
func legacyDetail(result forgeResult, tool string) string {
	if trimmed := strings.TrimSpace(result.stderr); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(string(result.stdout)); trimmed != "" {
		return trimmed
	}
	return "unknown " + tool + " api failure"
}

// FetchGitHubPullRequestReviews reads every review on a pull request.
func FetchGitHubPullRequestReviews(repo string, pullRequest int) ([]any, error) {
	value, mocked, err := readForgeMockValue(GitHubReviewsMockEnv)
	if err != nil {
		return nil, err
	}
	if !mocked {
		path := fmt.Sprintf("repos/%s/pulls/%d/reviews", encodeRepoPath(repo), pullRequest)
		result, err := runForgeCLIIn("gh", []string{"gh", "api", path})
		if err != nil {
			return nil, err
		}
		if !result.ok() {
			return nil, fmt.Errorf("unable to fetch GitHub reviews for %s PR %d: %s",
				repo, pullRequest, legacyDetail(result, "gh"))
		}
		if value, err = parseForgeJSON("gh", "fetch_github_pr_reviews", result.stdout); err != nil {
			return nil, err
		}
	}

	entries, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("GitHub reviews response must be a JSON array")
	}
	reviews := []any{}
	for _, entry := range entries {
		if _, isObject := entry.(map[string]any); !isObject {
			return nil, fmt.Errorf("GitHub reviews response contains non-object entries")
		}
		reviews = append(reviews, entry)
	}
	return reviews, nil
}

// SelectGitHubReview picks the review that counts as this reviewer's decision.
//
// The *latest* one, and then it must be an approval. Taking the latest
// approval instead would let a reviewer's approval survive their own
// subsequent request for changes -- the newest decision is the one that
// stands, and if it is not an approval there is nothing here to record.
func SelectGitHubReview(reviews []any, reviewerLogin, commitSHA string) (map[string]any, error) {
	wantedLogin := strings.ToLower(reviewerLogin)
	wantedCommit := NormalizeCommitSHA(commitSHA)

	var matching []map[string]any
	for _, raw := range reviews {
		review, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		user, _ := review["user"].(map[string]any)
		login, isString := user["login"].(string)
		if !isString || strings.ToLower(login) != wantedLogin {
			continue
		}
		if !IsValidDatetime(review["submitted_at"]) {
			continue
		}
		if wantedCommit != "" && NormalizeCommitSHA(toStringOrEmpty(review["commit_id"])) != wantedCommit {
			continue
		}
		matching = append(matching, review)
	}
	if len(matching) == 0 {
		return nil, fmt.Errorf("no GitHub review found for reviewer %s%s",
			reviewerLogin, atCommit(commitSHA))
	}
	// Stable, so equal timestamps keep the order the API returned -- Python's
	// sort is stable too, and which of two same-second reviews wins is
	// otherwise arbitrary in a way that would show up as a flaky difference.
	sort.SliceStable(matching, func(a, b int) bool {
		return toStringOrEmpty(matching[a]["submitted_at"]) <
			toStringOrEmpty(matching[b]["submitted_at"])
	})
	latest := matching[len(matching)-1]

	dismissed := toStringOrEmpty(latest["dismissed_state"])
	if latest["state"] != "APPROVED" ||
		strings.EqualFold(dismissed, "dismissed") {
		return nil, fmt.Errorf(
			"latest GitHub review for reviewer %s is not an effective approval", reviewerLogin)
	}
	return latest, nil
}

func atCommit(commitSHA string) string {
	if commitSHA == "" {
		return ""
	}
	return " at commit " + commitSHA
}

// NormalizeCommitSHA lowercases and trims a commit, or returns "".
func NormalizeCommitSHA(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// GitLabApprovalRecordsFromAPIResponse turns GitLab's one approvals object
// into per-approver records, shaped like GitHub's per-review list.
//
// Three things about the result are approximations, and each is worth knowing
// before treating one as evidence:
//
//   - `state` is always "approved". GitLab's approvals API has no per-user
//     pending or rejected value; presence in `approved_by` *is* the signal,
//     and it is independent of the MR-level `approved` flag, which only says
//     whether the rule threshold is met.
//   - `decided_at` is the MR's `updated_at`, not a per-approver time. GitLab
//     does not expose one. It moves on any MR update, so it can misstate when
//     this approver actually decided.
//   - `commit_sha` is the MR's `sha`, applied to every approver alike. So
//     `--commit-sha` filtering is only sound on a project with "reset
//     approvals on push" enabled; without it, a stale approval against an old
//     commit stays in `approved_by` and gets attributed to the current head.
//     Nothing here verifies that setting.
func GitLabApprovalRecordsFromAPIResponse(raw any) ([]any, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("GitLab approvals API response must be a JSON object")
	}
	commitSHA := object["sha"]
	records := []any{}
	for _, entry := range listOf(object["approved_by"]) {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		user, _ := item["user"].(map[string]any)
		username, isString := user["username"].(string)
		if !isString || username == "" {
			continue
		}
		// Only these five fields. name, email and avatar_url are present in
		// the response and are deliberately never read.
		approvalID := username
		if userID, present := user["id"]; present && userID != nil {
			approvalID = pythonStr(userID)
		}
		records = append(records, map[string]any{
			"approval_id": approvalID,
			"username":    username,
			"state":       "approved",
			"decided_at":  object["updated_at"],
			"commit_sha":  commitSHA,
		})
	}
	return records, nil
}

// FetchGitLabMergeRequestApprovals reads a merge request's approvals.
func FetchGitLabMergeRequestApprovals(projectPath string, mrIID int) ([]any, error) {
	value, mocked, err := readForgeMockValue(GitLabApprovalsMockEnv)
	if err != nil {
		return nil, err
	}
	if !mocked {
		path := fmt.Sprintf("projects/%s/merge_requests/%d/approvals",
			percentEncode(projectPath), mrIID)
		result, err := runForgeCLIIn("glab", []string{"glab", "api", path})
		if err != nil {
			return nil, err
		}
		if !result.ok() {
			return nil, fmt.Errorf("unable to fetch GitLab MR approvals for %s MR %d: %s",
				projectPath, mrIID, legacyDetail(result, "glab"))
		}
		if value, err = parseForgeJSON("glab", "fetch_gitlab_mr_approvals", result.stdout); err != nil {
			return nil, err
		}
	}
	// Both paths go through the normalizer, so a mocked run exercises the
	// data-minimization wiring rather than bypassing it.
	return GitLabApprovalRecordsFromAPIResponse(value)
}

// SelectGitLabApproval picks the approval that counts as this approver's.
func SelectGitLabApproval(
	approvals []any, approverUsername, commitSHA string,
) (map[string]any, error) {
	wantedUsername := strings.ToLower(approverUsername)
	wantedCommit := NormalizeCommitSHA(commitSHA)

	var matching []map[string]any
	for _, raw := range approvals {
		approval, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		username, isString := approval["username"].(string)
		if !isString || strings.ToLower(username) != wantedUsername {
			continue
		}
		if !IsValidDatetime(approval["decided_at"]) {
			continue
		}
		if wantedCommit != "" &&
			NormalizeCommitSHA(toStringOrEmpty(approval["commit_sha"])) != wantedCommit {
			continue
		}
		matching = append(matching, approval)
	}
	if len(matching) == 0 {
		return nil, fmt.Errorf("no GitLab approval found for approver %s%s",
			approverUsername, atCommit(commitSHA))
	}
	sort.SliceStable(matching, func(a, b int) bool {
		return toStringOrEmpty(matching[a]["decided_at"]) <
			toStringOrEmpty(matching[b]["decided_at"])
	})
	latest := matching[len(matching)-1]

	// Unreachable from a real response, where the normalizer above always
	// writes "approved". Kept for the shape it shares with the GitHub adapter,
	// and because a future GitLab that does report a per-user state should not
	// land in a branch that silently accepts it.
	state := strings.ToLower(pythonStr(latest["state"]))
	if state != "approved" && state != "active" {
		return nil, fmt.Errorf(
			"latest GitLab approval for approver %s is not an effective approval", approverUsername)
	}
	return latest, nil
}

// SourceIssue is the linkable part of a forge issue.
//
// The same shape for both forges, which is what lets the linking code below be
// forge-agnostic: GitHub says "open" where GitLab says "opened", and
// "html_url" where GitLab says "web_url", and both are normalised here.
type SourceIssue struct {
	Number    int
	Title     string
	State     string
	WebURL    any
	UpdatedAt any
}

// FetchGitLabIssue reads one GitLab issue's linkable fields.
func FetchGitLabIssue(projectPath string, issueIID int) (*SourceIssue, error) {
	value, mocked, err := readForgeMockValue(GitLabIssueFetchMock)
	if err != nil {
		return nil, err
	}
	if !mocked {
		path := fmt.Sprintf("projects/%s/issues/%d", percentEncode(projectPath), issueIID)
		result, err := runForgeCLIIn("glab", []string{"glab", "api", path})
		if err != nil {
			return nil, err
		}
		if !result.ok() {
			return nil, fmt.Errorf("unable to fetch GitLab issue for %s issue %d: %s",
				projectPath, issueIID, legacyDetail(result, "glab"))
		}
		if value, err = parseForgeJSON("glab", "fetch_gitlab_issue", result.stdout); err != nil {
			return nil, err
		}
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("GitLab issue API response must be a JSON object")
	}
	title, _ := raw["title"].(string)
	if title == "" {
		return nil, fmt.Errorf("GitLab issue %s#%d response is missing a title",
			projectPath, issueIID)
	}
	state, _ := raw["state"].(string)
	if state != "opened" && state != "closed" {
		return nil, fmt.Errorf("GitLab issue %s#%d response has an unrecognized state: %s",
			projectPath, issueIID, pythonRepr(raw["state"]))
	}
	return &SourceIssue{Number: issueIID, Title: title, State: state,
		WebURL: raw["web_url"], UpdatedAt: raw["updated_at"]}, nil
}

// FetchGitHubIssue reads one GitHub issue's linkable fields.
func FetchGitHubIssue(repo string, issueNumber int) (*SourceIssue, error) {
	value, mocked, err := readForgeMockValue(GitHubIssueFetchMock)
	if err != nil {
		return nil, err
	}
	if !mocked {
		// The repository is not percent-encoded here, unlike every other
		// GitHub call in this kernel. Mirroring the Python kernel, which does
		// the same -- a repo name needing encoding would not survive either
		// implementation, and diverging would make one of them work where the
		// other does not.
		path := fmt.Sprintf("repos/%s/issues/%d", repo, issueNumber)
		result, err := runForgeCLIIn("gh", []string{"gh", "api", path})
		if err != nil {
			return nil, err
		}
		if !result.ok() {
			return nil, fmt.Errorf("unable to fetch GitHub issue for %s issue %d: %s",
				repo, issueNumber, legacyDetail(result, "gh"))
		}
		if value, err = parseForgeJSON("gh", "fetch_github_issue", result.stdout); err != nil {
			return nil, err
		}
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("GitHub issue API response must be a JSON object")
	}
	title, _ := raw["title"].(string)
	if title == "" {
		return nil, fmt.Errorf("GitHub issue %s#%d response is missing a title", repo, issueNumber)
	}
	state, _ := raw["state"].(string)
	if state != "open" && state != "closed" {
		return nil, fmt.Errorf("GitHub issue %s#%d response has an unrecognized state: %s",
			repo, issueNumber, pythonRepr(raw["state"]))
	}
	return &SourceIssue{Number: issueNumber, Title: title, State: state,
		WebURL: raw["html_url"], UpdatedAt: raw["updated_at"]}, nil
}

// pythonStr renders a value the way Python's str() would.
//
// Only the shapes that reach it: a JSON number that is whole prints without a
// decimal point, which is what turns a GitLab user id into an approval id.
func pythonStr(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case string:
		return typed
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case int:
		return strconv.Itoa(typed)
	}
	return fmt.Sprint(value)
}
