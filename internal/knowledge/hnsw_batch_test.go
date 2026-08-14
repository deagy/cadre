package knowledge

import (
	"fmt"
	"testing"
)

func TestBatchDelete(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert vectors
	for i := 1; i <= 10; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i), float32(i + 1)})
	}

	// Batch delete
	ids := []string{"msg-1", "msg-2", "msg-3"}
	result := idx.BatchDelete(ids)

	if result.Successful != 3 {
		t.Errorf("Expected 3 successful, got %d", result.Successful)
	}

	if result.Failed != 0 {
		t.Errorf("Expected 0 failed, got %d", result.Failed)
	}

	status := idx.GetDeletionStatus()
	if status.DeletedCount != 3 {
		t.Errorf("Expected 3 deleted, got %d", status.DeletedCount)
	}
}

func TestBatchDeleteWithErrors(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Insert("msg-2", []float32{0.5, 0.5})

	// Delete one, then try again (should fail)
	idx.Delete("msg-1")

	// Batch with mix of valid and invalid
	ids := []string{"msg-1", "msg-2", "msg-99"}
	result := idx.BatchDelete(ids)

	if result.Successful != 1 {
		t.Errorf("Expected 1 successful, got %d", result.Successful)
	}

	if result.Failed != 2 {
		t.Errorf("Expected 2 failed, got %d", result.Failed)
	}

	if len(result.Errors) != 2 {
		t.Errorf("Expected 2 errors, got %d", len(result.Errors))
	}
}

func TestBatchUndelete(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert and delete
	for i := 1; i <= 5; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i), float32(i + 1)})
		idx.Delete(fmt.Sprintf("msg-%d", i))
	}

	status := idx.GetDeletionStatus()
	if status.DeletedCount != 5 {
		t.Error("Setup: expected 5 deleted")
	}

	// Batch undelete
	ids := []string{"msg-1", "msg-2", "msg-3"}
	result := idx.BatchUndelete(ids)

	if result.Successful != 3 {
		t.Errorf("Expected 3 successful, got %d", result.Successful)
	}

	status = idx.GetDeletionStatus()
	if status.DeletedCount != 2 {
		t.Errorf("Expected 2 deleted after undelete, got %d", status.DeletedCount)
	}
}

func TestBatchUpdate(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert vectors
	for i := 1; i <= 5; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i), float32(i + 1)})
	}

	// Batch update
	updates := map[string][]float32{
		"msg-1": {0.1, 0.2},
		"msg-2": {0.2, 0.3},
		"msg-3": {0.3, 0.4},
	}

	result := idx.BatchUpdate(updates)

	if result.Successful != 3 {
		t.Errorf("Expected 3 successful, got %d", result.Successful)
	}

	if result.Failed != 0 {
		t.Errorf("Expected 0 failed, got %d", result.Failed)
	}

	// Verify updates
	results := idx.Search([]float32{0.1, 0.2}, 1)
	if len(results) != 1 || results[0].MessageID != "msg-1" {
		t.Error("Updated message not found in search")
	}
}

func TestBatchUpdateWithErrors(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Insert("msg-2", []float32{0.5, 0.5})
	idx.Delete("msg-3") // Non-existent yet

	updates := map[string][]float32{
		"msg-1":  {0.1, 0.2},
		"msg-2":  {0.2, 0.3},
		"msg-99": {0.99, 0.99}, // Non-existent
	}

	result := idx.BatchUpdate(updates)

	if result.Successful != 2 {
		t.Errorf("Expected 2 successful, got %d", result.Successful)
	}

	if result.Failed != 1 {
		t.Errorf("Expected 1 failed, got %d", result.Failed)
	}
}

func TestBatchUpdateDeleted(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Delete("msg-1")

	// Try to update deleted
	updates := map[string][]float32{
		"msg-1": {0.5, 0.5},
	}

	result := idx.BatchUpdate(updates)

	if result.Successful != 0 {
		t.Error("Should not update deleted message")
	}

	if result.Failed != 1 {
		t.Error("Should count as failed")
	}
}

func TestCompactIncremental(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert 10 vectors
	for i := 1; i <= 10; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 10.0, float32((i+1)%10) / 10.0})
	}

	// Delete 5
	for i := 1; i <= 5; i++ {
		idx.Delete(fmt.Sprintf("msg-%d", i))
	}

	// Incremental compact with small batch
	progress := idx.CompactIncremental(2)

	if progress.State != "in_progress" && progress.State != "complete" {
		t.Errorf("Unexpected state: %s", progress.State)
	}

	if progress.EntriesRemoved < 1 {
		t.Error("Should remove some entries in batch")
	}

	if progress.EntriesRemoved > 5 {
		t.Error("Should not exceed deleted count")
	}
}

func TestCompactIncrementalComplete(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert vectors
	for i := 1; i <= 20; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 20.0, float32((i+1)%20) / 20.0})
	}

	// Delete 10
	for i := 1; i <= 10; i++ {
		idx.Delete(fmt.Sprintf("msg-%d", i))
	}

	// Incremental compact with large batch (processes all)
	progress := idx.CompactIncremental(100)

	if progress.EntriesRemoved != 10 {
		t.Errorf("Expected 10 removed, got %d", progress.EntriesRemoved)
	}

	// Check deletion status
	status := idx.GetDeletionStatus()
	if status.DeletedCount != 0 {
		t.Error("All deleted should be removed")
	}
}

func TestCompactIncrementalEmpty(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})

	// Compact with no deletions
	progress := idx.CompactIncremental(10)

	if progress.State != "complete" {
		t.Error("Should complete immediately when no deletions")
	}

	if progress.EntriesRemoved != 0 {
		t.Error("Should not remove anything")
	}
}

func TestGetCompactionProgress(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Delete("msg-1")

	progress := idx.GetCompactionProgress()

	if progress.State != "idle" {
		t.Errorf("Expected idle state, got %s", progress.State)
	}

	if progress.EntriesRemaining != 1 {
		t.Errorf("Expected 1 remaining, got %d", progress.EntriesRemaining)
	}
}

func TestBatchOperationsPerformance(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert 100 vectors
	for i := 1; i <= 100; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 100.0, float32((i+1)%100) / 100.0})
	}

	// Batch delete 30
	deleteIDs := make([]string, 0, 30)
	for i := 1; i <= 30; i++ {
		deleteIDs = append(deleteIDs, fmt.Sprintf("msg-%d", i))
	}
	deleteResult := idx.BatchDelete(deleteIDs)

	if deleteResult.Successful != 30 {
		t.Errorf("Expected 30 deleted, got %d", deleteResult.Successful)
	}

	// Batch update 20
	updates := make(map[string][]float32)
	for i := 31; i <= 50; i++ {
		updates[fmt.Sprintf("msg-%d", i)] = []float32{0.1 * float32(i), 0.9}
	}
	updateResult := idx.BatchUpdate(updates)

	if updateResult.Successful != 20 {
		t.Errorf("Expected 20 updated, got %d", updateResult.Successful)
	}

	// Batch undelete 15
	undeleteIDs := deleteIDs[:15]
	undeleteResult := idx.BatchUndelete(undeleteIDs)

	if undeleteResult.Successful != 15 {
		t.Errorf("Expected 15 undeleted, got %d", undeleteResult.Successful)
	}

	status := idx.GetDeletionStatus()
	if status.DeletedCount != 15 {
		t.Errorf("Expected 15 still deleted, got %d", status.DeletedCount)
	}
}

func TestIncrementalCompactionProgression(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert 50 vectors
	for i := 1; i <= 50; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 50.0, float32((i+1)%50) / 50.0})
	}

	// Delete 30
	for i := 1; i <= 30; i++ {
		idx.Delete(fmt.Sprintf("msg-%d", i))
	}

	removed := int64(0)

	// Compact in batches
	for removed < 30 {
		progress := idx.CompactIncremental(5)

		if progress.EntriesRemoved == 0 {
			break
		}

		removed += progress.EntriesRemoved
	}

	if removed < 30 {
		t.Errorf("Expected to remove ~30 total, removed %d", removed)
	}

	status := idx.GetDeletionStatus()
	if status.DeletedCount > 5 {
		t.Errorf("After incremental compaction, should have few deletes left, got %d", status.DeletedCount)
	}
}
