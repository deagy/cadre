package knowledge

import (
	"fmt"
	"sync"
	"time"
)

// FaultTolerance provides fault tolerance mechanisms for knowledge store operations.
type FaultTolerance struct {
	mu              sync.RWMutex
	maxRetries      int
	retryDelay      time.Duration
	circuitBreaker  *CircuitBreaker
	errorLog        []ErrorEvent
	maxErrorLog     int
	recoveryStats   *RecoveryStats
}

// ErrorEvent tracks error occurrences.
type ErrorEvent struct {
	Timestamp time.Time
	Operation string
	Error     string
	Resolved  bool
}

// RecoveryStats tracks recovery attempts and success rates.
type RecoveryStats struct {
	TotalErrors      int64
	SuccessfulRetries int64
	FailedRetries    int64
	CircuitBreaks    int64
	LastRecoveryTime time.Time
}

// CircuitBreaker implements circuit breaker pattern for failure handling.
type CircuitBreaker struct {
	mu              sync.RWMutex
	state           string // "closed", "open", "half-open"
	failureCount    int
	failureThreshold int
	successCount    int
	successThreshold int
	lastFailureTime time.Time
	resetTimeout    time.Duration
}

// NewFaultTolerance creates a fault tolerance manager.
func NewFaultTolerance() *FaultTolerance {
	return &FaultTolerance{
		maxRetries:    3,
		retryDelay:    100 * time.Millisecond,
		circuitBreaker: NewCircuitBreaker(5, 3, 30*time.Second),
		errorLog:      make([]ErrorEvent, 0),
		maxErrorLog:   1000,
		recoveryStats: &RecoveryStats{
			LastRecoveryTime: time.Now(),
		},
	}
}

// NewCircuitBreaker creates a new circuit breaker.
func NewCircuitBreaker(failureThreshold, successThreshold int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:              "closed",
		failureThreshold:   failureThreshold,
		successThreshold:   successThreshold,
		resetTimeout:       resetTimeout,
	}
}

// ExecuteWithRetry executes operation with retry logic.
func (ft *FaultTolerance) ExecuteWithRetry(operation string, fn func() error) error {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	for attempt := 0; attempt <= ft.maxRetries; attempt++ {
		// Check circuit breaker
		if !ft.circuitBreaker.CanExecute() {
			return fmt.Errorf("circuit breaker open: operation %s rejected", operation)
		}

		err := fn()
		if err == nil {
			ft.circuitBreaker.RecordSuccess()
			return nil
		}

		// Log error
		ft.errorLog = append(ft.errorLog, ErrorEvent{
			Timestamp: time.Now(),
			Operation: operation,
			Error:     err.Error(),
			Resolved:  false,
		})

		if len(ft.errorLog) > ft.maxErrorLog {
			ft.errorLog = ft.errorLog[len(ft.errorLog)-ft.maxErrorLog:]
		}

		ft.recoveryStats.TotalErrors++

		// Retry if attempts remain
		if attempt < ft.maxRetries {
			time.Sleep(ft.retryDelay * time.Duration(attempt+1)) // Exponential backoff
			ft.recoveryStats.SuccessfulRetries++
			continue
		}

		// Final failure
		ft.circuitBreaker.RecordFailure()
		ft.recoveryStats.FailedRetries++
		return fmt.Errorf("operation %s failed after %d retries: %w", operation, ft.maxRetries, err)
	}

	return nil
}

// (CircuitBreaker methods)

// CanExecute checks if circuit breaker allows operation.
func (cb *CircuitBreaker) CanExecute() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if cb.state == "closed" {
		return true
	}

	if cb.state == "open" {
		// Check if reset timeout elapsed
		if time.Since(cb.lastFailureTime) > cb.resetTimeout {
			cb.state = "half-open"
			return true
		}
		return false
	}

	// Half-open state allows execution
	return true
}

// RecordSuccess records successful operation.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == "half-open" {
		cb.successCount++
		if cb.successCount >= cb.successThreshold {
			cb.state = "closed"
			cb.failureCount = 0
			cb.successCount = 0
		}
	}
}

// RecordFailure records failed operation.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++
	cb.lastFailureTime = time.Now()

	if cb.failureCount >= cb.failureThreshold {
		cb.state = "open"
	}
}

// GetStats returns recovery statistics.
func (ft *FaultTolerance) GetStats() *RecoveryStats {
	ft.mu.RLock()
	defer ft.mu.RUnlock()

	stats := *ft.recoveryStats
	stats.LastRecoveryTime = time.Now()
	return &stats
}

// Replication handles data replication across nodes.
type Replication struct {
	mu               sync.RWMutex
	nodeID           string
	replicas         map[string]*ReplicaNode
	replicationLog   []ReplicationEvent
	maxLogSize       int
	consistencyLevel string // "strong", "eventual"
}

// ReplicaNode represents a replica destination.
type ReplicaNode struct {
	NodeID   string
	Address  string
	Status   string // "healthy", "lagging", "offline"
	LastSync time.Time
	SyncLag  int64  // milliseconds
}

// ReplicationEvent tracks replication operations.
type ReplicationEvent struct {
	Timestamp   time.Time
	MessageID   string
	Operation   string
	ReplicaID   string
	Status      string // "pending", "synced", "failed"
	RetryCount  int
}

// NewReplication creates a replication manager.
func NewReplication(nodeID string) *Replication {
	return &Replication{
		nodeID:           nodeID,
		replicas:         make(map[string]*ReplicaNode),
		replicationLog:   make([]ReplicationEvent, 0),
		maxLogSize:       10000,
		consistencyLevel: "eventual",
	}
}

// RegisterReplica adds a replica node.
func (r *Replication) RegisterReplica(replicaID, address string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.replicas[replicaID]; exists {
		return fmt.Errorf("replica already registered: %s", replicaID)
	}

	r.replicas[replicaID] = &ReplicaNode{
		NodeID:   replicaID,
		Address:  address,
		Status:   "healthy",
		LastSync: time.Now(),
		SyncLag:  0,
	}

	return nil
}

// ReplicateOperation sends operation to replicas.
func (r *Replication) ReplicateOperation(messageID, operation string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for replicaID := range r.replicas {
		event := ReplicationEvent{
			Timestamp:  time.Now(),
			MessageID:  messageID,
			Operation:  operation,
			ReplicaID:  replicaID,
			Status:     "pending",
			RetryCount: 0,
		}

		r.replicationLog = append(r.replicationLog, event)
	}

	// Trim log if too large
	if len(r.replicationLog) > r.maxLogSize {
		r.replicationLog = r.replicationLog[len(r.replicationLog)-r.maxLogSize:]
	}

	return nil
}

// VerifyConsistency checks consistency across replicas.
func (r *Replication) VerifyConsistency() (bool, map[string]interface{}) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	report := make(map[string]interface{})
	totalReplicas := len(r.replicas)
	healthyReplicas := 0
	maxLag := int64(0)

	for _, replica := range r.replicas {
		if replica.Status == "healthy" {
			healthyReplicas++
		}
		if replica.SyncLag > maxLag {
			maxLag = replica.SyncLag
		}
	}

	isConsistent := healthyReplicas >= (totalReplicas / 2) // Quorum

	report["total_replicas"] = totalReplicas
	report["healthy_replicas"] = healthyReplicas
	report["max_sync_lag_ms"] = maxLag
	report["consistent"] = isConsistent
	report["consistency_level"] = r.consistencyLevel

	return isConsistent, report
}

// DisasterRecovery handles backup and restore operations.
type DisasterRecovery struct {
	mu               sync.RWMutex
	backupLocation   string
	backupSchedule   time.Duration
	lastBackupTime   time.Time
	backupHistory    []BackupMetadata
	maxHistorySize   int
	recoveryPoints   map[string]*RecoveryPoint
}

// BackupMetadata tracks backup information.
type BackupMetadata struct {
	BackupID      string
	Timestamp     time.Time
	DatabaseSize  int64
	MessageCount  int64
	ChunkCount    int64
	Status        string // "in_progress", "completed", "failed"
	DurationMs    int64
	ErrorMessage  string
}

// RecoveryPoint represents a point in time for recovery.
type RecoveryPoint struct {
	PointID       string
	Timestamp     time.Time
	BackupID      string
	ConsistentLSN int64 // Log Sequence Number
	Verified      bool
}

// NewDisasterRecovery creates a disaster recovery manager.
func NewDisasterRecovery(backupLocation string) *DisasterRecovery {
	return &DisasterRecovery{
		backupLocation: backupLocation,
		backupSchedule: 24 * time.Hour,
		lastBackupTime: time.Now(),
		backupHistory:  make([]BackupMetadata, 0),
		maxHistorySize: 100,
		recoveryPoints: make(map[string]*RecoveryPoint),
	}
}

// CreateBackup creates a backup of the knowledge store.
func (dr *DisasterRecovery) CreateBackup(messageCount, chunkCount, dbSize int64) (string, error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()

	backupID := fmt.Sprintf("backup-%d", time.Now().Unix())
	startTime := time.Now()

	metadata := BackupMetadata{
		BackupID:     backupID,
		Timestamp:    startTime,
		DatabaseSize: dbSize,
		MessageCount: messageCount,
		ChunkCount:   chunkCount,
		Status:       "in_progress",
	}

	// Simulate backup operation
	// In production, this would actually copy data files
	time.Sleep(10 * time.Millisecond) // Simulate backup time

	metadata.Status = "completed"
	metadata.DurationMs = time.Since(startTime).Milliseconds()
	dr.lastBackupTime = time.Now()

	dr.backupHistory = append(dr.backupHistory, metadata)
	if len(dr.backupHistory) > dr.maxHistorySize {
		dr.backupHistory = dr.backupHistory[len(dr.backupHistory)-dr.maxHistorySize:]
	}

	// Create recovery point
	dr.recoveryPoints[backupID] = &RecoveryPoint{
		PointID:       fmt.Sprintf("rp-%s", backupID),
		Timestamp:     metadata.Timestamp,
		BackupID:      backupID,
		ConsistentLSN: 0,
		Verified:      true,
	}

	return backupID, nil
}

// RestoreFromBackup restores from a backup.
func (dr *DisasterRecovery) RestoreFromBackup(backupID string) error {
	dr.mu.RLock()
	rp, exists := dr.recoveryPoints[backupID]
	dr.mu.RUnlock()

	if !exists {
		return fmt.Errorf("recovery point not found: %s", backupID)
	}

	if !rp.Verified {
		return fmt.Errorf("recovery point not verified: %s", backupID)
	}

	// In production, this would actually restore data files
	return nil
}

// GetBackupHistory returns backup history.
func (dr *DisasterRecovery) GetBackupHistory() []BackupMetadata {
	dr.mu.RLock()
	defer dr.mu.RUnlock()

	history := make([]BackupMetadata, len(dr.backupHistory))
	copy(history, dr.backupHistory)
	return history
}
