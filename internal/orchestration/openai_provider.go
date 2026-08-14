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

// OpenAIProvider implements APIProvider using OpenAI's API.
type OpenAIProvider struct {
	apiKey     string
	httpClient *http.Client
	apiURL     string
}

// OpenAIMessage represents a message in the OpenAI API format.
type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIRequest represents a request to the OpenAI API.
type OpenAIRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature float32         `json:"temperature"`
	Messages    []OpenAIMessage `json:"messages"`
}

// OpenAIResponse represents a response from the OpenAI API.
type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// NewOpenAIProvider creates a new OpenAI provider.
// It reads the API key from OPENAI_API_KEY environment variable.
func NewOpenAIProvider() (*OpenAIProvider, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable not set")
	}

	return &OpenAIProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		apiURL:     "https://api.openai.com/v1/chat/completions",
	}, nil
}

// Name returns the provider name.
func (o *OpenAIProvider) Name() string {
	return "openai"
}

// CallAgent sends a request to the OpenAI API.
func (o *OpenAIProvider) CallAgent(ctx context.Context, request *AgentRequest, modelID string, maxTokens int, temperature float32) (string, error) {
	// Build system message
	systemMessage := o.buildSystemMessage(request)

	// Build user message
	userMessage, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build OpenAI API request
	apiReq := OpenAIRequest{
		Model:       modelID,
		MaxTokens:   maxTokens,
		Temperature: temperature,
		Messages: []OpenAIMessage{
			{
				Role:    "system",
				Content: systemMessage,
			},
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
	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", o.apiKey))

	// Execute request
	resp, err := o.httpClient.Do(httpReq)
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
	var openaiResp OpenAIResponse
	if err := json.Unmarshal(respBody, &openaiResp); err != nil {
		return "", fmt.Errorf("failed to parse API response: %w", err)
	}

	// Check for API errors
	if openaiResp.Error != nil {
		return "", fmt.Errorf("API error: %s - %s", openaiResp.Error.Type, openaiResp.Error.Message)
	}

	// Extract message content
	if len(openaiResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in API response")
	}

	return openaiResp.Choices[0].Message.Content, nil
}

// buildSystemMessage creates the system message for the OpenAI API.
func (o *OpenAIProvider) buildSystemMessage(request *AgentRequest) string {
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
