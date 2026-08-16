package kernel

import (
	"fmt"
	"strings"
)

// GitHub reads, for `request-gate-reviewers`.
//
// Read-only, and deliberately so. Requesting PR reviewers needs a token with
// `Pull requests: write`, which has no narrower equivalent and also permits
// editing and closing pull requests and changing labels. Granting that is a
// permission-escalation decision somebody has to make explicitly, so this
// client does not have the capability -- not even stubbed out, because a stub
// is the shape of a thing waiting to be filled in.
//
// Every call goes through `gh api ... GET`. There is no POST or DELETE here.

// GitHubClient reads from GitHub through the `gh` CLI, or from a mock file.
type GitHubClient struct {
	mock map[string]any
}

// NewGitHubClient loads any configured mock and returns a reader.
func NewGitHubClient() (*GitHubClient, error) {
	mock, err := loadForgeMock(GitHubReadMockEnv)
	if err != nil {
		return nil, err
	}
	return &GitHubClient{mock: mock}, nil
}

// Mocked reports whether this client is answering from a file.
//
// Callers record it in their ledger. A publication that never touched the
// network must not be indistinguishable from one that did.
func (c *GitHubClient) Mocked() bool { return c.mock != nil }

// PullRequestNotFound is a 404 on a pull request.
//
// A distinct type rather than a string to match on: `request-gate-reviewers`
// reports "that PR does not exist" differently from "GitHub refused", and
// telling them apart from prose would be guessing.
type PullRequestNotFound struct{ Message string }

func (e *PullRequestNotFound) Error() string { return e.Message }

// VerifyIdentity asserts the authenticated login is the expected one.
//
// Case-insensitively, because GitHub logins are. Checked at all because
// everything this kernel publishes is attributed to whoever the forge CLI is
// authenticated as -- and a run that publishes as the wrong identity has
// signed a project's evidence with the wrong name.
func (c *GitHubClient) VerifyIdentity(expectedLogin string) (string, error) {
	var raw map[string]any
	if c.mock != nil {
		object, ok := c.mock["identity"].(map[string]any)
		if !ok {
			return "", fmt.Errorf(
				"mocked %s response has no 'identity' object", GitHubReadMockEnv)
		}
		raw = object
	} else {
		result, err := runForgeCLIIn("gh", []string{"gh", "api", "user"})
		if err != nil {
			return "", err
		}
		if !result.ok() {
			return "", fmt.Errorf("unable to verify GitHub identity: %s", result.detail("gh"))
		}
		raw, err = parseForgeJSONObject("gh", "verify_github_identity",
			"GitHub user API response", result.stdout)
		if err != nil {
			return "", err
		}
	}

	login, _ := raw["login"].(string)
	if login == "" {
		return "", fmt.Errorf("GitHub user API response is missing a login")
	}
	if !strings.EqualFold(login, expectedLogin) {
		return "", fmt.Errorf(
			"authenticated GitHub identity %s does not match required bot identity %s "+
				"-- point your gh credential config at the bot's credentials",
			pythonRepr(login), pythonRepr(expectedLogin))
	}
	return login, nil
}

// FetchPullRequest reads a pull request.
func (c *GitHubClient) FetchPullRequest(repo string, pullRequest int) (map[string]any, error) {
	if c.mock != nil {
		raw, present := c.mock["pr"]
		if !present || raw == nil {
			return nil, &PullRequestNotFound{Message: fmt.Sprintf(
				"mocked PR lookup for %s#%d is missing (simulated 404)", repo, pullRequest)}
		}
		object, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mocked pr response must be a JSON object")
		}
		return object, nil
	}

	result, err := runForgeCLIIn("gh", []string{"gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d", encodeRepoPath(repo), pullRequest)})
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		detail := result.detail("gh")
		if isNotFoundError(detail) {
			return nil, &PullRequestNotFound{Message: fmt.Sprintf(
				"GitHub PR %s#%d not found: %s", repo, pullRequest, detail)}
		}
		return nil, fmt.Errorf("unable to fetch GitHub PR %s#%d: %s", repo, pullRequest, detail)
	}
	return parseForgeJSONObject("gh", "fetch_github_pr", "GitHub PR response", result.stdout)
}

// FetchRequestedReviewers returns the user logins already requested.
//
// Users only. A team review request satisfies GitHub but not this kernel's
// question, which is per-person: a gate needs a named human, and "the platform
// team was asked" does not say who.
func (c *GitHubClient) FetchRequestedReviewers(repo string, pullRequest int) ([]string, error) {
	var raw map[string]any
	if c.mock != nil {
		object, present := c.mock["requested_reviewers"].(map[string]any)
		if !present {
			// Absent means nobody has been asked, which is a real state and
			// not a malformed fixture.
			return []string{}, nil
		}
		raw = object
	} else {
		result, err := runForgeCLIIn("gh", []string{"gh", "api",
			fmt.Sprintf("repos/%s/pulls/%d/requested_reviewers",
				encodeRepoPath(repo), pullRequest)})
		if err != nil {
			return nil, err
		}
		if !result.ok() {
			return nil, fmt.Errorf("unable to fetch requested reviewers for %s#%d: %s",
				repo, pullRequest, result.detail("gh"))
		}
		raw, err = parseForgeJSONObject("gh", "fetch_requested_reviewers",
			"GitHub requested_reviewers response", result.stdout)
		if err != nil {
			return nil, err
		}
	}

	users, present := raw["users"]
	if !present {
		return []string{}, nil
	}
	list, ok := users.([]any)
	if !ok {
		return nil, fmt.Errorf("GitHub requested_reviewers 'users' field must be a JSON array")
	}
	logins := []string{}
	for _, entry := range list {
		user, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if login, ok := user["login"].(string); ok && login != "" {
			logins = append(logins, login)
		}
	}
	return logins, nil
}

// UserExists reports whether a login resolves to a GitHub account.
//
// An exact lookup, so there is no ambiguous-match case -- unlike GitLab, whose
// user lookup is a search and can return several. That asymmetry is why this
// kernel has no "github-user-ambiguous" reason code anywhere.
func (c *GitHubClient) UserExists(login string) (bool, error) {
	if c.mock != nil {
		users, _ := c.mock["users"].(map[string]any)
		value, present := users[login]
		if !present {
			return false, fmt.Errorf(
				"mocked users response is missing an entry for login %s", pythonRepr(login))
		}
		return value == true, nil
	}

	result, err := runForgeCLIIn("gh", []string{"gh", "api", "users/" + percentEncode(login)})
	if err != nil {
		return false, err
	}
	if !result.ok() {
		detail := result.detail("gh")
		if isNotFoundError(detail) {
			return false, nil
		}
		return false, fmt.Errorf("unable to check GitHub user %s: %s", pythonRepr(login), detail)
	}
	return true, nil
}

// IsCollaborator reports whether a login can be asked to review in a repo.
//
// Asked separately from UserExists because they fail differently: a login that
// does not exist is a typo in the project's authority map, and one that exists
// but cannot review is an access problem somebody has to fix on the repo.
func (c *GitHubClient) IsCollaborator(repo, login string) (bool, error) {
	if c.mock != nil {
		collaborators, _ := c.mock["collaborators"].(map[string]any)
		key := repo + ":" + login
		value, present := collaborators[key]
		if !present {
			return false, fmt.Errorf(
				"mocked collaborators response is missing an entry for %s", pythonRepr(key))
		}
		return value == true, nil
	}

	result, err := runForgeCLIIn("gh", []string{"gh", "api",
		fmt.Sprintf("repos/%s/collaborators/%s", encodeRepoPath(repo), percentEncode(login))})
	if err != nil {
		return false, err
	}
	if !result.ok() {
		detail := result.detail("gh")
		if isNotFoundError(detail) {
			return false, nil
		}
		return false, fmt.Errorf("unable to check GitHub collaborator %s on %s: %s",
			pythonRepr(login), repo, detail)
	}
	return true, nil
}
