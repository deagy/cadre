package knowledge

import (
	"testing"
)

// Search tests. Require CGO_ENABLED=1 due to SQLite.

func TestVectorSearch(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Create embedder
	embedder := NewLocalHashingEmbedder(128)

	// Save test messages with chunks
	msg1ID, _ := store.SaveMessage(
		"source-1", nil, "conv-1", nil, "msg-1",
		"user", "machine learning is fascinating", nil, "technical", false,
		`[]`, `{}`, nil,
	)

	msg2ID, _ := store.SaveMessage(
		"source-1", nil, "conv-2", nil, "msg-2",
		"user", "deep learning networks", nil, "technical", false,
		`[]`, `{}`, nil,
	)

	msg3ID, _ := store.SaveMessage(
		"source-1", nil, "conv-3", nil, "msg-3",
		"user", "cooking recipes and tips", nil, "general", false,
		`[]`, `{}`, nil,
	)

	// Embed and save chunks
	texts := []string{
		"machine learning is fascinating",
		"deep learning networks",
		"cooking recipes and tips",
	}

	embeddings, _ := embedder.Embed(texts)

	store.SaveChunk(msg1ID, 0, texts[0], embedder.Name(), embedder.Model(), embeddings[0])
	store.SaveChunk(msg2ID, 0, texts[1], embedder.Name(), embedder.Model(), embeddings[1])
	store.SaveChunk(msg3ID, 0, texts[2], embedder.Name(), embedder.Model(), embeddings[2])

	// Search for similar content
	results, err := store.Search(SearchOptions{
		Query:             "machine learning",
		Classification:    "technical",
		AllSources:        true,
		EmbeddingProvider: embedder,
		Top:               5,
	})

	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected search results")
	}

	// Results should be sorted by similarity
	if len(results) > 1 {
		for i := 0; i < len(results)-1; i++ {
			if results[i].CosineSimilarity < results[i+1].CosineSimilarity {
				t.Error("Results not sorted by similarity (descending)")
			}
		}
	}
}

func TestSearchWithSourceFilter(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Save messages from different sources
	msg1ID, _ := store.SaveMessage(
		"source-a", nil, "conv-1", nil, "msg-1",
		"user", "test message", nil, "general", false,
		`[]`, `{}`, nil,
	)

	msg2ID, _ := store.SaveMessage(
		"source-b", nil, "conv-2", nil, "msg-2",
		"user", "test message", nil, "general", false,
		`[]`, `{}`, nil,
	)

	embedding, _ := embedder.Embed([]string{"test message"})
	store.SaveChunk(msg1ID, 0, "test", embedder.Name(), embedder.Model(), embedding[0])
	store.SaveChunk(msg2ID, 0, "test", embedder.Name(), embedder.Model(), embedding[0])

	// Search filtering by source
	results, err := store.Search(SearchOptions{
		Query:             "test",
		Classification:    "general",
		SourceFilters:     []string{"source-a"},
		EmbeddingProvider: embedder,
	})

	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}

	if results[0].Message.Source != "source-a" {
		t.Errorf("Expected source-a, got %s", results[0].Message.Source)
	}
}

func TestSearchWithMultipleSources(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Save messages from three sources
	for i := 1; i <= 3; i++ {
		msgID, _ := store.SaveMessage(
			"source-"+string(rune(96+i)), nil, "conv", nil, "msg",
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)

		embedding, _ := embedder.Embed([]string{"content"})
		store.SaveChunk(msgID, 0, "c", embedder.Name(), embedder.Model(), embedding[0])
	}

	// Search filtering by multiple sources
	results, err := store.Search(SearchOptions{
		Query:             "content",
		Classification:    "general",
		SourceFilters:     []string{"source-a", "source-b"},
		EmbeddingProvider: embedder,
	})

	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}
}

func TestSearchByContent(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save test messages
	store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "The quick brown fox", nil, "general", false,
		`[]`, `{}`, nil,
	)

	store.SaveMessage(
		"source", nil, "conv-2", nil, "msg-2",
		"user", "The lazy dog", nil, "general", false,
		`[]`, `{}`, nil,
	)

	// Search by content
	results, err := store.SearchByContent("quick", "general", 10)
	if err != nil {
		t.Fatalf("SearchByContent failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}

	if results[0].Content != "The quick brown fox" {
		t.Errorf("Expected 'The quick brown fox', got %s", results[0].Content)
	}
}

func TestSearchClassificationFilter(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Save messages with different classifications
	msg1ID, _ := store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "technical content", nil, "technical", false,
		`[]`, `{}`, nil,
	)

	msg2ID, _ := store.SaveMessage(
		"source", nil, "conv-2", nil, "msg-2",
		"user", "general content", nil, "general", false,
		`[]`, `{}`, nil,
	)

	embeddings, _ := embedder.Embed([]string{"content", "content"})
	store.SaveChunk(msg1ID, 0, "tech", embedder.Name(), embedder.Model(), embeddings[0])
	store.SaveChunk(msg2ID, 0, "gen", embedder.Name(), embedder.Model(), embeddings[1])

	// Search for technical only
	results, err := store.Search(SearchOptions{
		Query:             "content",
		Classification:    "technical",
		AllSources:        true,
		EmbeddingProvider: embedder,
	})

	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 technical result, got %d", len(results))
	}

	if results[0].Message.Classification != "technical" {
		t.Errorf("Expected technical classification, got %s", results[0].Message.Classification)
	}
}

func TestSearchTopK(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Save multiple messages
	for i := 0; i < 5; i++ {
		msgID, _ := store.SaveMessage(
			"source", nil, "conv", nil, "msg"+string(rune(48+i)),
			"user", "message "+string(rune(48+i)), nil, "general", false,
			`[]`, `{}`, nil,
		)

		embeddings, _ := embedder.Embed([]string{"msg"})
		store.SaveChunk(msgID, 0, "c", embedder.Name(), embedder.Model(), embeddings[0])
	}

	// Search with limit
	results, err := store.Search(SearchOptions{
		Query:             "message",
		Classification:    "general",
		AllSources:        true,
		EmbeddingProvider: embedder,
		Top:               2,
	})

	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) > 2 {
		t.Errorf("Expected at most 2 results, got %d", len(results))
	}
}

func TestSearchDefaultTopK(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Save message
	msgID, _ := store.SaveMessage(
		"source", nil, "conv", nil, "msg",
		"user", "test", nil, "general", false,
		`[]`, `{}`, nil,
	)

	embeddings, _ := embedder.Embed([]string{"test"})
	store.SaveChunk(msgID, 0, "t", embedder.Name(), embedder.Model(), embeddings[0])

	// Search without specifying Top (should default to 10)
	results, err := store.Search(SearchOptions{
		Query:             "test",
		Classification:    "general",
		AllSources:        true,
		EmbeddingProvider: embedder,
		Top:               0, // Will be set to default
	})

	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected results with default top-k")
	}
}

func TestSearchAllSources(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Save from two sources
	for src := 1; src <= 2; src++ {
		msgID, _ := store.SaveMessage(
			"source-"+string(rune(48+src)), nil, "conv", nil, "msg",
			"user", "msg", nil, "general", false,
			`[]`, `{}`, nil,
		)

		embeddings, _ := embedder.Embed([]string{"msg"})
		store.SaveChunk(msgID, 0, "m", embedder.Name(), embedder.Model(), embeddings[0])
	}

	// Search all sources
	results, err := store.Search(SearchOptions{
		Query:             "msg",
		Classification:    "general",
		EmbeddingProvider: embedder,
		AllSources:        true,
	})

	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results from all sources, got %d", len(results))
	}
}

func TestSearchErrorMissingQuery(t *testing.T) {
	requireSQLite(t)
	store := setupTestDB(t)
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	_, err := store.Search(SearchOptions{
		Query:             "",
		Classification:    "general",
		EmbeddingProvider: embedder,
	})

	if err == nil {
		t.Error("Expected error for missing query")
	}
}

func TestSearchErrorMissingClassification(t *testing.T) {
	requireSQLite(t)
	store := setupTestDB(t)
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	_, err := store.Search(SearchOptions{
		Query:             "test",
		Classification:    "",
		EmbeddingProvider: embedder,
	})

	if err == nil {
		t.Error("Expected error for missing classification")
	}
}

func TestSearchErrorMissingProvider(t *testing.T) {
	requireSQLite(t)
	store := setupTestDB(t)
	defer store.Close()

	_, err := store.Search(SearchOptions{
		Query:             "test",
		Classification:    "general",
		EmbeddingProvider: nil,
	})

	if err == nil {
		t.Error("Expected error for missing embedding provider")
	}
}
