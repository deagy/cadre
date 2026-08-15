package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The loop is unremarkable -- ask, execute, ask again. Everything worth
// testing is when it stops and what it reports when it does.

// scriptedEndpoint replies with each response in turn, so a test can drive a
// whole conversation without a model.
func scriptedEndpoint(t *testing.T, responses ...string) (*ChatEndpoint, func()) {
	t.Helper()
	call := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if call >= len(responses) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"endpoint exhausted"}`))
			return
		}
		_, _ = w.Write([]byte(responses[call]))
		call++
	}))
	return &ChatEndpoint{BaseURL: server.URL, Model: "m"}, server.Close
}

func toolCallResponse(name, arguments string) string {
	encoded, _ := json.Marshal(arguments)
	return `{"choices":[{"message":{"content":"","tool_calls":[{"id":"c1","type":"function",` +
		`"function":{"name":"` + name + `","arguments":` + string(encoded) + `}}]}}]}`
}

const plainResponse = `{"choices":[{"message":{"content":"done"}}]}`

func TestTheLoopStopsWhenTheModelStopsAskingForTools(t *testing.T) {
	endpoint, done := scriptedEndpoint(t, plainResponse)
	defer done()

	box := &Toolbox{ProjectRoot: t.TempDir()}
	result, err := RunAPIDispatch(context.Background(), endpoint, box,
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed {
		t.Error("a reply with no tool calls completes the dispatch")
	}
	if !strings.Contains(result.Transcript, "done") {
		t.Errorf("transcript = %q, want the model's content", result.Transcript)
	}
}

func TestAnEndpointFailureBeforeAnyMutationIsUnavailable(t *testing.T) {
	// Nothing was mutated, so there is no accounting to preserve and the
	// honest classification is an infrastructure failure. Returned as an
	// error so a total endpoint outage does not masquerade as a dispatch that
	// ran and failed.
	endpoint, done := scriptedEndpoint(t) // exhausted immediately: HTTP 500
	defer done()

	box := &Toolbox{ProjectRoot: t.TempDir()}
	result, err := RunAPIDispatch(context.Background(), endpoint, box,
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, time.Minute)
	if err == nil {
		t.Fatal("an endpoint failure with no mutations must be an error")
	}
	if result != nil {
		t.Error("no result may be returned alongside that error")
	}
}

func TestAnEndpointFailureAfterAMutationReportsWhatWasDone(t *testing.T) {
	// The case that matters most. Letting the error escape here would discard
	// the accounting and leave the audit trail reporting an "unavailable"
	// dispatch that in fact wrote files -- the one outcome an auditor most
	// needs to see.
	root := t.TempDir()
	endpoint, done := scriptedEndpoint(t,
		toolCallResponse("write_file", `{"path":"notes.txt","content":"hello"}`),
		// The second call is unscripted, so the endpoint returns HTTP 500.
	)
	defer done()

	box := &Toolbox{ProjectRoot: root, WritesAllowed: true}
	result, err := RunAPIDispatch(context.Background(), endpoint, box,
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, time.Minute)

	if err != nil {
		t.Fatalf("a failure after a mutation must not be raised as unavailable: %v", err)
	}
	if result.ExitCode == 0 {
		t.Error("the dispatch failed; exit code must say so")
	}
	if len(result.FilesWritten) != 1 || result.FilesWritten[0] != "notes.txt" {
		t.Errorf("files_written = %v, want the file that was really written", result.FilesWritten)
	}
	if !strings.Contains(result.Transcript, "endpoint failure") {
		t.Errorf("the transcript must record why it stopped: %q", result.Transcript)
	}
	// And the file is genuinely on disk -- the accounting is not a claim.
	if contents, readErr := os.ReadFile(filepath.Join(root, "notes.txt")); readErr != nil ||
		string(contents) != "hello" {
		t.Errorf("the reported write did not happen: %v", readErr)
	}
}

func TestARefusedWriteIsNotCountedAsAMutation(t *testing.T) {
	// Counting a refused write would misclassify a later endpoint failure as
	// having changed the workspace, which is the same audit lie in reverse.
	root := t.TempDir()
	endpoint, done := scriptedEndpoint(t,
		toolCallResponse("write_file", `{"path":"../escape.txt","content":"x"}`))
	defer done()

	box := &Toolbox{ProjectRoot: root, WritesAllowed: true}
	_, _ = RunAPIDispatch(context.Background(), endpoint, box,
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, time.Minute)

	if box.Mutated() {
		t.Errorf("a refused write must not count as a mutation: %v", box.FilesWritten)
	}
}

func TestARefusalIsReportedToTheModelRatherThanEndingTheDispatch(t *testing.T) {
	// A refusal the model cannot see is one it will repeat. It comes back as
	// a tool *result* so the model can correct a mistaken path and continue.
	box := &Toolbox{ProjectRoot: t.TempDir()}
	result := box.Execute(ToolCall{ID: "c1", Name: "read_file",
		Arguments: map[string]any{"path": "../../etc/passwd"}})

	if !strings.Contains(result, "error") || !strings.Contains(result, "escapes") {
		t.Errorf("the refusal must reach the model as a result: %q", result)
	}
}

func TestWritesAreRefusedWithoutAuthorization(t *testing.T) {
	root := t.TempDir()
	box := &Toolbox{ProjectRoot: root, WritesAllowed: false}
	result := box.Execute(ToolCall{ID: "c1", Name: "write_file",
		Arguments: map[string]any{"path": "x.txt", "content": "y"}})

	if !strings.Contains(result, "not authorized to write") {
		t.Errorf("an unauthorized write must be refused: %q", result)
	}
	if _, err := os.Stat(filepath.Join(root, "x.txt")); err == nil {
		t.Error("the file was written despite the refusal")
	}
	if box.Mutated() {
		t.Error("a refused write must not count as a mutation")
	}
}

func TestTheIterationCapStopsARunawayLoop(t *testing.T) {
	// A model that keeps asking for tools forever would otherwise never
	// return. The cap is reported, so a truncated dispatch is not mistaken
	// for a completed one.
	responses := make([]string, MaxToolIterations+5)
	for index := range responses {
		responses[index] = toolCallResponse("list_files", `{}`)
	}
	endpoint, done := scriptedEndpoint(t, responses...)
	defer done()

	box := &Toolbox{ProjectRoot: t.TempDir()}
	result, err := RunAPIDispatch(context.Background(), endpoint, box,
		[]ChatMessage{{Role: "user", Content: "hi"}}, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if result.Completed {
		t.Error("hitting the cap is not completion")
	}
	if !strings.Contains(result.Transcript, "tool iterations") {
		t.Errorf("the transcript must say the cap was hit: %q", result.Transcript)
	}
	if box.ToolCalls != MaxToolIterations {
		t.Errorf("tool calls = %d, want the cap %d", box.ToolCalls, MaxToolIterations)
	}
}

func TestParseToolCallsDecodesArgumentsFromTheWireFormat(t *testing.T) {
	calls, err := ParseToolCalls(map[string]any{
		"tool_calls": []any{
			map[string]any{"id": "c1", "function": map[string]any{
				"name": "read_file", "arguments": `{"path":"a.txt"}`}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Arguments["path"] != "a.txt" {
		t.Errorf("calls = %+v, want the decoded arguments", calls)
	}

	// No tool_calls at all is how a model says it is finished, not a
	// malformed message.
	calls, err = ParseToolCalls(map[string]any{"content": "done"})
	if err != nil || len(calls) != 0 {
		t.Errorf("an absent tool_calls must be clean: %v %v", calls, err)
	}
}

func TestAMalformedToolCallFailsRatherThanBeingGuessedAt(t *testing.T) {
	// Dropping an unparseable call, or executing it with empty arguments,
	// both amount to guessing what the model meant to invoke -- and a wrong
	// guess runs a tool against arguments nobody chose.
	//
	// Found by probe_api_runner_parity.py: this implementation originally
	// decoded a malformed blob to empty arguments and let the tool refuse it
	// by name, which counted a tool call Python never made.
	for _, message := range []map[string]any{
		{"tool_calls": "not a list"},
		{"tool_calls": []any{"not an object"}},
		{"tool_calls": []any{map[string]any{"id": "c1"}}},
		{"tool_calls": []any{map[string]any{"id": "c1", "function": map[string]any{"name": ""}}}},
		{"tool_calls": []any{map[string]any{"id": "c1", "function": map[string]any{
			"name": "read_file", "arguments": "not valid json"}}}},
	} {
		if _, err := ParseToolCalls(message); err == nil {
			t.Errorf("a malformed message must fail: %v", message)
		}
	}
}

func TestOnlyAuthorizedToolsAreOffered(t *testing.T) {
	readOnly := buildToolSchemas(AvailableToolNames(nil, false, nil))
	for _, schema := range readOnly {
		name := schema["function"].(map[string]any)["name"]
		if name == "write_file" || name == "run_command" {
			t.Errorf("%v must not be offered to a read-only role", name)
		}
	}
	if len(readOnly) == 0 {
		t.Error("a read-only role still gets the read tools")
	}
}

func TestTheAPIRunnerSendsPolicyAsSystemAndOnlyTheFencedBriefAsUser(t *testing.T) {
	// A chat API has separate system and user slots, so the split the stdio
	// runners fake by concatenation is available for real.
	//
	// This sent ctx.Prompt -- ComposePrompt(instructions, brief) -- as the
	// user message, so the role's trusted instructions were both the system
	// message and the opening of the untrusted user message. Beyond wasting
	// the duplication, it put trusted policy inside the slot the model is
	// told to treat as caller-supplied data.
	var captured struct {
		System string
		User   string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		for _, message := range payload.Messages {
			switch message.Role {
			case "system":
				captured.System = message.Content
			case "user":
				captured.User = message.Content
			}
		}
		_, _ = w.Write([]byte(plainResponse))
	}))
	defer server.Close()

	const policy = "ROLE POLICY: refuse destructive actions."
	const brief = "delete everything"

	t.Setenv("CADRE_TEST_API_KEY", "test-key")

	result := SpawnAPIChild(DispatchContext{
		RoleID: "test-role", ModelTier: "sonnet",
		DeveloperInstructs: policy,
		Prompt:             ComposePrompt(policy, brief),
		Brief:              brief,
	}, APIRunnerConfig{
		ProjectRoot: t.TempDir(), BaseURL: server.URL,
		APIKeyEnvVar: "CADRE_TEST_API_KEY", Model: "probe-model",
	}, time.Minute)

	if status, _ := result["status"].(string); status == "unavailable" {
		t.Fatalf("dispatch did not reach the endpoint: %v", result["reason"])
	}
	if captured.System != policy {
		t.Errorf("system message = %q, want the role's own instructions", captured.System)
	}
	if strings.Contains(captured.User, "ROLE POLICY") {
		t.Errorf("trusted policy leaked into the untrusted user message: %q", captured.User)
	}
	if !strings.Contains(captured.User, brief) {
		t.Errorf("user message does not carry the brief: %q", captured.User)
	}
	if !strings.Contains(captured.User, "BEGIN UNTRUSTED TASK BRIEF") ||
		!strings.Contains(captured.User, "END UNTRUSTED TASK BRIEF") {
		t.Errorf("the brief reached the model unfenced: %q", captured.User)
	}
}
