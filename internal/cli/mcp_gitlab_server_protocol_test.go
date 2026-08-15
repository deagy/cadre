package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// The same framing the dispatch server needed, over the GitLab tool set.
//
// Worth repeating rather than assuming: the two servers now share one loop,
// but sharing it is exactly the change that could route one server's tools
// through the other's handshake without anything failing to compile.

func serveGitLab(t *testing.T, requests ...string) []map[string]any {
	t.Helper()
	var out strings.Builder
	_ = runMCPServer(
		context.Background(), "cadre-gitlab-evidence",
		orchestration.NewGitLabMCPServer(t.TempDir()+"/audit.jsonl"),
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

func TestTheGitLabServerCompletesAHandshakeAndListsItsTools(t *testing.T) {
	responses := serveGitLab(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)

	// Two requests, one notification: three messages in, two replies out.
	// A reply to the notification is a protocol violation.
	if len(responses) != 2 {
		t.Fatalf("got %d responses, want 2: %v", len(responses), responses)
	}

	result, _ := responses[0]["result"].(map[string]any)
	serverInfo, _ := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "cadre-gitlab-evidence" {
		t.Errorf("serverInfo.name = %v, want this server's own name", serverInfo["name"])
	}

	listed, _ := responses[1]["result"].(map[string]any)
	tools, _ := listed["tools"].([]any)
	names := map[string]bool{}
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		names[tool["name"].(string)] = true
		// MCP reads inputSchema; a tool advertised under "schema" arrives
		// argument-less at every client.
		if _, ok := tool["inputSchema"]; !ok {
			t.Errorf("%v has no inputSchema", tool["name"])
		}
	}
	for _, want := range []string{"create_review_subtask", "write_wiki_page", "write_evidence_comment"} {
		if !names[want] {
			t.Errorf("%s was not listed", want)
		}
	}
	if len(names) != 3 {
		t.Errorf("listed %v, want only the three create-only tools", names)
	}
}

func TestTheGitLabServerReportsAToolFailureAsAResultNotARPCError(t *testing.T) {
	// A JSON-RPC error is for protocol faults. Using one for a tool failure
	// hides the reason from the model, which then cannot tell a missing token
	// from a missing issue.
	//
	// The token is cleared so the call fails at config resolution, before
	// any network access -- otherwise this test would reach a real GitLab on
	// a machine that happens to have one configured.
	t.Setenv("GITLAB_SVC_TOKEN", "")

	responses := serveGitLab(t,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"write_evidence_comment",`+
			`"arguments":{"issue_iid":1,"content":"c","task_id":"T-1"}}}`)

	if len(responses) != 1 {
		t.Fatalf("got %d responses, want 1", len(responses))
	}
	if _, isRPCError := responses[0]["error"]; isRPCError {
		t.Fatalf("a tool failure must not be a JSON-RPC error: %v", responses[0])
	}
	result, _ := responses[0]["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("the failure must be flagged with isError: %v", result)
	}
}
