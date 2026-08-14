package cli

import (
	"fmt"
	"testing"

	"github.com/deagy/cadre/cli/internal/knowledge"
)

func TestHSNWDeleteCommand(t *testing.T) {
	// Test: cadre knowledge hnsw-delete <messageID>
	// Expected: Mark vector as deleted (tombstone)

	idx := knowledge.NewHSNWIndex(16, 200)

	// Insert and delete
	idx.Insert("msg-1", []float32{1.0, 0.0})
	err := idx.Delete("msg-1")

	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	status := idx.GetDeletionStatus()
	if status.DeletedCount != 1 {
		t.Errorf("Expected 1 deleted, got %d", status.DeletedCount)
	}
}

func TestHSNWUndeleteCommand(t *testing.T) {
	// Test: cadre knowledge hnsw-undelete <messageID>
	// Expected: Restore deleted vector

	idx := knowledge.NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Delete("msg-1")

	err := idx.Undelete("msg-1")

	if err != nil {
		t.Fatalf("Undelete failed: %v", err)
	}

	status := idx.GetDeletionStatus()
	if status.DeletedCount != 0 {
		t.Errorf("Expected 0 deleted after undelete, got %d", status.DeletedCount)
	}
}

func TestHSNWUpdateCommand(t *testing.T) {
	// Test: cadre knowledge hnsw-update <messageID> <newEmbedding>
	// Expected: Update vector in-place

	idx := knowledge.NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})

	newEmb := []float32{0.5, 0.5}
	err := idx.Update("msg-1", newEmb)

	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify by searching
	results := idx.Search(newEmb, 1)
	if len(results) != 1 || results[0].MessageID != "msg-1" {
		t.Error("Updated message should match new embedding")
	}
}

func TestHSNWCompactCommand(t *testing.T) {
	// Test: cadre knowledge hnsw-compact
	// Expected: Remove tombstones and rebuild connectivity

	idx := knowledge.NewHSNWIndex(16, 200)

	// Insert and delete
	for i := 0; i < 10; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i+1), []float32{float32(i), float32(i + 1)})
	}

	for i := 0; i < 5; i++ {
		idx.Delete(fmt.Sprintf("msg-%d", i+1))
	}

	status := idx.GetDeletionStatus()
	if status.DeletedCount != 5 {
		t.Error("Setup: expected 5 deleted")
	}

	err := idx.Compact()
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	status = idx.GetDeletionStatus()
	if status.DeletedCount != 0 {
		t.Error("Compact should remove all tombstones")
	}

	if status.LiveEntries != 5 {
		t.Errorf("Expected 5 live after compact, got %d", status.LiveEntries)
	}
}

func TestHSNWDeletionStatusCommand(t *testing.T) {
	// Test: cadre knowledge hnsw-status --deletions
	// Expected: Show deletion statistics

	idx := knowledge.NewHSNWIndex(16, 200)

	// Insert 20 vectors
	for i := 1; i <= 20; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 20.0, float32((i + 1) % 20) / 20.0})
	}

	// Delete 5
	for i := 1; i <= 5; i++ {
		idx.Delete(fmt.Sprintf("msg-%d", i))
	}

	status := idx.GetDeletionStatus()

	if status.TotalEntries != 20 {
		t.Errorf("Expected 20 total, got %d", status.TotalEntries)
	}

	if status.DeletedCount != 5 {
		t.Errorf("Expected 5 deleted, got %d", status.DeletedCount)
	}

	if status.LiveEntries != 15 {
		t.Errorf("Expected 15 live, got %d", status.LiveEntries)
	}

	if status.DeletionRatio < 24 || status.DeletionRatio > 26 {
		t.Errorf("Expected ~25%% deletion ratio, got %.1f%%", status.DeletionRatio)
	}
}

func TestHSNWDeletionRecommendation(t *testing.T) {
	// Test: Compact recommendation based on deletion ratio
	// Expected: NeedsCompaction=true when ratio > 10%

	idx := knowledge.NewHSNWIndex(16, 200)

	// Insert 100 vectors
	for i := 1; i <= 100; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 100.0, float32((i + 1) % 100) / 100.0})
	}

	// Delete to cross 10% threshold
	for i := 1; i <= 12; i++ {
		idx.Delete(fmt.Sprintf("msg-%d", i))
	}

	status := idx.GetDeletionStatus()

	if !status.NeedsCompaction {
		t.Error("Should recommend compaction at >10%% deletion ratio")
	}
}

func TestHSNWSearchSkipsTombstones(t *testing.T) {
	// Test: Search results exclude tombstones
	// Expected: Deleted vectors never appear in search results

	idx := knowledge.NewHSNWIndex(16, 200)

	embeddings := map[string][]float32{
		"msg-exact":  {1.0, 0.5},
		"msg-close":  {0.99, 0.51},
		"msg-far":    {0.5, 0.5},
		"msg-delete": {0.98, 0.52},
	}

	for msgID, emb := range embeddings {
		idx.Insert(msgID, emb)
	}

	// Delete one
	idx.Delete("msg-delete")

	// Search
	query := []float32{1.0, 0.5}
	results := idx.Search(query, 4)

	// Verify deleted not in results
	for _, result := range results {
		if result.MessageID == "msg-delete" {
			t.Error("Deleted message appeared in search results")
		}
	}

	// Should get exact + close + far, but not delete
	if len(results) > 3 {
		t.Errorf("Expected max 3 results (deleted excluded), got %d", len(results))
	}
}

func TestHSNWBatchDelete(t *testing.T) {
	// Test: Delete multiple vectors
	// Expected: All marked as tombstones

	idx := knowledge.NewHSNWIndex(16, 200)

	// Insert 10
	for i := 1; i <= 10; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 10.0, float32((i + 1) % 10) / 10.0})
	}

	// Delete batch
	deleteCount := int64(0)
	for i := 1; i <= 10; i++ {
		if i%2 == 0 { // Delete even numbered
			idx.Delete(fmt.Sprintf("msg-%d", i))
			deleteCount++
		}
	}

	status := idx.GetDeletionStatus()
	if status.DeletedCount != deleteCount {
		t.Errorf("Expected %d deleted, got %d", deleteCount, status.DeletedCount)
	}
}

func TestHSNWUpdateBatch(t *testing.T) {
	// Test: Update multiple vectors
	// Expected: All embeddings updated in-place

	idx := knowledge.NewHSNWIndex(16, 200)

	// Insert 5
	for i := 1; i <= 5; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 5.0, float32(i) / 5.0})
	}

	// Update with new pattern - similar to one message to ensure exact match
	targetEmb := []float32{0.3, 0.9}
	for i := 1; i <= 5; i++ {
		if i == 3 {
			// msg-3 gets exact match embedding
			idx.Update(fmt.Sprintf("msg-%d", i), targetEmb)
		} else {
			// Others get similar but different
			newEmb := []float32{0.1 * float32(i), 0.8}
			idx.Update(fmt.Sprintf("msg-%d", i), newEmb)
		}
	}

	// Search with pattern from updates
	results := idx.Search(targetEmb, 1)

	if len(results) == 0 {
		t.Error("Expected to find updated message")
	}

	// Should find msg-3 (has exact match)
	if results[0].MessageID != "msg-3" {
		t.Errorf("Expected msg-3 as closest, got %s", results[0].MessageID)
	}
}

func TestHSNWDeleteUpdateDelete(t *testing.T) {
	// Test: Complex lifecycle (delete -> undelete -> update -> delete)
	// Expected: Proper state transitions

	idx := knowledge.NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})

	// Delete
	idx.Delete("msg-1")
	status := idx.GetDeletionStatus()
	if status.DeletedCount != 1 {
		t.Error("Step 1: Should be deleted")
	}

	// Undelete
	idx.Undelete("msg-1")
	status = idx.GetDeletionStatus()
	if status.DeletedCount != 0 {
		t.Error("Step 2: Should be undeleted")
	}

	// Update
	idx.Update("msg-1", []float32{0.5, 0.5})
	results := idx.Search([]float32{0.5, 0.5}, 1)
	if len(results) != 1 {
		t.Error("Step 3: Should find updated message")
	}

	// Delete again
	idx.Delete("msg-1")
	status = idx.GetDeletionStatus()
	if status.DeletedCount != 1 {
		t.Error("Step 4: Should be deleted again")
	}

	// Search should exclude
	results = idx.Search([]float32{0.5, 0.5}, 1)
	if len(results) > 0 {
		t.Error("Step 4: Should not find deleted message")
	}
}

func TestHSNWIncrementalPerformance(t *testing.T) {
	// Test: Incremental operations don't degrade index quality
	// Expected: Search still finds neighbors after updates/deletes

	idx := knowledge.NewHSNWIndex(16, 200)

	// Build initial index
	for i := 0; i < 50; i++ {
		embedding := []float32{float32(i) / 50.0, float32((i + 1) % 50) / 50.0}
		idx.Insert(fmt.Sprintf("msg-%d", i+1), embedding)
	}

	// Update half
	for i := 0; i < 25; i++ {
		newEmb := []float32{float32(i) / 100.0, float32((i + 50) % 100) / 100.0}
		idx.Update(fmt.Sprintf("msg-%d", i+1), newEmb)
	}

	// Delete 10%
	for i := 0; i < 5; i++ {
		idx.Delete(fmt.Sprintf("msg-%d", i+1))
	}

	// Search should still work
	query := []float32{0.25, 0.75}
	results := idx.Search(query, 5)

	if len(results) == 0 {
		t.Error("Should find vectors after incremental operations")
	}

	if len(results) > 5 {
		t.Errorf("Expected max 5 results, got %d", len(results))
	}
}
