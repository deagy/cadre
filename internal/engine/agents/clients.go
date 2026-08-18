package agents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/engine/a2a"
)

// DefaultAnthropicModel is what an Anthropic client uses unless told otherwise.
//
// The Python pinned claude-sonnet-4-5, which is a generation behind. This
// repository's own guidance is to default to the latest and most capable
// model, so the port moves it rather than carrying the stale pin forward.
const DefaultAnthropicModel = "claude-sonnet-5"

// anthropicVersion is the API version header Anthropic requires on every call.
const anthropicVersion = "2023-06-01"

// requestTimeout bounds a model call.
//
// A dispatched agent that never returns would hold a gate open indefinitely,
// and the run has no way to tell that from a slow model.
const requestTimeout = 120 * time.Second

func httpClientOr(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: requestTimeout}
}

// AnthropicClient dispatches an agent to the Anthropic Messages API.
//
// Hand-rolled rather than built on an SDK: this is one POST with a forced tool
// choice, and a dependency would buy nothing a repository shipping static
// binaries wants to carry.
type AnthropicClient struct {
	Model   string
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

// Complete asks the model for a structured contribution.
func (c AnthropicClient) Complete(request CompletionRequest) (AgentContribution, error) {
	model := c.Model
	if model == "" {
		model = DefaultAnthropicModel
	}
	apiKey := c.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return AgentContribution{}, fmt.Errorf("no Anthropic API key: set ANTHROPIC_API_KEY or configure one")
	}

	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("ANTHROPIC_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	if err := a2a.RequireHTTPSOrLocal(baseURL, "ANTHROPIC_BASE_URL"); err != nil {
		return AgentContribution{}, err
	}

	body, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"system":     request.RolePrompt,
		"tools": []any{map[string]any{
			"name":         SubmitContributionToolName,
			"description":  SubmitContributionDescription,
			"input_schema": SubmitContributionSchema(),
		}},
		"tool_choice": map[string]any{"type": "tool", "name": SubmitContributionToolName},
		"messages":    []any{map[string]any{"role": "user", "content": request.TaskText}},
	})
	if err != nil {
		return AgentContribution{}, err
	}

	httpRequest, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return AgentContribution{}, err
	}
	httpRequest.Header.Set("content-type", "application/json")
	httpRequest.Header.Set("x-api-key", apiKey)
	httpRequest.Header.Set("anthropic-version", anthropicVersion)

	payload, err := callToolAPI(httpClientOr(c.HTTP), httpRequest, extractAnthropicToolInput)
	if err != nil {
		return AgentContribution{}, err
	}
	return contributionFrom(payload, request), nil
}

// OpenAICompatibleClient dispatches to any OpenAI-shaped chat-completions API.
//
// Client-side only: nothing in this repository serves such an API.
type OpenAICompatibleClient struct {
	Model   string
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

// Complete asks the model for a structured contribution.
func (c OpenAICompatibleClient) Complete(request CompletionRequest) (AgentContribution, error) {
	if c.Model == "" {
		return AgentContribution{}, fmt.Errorf("an OpenAI-compatible client needs a model")
	}
	apiKey := c.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	// The same guard the A2A client uses, for the same reason: a plain-http
	// base URL would send the role prompt, the task text and the credential
	// in cleartext.
	if err := a2a.RequireHTTPSOrLocal(baseURL, "OPENAI_BASE_URL"); err != nil {
		return AgentContribution{}, err
	}

	body, err := json.Marshal(map[string]any{
		"model": c.Model,
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        SubmitContributionToolName,
				"description": SubmitContributionDescription,
				"parameters":  SubmitContributionSchema(),
			},
		}},
		"tool_choice": map[string]any{
			"type":     "function",
			"function": map[string]any{"name": SubmitContributionToolName},
		},
		"messages": []any{
			map[string]any{"role": "system", "content": request.RolePrompt},
			map[string]any{"role": "user", "content": request.TaskText},
		},
	})
	if err != nil {
		return AgentContribution{}, err
	}

	httpRequest, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return AgentContribution{}, err
	}
	httpRequest.Header.Set("content-type", "application/json")
	if apiKey != "" {
		httpRequest.Header.Set("authorization", "Bearer "+apiKey)
	}

	payload, err := callToolAPI(httpClientOr(c.HTTP), httpRequest, extractOpenAIToolArguments)
	if err != nil {
		return AgentContribution{}, err
	}
	return contributionFrom(payload, request), nil
}

// callToolAPI performs the request and extracts the tool payload.
func callToolAPI(client *http.Client, request *http.Request, extract func([]byte) map[string]any) (map[string]any, error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		// The body can carry the caller's own prompt back, so only the status
		// and a short detail are surfaced.
		detail := strings.TrimSpace(string(raw))
		if len(detail) > 200 {
			detail = detail[:200] + "…"
		}
		return nil, fmt.Errorf("model API returned %s: %s", response.Status, detail)
	}
	return extract(raw), nil
}

// extractAnthropicToolInput pulls the submit_contribution tool block.
func extractAnthropicToolInput(raw []byte) map[string]any {
	var response struct {
		Content []struct {
			Type  string         `json:"type"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil
	}
	for _, block := range response.Content {
		if block.Type == "tool_use" && block.Name == SubmitContributionToolName {
			return block.Input
		}
	}
	return nil
}

// extractOpenAIToolArguments pulls and parses the tool call's arguments.
//
// The arguments arrive as a JSON *string*, so they are parsed rather than
// decoded in place; a malformed one yields no payload and the defaults apply,
// which is the Python's behaviour and better than failing a dispatch on a
// model's formatting.
func extractOpenAIToolArguments(raw []byte) map[string]any {
	var response struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || len(response.Choices) == 0 {
		return nil
	}
	for _, call := range response.Choices[0].Message.ToolCalls {
		if call.Function.Name != SubmitContributionToolName {
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &parsed); err != nil {
			return nil
		}
		return parsed
	}
	return nil
}

// contributionFrom applies the same defaults both real clients apply.
func contributionFrom(payload map[string]any, request CompletionRequest) AgentContribution {
	text := func(key string) string {
		value, _ := payload[key].(string)
		return value
	}
	contribution := AgentContribution{
		ArtifactID: text("artifact_id"),
		Revision:   text("revision"),
		Summary:    text("summary"),
	}
	if contribution.ArtifactID == "" {
		contribution.ArtifactID = request.GateID + "-" + request.AgentID + "-artifact"
	}
	if contribution.Revision == "" {
		contribution.Revision = "rev-1"
	}
	if blocking, ok := payload["blocking_question"].(string); ok && blocking != "" {
		contribution.BlockingQuestion = &blocking
	}
	return contribution
}

// A2AModelClient dispatches an agent to an external A2A-reachable agent.
type A2AModelClient struct {
	Endpoint string
	HTTP     *http.Client

	client *a2a.Client
}

// Complete sends the task to the external agent and reads its reply.
func (c *A2AModelClient) Complete(request CompletionRequest) (AgentContribution, error) {
	if c.client == nil {
		client, err := a2a.NewClient(c.Endpoint, c.HTTP)
		if err != nil {
			return AgentContribution{}, err
		}
		c.client = client
	}

	task, err := c.client.SendMessage(request.TaskText, "", map[string]any{
		"agent_id": request.AgentID, "kind": request.Kind, "gate_id": request.GateID,
		"role_prompt": request.RolePrompt,
	})
	if err != nil {
		return AgentContribution{}, err
	}

	summary := ""
	for _, message := range task.History {
		for _, part := range message.Parts {
			if part.Text != "" {
				summary = part.Text
			}
		}
	}
	return contributionFrom(map[string]any{"summary": summary}, request), nil
}

// DispatchingClient routes each agent to a local or external client.
//
// This is the one place the local-versus-external decision is made, so the
// executor can hold a single client for every node and still support a mix.
type DispatchingClient struct {
	Default      ModelClient
	AgentCatalog map[string]map[string]any

	external map[string]*A2AModelClient
}

// Complete routes by the catalog's transport field.
func (d *DispatchingClient) Complete(request CompletionRequest) (AgentContribution, error) {
	entry := d.AgentCatalog[request.AgentID]
	transport, _ := entry["transport"].(string)
	if transport != "a2a" {
		if d.Default == nil {
			return AgentContribution{}, fmt.Errorf("no model client for agent %s", request.AgentID)
		}
		return d.Default.Complete(request)
	}

	endpoint, _ := entry["endpoint"].(string)
	if endpoint == "" {
		return AgentContribution{}, fmt.Errorf("agent %s declares a2a transport with no endpoint", request.AgentID)
	}
	if d.external == nil {
		d.external = map[string]*A2AModelClient{}
	}
	if _, built := d.external[request.AgentID]; !built {
		d.external[request.AgentID] = &A2AModelClient{Endpoint: endpoint}
	}
	return d.external[request.AgentID].Complete(request)
}
