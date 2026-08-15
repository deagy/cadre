package kernel

import (
	"regexp"
	"strings"
)

// Approval-evidence URIs.
//
// A human approval is only worth what its evidence is. These URIs are how a
// record points at the thing that actually happened -- a GitHub review, a
// GitLab merge-request approval -- and they are parsed rather than matched
// loosely because the parts matter: the login inside the URI is checked
// against the approver's identity and against the assigned authority. A URI
// the kernel cannot decompose is a URI whose reviewer nobody can check.

var (
	githubReviewURI = regexp.MustCompile(
		`^github-review:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+):` +
			`pull/([0-9]+):review/([0-9]+):reviewer/([A-Za-z0-9-]+)$`)
	gitlabMRURI = regexp.MustCompile(
		`^gitlab-mr:([A-Za-z0-9_./-]+):merge_requests/(\d+):` +
			`approval/([^:]+):approver/([A-Za-z0-9_.-]+)$`)
)

// GitHubReviewLogin returns the reviewer login in a github-review URI, or ""
// if the URI is not one.
func GitHubReviewLogin(value string) (string, bool) {
	match := githubReviewURI.FindStringSubmatch(value)
	if match == nil {
		return "", false
	}
	return match[5], true
}

// GitLabMRUsername returns the approver username in a gitlab-mr URI.
func GitLabMRUsername(value string) (string, bool) {
	match := gitlabMRURI.FindStringSubmatch(value)
	if match == nil {
		return "", false
	}
	return match[4], true
}

// ForgeLoginFromIdentity reads a forge login out of an identity string of the
// form "github.com/octocat".
//
// An identity that is not forge-shaped yields "" rather than an error: plenty
// of legitimate identities are email addresses or employee IDs, and the
// callers treat "" as "nothing to cross-check" rather than as a failure.
func ForgeLoginFromIdentity(value any, forge string) string {
	prefix := "github.com/"
	if forge == "gitlab" {
		prefix = "gitlab.com/"
	}
	text, ok := value.(string)
	if !ok || !strings.HasPrefix(text, prefix) {
		return ""
	}
	return strings.Trim(strings.TrimPrefix(text, prefix), "/")
}
