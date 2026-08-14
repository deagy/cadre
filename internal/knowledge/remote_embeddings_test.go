package knowledge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRemoteEmbedderValid(t *testing.T) {
	config := &RemoteEmbedderConfig{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "sk-test",
		Model:   "text-embedding-3-small",
	}

	embedder, err := NewRemoteEmbedder(config)
	if err != nil {
		t.Fatalf("NewRemoteEmbedder failed: %v", err)
	}

	if embedder.Name() != "openai-compatible" {
		t.Errorf("Expected name 'openai-compatible', got %s", embedder.Name())
	}

	if embedder.Model() != "text-embedding-3-small" {
		t.Errorf("Expected model 'text-embedding-3-small', got %s", embedder.Model())
	}
}

func TestRemoteEmbedderMissingBaseURL(t *testing.T) {
	config := &RemoteEmbedderConfig{
		Model: "text-embedding-3-small",
	}

	_, err := NewRemoteEmbedder(config)
	if err == nil {
		t.Error("Expected error for missing BaseURL")
	}
}

func TestRemoteEmbedderMissingModel(t *testing.T) {
	config := &RemoteEmbedderConfig{
		BaseURL: "https://api.openai.com/v1",
	}

	_, err := NewRemoteEmbedder(config)
	if err == nil {
		t.Error("Expected error for missing Model")
	}
}

func TestRemoteEmbedderDefaultTimeout(t *testing.T) {
	config := &RemoteEmbedderConfig{
		BaseURL: "https://api.openai.com/v1",
		Model:   "text-embedding-3-small",
		Timeout: 0, // Should use default
	}

	embedder, _ := NewRemoteEmbedder(config)
	if config.Timeout != 30*time.Second {
		t.Errorf("Expected default timeout 30s, got %v", config.Timeout)
	}

	_ = embedder
}

func TestRemoteEmbedderEmptyInput(t *testing.T) {
	config := &RemoteEmbedderConfig{
		BaseURL: "https://api.openai.com/v1",
		Model:   "text-embedding-3-small",
	}

	embedder, _ := NewRemoteEmbedder(config)

	// Empty input should return empty output without calling API
	result, err := embedder.Embed([]string{})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("Expected empty result, got %d embeddings", len(result))
	}
}

func TestRemoteEmbedderWithMockServer(t *testing.T) {
	// Create mock server that returns embeddings
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("Expected Content-Type: application/json")
		}

		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Error("Expected Authorization header")
		}

		// Parse request
		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		// Return mock embeddings
		resp := embeddingResponse{
			Object: "list",
			Model:  "text-embedding-3-small",
		}

		for i := range req.Input {
			resp.Data = append(resp.Data, struct {
				Index     int       `json:"index"`
				Embedding []float64 `json:"embedding"`
			}{
				Index:     i,
				Embedding: []float64{0.1, 0.2, 0.3},
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	config := &RemoteEmbedderConfig{
		BaseURL: server.URL,
		APIKey:  "sk-test",
		Model:   "text-embedding-3-small",
	}

	embedder, _ := NewRemoteEmbedder(config)

	// Test embedding
	texts := []string{"hello", "world"}
	embeddings, err := embedder.Embed(texts)

	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(embeddings) != 2 {
		t.Errorf("Expected 2 embeddings, got %d", len(embeddings))
	}

	if len(embeddings[0]) != 3 {
		t.Errorf("Expected 3-dimensional embedding, got %d", len(embeddings[0]))
	}
}

func TestRemoteEmbedderRetry(t *testing.T) {
	attemptCount := 0

	// Create server that fails first time, succeeds second time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++

		if attemptCount == 1 {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}

		// Second attempt succeeds
		resp := embeddingResponse{
			Object: "list",
			Data: []struct {
				Index     int       `json:"index"`
				Embedding []float64 `json:"embedding"`
			}{
				{Index: 0, Embedding: []float64{0.1}},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	config := &RemoteEmbedderConfig{
		BaseURL:    server.URL,
		Model:      "test",
		MaxRetries: 2,
		RetryDelay: 10 * time.Millisecond,
	}

	embedder, _ := NewRemoteEmbedder(config)
	_, err := embedder.Embed([]string{"test"})

	if err != nil {
		t.Fatalf("Embed failed after retry: %v", err)
	}

	if attemptCount != 2 {
		t.Errorf("Expected 2 attempts, got %d", attemptCount)
	}
}

func TestRemoteEmbedderFallback(t *testing.T) {
	// Create server that always fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	config := &RemoteEmbedderConfig{
		BaseURL:       server.URL,
		Model:         "test",
		MaxRetries:    0,
		FallbackLocal: true,
	}

	embedder, _ := NewRemoteEmbedder(config)

	// Should fall back to local embedder
	embeddings, err := embedder.Embed([]string{"test"})

	if err != nil {
		t.Fatalf("Embed with fallback failed: %v", err)
	}

	if len(embeddings) != 1 {
		t.Errorf("Expected 1 embedding, got %d", len(embeddings))
	}
}

func TestRemoteEmbedderNoFallback(t *testing.T) {
	// Create server that always fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	config := &RemoteEmbedderConfig{
		BaseURL:       server.URL,
		Model:         "test",
		MaxRetries:    0,
		FallbackLocal: false, // No fallback
	}

	embedder, _ := NewRemoteEmbedder(config)

	// Should fail without fallback
	_, err := embedder.Embed([]string{"test"})

	if err == nil {
		t.Error("Expected error without fallback")
	}

	if !strings.Contains(err.Error(), "no fallback configured") {
		t.Errorf("Expected 'no fallback configured' in error, got: %v", err)
	}
}

func TestRemoteEmbedderInvalidResponse(t *testing.T) {
	// Create server that returns invalid response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return response with wrong number of embeddings
		resp := embeddingResponse{
			Object: "list",
			Data: []struct {
				Index     int       `json:"index"`
				Embedding []float64 `json:"embedding"`
			}{
				{Index: 0, Embedding: []float64{0.1}},
				// Should have 2, but only returning 1
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	config := &RemoteEmbedderConfig{
		BaseURL:       server.URL,
		Model:         "test",
		FallbackLocal: false,
	}

	embedder, _ := NewRemoteEmbedder(config)

	_, err := embedder.Embed([]string{"text1", "text2"})

	if err == nil {
		t.Error("Expected error for mismatched embedding count")
	}
}

func TestValidateConfigNil(t *testing.T) {
	err := validateConfig(nil)
	if err == nil {
		t.Error("Expected error for nil config")
	}
}

func TestValidateConfigBadURL(t *testing.T) {
	config := &RemoteEmbedderConfig{
		BaseURL: "not-a-url",
		Model:   "test",
	}

	err := validateConfig(config)
	if err == nil {
		t.Error("Expected error for invalid URL")
	}
}

func TestValidateConfigNegativeTimeout(t *testing.T) {
	config := &RemoteEmbedderConfig{
		BaseURL: "https://api.test.com",
		Model:   "test",
		Timeout: -1,
	}

	err := validateConfig(config)
	if err == nil {
		t.Error("Expected error for negative timeout")
	}
}
