package kernel

import (
	"fmt"
	"strconv"
	"strings"
)

// GitHub PR comment reads and writes, for the gate-status render.
//
// A separate client from the read-only one, with its own mock file, because
// they have genuinely different capabilities: that one cannot write at all and
// says so, and folding them together would put a write method on the type used
// by the command that must not have one.
//
// Everything here is issue-level comments on a pull request. There is no
// reactions endpoint and no review endpoint, deliberately -- the comment this
// posts is one-way, and reading a reaction back would be the first step
// towards treating a thumbs-up as an approval.

// GitHubStatusClient posts and reads pull-request comments.
type GitHubStatusClient struct {
	mock map[string]any
}

// NewGitHubStatusClient loads any configured mock and returns a client.
func NewGitHubStatusClient() (*GitHubStatusClient, error) {
	mock, err := loadForgeMock(GitHubStatusMockEnv)
	if err != nil {
		return nil, err
	}
	return &GitHubStatusClient{mock: mock}, nil
}

// Mocked reports whether this client is answering from a file.
func (c *GitHubStatusClient) Mocked() bool { return c.mock != nil }

// VerifyIdentity asserts the authenticated login is the expected one.
func (c *GitHubStatusClient) VerifyIdentity(expectedLogin string) (string, error) {
	var raw map[string]any
	if c.mock != nil {
		object, ok := c.mock["identity"].(map[string]any)
		if !ok {
			return "", fmt.Errorf("mocked %s response has no 'identity' object", GitHubStatusMockEnv)
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

// ListComments fetches one page of a pull request's comments.
//
// One page, and it never guesses about the next. The caller owns pagination
// and the cap on how far it will look, because the honest answer when a
// pull request has more comments than that is "I cannot tell", not a guess.
func (c *GitHubStatusClient) ListComments(
	repo string, pullRequest, page, perPage int,
) ([]any, error) {
	key := fmt.Sprintf("%s#%d", repo, pullRequest)
	if c.mock != nil {
		list, _ := c.mock["list"].(map[string]any)
		pages, _ := list[key].(map[string]any)
		raw, present := pages[strconv.Itoa(page)]
		if !present {
			return []any{}, nil
		}
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("mocked list response for %s page %d must be a JSON array",
				pythonRepr(key), page)
		}
		return items, nil
	}

	result, err := runForgeCLIIn("gh", []string{"gh", "api", fmt.Sprintf(
		"repos/%s/issues/%d/comments?per_page=%d&page=%d",
		encodeRepoPath(repo), pullRequest, perPage, page)})
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		return nil, fmt.Errorf("unable to list PR comments for %s#%d page %d: %s",
			repo, pullRequest, page, result.detail("gh"))
	}
	value, err := parseForgeJSON("gh", "list_pr_comments", result.stdout)
	if err != nil {
		return nil, err
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("GitHub PR comments response must be a JSON array")
	}
	return items, nil
}

// CreateComment posts a comment and returns its id.
func (c *GitHubStatusClient) CreateComment(
	repo string, pullRequest int, body string,
) (int, error) {
	key := fmt.Sprintf("%s#%d", repo, pullRequest)
	var raw map[string]any
	if c.mock != nil {
		create, _ := c.mock["create"].(map[string]any)
		object, ok := create[key].(map[string]any)
		if !ok {
			return 0, fmt.Errorf("mocked create response for %s must be a JSON object",
				pythonRepr(key))
		}
		raw = object
	} else {
		result, err := runForgeCLIWithBody("gh", []string{"gh", "api", fmt.Sprintf(
			"repos/%s/issues/%d/comments", encodeRepoPath(repo), pullRequest),
			"--method", "POST"}, map[string]any{"body": body})
		if err != nil {
			return 0, err
		}
		if !result.ok() {
			return 0, fmt.Errorf("unable to create PR comment on %s#%d: %s",
				repo, pullRequest, result.detail("gh"))
		}
		raw, err = parseForgeJSONObject("gh", "create_pr_comment",
			"GitHub PR comment create response", result.stdout)
		if err != nil {
			return 0, err
		}
	}

	commentID, ok := jsonNumber(raw["id"])
	if !ok {
		return 0, fmt.Errorf("GitHub PR comment create response is missing an integer 'id'")
	}
	return commentID, nil
}

// UpdateComment replaces a comment's body.
func (c *GitHubStatusClient) UpdateComment(repo string, commentID int, body string) error {
	if c.mock != nil {
		update, _ := c.mock["update"].(map[string]any)
		raw, _ := update[strconv.Itoa(commentID)].(map[string]any)
		if message, failed := raw["error"]; failed {
			return fmt.Errorf("unable to update PR comment %d: %v", commentID, message)
		}
		return nil
	}

	result, err := runForgeCLIWithBody("gh", []string{"gh", "api", fmt.Sprintf(
		"repos/%s/issues/comments/%d", encodeRepoPath(repo), commentID),
		"--method", "PATCH"}, map[string]any{"body": body})
	if err != nil {
		return err
	}
	if !result.ok() {
		return fmt.Errorf("unable to update PR comment %d: %s", commentID, result.detail("gh"))
	}
	return nil
}

// FetchComment reads one comment back.
//
// Used only to verify what was posted landed as posted. A body the forge
// rewrote, or a comment attributed to somebody else, means the write did not
// do what this kernel thinks it did.
func (c *GitHubStatusClient) FetchComment(repo string, commentID int) (map[string]any, error) {
	if c.mock != nil {
		fetch, _ := c.mock["fetch"].(map[string]any)
		object, ok := fetch[strconv.Itoa(commentID)].(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"mocked fetch response for comment %d must be a JSON object", commentID)
		}
		return object, nil
	}
	result, err := runForgeCLIIn("gh", []string{"gh", "api", fmt.Sprintf(
		"repos/%s/issues/comments/%d", encodeRepoPath(repo), commentID)})
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		return nil, fmt.Errorf("unable to fetch PR comment %d for %s: %s",
			commentID, repo, result.detail("gh"))
	}
	return parseForgeJSONObject("gh", "fetch_pr_comment",
		"GitHub PR comment fetch response", result.stdout)
}
