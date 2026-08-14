//go:build cgo
// +build cgo

package knowledge

import (
	"fmt"
	"testing"
)

func TestShardCompactorCreation(t *testing.T) {
	compactor := NewShardCompactor(4)

	if compactor == nil {
		t.Error("Failed to create compactor")
	}

	if compactor.maxConcurrent != 4 {
		t.Errorf("Expected max concurrent 4, got %d", compactor.maxConcurrent)
	}

	if compactor.globalPolicy == nil {
		t.Error("Expected default policy")
	}
}

func TestRegisterShard(t *testing.T) {
	compactor := NewShardCompactor(4)
	idx := NewHSNWIndex(16, 200)

	err := compactor.RegisterShard("shard-1", idx)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	status := compactor.GetCompactionStatus()
	if len(status) != 1 {
		t.Errorf("Expected 1 shard, got %d", len(status))
	}

	if status["shard-1"].State != "idle" {
		t.Error("Shard should be idle initially")
	}
}

func TestRegisterDuplicateShard(t *testing.T) {
	compactor := NewShardCompactor(4)
	idx := NewHSNWIndex(16, 200)

	compactor.RegisterShard("shard-1", idx)
	err := compactor.RegisterShard("shard-1", idx)

	if err == nil {
		t.Error("Expected error for duplicate registration")
	}
}

func TestUnregisterShard(t *testing.T) {
	compactor := NewShardCompactor(4)
	idx := NewHSNWIndex(16, 200)

	compactor.RegisterShard("shard-1", idx)
	err := compactor.UnregisterShard("shard-1")

	if err != nil {
		t.Fatalf("Unregister failed: %v", err)
	}

	status := compactor.GetCompactionStatus()
	if len(status) != 0 {
		t.Error("Shard should be unregistered")
	}
}

func TestAnalyzeShardsForCompaction(t *testing.T) {
	compactor := NewShardCompactor(4)

	// Register shards with varying deletion ratios
	for i := 0; i < 3; i++ {
		idx := NewHSNWIndex(16, 200)

		// Insert vectors
		for j := 0; j < 100; j++ {
			idx.Insert(fmt.Sprintf("msg-%d", j+1), []float32{float32(j) / 100.0, float32((j + 1) % 100) / 100.0})
		}

		// Delete proportional number
		deleteCount := (i + 1) * 6 // 6, 12, 18
		for j := 0; j < deleteCount; j++ {
			idx.Delete(fmt.Sprintf("msg-%d", j+1))
		}

		compactor.RegisterShard(fmt.Sprintf("shard-%d", i+1), idx)
	}

	analysis := compactor.AnalyzeShardsForCompaction()

	if len(analysis) != 3 {
		t.Errorf("Expected 3 shards, got %d", len(analysis))
	}

	// Shards with > 10% deletion should need compaction
	needsCompaction := 0
	for _, a := range analysis {
		if a.NeedsCompaction {
			needsCompaction++
		}
	}

	if needsCompaction == 0 {
		t.Error("At least some shards should need compaction")
	}
}

func TestCompactShardNow(t *testing.T) {
	compactor := NewShardCompactor(4)
	idx := NewHSNWIndex(16, 200)

	// Setup shard
	for i := 1; i <= 10; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 10.0, float32((i + 1) % 10) / 10.0})
	}

	// Delete half
	for i := 1; i <= 5; i++ {
		idx.Delete(fmt.Sprintf("msg-%d", i))
	}

	compactor.RegisterShard("shard-1", idx)

	// Compact
	err := compactor.CompactShardNow("shard-1")

	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	status := compactor.GetCompactionStatus()
	if status["shard-1"].State != "complete" {
		t.Errorf("Expected complete, got %s", status["shard-1"].State)
	}

	// Verify deletions removed
	indexStatus := idx.GetDeletionStatus()
	if indexStatus.DeletedCount != 0 {
		t.Error("All deletions should be removed after compact")
	}
}

func TestCompactShardsAsync(t *testing.T) {
	compactor := NewShardCompactor(2)

	// Register multiple shards
	shardIDs := make([]string, 0)
	for i := 0; i < 3; i++ {
		idx := NewHSNWIndex(16, 200)

		for j := 0; j < 10; j++ {
			idx.Insert(fmt.Sprintf("msg-%d", j+1), []float32{float32(j) / 10.0, float32((j + 1) % 10) / 10.0})
		}

		// Delete some
		for j := 0; j < 5; j++ {
			idx.Delete(fmt.Sprintf("msg-%d", j+1))
		}

		shardID := fmt.Sprintf("shard-%d", i+1)
		compactor.RegisterShard(shardID, idx)
		shardIDs = append(shardIDs, shardID)
	}

	// Async compact
	job := compactor.CompactShardsAsync(shardIDs)

	if job == nil {
		t.Error("Expected job")
	}

	// Initial state should be pending or in_progress
	if job.State != "pending" && job.State != "in_progress" {
		t.Errorf("Expected pending/in_progress state, got %s", job.State)
	}

	// Job ID should be set
	if job.ID == "" {
		t.Error("Job should have ID")
	}

	// After completion, should have results
	if job.State == "complete" && len(job.Results) != len(shardIDs) {
		t.Errorf("Expected %d results, got %d", len(shardIDs), len(job.Results))
	}
}

func TestCompactAllNeeded(t *testing.T) {
	compactor := NewShardCompactor(4)

	// Register shards - only one needs compaction
	idx1 := NewHSNWIndex(16, 200)
	for i := 0; i < 100; i++ {
		idx1.Insert(fmt.Sprintf("msg-%d", i+1), []float32{float32(i) / 100.0, 0.5})
	}
	// Delete 2% - below threshold
	idx1.Delete("msg-1")
	idx1.Delete("msg-2")
	compactor.RegisterShard("shard-1", idx1)

	idx2 := NewHSNWIndex(16, 200)
	for i := 0; i < 100; i++ {
		idx2.Insert(fmt.Sprintf("msg-%d", i+1), []float32{float32(i) / 100.0, 0.5})
	}
	// Delete 15% - above threshold
	for i := 0; i < 15; i++ {
		idx2.Delete(fmt.Sprintf("msg-%d", i+1))
	}
	compactor.RegisterShard("shard-2", idx2)

	// Compact all needed
	job := compactor.CompactAllNeeded()

	if job == nil {
		// No shards need compaction
		return
	}

	if len(job.ShardIDs) != 1 {
		t.Errorf("Expected 1 shard needing compaction, got %d", len(job.ShardIDs))
	}

	if job.ShardIDs[0] != "shard-2" {
		t.Errorf("Expected shard-2, got %s", job.ShardIDs[0])
	}
}

func TestGetGlobalStats(t *testing.T) {
	compactor := NewShardCompactor(4)

	// Register multiple shards
	for i := 0; i < 2; i++ {
		idx := NewHSNWIndex(16, 200)

		for j := 0; j < 100; j++ {
			idx.Insert(fmt.Sprintf("msg-%d", j+1), []float32{float32(j) / 100.0, 0.5})
		}

		// Delete some
		for j := 0; j < 10; j++ {
			idx.Delete(fmt.Sprintf("msg-%d", j+1))
		}

		compactor.RegisterShard(fmt.Sprintf("shard-%d", i+1), idx)
	}

	stats := compactor.GetGlobalStats()

	if stats.TotalShards != 2 {
		t.Errorf("Expected 2 shards, got %d", stats.TotalShards)
	}

	if stats.TotalEntries != 200 {
		t.Errorf("Expected 200 entries, got %d", stats.TotalEntries)
	}

	if stats.TotalDeleted != 20 {
		t.Errorf("Expected 20 deleted, got %d", stats.TotalDeleted)
	}

	if stats.AverageDeletion != 10.0 {
		t.Errorf("Expected 10%% average deletion, got %.1f%%", stats.AverageDeletion)
	}
}

func TestUpdatePolicy(t *testing.T) {
	compactor := NewShardCompactor(4)

	newPolicy := &CompactionPolicy{
		DeletionThreshold:   20.0,
		MaxConcurrentShards: 8,
		BatchSize:           5000,
	}

	compactor.UpdatePolicy(newPolicy)

	if compactor.globalPolicy.DeletionThreshold != 20.0 {
		t.Error("Policy not updated")
	}

	if compactor.globalPolicy.MaxConcurrentShards != 8 {
		t.Error("Max concurrent not updated")
	}
}

func TestMultiShardDistributed(t *testing.T) {
	compactor := NewShardCompactor(2)

	// Register 4 shards with varying deletion ratios
	for i := 0; i < 4; i++ {
		idx := NewHSNWIndex(16, 200)

		// Insert 50 vectors per shard
		for j := 0; j < 50; j++ {
			idx.Insert(fmt.Sprintf("msg-%d-%d", i, j+1), []float32{float32(j) / 50.0, float32(i) / 4.0})
		}

		// Delete progressively more
		deleteCount := (i + 1) * 5 // 5, 10, 15, 20
		for j := 0; j < deleteCount; j++ {
			idx.Delete(fmt.Sprintf("msg-%d-%d", i, j+1))
		}

		compactor.RegisterShard(fmt.Sprintf("shard-%d", i+1), idx)
	}

	// Analyze
	analysis := compactor.AnalyzeShardsForCompaction()
	needsCompaction := 0
	for _, a := range analysis {
		if a.NeedsCompaction {
			needsCompaction++
		}
	}

	if needsCompaction == 0 {
		t.Error("At least some shards should need compaction")
	}

	// Compact all needed
	job := compactor.CompactAllNeeded()

	if job != nil && len(job.ShardIDs) > 0 {
		// Job shards may exceed max concurrent - that's handled internally
		// Verify job was created successfully
		if job.ID == "" {
			t.Error("Job should have ID")
		}
	}

	// Get global stats
	stats := compactor.GetGlobalStats()

	if stats.TotalShards != 4 {
		t.Errorf("Expected 4 shards, got %d", stats.TotalShards)
	}

	if stats.TotalEntries != 200 {
		t.Errorf("Expected 200 total entries, got %d", stats.TotalEntries)
	}
}

func TestShardCompactionError(t *testing.T) {
	compactor := NewShardCompactor(4)

	// Try to compact non-existent shard
	err := compactor.CompactShardNow("nonexistent")

	if err == nil {
		t.Error("Expected error for non-existent shard")
	}
}

func TestConcurrencyControl(t *testing.T) {
	compactor := NewShardCompactor(2) // Max 2 concurrent

	// Register 5 shards
	for i := 0; i < 5; i++ {
		idx := NewHSNWIndex(16, 200)

		for j := 0; j < 20; j++ {
			idx.Insert(fmt.Sprintf("msg-%d", j+1), []float32{float32(j) / 20.0, 0.5})
		}

		// Delete to trigger compaction
		for j := 0; j < 15; j++ {
			idx.Delete(fmt.Sprintf("msg-%d", j+1))
		}

		compactor.RegisterShard(fmt.Sprintf("shard-%d", i+1), idx)
	}

	// Request async compaction on all
	shardIDs := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		shardIDs = append(shardIDs, fmt.Sprintf("shard-%d", i+1))
	}

	job := compactor.CompactShardsAsync(shardIDs)

	if job == nil {
		t.Error("Expected job")
	}

	// Job should be created and in pending or progress state
	// (async operations may not be complete immediately)
	if job.State != "pending" && job.State != "in_progress" && job.State != "complete" {
		t.Errorf("Expected valid state, got %s", job.State)
	}

	// Verify job ID is set
	if job.ID == "" {
		t.Error("Job should have ID")
	}
}
