package knowledge

import (
	"testing"
)

// The full-text index tracks the messages table, in both directions.
//
// Ingestion never wrote to the index: `cadre knowledge fts5-search` returned
// nothing for content ingested seconds earlier, and only `fts5-index document
// add` could put anything in. An empty result set is indistinguishable from no
// match, so the gap was silent.
//
// Deletion is the half that matters most. Messages are removed from five
// places -- retention by id, by classification, by source and by age, plus
// DeleteMessage -- several running raw SQL inside transactions. An index that
// kept a message deleted for retention or classification reasons would be a
// leak, not a stale row. Triggers cover every path, including ones added later.
//
// Skipped rather than failed without FTS5: mattn/go-sqlite3 compiles the module
// in only under `-tags sqlite_fts5`. The Makefile and CI pass it; a bare
// `go test ./...` does not, and this asserting nothing there is the honest
// outcome -- Available() is what the CLI checks to refuse.
func TestFullTextIndexTracksMessages(t *testing.T) {
	store, err := Open(t.TempDir() + "/fts.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	index := NewFTS5Index(store.db)
	if err := index.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !index.Available() {
		t.Skip("this build has no SQLite FTS5 module; rebuild with -tags sqlite_fts5")
	}

	title := "Payments rollout"
	id, err := store.SaveMessage("probe", nil, "c1", &title, "m1", "user",
		"deploy notes for the blue-green rollback plan", nil, "internal", false, "[]", "{}", nil)
	if err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	// Indexed by the insert trigger, with no explicit indexing call.
	if got := index.GetDocumentCount(); got != 1 {
		t.Fatalf("document count after one save = %d, want 1", got)
	}
	results, err := index.FullTextSearch("rollback", 10)
	if err != nil {
		t.Fatalf("FullTextSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("search for an ingested term returned %d results, want 1", len(results))
	}

	// Removed by the delete trigger. A message that survives deletion in the
	// index is findable after it was meant to be gone.
	if _, err := store.db.Exec("DELETE FROM messages WHERE id = ?", id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := index.GetDocumentCount(); got != 0 {
		t.Errorf("document count after delete = %d, want 0 -- the message is still findable", got)
	}
	after, err := index.FullTextSearch("rollback", 10)
	if err != nil {
		t.Fatalf("FullTextSearch after delete: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("a deleted message still matches full-text search (%d results)", len(after))
	}
}

// A store written before the index existed becomes searchable when reopened.
func TestFullTextIndexBackfillsExistingMessages(t *testing.T) {
	path := t.TempDir() + "/backfill.db"
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO messages
		(id, source, conversation_id, source_message_id, role, content, content_hash,
		 classification, injection_risk, redactions_json, metadata_json, ingested_at)
		VALUES ('x1','probe','c1','m9','user','ledger checkpoint restore runbook','h1',
		        'internal',0,'[]','{}','2026-08-18T00:00:00Z')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Drop the index so the row looks like one ingested before it existed.
	if _, err := store.db.Exec("DROP TABLE IF EXISTS documents_fts"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	_ = store.Close()

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	index := NewFTS5Index(reopened.db)
	if err := index.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !index.Available() {
		t.Skip("this build has no SQLite FTS5 module; rebuild with -tags sqlite_fts5")
	}
	if got := index.GetDocumentCount(); got != 1 {
		t.Errorf("document count after reopen = %d, want 1 -- an existing store never becomes searchable", got)
	}
}
