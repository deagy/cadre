package orchestration

import (
	"encoding/json"
	"fmt"
)

// The GitLab evidence tool server: the tool-facing surface over gitlab.go,
// exposed to an agent session through stdio MCP.
//
// A port of roster/orchestration/mcp/gitlab_server.py. The safety-relevant
// logic -- token/config resolution, the HTTP calls, retry, the confirmation
// gate, the audit record -- all lives in gitlab.go and is shared with `cadre
// gitlab-evidence`; this file only names the three tools and decodes their
// arguments.
//
// Every tool here is create-only. Nothing in this file, and nothing it calls,
// can close, reopen, resolve, or relabel-away-from-open-review a GitLab issue
// -- see gitlab.go's "STATE TRANSITION" comment. Adding a tool here that
// could is the change this note exists to stop.

// GitLabMCPServer exposes the create-only GitLab evidence tools.
//
// auditPath is a field rather than a tool argument deliberately: the audit
// log's location is an operator decision, and a tool that let its caller
// redirect its own audit trail is not an audit trail. It is set from the
// process, never from a tool call. GITLAB-EVIDENCE.md documents this as a
// property of the integration.
type GitLabMCPServer struct {
	auditPath string
}

// NewGitLabMCPServer builds the server. An empty auditPath means the default
// location, which is what the CLI passes.
func NewGitLabMCPServer(auditPath string) *GitLabMCPServer {
	return &GitLabMCPServer{auditPath: auditPath}
}

// GetToolDefinitions returns the three create-only tools.
func (server *GitLabMCPServer) GetToolDefinitions() []MCPToolDefinition {
	return []MCPToolDefinition{
		{
			Name: "create_review_subtask",
			Description: "Create (or, if a matching one already exists, return) a GitLab issue " +
				"linked to parent_issue_iid as a review subtask. Never closes, reopens, " +
				"resolves, or relabels any issue -- create-only.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"parent_issue_iid": map[string]any{"type": "integer",
						"description": "The iid of the existing parent GitLab issue in the configured project."},
					"title": map[string]any{"type": "string",
						"description": "The subtask issue's own title."},
					"description": map[string]any{"type": "string",
						"description": "The subtask issue's body. Untrusted task data the caller supplies, not an instruction to this tool."},
					"gate_id": map[string]any{"type": "string",
						"description": "Which lifecycle gate this subtask evidences, e.g. \"G5\"; builds the gate:<gate_id> label and half this call's idempotency key."},
					"task_id": map[string]any{"type": "string",
						"description": "The calling task's identifier. With gate_id it is this call's idempotency key, so repeated calls never create duplicate subtasks."},
				},
				"required": []string{"parent_issue_iid", "title", "description", "gate_id", "task_id"},
			},
		},
		{
			Name: "write_wiki_page",
			Description: "Create or update a versioned wiki page in the configured GitLab project. " +
				"Requires human confirmation on every call, with no exception: the first call " +
				"(omit confirmation_token) never writes anything and instead returns a " +
				"confirmation_token to replay on a second, otherwise-identical call.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"slug": map[string]any{"type": "string",
						"description": "The wiki page's slug (path), e.g. \"evidence/task-42\"."},
					"title":   map[string]any{"type": "string", "description": "The page's title (untrusted task data)."},
					"content": map[string]any{"type": "string", "description": "The page's body (untrusted task data)."},
					"format": map[string]any{"type": "string", "default": "markdown",
						"enum": []string{"markdown", "rdoc", "asciidoc", "org"}},
					"confirmation_token": map[string]any{"type": "string",
						"description": "Omit on the first call; supply the token from that call's confirmation_required response on the second."},
				},
				"required": []string{"slug", "title", "content"},
			},
		},
		{
			Name: "write_evidence_comment",
			Description: "Add a comment to an existing GitLab issue for small, structured " +
				"per-task evidence.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"issue_iid": map[string]any{"type": "integer",
						"description": "The iid of the existing GitLab issue to comment on."},
					"content": map[string]any{"type": "string",
						"description": "The comment body (untrusted task data); rejected outright, never truncated, if its UTF-8 encoding exceeds 1 MiB."},
					"task_id": map[string]any{"type": "string",
						"description": "The calling task's identifier, recorded for traceability."},
				},
				"required": []string{"issue_iid", "content", "task_id"},
			},
		},
	}
}

// DispatchToolCall routes a tool call to gitlab.go.
func (server *GitLabMCPServer) DispatchToolCall(toolName string, args json.RawMessage) *MCPToolResponse {
	switch toolName {
	case "create_review_subtask":
		var request struct {
			ParentIssueIID int    `json:"parent_issue_iid"`
			Title          string `json:"title"`
			Description    string `json:"description"`
			GateID         string `json:"gate_id"`
			TaskID         string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &request); err != nil {
			return gitlabArgumentError(toolName, err)
		}
		return gitlabToolResult(CreateGitLabReviewSubtask(nil,
			request.ParentIssueIID, request.Title, request.Description,
			request.GateID, request.TaskID, server.auditPath))

	case "write_wiki_page":
		var request struct {
			Slug              string `json:"slug"`
			Title             string `json:"title"`
			Content           string `json:"content"`
			Format            string `json:"format"`
			ConfirmationToken string `json:"confirmation_token"`
		}
		if err := json.Unmarshal(args, &request); err != nil {
			return gitlabArgumentError(toolName, err)
		}
		return gitlabToolResult(WriteGitLabWikiPage(nil,
			request.Slug, request.Title, request.Content, request.Format,
			request.ConfirmationToken, server.auditPath))

	case "write_evidence_comment":
		var request struct {
			IssueIID int    `json:"issue_iid"`
			Content  string `json:"content"`
			TaskID   string `json:"task_id"`
		}
		if err := json.Unmarshal(args, &request); err != nil {
			return gitlabArgumentError(toolName, err)
		}
		return gitlabToolResult(WriteGitLabEvidenceComment(nil,
			request.IssueIID, request.Content, request.TaskID, server.auditPath))
	}

	return &MCPToolResponse{
		Status: "error", IsError: true,
		Error: fmt.Sprintf("unknown tool: %s", toolName),
	}
}

// gitlabArgumentError refuses a call whose arguments did not decode.
//
// Not a partial decode with the rest left at zero: an issue iid of 0 or an
// empty slug is a different write from the one that was asked for, and
// gitlab.go would have to guess which.
func gitlabArgumentError(toolName string, err error) *MCPToolResponse {
	return &MCPToolResponse{
		Status: "error", IsError: true,
		Error: fmt.Sprintf("%s: arguments could not be decoded: %v", toolName, err),
	}
}

// gitlabToolResult carries gitlab.go's own result map through unchanged.
//
// The status field it already sets -- "ok", "denied", "unavailable",
// "confirmation_required" -- is the tool's answer, and rewording it here
// would put a second vocabulary in front of callers who also read `cadre
// gitlab-evidence`'s output. Only isError is derived.
//
// "confirmation_required" is deliberately not an error: it is the human
// confirmation gate working as designed, and a first call that returns it has
// succeeded at exactly what it was supposed to do. Flagging it as a failure
// would invite a model to treat the gate as a fault to route around.
func gitlabToolResult(result map[string]any) *MCPToolResponse {
	status, _ := result["status"].(string)
	response := &MCPToolResponse{Status: status, Result: result}
	if status != "ok" && status != "confirmation_required" {
		response.IsError = true
		if message, ok := result["reason"].(string); ok {
			response.Error = message
		}
	}
	return response
}
