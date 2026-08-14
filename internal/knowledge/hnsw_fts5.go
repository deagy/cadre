//nolint:errcheck
package knowledge

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// FTS5Index provides full-text search indexing using SQLite FTS5.
// For testing without CGO, uses in-memory document storage.
type FTS5Index struct {
	mu        sync.RWMutex
	db        *sql.DB
	ready     bool
	docCount  int64
	documents map[string]*DocumentMetadata // In-memory fallback
}

// DocumentMetadata holds searchable text metadata.
type DocumentMetadata struct {
	MessageID   string
	Title       string
	Content     string
	Classification string
	Source      string
	Timestamp   time.Time
	Embedding   []float32
}

// FTS5SearchResult represents a full-text search result.
type FTS5SearchResult struct {
	MessageID      string
	Title          string
	Content        string
	Classification string
	Source         string
	Timestamp      time.Time
	Rank           float64 // BM25 rank
	Relevance      float64 // 0-100 relevance score
}

// HybridSearchQuery combines vector and text search.
type HybridSearchQuery struct {
	QueryEmbedding    []float32 // Vector for semantic search
	QueryText         string    // Text for full-text search
	Classification    string    // Filter by classification
	MinVectorScore    float64   // Minimum vector similarity
	MinTextScore      float64   // Minimum text score
	VectorWeight      float64   // 0-1 weight for vector results
	TextWeight        float64   // 0-1 weight for text results
	TopK              int       // Number of results
	IncludeScores     bool      // Include similarity scores
}

// HybridSearchResult represents combined search result.
type HybridSearchResult struct {
	MessageID         string
	VectorSimilarity  float64 // From HNSW search
	TextRelevance     float64 // From FTS5 search
	CombinedScore     float64 // Weighted combination
	Title             string
	Content           string
	Classification    string
	Source            string
	Timestamp         time.Time
	RankPosition      int     // Position in final ranking
}

// HybridSearchStats tracks search performance.
type HybridSearchStats struct {
	TotalQueries       int64
	VectorQueries      int64
	TextQueries        int64
	HybridQueries      int64
	AverageLatencyMs   float64
	CacheHitRate       float64
	DocumentsIndexed   int64
	IndexSizeBytes     int64
	LastUpdateTime     time.Time
}

// NewFTS5Index creates a new full-text search index.
func NewFTS5Index(db *sql.DB) *FTS5Index {
	return &FTS5Index{
		db:        db,
		ready:     false,
		documents: make(map[string]*DocumentMetadata),
	}
}

// Initialize creates FTS5 virtual table and indexes.
// Falls back to in-memory storage if CGO is disabled.
func (fi *FTS5Index) Initialize() error {
	fi.mu.Lock()
	defer fi.mu.Unlock()

	// Try to use SQLite FTS5
	if fi.db != nil {
		// Create FTS5 virtual table
		createTableSQL := `
		CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
			message_id UNINDEXED,
			title,
			content,
			classification,
			source,
			timestamp UNINDEXED,
			tokenize = 'porter'
		)
		`

		_, err := fi.db.Exec(createTableSQL)
		if err == nil {
			fi.ready = true
			return nil
		}
		// If error, fall through to in-memory fallback
	}

	// Fallback to in-memory document storage
	fi.ready = true
	return nil
}

// IndexDocument adds or updates document in FTS5 index.
func (fi *FTS5Index) IndexDocument(meta *DocumentMetadata) error {
	fi.mu.Lock()
	defer fi.mu.Unlock()

	if !fi.ready {
		return fmt.Errorf("FTS5 index not initialized")
	}

	// Try SQLite if available
	if fi.db != nil {
		insertSQL := `
		INSERT INTO documents_fts(message_id, title, content, classification, source, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)
		`

		result, err := fi.db.Exec(insertSQL,
			meta.MessageID,
			meta.Title,
			meta.Content,
			meta.Classification,
			meta.Source,
			meta.Timestamp.Unix(),
		)

		if err == nil {
			rowsAffected, err := result.RowsAffected()
			if err == nil && rowsAffected > 0 {
				fi.docCount++
			}
			return nil
		}
		// Fall through to in-memory if SQLite fails
	}

	// In-memory fallback
	if fi.documents[meta.MessageID] == nil {
		fi.docCount++
	}
	fi.documents[meta.MessageID] = meta

	return nil
}

// FullTextSearch performs FTS5 search.
func (fi *FTS5Index) FullTextSearch(query string, limit int) ([]FTS5SearchResult, error) {
	fi.mu.RLock()
	defer fi.mu.RUnlock()

	if !fi.ready {
		return nil, fmt.Errorf("FTS5 index not initialized")
	}

	// Try SQLite first
	if fi.db != nil {
		searchSQL := `
		SELECT message_id, title, content, classification, source, timestamp, rank
		FROM documents_fts
		WHERE documents_fts MATCH ?
		ORDER BY rank DESC
		LIMIT ?
		`

		rows, err := fi.db.Query(searchSQL, query, limit)
		if err == nil {
			var results []FTS5SearchResult
			for rows.Next() {
				var result FTS5SearchResult
				var timestamp int64
				var rank sql.NullFloat64

				err := rows.Scan(
					&result.MessageID,
					&result.Title,
					&result.Content,
					&result.Classification,
					&result.Source,
					&timestamp,
					&rank,
				)
				if err != nil {
					rows.Close()
					return nil, fmt.Errorf("failed to scan result: %w", err)
				}

				result.Timestamp = time.Unix(timestamp, 0)
				if rank.Valid {
					result.Rank = rank.Float64
					result.Relevance = (rank.Float64 + 1.0) * 50.0 // Normalize to 0-100
				}

				results = append(results, result)
			}
			rows.Close()
			return results, rows.Err()
		}
		// Fall through to in-memory if SQLite fails
	}

	// In-memory fallback: simple substring matching
	var results []FTS5SearchResult
	for _, doc := range fi.documents {
		if containsSubstring(doc.Title, query) || containsSubstring(doc.Content, query) {
			results = append(results, FTS5SearchResult{
				MessageID:      doc.MessageID,
				Title:          doc.Title,
				Content:        doc.Content,
				Classification: doc.Classification,
				Source:         doc.Source,
				Timestamp:      doc.Timestamp,
				Rank:           -1.0,
				Relevance:      75.0,
			})
		}
	}

	// Sort by timestamp (descending) for in-memory results
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Timestamp.After(results[i].Timestamp) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// FilteredSearch performs FTS5 search with classification filter.
func (fi *FTS5Index) FilteredSearch(query, classification string, limit int) ([]FTS5SearchResult, error) {
	fi.mu.RLock()
	defer fi.mu.RUnlock()

	if !fi.ready {
		return nil, fmt.Errorf("FTS5 index not initialized")
	}

	// Try SQLite first
	if fi.db != nil {
		searchSQL := `
		SELECT message_id, title, content, classification, source, timestamp, rank
		FROM documents_fts
		WHERE documents_fts MATCH ? AND classification = ?
		ORDER BY rank DESC
		LIMIT ?
		`

		rows, err := fi.db.Query(searchSQL, query, classification, limit)
		if err == nil {
			var results []FTS5SearchResult
			for rows.Next() {
				var result FTS5SearchResult
				var timestamp int64
				var rank sql.NullFloat64

				err := rows.Scan(
					&result.MessageID,
					&result.Title,
					&result.Content,
					&result.Classification,
					&result.Source,
					&timestamp,
					&rank,
				)
				if err != nil {
					rows.Close()
					return nil, fmt.Errorf("failed to scan result: %w", err)
				}

				result.Timestamp = time.Unix(timestamp, 0)
				if rank.Valid {
					result.Rank = rank.Float64
					result.Relevance = (rank.Float64 + 1.0) * 50.0
				}

				results = append(results, result)
			}
			rows.Close()
			return results, rows.Err()
		}
		// Fall through to in-memory if SQLite fails
	}

	// In-memory fallback
	var results []FTS5SearchResult
	for _, doc := range fi.documents {
		// Match classification if specified, otherwise include all
		classificationMatch := classification == "" || doc.Classification == classification

		if classificationMatch && (containsSubstring(doc.Title, query) || containsSubstring(doc.Content, query)) {
			results = append(results, FTS5SearchResult{
				MessageID:      doc.MessageID,
				Title:          doc.Title,
				Content:        doc.Content,
				Classification: doc.Classification,
				Source:         doc.Source,
				Timestamp:      doc.Timestamp,
				Rank:           -1.0,
				Relevance:      75.0,
			})
		}
	}

	// Sort and limit
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Timestamp.After(results[i].Timestamp) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// DeleteDocument removes document from FTS5 index.
func (fi *FTS5Index) DeleteDocument(messageID string) error {
	fi.mu.Lock()
	defer fi.mu.Unlock()

	if !fi.ready {
		return fmt.Errorf("FTS5 index not initialized")
	}

	// Try SQLite first
	if fi.db != nil {
		deleteSQL := `DELETE FROM documents_fts WHERE message_id = ?`
		result, err := fi.db.Exec(deleteSQL, messageID)
		if err == nil {
			rowsAffected, err := result.RowsAffected()
			if err == nil && rowsAffected > 0 {
				fi.docCount--
			}
			return nil
		}
		// Fall through to in-memory if SQLite fails
	}

	// In-memory fallback
	if fi.documents[messageID] != nil {
		delete(fi.documents, messageID)
		fi.docCount--
	}

	return nil
}

// GetDocumentCount returns total indexed documents.
func (fi *FTS5Index) GetDocumentCount() int64 {
	fi.mu.RLock()
	defer fi.mu.RUnlock()
	return fi.docCount
}

// HybridSearcher combines vector and text search.
type HybridSearcher struct {
	mu        sync.RWMutex
	hnsw      *HSNWIndex
	fts5      *FTS5Index
	cache     map[string][]HybridSearchResult
	cacheSize int
	stats     *HybridSearchStats
}

// NewHybridSearcher creates a hybrid searcher.
func NewHybridSearcher(hnsw *HSNWIndex, fts5 *FTS5Index) *HybridSearcher {
	return &HybridSearcher{
		hnsw:      hnsw,
		fts5:      fts5,
		cache:     make(map[string][]HybridSearchResult),
		cacheSize: 100,
		stats: &HybridSearchStats{
			LastUpdateTime: time.Now(),
		},
	}
}

// Search performs hybrid vector + text search.
func (hs *HybridSearcher) Search(q *HybridSearchQuery) ([]HybridSearchResult, error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	// Normalize weights
	if q.VectorWeight+q.TextWeight == 0 {
		q.VectorWeight = 0.5
		q.TextWeight = 0.5
	}

	totalWeight := q.VectorWeight + q.TextWeight
	vecWeight := q.VectorWeight / totalWeight
	txtWeight := q.TextWeight / totalWeight

	// Perform vector search
	vectorResults := make(map[string]float64)
	vectorDetails := make(map[string]*HSNWSearchResult)
	if q.QueryEmbedding != nil && len(q.QueryEmbedding) > 0 {
		results := hs.hnsw.Search(q.QueryEmbedding, q.TopK*2)
		if results != nil {
			for _, result := range results {
				if float64(result.Distance) >= q.MinVectorScore {
					vectorResults[result.MessageID] = float64(result.Distance)
					vectorDetails[result.MessageID] = result
				}
			}
		}
		hs.stats.VectorQueries++
	}

	// Perform text search
	textResults := make(map[string]float64)
	textDetails := make(map[string]*FTS5SearchResult)
	if q.QueryText != "" {
		results, err := hs.fts5.FilteredSearch(q.QueryText, q.Classification, q.TopK*2)
		if err == nil {
			for i, result := range results {
				if result.Relevance >= q.MinTextScore {
					textResults[result.MessageID] = result.Relevance
					textDetails[result.MessageID] = &results[i]
				}
			}
		}
		hs.stats.TextQueries++
	}

	// Merge and rank results
	combined := make(map[string]float64)
	for msgID, score := range vectorResults {
		combined[msgID] = score * vecWeight
	}
	for msgID, score := range textResults {
		combined[msgID] += score * txtWeight
	}

	// Sort by combined score
	var results []HybridSearchResult
	for msgID, score := range combined {
		result := HybridSearchResult{
			MessageID:     msgID,
			CombinedScore: score,
		}

		// Populate details from text search if available
		if textDetail, ok := textDetails[msgID]; ok {
			result.Title = textDetail.Title
			result.Content = textDetail.Content
			result.Classification = textDetail.Classification
			result.Source = textDetail.Source
			result.Timestamp = textDetail.Timestamp
			result.TextRelevance = textDetail.Relevance
		}

		// Populate vector similarity if available
		if vecDetail, ok := vectorDetails[msgID]; ok {
			result.VectorSimilarity = float64(vecDetail.Distance)
		}

		results = append(results, result)
	}

	// Sort results by score (descending)
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].CombinedScore > results[i].CombinedScore {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Limit to TopK and set rank positions
	if len(results) > q.TopK {
		results = results[:q.TopK]
	}

	for i := range results {
		results[i].RankPosition = i + 1
	}

	hs.stats.HybridQueries++
	hs.stats.DocumentsIndexed = hs.fts5.GetDocumentCount()

	return results, nil
}

// GetStats returns search statistics.
func (hs *HybridSearcher) GetStats() *HybridSearchStats {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	stats := *hs.stats
	stats.LastUpdateTime = time.Now()
	return &stats
}

// ClearCache removes cached results.
func (hs *HybridSearcher) ClearCache() {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.cache = make(map[string][]HybridSearchResult)
}

// RankingStrategy defines how to combine vector and text scores.
type RankingStrategy struct {
	Name            string
	VectorWeight    float64
	TextWeight      float64
	VectorPower     float64 // For non-linear weighting
	TextPower       float64
	BoostClassification string
	BoostFactor     float64
}

// ApplyRankingStrategy reranks results with strategy.
func (hs *HybridSearcher) ApplyRankingStrategy(results []HybridSearchResult, strategy *RankingStrategy) []HybridSearchResult {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	for i := range results {
		// Apply power transformation for non-linear weighting
		vecScore := results[i].VectorSimilarity
		if strategy.VectorPower != 1.0 && vecScore > 0 {
			vecScore = powFloat(vecScore, strategy.VectorPower)
		}

		txtScore := results[i].TextRelevance
		if strategy.TextPower != 1.0 && txtScore > 0 {
			txtScore = powFloat(txtScore, strategy.TextPower)
		}

		// Combine with weights
		newScore := vecScore*strategy.VectorWeight + txtScore*strategy.TextWeight

		// Apply classification boost
		if strategy.BoostClassification != "" && results[i].Classification == strategy.BoostClassification {
			newScore *= strategy.BoostFactor
		}

		results[i].CombinedScore = newScore
	}

	// Re-sort by new scores
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].CombinedScore > results[i].CombinedScore {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Update rank positions
	for i := range results {
		results[i].RankPosition = i + 1
	}

	return results
}

// Helper functions

// containsSubstring checks if text contains query (case-insensitive).
func containsSubstring(text, query string) bool {
	textLower := ""
	queryLower := ""

	for _, c := range text {
		if c >= 'A' && c <= 'Z' {
			textLower += string(c + 32)
		} else {
			textLower += string(c)
		}
	}

	for _, c := range query {
		if c >= 'A' && c <= 'Z' {
			queryLower += string(c + 32)
		} else {
			queryLower += string(c)
		}
	}

	// Simple substring match
	for i := 0; i <= len(textLower)-len(queryLower); i++ {
		match := true
		for j := 0; j < len(queryLower); j++ {
			if textLower[i+j] != queryLower[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// powFloat calculates non-linear weighting.
func powFloat(x, y float64) float64 {
	if x <= 0 {
		return 0
	}
	result := 1.0
	for i := 0; i < int(y*10); i++ {
		result *= x
	}
	return result
}
