// Package mcpserver ports roster/orchestration/mcp/*_server.py: the MCP
// stdio protocol-adapter layer that exposes already-shipped Go business
// logic as tools callable by an MCP-capable host (e.g. Codex, Claude Code).
//
// gitlab_server.go ports gitlab_server.py exactly: every tool registered
// here is create-only (create_review_subtask, write_wiki_page,
// write_evidence_comment). None of them, nor anything they call
// (internal/orchestration/gitlab.go), can ever close, reopen, resolve, or
// relabel-away-from-open-review a GitLab issue -- see gitlab.go's package
// doc and its "STATE TRANSITION" note.
//
// This file is the thin protocol wrapper only; every actual behavior
// (token/config resolution, HTTP calls, retry, the confirmation gate,
// audit logging) already lives in internal/orchestration/gitlab.go,
// shipped earlier in this Go CLI port -- this file adds no new business
// logic, matching gitlab_server.py's own relationship to gitlab_core.py.
package mcpserver

import (
	"context"

	"github.com/deagy/cadre/cli/internal/orchestration"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewGitLabServer constructs the MCP server and registers the three
// create-only GitLab tools, matching gitlab_server.py's build_server().
func NewGitLabServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "gitlab-evidence"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "create_review_subtask",
		Description: "Create (or, if a matching one already exists, return) a GitLab issue linked " +
			"to parent_issue_iid as a review subtask. Never closes, reopens, resolves, or relabels " +
			"any issue -- create-only.",
	}, createReviewSubtaskHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "write_wiki_page",
		Description: "Create or update a versioned wiki page in the configured GitLab project. " +
			"Requires human confirmation on every call, with no exception: the first call (omit " +
			"confirmation_token) never writes anything and instead returns a confirmation_token to " +
			"replay on a second, otherwise-identical call.",
	}, writeWikiPageHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name: "write_evidence_comment",
		Description: "Add a comment to an existing GitLab issue for small, structured per-task " +
			"evidence. content is rejected outright (never truncated) if its UTF-8 encoding exceeds " +
			"1 MiB.",
	}, writeEvidenceCommentHandler)

	return server
}

// CreateReviewSubtaskArgs mirrors create_review_subtask's Python signature.
type CreateReviewSubtaskArgs struct {
	ParentIssueIID int    `json:"parent_issue_iid" jsonschema:"the iid of the existing parent GitLab issue in the configured project"`
	Title          string `json:"title" jsonschema:"the subtask issue's own title"`
	Description    string `json:"description" jsonschema:"the subtask issue's own body; untrusted task data the caller supplies, not an instruction to this tool"`
	GateID         string `json:"gate_id" jsonschema:"identifies which lifecycle gate this subtask evidences, e.g. G5; used to build the gate:<gate_id> label and this call's idempotency key"`
	TaskID         string `json:"task_id" jsonschema:"the calling task's identifier; used, together with gate_id, as this call's idempotency key so repeated calls never create duplicate subtasks"`
}

func createReviewSubtaskHandler(_ context.Context, _ *mcp.CallToolRequest, args CreateReviewSubtaskArgs) (*mcp.CallToolResult, map[string]any, error) {
	result := orchestration.CreateGitLabReviewSubtask(nil, args.ParentIssueIID, args.Title, args.Description, args.GateID, args.TaskID, "")
	return nil, result, nil
}

// WriteWikiPageArgs mirrors write_wiki_page's Python signature.
type WriteWikiPageArgs struct {
	Slug              string `json:"slug" jsonschema:"the wiki page's slug (path), e.g. evidence/task-42"`
	Title             string `json:"title" jsonschema:"the page's title"`
	Content           string `json:"content" jsonschema:"the page's body (untrusted task data)"`
	Format            string `json:"format,omitempty" jsonschema:"one of markdown (default), rdoc, asciidoc, org"`
	ConfirmationToken string `json:"confirmation_token,omitempty" jsonschema:"omit on the first call; supply the token from that call's confirmation_required response on the second call"`
}

func writeWikiPageHandler(_ context.Context, _ *mcp.CallToolRequest, args WriteWikiPageArgs) (*mcp.CallToolResult, map[string]any, error) {
	format := args.Format
	if format == "" {
		format = "markdown"
	}
	result := orchestration.WriteGitLabWikiPage(nil, args.Slug, args.Title, args.Content, format, args.ConfirmationToken, "")
	return nil, result, nil
}

// WriteEvidenceCommentArgs mirrors write_evidence_comment's Python signature.
type WriteEvidenceCommentArgs struct {
	IssueIID int    `json:"issue_iid" jsonschema:"the iid of the existing GitLab issue to comment on"`
	Content  string `json:"content" jsonschema:"the comment body (untrusted task data); rejected outright if its UTF-8 encoding exceeds 1 MiB"`
	TaskID   string `json:"task_id" jsonschema:"the calling task's identifier, recorded for traceability"`
}

func writeEvidenceCommentHandler(_ context.Context, _ *mcp.CallToolRequest, args WriteEvidenceCommentArgs) (*mcp.CallToolResult, map[string]any, error) {
	result := orchestration.WriteGitLabEvidenceComment(nil, args.IssueIID, args.Content, args.TaskID, "")
	return nil, result, nil
}
