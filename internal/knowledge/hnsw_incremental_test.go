package knowledge

import (
	"fmt"
	"testing"
)

func TestHSNWDelete(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	embedding := []float32{1.0, 0.0}
	idx.Insert("msg-1", embedding)

	// Delete existing message
	err := idx.Delete("msg-1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Search should skip deleted
	results := idx.Search(embedding, 1)
	if len(results) > 0 {
		t.Error("Deleted message should not appear in results")
	}

	// Verify deletion status
	status := idx.GetDeletionStatus()
	if status.DeletedCount != 1 {
		t.Errorf("Expected 1 deleted, got %d", status.DeletedCount)
	}
}

func TestHSNWDeleteNonexistent(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Delete non-existent message
	err := idx.Delete("msg-nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent delete")
	}
}

func TestHSNWDeleteAlreadyDeleted(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Delete("msg-1")

	// Try to delete again
	err := idx.Delete("msg-1")
	if err == nil {
		t.Error("Expected error for double delete")
	}
}

func TestHSNWUndelete(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	embedding := []float32{1.0, 0.0, 0.0}
	idx.Insert("msg-1", embedding)
	idx.Delete("msg-1")

	// Verify deleted
	status := idx.GetDeletionStatus()
	if status.DeletedCount != 1 {
		t.Error("Message should be deleted")
	}

	// Undelete
	err := idx.Undelete("msg-1")
	if err != nil {
		t.Fatalf("Undelete failed: %v", err)
	}

	// Verify restored
	status = idx.GetDeletionStatus()
	if status.DeletedCount != 0 {
		t.Error("Message should be undeleted")
	}

	// Should appear in search again
	results := idx.Search(embedding, 1)
	if len(results) != 1 {
		t.Errorf("Expected restored message in results, got %d", len(results))
	}
}

func TestHSNWUndeleteNondeleted(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})

	// Try to undelete non-deleted message
	err := idx.Undelete("msg-1")
	if err == nil {
		t.Error("Expected error for undelete non-deleted")
	}
}

func TestHSNWUpdate(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	oldEmb := []float32{1.0, 0.0, 0.0}
	idx.Insert("msg-1", oldEmb)

	// Update embedding
	newEmb := []float32{0.5, 0.5, 0.0}
	err := idx.Update("msg-1", newEmb)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Search should use new embedding
	results := idx.Search(newEmb, 1)
	if len(results) != 1 {
		t.Error("Expected updated message in results")
	}

	// Distance should be near zero (exact match)
	if results[0].Distance > 0.01 {
		t.Errorf("Expected near-zero distance after update, got %f", results[0].Distance)
	}
}

func TestHSNWUpdateNonexistent(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	err := idx.Update("msg-nonexistent", []float32{1.0, 0.0})
	if err == nil {
		t.Error("Expected error for non-existent update")
	}
}

func TestHSNWUpdateEmptyEmbedding(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})

	err := idx.Update("msg-1", []float32{})
	if err == nil {
		t.Error("Expected error for empty embedding update")
	}
}

func TestHSNWUpdateDeleted(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Delete("msg-1")

	err := idx.Update("msg-1", []float32{0.5, 0.5})
	if err == nil {
		t.Error("Expected error for updating deleted message")
	}
}

func TestHSNWCompact(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert multiple vectors
	for i := 0; i < 10; i++ {
		embedding := []float32{float32(i), float32(i + 1)}
		idx.Insert("msg-"+string(rune(48+i)), embedding)
	}

	stats := idx.GetStats()
	if stats.IndexSize != 10 {
		t.Errorf("Expected 10 vectors, got %d", stats.IndexSize)
	}

	// Delete some
	for i := 0; i < 5; i++ {
		idx.Delete("msg-" + string(rune(48+i)))
	}

	status := idx.GetDeletionStatus()
	if status.DeletedCount != 5 {
		t.Errorf("Expected 5 deleted, got %d", status.DeletedCount)
	}

	// Compact
	err := idx.Compact()
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	// After compact, tombstones should be gone
	status = idx.GetDeletionStatus()
	if status.DeletedCount != 0 {
		t.Error("Compact should remove all tombstones")
	}

	if status.LiveEntries != 5 {
		t.Errorf("Expected 5 live entries after compact, got %d", status.LiveEntries)
	}

	// Verify remaining nodes are searchable
	results := idx.Search([]float32{5.0, 6.0}, 3)
	if len(results) == 0 {
		t.Error("Expected to find remaining vectors after compact")
	}
}

func TestHSNWCompactEmpty(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Compact on empty index should be no-op
	err := idx.Compact()
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	status := idx.GetDeletionStatus()
	if status.DeletedCount != 0 {
		t.Error("Empty index should have no deletions")
	}
}

func TestHSNWCompactNoDeletions(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Insert("msg-2", []float32{0.0, 1.0})

	// Compact with no deletions should be no-op
	err := idx.Compact()
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	stats := idx.GetStats()
	if stats.IndexSize != 2 {
		t.Error("Compact should not affect live entries")
	}
}

func TestHSNWDeletionStatus(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert 10 vectors
	for i := 0; i < 10; i++ {
		idx.Insert("msg-"+string(rune(48+i)), []float32{float32(i), float32(i + 1)})
	}

	// Delete 3
	idx.Delete("msg-0")
	idx.Delete("msg-1")
	idx.Delete("msg-2")

	status := idx.GetDeletionStatus()

	if status.TotalEntries != 10 {
		t.Errorf("Expected total 10, got %d", status.TotalEntries)
	}

	if status.LiveEntries != 7 {
		t.Errorf("Expected 7 live, got %d", status.LiveEntries)
	}

	if status.DeletedCount != 3 {
		t.Errorf("Expected 3 deleted, got %d", status.DeletedCount)
	}

	if status.DeletionRatio < 29 || status.DeletionRatio > 31 {
		t.Errorf("Expected ~30%% deletion ratio, got %.1f%%", status.DeletionRatio)
	}

	if !status.NeedsCompaction {
		t.Error("Should recommend compaction at 30%% deletion ratio")
	}
}

func TestHSNWDeletionStatusNeedsCompaction(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert 100 vectors
	for i := 0; i < 100; i++ {
		msgID := fmt.Sprintf("msg-%d", i+1)
		embedding := []float32{float32(i) / 100.0, float32((i + 1) % 100) / 100.0}
		idx.Insert(msgID, embedding)
	}

	// Delete 15 (15% deletion ratio)
	for i := 0; i < 15; i++ {
		msgID := fmt.Sprintf("msg-%d", i+1)
		idx.Delete(msgID)
	}

	status := idx.GetDeletionStatus()

	if !status.NeedsCompaction {
		t.Error("Should recommend compaction at 15%% deletion ratio")
	}
}

func TestHSNWDeletionRatioThreshold(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert 100 vectors
	for i := 0; i < 100; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i+1), []float32{float32(i), float32(i + 1)})
	}

	// Delete only 1 (1% deletion ratio - below threshold)
	idx.Delete("msg-1")

	status := idx.GetDeletionStatus()

	if status.NeedsCompaction {
		t.Error("Should not recommend compaction below 10%% threshold")
	}
}

func TestHSNWSearchWithDeletions(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	embeddings := map[string][]float32{
		"msg-exact":   {1.0, 0.5, 0.2},
		"msg-close1":  {0.99, 0.51, 0.21},
		"msg-close2":  {0.98, 0.52, 0.22},
		"msg-far":     {0.5, 0.5, 0.5},
	}

	for msgID, emb := range embeddings {
		idx.Insert(msgID, emb)
	}

	// Delete close match
	idx.Delete("msg-close1")

	// Search should skip deleted
	query := []float32{1.0, 0.5, 0.2}
	results := idx.Search(query, 3)

	// Should get exact + close2, but not close1 (deleted)
	if len(results) > 3 {
		t.Errorf("Expected max 3 results, got %d", len(results))
	}

	// Verify deleted is not in results
	for _, result := range results {
		if result.MessageID == "msg-close1" {
			t.Error("Deleted message should not appear in results")
		}
	}
}

func TestHSNWCompactEntryPoint(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert vectors
	for i := 0; i < 10; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i+1), []float32{float32(i), float32(i + 1)})
	}

	// Manually mark entry point for deletion if it's low layer
	entryPointID := idx.entryPoint.MessageID

	// If entry point gets deleted, compact should find new one
	idx.Delete(entryPointID)

	err := idx.Compact()
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	// Entry point should still exist and not be deleted
	if idx.entryPoint == nil {
		t.Error("Entry point should exist after compact")
	}

	if idx.entryPoint != nil && idx.tombstones[idx.entryPoint.MessageID] {
		t.Error("Entry point should not be deleted after compact")
	}
}

func TestHSNWUpdateThenDelete(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})

	// Update then delete
	idx.Update("msg-1", []float32{0.5, 0.5})
	idx.Delete("msg-1")

	results := idx.Search([]float32{0.5, 0.5}, 1)
	if len(results) > 0 {
		t.Error("Updated then deleted message should not appear in results")
	}
}

func TestHSNWDeleteThenUpdate(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Delete("msg-1")

	// Try to update deleted message
	err := idx.Update("msg-1", []float32{0.5, 0.5})
	if err == nil {
		t.Error("Expected error for updating deleted message")
	}
}

func TestHSNWIncrementalWorkflow(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Phase 1: Initial inserts
	for i := 1; i <= 10; i++ {
		embedding := []float32{float32(i) / 10.0, float32((i + 1) % 10) / 10.0}
		idx.Insert(fmt.Sprintf("msg-%d", i), embedding)
	}

	stats := idx.GetStats()
	if stats.IndexSize != 10 {
		t.Error("Phase 1: Expected 10 vectors")
	}

	// Phase 2: Update some
	idx.Update("msg-1", []float32{0.1, 0.2})
	idx.Update("msg-5", []float32{0.5, 0.6})

	// Phase 3: Delete some
	for i := 1; i <= 3; i++ {
		idx.Delete(fmt.Sprintf("msg-%d", i))
	}

	status := idx.GetDeletionStatus()
	if status.DeletedCount != 3 {
		t.Error("Phase 3: Expected 3 deleted")
	}

	// Phase 4: Compact
	idx.Compact()

	status = idx.GetDeletionStatus()
	if status.LiveEntries != 7 {
		t.Errorf("Phase 4: Expected 7 live after compact, got %d", status.LiveEntries)
	}

	// Phase 5: Search
	results := idx.Search([]float32{0.5, 0.6}, 3)
	if len(results) == 0 {
		t.Error("Phase 5: Should find vectors after incremental updates")
	}
}
