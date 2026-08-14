package knowledge

import (
	"fmt"
	"testing"
	"time"
)

func TestFaultToleranceCreation(t *testing.T) {
	ft := NewFaultTolerance()
	if ft == nil {
		t.Error("Failed to create fault tolerance manager")
	}

	if ft.maxRetries != 3 {
		t.Errorf("Expected 3 retries, got %d", ft.maxRetries)
	}

	if ft.circuitBreaker == nil {
		t.Error("Circuit breaker should be initialized")
	}
}

func TestExecuteWithRetrySuccess(t *testing.T) {
	ft := NewFaultTolerance()

	callCount := 0
	err := ft.ExecuteWithRetry("test-op", func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Errorf("Expected success, got error: %v", err)
	}

	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}
}

func TestExecuteWithRetryFail(t *testing.T) {
	ft := NewFaultTolerance()

	callCount := 0
	err := ft.ExecuteWithRetry("test-op", func() error {
		callCount++
		return fmt.Errorf("test error")
	})

	if err == nil {
		t.Error("Expected error, got nil")
	}

	// Should retry 3 times plus initial attempt = 4 total
	if callCount != 4 {
		t.Errorf("Expected 4 calls (1 initial + 3 retries), got %d", callCount)
	}
}

func TestExecuteWithRetryRecovery(t *testing.T) {
	ft := NewFaultTolerance()

	callCount := 0
	err := ft.ExecuteWithRetry("test-op", func() error {
		callCount++
		if callCount < 2 {
			return fmt.Errorf("temporary failure")
		}
		return nil
	})

	if err != nil {
		t.Errorf("Expected success after retry, got error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("Expected 2 calls, got %d", callCount)
	}
}

func TestCircuitBreakerClosed(t *testing.T) {
	cb := NewCircuitBreaker(3, 2, 1*time.Second)

	if !cb.CanExecute() {
		t.Error("Circuit breaker should allow execution in closed state")
	}

	cb.RecordSuccess()
	if !cb.CanExecute() {
		t.Error("Circuit breaker should still allow execution after success")
	}
}

func TestCircuitBreakerOpen(t *testing.T) {
	cb := NewCircuitBreaker(3, 2, 1*time.Second)

	// Record 3 failures to open circuit
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	if cb.CanExecute() {
		t.Error("Circuit breaker should reject execution when open")
	}
}

func TestCircuitBreakerHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, 1, 100*time.Millisecond)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	if !cb.CanExecute() {
		t.Error("Circuit breaker should allow execution in half-open state")
	}

	// Record success to close
	cb.RecordSuccess()
	if !cb.CanExecute() {
		t.Error("Circuit breaker should close after successful recovery")
	}
}

func TestRecoveryStats(t *testing.T) {
	ft := NewFaultTolerance()

	// Generate some errors (will retry 3 times, so 4 total error events)
	ft.ExecuteWithRetry("test-op", func() error {
		return fmt.Errorf("test error")
	})

	stats := ft.GetStats()

	// 1 initial attempt + 3 retries = 4 error events
	if stats.TotalErrors != 4 {
		t.Errorf("Expected 4 error events (1 initial + 3 retries), got %d", stats.TotalErrors)
	}

	if stats.FailedRetries == 0 {
		t.Error("Expected failed retry count to be > 0")
	}
}

func TestReplicationCreation(t *testing.T) {
	rep := NewReplication("primary-node")
	if rep == nil {
		t.Error("Failed to create replication manager")
	}

	if rep.nodeID != "primary-node" {
		t.Errorf("Expected node ID 'primary-node', got %s", rep.nodeID)
	}
}

func TestReplicationRegisterReplica(t *testing.T) {
	rep := NewReplication("primary")

	err := rep.RegisterReplica("replica-1", "10.0.0.1:8080")
	if err != nil {
		t.Fatalf("Failed to register replica: %v", err)
	}

	if len(rep.replicas) != 1 {
		t.Errorf("Expected 1 replica, got %d", len(rep.replicas))
	}
}

func TestRegisterDuplicateReplica(t *testing.T) {
	rep := NewReplication("primary")

	rep.RegisterReplica("replica-1", "10.0.0.1:8080")
	err := rep.RegisterReplica("replica-1", "10.0.0.2:8080")

	if err == nil {
		t.Error("Expected error when registering duplicate replica")
	}
}

func TestReplicateOperation(t *testing.T) {
	rep := NewReplication("primary")

	rep.RegisterReplica("replica-1", "10.0.0.1:8080")
	rep.RegisterReplica("replica-2", "10.0.0.2:8080")

	err := rep.ReplicateOperation("msg-123", "delete")
	if err != nil {
		t.Fatalf("Failed to replicate: %v", err)
	}

	if len(rep.replicationLog) < 2 {
		t.Errorf("Expected at least 2 replication events, got %d", len(rep.replicationLog))
	}
}

func TestVerifyConsistency(t *testing.T) {
	rep := NewReplication("primary")

	rep.RegisterReplica("replica-1", "10.0.0.1:8080")
	rep.RegisterReplica("replica-2", "10.0.0.2:8080")
	rep.RegisterReplica("replica-3", "10.0.0.3:8080")

	// Mark one replica as lagging
	rep.mu.Lock()
	rep.replicas["replica-3"].Status = "lagging"
	rep.mu.Unlock()

	isConsistent, report := rep.VerifyConsistency()

	if !isConsistent {
		t.Error("Should be consistent with 2/3 healthy replicas")
	}

	if report["healthy_replicas"] != 2 {
		t.Errorf("Expected 2 healthy replicas, got %v", report["healthy_replicas"])
	}
}

func TestDisasterRecoveryCreation(t *testing.T) {
	dr := NewDisasterRecovery("/backups")
	if dr == nil {
		t.Error("Failed to create disaster recovery manager")
	}

	if dr.backupLocation != "/backups" {
		t.Errorf("Expected backup location '/backups', got %s", dr.backupLocation)
	}
}

func TestCreateBackup(t *testing.T) {
	dr := NewDisasterRecovery("/backups")

	backupID, err := dr.CreateBackup(1000, 500, 1024*1024)
	if err != nil {
		t.Fatalf("Failed to create backup: %v", err)
	}

	if backupID == "" {
		t.Error("Backup ID should not be empty")
	}

	if len(dr.backupHistory) != 1 {
		t.Errorf("Expected 1 backup in history, got %d", len(dr.backupHistory))
	}

	backup := dr.backupHistory[0]
	if backup.MessageCount != 1000 {
		t.Errorf("Expected 1000 messages, got %d", backup.MessageCount)
	}

	if backup.Status != "completed" {
		t.Errorf("Expected status 'completed', got %s", backup.Status)
	}
}

func TestRestoreFromBackup(t *testing.T) {
	dr := NewDisasterRecovery("/backups")

	backupID, _ := dr.CreateBackup(1000, 500, 1024*1024)

	err := dr.RestoreFromBackup(backupID)
	if err != nil {
		t.Fatalf("Failed to restore: %v", err)
	}
}

func TestRestoreFromInvalidBackup(t *testing.T) {
	dr := NewDisasterRecovery("/backups")

	err := dr.RestoreFromBackup("invalid-backup")
	if err == nil {
		t.Error("Expected error when restoring invalid backup")
	}
}

func TestBackupHistory(t *testing.T) {
	dr := NewDisasterRecovery("/backups")

	for i := 0; i < 3; i++ {
		dr.CreateBackup(int64(1000+i*100), 500, 1024*1024)
	}

	history := dr.GetBackupHistory()
	if len(history) != 3 {
		t.Errorf("Expected 3 backups in history, got %d", len(history))
	}
}

func TestRecoveryPointVerification(t *testing.T) {
	dr := NewDisasterRecovery("/backups")

	backupID, _ := dr.CreateBackup(1000, 500, 1024*1024)

	dr.mu.RLock()
	rp, exists := dr.recoveryPoints[backupID]
	dr.mu.RUnlock()

	if !exists {
		t.Error("Recovery point should exist")
	}

	if !rp.Verified {
		t.Error("Recovery point should be verified")
	}
}

func TestFaultToleranceIntegration(t *testing.T) {
	ft := NewFaultTolerance()
	dr := NewDisasterRecovery("/backups")

	// Simulate operation with retry and backup
	backupID, _ := dr.CreateBackup(1000, 500, 1024*1024)

	err := ft.ExecuteWithRetry("restore-op", func() error {
		return dr.RestoreFromBackup(backupID)
	})

	if err != nil {
		t.Errorf("Expected successful restore operation: %v", err)
	}

	stats := ft.GetStats()
	if stats.TotalErrors != 0 {
		t.Errorf("Expected 0 errors, got %d", stats.TotalErrors)
	}
}

func TestReplicationWithFaultTolerance(t *testing.T) {
	ft := NewFaultTolerance()
	rep := NewReplication("primary")

	rep.RegisterReplica("replica-1", "10.0.0.1:8080")
	rep.RegisterReplica("replica-2", "10.0.0.2:8080")

	err := ft.ExecuteWithRetry("replicate-op", func() error {
		return rep.ReplicateOperation("msg-123", "delete")
	})

	if err != nil {
		t.Errorf("Expected successful replication: %v", err)
	}

	isConsistent, _ := rep.VerifyConsistency()
	if !isConsistent {
		t.Error("Replication should maintain consistency")
	}
}

func TestDisasterRecoveryWithReplication(t *testing.T) {
	dr := NewDisasterRecovery("/backups")
	rep := NewReplication("primary")

	rep.RegisterReplica("replica-1", "10.0.0.1:8080")

	// Create backup and verify recovery point
	backupID, _ := dr.CreateBackup(1000, 500, 1024*1024)

	// Verify replication
	rep.ReplicateOperation("msg-123", "delete")
	isConsistent, report := rep.VerifyConsistency()

	if !isConsistent {
		t.Error("System should remain consistent")
	}

	if report["consistency_level"] != "eventual" {
		t.Errorf("Expected eventual consistency, got %v", report["consistency_level"])
	}

	// Restore from backup
	err := dr.RestoreFromBackup(backupID)
	if err != nil {
		t.Errorf("Should successfully restore: %v", err)
	}
}

func TestProductionEndToEnd(t *testing.T) {
	// Create all components
	ft := NewFaultTolerance()
	rep := NewReplication("primary")
	dr := NewDisasterRecovery("/backups")

	// Setup replication
	rep.RegisterReplica("replica-1", "10.0.0.1:8080")
	rep.RegisterReplica("replica-2", "10.0.0.2:8080")

	// Execute operations with fault tolerance
	ft.ExecuteWithRetry("setup-op", func() error {
		rep.ReplicateOperation("msg-001", "insert")
		return nil
	})

	// Verify consistency
	consistent, _ := rep.VerifyConsistency()
	if !consistent {
		t.Error("System should maintain consistency")
	}

	// Create backup
	backupID, _ := dr.CreateBackup(1000, 500, 1024*1024)

	// Simulate failure and recovery
	ft.ExecuteWithRetry("recovery-op", func() error {
		return dr.RestoreFromBackup(backupID)
	})

	// Final consistency check
	stats := ft.GetStats()
	if stats.TotalErrors > 0 {
		t.Errorf("System should have 0 errors in production scenario, got %d", stats.TotalErrors)
	}
}
