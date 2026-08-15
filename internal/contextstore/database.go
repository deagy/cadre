// database.go ports database.py: SQLite persistence for the context store.
//
// A separate database file from the knowledge store, not a second set of
// tables inside knowledge.db -- with two physically distinct files a
// cross-store JOIN cannot be written, which is what turns "no path exists
// from working context into the curated corpus without a steward
// disposition" into a property of the deployment rather than a claim in a
// document.
package contextstore

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newUUID mints a random (v4) UUID string, matching
// internal/knowledge/database.go's newUUID convention -- kept local rather
// than adding a github.com/google/uuid dependency neither package
// otherwise needs.
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func ContentHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func NowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func nowISOBeforeDays(days int) string {
	return time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format("2006-01-02T15:04:05.000Z")
}

const schema = `
CREATE TABLE IF NOT EXISTS entries (
  handle TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  source TEXT NOT NULL,
  task_id TEXT NOT NULL,
  agent TEXT NOT NULL,
  dispatch_id TEXT,
  label TEXT NOT NULL,
  tags_json TEXT NOT NULL,
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  byte_length INTEGER NOT NULL,
  classification TEXT NOT NULL,
  injection_risk INTEGER NOT NULL DEFAULT 0,
  untrusted_inputs INTEGER NOT NULL DEFAULT 0,
  derived_from_json TEXT NOT NULL,
  redactions_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS entry_chunks (
  id TEXT PRIMARY KEY,
  handle TEXT NOT NULL REFERENCES entries(handle) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL, content TEXT NOT NULL, content_hash TEXT NOT NULL,
  embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
  embedding_dimensions INTEGER NOT NULL, embedding_json TEXT NOT NULL,
  UNIQUE(handle, ordinal, embedding_provider, embedding_model)
);
CREATE TABLE IF NOT EXISTS access_runs (
  id TEXT PRIMARY KEY, operation TEXT NOT NULL, handle TEXT, query_hash TEXT,
  task_id TEXT NOT NULL, agent TEXT NOT NULL, classification TEXT NOT NULL,
  scope_filter TEXT, source TEXT NOT NULL, result_count INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS expiry_evidence (
  id TEXT PRIMARY KEY, handle TEXT NOT NULL, content_hash TEXT NOT NULL,
  byte_length INTEGER NOT NULL, classification TEXT NOT NULL, source TEXT NOT NULL,
  created_at TEXT NOT NULL, expires_at TEXT NOT NULL, swept_at TEXT NOT NULL,
  reason TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_entries_source ON entries(source);
CREATE INDEX IF NOT EXISTS idx_entries_agent_task ON entries(agent, task_id);
CREATE INDEX IF NOT EXISTS idx_entries_dispatch ON entries(dispatch_id);
CREATE INDEX IF NOT EXISTS idx_entries_expires ON entries(expires_at);
CREATE INDEX IF NOT EXISTS idx_access_runs_task ON access_runs(task_id, agent);
CREATE INDEX IF NOT EXISTS idx_expiry_evidence_handle ON expiry_evidence(handle);
CREATE INDEX IF NOT EXISTS idx_entry_chunks_handle ON entry_chunks(handle);
CREATE INDEX IF NOT EXISTS idx_entry_chunks_model ON entry_chunks(embedding_provider, embedding_model);
`

// Entry mirrors one row of the entries table, decoded from its stored JSON
// sidecar columns.
type Entry struct {
	Handle          string
	Scope           string
	Source          string
	TaskID          string
	Agent           string
	DispatchID      sql.NullString
	Label           string
	Tags            []string
	Content         string
	ContentHash     string
	ByteLength      int
	Classification  string
	InjectionRisk   bool
	UntrustedInputs bool
	DerivedFrom     []string
	Redactions      []string
	CreatedAt       string
	ExpiresAt       string
	PromotedAt      sql.NullString
}

func scanEntry(scanner interface {
	Scan(dest ...any) error
}) (*Entry, error) {
	var e Entry
	var tagsJSON, derivedJSON, redactionsJSON string
	var injectionRisk, untrustedInputs int
	if err := scanner.Scan(
		&e.Handle, &e.Scope, &e.Source, &e.TaskID, &e.Agent, &e.DispatchID, &e.Label,
		&tagsJSON, &e.Content, &e.ContentHash, &e.ByteLength, &e.Classification,
		&injectionRisk, &untrustedInputs, &derivedJSON, &redactionsJSON,
		&e.CreatedAt, &e.ExpiresAt, &e.PromotedAt,
	); err != nil {
		return nil, err
	}
	e.InjectionRisk = injectionRisk != 0
	e.UntrustedInputs = untrustedInputs != 0
	if err := json.Unmarshal([]byte(tagsJSON), &e.Tags); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(derivedJSON), &e.DerivedFrom); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(redactionsJSON), &e.Redactions); err != nil {
		return nil, err
	}
	return &e, nil
}

const entryColumns = `handle, scope, source, task_id, agent, dispatch_id, label, tags_json, content,
	content_hash, byte_length, classification, injection_risk, untrusted_inputs, derived_from_json,
	redactions_json, created_at, expires_at, promoted_at`

// migrateAdditiveColumns brings an older store up to the current shape.
//
// It attempts the ALTER unconditionally and treats "duplicate column name" as
// success, rather than reading PRAGMA table_info first and adding the column
// only when it looks absent.
//
// The check-then-add form was a race. Every OpenStore runs this, and a
// dispatched team opens the store from several processes at once: two openers
// both read the column as missing, both ALTER, and the loser's open failed --
// taking down a dispatch that had done nothing wrong. Tolerating the
// duplicate covers that, but leaves the guard for it probabilistic, because
// the interleaving cannot be forced from a test.
//
// Attempting unconditionally removes the window instead of narrowing it: the
// end state either party wanted is that the column exists, which is exactly
// what "duplicate column name" reports. The cost is one failed statement per
// open on an already-migrated store, which is nothing beside opening the file.
func migrateAdditiveColumns(db *sql.DB) error {
	for _, statement := range []string{
		"ALTER TABLE entries ADD COLUMN promoted_at TEXT",
	} {
		if _, err := db.Exec(statement); err != nil && !isDuplicateColumnError(err) {
			return err
		}
	}
	return nil
}

// isDuplicateColumnError reports whether err is SQLite refusing to add a
// column that already exists.
//
// Matched on the message because mattn/go-sqlite3 reports it as a generic
// SQLITE_ERROR, with no distinct code to test -- so this is deliberately
// narrow: any other schema failure still fails the open.
func isDuplicateColumnError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}

// retryWhileLocked runs an idempotent database step, retrying while SQLite
// reports the file as locked.
//
// Bounded, and it gives up by returning the last error rather than a
// substitute: a store that is genuinely unavailable must fail the open, not
// hang and not succeed with an unmigrated schema.
func retryWhileLocked(step func() error) error {
	delay := time.Millisecond
	var err error
	for attempt := 0; attempt < 12; attempt++ {
		if err = step(); err == nil || !isLockedError(err) {
			return err
		}
		time.Sleep(delay)
		if delay < 200*time.Millisecond {
			delay *= 2
		}
	}
	return err
}

// isLockedError matches SQLite's two lock messages.
//
// Matched on the message for the same reason isDuplicateColumnError is: the
// driver surfaces these as a generic error, and the code that would
// distinguish them is not carried through.
func isLockedError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked")
}

// OpenStore opens (creating if absent) and, by default, sweeps expired
// entries. sweep=false exists for `expire --dry-run`, which needs to read
// the expired set without destroying it.
func OpenStore(databasePath string, sweep bool) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", databasePath+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=10000")
	if err != nil {
		return nil, err
	}
	// Retried, because the schema statements race other openers of the same
	// file. `_busy_timeout` covers an ordinary write that has to wait for a
	// lock, but it does not cover this one: while the first connection is
	// converting the journal to WAL, SQLite returns SQLITE_BUSY to the others
	// without invoking the busy handler at all -- the failure comes back
	// immediately rather than after the ten seconds the timeout suggests.
	//
	// Safe to retry because every statement involved is idempotent: the schema
	// is CREATE ... IF NOT EXISTS throughout, and the migration tolerates a
	// duplicate column for the same reason.
	if err := retryWhileLocked(func() error {
		if _, err := db.Exec(schema); err != nil {
			return err
		}
		return migrateAdditiveColumns(db)
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	if sweep {
		if _, err := SweepExpired(db, ""); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

// ExpiredRows returns every entry whose expires_at is at or before asOf
// (NowISO() if empty).
func ExpiredRows(db *sql.DB, asOf string) ([]*Entry, error) {
	moment := asOf
	if moment == "" {
		moment = NowISO()
	}
	rows, err := db.Query("SELECT "+entryColumns+" FROM entries WHERE expires_at <= ? ORDER BY expires_at, handle", moment)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SweptEvidence is one swept entry's evidence, exactly the shape returned
// to a caller -- never the content.
type SweptEvidence struct {
	Handle         string `json:"handle"`
	ContentHash    string `json:"content_hash"`
	ByteLength     int    `json:"byte_length"`
	Classification string `json:"classification"`
	ExpiresAt      string `json:"expires_at"`
	Reason         string `json:"reason"`
}

// SweepExpired deletes expired entries, recording evidence that they
// existed. Not steward-gated, unlike knowledge-store deletion: context
// expiry destroys working scratch whose entire contract is that it
// expires.
func SweepExpired(db *sql.DB, asOf string) ([]SweptEvidence, error) {
	moment := asOf
	if moment == "" {
		moment = NowISO()
	}
	rows, err := ExpiredRows(db, moment)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	swept := make([]SweptEvidence, 0, len(rows))
	for _, row := range rows {
		if err := insertExpiryEvidence(tx, row, moment, "ttl-expiry"); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if _, err := tx.Exec("DELETE FROM entries WHERE handle = ?", row.Handle); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		swept = append(swept, SweptEvidence{
			Handle: row.Handle, ContentHash: row.ContentHash, ByteLength: row.ByteLength,
			Classification: row.Classification, ExpiresAt: row.ExpiresAt, Reason: "ttl-expiry",
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return swept, nil
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func insertExpiryEvidence(x execer, row *Entry, sweptAt, reason string) error {
	_, err := x.Exec(
		`INSERT INTO expiry_evidence (id, handle, content_hash, byte_length,
			classification, source, created_at, expires_at, swept_at, reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newUUID(), row.Handle, row.ContentHash, row.ByteLength,
		row.Classification, row.Source, row.CreatedAt, row.ExpiresAt, sweptAt, reason,
	)
	return err
}

// InsertEntry inserts entry within tx.
func InsertEntry(tx *sql.Tx, entry *Entry) error {
	tagsJSON, err := json.Marshal(entry.Tags)
	if err != nil {
		return err
	}
	derivedJSON, err := json.Marshal(entry.DerivedFrom)
	if err != nil {
		return err
	}
	redactionsJSON, err := json.Marshal(entry.Redactions)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`INSERT INTO entries (handle, scope, source, task_id, agent, dispatch_id, label,
			tags_json, content, content_hash, byte_length, classification, injection_risk,
			untrusted_inputs, derived_from_json, redactions_json, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Handle, entry.Scope, entry.Source, entry.TaskID, entry.Agent, entry.DispatchID,
		entry.Label, string(tagsJSON), entry.Content, entry.ContentHash, entry.ByteLength,
		entry.Classification, boolToInt(entry.InjectionRisk), boolToInt(entry.UntrustedInputs),
		string(derivedJSON), string(redactionsJSON), entry.CreatedAt, entry.ExpiresAt,
	)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ReplaceChunks writes an entry's chunks within tx, replacing any it
// already had -- so re-indexing under a changed configuration cannot leave
// old and new vectors scored against each other as if comparable.
func ReplaceChunks(tx *sql.Tx, handle string, chunks []string, vectors [][]float64, embedding Embedding) (int, error) {
	if _, err := tx.Exec("DELETE FROM entry_chunks WHERE handle = ?", handle); err != nil {
		return 0, err
	}
	for ordinal, chunk := range chunks {
		vector := vectors[ordinal]
		vectorJSON, err := json.Marshal(vector)
		if err != nil {
			return 0, err
		}
		id := ContentHash(fmt.Sprintf("%s|%d|%s|%s", handle, ordinal, embedding.Provider, embedding.Model))
		_, err = tx.Exec(
			`INSERT INTO entry_chunks (id, handle, ordinal, content, content_hash,
				embedding_provider, embedding_model, embedding_dimensions, embedding_json)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, handle, ordinal, chunk, ContentHash(chunk), embedding.Provider,
			embedding.Model, len(vector), string(vectorJSON),
		)
		if err != nil {
			return 0, err
		}
	}
	return len(chunks), nil
}

// SearchableChunk is one entry_chunks row joined to its parent entry.
type SearchableChunk struct {
	ChunkID      string
	Ordinal      int
	ChunkContent string
	ChunkHash    string
	EmbeddingRaw string
	Entry        Entry
}

// LoadSearchableChunksFilters narrows LoadSearchableChunks' join.
type LoadSearchableChunksFilters struct {
	Classification string
	Source         string
	Scope          string // "" means no scope filter
}

// LoadSearchableChunks joins chunks to their entries, filtered before any
// scoring happens -- classification, source, and (optionally) scope are
// SQL WHERE clauses, never left to a ranking function.
func LoadSearchableChunks(db *sql.DB, embedding Embedding, filters LoadSearchableChunksFilters) ([]*SearchableChunk, error) {
	clauses := []string{
		"c.embedding_provider = ?", "c.embedding_model = ?", "c.embedding_dimensions = ?",
		"e.classification = ?", "e.source = ?",
	}
	values := []any{embedding.Provider, embedding.Model, embedding.Dimensions, filters.Classification, filters.Source}
	if filters.Scope != "" {
		clauses = append(clauses, "e.scope = ?")
		values = append(values, filters.Scope)
	}
	query := fmt.Sprintf(
		`SELECT c.id, c.ordinal, c.content, c.content_hash, c.embedding_json, %s
		 FROM entry_chunks c JOIN entries e ON e.handle = c.handle
		 WHERE %s`, prefixedEntryColumns("e"), joinAnd(clauses))
	rows, err := db.Query(query, values...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*SearchableChunk
	for rows.Next() {
		sc := &SearchableChunk{}
		var tagsJSON, derivedJSON, redactionsJSON string
		var injectionRisk, untrustedInputs int
		if err := rows.Scan(
			&sc.ChunkID, &sc.Ordinal, &sc.ChunkContent, &sc.ChunkHash, &sc.EmbeddingRaw,
			&sc.Entry.Handle, &sc.Entry.Scope, &sc.Entry.Source, &sc.Entry.TaskID, &sc.Entry.Agent,
			&sc.Entry.DispatchID, &sc.Entry.Label, &tagsJSON, &sc.Entry.Content, &sc.Entry.ContentHash,
			&sc.Entry.ByteLength, &sc.Entry.Classification, &injectionRisk, &untrustedInputs,
			&derivedJSON, &redactionsJSON, &sc.Entry.CreatedAt, &sc.Entry.ExpiresAt, &sc.Entry.PromotedAt,
		); err != nil {
			return nil, err
		}
		sc.Entry.InjectionRisk = injectionRisk != 0
		sc.Entry.UntrustedInputs = untrustedInputs != 0
		if err := json.Unmarshal([]byte(tagsJSON), &sc.Entry.Tags); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(derivedJSON), &sc.Entry.DerivedFrom); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(redactionsJSON), &sc.Entry.Redactions); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func prefixedEntryColumns(alias string) string {
	cols := []string{
		"handle", "scope", "source", "task_id", "agent", "dispatch_id", "label", "tags_json",
		"content", "content_hash", "byte_length", "classification", "injection_risk",
		"untrusted_inputs", "derived_from_json", "redactions_json", "created_at", "expires_at",
		"promoted_at",
	}
	out := ""
	for i, c := range cols {
		if i > 0 {
			out += ", "
		}
		out += alias + "." + c
	}
	return out
}

func joinAnd(clauses []string) string {
	out := ""
	for i, c := range clauses {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}

// EntriesMissingChunks returns entries with no chunks under the configured
// provider/model/dimensions.
func EntriesMissingChunks(db *sql.DB, embedding Embedding) ([]*Entry, error) {
	rows, err := db.Query(
		`SELECT `+entryColumns+` FROM entries e
		 WHERE NOT EXISTS (
		   SELECT 1 FROM entry_chunks c
		   WHERE c.handle = e.handle AND c.embedding_provider = ?
		     AND c.embedding_model = ? AND c.embedding_dimensions = ?
		 )
		 ORDER BY e.created_at, e.handle`,
		embedding.Provider, embedding.Model, embedding.Dimensions,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FetchEntry returns the entry for handle, or nil if none exists.
func FetchEntry(db *sql.DB, handle string) (*Entry, error) {
	row := db.QueryRow("SELECT "+entryColumns+" FROM entries WHERE handle = ?", handle)
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// FetchEntriesFilters narrows FetchEntries. Empty string means "no filter"
// for every field except Classification, which is always required by the
// caller contract.
type FetchEntriesFilters struct {
	Classification string
	Source         string
	Agent          string
	TaskID         string
	DispatchID     string
	Scope          string
}

// FetchEntries filters rows without ranking -- every clause is an exact
// match.
func FetchEntries(db *sql.DB, filters FetchEntriesFilters) ([]*Entry, error) {
	clauses := []string{"classification = ?"}
	values := []any{filters.Classification}
	for _, pair := range []struct{ column, value string }{
		{"source", filters.Source}, {"agent", filters.Agent}, {"task_id", filters.TaskID},
		{"dispatch_id", filters.DispatchID}, {"scope", filters.Scope},
	} {
		if pair.value != "" {
			clauses = append(clauses, pair.column+" = ?")
			values = append(values, pair.value)
		}
	}
	query := fmt.Sprintf("SELECT %s FROM entries WHERE %s ORDER BY created_at DESC, handle", entryColumns, joinAnd(clauses))
	rows, err := db.Query(query, values...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FetchEntriesAll returns every entry, oldest first.
func FetchEntriesAll(db *sql.DB) ([]*Entry, error) {
	rows, err := db.Query("SELECT " + entryColumns + " FROM entries ORDER BY created_at, handle")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeletedEvidence is what DeleteEntry/SweepExpired report about a removed
// row.
type DeletedEvidence struct {
	Handle      string `json:"handle"`
	ContentHash string `json:"content_hash"`
	ByteLength  int    `json:"byte_length"`
	Reason      string `json:"reason"`
}

// DeleteEntry is a voluntary early release. Records the same evidence a
// sweep would.
func DeleteEntry(db *sql.DB, handle, reason string) (*DeletedEvidence, error) {
	row, err := FetchEntry(db, handle)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	if err := insertExpiryEvidence(tx, row, NowISO(), reason); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := tx.Exec("DELETE FROM entries WHERE handle = ?", handle); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &DeletedEvidence{Handle: row.Handle, ContentHash: row.ContentHash, ByteLength: row.ByteLength, Reason: reason}, nil
}

// MarkPromoted records that a proposal was emitted for handle -- not that
// one was accepted, or even staged.
func MarkPromoted(db *sql.DB, handle string) (string, error) {
	moment := NowISO()
	if _, err := db.Exec("UPDATE entries SET promoted_at = ? WHERE handle = ?", moment, handle); err != nil {
		return "", err
	}
	return moment, nil
}

// AccessRecord is one access_runs row to write.
type AccessRecord struct {
	Operation      string
	Handle         string
	QueryHash      string
	TaskID         string
	Agent          string
	Classification string
	Scope          string
	Source         string
	ResultCount    int
}

// RecordAccess writes one access_runs row, attributing an operation.
// access_runs never stores content or query text.
func RecordAccess(db *sql.DB, access AccessRecord) (string, error) {
	id := newUUID()
	_, err := db.Exec(
		`INSERT INTO access_runs (id, operation, handle, query_hash, task_id, agent,
			classification, scope_filter, source, result_count, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, access.Operation, nullableString(access.Handle), nullableString(access.QueryHash),
		access.TaskID, access.Agent, access.Classification, nullableString(access.Scope),
		access.Source, access.ResultCount, NowISO(),
	)
	if err != nil {
		return "", err
	}
	return id, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// PruneAuditResult reports how many rows PruneAuditRecords removed.
type PruneAuditResult struct {
	AccessRuns     int    `json:"access_runs"`
	ExpiryEvidence int    `json:"expiry_evidence"`
	Cutoff         string `json:"cutoff"`
}

// PruneAuditRecords deletes access_runs/expiry_evidence rows older than a
// caller-chosen age. Retention for these two tables is deliberately
// indefinite by default -- nothing in this package calls this on a
// schedule; it exists only for an operator who has explicitly decided a
// cutoff is appropriate.
func PruneAuditRecords(db *sql.DB, olderThanDays int) (*PruneAuditResult, error) {
	if olderThanDays < 1 {
		return nil, fmt.Errorf("older_than_days must be a positive integer")
	}
	cutoff := nowISOBeforeDays(olderThanDays)
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	accessResult, err := tx.Exec("DELETE FROM access_runs WHERE created_at < ?", cutoff)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	evidenceResult, err := tx.Exec("DELETE FROM expiry_evidence WHERE swept_at < ?", cutoff)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	accessRows, _ := accessResult.RowsAffected()
	evidenceRows, _ := evidenceResult.RowsAffected()
	return &PruneAuditResult{AccessRuns: int(accessRows), ExpiryEvidence: int(evidenceRows), Cutoff: cutoff}, nil
}

// ScopeCount is one (value, count) pair from a GROUP BY.
type ScopeCount struct {
	Scope   string `json:"scope"`
	Entries int    `json:"entries"`
}

// SourceCount is one (value, count) pair from a GROUP BY.
type SourceCount struct {
	Source  string `json:"source"`
	Entries int    `json:"entries"`
}

// StoreStats mirrors database.py's store_stats.
type StoreStats struct {
	Entries          int           `json:"entries"`
	BytesStored      int64         `json:"bytes_stored"`
	UntrustedEntries int           `json:"untrusted_entries"`
	AccessRuns       int           `json:"access_runs"`
	ExpiredOrDropped int           `json:"expired_or_dropped"`
	Chunks           int           `json:"chunks"`
	IndexedEntries   int           `json:"indexed_entries"`
	ByScope          []ScopeCount  `json:"by_scope"`
	BySource         []SourceCount `json:"by_source"`
}

// GetStoreStats computes summary counts across every table.
func GetStoreStats(db *sql.DB) (*StoreStats, error) {
	var stats StoreStats
	row := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM entries),
		(SELECT COALESCE(SUM(byte_length), 0) FROM entries),
		(SELECT COUNT(*) FROM entries WHERE untrusted_inputs = 1),
		(SELECT COUNT(*) FROM access_runs),
		(SELECT COUNT(*) FROM expiry_evidence)`)
	if err := row.Scan(&stats.Entries, &stats.BytesStored, &stats.UntrustedEntries, &stats.AccessRuns, &stats.ExpiredOrDropped); err != nil {
		return nil, err
	}

	scopeRows, err := db.Query("SELECT scope, COUNT(*) FROM entries GROUP BY scope ORDER BY scope")
	if err != nil {
		return nil, err
	}
	defer func() { _ = scopeRows.Close() }()
	for scopeRows.Next() {
		var sc ScopeCount
		if err := scopeRows.Scan(&sc.Scope, &sc.Entries); err != nil {
			return nil, err
		}
		stats.ByScope = append(stats.ByScope, sc)
	}
	if err := scopeRows.Err(); err != nil {
		return nil, err
	}

	sourceRows, err := db.Query("SELECT source, COUNT(*) FROM entries GROUP BY source ORDER BY source")
	if err != nil {
		return nil, err
	}
	defer func() { _ = sourceRows.Close() }()
	for sourceRows.Next() {
		var sc SourceCount
		if err := sourceRows.Scan(&sc.Source, &sc.Entries); err != nil {
			return nil, err
		}
		stats.BySource = append(stats.BySource, sc)
	}
	if err := sourceRows.Err(); err != nil {
		return nil, err
	}

	chunkRow := db.QueryRow("SELECT COUNT(*), COUNT(DISTINCT handle) FROM entry_chunks")
	if err := chunkRow.Scan(&stats.Chunks, &stats.IndexedEntries); err != nil {
		return nil, err
	}
	return &stats, nil
}
