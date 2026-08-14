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

	if err := requireExplicitSourceScope(opts.AllSources, opts.SourceFilters); err != nil {
		return nil, err
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

	// Add source filter unless the caller explicitly asked to span every
	// source. requireExplicitSourceScope above guarantees exactly one of
	// these two branches describes a deliberate caller choice.
	if !opts.AllSources {
		placeholders := make([]string, len(opts.SourceFilters))
		for i, src := range opts.SourceFilters {
			placeholders[i] = "?"
			args = append(args, src)
		}
		query += " AND m.source IN (" + strings.Join(placeholders, ", ") + ")"
	}

	// Only compare vectors produced by the same provider, model and
	// dimension. SECURITY.md: "Re-ingest after provider/model/dimension
	// changes and record exact model identity because the demo cannot safely
	// distinguish reused names; mismatched stored dimensions are excluded."
	// Excluding them in SQL (rather than scoring them as 0.0 and letting
	// top-k decide) keeps a differently-embedded corpus from surfacing at
	// all on a sparse store.
	query += " AND c.embedding_provider = ? AND c.embedding_model = ? AND c.embedding_dimensions = ?"
	args = append(args,
		opts.EmbeddingProvider.Name(),
		opts.EmbeddingProvider.Model(),
		opts.EmbeddingProvider.Dimensions())

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cannot query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Pre-allocate results with reasonable capacity (topK is default 10, but can be larger)
	// Most searches return fewer results, so cap initial allocation
	capacity := topK
	if capacity > 100 {
		capacity = 100 // Limit initial allocation to avoid wasteful memory for small searches
	}
	results := make([]resultWithScore, 0, capacity)

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

		// Deserialize embedding (optimized for common case)
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

	// Early return if no results
	if len(results) == 0 {
		return []*SearchResult{}, nil
	}

	// Sort by similarity (descending)
	sortByScore(results)

	// Limit to top-k
	if len(results) > topK {
		results = results[:topK]
	}

	// Convert to SearchResult (with pre-allocated capacity)
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
	defer func() { _ = rows.Close() }()

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
	if !opts.AllSources {
		sourceFilterJSON = strings.Join(opts.SourceFilters, ",")
	}

	// Caller attribution is recorded exactly as supplied. An absent value is
	// stored empty rather than as a plausible-looking placeholder: an audit
	// row that reads "unknown" is indistinguishable from one where an agent
	// genuinely called itself that, which is worse than a visibly blank field.
	taskID := opts.TaskID
	agent := opts.Agent

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
	defer func() { _ = rows.Close() }()

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

// requireExplicitSourceScope refuses a retrieval whose source scope was left
// to inference.
//
// The knowledge store defaults to a single shared database for every project
// that has not created its own partition, so "no source filter" is not a
// neutral default -- it is a cross-project read. roster/knowledge-store/
// SECURITY.md requires that such a read be an explicit caller choice
// (--all-sources) rather than the consequence of omitting --source, and that
// supplying both be refused as ambiguous rather than silently resolved.
func requireExplicitSourceScope(allSources bool, sourceFilters []string) error {
	if allSources && len(sourceFilters) > 0 {
		return fmt.Errorf(
			"source scope is ambiguous: pass source filters or all-sources, not both")
	}
	if !allSources {
		if len(sourceFilters) == 0 {
			return fmt.Errorf(
				"source scope is required: pass at least one source filter, " +
					"or set AllSources to deliberately span every source in the store")
		}
		for _, src := range sourceFilters {
			if strings.TrimSpace(src) == "" {
				return fmt.Errorf("source filter entries must be non-empty")
			}
		}
	}
	return nil
}

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
