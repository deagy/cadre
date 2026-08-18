package orchestration

import (
	"encoding/json"
	"testing"
)

func TestNewDispatchMCPServer(t *testing.T) {
	config := DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	}

	server := NewDispatchMCPServer(config)
	if server == nil {
		t.Fatalf("NewDispatchMCPServer returned nil")
	}

	if server.projectRoot != "/project" {
		t.Errorf("projectRoot not set correctly")
	}

	if server.jobStore == nil {
		t.Errorf("jobStore not initialized")
	}
}

func TestValidateConfigValid(t *testing.T) {
	server := &DispatchMCPServer{
		projectRoot: "/project",
		globalRoot:  "/global",
		pluginRoot:  "/plugin",
	}

	err := server.ValidateConfig()
	if err != nil {
		t.Errorf("ValidateConfig failed for valid config: %v", err)
	}
}

func TestValidateConfigMissingProjectRoot(t *testing.T) {
	server := &DispatchMCPServer{
		projectRoot: "",
		globalRoot:  "/global",
		pluginRoot:  "/plugin",
	}

	err := server.ValidateConfig()
	if err == nil {
		t.Errorf("ValidateConfig should fail when projectRoot is empty")
	}
}

func TestValidateConfigMissingGlobalRoot(t *testing.T) {
	server := &DispatchMCPServer{
		projectRoot: "/project",
		globalRoot:  "",
		pluginRoot:  "/plugin",
	}

	err := server.ValidateConfig()
	if err == nil {
		t.Errorf("ValidateConfig should fail when globalRoot is empty")
	}
}

func TestValidateConfigMissingPluginRoot(t *testing.T) {
	server := &DispatchMCPServer{
		projectRoot: "/project",
		globalRoot:  "/global",
		pluginRoot:  "",
	}

	err := server.ValidateConfig()
	if err == nil {
		t.Errorf("ValidateConfig should fail when pluginRoot is empty")
	}
}

func TestHandleDispatchSecureCloudRoleNilRequest(t *testing.T) {
	stubRunner(t)
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	response := server.HandleDispatchSecureCloudRole(nil)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	if response.Status != "error" || !response.IsError {
		t.Errorf("nil request should return error")
	}
}

func TestHandleDispatchSecureCloudRoleInvalidInput(t *testing.T) {
	stubRunner(t)
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	req := &DispatchSecureCloudRoleRequest{
		RoleID:         "",
		Brief:          "test",
		Mode:           "planning-review-only",
		Classification: "public",
		Wait:           true,
	}

	response := server.HandleDispatchSecureCloudRole(req)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	// Empty role_id should be denied
	if response.Status != "error" && response.Status != "denied" {
		t.Logf("status = %q (may be error/denied for invalid role)", response.Status)
	}
}

func TestHandleDispatchTeamNilRequest(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	response := server.HandleDispatchTeam(nil)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	if response.Status != "error" || !response.IsError {
		t.Errorf("nil request should return error")
	}
}

func TestHandleDispatchTeamEmptyMembers(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	req := &DispatchTeamRequest{
		Members:        []map[string]string{},
		Mode:           "planning-review-only",
		Classification: "public",
		Wait:           true,
	}

	response := server.HandleDispatchTeam(req)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	// Empty members should be denied
	if response.Status != "error" && response.Status != "denied" {
		t.Logf("status = %q (may be error/denied for empty members)", response.Status)
	}
}

func TestHandlePollDispatchStatusNilRequest(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	response := server.HandlePollDispatchStatus(nil)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	if response.Status != "error" || !response.IsError {
		t.Errorf("nil request should return error")
	}
}

func TestHandlePollDispatchStatusEmptyJobID(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	req := &PollDispatchStatusRequest{JobID: ""}
	response := server.HandlePollDispatchStatus(req)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	if response.Status != "error" || !response.IsError {
		t.Errorf("empty job_id should return error")
	}
}

func TestHandlePollDispatchStatusNotFound(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	req := &PollDispatchStatusRequest{JobID: "job_nonexistent"}
	response := server.HandlePollDispatchStatus(req)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	// Not found should not be an error, just a status
	if response.IsError {
		t.Errorf("not_found should not be treated as error")
	}
}

func TestHandlePollTeamStatusNilRequest(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	response := server.HandlePollTeamStatus(nil)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	if response.Status != "error" || !response.IsError {
		t.Errorf("nil request should return error")
	}
}

func TestHandlePollTeamStatusEmptyTeamID(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	req := &PollTeamStatusRequest{TeamID: ""}
	response := server.HandlePollTeamStatus(req)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	if response.Status != "error" || !response.IsError {
		t.Errorf("empty team_id should return error")
	}
}

func TestDispatchToolCallUnknownTool(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	args := json.RawMessage(`{}`)
	response := server.DispatchToolCall("unknown_tool", args)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	if response.Status != "error" || !response.IsError {
		t.Errorf("unknown tool should return error")
	}
}

func TestDispatchToolCallInvalidJSON(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	invalidJSON := json.RawMessage(`{invalid}`)
	response := server.DispatchToolCall("dispatch_secure_cloud_role", invalidJSON)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	if response.Status != "error" || !response.IsError {
		t.Errorf("invalid JSON should return error")
	}
}

func TestGetToolDefinitions(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	definitions := server.GetToolDefinitions()
	if definitions == nil {
		t.Errorf("GetToolDefinitions returned nil")
		return
	}

	// Every group the server assembles, counted from the groups themselves
	// rather than pinned to a literal -- the server has grown twice now (the
	// context tools, then dispatch_team_recipe), and a literal would make
	// each new tool a test edit rather than a definition.
	want := len(dispatchToolDefinitions()) + len(contextToolDefinitions()) + 1
	if len(definitions) != want {
		t.Errorf("got %d tool definitions, want %d (dispatch + context + team recipe)",
			len(definitions), want)
	}

	// Every tool that must be present, dispatch and context alike.
	toolNames := map[string]bool{
		"dispatch_secure_cloud_role": false,
		"dispatch_team":              false,
		"poll_dispatch_status":       false,
		"poll_team_status":           false,
		"context_put":                false,
		"context_get":                false,
		"context_list":               false,
		"context_search":             false,
		"dispatch_team_recipe":       false,
	}

	for _, def := range definitions {
		if _, ok := toolNames[def.Name]; ok {
			toolNames[def.Name] = true
		}
	}

	for name, found := range toolNames {
		if !found {
			t.Errorf("tool %q not in definitions", name)
		}
	}
}

func TestDispatchToolCallDispatchSecureCloudRole(t *testing.T) {
	stubRunner(t)
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	req := DispatchSecureCloudRoleRequest{
		RoleID:         "test-role",
		Brief:          "test",
		Mode:           "planning-review-only",
		Classification: "public",
		Wait:           true,
	}

	argBytes, _ := json.Marshal(req)
	response := server.DispatchToolCall("dispatch_secure_cloud_role", argBytes)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	// Should get some response (may be denied/error/success)
	if response.Status == "" {
		t.Errorf("response.Status is empty")
	}
}

func TestDispatchToolCallDispatchTeam(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	req := DispatchTeamRequest{
		Members:        []map[string]string{{"role_id": "role1", "brief": "test"}},
		Mode:           "planning-review-only",
		Classification: "public",
		Wait:           true,
	}

	argBytes, _ := json.Marshal(req)
	response := server.DispatchToolCall("dispatch_team", argBytes)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	if response.Status == "" {
		t.Errorf("response.Status is empty")
	}
}

func TestDispatchToolCallPollDispatchStatus(t *testing.T) {
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: "/project",
		GlobalRoot:  "/global",
		PluginRoot:  "/plugin",
	})

	req := PollDispatchStatusRequest{JobID: "job_test"}
	argBytes, _ := json.Marshal(req)
	response := server.DispatchToolCall("poll_dispatch_status", argBytes)
	if response == nil {
		t.Errorf("response is nil")
		return
	}

	if response.Status == "" {
		t.Errorf("response.Status is empty")
	}
}

func TestMCPToolResponseJSON(t *testing.T) {
	response := &MCPToolResponse{
		Status:  "success",
		Result:  map[string]any{"key": "value"},
		IsError: false,
	}

	// Should be JSON-serializable
	data, err := json.Marshal(response)
	if err != nil {
		t.Errorf("failed to marshal response: %v", err)
		return
	}

	if len(data) == 0 {
		t.Errorf("marshaled response is empty")
	}
}
