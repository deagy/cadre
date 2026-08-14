package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// MCPDispatchServerCmd implements the mcp-dispatch-server CLI command
// Starts a Go MCP dispatch server on stdio, replacing Python dispatch_server.py
// Exposes 4 dispatch tools: dispatch_secure_cloud_role, dispatch_team, poll_dispatch_status, poll_team_status
func MCPDispatchServerCmd(args []string, stdout, stderr io.Writer) int {
	ctx := context.Background()

	// Parse command-line flags
	config := orchestration.DispatchMCPServerConfig{
		ProjectRoot: os.Getenv("CADRE_PROJECT_ROOT"),
		GlobalRoot:  os.Getenv("CADRE_GLOBAL_ROOT"),
		PluginRoot:  os.Getenv("CADRE_PLUGIN_ROOT"),
	}

	// Defaults for local development
	if config.ProjectRoot == "" {
		config.ProjectRoot = "."
	}
	if config.GlobalRoot == "" {
		config.GlobalRoot = os.Getenv("HOME") + "/.config/cadre"
	}
	if config.PluginRoot == "" {
		config.PluginRoot = os.Getenv("HOME") + "/.claude/agents"
	}

	// Parse command-line arguments
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			fmt.Fprintf(stdout, "usage: cadre mcp-dispatch-server [options]\n\n")
			fmt.Fprintf(stdout, "Run the MCP dispatch server on stdio, exposing dispatch tools to MCP clients.\n\n")
			fmt.Fprintf(stdout, "Tools exposed:\n")
			fmt.Fprintf(stdout, "  dispatch_secure_cloud_role  - Dispatch a secure cloud role with confirmation\n")
			fmt.Fprintf(stdout, "  dispatch_team               - Dispatch multiple roles as a team\n")
			fmt.Fprintf(stdout, "  poll_dispatch_status        - Poll async dispatch job status\n")
			fmt.Fprintf(stdout, "  poll_team_status            - Poll team dispatch status\n\n")
			fmt.Fprintf(stdout, "Environment variables:\n")
			fmt.Fprintf(stdout, "  CADRE_PROJECT_ROOT     Project root directory (default: .)\n")
			fmt.Fprintf(stdout, "  CADRE_GLOBAL_ROOT      Global config directory (default: ~/.config/cadre)\n")
			fmt.Fprintf(stdout, "  CADRE_PLUGIN_ROOT      Plugin directory (default: ~/.claude/agents)\n")
			return 0

		case "--version":
			version, err := CLIVersion(".")
			if err != nil {
				fmt.Fprintf(stderr, "cadre: failed to get version: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "cadre mcp-dispatch-server %s\n", version)
			return 0
		}
	}

	// Create and validate server
	server := orchestration.NewDispatchMCPServer(config)
	if err := server.ValidateConfig(); err != nil {
		fmt.Fprintf(stderr, "cadre: invalid server configuration: %v\n", err)
		return 1
	}

	// Run the MCP server loop on stdio
	if err := runMCPDispatchServer(ctx, server, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(stderr, "cadre: mcp-dispatch-server error: %v\n", err)
		return 1
	}

	return 0
}

// runMCPDispatchServer runs the MCP dispatch server on stdio
// Reads MCP protocol messages from stdin, dispatches tool calls, writes responses to stdout
func runMCPDispatchServer(
	ctx context.Context,
	server *orchestration.DispatchMCPServer,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	decoder := json.NewDecoder(stdin)

	for {
		// Read MCP message
		var message map[string]any
		if err := decoder.Decode(&message); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read MCP message: %v", err)
		}

		// Process message based on type
		msgType, _ := message["type"].(string)

		switch msgType {
		case "initialize":
			// Respond with server capabilities
			response := map[string]any{
				"type": "initialize_response",
				"capabilities": map[string]any{
					"tools": server.GetToolDefinitions(),
				},
			}
			if err := json.NewEncoder(stdout).Encode(response); err != nil {
				return fmt.Errorf("failed to write initialize response: %v", err)
			}

		case "call_tool":
			// Handle tool call
			toolName, _ := message["name"].(string)
			arguments, _ := message["arguments"].(json.RawMessage)

			result := server.DispatchToolCall(toolName, arguments)

			// Write response
			response := map[string]any{
				"type":   "call_tool_response",
				"result": result,
			}

			if err := json.NewEncoder(stdout).Encode(response); err != nil {
				return fmt.Errorf("failed to write tool response: %v", err)
			}

		case "close":
			// Server close request
			response := map[string]any{
				"type": "close_response",
			}
			_ = json.NewEncoder(stdout).Encode(response)
			return nil

		default:
			// Unknown message type - respond with error
			response := map[string]any{
				"type":  "error",
				"error": fmt.Sprintf("unknown message type: %q", msgType),
			}
			_ = json.NewEncoder(stdout).Encode(response)
		}
	}

	return nil
}
