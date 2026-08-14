package knowledge

import (
	"errors"
	"testing"
)

func TestOpenStore(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	// Open store
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	// Verify schema was created
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if stats.TotalMessages != 0 {
		t.Errorf("Expected 0 messages, got %d", stats.TotalMessages)
	}
}

func TestBeginCompleteRun(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Begin run
	runID, err := store.BeginRun("test-source", "http://example.com")
	if err != nil {
		t.Fatalf("BeginRun failed: %v", err)
	}

	if runID == "" {
		t.Error("Expected non-empty run ID")
	}

	// Complete run
	err = store.CompleteRun(runID, 10, 20)
	if err != nil {
		t.Fatalf("CompleteRun failed: %v", err)
	}
}

func TestFailRun(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Begin run
	runID, err := store.BeginRun("test-source", "http://example.com")
	if err != nil {
		t.Fatalf("BeginRun failed: %v", err)
	}

	// Fail run
	err = store.FailRun(runID, errors.New("test error"))
	if err != nil {
		t.Fatalf("FailRun failed: %v", err)
	}
}

func TestStatsEmpty(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	if stats.TotalMessages != 0 || stats.TotalChunks != 0 {
		t.Errorf("Expected empty store, got messages=%d chunks=%d", stats.TotalMessages, stats.TotalChunks)
	}

	if len(stats.Sources) != 0 || len(stats.Classifications) != 0 {
		t.Error("Expected empty breakdowns")
	}
}

func setupTestStore(t *testing.T) *Store {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	return store
}
