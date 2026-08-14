package knowledge

import (
	"fmt"
	"sync"
	"time"
)

// RebalancingPolicy defines when and how to trigger rebalancing.
type RebalancingPolicy struct {
	Enabled                 bool
	CheckIntervalSeconds    int     // How often to check (default: 3600 = 1 hour)
	ImbalanceThreshold      float64 // Trigger if std dev > this (default: 20%)
	MinimumHotShardPercent  float64 // Min % for hot shard (default: 60%)
	MaxConcurrentMigrations int     // Max parallel migrations (default: 2)
	MaintenanceWindowStart  string  // Time window start (HH:MM UTC, optional)
	MaintenanceWindowEnd    string  // Time window end (HH:MM UTC, optional)
	MaxMessagesPerMigration int     // Batch size (default: 1000)
	NotifyOnCompletion      bool    // Send notifications
}

// DefaultRebalancingPolicy returns sensible defaults.
func DefaultRebalancingPolicy() *RebalancingPolicy {
	return &RebalancingPolicy{
		Enabled:                 true,
		CheckIntervalSeconds:    3600, // 1 hour
		ImbalanceThreshold:      20.0, // 20% std dev
		MinimumHotShardPercent:  60.0, // 60% capacity
		MaxConcurrentMigrations: 2,    // 2 parallel
		MaxMessagesPerMigration: 1000, // 1000 msgs per batch
		NotifyOnCompletion:      false,
	}
}

// ScheduledMigrationJob represents a scheduled migration task.
type ScheduledMigrationJob struct {
	ID            string
	ScheduledTime time.Time
	ExecutedTime  *time.Time
	Status        string // scheduled, executing, completed, failed, cancelled
	SourceShardID string
	DestShardID   string
	MessageCount  int
	ErrorReason   string
	Duration      time.Duration
}

// RebalancingScheduler manages automatic rebalancing operations.
type RebalancingScheduler struct {
	policy            *RebalancingPolicy
	rebalancer        *ShardRebalancer
	executor          *MigrationExecutor
	mu                sync.RWMutex
	isRunning         bool
	stopChan          chan struct{}
	jobs              map[string]*ScheduledMigrationJob
	lastCheckTime     *time.Time
	activeMigrations  int
	totalJobsExecuted int64
	totalJobsFailed   int64
}

// NewRebalancingScheduler creates a new scheduler with the given policy.
func NewRebalancingScheduler(
	policy *RebalancingPolicy,
	rebalancer *ShardRebalancer,
	executor *MigrationExecutor,
) *RebalancingScheduler {
	if policy == nil {
		policy = DefaultRebalancingPolicy()
	}

	return &RebalancingScheduler{
		policy:     policy,
		rebalancer: rebalancer,
		executor:   executor,
		stopChan:   make(chan struct{}),
		jobs:       make(map[string]*ScheduledMigrationJob),
	}
}

// Start begins the scheduling loop.
func (rs *RebalancingScheduler) Start() error {
	rs.mu.Lock()
	if rs.isRunning {
		rs.mu.Unlock()
		return fmt.Errorf("scheduler already running")
	}
	rs.isRunning = true
	rs.mu.Unlock()

	go rs.schedulingLoop()
	return nil
}

// Stop halts the scheduling loop.
func (rs *RebalancingScheduler) Stop() error {
	rs.mu.Lock()
	if !rs.isRunning {
		rs.mu.Unlock()
		return fmt.Errorf("scheduler not running")
	}
	rs.isRunning = false
	rs.mu.Unlock()

	rs.stopChan <- struct{}{}
	return nil
}

// schedulingLoop runs the main scheduling loop.
func (rs *RebalancingScheduler) schedulingLoop() {
	ticker := time.NewTicker(time.Duration(rs.policy.CheckIntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-rs.stopChan:
			return
		case <-ticker.C:
			rs.performScheduledCheck()
		}
	}
}

// performScheduledCheck checks for imbalances and schedules migrations if needed.
func (rs *RebalancingScheduler) performScheduledCheck() {
	rs.mu.Lock()
	if rs.activeMigrations >= rs.policy.MaxConcurrentMigrations {
		rs.mu.Unlock()
		return // Too many active migrations
	}
	rs.mu.Unlock()

	// Check if within maintenance window
	if !rs.isWithinMaintenanceWindow() {
		return
	}

	// Analyze shards
	analysis, err := rs.rebalancer.AnalyzeShard()
	if err != nil {
		return // Silently fail on analysis error
	}

	// Check if rebalancing needed
	if len(analysis.HotShards) == 0 && analysis.StandardDeviation < rs.policy.ImbalanceThreshold {
		return // System is balanced
	}

	// Schedule migrations for hot shards
	for _, hotShard := range analysis.HotShards {
		rs.scheduleMigrationForHotShard(hotShard, analysis)
	}

	// Update last check time
	now := time.Now()
	rs.mu.Lock()
	rs.lastCheckTime = &now
	rs.mu.Unlock()
}

// scheduleMigrationForHotShard schedules a migration to move messages from hot shard.
func (rs *RebalancingScheduler) scheduleMigrationForHotShard(hotShard ShardImbalance, analysis *RebalanceAnalysis) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	// Find coldest shard
	var coldestShard ShardImbalance
	if len(analysis.ColdShards) > 0 {
		coldestShard = analysis.ColdShards[0]
		for _, cs := range analysis.ColdShards {
			if cs.Percentage < coldestShard.Percentage {
				coldestShard = cs
			}
		}
	} else {
		// No cold shards, find least-filled shard
		stores := rs.rebalancer.registry.GetStores()
		minPercent := 100.0
		for shardID, store := range stores {
			stats, err := store.Stats()
			if err != nil {
				continue
			}
			percent := (float64(stats.TotalMessages) / float64(analysis.TotalMessages)) * 100
			if percent < minPercent && shardID != hotShard.ShardID {
				minPercent = percent
				coldestShard.ShardID = shardID
				coldestShard.Percentage = percent
			}
		}
	}

	// Create scheduled job
	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	job := &ScheduledMigrationJob{
		ID:            jobID,
		ScheduledTime: time.Now(),
		Status:        "scheduled",
		SourceShardID: hotShard.ShardID,
		DestShardID:   coldestShard.ShardID,
		MessageCount:  int(hotShard.MessageCount / 2), // Move 50% to start
	}

	rs.jobs[jobID] = job
	rs.activeMigrations++
}

// ExecuteScheduledJob executes a specific scheduled migration job.
func (rs *RebalancingScheduler) ExecuteScheduledJob(jobID string) error {
	rs.mu.RLock()
	job, ok := rs.jobs[jobID]
	if !ok {
		rs.mu.RUnlock()
		return fmt.Errorf("job not found: %s", jobID)
	}
	rs.mu.RUnlock()

	if job.Status != "scheduled" {
		return fmt.Errorf("job not in scheduled state: %s", job.Status)
	}

	// Start timing
	startTime := time.Now()

	// Prepare migration
	messageIDs := make([]string, job.MessageCount)
	for i := 0; i < job.MessageCount; i++ {
		messageIDs[i] = fmt.Sprintf("msg-%d", i)
	}

	txID, err := rs.executor.PrepareMigration(job.SourceShardID, job.DestShardID, messageIDs, "scheduler")
	if err != nil {
		rs.mu.Lock()
		job.Status = "failed"
		job.ErrorReason = err.Error()
		rs.totalJobsFailed++
		rs.activeMigrations--
		rs.mu.Unlock()
		return err
	}

	// Execute migration
	err = rs.executor.ExecuteMigration(txID)
	if err != nil {
		_ = rs.executor.RollbackMigration(txID)
		rs.mu.Lock()
		job.Status = "failed"
		job.ErrorReason = err.Error()
		rs.totalJobsFailed++
		rs.activeMigrations--
		rs.mu.Unlock()
		return err
	}

	// Commit migration
	err = rs.executor.CommitMigration(txID)
	if err != nil {
		_ = rs.executor.RollbackMigration(txID)
		rs.mu.Lock()
		job.Status = "failed"
		job.ErrorReason = err.Error()
		rs.totalJobsFailed++
		rs.activeMigrations--
		rs.mu.Unlock()
		return err
	}

	// Mark as completed
	now := time.Now()
	rs.mu.Lock()
	job.Status = "completed"
	job.ExecutedTime = &now
	job.Duration = now.Sub(startTime)
	rs.totalJobsExecuted++
	rs.activeMigrations--
	rs.mu.Unlock()

	return nil
}

// GetSchedulerStatus returns the current status of the scheduler.
func (rs *RebalancingScheduler) GetSchedulerStatus() *SchedulerStatus {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	var scheduled, executing, completed, failed, cancelled int
	for _, job := range rs.jobs {
		switch job.Status {
		case "scheduled":
			scheduled++
		case "executing":
			executing++
		case "completed":
			completed++
		case "failed":
			failed++
		case "cancelled":
			cancelled++
		}
	}

	return &SchedulerStatus{
		IsRunning:            rs.isRunning,
		CheckIntervalSeconds: rs.policy.CheckIntervalSeconds,
		ActiveMigrations:     rs.activeMigrations,
		ScheduledJobs:        scheduled,
		ExecutingJobs:        executing,
		CompletedJobs:        completed,
		FailedJobs:           failed,
		CancelledJobs:        cancelled,
		TotalJobsExecuted:    rs.totalJobsExecuted,
		TotalJobsFailed:      rs.totalJobsFailed,
		LastCheckTime:        rs.lastCheckTime,
	}
}

// SchedulerStatus provides current scheduler status.
type SchedulerStatus struct {
	IsRunning            bool
	CheckIntervalSeconds int
	ActiveMigrations     int
	ScheduledJobs        int
	ExecutingJobs        int
	CompletedJobs        int
	FailedJobs           int
	CancelledJobs        int
	TotalJobsExecuted    int64
	TotalJobsFailed      int64
	LastCheckTime        *time.Time
}

// GetScheduledJobs returns all scheduled migration jobs.
func (rs *RebalancingScheduler) GetScheduledJobs() []*ScheduledMigrationJob {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	jobs := make([]*ScheduledMigrationJob, 0, len(rs.jobs))
	for _, job := range rs.jobs {
		jobCopy := *job
		jobs = append(jobs, &jobCopy)
	}

	return jobs
}

// isWithinMaintenanceWindow checks if current time is within maintenance window.
func (rs *RebalancingScheduler) isWithinMaintenanceWindow() bool {
	if rs.policy.MaintenanceWindowStart == "" || rs.policy.MaintenanceWindowEnd == "" {
		return true // No window specified, always allow
	}

	now := time.Now().UTC()
	hour := now.Hour()
	minute := now.Minute()
	currentTime := hour*60 + minute

	// Parse window times
	startParts := parseTimeString(rs.policy.MaintenanceWindowStart)
	endParts := parseTimeString(rs.policy.MaintenanceWindowEnd)

	if len(startParts) < 2 || len(endParts) < 2 {
		return true // Invalid format, allow all
	}

	startTime := startParts[0]*60 + startParts[1]
	endTime := endParts[0]*60 + endParts[1]

	if startTime <= endTime {
		return currentTime >= startTime && currentTime < endTime
	} else {
		// Window wraps around midnight
		return currentTime >= startTime || currentTime < endTime
	}
}

// Helper to parse "HH:MM" format
func parseTimeString(s string) []int {
	parts := make([]int, 0, 2)
	// Simple parsing (production would use time.Parse)
	if len(s) >= 5 {
		// Try to extract hour and minute
		var hour, minute int
		n, _ := fmt.Sscanf(s, "%d:%d", &hour, &minute)
		if n == 2 {
			parts = append(parts, hour, minute)
		}
	}
	return parts
}
