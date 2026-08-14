package knowledge

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// CrossShardCompactor manages incremental compaction across multiple shards.
type CrossShardCompactor struct {
	mu               sync.RWMutex
	shards           map[string]*HSNWIndex
	compactionPlan   *CompactionPlan
	executionHistory []CompactionExecution
	predictor        *PriorityPredictor
	currentExecution *CompactionExecution
	metrics          *CrossShardMetrics
}

// CompactionPlan defines strategy for cross-shard compaction.
type CompactionPlan struct {
	Strategy        string // "sequential", "interleaved", "parallel"
	MaxConcurrent   int64
	BatchesPerShard int64
	BatchSize       int64
	TimeoutSeconds  int64
	PreferredWindow string // HH:MM-HH:MM
	EstimatedTimeMs int64
	Priority        []string // Ordered shard IDs
}

// CompactionExecution tracks execution of a compaction plan.
type CompactionExecution struct {
	ID              string
	Plan            *CompactionPlan
	StartTime       time.Time
	EndTime         time.Time
	State           string // "pending", "running", "complete", "failed"
	ShardsCompleted int64
	TotalShards     int64
	EntriesRemoved  int64
	Duration        int64
	ErrorMessage    string
}

// CrossShardMetrics aggregates metrics across shards.
type CrossShardMetrics struct {
	TotalShards         int64
	TotalEntries        int64
	TotalDeleted        int64
	AverageDeletion     float64
	MaxDeletion         float64
	MinDeletion         float64
	CompactionFrequency map[string]int64 // Shard -> count
	LastCompactionTime  map[string]time.Time
}

// PriorityPredictor predicts compaction urgency based on patterns.
type PriorityPredictor struct {
	mu                  sync.RWMutex
	history             map[string][]PredictionDataPoint // Shard -> history
	deletionTrend       map[string]float64               // Shard -> deletion rate
	compactionFrequency map[string]int64                 // Shard -> times compacted
	timesSinceCompact   map[string]int64                 // Shard -> seconds
}

// PredictionDataPoint represents a historical data point.
type PredictionDataPoint struct {
	Timestamp     time.Time
	DeletionRatio float64
	EntryCount    int64
	Priority      int
}

// PriorityPrediction predicts when compaction is needed.
type PriorityPrediction struct {
	ShardID                     string
	CurrentDeletionRatio        float64
	PredictedDeletionRatio      float64
	TrendSlope                  float64 // Deletion rate (% per hour)
	EstimatedCompactTimeMinutes int64
	RecommendedPriority         int // 1-10
	Reason                      string
}

// NewCrossShardCompactor creates a cross-shard compaction manager.
func NewCrossShardCompactor() *CrossShardCompactor {
	return &CrossShardCompactor{
		shards:           make(map[string]*HSNWIndex),
		executionHistory: make([]CompactionExecution, 0),
		predictor:        NewPriorityPredictor(),
		metrics:          &CrossShardMetrics{},
	}
}

// RegisterShard adds a shard for cross-shard compaction.
func (csc *CrossShardCompactor) RegisterShard(shardID string, index *HSNWIndex) {
	csc.mu.Lock()
	defer csc.mu.Unlock()

	csc.shards[shardID] = index
	csc.predictor.Initialize(shardID)
}

// PlanCompaction creates an optimal compaction strategy.
func (csc *CrossShardCompactor) PlanCompaction(strategy string) *CompactionPlan {
	csc.mu.Lock()
	defer csc.mu.Unlock()

	// Get current state
	csc.updateMetrics()

	// Calculate priorities. PredictAll already returns them most-urgent-first
	// with a deterministic tie-break, so no second sort is needed here.
	priorities := csc.predictor.PredictAll(csc.shards)

	shardOrder := make([]string, len(priorities))
	for i, p := range priorities {
		shardOrder[i] = p.ShardID
	}

	// Estimate time
	estimatedMs := int64(0)
	for _, p := range priorities {
		// Rough estimate: 100 entries/ms
		deleted := int64(float64(csc.shards[p.ShardID].GetStats().IndexSize) * (p.CurrentDeletionRatio / 100.0))
		estimatedMs += deleted / 100
	}

	plan := &CompactionPlan{
		Strategy:        strategy,
		MaxConcurrent:   4,
		BatchesPerShard: 10,
		BatchSize:       5000,
		TimeoutSeconds:  300,
		EstimatedTimeMs: estimatedMs,
		Priority:        shardOrder,
	}

	csc.compactionPlan = plan
	return plan
}

// ExecutePlan executes the compaction plan.
func (csc *CrossShardCompactor) ExecutePlan(plan *CompactionPlan) error {
	csc.mu.Lock()

	execution := &CompactionExecution{
		ID:          fmt.Sprintf("exec-%d", time.Now().UnixNano()),
		Plan:        plan,
		StartTime:   time.Now(),
		State:       "running",
		TotalShards: int64(len(plan.Priority)),
	}

	csc.currentExecution = execution
	csc.mu.Unlock()

	// Execute based on strategy
	var err error
	switch plan.Strategy {
	case "sequential":
		err = csc.executeSequential(execution)
	case "interleaved":
		err = csc.executeInterleaved(execution)
	case "parallel":
		err = csc.executeParallel(execution)
	default:
		err = fmt.Errorf("unknown strategy: %s", plan.Strategy)
	}

	execution.EndTime = time.Now()
	execution.Duration = execution.EndTime.Sub(execution.StartTime).Milliseconds()

	if err != nil {
		execution.State = "failed"
		execution.ErrorMessage = err.Error()
	} else {
		execution.State = "complete"
	}

	csc.mu.Lock()
	csc.executionHistory = append(csc.executionHistory, *execution)
	csc.mu.Unlock()

	return err
}

// executeSequential runs shards one at a time.
func (csc *CrossShardCompactor) executeSequential(exec *CompactionExecution) error {
	for _, shardID := range exec.Plan.Priority {
		idx, ok := csc.shards[shardID]
		if !ok {
			continue
		}

		err := idx.Compact()
		if err != nil {
			return fmt.Errorf("shard %s failed: %w", shardID, err)
		}

		status := idx.GetDeletionStatus()
		exec.EntriesRemoved += status.DeletedCount
		exec.ShardsCompleted++
	}

	return nil
}

// executeInterleaved alternates between shards.
func (csc *CrossShardCompactor) executeInterleaved(exec *CompactionExecution) error {
	// Compact in round-robin fashion
	for _, shardID := range exec.Plan.Priority {
		idx, ok := csc.shards[shardID]
		if !ok {
			continue
		}

		for i := int64(0); i < exec.Plan.BatchesPerShard; i++ {
			progress := idx.CompactIncremental(exec.Plan.BatchSize)
			if progress.State == "complete" {
				break
			}
		}

		exec.ShardsCompleted++
	}

	return nil
}

// executeParallel runs shards concurrently.
func (csc *CrossShardCompactor) executeParallel(exec *CompactionExecution) error {
	semaphore := make(chan struct{}, exec.Plan.MaxConcurrent)
	var wg sync.WaitGroup

	for _, shardID := range exec.Plan.Priority {
		wg.Add(1)

		go func(sid string) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			idx, ok := csc.shards[sid]
			if !ok {
				return
			}

			_ = idx.Compact()
			// Every worker increments the same counter, so it needs an atomic
			// read-modify-write; a plain `++` here is a data race (and loses
			// completions) under concurrent shards.
			atomic.AddInt64(&exec.ShardsCompleted, 1)
		}(shardID)
	}

	wg.Wait()
	return nil
}

// updateMetrics refreshes cross-shard metrics.
func (csc *CrossShardCompactor) updateMetrics() {
	metrics := &CrossShardMetrics{
		TotalShards:         int64(len(csc.shards)),
		CompactionFrequency: make(map[string]int64),
		LastCompactionTime:  make(map[string]time.Time),
	}

	var deletionRatios []float64

	for _, idx := range csc.shards {
		status := idx.GetDeletionStatus()
		metrics.TotalEntries += status.TotalEntries
		metrics.TotalDeleted += status.DeletedCount

		deletionRatios = append(deletionRatios, status.DeletionRatio)
	}

	if len(deletionRatios) > 0 {
		// Calculate statistics
		sum := 0.0
		max := 0.0
		min := 100.0

		for _, ratio := range deletionRatios {
			sum += ratio
			if ratio > max {
				max = ratio
			}
			if ratio < min {
				min = ratio
			}
		}

		metrics.AverageDeletion = sum / float64(len(deletionRatios))
		metrics.MaxDeletion = max
		metrics.MinDeletion = min
	}

	csc.metrics = metrics
}

// GetMetrics returns current cross-shard metrics.
func (csc *CrossShardCompactor) GetMetrics() *CrossShardMetrics {
	csc.mu.Lock()
	defer csc.mu.Unlock()

	csc.updateMetrics()
	return csc.metrics
}

// GetExecutionHistory returns past executions.
func (csc *CrossShardCompactor) GetExecutionHistory(limit int) []CompactionExecution {
	csc.mu.RLock()
	defer csc.mu.RUnlock()

	if limit > len(csc.executionHistory) {
		limit = len(csc.executionHistory)
	}

	history := make([]CompactionExecution, limit)
	copy(history, csc.executionHistory[len(csc.executionHistory)-limit:])

	return history
}

// NewPriorityPredictor creates a priority prediction engine.
func NewPriorityPredictor() *PriorityPredictor {
	return &PriorityPredictor{
		history:             make(map[string][]PredictionDataPoint),
		deletionTrend:       make(map[string]float64),
		compactionFrequency: make(map[string]int64),
		timesSinceCompact:   make(map[string]int64),
	}
}

// Initialize sets up tracking for a shard.
func (pp *PriorityPredictor) Initialize(shardID string) {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	if _, exists := pp.history[shardID]; !exists {
		pp.history[shardID] = make([]PredictionDataPoint, 0)
		pp.deletionTrend[shardID] = 0.0
		pp.compactionFrequency[shardID] = 0
		pp.timesSinceCompact[shardID] = 0
	}
}

// RecordMeasurement adds a data point to history.
func (pp *PriorityPredictor) RecordMeasurement(shardID string, deletionRatio float64, entryCount int64) {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	point := PredictionDataPoint{
		Timestamp:     time.Now(),
		DeletionRatio: deletionRatio,
		EntryCount:    entryCount,
	}

	if _, exists := pp.history[shardID]; exists {
		pp.history[shardID] = append(pp.history[shardID], point)

		// Keep last 100 measurements
		if len(pp.history[shardID]) > 100 {
			pp.history[shardID] = pp.history[shardID][1:]
		}
	}
}

// PredictAll makes predictions for all shards.
func (pp *PriorityPredictor) PredictAll(shards map[string]*HSNWIndex) []*PriorityPrediction {
	pp.mu.RLock()
	defer pp.mu.RUnlock()

	predictions := make([]*PriorityPrediction, 0, len(shards))

	for shardID, idx := range shards {
		status := idx.GetDeletionStatus()

		pred := &PriorityPrediction{
			ShardID:                shardID,
			CurrentDeletionRatio:   status.DeletionRatio,
			PredictedDeletionRatio: status.DeletionRatio,
			TrendSlope:             pp.deletionTrend[shardID],
			RecommendedPriority:    calculatePriority(status),
		}

		// Estimate time until next compaction needed
		if status.DeletionRatio > 0 {
			timeToThreshold := int64((20.0 - status.DeletionRatio) / (status.DeletionRatio / 60.0))
			pred.EstimatedCompactTimeMinutes = timeToThreshold
		}

		switch {
		case status.DeletionRatio > 20:
			pred.Reason = "HIGH: deletion > 20%, immediate action needed"
		case status.DeletionRatio > 15:
			pred.Reason = "MEDIUM: deletion 15-20%, compact soon"
		case status.DeletionRatio > 10:
			pred.Reason = "LOW: deletion 10-15%, monitor"
		default:
			pred.Reason = "OK: deletion < 10%"
		}

		predictions = append(predictions, pred)
	}

	// Ranging over a map yields a random order, so sort before returning:
	// callers (and the compaction plan built from this list) depend on
	// most-urgent-first, and an unordered result would also make the same
	// inputs produce a different plan on every run. ShardID breaks ties so
	// the ordering is total.
	sort.Slice(predictions, func(i, j int) bool {
		if predictions[i].RecommendedPriority != predictions[j].RecommendedPriority {
			return predictions[i].RecommendedPriority > predictions[j].RecommendedPriority
		}
		return predictions[i].ShardID < predictions[j].ShardID
	})

	return predictions
}
