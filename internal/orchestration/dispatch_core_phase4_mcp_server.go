package orchestration

import (
	"encoding/json"
	"fmt"
)

// Phase 4.1: MCP Dispatch Server
// Go implementation of roster/orchestration/mcp/dispatch_server.py
// Replaces Python subprocess-based dispatch with native MCP tool interface

// DispatchMCPServer provides MCP tool interface for dispatch operations
type DispatchMCPServer struct {
	projectRoot       string
	globalRoot        string
	pluginRoot        string
	jobStore          *DispatchJobStore
	teamJobStore      *TeamDispatchJobStore
	confirmationGates map[string]*ConfirmationGate
}

// DispatchMCPServerConfig holds configuration for the MCP server
type DispatchMCPServerConfig struct {
	ProjectRoot string
	GlobalRoot  string
	PluginRoot  string
}

// NewDispatchMCPServer creates a new MCP dispatch server
func NewDispatchMCPServer(config DispatchMCPServerConfig) *DispatchMCPServer {
	return &DispatchMCPServer{
		projectRoot:       config.ProjectRoot,
		globalRoot:        config.GlobalRoot,
		pluginRoot:        config.PluginRoot,
		jobStore:          NewDispatchJobStore(),
		teamJobStore:      NewTeamDispatchJobStore(),
		confirmationGates: make(map[string]*ConfirmationGate),
	}
}

// MCPToolRequest represents an incoming MCP tool call
type MCPToolRequest struct {
	Name      string
	Arguments map[string]any
}

// MCPToolResponse represents the response to an MCP tool call
type MCPToolResponse struct {
	Status  string
	Result  map[string]any
	Error   string
	IsError bool
}

// DispatchSecureCloudRoleRequest is the MCP tool arguments for dispatch_secure_cloud_role
type DispatchSecureCloudRoleRequest struct {
	RoleID               string `json:"role_id"`
	Brief                string `json:"brief"`
	Mode                 string `json:"mode"`
	Classification       string `json:"classification"`
	ConfirmationToken    string `json:"confirmation_token,omitempty"`
	TaskID               string `json:"task_id,omitempty"`
	SessionID            string `json:"session_id,omitempty"`
	ParentClassification string `json:"parent_classification,omitempty"`
	Runner               string `json:"runner,omitempty"`
	Wait                 bool   `json:"wait"`
}

// DispatchTeamRequest is the MCP tool arguments for dispatch_team
type DispatchTeamRequest struct {
	Members              []map[string]string `json:"members"`
	Mode                 string              `json:"mode"`
	Classification       string              `json:"classification"`
	ConfirmationToken    string              `json:"confirmation_token,omitempty"`
	TaskID               string              `json:"task_id,omitempty"`
	SessionID            string              `json:"session_id,omitempty"`
	ParentClassification string              `json:"parent_classification,omitempty"`
	Runner               string              `json:"runner,omitempty"`
	Wait                 bool                `json:"wait"`
}

// PollDispatchStatusRequest is the MCP tool arguments for poll_dispatch_status
type PollDispatchStatusRequest struct {
	JobID string `json:"job_id"`
}

// PollTeamStatusRequest is the MCP tool arguments for poll_team_status
type PollTeamStatusRequest struct {
	TeamID string `json:"team_id"`
}

// HandleDispatchSecureCloudRole processes the dispatch_secure_cloud_role MCP tool call
func (server *DispatchMCPServer) HandleDispatchSecureCloudRole(req *DispatchSecureCloudRoleRequest) *MCPToolResponse {
	if req == nil {
		return &MCPToolResponse{
			Status:  "error",
			Error:   "request is nil",
			IsError: true,
		}
	}

	// Call the dispatch function
	result := DispatchSecureCloudRole(
		req.RoleID,
		req.Brief,
		req.Mode,
		req.Classification,
		req.ConfirmationToken,
		req.TaskID,
		req.SessionID,
		req.ParentClassification,
		req.Runner,
		req.Wait,
	)

	// Extract status
	status := "unknown"
	if s, ok := result["status"].(string); ok {
		status = s
	}

	isError := status == "error" || status == "denied"

	return &MCPToolResponse{
		Status:  status,
		Result:  result,
		IsError: isError,
	}
}

// HandleDispatchTeam processes the dispatch_team MCP tool call
func (server *DispatchMCPServer) HandleDispatchTeam(req *DispatchTeamRequest) *MCPToolResponse {
	if req == nil {
		return &MCPToolResponse{
			Status:  "error",
			Error:   "request is nil",
			IsError: true,
		}
	}

	// Call the dispatch function
	result := DispatchTeam(
		req.Members,
		req.Mode,
		req.Classification,
		req.ConfirmationToken,
		req.TaskID,
		req.SessionID,
		req.ParentClassification,
		req.Runner,
		req.Wait,
	)

	// Extract status
	status := "unknown"
	if s, ok := result["status"].(string); ok {
		status = s
	}

	isError := status == "error" || status == "denied"

	return &MCPToolResponse{
		Status:  status,
		Result:  result,
		IsError: isError,
	}
}

// HandlePollDispatchStatus processes the poll_dispatch_status MCP tool call
func (server *DispatchMCPServer) HandlePollDispatchStatus(req *PollDispatchStatusRequest) *MCPToolResponse {
	if req == nil {
		return &MCPToolResponse{
			Status:  "error",
			Error:   "request is nil",
			IsError: true,
		}
	}

	if req.JobID == "" {
		return &MCPToolResponse{
			Status:  "error",
			Error:   "job_id is required",
			IsError: true,
		}
	}

	// Call the poll function
	result := PollDispatchStatus(req.JobID)

	// Extract status
	status := "unknown"
	if s, ok := result["status"].(string); ok {
		status = s
	}

	isError := status == "error"

	return &MCPToolResponse{
		Status:  status,
		Result:  result,
		IsError: isError,
	}
}

// HandlePollTeamStatus processes the poll_team_status MCP tool call
func (server *DispatchMCPServer) HandlePollTeamStatus(req *PollTeamStatusRequest) *MCPToolResponse {
	if req == nil {
		return &MCPToolResponse{
			Status:  "error",
			Error:   "request is nil",
			IsError: true,
		}
	}

	if req.TeamID == "" {
		return &MCPToolResponse{
			Status:  "error",
			Error:   "team_id is required",
			IsError: true,
		}
	}

	// Call the poll function
	result := PollTeamStatus(req.TeamID)

	// Extract status
	status := "unknown"
	if s, ok := result["status"].(string); ok {
		status = s
	}

	isError := status == "error"

	return &MCPToolResponse{
		Status:  status,
		Result:  result,
		IsError: isError,
	}
}

// DispatchToolCall routes an MCP tool call to the appropriate handler
func (server *DispatchMCPServer) DispatchToolCall(toolName string, args json.RawMessage) *MCPToolResponse {
	// The context-store tools live in mcp_context_tools.go beside their own
	// definitions, so adding one cannot leave the two out of step.
	if response, handled := server.dispatchContextToolCall(toolName, args); handled {
		return response
	}

	switch toolName {
	case "dispatch_secure_cloud_role":
		var req DispatchSecureCloudRoleRequest
		if err := json.Unmarshal(args, &req); err != nil {
			return &MCPToolResponse{
				Status:  "error",
				Error:   fmt.Sprintf("failed to parse arguments: %v", err),
				IsError: true,
			}
		}
		return server.HandleDispatchSecureCloudRole(&req)

	case "dispatch_team":
		var req DispatchTeamRequest
		if err := json.Unmarshal(args, &req); err != nil {
			return &MCPToolResponse{
				Status:  "error",
				Error:   fmt.Sprintf("failed to parse arguments: %v", err),
				IsError: true,
			}
		}
		return server.HandleDispatchTeam(&req)

	case "poll_dispatch_status":
		var req PollDispatchStatusRequest
		if err := json.Unmarshal(args, &req); err != nil {
			return &MCPToolResponse{
				Status:  "error",
				Error:   fmt.Sprintf("failed to parse arguments: %v", err),
				IsError: true,
			}
		}
		return server.HandlePollDispatchStatus(&req)

	case "poll_team_status":
		var req PollTeamStatusRequest
		if err := json.Unmarshal(args, &req); err != nil {
			return &MCPToolResponse{
				Status:  "error",
				Error:   fmt.Sprintf("failed to parse arguments: %v", err),
				IsError: true,
			}
		}
		return server.HandlePollTeamStatus(&req)

	default:
		return &MCPToolResponse{
			Status:  "error",
			Error:   fmt.Sprintf("unknown tool: %q", toolName),
			IsError: true,
		}
	}
}

// ValidateConfig validates server configuration
func (server *DispatchMCPServer) ValidateConfig() error {
	// Verify all required paths are set
	if server.projectRoot == "" {
		return fmt.Errorf("project_root is required")
	}

	if server.globalRoot == "" {
		return fmt.Errorf("global_root is required")
	}

	if server.pluginRoot == "" {
		return fmt.Errorf("plugin_root is required")
	}

	return nil
}

// MCPToolDefinition describes an MCP tool
type MCPToolDefinition struct {
	Name        string
	Description string
	Schema      map[string]any
}

// GetToolDefinitions returns the definitions of all MCP tools
func (server *DispatchMCPServer) GetToolDefinitions() []MCPToolDefinition {
	return append(dispatchToolDefinitions(), contextToolDefinitions()...)
}

func dispatchToolDefinitions() []MCPToolDefinition {
	return []MCPToolDefinition{
		{
			Name:        "dispatch_secure_cloud_role",
			Description: "Dispatch a secure cloud role with validation, confirmation gating, and audit logging",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"role_id": map[string]any{
						"type":        "string",
						"description": "The role ID to dispatch",
					},
					"brief": map[string]any{
						"type":        "string",
						"description": "The task brief for the role",
					},
					"mode": map[string]any{
						"type":        "string",
						"description": "Dispatch mode: planning-review-only or scoped-repository-edit",
					},
					"classification": map[string]any{
						"type":        "string",
						"description": "Data classification: public, internal, confidential, restricted",
					},
					"confirmation_token": map[string]any{
						"type":        "string",
						"description": "Optional confirmation token for write-mode approval",
					},
					"task_id": map[string]any{
						"type":        "string",
						"description": "Optional task ID for audit tracking",
					},
					"wait": map[string]any{
						"type":        "boolean",
						"description": "Wait for completion (sync) or return immediately (async)",
					},
				},
				"required": []string{"role_id", "brief", "mode", "classification", "wait"},
			},
		},
		{
			Name:        "dispatch_team",
			Description: "Dispatch multiple roles as a coordinated team",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"members": map[string]any{
						"type":        "array",
						"description": "Team members, each with role_id and brief",
					},
					"mode": map[string]any{
						"type":        "string",
						"description": "Dispatch mode for all members",
					},
					"wait": map[string]any{
						"type":        "boolean",
						"description": "Wait for all members (sync) or return immediately (async)",
					},
				},
				"required": []string{"members", "mode", "wait"},
			},
		},
		{
			Name:        "poll_dispatch_status",
			Description: "Poll the status of an async dispatch job",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"job_id": map[string]any{
						"type":        "string",
						"description": "The job ID to poll",
					},
				},
				"required": []string{"job_id"},
			},
		},
		{
			Name:        "poll_team_status",
			Description: "Poll the status of an async team dispatch",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"team_id": map[string]any{
						"type":        "string",
						"description": "The team ID to poll",
					},
				},
				"required": []string{"team_id"},
			},
		},
	}
}
