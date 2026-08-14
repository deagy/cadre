package knowledge

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store manages persistent knowledge store operations via SQLite.
// Equivalent to Python's database.py open_store() context manager.
type Store struct {
	db *sql.DB
	path string
}

// schema defines the SQLite database schema, matching the Python implementation.
const schema = `
CREATE TABLE IF NOT EXISTS ingestion_runs (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL,
  source_uri TEXT,
  started_at TEXT NOT NULL,
  completed_at TEXT,
  status TEXT NOT NULL,
  message_count INTEGER NOT NULL DEFAULT 0,
  chunk_count INTEGER NOT NULL DEFAULT 0,
  error TEXT
);

CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL,
  source_uri TEXT,
  conversation_id TEXT NOT NULL,
  conversation_title TEXT,
  source_message_id TEXT NOT NULL,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  created_at TEXT,
  classification TEXT NOT NULL,
  injection_risk INTEGER NOT NULL DEFAULT 0,
  redactions_json TEXT NOT NULL,
  metadata_json TEXT NOT NULL,
  ingested_at TEXT NOT NULL,
  retention_until TEXT,
  UNIQUE(source, conversation_id, source_message_id)
);

CREATE TABLE IF NOT EXISTS chunks (
  id TEXT PRIMARY KEY,
  message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL,
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  embedding_provider TEXT NOT NULL,
  embedding_model TEXT NOT NULL,
  embedding_dimensions INTEGER NOT NULL,
  embedding_json TEXT NOT NULL,
  UNIQUE(message_id, ordinal, embedding_provider, embedding_model)
);

CREATE TABLE IF NOT EXISTS retrieval_runs (
  id TEXT PRIMARY KEY,
  query_hash TEXT NOT NULL,
  task_id TEXT NOT NULL,
  agent TEXT NOT NULL,
  classification TEXT NOT NULL,
  source_filter TEXT,
  embedding_provider TEXT NOT NULL,
  embedding_model TEXT NOT NULL,
  requested_top INTEGER NOT NULL,
  result_count INTEGER NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_source ON messages(source);
CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id);
CREATE INDEX IF NOT EXISTS idx_messages_classification ON messages(classification);
CREATE INDEX IF NOT EXISTS idx_chunks_model ON chunks(embedding_provider, embedding_model);
CREATE INDEX IF NOT EXISTS idx_retrieval_runs_task ON retrieval_runs(task_id, agent);
`

// Open opens or creates a knowledge store at the given path.
// Parent directories are created as needed. Returns an error if the database
// cannot be opened or if schema initialization fails.
func Open(dbPath string) (*Store, error) {
	// Create parent directories if needed
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create knowledge store directory: %w", err)
	}

	// Open SQLite database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open knowledge store database: %w", err)
	}

	// Configure SQLite
	if err := configureDB(db); err != nil {
		db.Close()
		return nil, err
	}

	// Initialize schema
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db, path: dbPath}, nil
}

// configureDB sets SQLite pragmas for consistency and performance.
func configureDB(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("failed to set pragma: %w", err)
		}
	}
	return nil
}

// initSchema creates tables and indexes. Idempotent: safe to call multiple times.
func initSchema(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("cannot initialize schema: %w", err)
	}
	// Add any new columns via migration function (for schema evolution)
	if err := migrateAdditiveColumns(db); err != nil {
		return fmt.Errorf("cannot migrate schema: %w", err)
	}
	return nil
}

// migrateAdditiveColumns adds new columns introduced after initial schema creation.
// Matches Python's _migrate_additive_columns(). Currently no-op but structure
// is in place for future schema evolution (e.g., adding retention_until column).
func migrateAdditiveColumns(db *sql.DB) error {
	// Future: add logic to ALTER TABLE and add columns as schema evolves
	// For now, retention_until is part of the base schema, so this is a no-op
	return nil
}

// Close closes the database connection. Safe to call multiple times.
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Stats returns summary statistics about the store.
func (s *Store) Stats() (*StoreStats, error) {
	stats := &StoreStats{
		CreatedAt:        time.Now(),
		Sources:          make(map[string]int64),
		Classifications:  make(map[string]int64),
		EmbeddingModels:  make(map[string]int64),
	}

	// Total messages, chunks, runs
	row := s.db.QueryRow("SELECT COUNT(*) FROM messages")
	if err := row.Scan(&stats.TotalMessages); err != nil {
		return nil, fmt.Errorf("cannot count messages: %w", err)
	}

	row = s.db.QueryRow("SELECT COUNT(*) FROM chunks")
	if err := row.Scan(&stats.TotalChunks); err != nil {
		return nil, fmt.Errorf("cannot count chunks: %w", err)
	}

	row = s.db.QueryRow("SELECT COUNT(*) FROM ingestion_runs")
	if err := row.Scan(&stats.IngestionRuns); err != nil {
		return nil, fmt.Errorf("cannot count ingestion runs: %w", err)
	}

	row = s.db.QueryRow("SELECT COUNT(*) FROM retrieval_runs")
	if err := row.Scan(&stats.RetrievalRuns); err != nil {
		return nil, fmt.Errorf("cannot count retrieval runs: %w", err)
	}

	// Database file size
	fi, err := os.Stat(s.path)
	if err == nil {
		stats.DatabaseSize = fi.Size()
	}

	// Breakdowns by source and classification
	rows, err := s.db.Query("SELECT source, COUNT(*) FROM messages GROUP BY source")
	if err != nil {
		return nil, fmt.Errorf("cannot query sources: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var source string
		var count int64
		if err := rows.Scan(&source, &count); err != nil {
			return nil, err
		}
		stats.Sources[source] = count
	}

	rows, err = s.db.Query("SELECT classification, COUNT(*) FROM messages GROUP BY classification")
	if err != nil {
		return nil, fmt.Errorf("cannot query classifications: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var classification string
		var count int64
		if err := rows.Scan(&classification, &count); err != nil {
			return nil, err
		}
		stats.Classifications[classification] = count
	}

	rows, err = s.db.Query("SELECT embedding_provider || '/' || embedding_model, COUNT(*) FROM chunks GROUP BY embedding_provider, embedding_model")
	if err != nil {
		return nil, fmt.Errorf("cannot query embedding models: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		var count int64
		if err := rows.Scan(&model, &count); err != nil {
			return nil, err
		}
		stats.EmbeddingModels[model] = count
	}

	return stats, nil
}

// hashValue computes SHA256 hash of a string, matching Python's _hash().
func hashValue(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// nowISO returns current UTC time in ISO format with millisecond precision, matching Python's _now().
func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// BeginRun starts a new ingestion run and returns its ID.
func (s *Store) BeginRun(source, sourceURI string) (string, error) {
	runID := newUUID()
	_, err := s.db.Exec(`
		INSERT INTO ingestion_runs (id, source, source_uri, started_at, status)
		VALUES (?, ?, ?, ?, 'running')
	`, runID, source, sourceURI, nowISO())
	if err != nil {
		return "", fmt.Errorf("cannot begin ingestion run: %w", err)
	}
	return runID, nil
}

// CompleteRun marks an ingestion run as complete.
func (s *Store) CompleteRun(runID string, messageCount, chunkCount int) error {
	_, err := s.db.Exec(`
		UPDATE ingestion_runs
		SET completed_at = ?, status = 'complete', message_count = ?, chunk_count = ?
		WHERE id = ?
	`, nowISO(), messageCount, chunkCount, runID)
	if err != nil {
		return fmt.Errorf("cannot complete ingestion run: %w", err)
	}
	return nil
}

// FailRun marks an ingestion run as failed.
func (s *Store) FailRun(runID string, err error) error {
	errMsg := err.Error()
	if len(errMsg) > 2000 {
		errMsg = errMsg[:2000]
	}
	_, dbErr := s.db.Exec(`
		UPDATE ingestion_runs
		SET completed_at = ?, status = 'failed', error = ?
		WHERE id = ?
	`, nowISO(), errMsg, runID)
	if dbErr != nil {
		return fmt.Errorf("cannot fail ingestion run: %w", dbErr)
	}
	return nil
}

// helper to generate UUIDs (using crypto/rand in production)
func newUUID() string {
	b := make([]byte, 16)
	if n, _ := rand.Read(b); n < 16 {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
