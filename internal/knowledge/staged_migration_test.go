package knowledge

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// Staged records used to live in the same database as the corpus. That
// database is recall's now and cadre no longer opens it with its own schema,
// so a record staged before the separation would be stranded in a file only
// an older cadre could read.

// legacyStore builds a database shaped like the combined one: the staged
// tables alongside a corpus table cadre no longer knows about.
func legacyStore(t *testing.T, path string, recordIDs ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("cannot create the legacy store: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(stagedSchema); err != nil {
		t.Fatalf("cannot create staged tables: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE messages (id TEXT PRIMARY KEY, content TEXT)`); err != nil {
		t.Fatalf("cannot create the corpus table: %v", err)
	}
	for _, id := range recordIDs {
		if _, err := db.Exec(`
INSERT INTO staged_records (id, status, frontmatter_json, body, content_digest, created_at, updated_at)
VALUES (?, 'proposed', ?, 'body', 'digest', ?, ?)`,
			id, fmt.Sprintf(`{"id":%q,"status":"proposed"}`, id), nowISO(), nowISO()); err != nil {
			t.Fatalf("cannot stage %s: %v", id, err)
		}
	}
}

func TestStagedRecordsMoveOutOfALegacyCombinedStore(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "store.db")
	stagedPath := filepath.Join(dir, StagedDatabaseFile)
	legacyStore(t, legacyPath, "KS-1", "KS-2")

	copied, err := MigrateStagedRecords(legacyPath, stagedPath)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if copied != 2 {
		t.Errorf("copied %d rows, want 2", copied)
	}

	store, err := OpenStaged(stagedPath)
	if err != nil {
		t.Fatalf("cannot open the migrated store: %v", err)
	}
	defer func() { _ = store.Close() }()

	records, err := store.ListStagedRecords("")
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("migrated store holds %d records, want 2", len(records))
	}
}

// TestTheLegacyStoreIsLeftIntact. A migration that deletes its source cannot
// be re-run after someone notices it went wrong.
func TestTheLegacyStoreIsLeftIntact(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "store.db")
	legacyStore(t, legacyPath, "KS-1")

	if _, err := MigrateStagedRecords(legacyPath, filepath.Join(dir, StagedDatabaseFile)); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	db, err := sql.Open("sqlite", dsn(legacyPath))
	if err != nil {
		t.Fatalf("cannot reopen the legacy store: %v", err)
	}
	defer func() { _ = db.Close() }()
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM staged_records`).Scan(&remaining); err != nil {
		t.Fatalf("cannot count legacy rows: %v", err)
	}
	if remaining != 1 {
		t.Errorf("legacy store holds %d rows after migration, want 1 (untouched)", remaining)
	}
}

// TestMigratingTwiceDoesNotDuplicate: the staged store is opened per CLI
// invocation, so a migration that ran twice on a half-created store would
// double every record rather than fail.
func TestMigratingTwiceDoesNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "store.db")
	stagedPath := filepath.Join(dir, StagedDatabaseFile)
	legacyStore(t, legacyPath, "KS-1")

	if _, err := MigrateStagedRecords(legacyPath, stagedPath); err != nil {
		t.Fatalf("first migration failed: %v", err)
	}
	copied, err := MigrateStagedRecords(legacyPath, stagedPath)
	if err != nil {
		t.Fatalf("second migration failed: %v", err)
	}
	if copied != 0 {
		t.Errorf("the second migration copied %d rows, want 0", copied)
	}
}

// TestNothingToMigrateIsNotAnError: most stores have no legacy records, and a
// first open must not fail because of it.
func TestNothingToMigrateIsNotAnError(t *testing.T) {
	dir := t.TempDir()

	_, err := MigrateStagedRecords(filepath.Join(dir, "absent.db"), filepath.Join(dir, StagedDatabaseFile))
	if !errors.Is(err, ErrNoLegacyStagedRecords) {
		t.Errorf("a missing legacy store returned %v, want ErrNoLegacyStagedRecords", err)
	}

	// A recall store: real, and holding no staged tables at all.
	corpusOnly := filepath.Join(dir, "recall.db")
	db, err := sql.Open("sqlite", dsn(corpusOnly))
	if err != nil {
		t.Fatalf("cannot create: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE chunks (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("cannot create: %v", err)
	}
	_ = db.Close()

	if _, err := MigrateStagedRecords(corpusOnly, filepath.Join(dir, "other.db")); !errors.Is(err, ErrNoLegacyStagedRecords) {
		t.Errorf("a store with no staged tables returned %v, want ErrNoLegacyStagedRecords", err)
	}
}
