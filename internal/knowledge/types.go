// Package knowledge implements the Go foundation for the vectorized knowledge store.
// This is a partial port of roster/knowledge-store/src with focus on database persistence,
// schema management, and embedding interfaces. Full text search, vector operations, and
// retention policies are planned for Phase 4.2+.
package knowledge

import "time"

// Message represents a persisted message record with optional embeddings.
type Message struct {
	ID                   string                 `json:"id"`
	Source               string                 `json:"source"`
	SourceURI            *string                `json:"source_uri,omitempty"`
	ConversationID       string                 `json:"conversation_id"`
	ConversationTitle    *string                `json:"conversation_title,omitempty"`
	SourceMessageID      string                 `json:"source_message_id"`
	Role                 string                 `json:"role"`
	Content              string                 `json:"content"`
	ContentHash          string                 `json:"content_hash"`
	CreatedAt            *string                `json:"created_at,omitempty"`
	Classification       string                 `json:"classification"`
	InjectionRisk        bool                   `json:"injection_risk"`
	RedactionsJSON       string                 `json:"redactions_json"`
	MetadataJSON         string                 `json:"metadata_json"`
	IngestedAt           string                 `json:"ingested_at"`
	RetentionUntil       *string                `json:"retention_until,omitempty"`
}

// Chunk represents a chunked piece of message content with vector embedding.
type Chunk struct {
	ID                  string    `json:"id"`
	MessageID           string    `json:"message_id"`
	Ordinal             int       `json:"ordinal"`
	Content             string    `json:"content"`
	ContentHash         string    `json:"content_hash"`
	EmbeddingProvider   string    `json:"embedding_provider"`
	EmbeddingModel      string    `json:"embedding_model"`
	EmbeddingDimensions int       `json:"embedding_dimensions"`
	EmbeddingJSON       string    `json:"embedding_json"`
}

// IngestionRun tracks a single ingestion operation.
type IngestionRun struct {
	ID           string     `json:"id"`
	Source       string     `json:"source"`
	SourceURI    *string    `json:"source_uri,omitempty"`
	StartedAt    string     `json:"started_at"`
	CompletedAt  *string    `json:"completed_at,omitempty"`
	Status       string     `json:"status"` // "running", "complete", "failed"
	MessageCount int        `json:"message_count"`
	ChunkCount   int        `json:"chunk_count"`
	Error        *string    `json:"error,omitempty"`
}

// RetrievalRun tracks a single search/retrieval operation for analytics.
type RetrievalRun struct {
	ID                string     `json:"id"`
	QueryHash         string     `json:"query_hash"`
	TaskID            string     `json:"task_id"`
	Agent             string     `json:"agent"`
	Classification    string     `json:"classification"`
	SourceFilter      *string    `json:"source_filter,omitempty"`
	EmbeddingProvider string     `json:"embedding_provider"`
	EmbeddingModel    string     `json:"embedding_model"`
	RequestedTop      int        `json:"requested_top"`
	ResultCount       int        `json:"result_count"`
	CreatedAt         string     `json:"created_at"`
}

// StoreStats returns summary statistics about the knowledge store.
type StoreStats struct {
	TotalMessages      int64         `json:"total_messages"`
	TotalChunks        int64         `json:"total_chunks"`
	IngestionRuns      int64         `json:"ingestion_runs"`
	RetrievalRuns      int64         `json:"retrieval_runs"`
	DatabaseSize       int64         `json:"database_size_bytes"`
	CreatedAt          time.Time     `json:"created_at"`
	Sources            map[string]int64 `json:"sources_by_message_count"`
	Classifications    map[string]int64 `json:"classifications_by_message_count"`
	EmbeddingModels    map[string]int64 `json:"embedding_models_by_chunk_count"`
}

// EmbeddingProvider defines the interface for computing vector embeddings.
type EmbeddingProvider interface {
	// Name returns the provider identifier (e.g., "local-hashing", "openai-compatible").
	Name() string
	// Model returns the model identifier (e.g., "text-embedding-3-small").
	Model() string
	// Dimensions returns the vector dimension size.
	Dimensions() int
	// Embed computes embeddings for the given texts, returning vectors in parallel order.
	// Texts with same embeddings are deduped server-side; the returned slice length
	// matches the input length, with deduped values replicated.
	Embed(texts []string) ([][]float64, error)
}

// SearchOptions controls how a retrieval search is executed.
type SearchOptions struct {
	Query           string
	Classification  string
	SourceFilters   []string // If set, restrict to these sources only
	AllSources      bool     // If true, ignore SourceFilters
	Top             int      // Number of results to return (default 10)
	EmbeddingProvider EmbeddingProvider
}

// SearchResult is a single result from a retrieval search.
type SearchResult struct {
	Message       *Message `json:"message"`
	Chunk         *Chunk   `json:"chunk"`
	CosineSimilarity float64 `json:"cosine_similarity"`
}
