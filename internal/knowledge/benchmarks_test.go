//go:build cgo
// +build cgo

package knowledge

import (
	"fmt"
	"testing"
)

// Performance benchmarks for the knowledge store.
// Run with: go test -bench=. -benchmem ./internal/knowledge
// Run specific benchmark: go test -bench=BenchmarkSaveMessage ./internal/knowledge

func BenchmarkSaveMessage(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		source := fmt.Sprintf("source-%d", i%10)
		convID := fmt.Sprintf("conv-%d", i%100)
		msgID := fmt.Sprintf("msg-%d", i)
		content := fmt.Sprintf("Test message %d with some content", i)

		store.SaveMessage(
			source, nil, convID, nil, msgID,
			"user", content, nil, "general", false,
			`[]`, `{}`, nil,
		)
	}
}

func BenchmarkSaveChunk(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Pre-create messages
	messageIDs := make([]string, 100)
	for i := 0; i < 100; i++ {
		msgID, _ := store.SaveMessage(
			"bench-source", nil, "bench-conv", nil, fmt.Sprintf("bench-msg-%d", i),
			"user", fmt.Sprintf("content %d", i), nil, "general", false,
			`[]`, `{}`, nil,
		)
		messageIDs[i] = msgID
	}

	embedding, _ := embedder.Embed([]string{"test content"})
	testEmbedding := embedding[0]

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		store.SaveChunk(
			messageIDs[i%100], 0,
			fmt.Sprintf("chunk content %d", i),
			embedder.Name(), embedder.Model(),
			testEmbedding,
		)
	}
}

func BenchmarkSaveChunks(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Pre-create messages
	messageIDs := make([]string, 100)
	for i := 0; i < 100; i++ {
		msgID, _ := store.SaveMessage(
			"bench-source", nil, "bench-conv", nil, fmt.Sprintf("bench-msg-%d", i),
			"user", fmt.Sprintf("content %d", i), nil, "general", false,
			`[]`, `{}`, nil,
		)
		messageIDs[i] = msgID
	}

	// Pre-generate embeddings
	contents := make([]string, 10)
	embeddings := make([][]float64, 10)
	for i := 0; i < 10; i++ {
		contents[i] = fmt.Sprintf("chunk %d content", i)
		embs, _ := embedder.Embed([]string{contents[i]})
		embeddings[i] = embs[0]
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		store.SaveChunks(
			messageIDs[i%100],
			contents, embeddings,
			embedder.Name(), embedder.Model(),
		)
	}
}

func BenchmarkLocalHashingEmbed(b *testing.B) {
	embedder := NewLocalHashingEmbedder(128)

	texts := []string{
		"This is a test message for embedding",
		"Another example text to embed",
		"Machine learning is fascinating",
		"Data science and AI",
		"Deep learning models",
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		embedder.Embed([]string{texts[i%len(texts)]})
	}
}

func BenchmarkLocalHashingEmbedBatch(b *testing.B) {
	embedder := NewLocalHashingEmbedder(128)

	texts := []string{
		"Text 1", "Text 2", "Text 3", "Text 4", "Text 5",
		"Text 6", "Text 7", "Text 8", "Text 9", "Text 10",
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		embedder.Embed(texts)
	}
}

func BenchmarkCosineSimilarity(b *testing.B) {
	v1 := []float64{0.1, 0.2, 0.3, 0.4, 0.5}
	v2 := []float64{0.5, 0.4, 0.3, 0.2, 0.1}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		CosineSimilarity(v1, v2)
	}
}

func BenchmarkSearch(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Pre-populate with test data (100 messages, 3 chunks each)
	for i := 0; i < 100; i++ {
		msgID, _ := store.SaveMessage(
			"bench-source", nil, "bench-conv", nil, fmt.Sprintf("msg-%d", i),
			"user", fmt.Sprintf("Message %d content about machine learning", i), nil, "general", false,
			`[]`, `{}`, nil,
		)

		for j := 0; j < 3; j++ {
			content := fmt.Sprintf("Chunk %d of message %d", j, i)
			embs, _ := embedder.Embed([]string{content})
			store.SaveChunk(msgID, j, content, embedder.Name(), embedder.Model(), embs[0])
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		store.Search(SearchOptions{
			Query:             "machine learning algorithms",
			Classification:    "general",
			EmbeddingProvider: embedder,
			Top:               10,
		})
	}
}

func BenchmarkSearchWithSourceFilter(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Pre-populate with test data from multiple sources
	for i := 0; i < 100; i++ {
		source := fmt.Sprintf("source-%d", i%5)
		msgID, _ := store.SaveMessage(
			source, nil, "bench-conv", nil, fmt.Sprintf("msg-%d", i),
			"user", fmt.Sprintf("Message from %s", source), nil, "general", false,
			`[]`, `{}`, nil,
		)

		embs, _ := embedder.Embed([]string{fmt.Sprintf("content %d", i)})
		store.SaveChunk(msgID, 0, fmt.Sprintf("content %d", i), embedder.Name(), embedder.Model(), embs[0])
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		store.Search(SearchOptions{
			Query:             "message",
			Classification:    "general",
			SourceFilters:     []string{"source-0", "source-1"},
			EmbeddingProvider: embedder,
			Top:               10,
		})
	}
}

func BenchmarkSearchByContent(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	// Pre-populate with test data
	for i := 0; i < 100; i++ {
		store.SaveMessage(
			"bench-source", nil, "bench-conv", nil, fmt.Sprintf("msg-%d", i),
			"user", fmt.Sprintf("Message %d with machine learning content", i), nil, "general", false,
			`[]`, `{}`, nil,
		)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		store.SearchByContent("machine learning", "general", 10)
	}
}

func BenchmarkDeleteMessage(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	// Pre-create messages to delete
	messageIDs := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		msgID, _ := store.SaveMessage(
			"bench-source", nil, "bench-conv", nil, fmt.Sprintf("msg-%d", i),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)
		messageIDs[i] = msgID
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		store.DeleteMessage(messageIDs[i])
	}
}

func BenchmarkDeleteExpired(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	// Pre-create expired messages (1 per iteration, to test realistic scenario)
	past := "2000-01-01T00:00:00.000Z"

	for i := 0; i < b.N; i++ {
		store.SaveMessage(
			"bench-source", nil, "bench-conv", nil, fmt.Sprintf("msg-%d", i),
			"user", "content", nil, "general", false,
			`[]`, `{}`, &past,
		)
	}

	b.ReportAllocs()
	b.ResetTimer()

	// All b.N messages are expired, so each call deletes all remaining
	for i := 0; i < b.N; i++ {
		store.DeleteExpired("bench-user")
		if i < b.N-1 {
			// Re-create for next iteration
			store.SaveMessage(
				"bench-source", nil, "bench-conv", nil, fmt.Sprintf("msg2-%d", i),
				"user", "content", nil, "general", false,
				`[]`, `{}`, &past,
			)
		}
	}
}

func BenchmarkDeleteByClassification(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	// Pre-create messages with classifications
	classifications := []string{"class-0", "class-1", "class-2"}

	for i := 0; i < b.N; i++ {
		classification := classifications[i%len(classifications)]
		store.SaveMessage(
			"bench-source", nil, "bench-conv", nil, fmt.Sprintf("msg-%d", i),
			"user", "content", nil, classification, false,
			`[]`, `{}`, nil,
		)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		classification := classifications[i%len(classifications)]
		store.DeleteByClassification(classification, "bench", "bench-user")
		// Re-create for next iteration
		store.SaveMessage(
			"bench-source", nil, "bench-conv", nil, fmt.Sprintf("msg2-%d", i),
			"user", "content", nil, classification, false,
			`[]`, `{}`, nil,
		)
	}
}

func BenchmarkBulkIngest(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Benchmark bulk ingestion of 100 messages per iteration
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		runID, _ := store.BeginRun("bench-source", "")

		for j := 0; j < 100; j++ {
			msgIdx := i*100 + j
			msgID, _ := store.SaveMessage(
				"bench-source", nil, "bench-conv", nil, fmt.Sprintf("msg-%d", msgIdx),
				"user", fmt.Sprintf("Message %d", msgIdx), nil, "general", false,
				`[]`, `{}`, nil,
			)

			embs, _ := embedder.Embed([]string{fmt.Sprintf("content %d", msgIdx)})
			store.SaveChunk(msgID, 0, fmt.Sprintf("content %d", msgIdx), embedder.Name(), embedder.Model(), embs[0])
		}

		store.CompleteRun(runID, 100, 100)
	}
}

func BenchmarkStats(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	// Pre-populate with diverse data
	for i := 0; i < 100; i++ {
		source := fmt.Sprintf("source-%d", i%5)
		classification := []string{"public", "private", "secret"}[i%3]

		msgID, _ := store.SaveMessage(
			source, nil, "bench-conv", nil, fmt.Sprintf("msg-%d", i),
			"user", fmt.Sprintf("content %d", i), nil, classification, false,
			`[]`, `{}`, nil,
		)

		embedder := NewLocalHashingEmbedder(128)
		embs, _ := embedder.Embed([]string{fmt.Sprintf("content %d", i)})
		store.SaveChunk(msgID, 0, fmt.Sprintf("content %d", i), embedder.Name(), embedder.Model(), embs[0])
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		store.Stats()
	}
}

func BenchmarkGetMessage(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	// Pre-create messages
	messageIDs := make([]string, 100)
	for i := 0; i < 100; i++ {
		msgID, _ := store.SaveMessage(
			"bench-source", nil, "bench-conv", nil, fmt.Sprintf("msg-%d", i),
			"user", fmt.Sprintf("content %d", i), nil, "general", false,
			`[]`, `{}`, nil,
		)
		messageIDs[i] = msgID
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		store.GetMessage(messageIDs[i%100])
	}
}

func BenchmarkGetChunks(b *testing.B) {
	store := setupTestDB(&testing.T{})
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Pre-create messages with chunks
	messageIDs := make([]string, 100)
	for i := 0; i < 100; i++ {
		msgID, _ := store.SaveMessage(
			"bench-source", nil, "bench-conv", nil, fmt.Sprintf("msg-%d", i),
			"user", fmt.Sprintf("content %d", i), nil, "general", false,
			`[]`, `{}`, nil,
		)
		messageIDs[i] = msgID

		// Add 5 chunks per message
		for j := 0; j < 5; j++ {
			embs, _ := embedder.Embed([]string{fmt.Sprintf("chunk %d", j)})
			store.SaveChunk(msgID, j, fmt.Sprintf("chunk %d", j), embedder.Name(), embedder.Model(), embs[0])
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		store.GetChunks(messageIDs[i%100])
	}
}

func BenchmarkVectorToJSON(b *testing.B) {
	vector := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		VectorToJSON(vector)
	}
}

func BenchmarkJSONToVector(b *testing.B) {
	json := `[0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9,1.0]`

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		JSONToVector(json)
	}
}
