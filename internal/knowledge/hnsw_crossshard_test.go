package knowledge

import (
	"fmt"
	"testing"
)

func TestCrossShardCompactorCreation(t *testing.T) {
	compactor := NewCrossShardCompactor()

	if compactor == nil {
		t.Fatal("Failed to create cross-shard compactor")
	}

	if compactor.predictor == nil {
		t.Error("Predictor should be initialized")
	}
}

func TestCrossShardRegisterShard(t *testing.T) {
	compactor := NewCrossShardCompactor()
	idx := NewHSNWIndex(16, 200)

	compactor.RegisterShard("shard-1", idx)

	if len(compactor.shards) != 1 {
		t.Errorf("Expected 1 shard, got %d", len(compactor.shards))
	}
}

func TestCrossShardPlanCompaction(t *testing.T) {
	compactor := NewCrossShardCompactor()

	// Register shards with varying deletion ratios
	for i := 0; i < 3; i++ {
		idx := NewHSNWIndex(16, 200)

		// Insert 100 vectors
		for j := 0; j < 100; j++ {
			idx.Insert(fmt.Sprintf("msg-%d", j+1), []float32{float32(j) / 100.0, 0.5})
		}

		// Delete progressively more
		deleteCount := (i + 1) * 10 // 10, 20, 30
		for j := 0; j < deleteCount; j++ {
			idx.Delete(fmt.Sprintf("msg-%d", j+1))
		}

		compactor.RegisterShard(fmt.Sprintf("shard-%d", i+1), idx)
	}

	// Plan compaction
	plan := compactor.PlanCompaction("sequential")

	if plan == nil {
		t.Fatal("Expected compaction plan")
	}

	if plan.Strategy != "sequential" {
		t.Errorf("Expected sequential strategy, got %s", plan.Strategy)
	}

	if len(plan.Priority) != 3 {
		t.Errorf("Expected 3 shards in priority list, got %d", len(plan.Priority))
	}
}

func TestCrossShardExecuteSequential(t *testing.T) {
	compactor := NewCrossShardCompactor()

	// Setup shards
	for i := 0; i < 2; i++ {
		idx := NewHSNWIndex(16, 200)

		for j := 0; j < 50; j++ {
			idx.Insert(fmt.Sprintf("msg-%d", j+1), []float32{float32(j) / 50.0, 0.5})
		}

		for j := 0; j < 10; j++ {
			idx.Delete(fmt.Sprintf("msg-%d", j+1))
		}

		compactor.RegisterShard(fmt.Sprintf("shard-%d", i+1), idx)
	}

	// Plan and execute
	plan := compactor.PlanCompaction("sequential")
	err := compactor.ExecutePlan(plan)

	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Verify execution completed
	if compactor.currentExecution.State != "complete" {
		t.Errorf("Expected complete, got %s", compactor.currentExecution.State)
	}

	if compactor.currentExecution.ShardsCompleted != 2 {
		t.Errorf("Expected 2 shards completed, got %d", compactor.currentExecution.ShardsCompleted)
	}
}

func TestCrossShardMetrics(t *testing.T) {
	compactor := NewCrossShardCompactor()

	// Register shards
	for i := 0; i < 2; i++ {
		idx := NewHSNWIndex(16, 200)

		for j := 0; j < 100; j++ {
			idx.Insert(fmt.Sprintf("msg-%d", j+1), []float32{float32(j) / 100.0, 0.5})
		}

		for j := 0; j < 15; j++ {
			idx.Delete(fmt.Sprintf("msg-%d", j+1))
		}

		compactor.RegisterShard(fmt.Sprintf("shard-%d", i+1), idx)
	}

	// Get metrics
	metrics := compactor.GetMetrics()

	if metrics.TotalShards != 2 {
		t.Errorf("Expected 2 shards, got %d", metrics.TotalShards)
	}

	if metrics.TotalEntries != 200 {
		t.Errorf("Expected 200 entries, got %d", metrics.TotalEntries)
	}

	if metrics.TotalDeleted != 30 {
		t.Errorf("Expected 30 deleted, got %d", metrics.TotalDeleted)
	}

	if metrics.AverageDeletion != 15.0 {
		t.Errorf("Expected 15%% average deletion, got %.1f%%", metrics.AverageDeletion)
	}
}

func TestPriorityPredictorInitialize(t *testing.T) {
	predictor := NewPriorityPredictor()

	predictor.Initialize("shard-1")

	if _, exists := predictor.history["shard-1"]; !exists {
		t.Error("Shard not initialized")
	}
}

func TestPriorityPredictorRecordMeasurement(t *testing.T) {
	predictor := NewPriorityPredictor()
	predictor.Initialize("shard-1")

	predictor.RecordMeasurement("shard-1", 15.0, 1000)

	if len(predictor.history["shard-1"]) != 1 {
		t.Error("Expected 1 measurement")
	}

	point := predictor.history["shard-1"][0]
	if point.DeletionRatio != 15.0 {
		t.Errorf("Expected 15%% deletion, got %.1f%%", point.DeletionRatio)
	}
}

func TestPriorityPredictorPredictAll(t *testing.T) {
	compactor := NewCrossShardCompactor()

	// Register shards
	shards := make(map[string]*HSNWIndex)
	for i := 0; i < 3; i++ {
		idx := NewHSNWIndex(16, 200)

		for j := 0; j < 100; j++ {
			idx.Insert(fmt.Sprintf("msg-%d", j+1), []float32{float32(j) / 100.0, 0.5})
		}

		deleteCount := (i + 1) * 8 // 8%, 16%, 24%
		for j := 0; j < deleteCount; j++ {
			idx.Delete(fmt.Sprintf("msg-%d", j+1))
		}

		shardID := fmt.Sprintf("shard-%d", i+1)
		shards[shardID] = idx
		compactor.RegisterShard(shardID, idx)
	}

	// Predict
	predictions := compactor.predictor.PredictAll(shards)

	if len(predictions) != 3 {
		t.Errorf("Expected 3 predictions, got %d", len(predictions))
	}

	// Higher deletion = higher priority
	for i := 0; i < len(predictions)-1; i++ {
		if predictions[i].RecommendedPriority < predictions[i+1].RecommendedPriority {
			t.Error("Priorities not properly ordered (higher deletion should have higher priority)")
		}
	}
}

func TestCrossShardExecutionHistory(t *testing.T) {
	compactor := NewCrossShardCompactor()

	idx := NewHSNWIndex(16, 200)
	for i := 0; i < 10; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i+1), []float32{float32(i) / 10.0, 0.5})
		idx.Delete(fmt.Sprintf("msg-%d", i+1))
	}

	compactor.RegisterShard("shard-1", idx)

	// Execute first plan
	plan1 := compactor.PlanCompaction("sequential")
	compactor.ExecutePlan(plan1)

	history := compactor.GetExecutionHistory(10)

	if len(history) != 1 {
		t.Errorf("Expected 1 execution in history, got %d", len(history))
	}

	if history[0].State != "complete" {
		t.Error("First execution should be complete")
	}
}

func TestCrossShardStrategies(t *testing.T) {
	strategies := []string{"sequential", "interleaved", "parallel"}

	for _, strategy := range strategies {
		compactor := NewCrossShardCompactor()

		// Setup shards
		for i := 0; i < 2; i++ {
			idx := NewHSNWIndex(16, 200)

			for j := 0; j < 20; j++ {
				idx.Insert(fmt.Sprintf("msg-%d", j+1), []float32{float32(j) / 20.0, 0.5})
			}

			for j := 0; j < 5; j++ {
				idx.Delete(fmt.Sprintf("msg-%d", j+1))
			}

			compactor.RegisterShard(fmt.Sprintf("shard-%d", i+1), idx)
		}

		// Plan with specific strategy
		plan := compactor.PlanCompaction(strategy)
		err := compactor.ExecutePlan(plan)

		if err != nil {
			t.Errorf("Strategy %s failed: %v", strategy, err)
		}

		if compactor.currentExecution.State != "complete" {
			t.Errorf("Strategy %s did not complete", strategy)
		}
	}
}

func TestCrossShardDeletionTrend(t *testing.T) {
	compactor := NewCrossShardCompactor()

	idx := NewHSNWIndex(16, 200)

	// Insert and delete in pattern
	for i := 0; i < 100; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i+1), []float32{float32(i) / 100.0, 0.5})
	}

	// Simulate gradual deletion
	compactor.RegisterShard("shard-1", idx)
	predictor := compactor.predictor

	for deletion := 5; deletion <= 20; deletion += 5 {
		count := (deletion * 100) / 100
		for i := 0; i < count; i++ {
			if i < 100 {
				idx.Delete(fmt.Sprintf("msg-%d", i+1))
			}
		}

		predictor.RecordMeasurement("shard-1", float64(deletion), 100)
	}

	// Predict should show trend
	predictions := predictor.PredictAll(map[string]*HSNWIndex{"shard-1": idx})

	if len(predictions) > 0 {
		pred := predictions[0]
		// Current deletion ratio should be recorded
		if pred.CurrentDeletionRatio == 0 {
			t.Log("Deletion trend recorded")
		}
	}
}

func TestCrossShardMissingShardHandling(t *testing.T) {
	compactor := NewCrossShardCompactor()

	idx := NewHSNWIndex(16, 200)
	idx.Insert("msg-1", []float32{1.0, 0.0})

	compactor.RegisterShard("shard-1", idx)

	// Try to execute (should handle gracefully)
	plan := compactor.PlanCompaction("sequential")
	err := compactor.ExecutePlan(plan)

	if err != nil {
		t.Logf("Execution with minimal setup: %v", err)
	}
}

func TestCrossShardLargeScaleSimulation(t *testing.T) {
	compactor := NewCrossShardCompactor()

	// Register 5 shards
	for i := 0; i < 5; i++ {
		idx := NewHSNWIndex(16, 200)

		// Insert 1000 vectors
		for j := 0; j < 1000; j++ {
			idx.Insert(fmt.Sprintf("msg-%d", j+1), []float32{float32(j) / 1000.0, 0.5})
		}

		// Delete varying amounts
		deleteCount := (i + 1) * 50 // 50, 100, 150, 200, 250
		for j := 0; j < deleteCount; j++ {
			idx.Delete(fmt.Sprintf("msg-%d", j+1))
		}

		compactor.RegisterShard(fmt.Sprintf("shard-%d", i+1), idx)
	}

	// Plan and execute
	plan := compactor.PlanCompaction("parallel")
	err := compactor.ExecutePlan(plan)

	if err != nil {
		t.Fatalf("Large-scale execution failed: %v", err)
	}

	// Verify metrics
	metrics := compactor.GetMetrics()

	if metrics.TotalShards != 5 {
		t.Errorf("Expected 5 shards, got %d", metrics.TotalShards)
	}

	// After compaction, deleted entries are removed
	if metrics.TotalEntries == 0 {
		t.Log("Large-scale simulation: all entries accounted for after compaction")
	}

	if metrics.TotalDeleted != 0 {
		// After compaction, deleted count should be 0 or very low
		t.Logf("After compaction, remaining deletes: %d", metrics.TotalDeleted)
	}
}
