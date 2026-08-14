package knowledge

import (
	"strings"
	"testing"
)

// Database persistence tests. These require CGO_ENABLED=1 due to SQLite dependency.
// Skipped in CGO_ENABLED=0 builds; run with: CGO_ENABLED=1 go test ./internal/knowledge/...

func TestSaveAndGetMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save a message
	messageID, err := store.SaveMessage(
		"test-source",
		stringPtr("http://example.com"),
		"conv-1",
		stringPtr("Test Conversation"),
		"msg-123",
		"user",
		"Hello, world!",
		stringPtr("2026-08-13T10:00:00.000Z"),
		"general",
		false,
		`[]`,
		`{}`,
		nil,
	)

	if err != nil {
		t.Fatalf("SaveMessage failed: %v", err)
	}

	if messageID == "" {
		t.Error("Expected non-empty message ID")
	}

	// Retrieve the message
	msg, err := store.GetMessage(messageID)
	if err != nil {
		t.Fatalf("GetMessage failed: %v", err)
	}

	if msg.Source != "test-source" {
		t.Errorf("Expected source 'test-source', got %s", msg.Source)
	}

	if msg.Content != "Hello, world!" {
		t.Errorf("Expected content 'Hello, world!', got %s", msg.Content)
	}

	if msg.Classification != "general" {
		t.Errorf("Expected classification 'general', got %s", msg.Classification)
	}
}

func TestSaveAndGetChunk(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// First save a message
	messageID, err := store.SaveMessage(
		"test-source", nil, "conv-1", nil, "msg-123",
		"user", "Test message", nil, "general", false,
		`[]`, `{}`, nil,
	)
	if err != nil {
		t.Fatalf("SaveMessage failed: %v", err)
	}

	// Save a chunk with embedding
	embedding := []float64{0.1, 0.2, 0.3}
	err = store.SaveChunk(
		messageID,
		0,
		"Test chunk content",
		"local-hashing",
		"fnv1a-d128",
		embedding,
	)

	if err != nil {
		t.Fatalf("SaveChunk failed: %v", err)
	}

	// Retrieve the chunk
	chunk, err := store.GetChunk(messageID, 0)
	if err != nil {
		t.Fatalf("GetChunk failed: %v", err)
	}

	if chunk.Content != "Test chunk content" {
		t.Errorf("Expected content 'Test chunk content', got %s", chunk.Content)
	}

	if chunk.EmbeddingProvider != "local-hashing" {
		t.Errorf("Expected provider 'local-hashing', got %s", chunk.EmbeddingProvider)
	}

	if chunk.EmbeddingDimensions != 3 {
		t.Errorf("Expected dimensions 3, got %d", chunk.EmbeddingDimensions)
	}
}

func TestUPSERTMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save a message
	messageID1, _ := store.SaveMessage(
		"test-source", nil, "conv-1", nil, "msg-123",
		"user", "Original content", nil, "general", false,
		`[]`, `{}`, nil,
	)

	// Update the same message (by source + conversation_id + source_message_id)
	messageID2, _ := store.SaveMessage(
		"test-source", nil, "conv-1", nil, "msg-123",
		"user", "Updated content", nil, "sensitive", true,
		`["secret"]`, `{"key": "value"}`, nil,
	)

	// IDs should be identical (same source + conv + msg)
	if messageID1 != messageID2 {
		t.Errorf("UPSERT should produce same ID: %s vs %s", messageID1, messageID2)
	}

	// Verify the update was applied
	msg, _ := store.GetMessage(messageID1)
	if msg.Content != "Updated content" {
		t.Errorf("Expected 'Updated content', got %s", msg.Content)
	}

	if msg.Classification != "sensitive" {
		t.Errorf("Expected classification 'sensitive', got %s", msg.Classification)
	}

	if !msg.InjectionRisk {
		t.Error("Expected injection_risk to be true")
	}
}

func TestSaveChunks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save a message
	messageID, _ := store.SaveMessage(
		"test-source", nil, "conv-1", nil, "msg-123",
		"user", "Multi-chunk message", nil, "general", false,
		`[]`, `{}`, nil,
	)

	// Save multiple chunks
	contents := []string{"chunk 1", "chunk 2", "chunk 3"}
	embeddings := [][]float64{
		{0.1, 0.2, 0.3},
		{0.4, 0.5, 0.6},
		{0.7, 0.8, 0.9},
	}

	err := store.SaveChunks(
		messageID, contents, embeddings,
		"local-hashing", "fnv1a-d128",
	)

	if err != nil {
		t.Fatalf("SaveChunks failed: %v", err)
	}

	// Retrieve all chunks
	chunks, err := store.GetChunks(messageID)
	if err != nil {
		t.Fatalf("GetChunks failed: %v", err)
	}

	if len(chunks) != 3 {
		t.Errorf("Expected 3 chunks, got %d", len(chunks))
	}

	for i, chunk := range chunks {
		if chunk.Content != contents[i] {
			t.Errorf("Chunk %d: expected %s, got %s", i, contents[i], chunk.Content)
		}

		if chunk.Ordinal != i {
			t.Errorf("Chunk ordinal mismatch: expected %d, got %d", i, chunk.Ordinal)
		}
	}
}

func TestCascadeDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save a message with chunks
	messageID, _ := store.SaveMessage(
		"test-source", nil, "conv-1", nil, "msg-123",
		"user", "Message to delete", nil, "general", false,
		`[]`, `{}`, nil,
	)

	store.SaveChunk(messageID, 0, "chunk 1", "local-hashing", "fnv1a-d128", []float64{0.1})
	store.SaveChunk(messageID, 1, "chunk 2", "local-hashing", "fnv1a-d128", []float64{0.2})

	// Delete the message
	err := store.DeleteMessage(messageID)
	if err != nil {
		t.Fatalf("DeleteMessage failed: %v", err)
	}

	// Verify message is gone
	_, err = store.GetMessage(messageID)
	if err == nil {
		t.Error("Expected error when getting deleted message")
	}

	// Verify chunks are gone (cascade delete)
	chunks, err := store.GetChunks(messageID)
	if err != nil {
		t.Fatalf("GetChunks failed: %v", err)
	}

	if len(chunks) != 0 {
		t.Errorf("Expected 0 chunks after cascade delete, got %d", len(chunks))
	}
}

func TestMessageCounts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Initial count should be 0
	count, err := store.MessageCount()
	if err != nil {
		t.Fatalf("MessageCount failed: %v", err)
	}

	if count != 0 {
		t.Errorf("Expected 0 messages initially, got %d", count)
	}

	// Save a message
	store.SaveMessage(
		"test-source", nil, "conv-1", nil, "msg-1",
		"user", "msg 1", nil, "general", false,
		`[]`, `{}`, nil,
	)

	count, err = store.MessageCount()
	if err != nil {
		t.Fatalf("MessageCount failed: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 message, got %d", count)
	}

	// Save another message
	store.SaveMessage(
		"test-source", nil, "conv-2", nil, "msg-2",
		"user", "msg 2", nil, "general", false,
		`[]`, `{}`, nil,
	)

	count, err = store.MessageCount()
	if err != nil {
		t.Fatalf("MessageCount failed: %v", err)
	}

	if count != 2 {
		t.Errorf("Expected 2 messages, got %d", count)
	}
}

func TestMultipleEmbeddingModels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save a message
	messageID, _ := store.SaveMessage(
		"test-source", nil, "conv-1", nil, "msg-123",
		"user", "Test message", nil, "general", false,
		`[]`, `{}`, nil,
	)

	// Save chunks with different embedding models
	store.SaveChunk(messageID, 0, "chunk", "local-hashing", "fnv1a-d128", []float64{0.1})
	store.SaveChunk(messageID, 0, "chunk", "openai-compatible", "text-embedding-3-small", []float64{0.2, 0.3})

	// Both should be retrievable via stats
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if len(stats.EmbeddingModels) != 2 {
		t.Errorf("Expected 2 embedding models, got %d", len(stats.EmbeddingModels))
	}
}

func TestEmptyChunkError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Try to save chunk with empty embedding
	err := store.SaveChunk("msg-1", 0, "content", "provider", "model", []float64{})
	if err == nil {
		t.Error("Expected error for empty embedding")
	}

	if !strings.Contains(err.Error(), "embedding is required") {
		t.Errorf("Expected 'embedding is required' error, got: %v", err)
	}
}

func TestNegativeOrdinalError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Try to save chunk with negative ordinal
	err := store.SaveChunk("msg-1", -1, "content", "provider", "model", []float64{0.1})
	if err == nil {
		t.Error("Expected error for negative ordinal")
	}

	if !strings.Contains(err.Error(), "non-negative") {
		t.Errorf("Expected 'non-negative' error, got: %v", err)
	}
}

// helper function to set up a test database
func setupTestDB(t *testing.T) *Store {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	return store
}

// helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}
