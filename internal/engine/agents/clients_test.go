package agents

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureServer records the request it received and replies with a fixed body.
func captureServer(t *testing.T, reply any, captured *map[string]any, headers *http.Header) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, captured)
		if headers != nil {
			*headers = r.Header.Clone()
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(server.Close)
	return server
}

// The Anthropic request must force the tool, so a model cannot answer in prose
// that then has to be parsed.
func TestAnthropicClientForcesTheContributionTool(t *testing.T) {
	var sent map[string]any
	var headers http.Header
	server := captureServer(t, map[string]any{
		"content": []any{map[string]any{
			"type": "tool_use", "name": SubmitContributionToolName,
			"input": map[string]any{"artifact_id": "a-1", "revision": "rev-9", "summary": "did it"},
		}},
	}, &sent, &headers)

	client := AnthropicClient{APIKey: "test-key", BaseURL: server.URL, HTTP: server.Client()}
	contribution, err := client.Complete(CompletionRequest{
		AgentID: "backend-engineer", Kind: "author", GateID: "G3",
		RolePrompt: "be an engineer", TaskText: "do the thing",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if contribution.ArtifactID != "a-1" || contribution.Revision != "rev-9" || contribution.Summary != "did it" {
		t.Errorf("contribution = %+v", contribution)
	}
	if sent["model"] != DefaultAnthropicModel {
		t.Errorf("model = %v, want the current default %q", sent["model"], DefaultAnthropicModel)
	}
	if sent["system"] != "be an engineer" {
		t.Errorf("the role prompt was not sent as the system prompt: %v", sent["system"])
	}
	choice, _ := sent["tool_choice"].(map[string]any)
	if choice["type"] != "tool" || choice["name"] != SubmitContributionToolName {
		t.Errorf("tool_choice = %v, want the contribution tool forced", choice)
	}
	if headers.Get("x-api-key") != "test-key" || headers.Get("anthropic-version") == "" {
		t.Errorf("headers missing the key or version: %v", headers)
	}
}

// The OpenAI-compatible client parses arguments that arrive as a JSON string.
func TestOpenAIClientParsesToolArguments(t *testing.T) {
	var sent map[string]any
	server := captureServer(t, map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{
			"tool_calls": []any{map[string]any{"function": map[string]any{
				"name":      SubmitContributionToolName,
				"arguments": `{"artifact_id":"o-1","revision":"rev-2","summary":"ok","blocking_question":"which one?"}`,
			}}},
		}}},
	}, &sent, nil)

	client := OpenAICompatibleClient{Model: "some-model", APIKey: "k", BaseURL: server.URL, HTTP: server.Client()}
	contribution, err := client.Complete(CompletionRequest{AgentID: "a", Kind: "author", GateID: "G1"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if contribution.ArtifactID != "o-1" || contribution.Summary != "ok" {
		t.Errorf("contribution = %+v", contribution)
	}
	if contribution.BlockingQuestion == nil || *contribution.BlockingQuestion != "which one?" {
		t.Errorf("blocking question = %v", contribution.BlockingQuestion)
	}
}

// A model returning malformed arguments must not fail the dispatch: the
// defaults apply, matching the Python.
func TestMalformedToolArgumentsFallBackToDefaults(t *testing.T) {
	var sent map[string]any
	server := captureServer(t, map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{
			"tool_calls": []any{map[string]any{"function": map[string]any{
				"name": SubmitContributionToolName, "arguments": "{not json",
			}}},
		}}},
	}, &sent, nil)

	client := OpenAICompatibleClient{Model: "m", APIKey: "k", BaseURL: server.URL, HTTP: server.Client()}
	contribution, err := client.Complete(CompletionRequest{AgentID: "a", Kind: "author", GateID: "G1"})
	if err != nil {
		t.Fatalf("malformed arguments failed the dispatch: %v", err)
	}
	if contribution.ArtifactID != "G1-a-artifact" || contribution.Revision != "rev-1" {
		t.Errorf("contribution = %+v, want the generated defaults", contribution)
	}
}

// Both clients refuse a plain-http base URL, for the same reason the A2A
// client does: the role prompt, the task text and the credential would go in
// cleartext.
func TestClientsRefuseAPlainHTTPBaseURL(t *testing.T) {
	// Asserting merely "an error" proves nothing here: without the guard the
	// client still fails, on DNS, and the test passes while the request has
	// already been sent. The error has to name the refusal.
	anthropic := AnthropicClient{APIKey: "k", BaseURL: "http://models.example.com"}
	_, err := anthropic.Complete(CompletionRequest{})
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Errorf("the Anthropic client gave %v, want a refusal before sending", err)
	}
	openai := OpenAICompatibleClient{Model: "m", APIKey: "k", BaseURL: "http://models.example.com"}
	_, err = openai.Complete(CompletionRequest{})
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Errorf("the OpenAI-compatible client gave %v, want a refusal before sending", err)
	}
}

// An API error must not echo the request body back, which carries the prompt.
func TestAModelErrorDoesNotEchoTheWholeBody(t *testing.T) {
	secret := strings.Repeat("prompt-content ", 50)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(secret))
	}))
	t.Cleanup(server.Close)

	client := AnthropicClient{APIKey: "k", BaseURL: server.URL, HTTP: server.Client()}
	_, err := client.Complete(CompletionRequest{})
	if err == nil {
		t.Fatal("a 400 was treated as success")
	}
	if len(err.Error()) > 300 {
		t.Errorf("the error carries %d characters; it should be truncated", len(err.Error()))
	}
}

// The dispatcher is the one place the local-versus-external choice is made.
func TestDispatchingClientRoutesByTransport(t *testing.T) {
	dispatcher := &DispatchingClient{
		Default: FakeModelClient{},
		AgentCatalog: map[string]map[string]any{
			"local-agent":    {"kind": "author"},
			"external-agent": {"kind": "author", "transport": "a2a", "endpoint": "https://agent.example.com"},
		},
	}

	local, err := dispatcher.Complete(CompletionRequest{AgentID: "local-agent", Kind: "author", GateID: "G1"})
	if err != nil {
		t.Fatalf("a local agent failed: %v", err)
	}
	if local.Summary == "" {
		t.Error("the local agent did not reach the default client")
	}

	// An agent absent from the catalog is local too.
	if _, err := dispatcher.Complete(CompletionRequest{AgentID: "unknown", Kind: "author", GateID: "G1"}); err != nil {
		t.Errorf("an uncatalogued agent did not fall back to the default client: %v", err)
	}

	// An a2a entry with no endpoint is a configuration error, not a silent
	// fallback to the local model -- dispatching an external agent locally
	// would run it under the wrong identity entirely.
	dispatcher.AgentCatalog["broken"] = map[string]any{"transport": "a2a"}
	if _, err := dispatcher.Complete(CompletionRequest{AgentID: "broken"}); err == nil {
		t.Error("an a2a agent with no endpoint silently ran on the default client")
	}
}

// The default model must not be one that has been superseded.
//
// A denylist rather than an assertion of what is current: "is this the latest
// model" cannot be answered offline, and a test pinning the expected id would
// simply restate the constant. This only ever grows, and it fails in the
// direction that matters -- the Python's default sat at claude-sonnet-4-5, a
// generation behind, because nothing ever said so.
func TestTheDefaultModelIsNotASupersededOne(t *testing.T) {
	superseded := map[string]string{
		"claude-sonnet-4-5": "superseded by the Claude 5 family",
		"claude-3-5-sonnet": "superseded",
		"claude-3-opus":     "superseded",
		"claude-2":          "superseded",
		"claude-instant-1":  "superseded",
	}
	if reason, stale := superseded[DefaultAnthropicModel]; stale {
		t.Errorf("DefaultAnthropicModel is %q, which is %s", DefaultAnthropicModel, reason)
	}
	if DefaultAnthropicModel == "" {
		t.Error("there is no default model")
	}
}
