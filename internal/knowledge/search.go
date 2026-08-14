package knowledge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// resultWithScore is used internally during search to track similarity.
type resultWithScore struct {
	msg        *Message
	chunk      *Chunk
	similarity float64
}

// Search retrieves messages similar to the query using cosine similarity over embeddings,
// with optional filtering by classification and source.
// Returns results sorted by similarity (highest first).
func (s *Store) Search(opts SearchOptions) ([]*SearchResult, error) {
	if opts.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	if opts.Classification == "" {
		return nil, fmt.Errorf("classification is required")
	}

	if opts.EmbeddingProvider == nil {
		return nil, fmt.Errorf("embedding provider is required")
	}

	// Compute embedding for query
	queryEmbedding, err := opts.EmbeddingProvider.Embed([]string{opts.Query})
	if err != nil {
		return nil, fmt.Errorf("cannot embed query: %w", err)
	}

	if len(queryEmbedding) == 0 {
		return nil, fmt.Errorf("embedding provider returned no embeddings")
	}

	queryVec := queryEmbedding[0]

	// Default top-k value
	topK := opts.Top
	if topK <= 0 {
		topK = 10
	}

	// Build SQL query with filters
	query := `
		SELECT
			m.id, m.source, m.source_uri, m.conversation_id, m.conversation_title,
			m.source_message_id, m.role, m.content, m.content_hash, m.created_at,
			m.classification, m.injection_risk, m.redactions_json, m.metadata_json,
			m.ingested_at, m.retention_until,
			c.id, c.ordinal, c.content, c.embedding_json
		FROM messages m
		JOIN chunks c ON m.id = c.message_id
		WHERE m.classification = ?
	`

	args := []interface{}{opts.Classification}

	// Add source filter if specified
	if !opts.AllSources && len(opts.SourceFilters) > 0 {
		placeholders := make([]string, len(opts.SourceFilters))
		for i, src := range opts.SourceFilters {
			placeholders[i] = "?"
			args = append(args, src)
		}
		query += " AND m.source IN (" + strings.Join(placeholders, ", ") + ")"
	}

	// Add embedding provider filter
	query += " AND c.embedding_provider = ?"
	args = append(args, opts.EmbeddingProvider.Name())

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cannot query messages: %w", err)
	}
	defer rows.Close()

	var results []resultWithScore

	for rows.Next() {
		var msg Message
		var chunk Chunk
		var injectionRiskInt int

		err := rows.Scan(
			&msg.ID, &msg.Source, &msg.SourceURI, &msg.ConversationID, &msg.ConversationTitle,
			&msg.SourceMessageID, &msg.Role, &msg.Content, &msg.ContentHash, &msg.CreatedAt,
			&msg.Classification, &injectionRiskInt, &msg.RedactionsJSON, &msg.MetadataJSON,
			&msg.IngestedAt, &msg.RetentionUntil,
			&chunk.ID, &chunk.Ordinal, &chunk.Content, &chunk.EmbeddingJSON,
		)

		if err != nil {
			return nil, fmt.Errorf("cannot scan result: %w", err)
		}

		msg.InjectionRisk = injectionRiskInt != 0

		// Deserialize embedding
		chunkVec, err := JSONToVector(chunk.EmbeddingJSON)
		if err != nil {
			return nil, fmt.Errorf("cannot deserialize embedding: %w", err)
		}

		// Compute similarity
		similarity := CosineSimilarity(queryVec, chunkVec)

		results = append(results, resultWithScore{
			msg:        &msg,
			chunk:      &chunk,
			similarity: similarity,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query error: %w", err)
	}

	// Sort by similarity (descending)
	sortByScore(results)

	// Limit to top-k
	if len(results) > topK {
		results = results[:topK]
	}

	// Convert to SearchResult
	output := make([]*SearchResult, len(results))
	for i, r := range results {
		output[i] = &SearchResult{
			Message:          r.msg,
			Chunk:            r.chunk,
			CosineSimilarity: r.similarity,
		}
	}

	// Track retrieval run
	_ = s.recordRetrievalRun(opts, len(output))

	return output, nil
}

// SearchByContent searches for messages containing text content.
// This is a simple text search that matches substring queries.
// For full-text search, a MATCH operator or FTS extension would be more efficient.
func (s *Store) SearchByContent(query string, classification string, limit int) ([]*Message, error) {
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	if classification == "" {
		return nil, fmt.Errorf("classification is required")
	}

	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.Query(`
		SELECT DISTINCT
			m.id, m.source, m.source_uri, m.conversation_id, m.conversation_title,
			m.source_message_id, m.role, m.content, m.content_hash, m.created_at,
			m.classification, m.injection_risk, m.redactions_json, m.metadata_json,
			m.ingested_at, m.retention_until
		FROM messages m
		WHERE m.classification = ? AND (m.content LIKE ? OR m.conversation_title LIKE ?)
		LIMIT ?
	`, classification, "%"+query+"%", "%"+query+"%", limit)

	if err != nil {
		return nil, fmt.Errorf("cannot query by content: %w", err)
	}
	defer rows.Close()

	var messages []*Message
	for rows.Next() {
		var msg Message
		var injectionRiskInt int

		err := rows.Scan(
			&msg.ID, &msg.Source, &msg.SourceURI, &msg.ConversationID, &msg.ConversationTitle,
			&msg.SourceMessageID, &msg.Role, &msg.Content, &msg.ContentHash, &msg.CreatedAt,
			&msg.Classification, &injectionRiskInt, &msg.RedactionsJSON, &msg.MetadataJSON,
			&msg.IngestedAt, &msg.RetentionUntil,
		)

		if err != nil {
			return nil, fmt.Errorf("cannot scan message: %w", err)
		}

		msg.InjectionRisk = injectionRiskInt != 0
		messages = append(messages, &msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query error: %w", err)
	}

	return messages, nil
}

// recordRetrievalRun tracks a search operation for analytics.
func (s *Store) recordRetrievalRun(opts SearchOptions, resultCount int) error {
	runID := generateUUID()
	queryHash := hashQueryString(opts.Query)
	sourceFilterJSON := ""
	if !opts.AllSources && len(opts.SourceFilters) > 0 {
		sourceFilterJSON = strings.Join(opts.SourceFilters, ",")
	}

	// For now, we don't have a task_id or agent, so use defaults
	taskID := "unknown"
	agent := "unknown"

	_, err := s.db.Exec(`
		INSERT INTO retrieval_runs (
			id, query_hash, task_id, agent, classification,
			source_filter, embedding_provider, embedding_model,
			requested_top, result_count, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		runID, queryHash, taskID, agent, opts.Classification,
		sourceFilterJSON, opts.EmbeddingProvider.Name(), opts.EmbeddingProvider.Model(),
		opts.Top, resultCount, nowISO())

	return err
}

// GetSearchStats returns statistics about retrieval runs.
func (s *Store) GetSearchStats(classification string) (map[string]int64, error) {
	rows, err := s.db.Query(`
		SELECT embedding_model, COUNT(*) as count
		FROM retrieval_runs
		WHERE classification = ?
		GROUP BY embedding_model
		ORDER BY count DESC
	`, classification)

	if err != nil {
		return nil, fmt.Errorf("cannot query search stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]int64)
	for rows.Next() {
		var model string
		var count int64
		if err := rows.Scan(&model, &count); err != nil {
			return nil, err
		}
		stats[model] = count
	}

	return stats, rows.Err()
}

// helper functions

// sortByScore sorts results in descending order by similarity score.
func sortByScore(results []resultWithScore) {
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].similarity > results[i].similarity {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

// hashQueryString computes a hash of the query for analytics.
func hashQueryString(query string) string {
	h := sha256.Sum256([]byte(query))
	return hex.EncodeToString(h[:])
}

// generateUUID generates a UUID4-like string.
func generateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use hash of current time
		return "uuid-" + hashValue(fmt.Sprintf("%d", time.Now().UnixNano()))[:8]
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
