package knowledge

import (
	"fmt"
	"sync"
	"time"
)

// ShardCompactor manages compaction across multiple HNSW indexes (shards).
type ShardCompactor struct {
	mu              sync.RWMutex
	shards          map[string]*HSNWIndex       // shardID -> index
	compactionState map[string]*CompactionState // shardID -> state
	globalPolicy    *CompactionPolicy
	activeJobs      int64
	maxConcurrent   int64
}

// CompactionState tracks compaction for a shard.
type CompactionState struct {
	ShardID          string
	State            string // "idle", "pending", "in_progress", "complete", "failed"
	Progress         *CompactionProgress
	StartTime        time.Time
	EndTime          time.Time
	ErrorMessage     string
}

// CompactionPolicy defines distributed compaction rules.
type CompactionPolicy struct {
	DeletionThreshold    float64       // Compact when ratio > threshold (0-100)
	MaxConcurrentShards  int64         // Max shards compacting simultaneously
	BatchSize            int64         // Entries per batch
	TimeoutSeconds       int64         // Max time per shard
	PreferredTimeWindow  string        // HH:MM-HH:MM
	CompactSmallShards   bool          // Compact even if < threshold
	SmallShardThreshold  int64         // Consider shard small if < this
}

// NewShardCompactor creates a distributed compaction coordinator.
func NewShardCompactor(maxConcurrent int64) *ShardCompactor {
	return &ShardCompactor{
		shards:          make(map[string]*HSNWIndex),
		compactionState: make(map[string]*CompactionState),
		maxConcurrent:   maxConcurrent,
		globalPolicy: &CompactionPolicy{
			DeletionThreshold:   10.0,
			MaxConcurrentShards: maxConcurrent,
			BatchSize:           10000,
			TimeoutSeconds:      300,
			CompactSmallShards:  false,
			SmallShardThreshold: 1000,
		},
	}
}

// RegisterShard registers an HNSW index for distributed compaction.
func (sc *ShardCompactor) RegisterShard(shardID string, index *HSNWIndex) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if _, exists := sc.shards[shardID]; exists {
		return fmt.Errorf("shard already registered: %s", shardID)
	}

	sc.shards[shardID] = index
	sc.compactionState[shardID] = &CompactionState{
		ShardID: shardID,
		State:   "idle",
	}

	return nil
}

// UnregisterShard removes a shard from compaction management.
func (sc *ShardCompactor) UnregisterShard(shardID string) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if _, exists := sc.shards[shardID]; !exists {
		return fmt.Errorf("shard not found: %s", shardID)
	}

	// Don't unregister while compacting
	if state, ok := sc.compactionState[shardID]; ok && state.State == "in_progress" {
		return fmt.Errorf("cannot unregister shard during compaction: %s", shardID)
	}

	delete(sc.shards, shardID)
	delete(sc.compactionState, shardID)

	return nil
}

// AnalyzeShardsForCompaction identifies shards that need compaction.
func (sc *ShardCompactor) AnalyzeShardsForCompaction() map[string]*CompactionAnalysis {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	analysis := make(map[string]*CompactionAnalysis)

	for shardID, index := range sc.shards {
		status := index.GetDeletionStatus()

		needsCompaction := false
		reason := ""

		// Check deletion threshold
		if status.DeletionRatio > sc.globalPolicy.DeletionThreshold {
			needsCompaction = true
			reason = fmt.Sprintf("deletion ratio %.1f%% > threshold %.1f%%",
				status.DeletionRatio, sc.globalPolicy.DeletionThreshold)
		}

		// Check small shard compaction
		if sc.globalPolicy.CompactSmallShards && status.TotalEntries < sc.globalPolicy.SmallShardThreshold {
			if status.DeletedCount > 0 {
				needsCompaction = true
				reason = fmt.Sprintf("small shard (%d entries) with %d deletions",
					status.TotalEntries, status.DeletedCount)
			}
		}

		analysis[shardID] = &CompactionAnalysis{
			ShardID:          shardID,
			TotalEntries:     status.TotalEntries,
			DeletedCount:     status.DeletedCount,
			DeletionRatio:    status.DeletionRatio,
			NeedsCompaction:  needsCompaction,
			Priority:         calculatePriority(status),
			Reason:           reason,
		}
	}

	return analysis
}

// CompactShardNow compacts a specific shard synchronously.
func (sc *ShardCompactor) CompactShardNow(shardID string) error {
	sc.mu.Lock()

	index, exists := sc.shards[shardID]
	if !exists {
		sc.mu.Unlock()
		return fmt.Errorf("shard not found: %s", shardID)
	}

	state := sc.compactionState[shardID]
	if state.State == "in_progress" {
		sc.mu.Unlock()
		return fmt.Errorf("compaction already in progress for shard: %s", shardID)
	}

	state.State = "in_progress"
	state.StartTime = time.Now()
	sc.activeJobs++

	sc.mu.Unlock()

	// Perform compaction
	err := index.Compact()

	// Update state
	sc.mu.Lock()
	if err != nil {
		state.State = "failed"
		state.ErrorMessage = err.Error()
	} else {
		state.State = "complete"
		state.EndTime = time.Now()
	}
	sc.activeJobs--
	sc.mu.Unlock()

	return err
}

// CompactShardsAsync compacts multiple shards respecting concurrency limit.
func (sc *ShardCompactor) CompactShardsAsync(shardIDs []string) *AsyncCompactionJob {
	sc.mu.Lock()

	job := &AsyncCompactionJob{
		ID:         fmt.Sprintf("job-%d", time.Now().UnixNano()),
		ShardIDs:   shardIDs,
		Results:    make(map[string]*CompactionState),
		StartTime:  time.Now(),
		State:      "pending",
	}

	sc.mu.Unlock()

	// Start compaction in background
	go sc.runAsyncCompaction(job)

	return job
}

// runAsyncCompaction executes async compaction with concurrency control.
func (sc *ShardCompactor) runAsyncCompaction(job *AsyncCompactionJob) {
	sc.mu.Lock()
	job.State = "in_progress"
	sc.mu.Unlock()

	semaphore := make(chan struct{}, sc.globalPolicy.MaxConcurrentShards)
	var wg sync.WaitGroup

	for _, shardID := range job.ShardIDs {
		wg.Add(1)

		go func(sid string) {
			defer wg.Done()

			// Acquire semaphore slot
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Perform compaction
			err := sc.CompactShardNow(sid)

			// Store result
			sc.mu.Lock()
			state := sc.compactionState[sid]
			job.Results[sid] = &CompactionState{
				ShardID:      sid,
				State:        state.State,
				ErrorMessage: state.ErrorMessage,
				StartTime:    state.StartTime,
				EndTime:      state.EndTime,
			}
			sc.mu.Unlock()

			if err != nil {
				job.FailureCount++
			} else {
				job.SuccessCount++
			}
		}(shardID)
	}

	// Wait for all to complete
	wg.Wait()

	sc.mu.Lock()
	job.State = "complete"
	job.EndTime = time.Now()
	sc.mu.Unlock()
}

// CompactAllNeeded compacts all shards requiring compaction.
func (sc *ShardCompactor) CompactAllNeeded() *AsyncCompactionJob {
	analysis := sc.AnalyzeShardsForCompaction()

	shardIDs := make([]string, 0)
	for shardID, info := range analysis {
		if info.NeedsCompaction {
			shardIDs = append(shardIDs, shardID)
		}
	}

	if len(shardIDs) == 0 {
		return nil // No shards need compaction
	}

	return sc.CompactShardsAsync(shardIDs)
}

// GetCompactionStatus returns status of all shard compactions.
func (sc *ShardCompactor) GetCompactionStatus() map[string]*CompactionState {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	status := make(map[string]*CompactionState)
	for shardID, state := range sc.compactionState {
		stateCopy := *state
		status[shardID] = &stateCopy
	}

	return status
}

// GetGlobalStats returns aggregated statistics across all shards.
func (sc *ShardCompactor) GetGlobalStats() *GlobalCompactionStats {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	stats := &GlobalCompactionStats{
		TotalShards:    int64(len(sc.shards)),
		TotalEntries:   0,
		TotalDeleted:   0,
		AverageDeletion: 0.0,
		ActiveJobs:     sc.activeJobs,
	}

	var deletionSum float64
	for _, index := range sc.shards {
		status := index.GetDeletionStatus()
		stats.TotalEntries += status.TotalEntries
		stats.TotalDeleted += status.DeletedCount
		deletionSum += status.DeletionRatio
	}

	if len(sc.shards) > 0 {
		stats.AverageDeletion = deletionSum / float64(len(sc.shards))
	}

	return stats
}

// UpdatePolicy updates compaction policy.
func (sc *ShardCompactor) UpdatePolicy(policy *CompactionPolicy) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if policy != nil {
		sc.globalPolicy = policy
	}
}

// CompactionAnalysis provides analysis for a shard.
type CompactionAnalysis struct {
	ShardID         string
	TotalEntries    int64
	DeletedCount    int64
	DeletionRatio   float64
	NeedsCompaction bool
	Priority        int   // 1-10, higher = more urgent
	Reason          string
}

// AsyncCompactionJob tracks async compaction progress.
type AsyncCompactionJob struct {
	ID           string
	ShardIDs     []string
	State        string // "pending", "in_progress", "complete"
	Results      map[string]*CompactionState
	SuccessCount int64
	FailureCount int64
	StartTime    time.Time
	EndTime      time.Time
}

// GlobalCompactionStats provides cross-shard statistics.
type GlobalCompactionStats struct {
	TotalShards     int64
	TotalEntries    int64
	TotalDeleted    int64
	AverageDeletion float64
	ActiveJobs      int64
}

// calculatePriority computes compaction urgency (1-10).
func calculatePriority(status *DeletionStatus) int {
	ratio := status.DeletionRatio
	if ratio < 5 {
		return 1
	}
	if ratio < 10 {
		return 3
	}
	if ratio < 15 {
		return 5
	}
	if ratio < 20 {
		return 7
	}
	return 10
}
