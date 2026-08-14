package knowledge

import (
	"fmt"
)

// SaveMessage persists a message record with associated protection metadata.
// Implements UPSERT logic: if the message already exists (by source + conversation_id + message_id),
// it is updated; otherwise a new record is created.
// Returns the message ID (hash) for use in chunk operations.
func (s *Store) SaveMessage(
	source string,
	sourceURI *string,
	conversationID string,
	conversationTitle *string,
	sourceMessageID string,
	role string,
	content string,
	createdAt *string,
	classification string,
	injectionRisk bool,
	redactionsJSON string,
	metadataJSON string,
	retentionUntil *string,
) (string, error) {
	// Compute message ID as deterministic hash
	messageID := hashMessageID(source, conversationID, sourceMessageID)
	contentHash := hashValue(content)
	injectionRiskInt := 0
	if injectionRisk {
		injectionRiskInt = 1
	}

	// UPSERT: insert or update on conflict
	_, err := s.db.Exec(`
		INSERT INTO messages (
			id, source, source_uri, conversation_id, conversation_title,
			source_message_id, role, content, content_hash, created_at, classification,
			injection_risk, redactions_json, metadata_json, ingested_at, retention_until
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source, conversation_id, source_message_id)
		DO UPDATE SET
			source_uri = excluded.source_uri,
			conversation_title = excluded.conversation_title,
			role = excluded.role,
			content = excluded.content,
			content_hash = excluded.content_hash,
			created_at = excluded.created_at,
			classification = excluded.classification,
			injection_risk = excluded.injection_risk,
			redactions_json = excluded.redactions_json,
			metadata_json = excluded.metadata_json,
			retention_until = excluded.retention_until
	`, messageID, source, sourceURI, conversationID, conversationTitle,
		sourceMessageID, role, content, contentHash, createdAt, classification,
		injectionRiskInt, redactionsJSON, metadataJSON, nowISO(), retentionUntil)

	if err != nil {
		return "", fmt.Errorf("cannot save message: %w", err)
	}

	return messageID, nil
}

// SaveChunk persists a chunk of message content with its vector embedding.
// Each chunk is indexed by (message_id, ordinal, embedding_provider, embedding_model),
// allowing multiple chunks per message and multiple embedding models per chunk.
// Chunks are automatically deleted when their parent message is deleted (CASCADE).
func (s *Store) SaveChunk(
	messageID string,
	ordinal int,
	content string,
	embeddingProvider string,
	embeddingModel string,
	embedding []float64,
) error {
	if messageID == "" {
		return fmt.Errorf("cannot save chunk: message_id is required")
	}

	if ordinal < 0 {
		return fmt.Errorf("cannot save chunk: ordinal must be non-negative")
	}

	if len(embedding) == 0 {
		return fmt.Errorf("cannot save chunk: embedding is required")
	}

	// Compute chunk identifiers
	chunkID := hashChunkID(messageID, ordinal, embeddingProvider, embeddingModel)
	contentHash := hashValue(content)
	embeddingJSON := VectorToJSON(embedding)

	// UPSERT: insert or update on conflict
	_, err := s.db.Exec(`
		INSERT INTO chunks (
			id, message_id, ordinal, content, content_hash,
			embedding_provider, embedding_model, embedding_dimensions, embedding_json
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id, ordinal, embedding_provider, embedding_model)
		DO UPDATE SET
			content = excluded.content,
			content_hash = excluded.content_hash,
			embedding_dimensions = excluded.embedding_dimensions,
			embedding_json = excluded.embedding_json
	`, chunkID, messageID, ordinal, content, contentHash,
		embeddingProvider, embeddingModel, len(embedding), embeddingJSON)

	if err != nil {
		return fmt.Errorf("cannot save chunk: %w", err)
	}

	return nil
}

// SaveChunks persists multiple chunks for a message in a single transaction.
// All chunks for a message are typically written together, so this is more efficient
// than individual SaveChunk calls. Returns early on first error.
func (s *Store) SaveChunks(
	messageID string,
	contents []string,
	embeddings [][]float64,
	embeddingProvider string,
	embeddingModel string,
) error {
	if len(contents) != len(embeddings) {
		return fmt.Errorf("cannot save chunks: content and embedding counts mismatch")
	}

	// Begin transaction for atomic write
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("cannot begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Write each chunk
	for ordinal, content := range contents {
		embedding := embeddings[ordinal]

		chunkID := hashChunkID(messageID, ordinal, embeddingProvider, embeddingModel)
		contentHash := hashValue(content)
		embeddingJSON := VectorToJSON(embedding)

		_, err := tx.Exec(`
			INSERT INTO chunks (
				id, message_id, ordinal, content, content_hash,
				embedding_provider, embedding_model, embedding_dimensions, embedding_json
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(message_id, ordinal, embedding_provider, embedding_model)
			DO UPDATE SET
				content = excluded.content,
				content_hash = excluded.content_hash,
				embedding_dimensions = excluded.embedding_dimensions,
				embedding_json = excluded.embedding_json
		`, chunkID, messageID, ordinal, content, contentHash,
			embeddingProvider, embeddingModel, len(embedding), embeddingJSON)

		if err != nil {
			return fmt.Errorf("cannot save chunk %d: %w", ordinal, err)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cannot commit chunk transaction: %w", err)
	}

	return nil
}

// DeleteMessage removes a message and its associated chunks.
// Uses CASCADE delete via foreign key constraint.
func (s *Store) DeleteMessage(messageID string) error {
	result, err := s.db.Exec("DELETE FROM messages WHERE id = ?", messageID)
	if err != nil {
		return fmt.Errorf("cannot delete message: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("cannot get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("message not found: %s", messageID)
	}

	return nil
}

// GetMessage retrieves a message by ID.
func (s *Store) GetMessage(messageID string) (*Message, error) {
	row := s.db.QueryRow(`
		SELECT id, source, source_uri, conversation_id, conversation_title,
			source_message_id, role, content, content_hash, created_at, classification,
			injection_risk, redactions_json, metadata_json, ingested_at, retention_until
		FROM messages WHERE id = ?
	`, messageID)

	var msg Message
	var injectionRiskInt int

	err := row.Scan(
		&msg.ID, &msg.Source, &msg.SourceURI, &msg.ConversationID, &msg.ConversationTitle,
		&msg.SourceMessageID, &msg.Role, &msg.Content, &msg.ContentHash, &msg.CreatedAt,
		&msg.Classification, &injectionRiskInt, &msg.RedactionsJSON, &msg.MetadataJSON,
		&msg.IngestedAt, &msg.RetentionUntil,
	)

	if err != nil {
		return nil, fmt.Errorf("cannot get message: %w", err)
	}

	msg.InjectionRisk = injectionRiskInt != 0
	return &msg, nil
}

// GetChunks retrieves all chunks for a message.
func (s *Store) GetChunks(messageID string) ([]*Chunk, error) {
	rows, err := s.db.Query(`
		SELECT id, message_id, ordinal, content, content_hash,
			embedding_provider, embedding_model, embedding_dimensions, embedding_json
		FROM chunks WHERE message_id = ? ORDER BY ordinal
	`, messageID)

	if err != nil {
		return nil, fmt.Errorf("cannot query chunks: %w", err)
	}
	defer rows.Close()

	var chunks []*Chunk
	for rows.Next() {
		var chunk Chunk
		err := rows.Scan(
			&chunk.ID, &chunk.MessageID, &chunk.Ordinal, &chunk.Content, &chunk.ContentHash,
			&chunk.EmbeddingProvider, &chunk.EmbeddingModel, &chunk.EmbeddingDimensions, &chunk.EmbeddingJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("cannot scan chunk: %w", err)
		}
		chunks = append(chunks, &chunk)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("chunk query error: %w", err)
	}

	return chunks, nil
}

// GetChunk retrieves a specific chunk by message ID and ordinal.
func (s *Store) GetChunk(messageID string, ordinal int) (*Chunk, error) {
	row := s.db.QueryRow(`
		SELECT id, message_id, ordinal, content, content_hash,
			embedding_provider, embedding_model, embedding_dimensions, embedding_json
		FROM chunks WHERE message_id = ? AND ordinal = ?
	`, messageID, ordinal)

	var chunk Chunk
	err := row.Scan(
		&chunk.ID, &chunk.MessageID, &chunk.Ordinal, &chunk.Content, &chunk.ContentHash,
		&chunk.EmbeddingProvider, &chunk.EmbeddingModel, &chunk.EmbeddingDimensions, &chunk.EmbeddingJSON,
	)

	if err != nil {
		return nil, fmt.Errorf("cannot get chunk: %w", err)
	}

	return &chunk, nil
}

// MessageCount returns the total number of messages in the store.
func (s *Store) MessageCount() (int64, error) {
	var count int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	return count, err
}

// ChunkCount returns the total number of chunks in the store.
func (s *Store) ChunkCount() (int64, error) {
	var count int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM chunks").Scan(&count)
	return count, err
}

// helper functions for hashing

// hashMessageID computes the message ID as a hash of the source + conversation_id + message_id.
// This ensures deterministic IDs across multiple ingestion runs.
func hashMessageID(source, conversationID, sourceMessageID string) string {
	key := fmt.Sprintf("%s|%s|%s", source, conversationID, sourceMessageID)
	return hashValue(key)
}

// hashChunkID computes the chunk ID as a hash of message_id + ordinal + embedding provider + model.
func hashChunkID(messageID string, ordinal int, provider, model string) string {
	key := fmt.Sprintf("%s|%d|%s|%s", messageID, ordinal, provider, model)
	return hashValue(key)
}
