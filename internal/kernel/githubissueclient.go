package kernel

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// GitHub issue writes, through the `gh` CLI.
//
// Separate from GitHubClient, whose mock file and whose whole surface are
// reads: pull requests, requested reviewers, identity, collaborator checks.
// This one creates and modifies issues, and it answers from its own mock file
// (`AGENTIC_SDLC_TEST_GITHUB_ISSUE_FILE`) so a test can arrange a read world
// and a write world independently -- which is the only way to reach the
// TOCTOU case where a collaborator check passes and the write still drops the
// assignee.
//
// Three things here have no GitLab counterpart, and each is a response to
// something GitHub does that GitLab does not:
//
//   - **Issues and pull requests share a namespace.** The issue-list endpoint
//     returns pull requests too. A PR carrying one of this kernel's marker
//     labels cannot happen legitimately, so the caller treats it as tampering
//     rather than filtering it out -- see checkSearchResults.
//   - **Labels are unique case-insensitively.** Creating `Agentic-SDLC` where
//     `agentic-sdlc` exists is merged server-side, so every label comparison
//     in the GitHub path is lowercased. This client returns labels and logins
//     exactly as GitHub gave them, so that decision stays visible at the call
//     site rather than hidden in a getter.
//   - **Assigning a non-collaborator is silently accepted and never takes.**
//     Not an error, just an issue with no assignee. Every write is followed by
//     a re-fetch, and the caller compares.
//
// One assumption is documented rather than verified, carried over from the
// Python kernel's own docstring: `state=all` is assumed to be accepted
// alongside `labels=` on the issue-list endpoint. There were no scratch-repo
// credentials when either implementation was written. If a live instance
// rejects it, the fallback is two calls -- `state=open` and `state=closed` --
// unioned by the caller, and it is deliberately not implemented ahead of being
// able to exercise it.

// issueSearchPageSize is how many entries one search asks for.
//
// This client never paginates. A search that comes back exactly full is
// treated as an ambiguity too large to resolve automatically, rather than as
// the first page of an answer.
const issueSearchPageSize = 20

// githubWriteDelay separates consecutive mutative calls.
//
// GitHub's secondary rate limit is triggered by bursts rather than by a
// sustained rate, and it is not a thing to back off from and retry: a run that
// hit it has already made some of the issues it planned. Spacing the calls out
// is the cheap way to not get there.
const githubWriteDelay = time.Second

// labelColor is the neutral grey every label this kernel creates gets.
const labelColor = "ededed"

// GitHubIssueClient creates and reconciles GitHub issues.
type GitHubIssueClient struct {
	mock map[string]any
}

// NewGitHubIssueClient loads any configured mock and returns a client.
func NewGitHubIssueClient() (*GitHubIssueClient, error) {
	mock, err := loadForgeMock(GitHubIssueMockEnv)
	if err != nil {
		return nil, err
	}
	return &GitHubIssueClient{mock: mock}, nil
}

// Mocked reports whether this client is answering from a file.
func (c *GitHubIssueClient) Mocked() bool { return c.mock != nil }

// SecondaryRateLimit reports a burst GitHub refused.
//
// Distinct from an ordinary failure because the correct response is different:
// never retried, never backed off from, never partially continued. A run that
// sees this stops where it is, and the ledger it already wrote says which
// issues exist.
type SecondaryRateLimit struct{ Message string }

func (e *SecondaryRateLimit) Error() string { return e.Message }

// isSecondaryRateLimit matches GitHub's documented message text.
//
// Substring rather than status code, because `gh` surfaces neither the status
// nor a structured error -- the same limitation isNotFoundError documents. The
// match is case-insensitive and unanchored so it survives whatever `gh` wraps
// the message in; what it cannot survive is GitHub rewording the message
// itself, which is the assumption being taken on here.
func isSecondaryRateLimit(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "secondary rate limit")
}

// isLabelAlreadyExists matches the 422 a repeated label creation returns.
func isLabelAlreadyExists(stderr string) bool {
	return strings.Contains(stderr, "422") &&
		strings.Contains(strings.ToLower(stderr), "already_exists")
}

// DelayBetweenMutations sleeps between two mutative calls.
//
// Called by the orchestration above rather than by each write, so the count
// and the ordering stay somewhere a reader can see them -- a sleep buried in
// every writer is a sleep nobody can tell is happening twice.
var DelayBetweenMutations = func() { time.Sleep(githubWriteDelay) }

// FetchRepo reads a repository's own record.
//
// The pre-flight this command runs before any write. GitHub has no per-issue
// confidential flag, so whether the repository is private is the only control
// over who can read a gate issue's contents, and it has to be checked before
// the first one is created rather than after.
func (c *GitHubIssueClient) FetchRepo(repo string) (map[string]any, error) {
	if c.mock != nil {
		object, ok := c.mock["repo"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mocked repo response must be a JSON object")
		}
		return object, nil
	}
	result, err := runForgeCLIIn("gh", []string{"gh", "api", "repos/" + encodeRepoPath(repo)})
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		return nil, fmt.Errorf("unable to fetch repository %s: %s", repo, result.detail("gh"))
	}
	return parseForgeJSONObject("gh", "fetch_github_repo",
		"GitHub repository response", result.stdout)
}

// EnsureLabel creates a label, treating "it already exists" as success.
//
// Runs before every creation. Whether GitHub auto-creates a missing label on
// issue-create was never verified against a live instance, and this is the
// cheap way to stop depending on the answer: if it does, this is one wasted
// call; if it does not, an issue created without it would carry no marker
// label and the next run would create a duplicate.
func (c *GitHubIssueClient) EnsureLabel(repo, label string) error {
	if c.mock != nil {
		labels, _ := c.mock["labels"].(map[string]any)
		raw, _ := labels[label].(map[string]any)
		if raw == nil {
			return nil
		}
		status, present := raw["error_status"]
		if !present {
			return nil
		}
		code, _ := jsonNumber(status)
		detail := "no detail"
		if text, ok := raw["error"].(string); ok {
			detail = text
		}
		if code == 422 && strings.Contains(strings.ToLower(detail), "already_exists") {
			return nil
		}
		return fmt.Errorf("unable to ensure label %s on %s: HTTP %d: %s",
			pythonRepr(label), repo, code, detail)
	}

	result, err := runForgeCLIWithBody("gh",
		[]string{"gh", "api", "repos/" + encodeRepoPath(repo) + "/labels", "--method", "POST"},
		map[string]any{"name": label, "color": labelColor})
	if err != nil {
		return err
	}
	if result.ok() {
		return nil
	}
	detail := result.detail("gh")
	if isLabelAlreadyExists(detail) {
		return nil
	}
	message := fmt.Sprintf("unable to ensure label %s on %s: %s", pythonRepr(label), repo, detail)
	if isSecondaryRateLimit(detail) {
		return &SecondaryRateLimit{Message: message}
	}
	return fmt.Errorf("%s", message)
}

// SearchIssuesByLabel finds every issue carrying one label.
//
// The marker label alone, never a pair. GitHub's `labels=` parameter ANDs the
// labels it is given, so a pair would work -- but the anchor label is checked
// on the match instead, which means a match missing the anchor is reported as
// a poisoned issue rather than silently not found.
//
// GitHub's issue-search endpoint is never called, here or anywhere in this
// kernel. A full-text index is stale by an unknown amount and matches on
// prose; a label filter is exact and answers from the issue's own record.
func (c *GitHubIssueClient) SearchIssuesByLabel(repo, label string) ([]any, error) {
	if c.mock != nil {
		search, _ := c.mock["search"].(map[string]any)
		raw, present := search[label]
		if !present {
			return []any{}, nil
		}
		matches, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("mocked search response for label %s must be a JSON array",
				pythonRepr(label))
		}
		return matches, nil
	}

	path := fmt.Sprintf("repos/%s/issues?labels=%s&state=all&per_page=%d",
		encodeRepoPath(repo), percentEncode(label), issueSearchPageSize)
	result, err := runForgeCLIIn("gh", []string{"gh", "api", path})
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		return nil, fmt.Errorf("unable to search GitHub issues in %s for label %s: %s",
			repo, pythonRepr(label), result.detail("gh"))
	}
	value, err := parseForgeJSON("gh", "search_issues_by_label", result.stdout)
	if err != nil {
		return nil, err
	}
	matches, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("GitHub issue search response must be a JSON array")
	}
	return matches, nil
}

// CreateIssue opens one issue and returns its number.
func (c *GitHubIssueClient) CreateIssue(
	repo, title, body string, labels, assignees []string,
) (int, error) {
	var raw map[string]any
	if c.mock != nil {
		responses, _ := c.mock["create"].(map[string]any)
		object, ok := responses[strings.Join(labels, ",")].(map[string]any)
		if !ok {
			return 0, fmt.Errorf("mocked create response for labels %s must be a JSON object",
				pythonRepr(strings.Join(labels, ",")))
		}
		raw = object
	} else {
		payload := map[string]any{"title": title, "body": body, "labels": labels}
		if len(assignees) > 0 {
			payload["assignees"] = assignees
		}
		result, err := runForgeCLIWithBody("gh",
			[]string{"gh", "api", "repos/" + encodeRepoPath(repo) + "/issues", "--method", "POST"},
			payload)
		if err != nil {
			return 0, err
		}
		if !result.ok() {
			detail := result.detail("gh")
			message := fmt.Sprintf("unable to create GitHub issue in %s: %s", repo, detail)
			if isSecondaryRateLimit(detail) {
				return 0, &SecondaryRateLimit{
					Message: fmt.Sprintf("unable to create GitHub issue in %s: secondary rate limit hit: %s",
						repo, detail)}
			}
			return 0, fmt.Errorf("%s", message)
		}
		raw, err = parseForgeJSONObject("gh", "create_issue",
			"GitHub issue create response", result.stdout)
		if err != nil {
			return 0, err
		}
	}

	number, ok := jsonInteger(raw["number"])
	if !ok {
		return 0, fmt.Errorf("GitHub issue create response is missing an integer 'number'")
	}
	return number, nil
}

// jsonInteger reads a JSON number that must be a whole number.
//
// Go's decoder makes every JSON number a float64, so the literal `12.0` is
// indistinguishable from `12` by the time it gets here -- Python's decoder
// keeps them apart and rejects the former. The divergence is reachable only
// from a hand-written mock; GitHub sends issue numbers as integers. Rejecting
// a genuinely fractional number is the part that matters, and that is exact.
func jsonInteger(value any) (int, bool) {
	number, ok := jsonNumber(value)
	if !ok {
		return 0, false
	}
	if fractional, isFloat := value.(float64); isFloat && fractional != float64(number) {
		return 0, false
	}
	return number, true
}

// GitHubIssueVerification is what an issue actually says after a write.
//
// Every field here exists because something is compared against it. Labels and
// logins are returned exactly as GitHub gave them -- the lowercasing happens
// where the comparison does.
type GitHubIssueVerification struct {
	Number            int
	Title             any
	State             any
	Labels            []string
	Assignees         []string
	AuthorLogin       any
	RepoFromURL       string
	HasPullRequestKey bool
	HTMLURL           any
}

// FetchIssueVerification re-reads an issue after touching it.
func (c *GitHubIssueClient) FetchIssueVerification(
	repo string, number int,
) (*GitHubIssueVerification, error) {
	var raw map[string]any
	if c.mock != nil {
		verify, _ := c.mock["verify"].(map[string]any)
		object, ok := verify[strconv.Itoa(number)].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mocked verification response for issue %d must be a JSON object", number)
		}
		raw = object
	} else {
		path := fmt.Sprintf("repos/%s/issues/%d", encodeRepoPath(repo), number)
		result, err := runForgeCLIIn("gh", []string{"gh", "api", path})
		if err != nil {
			return nil, err
		}
		if !result.ok() {
			return nil, fmt.Errorf("unable to fetch GitHub issue %s#%d: %s",
				repo, number, result.detail("gh"))
		}
		raw, err = parseForgeJSONObject("gh", "fetch_issue_verification",
			"GitHub issue fetch response", result.stdout)
		if err != nil {
			return nil, err
		}
	}
	return extractGitHubVerification(raw, number), nil
}

func extractGitHubVerification(raw map[string]any, number int) *GitHubIssueVerification {
	verification := &GitHubIssueVerification{
		Number: number,
		Title:  raw["title"],
		State:  raw["state"],
		// Never nil: a nil slice and an empty one compare the same here, but
		// they render differently if one is ever reported.
		Labels:    []string{},
		Assignees: []string{},
		HTMLURL:   raw["html_url"],
	}

	// GitHub returns labels as objects; some fixtures and some endpoints
	// return plain strings. Both shapes are read rather than one being
	// declared correct.
	for _, item := range listOf(raw["labels"]) {
		switch label := item.(type) {
		case map[string]any:
			if name, ok := label["name"].(string); ok {
				verification.Labels = append(verification.Labels, name)
			}
		case string:
			verification.Labels = append(verification.Labels, label)
		}
	}
	for _, item := range listOf(raw["assignees"]) {
		assignee, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if login, ok := assignee["login"].(string); ok {
			verification.Assignees = append(verification.Assignees, login)
		}
	}
	if user, ok := raw["user"].(map[string]any); ok {
		verification.AuthorLogin = user["login"]
	}
	// The repository the issue is actually in, read back from the API's own
	// URL rather than from the one that was asked for -- a create that landed
	// somewhere else is exactly what this is looking for.
	if url, ok := raw["repository_url"].(string); ok {
		parts := strings.Split(strings.TrimRight(url, "/"), "/")
		if len(parts) >= 2 {
			verification.RepoFromURL = parts[len(parts)-2] + "/" + parts[len(parts)-1]
		}
	}
	_, verification.HasPullRequestKey = raw["pull_request"]
	return verification
}

// UpdateIssueAssignees overwrites an issue's assignees.
//
// Only ever reached behind `--reconcile-assignees`. GitHub accepts this call
// for a non-collaborator and then does not apply it, so the caller re-fetches
// and compares rather than trusting the 200.
func (c *GitHubIssueClient) UpdateIssueAssignees(repo string, number int, assignees []string) error {
	if c.mock != nil {
		updates, _ := c.mock["assignee_update"].(map[string]any)
		raw, _ := updates[strconv.Itoa(number)].(map[string]any)
		if raw == nil {
			return nil
		}
		if detail, present := raw["error"]; present {
			return fmt.Errorf("unable to update assignees for issue %d: %s", number, toStringOrEmpty(detail))
		}
		return nil
	}

	path := fmt.Sprintf("repos/%s/issues/%d", encodeRepoPath(repo), number)
	result, err := runForgeCLIWithBody("gh",
		[]string{"gh", "api", path, "--method", "PATCH"},
		map[string]any{"assignees": assignees})
	if err != nil {
		return err
	}
	if result.ok() {
		return nil
	}
	detail := result.detail("gh")
	if isSecondaryRateLimit(detail) {
		return &SecondaryRateLimit{Message: fmt.Sprintf(
			"unable to update assignees for issue %s#%d: secondary rate limit hit: %s",
			repo, number, detail)}
	}
	return fmt.Errorf("unable to update assignees for issue %s#%d: %s", repo, number, detail)
}
