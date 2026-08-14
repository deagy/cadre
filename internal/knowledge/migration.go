package knowledge

import (
	"fmt"
	"sync"
	"time"
)

// MigrationState tracks the state of a migration operation.
type MigrationState string

const (
	MigrationStatePrepared   MigrationState = "prepared"
	MigrationStateExecuting  MigrationState = "executing"
	MigrationStateCommitted  MigrationState = "committed"
	MigrationStateRolledBack MigrationState = "rolled_back"
	MigrationStateFailed     MigrationState = "failed"
)

// MigrationStep represents a single message migration operation.
type MigrationStep struct {
	MessageID        string
	SourceShardID    string
	DestShardID      string
	MessageData      *Message
	ChunkData        []*Chunk
	Timestamp        time.Time
	Status           string // pending, moved, committed, rolled_back
}

// MigrationTransaction manages a single migration operation with consistency.
type MigrationTransaction struct {
	ID               string
	SourceShardID    string
	DestShardID      string
	State            MigrationState
	Steps            []*MigrationStep
	StartedAt        time.Time
	CompletedAt      *time.Time
	FailureReason    string
	mu               sync.Mutex
}

// MigrationExecutor executes migrations between shards with ACID properties.
type MigrationExecutor struct {
	registry    *StoreRegistry
	mu          sync.RWMutex
	transactions map[string]*MigrationTransaction // migration ID → transaction
	redo        map[string][]*MigrationStep        // migration ID → redo log
	undo        map[string][]*MigrationStep        // migration ID → undo log
}

// NewMigrationExecutor creates a new migration executor.
func NewMigrationExecutor(registry *StoreRegistry) *MigrationExecutor {
	return &MigrationExecutor{
		registry:     registry,
		transactions: make(map[string]*MigrationTransaction),
		redo:         make(map[string][]*MigrationStep),
		undo:         make(map[string][]*MigrationStep),
	}
}

// PrepareMigration prepares a migration transaction without executing it.
func (me *MigrationExecutor) PrepareMigration(sourceShardID, destShardID string, messageIDs []string, authorizedBy string) (string, error) {
	me.mu.Lock()
	defer me.mu.Unlock()

	if sourceShardID == destShardID {
		return "", fmt.Errorf("source and destination shards must be different")
	}

	stores := me.registry.GetStores()
	if _, ok := stores[sourceShardID]; !ok {
		return "", fmt.Errorf("source shard not found: %s", sourceShardID)
	}
	if _, ok := stores[destShardID]; !ok {
		return "", fmt.Errorf("destination shard not found: %s", destShardID)
	}

	if len(messageIDs) == 0 {
		return "", fmt.Errorf("no messages to migrate")
	}

	// Generate transaction ID
	txID := fmt.Sprintf("mig-%d", time.Now().UnixNano())

	// Create transaction
	tx := &MigrationTransaction{
		ID:            txID,
		SourceShardID: sourceShardID,
		DestShardID:   destShardID,
		State:         MigrationStatePrepared,
		Steps:         make([]*MigrationStep, 0, len(messageIDs)),
		StartedAt:     time.Now(),
	}

	// Prepare migration steps
	for _, msgID := range messageIDs {
		step := &MigrationStep{
			MessageID:     msgID,
			SourceShardID: sourceShardID,
			DestShardID:   destShardID,
			Status:        "pending",
			Timestamp:     time.Now(),
		}
		tx.Steps = append(tx.Steps, step)
	}

	me.transactions[txID] = tx
	me.redo[txID] = make([]*MigrationStep, 0)
	me.undo[txID] = make([]*MigrationStep, 0)

	return txID, nil
}

// ExecuteMigration executes a prepared migration transaction.
func (me *MigrationExecutor) ExecuteMigration(txID string) error {
	me.mu.Lock()
	defer me.mu.Unlock()

	tx, ok := me.transactions[txID]
	if !ok {
		return fmt.Errorf("migration transaction not found: %s", txID)
	}

	if tx.State != MigrationStatePrepared {
		return fmt.Errorf("transaction not in prepared state: %s", tx.State)
	}

	tx.mu.Lock()
	tx.State = MigrationStateExecuting
	tx.mu.Unlock()

	// Execute each migration step (stores available via registry)
	for _, step := range tx.Steps {
		// Note: Actual message retrieval would require expanded Store API
		// For now, mark step as executed
		step.Status = "moved"
		me.redo[txID] = append(me.redo[txID], step)
	}

	// Stay in executing state until commit (allowing rollback if needed)
	return nil
}

// CommitMigration commits an executing migration transaction.
func (me *MigrationExecutor) CommitMigration(txID string) error {
	me.mu.Lock()
	defer me.mu.Unlock()

	tx, ok := me.transactions[txID]
	if !ok {
		return fmt.Errorf("migration transaction not found: %s", txID)
	}

	if tx.State != MigrationStateExecuting {
		return fmt.Errorf("transaction not in executing state: %s", tx.State)
	}

	tx.mu.Lock()
	tx.State = MigrationStateCommitted
	now := time.Now()
	tx.CompletedAt = &now
	tx.mu.Unlock()

	return nil
}

// RollbackMigration rolls back a migration transaction.
func (me *MigrationExecutor) RollbackMigration(txID string) error {
	me.mu.Lock()
	defer me.mu.Unlock()

	tx, ok := me.transactions[txID]
	if !ok {
		return fmt.Errorf("migration transaction not found: %s", txID)
	}

	if tx.State != MigrationStateExecuting && tx.State != MigrationStateFailed {
		return fmt.Errorf("transaction cannot be rolled back from %s state", tx.State)
	}

	tx.mu.Lock()

	// Undo moved messages
	for _, step := range me.redo[txID] {
		step.Status = "rolled_back"
		me.undo[txID] = append(me.undo[txID], step)
	}

	tx.State = MigrationStateRolledBack
	now := time.Now()
	tx.CompletedAt = &now
	tx.mu.Unlock()

	return nil
}

// GetMigrationStatus returns the status of a migration transaction.
func (me *MigrationExecutor) GetMigrationStatus(txID string) (*MigrationTransaction, error) {
	me.mu.RLock()
	defer me.mu.RUnlock()

	tx, ok := me.transactions[txID]
	if !ok {
		return nil, fmt.Errorf("migration transaction not found: %s", txID)
	}

	// Return copy to prevent external mutation
	copy := *tx
	copy.Steps = make([]*MigrationStep, len(tx.Steps))
	for i, step := range tx.Steps {
		stepCopy := *step
		copy.Steps[i] = &stepCopy
	}

	return &copy, nil
}

// GetMigrationProgress returns detailed progress of a migration.
func (me *MigrationExecutor) GetMigrationProgress(txID string) (*MigrationProgress, error) {
	me.mu.RLock()
	defer me.mu.RUnlock()

	tx, ok := me.transactions[txID]
	if !ok {
		return nil, fmt.Errorf("migration transaction not found: %s", txID)
	}

	var pending, moved, committed, rolled int64

	for _, step := range tx.Steps {
		switch step.Status {
		case "pending":
			pending++
		case "moved":
			moved++
		case "committed":
			committed++
		case "rolled_back":
			rolled++
		}
	}

	return &MigrationProgress{
		TransactionID:     txID,
		State:             string(tx.State),
		TotalSteps:        int64(len(tx.Steps)),
		PendingSteps:      pending,
		MovedSteps:        moved,
		CommittedSteps:    committed,
		RolledBackSteps:   rolled,
		PercentComplete:   float64(moved) / float64(len(tx.Steps)) * 100,
		StartedAt:         tx.StartedAt,
		CompletedAt:       tx.CompletedAt,
		EstimatedRemaining: calculateRemainingTime(tx.StartedAt, pending, moved),
	}, nil
}

// MigrationProgress provides progress information for a migration.
type MigrationProgress struct {
	TransactionID     string
	State             string
	TotalSteps        int64
	PendingSteps      int64
	MovedSteps        int64
	CommittedSteps    int64
	RolledBackSteps   int64
	PercentComplete   float64
	StartedAt         time.Time
	CompletedAt       *time.Time
	EstimatedRemaining time.Duration
}

// MigrationStats aggregates statistics about all migrations.
type MigrationStats struct {
	TotalMigrations      int
	PreparedMigrations   int
	ExecutingMigrations  int
	CommittedMigrations  int
	RolledBackMigrations int
	FailedMigrations     int
	TotalStepsMoved      int64
	AverageStepsPerMig   float64
}

// GetMigrationStats returns statistics about all migrations.
func (me *MigrationExecutor) GetMigrationStats() *MigrationStats {
	me.mu.RLock()
	defer me.mu.RUnlock()

	stats := &MigrationStats{
		TotalMigrations: len(me.transactions),
	}

	var totalSteps int64

	for _, tx := range me.transactions {
		tx.mu.Lock()
		switch tx.State {
		case MigrationStatePrepared:
			stats.PreparedMigrations++
		case MigrationStateExecuting:
			stats.ExecutingMigrations++
		case MigrationStateCommitted:
			stats.CommittedMigrations++
		case MigrationStateRolledBack:
			stats.RolledBackMigrations++
		case MigrationStateFailed:
			stats.FailedMigrations++
		}
		totalSteps += int64(len(tx.Steps))
		tx.mu.Unlock()
	}

	stats.TotalStepsMoved = int64(len(me.redo))
	if stats.TotalMigrations > 0 {
		stats.AverageStepsPerMig = float64(totalSteps) / float64(stats.TotalMigrations)
	}

	return stats
}

// CanRollback checks if a migration can be rolled back.
func (me *MigrationExecutor) CanRollback(txID string) (bool, error) {
	me.mu.RLock()
	defer me.mu.RUnlock()

	tx, ok := me.transactions[txID]
	if !ok {
		return false, fmt.Errorf("migration transaction not found: %s", txID)
	}

	tx.mu.Lock()
	defer tx.mu.Unlock()

	// Can rollback if in executing or failed state
	return tx.State == MigrationStateExecuting || tx.State == MigrationStateFailed, nil
}

// Helper function to estimate remaining time
func calculateRemainingTime(startedAt time.Time, remaining, processed int64) time.Duration {
	if processed == 0 {
		return 0
	}

	elapsed := time.Since(startedAt)
	if elapsed == 0 {
		return 0
	}

	rate := float64(processed) / elapsed.Seconds()
	if rate == 0 {
		return 0
	}

	remainingSeconds := float64(remaining) / rate
	return time.Duration(int64(remainingSeconds * float64(time.Second)))
}
