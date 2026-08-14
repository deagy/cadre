package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// GitLabEvidenceCmd is the `cadre gitlab-evidence` command: a non-MCP CLI
// over the create-only GitLab evidence tools (create-review-subtask,
// write-wiki-page, write-evidence-comment). See internal/orchestration/
// gitlab.go's package doc for the hard invariant this integration
// maintains (never closes/reopens/resolves/relabels-away-from-open-review
// an issue) and the scope boundary it documents.
func GitLabEvidenceCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cadre gitlab-evidence <create-review-subtask|write-wiki-page|write-evidence-comment> [args...]")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "create-review-subtask":
		return gitlabCreateReviewSubtaskCmd(rest)
	case "write-wiki-page":
		return gitlabWriteWikiPageCmd(rest)
	case "write-evidence-comment":
		return gitlabWriteEvidenceCommentCmd(rest)
	case "-h", "--help":
		fmt.Println("usage: cadre gitlab-evidence <create-review-subtask|write-wiki-page|write-evidence-comment> [args...]")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "cadre gitlab-evidence: unknown subcommand %q\n", sub)
		return 2
	}
}

func printGitLabResult(result map[string]any) int {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre gitlab-evidence: %v\n", err)
		return 1
	}
	fmt.Println(string(data))
	switch result["status"] {
	case "ok", "confirmation_required":
		return 0
	default:
		return 1
	}
}

func gitlabCreateReviewSubtaskCmd(args []string) int {
	fs := flag.NewFlagSet("cadre gitlab-evidence create-review-subtask", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	parentIssueIID := fs.Int("parent-issue-iid", 0, "Parent issue IID (required)")
	title := fs.String("title", "", "Review-subtask issue title (required)")
	description := fs.String("description", "", "Review-subtask issue description")
	gateID := fs.String("gate-id", "", "Gate identifier (required)")
	taskID := fs.String("task-id", "", "Task identifier (required)")
	auditPath := fs.String("audit-path", "", "Override the audit log path (default: ~/.agents/mcp-gitlab/audit.jsonl)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *parentIssueIID <= 0 || *title == "" || *gateID == "" || *taskID == "" {
		fmt.Fprintln(os.Stderr, "cadre gitlab-evidence create-review-subtask: --parent-issue-iid, --title, --gate-id, and --task-id are required")
		return 2
	}
	result := orchestration.CreateGitLabReviewSubtask(nil, *parentIssueIID, *title, *description, *gateID, *taskID, *auditPath)
	return printGitLabResult(result)
}

func gitlabWriteWikiPageCmd(args []string) int {
	fs := flag.NewFlagSet("cadre gitlab-evidence write-wiki-page", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	slug := fs.String("slug", "", "Wiki page slug (required)")
	title := fs.String("title", "", "Wiki page title (required)")
	content := fs.String("content", "", "Wiki page content (required)")
	format := fs.String("format", "markdown", "markdown, rdoc, asciidoc, or org")
	confirmationToken := fs.String("confirmation-token", "", "Confirmation token from a prior confirmation_required response")
	auditPath := fs.String("audit-path", "", "Override the audit log path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *slug == "" || *title == "" || *content == "" {
		fmt.Fprintln(os.Stderr, "cadre gitlab-evidence write-wiki-page: --slug, --title, and --content are required")
		return 2
	}
	result := orchestration.WriteGitLabWikiPage(nil, *slug, *title, *content, *format, *confirmationToken, *auditPath)
	return printGitLabResult(result)
}

func gitlabWriteEvidenceCommentCmd(args []string) int {
	fs := flag.NewFlagSet("cadre gitlab-evidence write-evidence-comment", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	issueIID := fs.Int("issue-iid", 0, "Issue IID to comment on (required)")
	content := fs.String("content", "", "Comment content (required)")
	taskID := fs.String("task-id", "", "Task identifier (required)")
	auditPath := fs.String("audit-path", "", "Override the audit log path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *issueIID <= 0 || *content == "" || *taskID == "" {
		fmt.Fprintln(os.Stderr, "cadre gitlab-evidence write-evidence-comment: --issue-iid, --content, and --task-id are required")
		return 2
	}
	result := orchestration.WriteGitLabEvidenceComment(nil, *issueIID, *content, *taskID, *auditPath)
	return printGitLabResult(result)
}
