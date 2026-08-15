package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// The dispatch server speaks Model Context Protocol, which is JSON-RPC 2.0.
//
// It previously spoke an invented protocol -- {"type":"initialize"},
// {"type":"call_tool"} -- and answered every real MCP message with
// {"type":"error","error":"unknown message type: \"\""}. No client could
// complete a handshake, which made the setup docs/INSTALL.md documents
// (`command = "cadre"`, `args = ["mcp-dispatch-server"]`) fail on its first
// message while the command itself looked healthy.
//
// These pin the framing rather than the tool behaviour: a tool that returns
// the wrong answer is a bug, but a server that cannot be connected to at all
// is invisible to every test that exercises tools directly.

func serve(t *testing.T, requests ...string) []map[string]any {
	t.Helper()
	server := orchestration.NewDispatchMCPServer(orchestration.DispatchMCPServerConfig{
		ProjectRoot: t.TempDir(), GlobalRoot: t.TempDir(), PluginRoot: t.TempDir(),
	})
	var out strings.Builder
	_ = runMCPServer(
		context.Background(), "cadre-dispatch", server,
		strings.NewReader(strings.Join(requests, "\n")+"\n"),
		&out, io.Discard,
	)

	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("server emitted a line that is not JSON: %q", line)
		}
		responses = append(responses, decoded)
	}
	return responses
}

func TestInitializeCompletesTheMCPHandshake(t *testing.T) {
	responses := serve(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`)
	if len(responses) != 1 {
		t.Fatalf("got %d responses, want 1", len(responses))
	}

	response := responses[0]
	if response["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", response["jsonrpc"])
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result object: %v", response)
	}
	// Echoed, not pinned: the client chooses the version, and a server that
	// insists on its own fails handshakes for no reason.
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %v, want the client's own", result["protocolVersion"])
	}
	if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
		t.Errorf("capabilities must advertise tools: %v", result["capabilities"])
	}
	if _, ok := result["serverInfo"].(map[string]any); !ok {
		t.Errorf("no serverInfo: %v", result)
	}
}

func TestNotificationsGetNoReply(t *testing.T) {
	// A notification has no id. Replying to one is a protocol violation, and
	// every client sends notifications/initialized immediately after the
	// handshake -- so a server that answers it breaks on the first exchange.
	responses := serve(t,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if len(responses) != 1 {
		t.Fatalf("got %d responses, want exactly 1 (the ping)", len(responses))
	}
	if responses[0]["id"] != float64(1) {
		t.Errorf("the surviving response is not the ping: %v", responses[0])
	}
}

func TestToolsListUsesInputSchema(t *testing.T) {
	responses := serve(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	result := responses[0]["result"].(map[string]any)
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("no tools listed: %v", result)
	}
	for _, entry := range tools {
		tool := entry.(map[string]any)
		// MCP names this inputSchema. A server emitting "schema" lists tools
		// that every client sees as taking no arguments.
		if _, ok := tool["inputSchema"]; !ok {
			t.Errorf("tool %v has no inputSchema", tool["name"])
		}
		if tool["description"] == "" {
			t.Errorf("tool %v has no description", tool["name"])
		}
	}
}

func TestToolFailureIsNotAJSONRPCError(t *testing.T) {
	// A tool that fails reports isError inside a *successful* response. A
	// JSON-RPC error is for protocol faults; using it for a tool failure
	// hides the detail from the model and looks like a broken server.
	responses := serve(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"poll_dispatch_status","arguments":{"job_id":"missing"}}}`)
	response := responses[0]
	if _, isError := response["error"]; isError {
		t.Fatalf("a tool result must not be a JSON-RPC error: %v", response)
	}
	result := response["result"].(map[string]any)
	if _, ok := result["content"].([]any); !ok {
		t.Errorf("result has no content array: %v", result)
	}
}

func TestUnknownMethodIsMethodNotFound(t *testing.T) {
	responses := serve(t, `{"jsonrpc":"2.0","id":1,"method":"no/such/method"}`)
	failure, ok := responses[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("want a JSON-RPC error: %v", responses[0])
	}
	if failure["code"] != float64(-32601) {
		t.Errorf("code = %v, want -32601 (method not found)", failure["code"])
	}
}

func TestEveryResponseCarriesExactlyOneOfResultOrError(t *testing.T) {
	// `ping` returns an empty result object, which an omitempty-tagged map
	// silently drops -- producing a response with neither result nor error,
	// which is not valid JSON-RPC and which a strict client rejects.
	responses := serve(t,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"no/such/method"}`)
	for _, response := range responses {
		_, hasResult := response["result"]
		_, hasError := response["error"]
		if hasResult == hasError {
			t.Errorf("response must carry exactly one of result/error: %v", response)
		}
	}
}

func TestAMalformedFrameEndsTheSessionRatherThanSpinning(t *testing.T) {
	// json.Decoder does not advance past a syntax error, so reporting the
	// error and continuing re-reads the same bytes forever. That is not
	// hypothetical: it emitted 269 MB of identical parse errors from a single
	// malformed line before this was fixed -- a denial of service triggered
	// by one bad frame.
	responses := serve(t,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{not json}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`)

	if len(responses) != 2 {
		t.Fatalf("got %d responses, want 2 (the ping, then one parse error)", len(responses))
	}
	failure, ok := responses[1]["error"].(map[string]any)
	if !ok {
		t.Fatalf("second response is not an error: %v", responses[1])
	}
	if failure["code"] != float64(-32700) {
		t.Errorf("code = %v, want -32700 (parse error)", failure["code"])
	}
}
