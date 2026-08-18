// Package gitlabissue links GitLab issues to G1 Intent and G2 Requirements.
//
// Ported from engine/agentic_sdlc_langgraph/gitlab_issue.py.
//
// Deliberately not an approval adapter. Linking an issue records where a
// task's intent or requirements came from, not a human's sign-off on it, so
// nothing here produces an Approval -- only a URI string that seeds
// intent_record_id or requirements_baseline_id once, at plan time.
package gitlabissue

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// IssueMockEnvVar and CreateMockEnvVar are the mocking conventions the Python
// used, carried over unchanged so existing fixtures keep working and tests
// need neither the network nor a glab binary.
const (
	IssueMockEnvVar  = "AGENTIC_SDLC_TEST_GITLAB_ISSUE_FILE"
	CreateMockEnvVar = "AGENTIC_SDLC_TEST_ISSUE_CREATE_FILE"
)

const glabTimeout = 30 * time.Second

var issueURIPattern = regexp.MustCompile(`^gitlab-issue:([A-Za-z0-9_./-]+):issues/([0-9]+)$`)

// IssueReference is a parsed gitlab-issue: URI.
type IssueReference struct {
	ProjectPath string
	IID         string
}

// Issue is the minimal set of fields needed to reference an issue.
//
// No author or assignee identity is read: an issue link has no approver
// concept, so there is nothing to minimise away.
type Issue struct {
	IID       int    `json:"iid"`
	Title     string `json:"title"`
	State     string `json:"state"`
	WebURL    any    `json:"web_url"`
	UpdatedAt any    `json:"updated_at"`
}

// percentEncode reproduces Python's urllib quote(value, safe="").
//
// Neither of Go's built-ins matches. url.QueryEscape renders a space as "+",
// and url.PathEscape leaves "/" alone -- which matters most here, because a
// GitLab project path *is* "group/project" and the API needs it encoded as a
// single path segment. PathEscape would silently address a different endpoint.
func percentEncode(value string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	var encoded strings.Builder
	for _, octet := range []byte(value) {
		if strings.IndexByte(unreserved, octet) >= 0 {
			encoded.WriteByte(octet)
			continue
		}
		fmt.Fprintf(&encoded, "%%%02X", octet)
	}
	return encoded.String()
}

// runGlab invokes glab with an argv list and a private working directory.
//
// The temp cwd is deliberate: run in this repository, glab porcelain can
// discover a git remote and act against whatever project that names. A neutral
// directory means the only project involved is the one named on argv.
//
// argv only, never a shell, and no secrets on the command line.
func runGlab(argv []string, input []byte) (stdout, stderr []byte, code int, err error) {
	workingDir, err := os.MkdirTemp("", "agentic-sdlc-glab-")
	if err != nil {
		return nil, nil, 0, err
	}
	defer os.RemoveAll(workingDir)

	ctx, cancel := context.WithTimeout(context.Background(), glabTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = workingDir
	if input != nil {
		command.Stdin = strings.NewReader(string(input))
	}
	var outBuffer, errBuffer strings.Builder
	command.Stdout = &outBuffer
	command.Stderr = &errBuffer

	runErr := command.Run()
	stdout, stderr = []byte(outBuffer.String()), []byte(errBuffer.String())

	if ctx.Err() == context.DeadlineExceeded {
		return stdout, stderr, 0, fmt.Errorf("glab %s timed out after %s", argv[1], glabTimeout)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(runErr, &exitErr); ok {
			return stdout, stderr, exitErr.ExitCode(), nil
		}
		// A launch failure -- most often no glab on PATH. Reported without
		// the working directory, which is a private temp path.
		return stdout, stderr, 0, fmt.Errorf("unable to run glab: %w", runErr)
	}
	return stdout, stderr, 0, nil
}

func asExitError(err error, target **exec.ExitError) bool {
	exitErr, ok := err.(*exec.ExitError)
	if ok {
		*target = exitErr
	}
	return ok
}

func glabFailure(stderr []byte) string {
	detail := strings.TrimSpace(string(stderr))
	if detail == "" {
		return "unknown glab api failure"
	}
	return detail
}

// ParseIssueURI parses a gitlab-issue: URI, returning nil when it does not match.
func ParseIssueURI(value string) *IssueReference {
	match := issueURIPattern.FindStringSubmatch(value)
	if match == nil {
		return nil
	}
	return &IssueReference{ProjectPath: match[1], IID: match[2]}
}

// IssueURI builds a gitlab-issue: URI and parses its own output before
// returning it, mirroring the kernel's parse-your-own-output discipline.
func IssueURI(projectPath string, issueIID int) (string, error) {
	uri := fmt.Sprintf("gitlab-issue:%s:issues/%d", projectPath, issueIID)
	if ParseIssueURI(uri) == nil {
		return "", fmt.Errorf("invalid GitLab issue URI components for %s", uri)
	}
	return uri, nil
}

func loadMockObject(envVar string) (map[string]any, error) {
	path := os.Getenv(envVar)
	if path == "" {
		return nil, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(contents, &value); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return value, nil
}

// FetchIssue reads an issue, honouring the mock env var when set.
func FetchIssue(projectPath string, issueIID int) (Issue, error) {
	var issue Issue

	var raw map[string]any
	if path := os.Getenv(IssueMockEnvVar); path != "" {
		contents, err := os.ReadFile(path)
		if err != nil {
			return issue, err
		}
		if err := json.Unmarshal(contents, &raw); err != nil {
			return issue, fmt.Errorf("GitLab issue API response must be a JSON object: %w", err)
		}
	} else {
		stdout, stderr, code, err := runGlab([]string{
			"glab", "api", fmt.Sprintf("projects/%s/issues/%d", percentEncode(projectPath), issueIID),
		}, nil)
		if err != nil {
			return issue, err
		}
		if code != 0 {
			return issue, fmt.Errorf("unable to fetch GitLab issue for %s issue %d: %s",
				projectPath, issueIID, glabFailure(stderr))
		}
		if err := json.Unmarshal(stdout, &raw); err != nil {
			return issue, fmt.Errorf("GitLab issue API response must be a JSON object: %w", err)
		}
	}

	title, _ := raw["title"].(string)
	if title == "" {
		return issue, fmt.Errorf("GitLab issue %s#%d response is missing a title", projectPath, issueIID)
	}
	state, _ := raw["state"].(string)
	if state != "opened" && state != "closed" {
		return issue, fmt.Errorf("GitLab issue %s#%d response has an unrecognized state: %q",
			projectPath, issueIID, state)
	}

	return Issue{
		IID:       issueIID,
		Title:     title,
		State:     state,
		WebURL:    raw["web_url"],
		UpdatedAt: raw["updated_at"],
	}, nil
}

// ResolveIssueReference parses a <project-path>#<iid> reference, fetches the
// issue and returns its validated URI. An empty reference resolves to empty.
//
// Digits are ASCII-only here. Python's str.isdigit accepts other Unicode
// digit forms, which int() then happily parses; nothing sensible sends those
// as an issue number, and being strict avoids a reference that means one thing
// to a human and another to the API.
func ResolveIssueReference(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	separator := strings.LastIndex(value, "#")
	projectPath, iidText := "", ""
	if separator >= 0 {
		projectPath, iidText = value[:separator], value[separator+1:]
	}
	iid, err := strconv.Atoi(iidText)
	if separator < 0 || projectPath == "" || iidText == "" || err != nil || iid < 0 {
		return "", fmt.Errorf("GitLab issue reference must be in <project-path>#<iid> form, got %q", value)
	}

	issue, err := FetchIssue(projectPath, iid)
	if err != nil {
		return "", err
	}
	return IssueURI(projectPath, issue.IID)
}

// VerifyIdentity asserts the authenticated glab user matches the expected one,
// case-insensitively, and returns the confirmed username.
//
// Callers use the returned name rather than the one they asked for: it is the
// one actually confirmed against the live credential.
func VerifyIdentity(expectedUsername string) (string, error) {
	var raw map[string]any

	mock, err := loadMockObject(CreateMockEnvVar)
	if err != nil {
		return "", err
	}
	if mock != nil {
		identity, isObject := mock["identity"].(map[string]any)
		if !isObject {
			return "", fmt.Errorf("mocked %s response has no 'identity' object", CreateMockEnvVar)
		}
		raw = identity
	} else {
		stdout, stderr, code, err := runGlab([]string{"glab", "api", "user"}, nil)
		if err != nil {
			return "", err
		}
		if code != 0 {
			return "", fmt.Errorf("unable to verify GitLab identity: %s", glabFailure(stderr))
		}
		if err := json.Unmarshal(stdout, &raw); err != nil {
			return "", fmt.Errorf("GitLab user API response must be a JSON object: %w", err)
		}
	}

	username, _ := raw["username"].(string)
	if username == "" {
		return "", fmt.Errorf("GitLab user API response is missing a username")
	}
	if !strings.EqualFold(username, expectedUsername) {
		return "", fmt.Errorf(
			"authenticated GitLab identity %q does not match required bot identity %q -- "+
				"point your glab credential config at the bot's credentials",
			username, expectedUsername)
	}
	return username, nil
}

// SearchIssuesByLabels searches a project's issues by label.
//
// state=all is required: the default is open-only, which would cause a
// duplicate create against an already-reused but closed issue.
//
// Label filter only, never GitLab's free-text search parameter -- that matches
// model-controlled body text and would reopen the forgery vector the label
// anchor exists to close.
func SearchIssuesByLabels(projectPath string, labels []string) ([]any, error) {
	key := strings.Join(labels, ",")

	mock, err := loadMockObject(CreateMockEnvVar)
	if err != nil {
		return nil, err
	}
	if mock != nil {
		searches, _ := mock["search"].(map[string]any)
		entry, present := searches[key]
		if !present {
			return []any{}, nil
		}
		results, isList := entry.([]any)
		if !isList {
			return nil, fmt.Errorf("mocked search response for labels %q must be a JSON array", key)
		}
		return results, nil
	}

	stdout, stderr, code, err := runGlab([]string{
		"glab", "api", fmt.Sprintf("projects/%s/issues?labels=%s&state=all&per_page=20",
			percentEncode(projectPath), percentEncode(key)),
	}, nil)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("unable to search GitLab issues in %s for labels %v: %s",
			projectPath, labels, glabFailure(stderr))
	}
	var results []any
	if err := json.Unmarshal(stdout, &results); err != nil {
		return nil, fmt.Errorf("GitLab issue search response must be a JSON array: %w", err)
	}
	return results, nil
}
