package orchestration

import (
	"encoding/json"
	"strings"
	"testing"
)

// The tool surface is thin -- decode, call gitlab.go, pass the result back --
// so these test the two things that are not: which tools exist at all, and
// how a result is classified for the model.

func TestOnlyTheThreeCreateOnlyToolsAreOffered(t *testing.T) {
	// The invariant this server exists under: nothing it exposes can close,
	// reopen, resolve, or relabel an issue. A tool added here that could
	// would break it silently -- the transport would happily serve it -- so
	// the offered set is pinned by name.
	offered := map[string]bool{}
	for _, definition := range NewGitLabMCPServer("").GetToolDefinitions() {
		offered[definition.Name] = true
	}

	want := []string{"create_review_subtask", "write_wiki_page", "write_evidence_comment"}
	for _, name := range want {
		if !offered[name] {
			t.Errorf("%s is not offered", name)
		}
	}
	if len(offered) != len(want) {
		t.Errorf("offered = %v, want exactly the three create-only tools", offered)
	}
}

func TestEveryToolDeclaresItsRequiredArguments(t *testing.T) {
	// An inputSchema with no required list lets a client call a tool with
	// nothing at all, which reaches gitlab.go as an issue iid of 0.
	for _, definition := range NewGitLabMCPServer("").GetToolDefinitions() {
		required, ok := definition.Schema["required"].([]string)
		if !ok || len(required) == 0 {
			t.Errorf("%s declares no required arguments", definition.Name)
		}
		properties, ok := definition.Schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s declares no properties", definition.Name)
		}
		for _, name := range required {
			if _, present := properties[name]; !present {
				t.Errorf("%s requires %q but does not describe it", definition.Name, name)
			}
		}
	}
}

func TestAnUnknownToolIsRefused(t *testing.T) {
	// Notably including the ones this server must never grow: a client that
	// asks for close_issue gets a refusal, not a silent no-op it could read
	// as success.
	for _, name := range []string{"close_issue", "resolve_issue", ""} {
		response := NewGitLabMCPServer("").DispatchToolCall(name, json.RawMessage(`{}`))
		if !response.IsError {
			t.Errorf("tool %q must be refused", name)
		}
	}
}

func TestMalformedArgumentsAreRefusedRatherThanPartiallyDecoded(t *testing.T) {
	// A partial decode would leave issue_iid at 0 and content empty -- a
	// different write from the one asked for, against an issue nobody named.
	// No HTTP call is made, so this needs no server.
	response := NewGitLabMCPServer("").DispatchToolCall("write_evidence_comment",
		json.RawMessage(`{"issue_iid": "not a number"}`))
	if !response.IsError {
		t.Fatal("undecodable arguments must be refused")
	}
	if !strings.Contains(response.Error, "write_evidence_comment") {
		t.Errorf("the refusal must name the tool: %q", response.Error)
	}
}

func TestConfirmationRequiredIsNotReportedAsAFailure(t *testing.T) {
	// The confirmation gate working is not a fault. Flagging it as one
	// invites a model to treat the human gate as an error to route around,
	// which is the single behaviour the gate exists to prevent.
	response := gitlabToolResult(map[string]any{
		"status": "confirmation_required", "confirmation_token": "t"})
	if response.IsError {
		t.Error("confirmation_required must not be an error")
	}
	if response.Status != "confirmation_required" {
		t.Errorf("status = %q, want the tool's own vocabulary", response.Status)
	}
}

func TestAFailureIsFlaggedWithItsReason(t *testing.T) {
	for _, status := range []string{"denied", "unavailable"} {
		response := gitlabToolResult(map[string]any{"status": status, "reason": "why it failed"})
		if !response.IsError {
			t.Errorf("status %q must be flagged as an error", status)
		}
		if response.Error != "why it failed" {
			t.Errorf("error = %q, want gitlab.go's own reason", response.Error)
		}
	}

	ok := gitlabToolResult(map[string]any{"status": "ok", "created": true})
	if ok.IsError {
		t.Error("a successful result must not be flagged as an error")
	}
}

func TestTheAuditPathIsNotACallerArgument(t *testing.T) {
	// An audit trail a caller can redirect is not an audit trail. The path
	// comes from the process, so no tool schema may accept one.
	for _, definition := range NewGitLabMCPServer("").GetToolDefinitions() {
		properties, _ := definition.Schema["properties"].(map[string]any)
		for name := range properties {
			if strings.Contains(name, "audit") {
				t.Errorf("%s accepts %q as an argument", definition.Name, name)
			}
		}
	}
}
