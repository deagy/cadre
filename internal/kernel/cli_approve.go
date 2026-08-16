package kernel

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The command layer for the forge approval adapters and the source-link
// commands.
//
// Eight subcommands, four of them pairs: a manual form where the operator
// supplies the review or approval identifiers themselves, and an automatic
// form that reads them off the forge first. The automatic form adds two keys
// to the report saying which review or approval it picked, because an operator
// who did not name one needs to know what was recorded on their behalf.

// approveFromGithubCmd answers `approve-from-github`.
func approveFromGithubCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := GithubApprovalRequest{Root: "."}
	var pullRequest, reviewID string
	fields := map[string]*string{
		"--root": &request.Root, "--task-id": &request.TaskID,
		"--gate": &request.GateID, "--role": &request.AuthorityRole,
		"--repo": &request.Repo, "--pr": &pullRequest, "--review-id": &reviewID,
		"--reviewer-login": &request.ReviewerLogin, "--commit-sha": &request.CommitSHA,
		"--decided-at": &request.DecidedAt,
	}
	if code := parseFlags("approve-from-github", args, fields, stderr); code != 0 {
		return code
	}
	if code := requireFlags(stderr, "approve-from-github", [][2]string{
		{"--task-id", request.TaskID}, {"--gate", request.GateID},
		{"--role", request.AuthorityRole}, {"--repo", request.Repo},
		{"--pr", pullRequest}, {"--review-id", reviewID},
		{"--reviewer-login", request.ReviewerLogin}, {"--commit-sha", request.CommitSHA},
	}); code != 0 {
		return code
	}
	if code := checkGateAndRole(stderr, "approve-from-github",
		request.GateID, request.AuthorityRole); code != 0 {
		return code
	}
	number, code := requireInt(stderr, "approve-from-github", "--pr", pullRequest)
	if code != 0 {
		return code
	}
	request.PullRequest = number
	review, code := requireInt(stderr, "approve-from-github", "--review-id", reviewID)
	if code != 0 {
		return code
	}
	request.ReviewID = review

	result, err := registry.RecordGithubApproval(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	return 0
}

// approveFromGithubPRCmd answers `approve-from-github-pr`.
//
// Reads the pull request's reviews and picks this reviewer's latest one. The
// reviewer defaults to the authority's own GitHub binding, so the ordinary
// invocation names nobody -- which is the point: an operator who has to type a
// login is an operator who can type the wrong one.
func approveFromGithubPRCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	root, taskID, gateID, role := ".", "", "", ""
	repo, pullRequest, reviewerLogin, commitSHA := "", "", "", ""
	fields := map[string]*string{
		"--root": &root, "--task-id": &taskID, "--gate": &gateID, "--role": &role,
		"--repo": &repo, "--pr": &pullRequest,
		"--reviewer-login": &reviewerLogin, "--commit-sha": &commitSHA,
	}
	if code := parseFlags("approve-from-github-pr", args, fields, stderr); code != 0 {
		return code
	}
	if code := requireFlags(stderr, "approve-from-github-pr", [][2]string{
		{"--task-id", taskID}, {"--gate", gateID}, {"--role", role},
		{"--repo", repo}, {"--pr", pullRequest},
	}); code != 0 {
		return code
	}
	if code := checkGateAndRole(stderr, "approve-from-github-pr", gateID, role); code != 0 {
		return code
	}
	number, code := requireInt(stderr, "approve-from-github-pr", "--pr", pullRequest)
	if code != 0 {
		return code
	}

	result, err := registry.approveFromGithubPR(GithubApprovalRequest{
		Root: root, TaskID: taskID, GateID: gateID, AuthorityRole: role,
		Repo: repo, PullRequest: number, ReviewerLogin: reviewerLogin,
		CommitSHA: commitSHA,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	return 0
}

// approveFromGithubPR selects a review and records it.
func (r *Registry) approveFromGithubPR(
	request GithubApprovalRequest,
) (*orderedObject, error) {
	root, err := resolveExisting(request.Root)
	if err != nil {
		return nil, err
	}
	overlay, err := LoadOverlay(root)
	if err != nil {
		return nil, err
	}
	authority, isAuthority := overlay.Authorities[request.AuthorityRole].(map[string]any)
	if !isAuthority {
		return nil, fmt.Errorf("unknown authority role: %s", request.AuthorityRole)
	}
	if request.ReviewerLogin == "" {
		request.ReviewerLogin = AuthorityForgeLogin(authority, "github")
	}
	if request.ReviewerLogin == "" {
		return nil, fmt.Errorf(
			"authority %s has no GitHub login binding and --reviewer-login was not supplied",
			request.AuthorityRole)
	}

	reviews, err := FetchGitHubPullRequestReviews(request.Repo, request.PullRequest)
	if err != nil {
		return nil, err
	}
	review, err := SelectGitHubReview(reviews, request.ReviewerLogin, request.CommitSHA)
	if err != nil {
		return nil, err
	}
	reviewID, isInteger := jsonInteger(review["id"])
	if !isInteger {
		return nil, fmt.Errorf("selected GitHub review is missing a numeric id")
	}
	if !IsValidDatetime(review["submitted_at"]) {
		return nil, fmt.Errorf("selected GitHub review is missing a valid submitted_at timestamp")
	}
	commitSHA, _ := review["commit_id"].(string)
	if commitSHA == "" {
		return nil, fmt.Errorf("selected GitHub review is missing a commit_id")
	}

	request.ReviewID = reviewID
	request.CommitSHA = commitSHA
	request.DecidedAt = toStringOrEmpty(review["submitted_at"])
	result, err := r.RecordGithubApproval(request)
	if err != nil {
		return nil, err
	}
	// Appended after the shared report, exactly where the Python kernel adds
	// them: an operator who named no review needs to see which one this took.
	result.set("selected_review_id", reviewID)
	result.set("selected_commit_sha", commitSHA)
	return result, nil
}

// approveFromGitlabCmd answers `approve-from-gitlab`.
func approveFromGitlabCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := GitlabApprovalRequest{Root: "."}
	var mrIID string
	fields := map[string]*string{
		"--root": &request.Root, "--task-id": &request.TaskID,
		"--gate": &request.GateID, "--role": &request.AuthorityRole,
		"--project-path": &request.ProjectPath, "--mr-iid": &mrIID,
		"--approval-id": &request.ApprovalID, "--approver-username": &request.ApproverUsername,
		"--commit-sha": &request.CommitSHA, "--decided-at": &request.DecidedAt,
	}
	if code := parseFlags("approve-from-gitlab", args, fields, stderr); code != 0 {
		return code
	}
	if code := requireFlags(stderr, "approve-from-gitlab", [][2]string{
		{"--task-id", request.TaskID}, {"--gate", request.GateID},
		{"--role", request.AuthorityRole}, {"--project-path", request.ProjectPath},
		{"--mr-iid", mrIID}, {"--approval-id", request.ApprovalID},
		{"--approver-username", request.ApproverUsername}, {"--commit-sha", request.CommitSHA},
	}); code != 0 {
		return code
	}
	if code := checkGateAndRole(stderr, "approve-from-gitlab",
		request.GateID, request.AuthorityRole); code != 0 {
		return code
	}
	iid, code := requireInt(stderr, "approve-from-gitlab", "--mr-iid", mrIID)
	if code != 0 {
		return code
	}
	request.MergeRequestIID = iid

	result, err := registry.RecordGitlabApproval(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	return 0
}

// approveFromGitlabMRCmd answers `approve-from-gitlab-mr`.
func approveFromGitlabMRCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	root, taskID, gateID, role := ".", "", "", ""
	projectPath, mrIID, approverUsername, commitSHA := "", "", "", ""
	fields := map[string]*string{
		"--root": &root, "--task-id": &taskID, "--gate": &gateID, "--role": &role,
		"--project-path": &projectPath, "--mr-iid": &mrIID,
		"--approver-username": &approverUsername, "--commit-sha": &commitSHA,
	}
	if code := parseFlags("approve-from-gitlab-mr", args, fields, stderr); code != 0 {
		return code
	}
	if code := requireFlags(stderr, "approve-from-gitlab-mr", [][2]string{
		{"--task-id", taskID}, {"--gate", gateID}, {"--role", role},
		{"--project-path", projectPath}, {"--mr-iid", mrIID},
	}); code != 0 {
		return code
	}
	if code := checkGateAndRole(stderr, "approve-from-gitlab-mr", gateID, role); code != 0 {
		return code
	}
	iid, code := requireInt(stderr, "approve-from-gitlab-mr", "--mr-iid", mrIID)
	if code != 0 {
		return code
	}

	result, err := registry.approveFromGitlabMR(GitlabApprovalRequest{
		Root: root, TaskID: taskID, GateID: gateID, AuthorityRole: role,
		ProjectPath: projectPath, MergeRequestIID: iid,
		ApproverUsername: approverUsername, CommitSHA: commitSHA,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	return 0
}

// approveFromGitlabMR selects an approval and records it.
func (r *Registry) approveFromGitlabMR(
	request GitlabApprovalRequest,
) (*orderedObject, error) {
	root, err := resolveExisting(request.Root)
	if err != nil {
		return nil, err
	}
	overlay, err := LoadOverlay(root)
	if err != nil {
		return nil, err
	}
	authority, isAuthority := overlay.Authorities[request.AuthorityRole].(map[string]any)
	if !isAuthority {
		return nil, fmt.Errorf("unknown authority role: %s", request.AuthorityRole)
	}
	if request.ApproverUsername == "" {
		request.ApproverUsername = AuthorityForgeLogin(authority, "gitlab")
	}
	if request.ApproverUsername == "" {
		return nil, fmt.Errorf(
			"authority %s has no GitLab username binding and --approver-username was not supplied",
			request.AuthorityRole)
	}

	approvals, err := FetchGitLabMergeRequestApprovals(request.ProjectPath, request.MergeRequestIID)
	if err != nil {
		return nil, err
	}
	approval, err := SelectGitLabApproval(approvals, request.ApproverUsername, request.CommitSHA)
	if err != nil {
		return nil, err
	}
	approvalID := toStringOrEmpty(approval["approval_id"])
	if approvalID == "" {
		return nil, fmt.Errorf("selected GitLab approval is missing an approval id")
	}
	if !IsValidDatetime(approval["decided_at"]) {
		return nil, fmt.Errorf("selected GitLab approval is missing a valid decided_at timestamp")
	}
	commitSHA, _ := approval["commit_sha"].(string)
	if commitSHA == "" {
		return nil, fmt.Errorf("selected GitLab approval is missing a commit sha")
	}

	request.ApprovalID = approvalID
	request.CommitSHA = commitSHA
	request.DecidedAt = toStringOrEmpty(approval["decided_at"])
	result, err := r.RecordGitlabApproval(request)
	if err != nil {
		return nil, err
	}
	result.set("selected_approval_id", approvalID)
	result.set("selected_commit_sha", commitSHA)
	return result, nil
}

// linkSourceIssueCmd answers all four `link-*-from-*-issue` subcommands.
//
// One function, because only three things vary: which gate the link is for,
// which forge is read, and what the identifying flag is called. Nothing about
// the link itself differs, and neither does the authorization.
func linkSourceIssueCmd(
	registry *Registry, command, gateID, forge string,
	args []string, stdout, stderr io.Writer,
) int {
	request := SourceLinkRequest{Root: ".", GateID: gateID}
	var issueNumber string
	fields := map[string]*string{
		"--root": &request.Root, "--task-id": &request.TaskID,
		"--role": &request.AuthorityRole,
	}
	numberFlag := "--issue-iid"
	locationFlag := "--project-path"
	if forge == ForgeGitHub {
		numberFlag = "--issue-number"
		locationFlag = "--repo"
		fields["--repo"] = &request.Repo
	} else {
		fields["--project-path"] = &request.ProjectPath
	}
	fields[numberFlag] = &issueNumber

	if code := parseFlags(command, args, fields, stderr); code != 0 {
		return code
	}
	location := request.ProjectPath
	if forge == ForgeGitHub {
		location = request.Repo
	}
	if code := requireFlags(stderr, command, [][2]string{
		{"--task-id", request.TaskID}, {"--role", request.AuthorityRole},
		{locationFlag, location}, {numberFlag, issueNumber},
	}); code != 0 {
		return code
	}
	if code := rejectInvalidChoice(stderr, command, "--role", request.AuthorityRole,
		sortedAuthorityRoles()); code != 0 {
		return code
	}
	number, code := requireInt(stderr, command, numberFlag, issueNumber)
	if code != 0 {
		return code
	}
	request.IssueNumber = number

	var issue *SourceIssue
	var err error
	if forge == ForgeGitHub {
		issue, err = FetchGitHubIssue(request.Repo, number)
	} else {
		issue, err = FetchGitLabIssue(request.ProjectPath, number)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}

	result, err := registry.RecordSourceIssueLink(request, issue)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	return 0
}

// requireFlags reports every missing required flag at once.
//
// All of them, not the first: an operator fixing one at a time and re-running
// learns about the next one only after another failure.
func requireFlags(stderr io.Writer, command string, required [][2]string) int {
	var missing []string
	for _, pair := range required {
		if pair[1] == "" {
			missing = append(missing, pair[0])
		}
	}
	if len(missing) == 0 {
		return 0
	}
	_, _ = fmt.Fprintf(stderr,
		"agentic-sdlc %s: error: the following arguments are required: %s\n",
		command, strings.Join(missing, ", "))
	return 2
}

func requireInt(stderr io.Writer, command, flag, value string) (int, int) {
	number, err := strconv.Atoi(value)
	if err != nil {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc %s: error: argument %s: invalid int value: %s\n",
			command, flag, pythonRepr(value))
		return 0, 2
	}
	return number, 0
}

// checkGateAndRole rejects a gate id or authority role the parser would not
// have accepted, as a usage error rather than something the kernel attempts.
func checkGateAndRole(stderr io.Writer, command, gateID, role string) int {
	if code := rejectInvalidChoice(stderr, command, "--gate", gateID, GateIDs); code != 0 {
		return code
	}
	return rejectInvalidChoice(stderr, command, "--role", role, sortedAuthorityRoles())
}
