package a2a

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout matches the Python client's.
const DefaultTimeout = 60 * time.Second

var localHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// RequireHTTPSOrLocal refuses a URL that is neither https nor a recognised
// local-dev host.
//
// Shared by the A2A client and the OpenAI-compatible model client so a
// misconfigured plain-http endpoint cannot silently send credentials, role
// prompts or task content in cleartext.
func RequireHTTPSOrLocal(rawURL, label string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%s %q is not a valid URL: %w", label, rawURL, err)
	}
	if parsed.Scheme == "https" || localHosts[parsed.Hostname()] {
		return nil
	}
	return fmt.Errorf("%s %q must use https unless the host is a recognized local-dev host", label, rawURL)
}

// Client talks to one external A2A agent's JSON-RPC endpoint, discovered via
// its agent card.
type Client struct {
	baseURL string
	http    *http.Client
	rpcURL  string
}

// NewClient builds a client for an agent's base URL.
//
// Authentication for the endpoint is not implemented, matching the Python: it
// is a known, owned gap that needs new agent-catalog schema surface first.
func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	if err := RequireHTTPSOrLocal(baseURL, "A2A endpoint"); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}, nil
}

// endpoint resolves the RPC URL from the agent card, once, and pins its origin.
//
// The origin check is the security-relevant half: the card is fetched from the
// endpoint but its `url` field is data, and without this a card could redirect
// every subsequent RPC -- carrying role prompts and task content -- to another
// host entirely.
func (c *Client) endpoint() (string, error) {
	if c.rpcURL != "" {
		return c.rpcURL, nil
	}

	response, err := c.http.Get(c.baseURL + "/.well-known/agent.json")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return "", fmt.Errorf("agent card request to %q returned %s", c.baseURL, response.Status)
	}
	var card struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&card); err != nil {
		return "", fmt.Errorf("agent card from %q is not valid JSON: %w", c.baseURL, err)
	}

	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	rpc, err := url.Parse(card.URL)
	if err != nil {
		return "", fmt.Errorf("agent card url %q is not a valid URL: %w", card.URL, err)
	}
	if base.Scheme != rpc.Scheme || base.Hostname() != rpc.Hostname() || base.Port() != rpc.Port() {
		return "", fmt.Errorf("agent card url %q origin does not match configured endpoint %q",
			card.URL, c.baseURL)
	}

	c.rpcURL = card.URL
	return c.rpcURL, nil
}

func requestID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "0"
	}
	return hex.EncodeToString(raw)
}

func (c *Client) call(method string, params map[string]any) (any, error) {
	endpoint, err := c.endpoint()
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": requestID(), "method": method, "params": params,
	})
	if err != nil {
		return nil, err
	}

	response, err := c.http.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("A2A call %s to %q returned %s", method, c.baseURL, response.Status)
	}

	payload := struct {
		Result any `json:"result"`
		Error  any `json:"error"`
	}{}
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("A2A response from %q is not valid JSON: %w", c.baseURL, err)
	}
	if payload.Error != nil {
		return nil, fmt.Errorf("A2A error from %q: %v", c.baseURL, payload.Error)
	}
	return payload.Result, nil
}

func decodeTask(result any) (Task, error) {
	var task Task
	encoded, err := json.Marshal(result)
	if err != nil {
		return task, err
	}
	if err := json.Unmarshal(encoded, &task); err != nil {
		return task, fmt.Errorf("A2A result is not a task: %w", err)
	}
	return task, nil
}

// SendMessage creates a task and sends it text.
//
// taskID becomes the *new* task's id via metadata, deliberately not via
// Message.taskId: this engine's own A2A server reserves that field for
// continuing an existing task, so setting it here would resume rather than
// create. ContinueTask is the other path.
func (c *Client) SendMessage(text, taskID string, metadata map[string]any) (Task, error) {
	combined := map[string]any{}
	for key, value := range metadata {
		combined[key] = value
	}
	if taskID != "" {
		combined["task_id"] = taskID
	}

	message := NewMessage(NewTextPart(text))
	if len(combined) > 0 {
		message.Metadata = combined
	}

	payload, err := ExcludeNone(message)
	if err != nil {
		return Task{}, err
	}
	result, err := c.call("message/send", map[string]any{"message": payload})
	if err != nil {
		return Task{}, err
	}
	return decodeTask(result)
}

// ContinueTask continues an existing task with a decision, such as a human
// approval payload.
func (c *Client) ContinueTask(taskID string, decision any) (Task, error) {
	message := Message{Role: "user", Parts: []TextPart{}, TaskID: &taskID,
		Metadata: map[string]any{"decision": decision}}

	payload, err := ExcludeNone(message)
	if err != nil {
		return Task{}, err
	}
	result, err := c.call("message/send", map[string]any{"message": payload})
	if err != nil {
		return Task{}, err
	}
	return decodeTask(result)
}

// GetTask polls a task by id.
func (c *Client) GetTask(taskID string) (Task, error) {
	result, err := c.call("tasks/get", map[string]any{"id": taskID})
	if err != nil {
		return Task{}, err
	}
	return decodeTask(result)
}
