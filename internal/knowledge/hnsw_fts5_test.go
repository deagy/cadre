package knowledge

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func setupFTS5TestDB() *sql.DB {
	// Shared cache and a single connection, not a bare ":memory:".
	//
	// database/sql pools connections, and each connection to ":memory:" gets
	// its *own* database. Initialize would create documents_fts on one
	// connection and the search would run on another that had never seen it,
	// so every FTS5 path failed and silently fell back to the in-memory map.
	// These tests therefore passed while asserting nothing about SQLite --
	// which is why a real bug in the SQLite path could not have failed them.
	db, err := sql.Open("sqlite3", "file:fts5test?mode=memory&cache=shared")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1)

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
