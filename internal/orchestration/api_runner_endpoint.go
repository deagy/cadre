package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// The chat endpoint the API runner drives.
//
// A port of api_runner.py's ChatEndpoint. Separate from the sandbox in
// api_runner_sandbox.go, which decides what a tool call may do: this decides
// how the model is reached, and its refusals are about the *network* rather
// than the filesystem.

// DefaultRequestTimeout bounds one completion call.
const DefaultRequestTimeout = 120 * time.Second

// APIRunnerError is a failure that makes the runner unavailable, as distinct
// from a tool the model may retry.
type APIRunnerError struct{ message string }

func (e *APIRunnerError) Error() string { return e.message }

func apiRunnerErrorf(format string, args ...any) error {
	return &APIRunnerError{message: fmt.Sprintf(format, args...)}
}

// noRedirectClient never follows a redirect.
//
// A redirect from a configured inference endpoint is never legitimate, and
// following one would let that endpoint move the request -- *and its
// Authorization header* -- to a host the operator never configured. That is
// a credential disclosure initiated by the very party the credential
// authenticates to, so the answer is to refuse rather than to validate the
// new location.
func noRedirectClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ChatEndpoint is an OpenAI-compatible chat-completions endpoint.
type ChatEndpoint struct {
	BaseURL string
	Model   string
	APIKey  string
	Timeout time.Duration
}

// ChatMessage is one turn in the conversation sent to the endpoint.
type ChatMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

// Complete sends one chat-completions request and returns the decoded body.
//
// timeout, when non-zero, overrides this endpoint's default for one call --
// taking the *smaller* of the two, so a per-call deadline can tighten the
// bound but never loosen it past what the operator configured.
func (endpoint *ChatEndpoint) Complete(
	ctx context.Context,
	messages []ChatMessage,
	tools []map[string]any,
	timeout time.Duration,
) (map[string]any, error) {
	effective := endpoint.Timeout
	if effective == 0 {
		effective = DefaultRequestTimeout
	}
	if timeout > 0 && timeout < effective {
		effective = timeout
	}

	payload := map[string]any{
		"model":    endpoint.Model,
		"messages": messages,
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, apiRunnerErrorf("could not encode the request: %s", err)
	}

	url := strings.TrimRight(endpoint.BaseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, apiRunnerErrorf("%s: %s", url, err)
	}
	request.Header.Set("Content-Type", "application/json")
	if endpoint.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
	}

	response, err := noRedirectClient(effective).Do(request)
	if err != nil {
		return nil, apiRunnerErrorf("%s: cannot reach endpoint: %s", url, err)
	}
	defer func() { _ = response.Body.Close() }()

	// A redirect arrives as a response rather than being followed. Reported
	// as a refusal, so the operator sees that their endpoint tried to move
	// the request rather than a confusing decode failure.
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, apiRunnerErrorf(
			"%s: endpoint returned a redirect (HTTP %d) to %q; redirects are never "+
				"followed, because the request and its Authorization header would move "+
				"to a host the operator never configured",
			url, response.StatusCode, response.Header.Get("Location"))
	}

	// One byte over the cap, so a body exactly at the limit still decodes and
	// anything larger is refused rather than silently truncated into invalid
	// JSON.
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxAPIResponseBytes+1))
	if err != nil {
		return nil, apiRunnerErrorf("%s: request failed: %s", url, err)
	}
	if len(raw) > MaxAPIResponseBytes {
		return nil, apiRunnerErrorf("%s: response exceeds the %d-byte cap", url, MaxAPIResponseBytes)
	}

	if response.StatusCode >= 400 {
		detail := strings.TrimSpace(string(raw))
		if len(detail) > 500 {
			detail = detail[:500]
		}
		return nil, apiRunnerErrorf("%s: HTTP %d: %s", url, response.StatusCode, detail)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, apiRunnerErrorf("%s: response was not valid JSON: %s", url, err)
	}
	return decoded, nil
}

// ResolveEndpoint builds the endpoint client from operator settings.
//
// The API key is read from the *environment variable named by*
// runners.api_key_env, never from a settings file. A project-local settings
// file is untrusted, clonable content; a credential read from one would be a
// credential any clone could substitute.
func ResolveEndpoint(baseURL, keyEnvName, model string) (*ChatEndpoint, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, apiRunnerErrorf(
			"runner='api' requires runners.api_base_url " +
				"(SECURE_CLOUD_AGENTS_API_BASE_URL) to be configured")
	}
	endpoint := &ChatEndpoint{
		BaseURL: baseURL,
		Model:   model,
		Timeout: DefaultRequestTimeout,
	}
	if keyEnvName != "" {
		key := os.Getenv(keyEnvName)
		if key == "" {
			return nil, apiRunnerErrorf(
				"runners.api_key_env names %q, but that variable is unset in this "+
					"environment; the key is read from the environment, never from a "+
					"settings file", keyEnvName)
		}
		endpoint.APIKey = key
	}
	return endpoint, nil
}

// ResolveModel is the model this dispatch addresses.
//
// Always an operator setting, never the role wrapper's own model field: that
// holds a vendor identifier a self-hosted endpoint has never heard of, and
// sending it would 404 against exactly the deployments this runner exists to
// serve. A tier with no configured model is a configuration error, reported
// as one.
func ResolveModel(tier, configuredModel, roleID string) (string, error) {
	if configuredModel != "" {
		return configuredModel, nil
	}
	if tier == "" {
		tier = "unknown"
	}
	return "", apiRunnerErrorf(
		"runner='api' has no model configured for the %q tier of role %q; "+
			"set runners.local_model_%s (SECURE_CLOUD_AGENTS_LOCAL_MODEL_%s)",
		tier, roleID, tier, strings.ToUpper(tier))
}
