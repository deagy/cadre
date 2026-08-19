package knowledge

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// (CircuitBreaker methods)

// DisasterRecovery handles backup and restore operations.
type DisasterRecovery struct {
	mu             sync.RWMutex
	backupLocation string
	backupSchedule time.Duration
	lastBackupTime time.Time
	backupHistory  []BackupMetadata
	maxHistorySize int
	recoveryPoints map[string]*RecoveryPoint
}

// BackupMetadata tracks backup information.
type BackupMetadata struct {
	BackupID     string
	Timestamp    time.Time
	DatabaseSize int64
	MessageCount int64
	ChunkCount   int64
	Status       string // "in_progress", "completed", "failed"
	DurationMs   int64
	ErrorMessage string
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

// ErrNotImplemented reports a capability this package declares but does not
// have. Callers should surface it; nothing should treat it as a soft failure.
//
// It exists because the alternative was worse than a missing feature.
// CreateBackup slept 10ms, copied nothing, recorded the counts its caller
// passed it, and returned status "completed"; RestoreFromBackup returned nil
// under a comment saying production "would actually restore data files"; and
// the recovery point CreateBackup wrote set Verified: true, so a later verify
// agreed with both. An operator who took a backup before an incident and
// restored after one was told, twice, that it had worked.
//
// Refusing is the fail-closed behaviour this repository applies everywhere
// else: asserting a result that nothing computed is a worse claim than
// admitting the capability is absent.
var ErrNotImplemented = errors.New("not implemented")

// CreateBackup is not implemented and refuses rather than reporting success.
//
// The parameters are retained so the signature still describes what a real
// implementation needs; they are deliberately unused.
func (dr *DisasterRecovery) CreateBackup(messageCount, chunkCount, dbSize int64) (string, error) {
	return "", fmt.Errorf("%w: no data is copied and no backup is retained. "+
		"Copy the store's database file directly instead", ErrNotImplemented)
}

// RestoreFromBackup is not implemented and refuses rather than reporting
// success. Returning nil here told an operator recovering from data loss that
// their restore had completed.
func (dr *DisasterRecovery) RestoreFromBackup(backupID string) error {
	return fmt.Errorf("%w: no data is restored. Restore the store's database "+
		"file from your own copy instead", ErrNotImplemented)
}

// GetBackupHistory returns backup history.
func (dr *DisasterRecovery) GetBackupHistory() []BackupMetadata {
	dr.mu.RLock()
	defer dr.mu.RUnlock()

	history := make([]BackupMetadata, len(dr.backupHistory))
	copy(history, dr.backupHistory)
	return history
}
