package kernel

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// GitLab reads and writes, through the `glab` CLI.
//
// Unlike the GitHub client, this one writes: it creates issues, links them,
// and posts merge-request notes. Every write is behind an explicit operator
// opt-in at the command layer above, and every one of them goes through
// runForgeCLIWithBody so the request body reaches `glab` in a 0600 file rather
// than in argv, where the process table would expose a project's own text to
// every user on the machine.
//
// Two shapes here differ from their GitHub counterparts in ways worth naming:
//
//   - A GitLab user lookup is a *search*, and can return several matches. The
//     caller decides what "exactly one active match" means; this client
//     returns the raw list rather than picking. Picking here would resolve an
//     ambiguous username silently, and assigning an approval issue to the
//     wrong person is exactly the failure the gates exist to prevent.
//   - The Issue Links API is not available on every instance. A 403 or 404
//     there raises a distinct error so the caller can fail closed rather than
//     quietly publishing an issue with no link back to its parent.

// FixedLabel marks every issue this kernel creates as its own.
const FixedLabel = "agentic-sdlc"

// GitLabClient talks to GitLab through `glab`, or to a mock file.
type GitLabClient struct {
	mock map[string]any
}

// NewGitLabClient loads any configured mock and returns a client.
func NewGitLabClient() (*GitLabClient, error) {
	mock, err := loadForgeMock(GitLabIssueMockEnv)
	if err != nil {
		return nil, err
	}
	return &GitLabClient{mock: mock}, nil
}

// Mocked reports whether this client is answering from a file.
func (c *GitLabClient) Mocked() bool { return c.mock != nil }

// MergeRequestNotFound is a 404 on a merge request.
type MergeRequestNotFound struct{ Message string }

func (e *MergeRequestNotFound) Error() string { return e.Message }

// IssueLinksUnavailable reports an instance without the Issue Links API.
//
// Distinct so the caller fails closed. Downgrading to "skip the link" would
// publish an approval issue with no recorded relationship to the gate issue it
// belongs to, and nothing downstream would know the link was ever intended.
type IssueLinksUnavailable struct{ Message string }

func (e *IssueLinksUnavailable) Error() string { return e.Message }

// VerifyIdentity asserts the authenticated username is the expected one.
func (c *GitLabClient) VerifyIdentity(expectedUsername string) (string, error) {
	var raw map[string]any
	if c.mock != nil {
		object, ok := c.mock["identity"].(map[string]any)
		if !ok {
			return "", fmt.Errorf("mocked %s response has no 'identity' object", GitLabIssueMockEnv)
		}
		raw = object
	} else {
		result, err := runForgeCLIIn("glab", []string{"glab", "api", "user"})
		if err != nil {
			return "", err
		}
		if !result.ok() {
			return "", fmt.Errorf("unable to verify GitLab identity: %s", result.detail("glab"))
		}
		raw, err = parseForgeJSONObject("glab", "verify_gitlab_identity",
			"GitLab user API response", result.stdout)
		if err != nil {
			return "", err
		}
	}

	username, _ := raw["username"].(string)
	if username == "" {
		return "", fmt.Errorf("GitLab user API response is missing a username")
	}
	if !strings.EqualFold(username, expectedUsername) {
		return "", fmt.Errorf(
			"authenticated GitLab identity %s does not match required bot identity %s "+
				"-- point your glab credential config at the bot's credentials",
			pythonRepr(username), pythonRepr(expectedUsername))
	}
	return username, nil
}

// SearchIssuesByLabels finds the issues carrying every one of these labels.
//
// `state=all`, deliberately: a gate issue somebody closed still exists, and
// searching only open issues would create a second one beside it.
func (c *GitLabClient) SearchIssuesByLabels(projectPath string, labels []string) ([]any, error) {
	key := strings.Join(labels, ",")
	if c.mock != nil {
		search, _ := c.mock["search"].(map[string]any)
		raw, present := search[key]
		if !present {
			return []any{}, nil
		}
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("mocked search response for labels %s must be a JSON array", pythonRepr(key))
		}
		return list, nil
	}

	result, err := runForgeCLIIn("glab", []string{"glab", "api", fmt.Sprintf(
		"projects/%s/issues?labels=%s&state=all&per_page=20",
		percentEncode(projectPath), percentEncode(key))})
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		return nil, fmt.Errorf("unable to search GitLab issues in %s for labels %s: %s",
			projectPath, pythonList(labels), result.detail("glab"))
	}
	value, err := parseForgeJSON("glab", "search_gitlab_issues_by_labels", result.stdout)
	if err != nil {
		return nil, err
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("GitLab issue search response must be a JSON array")
	}
	return list, nil
}

// CreateIssue creates one issue and returns its iid.
//
// assigneeIDs is omitted from the body entirely when empty rather than sent as
// an empty list: gate issues must never carry an assignee, and an explicit
// empty list would clear one somebody set deliberately.
func (c *GitLabClient) CreateIssue(
	projectPath, title, description string, labels []string, assigneeIDs []int,
) (int, error) {
	key := strings.Join(labels, ",")
	var raw map[string]any
	if c.mock != nil {
		create, _ := c.mock["create"].(map[string]any)
		object, ok := create[key].(map[string]any)
		if !ok {
			return 0, fmt.Errorf(
				"mocked create response for labels %s must be a JSON object", pythonRepr(key))
		}
		raw = object
	} else {
		body := map[string]any{"title": title, "description": description, "labels": labels}
		if len(assigneeIDs) > 0 {
			body["assignee_ids"] = assigneeIDs
		}
		result, err := runForgeCLIWithBody("glab", []string{"glab", "api",
			fmt.Sprintf("projects/%s/issues", percentEncode(projectPath)),
			"--method", "POST"}, body)
		if err != nil {
			return 0, err
		}
		if !result.ok() {
			return 0, fmt.Errorf("unable to create GitLab issue in %s: %s",
				projectPath, result.detail("glab"))
		}
		raw, err = parseForgeJSONObject("glab", "create_gitlab_issue",
			"GitLab issue create response", result.stdout)
		if err != nil {
			return 0, err
		}
	}

	iid, ok := jsonNumber(raw["iid"])
	if !ok {
		return 0, fmt.Errorf("GitLab issue create response is missing an integer 'iid'")
	}
	return iid, nil
}

// IssueVerification is what this kernel reads back about an issue it created.
type IssueVerification struct {
	IID               int      `json:"iid"`
	Title             any      `json:"title"`
	State             any      `json:"state"`
	Labels            []any    `json:"labels"`
	AssigneeCount     int      `json:"assignee_count"`
	AssigneeUsernames []string `json:"assignee_usernames"`
	Confidential      bool     `json:"confidential"`
	ProjectPath       any      `json:"project_path"`
	AuthorUsername    any      `json:"author_username"`
	WebURL            any      `json:"web_url"`
}

// FetchIssueVerification reads back an issue to confirm what was created.
//
// Read back rather than trusted from the create response: the create tells you
// what was sent, and this tells you what the instance actually stored --
// including a label an admin's rule stripped, or a confidentiality setting a
// project template applied.
func (c *GitLabClient) FetchIssueVerification(projectPath string, iid int) (*IssueVerification, error) {
	raw, err := c.fetchRawIssue(projectPath, iid, "fetch_gitlab_issue_verification")
	if err != nil {
		return nil, err
	}
	return extractIssueVerification(raw, iid), nil
}

// FetchIssueAssignmentVerification is the same read, named for the one caller
// permitted to look at who an issue is assigned to.
//
// A sibling rather than a widening. Reading assignee identity is a deliberate,
// narrowly-scoped exception to this kernel's data minimisation, allowed for
// approval subtasks only -- gate issues use FetchIssueVerification and must
// never branch on AssigneeUsernames. The two functions exist so a call site
// says which of those it is.
func (c *GitLabClient) FetchIssueAssignmentVerification(
	projectPath string, iid int,
) (*IssueVerification, error) {
	raw, err := c.fetchRawIssue(projectPath, iid, "fetch_gitlab_issue_assignment_verification")
	if err != nil {
		return nil, err
	}
	return extractIssueVerification(raw, iid), nil
}

func (c *GitLabClient) fetchRawIssue(
	projectPath string, iid int, context string,
) (map[string]any, error) {
	if c.mock != nil {
		verify, _ := c.mock["verify"].(map[string]any)
		object, ok := verify[strconv.Itoa(iid)].(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"mocked verification response for iid %d must be a JSON object", iid)
		}
		return object, nil
	}
	result, err := runForgeCLIIn("glab", []string{"glab", "api",
		fmt.Sprintf("projects/%s/issues/%d", percentEncode(projectPath), iid)})
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		return nil, fmt.Errorf("unable to fetch %s for %s issue %d: %s",
			context, projectPath, iid, result.detail("glab"))
	}
	return parseForgeJSONObject("glab", context, context+" response", result.stdout)
}

// extractIssueVerification narrows a raw issue to the fields this kernel checks.
func extractIssueVerification(raw map[string]any, iid int) *IssueVerification {
	labels := listOf(raw["labels"])
	if labels == nil {
		labels = []any{}
	}
	assignees := listOf(raw["assignees"])

	usernames := map[string]bool{}
	for _, entry := range assignees {
		assignee, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if username, ok := assignee["username"].(string); ok {
			usernames[username] = true
		}
	}
	sortedUsernames := make([]string, 0, len(usernames))
	for username := range usernames {
		sortedUsernames = append(sortedUsernames, username)
	}
	sort.Strings(sortedUsernames)

	var authorUsername any
	if author, ok := raw["author"].(map[string]any); ok {
		authorUsername = author["username"]
	}

	// GitLab reports the project on some endpoints and not others; where it
	// does not, the full reference ("group/project#12") carries it.
	projectPath := raw["project_path"]
	if projectPath == nil {
		if references, ok := raw["references"].(map[string]any); ok {
			if full, ok := references["full"].(string); ok && strings.Contains(full, "#") {
				projectPath = full[:strings.LastIndex(full, "#")]
			}
		}
	}

	return &IssueVerification{
		IID:               iid,
		Title:             raw["title"],
		State:             raw["state"],
		Labels:            labels,
		AssigneeCount:     len(assignees),
		AssigneeUsernames: sortedUsernames,
		Confidential:      raw["confidential"] == true,
		ProjectPath:       projectPath,
		AuthorUsername:    authorUsername,
		WebURL:            raw["web_url"],
	}
}

// ResolveUsername returns every account matching a username.
//
// Every match, not the best one. GitLab's user lookup is a search, and
// deciding which of several is "the" user is a judgement that belongs to the
// caller with the project's authority map in hand -- resolving it here would
// assign an approval issue to somebody nobody named.
func (c *GitLabClient) ResolveUsername(username string) ([]any, error) {
	if c.mock != nil {
		users, _ := c.mock["users"].(map[string]any)
		raw, present := users[username]
		if !present {
			return []any{}, nil
		}
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf(
				"mocked users response for username %s must be a JSON array", pythonRepr(username))
		}
		return list, nil
	}

	result, err := runForgeCLIIn("glab", []string{"glab", "api",
		"users?username=" + percentEncode(username)})
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		return nil, fmt.Errorf("unable to resolve GitLab username %s: %s",
			pythonRepr(username), result.detail("glab"))
	}
	value, err := parseForgeJSON("glab", "resolve_gitlab_user_id", result.stdout)
	if err != nil {
		return nil, err
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("GitLab users response must be a JSON array")
	}
	return list, nil
}

// UpdateIssueAssignee overwrites an issue's assignees.
//
// Only ever reached behind --reconcile-assignees, which is an operator saying
// explicitly that this kernel's view should replace GitLab's. Without that
// flag an assignee somebody set by hand is left alone.
func (c *GitLabClient) UpdateIssueAssignee(projectPath string, iid int, assigneeIDs []int) error {
	if c.mock != nil {
		update, _ := c.mock["assignee_update"].(map[string]any)
		raw, _ := update[strconv.Itoa(iid)].(map[string]any)
		if message, failed := raw["error"]; failed {
			return fmt.Errorf("unable to update assignee for issue %d: %v", iid, message)
		}
		return nil
	}

	result, err := runForgeCLIWithBody("glab", []string{"glab", "api",
		fmt.Sprintf("projects/%s/issues/%d", percentEncode(projectPath), iid),
		"--method", "PUT"}, map[string]any{"assignee_ids": assigneeIDs})
	if err != nil {
		return err
	}
	if !result.ok() {
		return fmt.Errorf("unable to update assignee for issue %s#%d: %s",
			projectPath, iid, result.detail("glab"))
	}
	return nil
}

// ListMergeRequestNotes fetches one page of a merge request's notes.
//
// One page, not all of them: the caller owns pagination and its own cap on how
// many pages it will read, because an unbounded walk of a busy merge request
// is a way to spend a timeout doing nothing useful.
func (c *GitLabClient) ListMergeRequestNotes(
	projectPath string, mergeRequestIID, page, perPage int,
) ([]any, error) {
	key := fmt.Sprintf("%s:%d", projectPath, mergeRequestIID)
	if c.mock != nil {
		notes, _ := c.mock["notes_list"].(map[string]any)
		pages, _ := notes[key].(map[string]any)
		raw, present := pages[strconv.Itoa(page)]
		if !present {
			return []any{}, nil
		}
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf(
				"mocked notes_list response for %s page %d must be a JSON array", pythonRepr(key), page)
		}
		return list, nil
	}

	result, err := runForgeCLIIn("glab", []string{"glab", "api", fmt.Sprintf(
		"projects/%s/merge_requests/%d/notes?per_page=%d&page=%d",
		percentEncode(projectPath), mergeRequestIID, perPage, page)})
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		return nil, fmt.Errorf("unable to list MR notes for %s MR %d page %d: %s",
			projectPath, mergeRequestIID, page, result.detail("glab"))
	}
	value, err := parseForgeJSON("glab", "list_mr_notes", result.stdout)
	if err != nil {
		return nil, err
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("GitLab MR notes response must be a JSON array")
	}
	return list, nil
}

// CreateMergeRequestNote posts a note and returns its id.
func (c *GitLabClient) CreateMergeRequestNote(
	projectPath string, mergeRequestIID int, body string,
) (int, error) {
	key := fmt.Sprintf("%s:%d", projectPath, mergeRequestIID)
	var raw map[string]any
	if c.mock != nil {
		notes, _ := c.mock["notes_create"].(map[string]any)
		object, ok := notes[key].(map[string]any)
		if !ok {
			return 0, fmt.Errorf(
				"mocked notes_create response for %s must be a JSON object", pythonRepr(key))
		}
		raw = object
	} else {
		result, err := runForgeCLIWithBody("glab", []string{"glab", "api", fmt.Sprintf(
			"projects/%s/merge_requests/%d/notes", percentEncode(projectPath), mergeRequestIID),
			"--method", "POST"}, map[string]any{"body": body})
		if err != nil {
			return 0, err
		}
		if !result.ok() {
			return 0, fmt.Errorf("unable to create MR note on %s MR %d: %s",
				projectPath, mergeRequestIID, result.detail("glab"))
		}
		raw, err = parseForgeJSONObject("glab", "create_mr_note",
			"GitLab MR note create response", result.stdout)
		if err != nil {
			return 0, err
		}
	}

	noteID, ok := jsonNumber(raw["id"])
	if !ok {
		return 0, fmt.Errorf("GitLab MR note create response is missing an integer 'id'")
	}
	return noteID, nil
}

// UpdateMergeRequestNote replaces a note's body.
func (c *GitLabClient) UpdateMergeRequestNote(
	projectPath string, mergeRequestIID, noteID int, body string,
) error {
	if c.mock != nil {
		notes, _ := c.mock["notes_update"].(map[string]any)
		raw, _ := notes[strconv.Itoa(noteID)].(map[string]any)
		if message, failed := raw["error"]; failed {
			return fmt.Errorf("unable to update MR note %d: %v", noteID, message)
		}
		return nil
	}

	result, err := runForgeCLIWithBody("glab", []string{"glab", "api", fmt.Sprintf(
		"projects/%s/merge_requests/%d/notes/%d",
		percentEncode(projectPath), mergeRequestIID, noteID),
		"--method", "PUT"}, map[string]any{"body": body})
	if err != nil {
		return err
	}
	if !result.ok() {
		return fmt.Errorf("unable to update MR note %d for %s MR %d: %s",
			noteID, projectPath, mergeRequestIID, result.detail("glab"))
	}
	return nil
}

// FetchMergeRequestNote reads one note back.
//
// Used only to verify what was posted actually landed as posted. A note whose
// body an instance rewrote -- a mention expanded, a reference linked -- is not
// the note this kernel rendered, and the caller needs to see that.
func (c *GitLabClient) FetchMergeRequestNote(
	projectPath string, mergeRequestIID, noteID int,
) (map[string]any, error) {
	if c.mock != nil {
		notes, _ := c.mock["notes_fetch"].(map[string]any)
		object, ok := notes[strconv.Itoa(noteID)].(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"mocked notes_fetch response for note %d must be a JSON object", noteID)
		}
		return object, nil
	}
	result, err := runForgeCLIIn("glab", []string{"glab", "api", fmt.Sprintf(
		"projects/%s/merge_requests/%d/notes/%d",
		percentEncode(projectPath), mergeRequestIID, noteID)})
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		return nil, fmt.Errorf("unable to fetch MR note %d for %s MR %d: %s",
			noteID, projectPath, mergeRequestIID, result.detail("glab"))
	}
	return parseForgeJSONObject("glab", "fetch_mr_note",
		"GitLab MR note fetch response", result.stdout)
}

// CreateIssueLink relates one issue to another.
//
// A 403 or 404 here means the instance does not offer the Issue Links API at
// all, which is reported as its own error type so the caller fails closed. The
// always-on floor is a markdown parent line in the description; this call is
// the extra, and skipping it silently would make the two look the same.
func (c *GitLabClient) CreateIssueLink(
	projectPath string, sourceIID int, targetProjectPath string, targetIID int, linkType string,
) (map[string]any, error) {
	if linkType == "" {
		linkType = "relates_to"
	}
	if c.mock != nil {
		links, _ := c.mock["link"].(map[string]any)
		raw, ok := links[strconv.Itoa(sourceIID)].(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"mocked link response for iid %d must be a JSON object", sourceIID)
		}
		if status, unavailable := raw["error_status"]; unavailable {
			detail := "no detail"
			if message, present := raw["error"]; present {
				detail = fmt.Sprint(message)
			}
			return nil, &IssueLinksUnavailable{Message: fmt.Sprintf(
				"GitLab Issue Links API unavailable for %s#%d (HTTP %v): %s",
				projectPath, sourceIID, status, detail)}
		}
		return raw, nil
	}

	body := map[string]any{
		"target_project_id": percentEncode(targetProjectPath),
		"target_issue_iid":  targetIID,
		"link_type":         linkType,
	}
	result, err := runForgeCLIWithBody("glab", []string{"glab", "api", fmt.Sprintf(
		"projects/%s/issues/%d/links", percentEncode(projectPath), sourceIID),
		"--method", "POST"}, body)
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		detail := result.detail("glab")
		if isLinkUnavailableError(detail) {
			return nil, &IssueLinksUnavailable{Message: fmt.Sprintf(
				"GitLab Issue Links API unavailable for %s#%d: %s",
				projectPath, sourceIID, detail)}
		}
		return nil, fmt.Errorf("unable to create issue link for %s#%d: %s",
			projectPath, sourceIID, detail)
	}
	value, err := parseForgeJSON("glab", "create_gitlab_issue_link", result.stdout)
	if err != nil {
		return nil, err
	}
	object, _ := value.(map[string]any)
	return object, nil
}

// isLinkUnavailableError reports whether stderr describes an instance without
// the Issue Links API. Substring matching for the same reason as
// isNotFoundError: `glab` reports status as prose.
func isLinkUnavailableError(stderr string) bool {
	return strings.Contains(stderr, "403") || strings.Contains(stderr, "404")
}

// FetchMergeRequest reads a merge request.
func (c *GitLabClient) FetchMergeRequest(
	projectPath string, mergeRequestIID int,
) (map[string]any, error) {
	if c.mock != nil {
		raw, present := c.mock["mr"]
		if !present || raw == nil {
			return nil, &MergeRequestNotFound{Message: fmt.Sprintf(
				"mocked MR lookup for %s!%d is missing (simulated 404)",
				projectPath, mergeRequestIID)}
		}
		object, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mocked mr response must be a JSON object")
		}
		return object, nil
	}

	result, err := runForgeCLIIn("glab", []string{"glab", "api", fmt.Sprintf(
		"projects/%s/merge_requests/%d", percentEncode(projectPath), mergeRequestIID)})
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		detail := result.detail("glab")
		if isNotFoundError(detail) {
			return nil, &MergeRequestNotFound{Message: fmt.Sprintf(
				"GitLab MR %s!%d not found: %s", projectPath, mergeRequestIID, detail)}
		}
		return nil, fmt.Errorf("unable to fetch GitLab MR %s!%d: %s",
			projectPath, mergeRequestIID, detail)
	}
	return parseForgeJSONObject("glab", "fetch_gitlab_mr",
		"GitLab MR response", result.stdout)
}
