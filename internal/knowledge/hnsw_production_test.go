package knowledge

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// Backup and restore refuse, and nothing is recorded as though they had run.
//
// This replaces four tests that asserted the simulation was intact:
// TestCreateBackup checked for a backup id and a history entry reading
// "completed" with 1000 messages; TestRestoreFromBackup checked that restore
// returned nil, which it did by doing nothing at all; TestBackupHistory
// checked that three backups that never happened were all remembered; and
// TestRecoveryPointVerification checked rp.Verified, which was assigned true
// unconditionally. Together they made the fabrication look deliberate and
// protected it from removal -- a passing suite is not evidence of a working
// feature if the suite asserts the placeholder.
func TestBackupAndRestoreRefuseRatherThanReportSuccess(t *testing.T) {
	dr := NewDisasterRecovery("/backups")

	backupID, err := dr.CreateBackup(1000, 500, 1024*1024)
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("CreateBackup error = %v, want ErrNotImplemented", err)
	}
	if backupID != "" {
		t.Errorf("CreateBackup returned id %q; a refused backup must not be identified", backupID)
	}
	if err := dr.RestoreFromBackup("backup-anything"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("RestoreFromBackup error = %v, want ErrNotImplemented", err)
	}

	// Nothing may be recorded as a backup. History that remembers a refused
	// backup is what let `backup create` be believed in the first place.
	if history := dr.GetBackupHistory(); len(history) != 0 {
		t.Errorf("backup history has %d entries after a refused backup, want 0", len(history))
	}
	dr.mu.RLock()
	points := len(dr.recoveryPoints)
	dr.mu.RUnlock()
	if points != 0 {
		t.Errorf("%d recovery points exist after a refused backup, want 0", points)
	}
}
func TestFaultToleranceCreation(t *testing.T) {
	ft := NewFaultTolerance()
	if ft == nil {
		t.Fatal("Failed to create fault tolerance manager")
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
		t.Fatal("Failed to create replication manager")
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
		t.Fatal("Failed to create disaster recovery manager")
	}

	if dr.backupLocation != "/backups" {
		t.Errorf("Expected backup location '/backups', got %s", dr.backupLocation)
	}
}

func TestRestoreFromInvalidBackup(t *testing.T) {
	dr := NewDisasterRecovery("/backups")

	err := dr.RestoreFromBackup("invalid-backup")
	if err == nil {
		t.Error("Expected error when restoring invalid backup")
	}
}

func TestFaultToleranceIntegration(t *testing.T) {
	ft := NewFaultTolerance()
	dr := NewDisasterRecovery("/backups")

	// Backup and restore are refused; asserted as such rather than used to
	// manufacture a successful retry. ExecuteWithRetry itself is covered by
	// the retry tests above.
	assertBackupPathRefuses(t, dr)

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

	// Backup and restore are refused, so the recovery leg is asserted as a
	// refusal instead of driven as a working one. Retry, circuit breaking and
	// replication above are real and still covered.
	assertBackupPathRefuses(t, dr)

	// Verify replication
	rep.ReplicateOperation("msg-123", "delete")
	isConsistent, report := rep.VerifyConsistency()

	if !isConsistent {
		t.Error("System should remain consistent")
	}

	if report["consistency_level"] != "eventual" {
		t.Errorf("Expected eventual consistency, got %v", report["consistency_level"])
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

	// Backup and restore are refused, so the recovery leg is asserted as a
	// refusal instead of driven as a working one. Retry, circuit breaking and
	// replication above are real and still covered.
	assertBackupPathRefuses(t, dr)

	// Final consistency check
	stats := ft.GetStats()
	if stats.TotalErrors > 0 {
		t.Errorf("System should have 0 errors in production scenario, got %d", stats.TotalErrors)
	}
}

// assertBackupPathRefuses checks that both halves of disaster recovery refuse.
//
// Shared by the integration tests, which used to drive CreateBackup and
// RestoreFromBackup as working steps and assert zero errors -- an end-to-end
// test over a simulation, which is the most reassuring kind of test to have
// and the least informative.
func assertBackupPathRefuses(t *testing.T, dr *DisasterRecovery) {
	t.Helper()
	if _, err := dr.CreateBackup(1000, 500, 1024*1024); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("CreateBackup error = %v, want ErrNotImplemented", err)
	}
	if err := dr.RestoreFromBackup("backup-anything"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("RestoreFromBackup error = %v, want ErrNotImplemented", err)
	}
}
