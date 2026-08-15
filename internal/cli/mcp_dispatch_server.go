package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
			_, _ = fmt.Fprintf(stdout, "usage: cadre mcp-dispatch-server [options]\n\n")
			_, _ = fmt.Fprintf(stdout, "Run the MCP dispatch server on stdio, exposing dispatch tools to MCP clients.\n\n")
			_, _ = fmt.Fprintf(stdout, "Tools exposed:\n")
			_, _ = fmt.Fprintf(stdout, "  dispatch_secure_cloud_role  - Dispatch a secure cloud role with confirmation\n")
			_, _ = fmt.Fprintf(stdout, "  dispatch_team               - Dispatch multiple roles as a team\n")
			_, _ = fmt.Fprintf(stdout, "  poll_dispatch_status        - Poll async dispatch job status\n")
			_, _ = fmt.Fprintf(stdout, "  poll_team_status            - Poll team dispatch status\n\n")
			_, _ = fmt.Fprintf(stdout, "Environment variables:\n")
			_, _ = fmt.Fprintf(stdout, "  CADRE_PROJECT_ROOT     Project root directory (default: .)\n")
			_, _ = fmt.Fprintf(stdout, "  CADRE_GLOBAL_ROOT      Global config directory (default: ~/.config/cadre)\n")
			_, _ = fmt.Fprintf(stdout, "  CADRE_PLUGIN_ROOT      Plugin directory (default: ~/.claude/agents)\n")
			return 0

		case "--version":
			version, err := CLIVersion(".")
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "cadre: failed to get version: %v\n", err)
				return 1
			}
			_, _ = fmt.Fprintf(stdout, "cadre mcp-dispatch-server %s\n", version)
			return 0
		}
	}

	// Create and validate server
	server := orchestration.NewDispatchMCPServer(config)
	if err := server.ValidateConfig(); err != nil {
		_, _ = fmt.Fprintf(stderr, "cadre: invalid server configuration: %v\n", err)
		return 1
	}

	// Run the MCP server loop on stdio
	if err := runMCPServer(ctx, "cadre-dispatch", server, os.Stdin, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "cadre: mcp-dispatch-server error: %v\n", err)
		return 1
	}

	return 0
}

// mcpToolServer is the whole of what the transport needs from a tool server:
// what it offers, and what happens when one is called. Both stdio servers --
// dispatch and GitLab evidence -- satisfy it, so the protocol is written and
// tested once. Two hand-rolled JSON-RPC loops would be two chances to make
// the framing mistakes documented below.
type mcpToolServer interface {
	GetToolDefinitions() []orchestration.MCPToolDefinition
	DispatchToolCall(name string, args json.RawMessage) *orchestration.MCPToolResponse
}

// runMCPServer serves the Model Context Protocol on stdio.
//
// MCP is JSON-RPC 2.0: every message carries "jsonrpc":"2.0", requests carry
// an "id" and a "method", and notifications carry a method with no id and get
// no reply. The methods a client needs from a tool server are "initialize",
// "tools/list" and "tools/call".
//
// This previously spoke an invented protocol -- {"type":"initialize"},
// {"type":"call_tool"}, {"type":"close"} -- and answered every real MCP
// message with {"type":"error","error":"unknown message type: \"\""}. No MCP
// client could complete a handshake with it, which made the setup
// docs/INSTALL.md documents (`command = "cadre"`, `args =
// ["mcp-dispatch-server"]`) fail on the first message. The Python server it
// replaced used the official SDK and spoke the real protocol.
func runMCPServer(
	_ context.Context,
	serverName string,
	server mcpToolServer,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	decoder := json.NewDecoder(stdin)
	encoder := json.NewEncoder(stdout)

	for {
		var request jsonRPCRequest
		if err := decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// Report the parse error and stop reading.
			//
			// Continuing is not an option: json.Decoder does not advance past
			// a syntax error, so the next Decode fails on the same bytes and
			// the loop spins. A `continue` here emitted 269 MB of identical
			// parse errors from a single malformed line -- a denial of
			// service triggered by one bad frame.
			//
			// Resynchronising a JSON stream is not reliably possible either,
			// so the session ends, which is what a client can actually
			// recover from by reconnecting.
			_ = encoder.Encode(newRPCError(nil, rpcParseError, err.Error()))
			return fmt.Errorf("malformed JSON-RPC frame: %w", err)
		}

		// A notification has no id and takes no reply -- including
		// "notifications/initialized", which every client sends after the
		// handshake. Replying to one is a protocol violation.
		if request.ID == nil {
			continue
		}

		switch request.Method {
		case "initialize":
			// protocolVersion is echoed from the client rather than pinned:
			// the client picks the version, and a server that insists on its
			// own is the kind of mismatch that fails a handshake for no
			// reason.
			version := "2024-11-05"
			if request.Params != nil {
				var params struct {
					ProtocolVersion string `json:"protocolVersion"`
				}
				if json.Unmarshal(request.Params, &params) == nil && params.ProtocolVersion != "" {
					version = params.ProtocolVersion
				}
			}
			if err := encoder.Encode(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      request.ID,
				Result: rpcResult(map[string]any{
					"protocolVersion": version,
					// Tools only. This server exposes no resources or prompts,
					// and advertising capabilities it does not serve invites
					// calls it would have to refuse.
					"capabilities": map[string]any{"tools": map[string]any{}},
					"serverInfo": map[string]any{
						"name":    serverName,
						"version": CLIVersionOrUnknown(),
					},
				}),
			}); err != nil {
				return err
			}

		case "tools/list":
			tools := make([]map[string]any, 0)
			for _, definition := range server.GetToolDefinitions() {
				tools = append(tools, map[string]any{
					"name":        definition.Name,
					"description": definition.Description,
					// MCP names this inputSchema; "schema" is not read by any
					// client, which would leave every tool argument-less.
					"inputSchema": definition.Schema,
				})
			}
			if err := encoder.Encode(jsonRPCResponse{
				JSONRPC: "2.0", ID: request.ID,
				Result: rpcResult(map[string]any{"tools": tools}),
			}); err != nil {
				return err
			}

		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if request.Params != nil {
				if err := json.Unmarshal(request.Params, &params); err != nil {
					if writeErr := encoder.Encode(newRPCError(request.ID, rpcInvalidParams, err.Error())); writeErr != nil {
						return writeErr
					}
					continue
				}
			}
			if params.Arguments == nil {
				params.Arguments = json.RawMessage("{}")
			}

			result := server.DispatchToolCall(params.Name, params.Arguments)

			// A tool that fails reports isError on a *successful* JSON-RPC
			// response. A JSON-RPC error is for protocol faults, and using it
			// for a tool failure hides the detail from the model.
			payload, err := json.Marshal(result.Result)
			if err != nil {
				payload = []byte("{}")
			}
			text := string(payload)
			if result.Error != "" {
				text = result.Error
			}
			if err := encoder.Encode(jsonRPCResponse{
				JSONRPC: "2.0", ID: request.ID,
				Result: rpcResult(map[string]any{
					"content": []map[string]any{{"type": "text", "text": text}},
					"isError": result.IsError,
				}),
			}); err != nil {
				return err
			}

		case "ping":
			if err := encoder.Encode(jsonRPCResponse{
				JSONRPC: "2.0", ID: request.ID, Result: rpcResult(map[string]any{}),
			}); err != nil {
				return err
			}

		default:
			if err := encoder.Encode(newRPCError(request.ID, rpcMethodNotFound,
				fmt.Sprintf("method not found: %s", request.Method))); err != nil {
				return err
			}
		}
	}
}

// JSON-RPC 2.0 framing.

const (
	rpcParseError     = -32700
	rpcInvalidParams  = -32602
	rpcMethodNotFound = -32601
)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	// json.RawMessage, not map[string]any: with omitempty an *empty* result
	// map is dropped, and a response carrying neither result nor error is
	// not valid JSON-RPC. `ping` returns exactly that empty object.
	Result json.RawMessage `json:"result,omitempty"`
	Error  *jsonRPCError   `json:"error,omitempty"`
}

// rpcResult marshals a result payload for jsonRPCResponse. A marshalling
// failure becomes an empty object rather than a dropped field, for the same
// reason.
func rpcResult(value map[string]any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("{}")
	}
	return encoded
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newRPCError(id json.RawMessage, code int, message string) jsonRPCResponse {
	return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &jsonRPCError{Code: code, Message: message}}
}

// CLIVersionOrUnknown reports the version for serverInfo without failing the
// handshake when the marker cannot be read -- a server that refuses to start
// because it cannot name its own version is worse than one that says so.
func CLIVersionOrUnknown() string {
	root, err := FindCadreFile("roster/catalog.yaml")
	if err != nil {
		return "unknown"
	}
	version, err := CLIVersion(filepath.Dir(filepath.Dir(root)))
	if err != nil {
		return "unknown"
	}
	return version
}
