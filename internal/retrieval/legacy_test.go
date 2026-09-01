package retrieval

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// combinedStore builds a database shaped like the one cadre's retrieval
// engine wrote: its own corpus tables, and a `chunks` table whose columns are
// nothing like recall's.
func combinedStore(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("cannot create the legacy store: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, statement := range []string{
		`CREATE TABLE messages (id TEXT PRIMARY KEY, source TEXT, content TEXT)`,
		`CREATE TABLE ingestion_runs (id TEXT PRIMARY KEY)`,
		`CREATE TABLE retrieval_runs (id TEXT PRIMARY KEY)`,
		`CREATE TABLE deletion_runs (id TEXT PRIMARY KEY)`,
		`CREATE TABLE chunks (id TEXT PRIMARY KEY, message_id TEXT, ordinal INTEGER,
			content TEXT, embedding_provider TEXT, embedding_model TEXT, embedding_json TEXT)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("cannot create the legacy schema: %v", err)
		}
	}
}

// TestAPreMigrationStoreIsRefusedBeforeItIsTouched.
//
// recall's store initializer is additive: pointed at a file that already has a
// `chunks` table with the old engine's columns, it creates what is missing,
// finds `chunks` present, and succeeds -- leaving a file that is neither a
// valid legacy store nor a valid recall one. `cadre knowledge init`, the first
// command the quickstart tells an operator to run, reported ordinary success
// and every later search failed with `no such column: c.document_ref`.
//
// Silent corruption on the first documented command is the worst failure this
// migration can have, so the refusal happens before recall's schema
// initializer is allowed near the file.
func TestAPreMigrationStoreIsRefusedBeforeItIsTouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "combined.db")
	combinedStore(t, path)

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	_, err = Open(Options{
		Database: path, EmbedderName: "local-hashing", Dimensions: 128,
	}, stubProvider{})
	if err == nil {
		t.Fatal("a pre-migration store was opened")
	}
	if !errors.Is(err, ErrLegacyStore) {
		t.Errorf("error = %v, want ErrLegacyStore", err)
	}
	for _, want := range []string{"messages", "recall upload", "not at risk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if after.Size() != before.Size() {
		t.Errorf("the refused open modified the store: %d -> %d bytes", before.Size(), after.Size())
	}
}

// TestIngestAlsoRefusesAPreMigrationStore: the write path opens through the
// same door, and writing into a half-initialised store would be worse than
// reading from one.
func TestIngestAlsoRefusesAPreMigrationStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "combined.db")
	combinedStore(t, path)

	_, err := OpenForIngest(Options{
		Database: path, EmbedderName: "local-hashing", Dimensions: 128,
	}, stubProvider{})
	if !errors.Is(err, ErrLegacyStore) {
		t.Errorf("error = %v, want ErrLegacyStore", err)
	}
}

// TestARecallStoreIsNotMistakenForALegacyOne. The guard keys on tables only
// the engine had; a real recall store, and a store that does not exist yet,
// must both pass straight through.
func TestARecallStoreIsNotMistakenForALegacyOne(t *testing.T) {
	dir := t.TempDir()

	if err := RefuseLegacyStore(filepath.Join(dir, "absent.db")); err != nil {
		t.Errorf("a store that does not exist was refused: %v", err)
	}

	recallLike := filepath.Join(dir, "recall.db")
	db, err := sql.Open("sqlite", "file:"+recallLike)
	if err != nil {
		t.Fatalf("cannot create: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE chunks (id TEXT PRIMARY KEY, content TEXT,
		document_ref TEXT, chunk_index INTEGER, namespace TEXT, metadata TEXT,
		created_at TEXT, updated_at TEXT)`); err != nil {
		t.Fatalf("cannot create: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE embeddings (chunk_id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("cannot create: %v", err)
	}
	_ = db.Close()

	if err := RefuseLegacyStore(recallLike); err != nil {
		t.Errorf("a recall store was refused as legacy: %v", err)
	}
}

// TestAStoreHoldingOnlyStagedTablesIsNotLegacy: after the staged records are
// migrated out, a combined store's own staged tables are still in it. That
// alone must not make a different file look like an engine store.
func TestAStoreHoldingOnlyStagedTablesIsNotLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "staged-records.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("cannot create: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE staged_records (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("cannot create: %v", err)
	}
	_ = db.Close()

	if err := RefuseLegacyStore(path); err != nil {
		t.Errorf("a staged-record store was refused as legacy: %v", err)
	}
}
