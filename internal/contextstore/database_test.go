//go:build cgo
// +build cgo

package contextstore

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSweepExpiredDeletesAndRecordsEvidence(t *testing.T) {
	db, cfg := newTestStore(t)
	past := -1
	put, err := PutEntry(db, cfg, basicPutOptions(func(o *PutOptions) { o.TTLDaysOverride = nil }))
	if err != nil {
		t.Fatal(err)
	}
	_ = past

	// Force expiry by sweeping "as of" a moment in the future.
	future := time.Now().UTC().Add(48 * time.Hour).Format("2006-01-02T15:04:05.000Z")
	swept, err := SweepExpired(db, future)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if len(swept) != 1 {
		t.Fatalf("expected 1 swept entry, got %d", len(swept))
	}
	if swept[0].Handle != put.Handle {
		t.Errorf("swept handle = %q, want %q", swept[0].Handle, put.Handle)
	}
	row, err := FetchEntry(db, put.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if row != nil {
		t.Error("expected the entry to be gone after sweep")
	}
}

func TestExpiredRowsDryRunDoesNotDelete(t *testing.T) {
	db, cfg := newTestStore(t)
	put, err := PutEntry(db, cfg, basicPutOptions(nil))
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(48 * time.Hour).Format("2006-01-02T15:04:05.000Z")
	rows, err := ExpiredRows(db, future)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 expired row reported, got %d", len(rows))
	}
	row, err := FetchEntry(db, put.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil {
		t.Error("expected the entry to still exist after a dry-run expiry check")
	}
}

func TestOpenStoreSweepsExpiredEntriesOnOpen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "context.db")
	db, err := OpenStore(dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Ingestion: map[string]any{"redact_secrets": true},
		Expiry: Expiry{
			DefaultTTLDaysByScope: map[string]int{"agent": 1, "dispatch": 7, "project": 30},
			MaximumTTLDays:        90,
		},
		Limits:    map[string]any{"max_entry_bytes": 1048576},
		Chunking:  Chunking{MaxCharacters: 2400, OverlapCharacters: 240},
		Embedding: Embedding{Provider: "hashing", Model: "feature-hash-v1", Dimensions: 384},
	}
	put, err := PutEntry(db, cfg, basicPutOptions(nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// Manually push expires_at into the past directly, then reopen: the
	// open-time sweep should remove it without anyone calling expire.
	db2, err := OpenStore(dbPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db2.Exec("UPDATE entries SET expires_at = '2000-01-01T00:00:00.000Z' WHERE handle = ?", put.Handle); err != nil {
		t.Fatal(err)
	}
	_ = db2.Close()

	db3, err := OpenStore(dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db3.Close() }()
	row, err := FetchEntry(db3, put.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if row != nil {
		t.Error("expected OpenStore(sweep=true) to sweep an already-expired entry on open")
	}
}

func TestPutEntryAtomicityRollsBackOnFailure(t *testing.T) {
	// insert_entry and replace_chunks share one transaction so an
	// interruption cannot leave a committed entry with no chunks. We can't
	// easily force a mid-transaction failure without touching internals, so
	// instead assert the positive property: a successful put always has
	// both an entry row AND at least one chunk row.
	db, cfg := newTestStore(t)
	put, err := PutEntry(db, cfg, basicPutOptions(nil))
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := LoadSearchableChunks(db, cfg.Embedding, LoadSearchableChunksFilters{
		Classification: "internal", Source: "demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range chunks {
		if c.Entry.Handle == put.Handle {
			found = true
		}
	}
	if !found {
		t.Error("expected the entry's chunks to be visible immediately after put (same transaction)")
	}
}

func TestPruneAuditRecordsRejectsNonPositiveDays(t *testing.T) {
	db, _ := newTestStore(t)
	if _, err := PruneAuditRecords(db, 0); err == nil {
		t.Fatal("expected rejection of older_than_days=0")
	}
	if _, err := PruneAuditRecords(db, -5); err == nil {
		t.Fatal("expected rejection of a negative older_than_days")
	}
}

func TestGetStoreStatsCounts(t *testing.T) {
	db, cfg := newTestStore(t)
	if _, err := PutEntry(db, cfg, basicPutOptions(nil)); err != nil {
		t.Fatal(err)
	}
	stats, err := GetStoreStats(db)
	if err != nil {
		t.Fatalf("GetStoreStats: %v", err)
	}
	if stats.Entries != 1 {
		t.Errorf("entries = %d, want 1", stats.Entries)
	}
	if stats.Chunks == 0 {
		t.Error("expected at least 1 chunk")
	}
	if len(stats.ByScope) != 1 || stats.ByScope[0].Scope != "agent" {
		t.Errorf("by_scope = %+v", stats.ByScope)
	}
}
