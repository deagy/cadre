package kernel

import (
	"fmt"
	"sort"
	"strings"
)

// `create-github-gate-issues` -- the GitHub half of gate tracking.
//
// Same job as `create-gate-issues`: one tracking issue per applicable
// lifecycle gate, one approval issue per applicable authority requirement, the
// forge as the source of truth for what already exists, and a dry-run digest
// the operator hands back to `--apply`. The plan is built by the same code
// (see issueForge), because the planning rules genuinely are the same.
//
// What is different is everything downstream of the plan, and each difference
// is a response to something GitHub does that GitLab does not:
//
//   - **Issues and pull requests share a numbering space**, and the issue-list
//     endpoint returns both. A pull request carrying one of these marker
//     labels cannot happen by accident, so it blocks the run rather than being
//     filtered out.
//   - **A single unpaginated page is all this asks for.** Twenty results back
//     means there may be more, and an ambiguity that large is not something to
//     resolve automatically.
//   - **Labels are unique case-insensitively**, so every label comparison here
//     is lowercased -- unlike the GitLab command's exact-match sets.
//   - **There is no per-issue confidential flag.** GitLab's post-creation
//     verification checks one; GitHub has none, so the substitute is a
//     repository pre-flight: issues must be enabled, and a public repository
//     needs `--allow-public-repo` before anything carrying gate names, phases
//     and authority roles is published into it.
//   - **Assigning a non-collaborator is accepted and then does not happen.**
//     Not an error -- an issue with no assignee. So there is a collaborator
//     pre-check before creating (a per-authority refusal, the batch
//     continues), and a re-fetch after every write (a block, because a write
//     that reported success and did nothing is worse than one that failed).
//   - **There is no Issue Links API.** The `> parent owner/repo#N` line in an
//     approval issue's description is the whole linkage, and GitHub renders it
//     as a live cross-reference. There is deliberately no link-type flag.
//   - **Bursts trip a secondary rate limit**, so mutative calls are spaced.
//
// The markers are domain-separated from the GitLab command's by a different
// leading tag, so the same task's GitLab and GitHub artifacts can never match
// each other's labels. The task id is never published: only its hash.

const (
	githubGateLabelPrefix     = "agentic-sdlc-gh-gate-"
	githubApprovalLabelPrefix = "agentic-sdlc-gh-approval-"
	githubLedgerFile          = "gate-issues-github.json"
	githubLockFile            = "gate-issues-github.lock"
)

// githubAdvisoryTemplate heads every GitHub approval issue.
//
// Differs from the GitLab command's only in which subcommand it points at --
// somebody who closes one of these has to be told what actually records an
// approval, and telling them the wrong forge's command is worse than telling
// them nothing.
const githubAdvisoryTemplate = "%s Tracking artifact only — closing this issue is not approval " +
	"evidence and does not approve %s. The approver must not be a preparer or the independent " +
	"verifier of this gate. Record approval via `agentic-sdlc approve-from-github-pr` or " +
	"`agentic-sdlc decide`."

// GateIssuesGithubError is a structural or policy failure -- exit 1.
type GateIssuesGithubError struct{ Message string }

func (e *GateIssuesGithubError) Error() string { return e.Message }

// GateIssuesGithubBlocked needs a human before the run can continue -- exit 2.
type GateIssuesGithubBlocked struct{ Message string }

func (e *GateIssuesGithubBlocked) Error() string { return e.Message }

// githubIssueForge is `create-github-gate-issues`.
var githubIssueForge = &issueForge{
	name: "github", pathKey: "repo", resolvedKey: "resolved_logins",
	bindingReason: "no-github-binding", bindingNoun: "GitHub login",
	forgeLogin:     "github",
	tagsDigest:     true,
	gateMarker:     ComputeGithubGateMarker,
	approvalMarker: ComputeGithubApprovalMarker,
	gateLabel:      GithubGateLabel,
	approvalLabel:  GithubApprovalLabel,
	fail:           func(message string) error { return &GateIssuesGithubError{Message: message} },
}

// ComputeGithubGateMarker identifies a gate's GitHub tracking issue.
func ComputeGithubGateMarker(taskID, gateID string) string {
	return hexSHA256([]byte("github-gate\x00" + taskID + "\x00" + gateID))[:16]
}

// ComputeGithubApprovalMarker identifies one authority's GitHub approval issue.
func ComputeGithubApprovalMarker(taskID, gateID, authorityID string) string {
	return hexSHA256([]byte("github-approval\x00" + taskID + "\x00" + gateID + "\x00" + authorityID))[:16]
}

// GithubGateLabel is the label a GitHub gate issue is found by.
func GithubGateLabel(marker string) (string, error) {
	return checkedGithubLabel(githubGateLabelPrefix + marker)
}

// GithubApprovalLabel is the label a GitHub approval issue is found by.
func GithubApprovalLabel(marker string) (string, error) {
	return checkedGithubLabel(githubApprovalLabelPrefix + marker)
}

func checkedGithubLabel(label string) (string, error) {
	if !IsLabelCharset(label) {
		return "", &GateIssuesGithubError{Message: fmt.Sprintf(
			"computed label %s violates the [a-z0-9-] label charset", pythonRepr(label))}
	}
	return label, nil
}

// RenderGithubApprovalDescription is a GitHub approval issue's body.
//
// The parent line is the only cross-reference this command makes. GitHub
// renders it as a working bidirectional link with no API call, which is why
// there is no linking step to fail and no flag to enable one.
func RenderGithubApprovalDescription(
	taskID, gateID, marker, repo string, gateIssueNumber int, rationale any,
) (string, error) {
	lines := []string{
		fmt.Sprintf(githubAdvisoryTemplate, provenanceLine, gateID),
		fmt.Sprintf("%s%s/%s/%s", refLinePrefix, TaskHash(taskID), gateID, marker),
		fmt.Sprintf("%s%s#%d", parentLinePrefix, repo, gateIssueNumber),
		"",
	}
	if text, ok := rationale.(string); ok && text != "" {
		sanitized, err := SanitizeFreeText(text, gateID+" authority rationale")
		if err != nil {
			return "", &GateIssuesGithubError{Message: err.Error()}
		}
		lines = append(lines, sanitized)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// checkSearchResults decides whether a label search answered the question.
//
// Three ways it can fail to, in the order the Python kernel checks them, and
// the order matters: a page of twenty entries that includes a pull request
// should say so, because that is the more specific problem.
func checkSearchResults(matches []any, context string) error {
	for _, raw := range matches {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, isPullRequest := entry["pull_request"]; isPullRequest {
			return &GateIssuesGithubBlocked{Message: fmt.Sprintf(
				"%s: label-on-pull-request -- a matched entry carries a 'pull_request' key; this "+
					"module's marker label can never legitimately be on a PR, treating this as "+
					"tampering/collision", context)}
		}
	}
	if len(matches) == issueSearchPageSize {
		return &GateIssuesGithubBlocked{Message: fmt.Sprintf(
			"%s: result-cap-exceeded -- the search returned exactly %d (per_page) entries; this "+
				"module never paginates, needs human resolution", context, issueSearchPageSize)}
	}
	if len(matches) > 1 {
		return &GateIssuesGithubBlocked{Message: fmt.Sprintf(
			"%s: %d issues matched -- ambiguous identity, needs human resolution",
			context, len(matches))}
	}
	return nil
}

// githubIssueLabels reads an issue's labels in either shape GitHub returns.
func githubIssueLabels(entry map[string]any) []string {
	labels := []string{}
	for _, raw := range listOf(entry["labels"]) {
		switch label := raw.(type) {
		case map[string]any:
			if name, ok := label["name"].(string); ok {
				labels = append(labels, name)
			}
		case string:
			labels = append(labels, label)
		}
	}
	return labels
}

// validateMatchedGithubIssue checks an issue found by label really is ours.
//
// Same three checks as the GitLab command's, all lowercased: GitHub treats
// `Agentic-SDLC` and `agentic-sdlc` as one label, so an exact-match comparison
// would report a legitimately-matched issue as missing its anchor.
func validateMatchedGithubIssue(
	entry map[string]any, ownLabel, foreignPrefix, context string,
) error {
	present := map[string]bool{}
	for _, label := range githubIssueLabels(entry) {
		present[strings.ToLower(label)] = true
	}
	if !present[strings.ToLower(FixedLabel)] {
		return &GateIssuesGithubBlocked{Message: fmt.Sprintf(
			"%s: matched issue is missing the %s anchor label", context, pythonRepr(FixedLabel))}
	}
	if !present[strings.ToLower(ownLabel)] {
		return &GateIssuesGithubBlocked{Message: fmt.Sprintf(
			"%s: matched issue is missing its own label %s", context, pythonRepr(ownLabel))}
	}
	var foreign []string
	for label := range present {
		if strings.HasPrefix(label, strings.ToLower(foreignPrefix)) &&
			label != strings.ToLower(ownLabel) {
			foreign = append(foreign, label)
		}
	}
	if len(foreign) > 0 {
		sort.Strings(foreign)
		return &GateIssuesGithubBlocked{Message: fmt.Sprintf(
			"%s: matched issue carries a foreign label %s -- possible mismatch/poisoned issue",
			context, pythonList(foreign))}
	}
	return nil
}

// verifyCreatedGithubIssue reports what an issue does not match.
//
// Every comparison is case-insensitive on the identity fields, and the
// repository is read back from the API's own URL rather than from the one that
// was asked for -- a create that landed elsewhere is exactly what this catches.
func verifyCreatedGithubIssue(
	verification *GitHubIssueVerification, title, repo, botLogin string,
	expectedLabels, expectedAssignees []string,
) []string {
	var failures []string
	if verification.Title != title {
		failures = append(failures, "title")
	}
	if verification.State != "open" {
		failures = append(failures, "state")
	}
	if !sameLowercasedSet(verification.Labels, expectedLabels) {
		failures = append(failures, "labels")
	}
	if !sameStrings(lowercased(verification.Assignees), lowercased(expectedAssignees)) {
		failures = append(failures, "assignees")
	}
	if !strings.EqualFold(verification.RepoFromURL, repo) {
		failures = append(failures, "repo_from_url")
	}
	if !strings.EqualFold(toStringOrEmpty(verification.AuthorLogin), botLogin) {
		failures = append(failures, "author_login")
	}
	// A pull request wearing this issue's number. Reached only if GitHub
	// returned one from the create, which would mean the number is not an
	// issue's at all.
	if verification.HasPullRequestKey {
		failures = append(failures, "has_pull_request_key")
	}
	return failures
}

func sameLowercasedSet(actual, expected []string) bool {
	present := map[string]bool{}
	for _, value := range actual {
		present[strings.ToLower(value)] = true
	}
	wanted := map[string]bool{}
	for _, value := range expected {
		wanted[strings.ToLower(value)] = true
	}
	if len(present) != len(wanted) {
		return false
	}
	for value := range wanted {
		if !present[value] {
			return false
		}
	}
	return true
}
