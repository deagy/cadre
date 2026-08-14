//go:build cgo
// +build cgo

package knowledge

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func setupFTS5TestDB() *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		panic(err)
	}

	// Create embeddings table for HNSW
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS embeddings (
		message_id TEXT PRIMARY KEY,
		embedding BLOB,
		deleted INTEGER DEFAULT 0
	)
	`
	db.Exec(createTableSQL)

	return db
}

func TestFTS5IndexInitialize(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	fts5 := NewFTS5Index(db)
	err := fts5.Initialize()

	if err != nil {
		t.Fatalf("Failed to initialize FTS5: %v", err)
	}

	if !fts5.ready {
		t.Error("FTS5 should be ready after initialization")
	}
}

func TestFTS5IndexDocument(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	fts5 := NewFTS5Index(db)
	fts5.Initialize()

	doc := &DocumentMetadata{
		MessageID:      "msg-1",
		Title:          "Test Document",
		Content:        "This is a test content for full-text search",
		Classification: "internal",
		Source:         "test-source",
		Timestamp:      time.Now(),
	}

	err := fts5.IndexDocument(doc)
	if err != nil {
		t.Fatalf("Failed to index document: %v", err)
	}

	count := fts5.GetDocumentCount()
	if count != 1 {
		t.Errorf("Expected 1 document, got %d", count)
	}
}

func TestFTS5FullTextSearch(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	fts5 := NewFTS5Index(db)
	fts5.Initialize()

	// Index documents
	docs := []struct {
		id      string
		title   string
		content string
	}{
		{"msg-1", "Search Basics", "Learn how to search efficiently"},
		{"msg-2", "Advanced Search", "Advanced search techniques and tips"},
		{"msg-3", "Database Query", "Query optimization for databases"},
	}

	for _, doc := range docs {
		fts5.IndexDocument(&DocumentMetadata{
			MessageID:      doc.id,
			Title:          doc.title,
			Content:        doc.content,
			Classification: "internal",
			Source:         "test",
			Timestamp:      time.Now(),
		})
	}

	// Search for "search"
	results, err := fts5.FullTextSearch("search", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected search results")
	}

	// Results should include msg-1 and msg-2
	found := 0
	for _, result := range results {
		if result.MessageID == "msg-1" || result.MessageID == "msg-2" {
			found++
		}
	}

	if found < 1 {
		t.Errorf("Expected to find search-related documents, got %d", found)
	}
}

func TestFTS5FilteredSearch(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	fts5 := NewFTS5Index(db)
	fts5.Initialize()

	// Index documents with different classifications
	docs := []struct {
		id             string
		content        string
		classification string
	}{
		{"msg-1", "confidential data", "confidential"},
		{"msg-2", "internal memo", "internal"},
		{"msg-3", "public announcement", "public"},
	}

	for _, doc := range docs {
		fts5.IndexDocument(&DocumentMetadata{
			MessageID:      doc.id,
			Title:          fmt.Sprintf("Doc %s", doc.id),
			Content:        doc.content,
			Classification: doc.classification,
			Source:         "test",
			Timestamp:      time.Now(),
		})
	}

	// Search only in "internal" classification
	results, err := fts5.FilteredSearch("memo", "internal", 10)
	if err != nil {
		t.Fatalf("Filtered search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected filtered results")
	}

	for _, result := range results {
		if result.Classification != "internal" {
			t.Errorf("Expected internal classification, got %s", result.Classification)
		}
	}
}

func TestFTS5DeleteDocument(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	fts5 := NewFTS5Index(db)
	fts5.Initialize()

	doc := &DocumentMetadata{
		MessageID:      "msg-1",
		Title:          "Test",
		Content:        "Test content",
		Classification: "internal",
		Source:         "test",
		Timestamp:      time.Now(),
	}

	fts5.IndexDocument(doc)

	if fts5.GetDocumentCount() != 1 {
		t.Error("Document should be indexed")
	}

	err := fts5.DeleteDocument("msg-1")
	if err != nil {
		t.Fatalf("Failed to delete document: %v", err)
	}

	if fts5.GetDocumentCount() != 0 {
		t.Error("Document count should be 0 after deletion")
	}
}

func TestHybridSearcherVectorOnly(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	hnsw := NewHSNWIndex(16, 200)
	fts5 := NewFTS5Index(db)
	fts5.Initialize()

	// Insert vectors
	hnsw.Insert("msg-1", []float32{1.0, 0.0, 0.0})
	hnsw.Insert("msg-2", []float32{0.9, 0.1, 0.0})
	hnsw.Insert("msg-3", []float32{0.5, 0.5, 0.0})

	searcher := NewHybridSearcher(hnsw, fts5)

	query := &HybridSearchQuery{
		QueryEmbedding: []float32{1.0, 0.0, 0.0},
		VectorWeight:   1.0,
		TextWeight:     0.0,
		TopK:           2,
	}

	results, err := searcher.Search(query)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected vector search results")
	}

	if len(results) > 2 {
		t.Errorf("Expected at most 2 results, got %d", len(results))
	}
}

func TestHybridSearcherTextOnly(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	hnsw := NewHSNWIndex(16, 200)
	fts5 := NewFTS5Index(db)
	fts5.Initialize()

	// Index documents
	for i := 1; i <= 3; i++ {
		fts5.IndexDocument(&DocumentMetadata{
			MessageID:      fmt.Sprintf("msg-%d", i),
			Title:          fmt.Sprintf("Title %d", i),
			Content:        "test content search query",
			Classification: "internal",
			Source:         "test",
			Timestamp:      time.Now(),
		})
	}

	searcher := NewHybridSearcher(hnsw, fts5)

	query := &HybridSearchQuery{
		QueryText:    "search",
		VectorWeight: 0.0,
		TextWeight:   1.0,
		TopK:         2,
	}

	results, err := searcher.Search(query)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected text search results")
	}
}

func TestHybridSearcherCombined(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	hnsw := NewHSNWIndex(16, 200)
	fts5 := NewFTS5Index(db)
	fts5.Initialize()

	// Insert vectors
	hnsw.Insert("msg-1", []float32{1.0, 0.0, 0.0})
	hnsw.Insert("msg-2", []float32{0.9, 0.1, 0.0})

	// Index documents
	for i := 1; i <= 2; i++ {
		fts5.IndexDocument(&DocumentMetadata{
			MessageID:      fmt.Sprintf("msg-%d", i),
			Title:          fmt.Sprintf("Title %d", i),
			Content:        "search content",
			Classification: "internal",
			Source:         "test",
			Timestamp:      time.Now(),
		})
	}

	searcher := NewHybridSearcher(hnsw, fts5)

	query := &HybridSearchQuery{
		QueryEmbedding: []float32{1.0, 0.0, 0.0},
		QueryText:      "search",
		VectorWeight:   0.5,
		TextWeight:     0.5,
		TopK:           2,
	}

	results, err := searcher.Search(query)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected hybrid search results")
	}

	// Verify combined score is calculated
	for _, result := range results {
		if result.CombinedScore <= 0 {
			t.Error("Combined score should be positive")
		}
	}
}

func TestHybridSearcherStats(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	hnsw := NewHSNWIndex(16, 200)
	fts5 := NewFTS5Index(db)
	fts5.Initialize()

	searcher := NewHybridSearcher(hnsw, fts5)

	hnsw.Insert("msg-1", []float32{1.0, 0.0, 0.0})

	fts5.IndexDocument(&DocumentMetadata{
		MessageID:      "msg-1",
		Title:          "Test",
		Content:        "test content",
		Classification: "internal",
		Source:         "test",
		Timestamp:      time.Now(),
	})

	// Perform searches
	searcher.Search(&HybridSearchQuery{
		QueryEmbedding: []float32{1.0, 0.0, 0.0},
		VectorWeight:   1.0,
		TextWeight:     0.0,
		TopK:           1,
	})

	searcher.Search(&HybridSearchQuery{
		QueryText:    "test",
		VectorWeight: 0.0,
		TextWeight:   1.0,
		TopK:         1,
	})

	searcher.Search(&HybridSearchQuery{
		QueryEmbedding: []float32{1.0, 0.0, 0.0},
		QueryText:      "test",
		VectorWeight:   0.5,
		TextWeight:     0.5,
		TopK:           1,
	})

	stats := searcher.GetStats()

	if stats.VectorQueries != 2 {
		t.Errorf("Expected 2 vector queries, got %d", stats.VectorQueries)
	}

	if stats.TextQueries != 2 {
		t.Errorf("Expected 2 text queries, got %d", stats.TextQueries)
	}

	if stats.HybridQueries != 3 {
		t.Errorf("Expected 3 hybrid queries, got %d", stats.HybridQueries)
	}

	if stats.DocumentsIndexed != 1 {
		t.Errorf("Expected 1 document indexed, got %d", stats.DocumentsIndexed)
	}
}

func TestHybridSearcherRankingStrategy(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	hnsw := NewHSNWIndex(16, 200)
	fts5 := NewFTS5Index(db)
	fts5.Initialize()

	// Insert vectors with known similarities
	hnsw.Insert("msg-1", []float32{1.0, 0.0})
	hnsw.Insert("msg-2", []float32{0.5, 0.5})

	fts5.IndexDocument(&DocumentMetadata{
		MessageID:      "msg-1",
		Title:          "High relevance",
		Content:        "high relevance content",
		Classification: "confidential",
		Source:         "test",
		Timestamp:      time.Now(),
	})

	fts5.IndexDocument(&DocumentMetadata{
		MessageID:      "msg-2",
		Title:          "Low relevance",
		Content:        "low relevance content",
		Classification: "internal",
		Source:         "test",
		Timestamp:      time.Now(),
	})

	searcher := NewHybridSearcher(hnsw, fts5)

	// Create results
	results := []HybridSearchResult{
		{MessageID: "msg-1", VectorSimilarity: 0.9, TextRelevance: 50.0, Classification: "confidential"},
		{MessageID: "msg-2", VectorSimilarity: 0.5, TextRelevance: 30.0, Classification: "internal"},
	}

	// Apply strategy with boost for confidential
	strategy := &RankingStrategy{
		Name:                "boost-confidential",
		VectorWeight:        0.6,
		TextWeight:          0.4,
		BoostClassification: "confidential",
		BoostFactor:         1.5,
	}

	reranked := searcher.ApplyRankingStrategy(results, strategy)

	// Boosted result should be first
	if reranked[0].MessageID != "msg-1" {
		t.Errorf("Expected msg-1 first, got %s", reranked[0].MessageID)
	}

	// Verify rank positions updated
	for i, result := range reranked {
		if result.RankPosition != i+1 {
			t.Errorf("Expected rank %d, got %d", i+1, result.RankPosition)
		}
	}
}

func TestHybridSearcherClassificationFilter(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	hnsw := NewHSNWIndex(16, 200)
	fts5 := NewFTS5Index(db)
	fts5.Initialize()

	// Insert documents with different classifications
	for i := 1; i <= 3; i++ {
		classification := "internal"
		if i == 3 {
			classification = "confidential"
		}

		hnsw.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 3.0, 0.5})

		fts5.IndexDocument(&DocumentMetadata{
			MessageID:      fmt.Sprintf("msg-%d", i),
			Title:          fmt.Sprintf("Document %d", i),
			Content:        "search test content",
			Classification: classification,
			Source:         "test",
			Timestamp:      time.Now(),
		})
	}

	searcher := NewHybridSearcher(hnsw, fts5)

	// Search with classification filter
	query := &HybridSearchQuery{
		QueryText:      "search",
		Classification: "internal",
		VectorWeight:   0.0,
		TextWeight:     1.0,
		TopK:           10,
	}

	results, err := searcher.Search(query)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Results should only contain internal documents
	for _, result := range results {
		if result.Classification != "internal" {
			t.Errorf("Expected internal, got %s", result.Classification)
		}
	}
}

func TestHybridSearcherLargeScale(t *testing.T) {
	db := setupFTS5TestDB()
	defer db.Close()

	hnsw := NewHSNWIndex(16, 200)
	fts5 := NewFTS5Index(db)
	fts5.Initialize()

	// Insert 100 documents
	for i := 1; i <= 100; i++ {
		msgID := fmt.Sprintf("msg-%d", i)

		// Insert vector
		hnsw.Insert(msgID, []float32{
			float32(i%10) / 10.0,
			float32((i+1)%10) / 10.0,
		})

		// Index document
		fts5.IndexDocument(&DocumentMetadata{
			MessageID:      msgID,
			Title:          fmt.Sprintf("Document %d", i),
			Content:        "search query test content data",
			Classification: "internal",
			Source:         "test",
			Timestamp:      time.Now(),
		})
	}

	searcher := NewHybridSearcher(hnsw, fts5)

	// Perform hybrid search
	query := &HybridSearchQuery{
		QueryEmbedding: []float32{0.5, 0.5},
		QueryText:      "search",
		VectorWeight:   0.5,
		TextWeight:     0.5,
		TopK:           10,
	}

	results, err := searcher.Search(query)
	if err != nil {
		t.Fatalf("Large-scale search failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected results from large-scale search")
	}

	if len(results) > 10 {
		t.Errorf("Expected at most 10 results, got %d", len(results))
	}

	// Verify results are ranked
	for i := 0; i < len(results)-1; i++ {
		if results[i].CombinedScore < results[i+1].CombinedScore {
			t.Error("Results should be sorted by score (descending)")
		}
	}
}
