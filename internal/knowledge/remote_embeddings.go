package knowledge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// RemoteEmbedderConfig configures an OpenAI-compatible embeddings client.
type RemoteEmbedderConfig struct {
	BaseURL       string        // e.g., "https://api.openai.com/v1"
	APIKey        string        // API key (can be empty for local deployments)
	Model         string        // Model name, e.g., "text-embedding-3-small"
	Timeout       time.Duration // Request timeout (default 30s)
	MaxRetries    int           // Number of retries (default 3)
	RetryDelay    time.Duration // Initial retry delay (default 1s)
	FallbackLocal bool          // Fallback to local hashing on error (default true)
}

// RemoteEmbedder calls an OpenAI-compatible embeddings API.
// Implements EmbeddingProvider interface.
type RemoteEmbedder struct {
	config    *RemoteEmbedderConfig
	client    *http.Client
	fallback  EmbeddingProvider // Local embedder as fallback
}

// embeddingRequest is the request payload for embeddings API.
type embeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions *int     `json:"dimensions,omitempty"`
}

// embeddingResponse is the response payload from embeddings API.
type embeddingResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// NewRemoteEmbedder creates a new remote embeddings client.
// If FallbackLocal is true, uses local hashing as fallback on errors.
func NewRemoteEmbedder(config *RemoteEmbedderConfig) (*RemoteEmbedder, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}

	// Apply defaults
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = 1 * time.Second
	}

	// Create HTTP client
	client := &http.Client{
		Timeout: config.Timeout,
	}

	// Create fallback embedder if enabled
	var fallback EmbeddingProvider
	if config.FallbackLocal {
		fallback = NewLocalHashingEmbedder(128)
	}

	return &RemoteEmbedder{
		config:   config,
		client:   client,
		fallback: fallback,
	}, nil
}

// NewRemoteEmbedderFromEnv creates a RemoteEmbedder from environment variables.
// Reads: EMBEDDINGS_BASE_URL, EMBEDDINGS_API_KEY, EMBEDDINGS_MODEL, EMBEDDINGS_TIMEOUT_SECONDS
func NewRemoteEmbedderFromEnv() (*RemoteEmbedder, error) {
	config := &RemoteEmbedderConfig{
		BaseURL: os.Getenv("EMBEDDINGS_BASE_URL"),
		APIKey:  os.Getenv("EMBEDDINGS_API_KEY"),
		Model:   os.Getenv("EMBEDDINGS_MODEL"),
	}

	// Parse timeout if set
	if timeoutStr := os.Getenv("EMBEDDINGS_TIMEOUT_SECONDS"); timeoutStr != "" {
		var timeoutSecs int
		if _, err := fmt.Sscanf(timeoutStr, "%d", &timeoutSecs); err == nil && timeoutSecs > 0 {
			config.Timeout = time.Duration(timeoutSecs) * time.Second
		}
	}

	// Default to fallback enabled
	config.FallbackLocal = true

	return NewRemoteEmbedder(config)
}

// Name returns the provider identifier.
func (r *RemoteEmbedder) Name() string {
	return "openai-compatible"
}

// Model returns the model identifier.
func (r *RemoteEmbedder) Model() string {
	return r.config.Model
}

// Dimensions returns the embedding vector dimensions (unknown for remote).
func (r *RemoteEmbedder) Dimensions() int {
	// Typical OpenAI embeddings are 1536-dim, but we don't know without calling API
	return 1536
}

// Embed calls the remote embeddings API with retry logic and fallback.
func (r *RemoteEmbedder) Embed(texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return [][]float64{}, nil
	}

	// Try remote API with retries
	embeddings, err := r.embedWithRetry(texts)
	if err == nil {
		return embeddings, nil
	}

	// Fallback to local if configured
	if r.fallback != nil {
		fmt.Fprintf(os.Stderr, "warning: remote embeddings failed, falling back to local: %v\n", err)
		return r.fallback.Embed(texts)
	}

	return nil, fmt.Errorf("remote embeddings failed and no fallback configured: %w", err)
}

// embedWithRetry calls the API with exponential backoff retry logic.
func (r *RemoteEmbedder) embedWithRetry(texts []string) ([][]float64, error) {
	var lastErr error
	delay := r.config.RetryDelay

	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(delay)
			delay *= 2 // Exponential backoff
		}

		embeddings, err := r.embedOnce(texts)
		if err == nil {
			return embeddings, nil
		}

		lastErr = err

		// Don't retry on validation errors
		if isValidationError(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("embeddings API failed after %d retries: %w", r.config.MaxRetries+1, lastErr)
}

// embedOnce makes a single API call.
func (r *RemoteEmbedder) embedOnce(texts []string) ([][]float64, error) {
	// Build request
	reqBody := embeddingRequest{
		Model: r.config.Model,
		Input: texts,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal request: %w", err)
	}

	// Create HTTP request
	endpoint := r.config.BaseURL + "/embeddings"
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("cannot create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	if r.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	}

	// Execute request
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body (with size limit)
	bodyReader := io.LimitReader(resp.Body, 100*1024*1024) // 100MB limit
	respBytes, err := io.ReadAll(bodyReader)
	if err != nil {
		return nil, fmt.Errorf("cannot read response: %w", err)
	}

	// Check HTTP status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API returned HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	// Parse response
	var respBody embeddingResponse
	if err := json.Unmarshal(respBytes, &respBody); err != nil {
		return nil, fmt.Errorf("cannot unmarshal response: %w", err)
	}

	// Extract embeddings in correct order
	if len(respBody.Data) != len(texts) {
		return nil, fmt.Errorf("response has %d embeddings, expected %d", len(respBody.Data), len(texts))
	}

	// Sort by index to ensure correct ordering
	embeddings := make([][]float64, len(texts))
	for _, item := range respBody.Data {
		if item.Index < 0 || item.Index >= len(texts) {
			return nil, fmt.Errorf("invalid index in response: %d", item.Index)
		}
		embeddings[item.Index] = item.Embedding
	}

	return embeddings, nil
}

// validateConfig checks configuration validity.
func validateConfig(config *RemoteEmbedderConfig) error {
	if config == nil {
		return fmt.Errorf("config is required")
	}

	if config.BaseURL == "" {
		return fmt.Errorf("BaseURL is required")
	}

	if config.Model == "" {
		return fmt.Errorf("Model is required")
	}

	// Validate URL format (basic check)
	if config.BaseURL != "https://api.openai.com/v1" &&
		!bytes.Contains([]byte(config.BaseURL), []byte("http://")) &&
		!bytes.Contains([]byte(config.BaseURL), []byte("https://")) {
		return fmt.Errorf("BaseURL must start with http:// or https://")
	}

	// Validate timeout is positive if set
	if config.Timeout < 0 {
		return fmt.Errorf("Timeout must be non-negative")
	}

	// Validate retry counts
	if config.MaxRetries < 0 {
		return fmt.Errorf("MaxRetries must be non-negative")
	}

	if config.RetryDelay < 0 {
		return fmt.Errorf("RetryDelay must be non-negative")
	}

	return nil
}

// isValidationError determines if an error is a validation error (non-retryable).
func isValidationError(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	// Check for common validation errors
	nonRetryable := []string{
		"cannot marshal",
		"must start with",
		"is required",
		"Model is required",
		"invalid",
	}

	for _, pattern := range nonRetryable {
		if bytes.Contains([]byte(msg), []byte(pattern)) {
			return true
		}
	}

	return false
}
