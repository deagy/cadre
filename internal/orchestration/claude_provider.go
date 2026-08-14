package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// ClaudeProvider implements APIProvider using Anthropic's Claude API.
type ClaudeProvider struct {
	apiKey     string
	httpClient *http.Client
	apiURL     string
}

// ClaudeMessage represents a message in the Claude API format.
type ClaudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ClaudeRequest represents a request to the Claude API.
type ClaudeRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature float32         `json:"temperature"`
	System      string          `json:"system,omitempty"`
	Messages    []ClaudeMessage `json:"messages"`
}

// ClaudeResponse represents a response from the Claude API.
type ClaudeResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// NewClaudeProvider creates a new Claude API provider.
// It reads the API key from ANTHROPIC_API_KEY environment variable.
func NewClaudeProvider() (*ClaudeProvider, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable not set")
	}

	return &ClaudeProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		apiURL:     "https://api.anthropic.com/v1/messages",
	}, nil
}

// Name returns the provider name.
func (c *ClaudeProvider) Name() string {
	return "claude"
}

// CallAgent sends a request to the Claude API.
func (c *ClaudeProvider) CallAgent(ctx context.Context, request *AgentRequest, modelID string, maxTokens int, temperature float32) (string, error) {
	// Build system prompt
	systemPrompt := c.buildSystemPrompt(request)

	// Build user message
	userMessage, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build Claude API request
	apiReq := ClaudeRequest{
		Model:       modelID,
		MaxTokens:   maxTokens,
		Temperature: temperature,
		System:      systemPrompt,
		Messages: []ClaudeMessage{
			{
				Role:    "user",
				Content: string(userMessage),
			},
		},
	}

	// Serialize request
	reqBody, err := json.Marshal(apiReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal API request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	// Execute request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for HTTP errors
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var claudeResp ClaudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return "", fmt.Errorf("failed to parse API response: %w", err)
	}

	// Check for API errors
	if claudeResp.Error != nil {
		return "", fmt.Errorf("API error: %s - %s", claudeResp.Error.Type, claudeResp.Error.Message)
	}

	// Extract text content
	if len(claudeResp.Content) == 0 {
		return "", fmt.Errorf("no content in API response")
	}

	return claudeResp.Content[0].Text, nil
}

// buildSystemPrompt creates the system prompt for the Claude API.
func (c *ClaudeProvider) buildSystemPrompt(request *AgentRequest) string {
	return fmt.Sprintf(`You are a specialist agent performing a software engineering task.

Agent Role: %s
Task ID: %s
Classification: %s

Your task:
%s

Changed Files:
%v

Your response should be a JSON object with the following structure:
{
  "agent_id": "your-agent-id",
  "status": "success" | "failed",
  "findings": ["finding1", "finding2"],
  "output": "detailed analysis or results",
  "exit_code": 0,
  "error": "error message if failed"
}

If you cannot return structured JSON, return your findings as plain text and the orchestrator will parse them as output.
`, request.AgentID, request.TaskID, request.Classification, request.Task, request.ChangedFiles)
}
