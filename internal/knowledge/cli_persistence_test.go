//go:build cgo
// +build cgo

package knowledge

import (
	"path/filepath"
	"testing"
)

func skipIfNoCGO(t *testing.T) {
	// SQLite requires CGO, so skip tests if not available
	// This is detected by trying to create a persistence instance
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "check.db")
	persist, err := NewCLIPersistence(dbPath)
	if err != nil {
		t.Skipf("SQLite/CGO not available: %v", err)
	}
	persist.Close()
}

func TestCLIPersistenceCreation(t *testing.T) {
	skipIfNoCGO(t)
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, err := NewCLIPersistence(dbPath)
	if err != nil {
		t.Fatalf("Failed to create persistence: %v", err)
	}
	defer persist.Close()

	if persist == nil {
		t.Error("Persistence should not be nil")
	}
}

func TestRegisterReplica(t *testing.T) {
	skipIfNoCGO(t)
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, _ := NewCLIPersistence(dbPath)
	defer persist.Close()

	err := persist.RegisterReplica("replica-1", "10.0.0.1:8080")
	if err != nil {
		t.Fatalf("Failed to register replica: %v", err)
	}

	replicas, _ := persist.GetReplicas()
	if len(replicas) != 1 {
		t.Errorf("Expected 1 replica, got %d", len(replicas))
	}

	if replicas[0]["replica_id"] != "replica-1" {
		t.Errorf("Expected replica_id 'replica-1', got %v", replicas[0]["replica_id"])
	}
}

func TestGetReplicationStatus(t *testing.T) {
	skipIfNoCGO(t)
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, _ := NewCLIPersistence(dbPath)
	defer persist.Close()

	// Register replicas
	persist.RegisterReplica("replica-1", "10.0.0.1:8080")
	persist.RegisterReplica("replica-2", "10.0.0.2:8080")

	status, _ := persist.GetReplicationStatus()

	if status["total_replicas"] != 2 {
		t.Errorf("Expected 2 replicas, got %v", status["total_replicas"])
	}

	if status["healthy_replicas"] != 2 {
		t.Errorf("Expected 2 healthy replicas, got %v", status["healthy_replicas"])
	}
}

func TestRecordReplication(t *testing.T) {
	skipIfNoCGO(t)
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, _ := NewCLIPersistence(dbPath)
	defer persist.Close()

	persist.RegisterReplica("replica-1", "10.0.0.1:8080")

	err := persist.RecordReplication("replica-1", "msg-123", "delete")
	if err != nil {
		t.Fatalf("Failed to record replication: %v", err)
	}

	ops, _ := persist.GetOperationsLog(10)
	if len(ops) == 0 {
		t.Error("Expected at least 1 operation logged")
	}
}

func TestGetFaultToleranceStats(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, _ := NewCLIPersistence(dbPath)
	defer persist.Close()

	stats, err := persist.GetFaultToleranceStats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	if stats["circuit_state"] != "closed" {
		t.Errorf("Expected circuit_state 'closed', got %v", stats["circuit_state"])
	}
}

func TestRecordFaultToleranceEvent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, _ := NewCLIPersistence(dbPath)
	defer persist.Close()

	// Record an error
	persist.RecordFaultToleranceEvent("error")

	stats, _ := persist.GetFaultToleranceStats()
	if stats["total_errors"] != 1 {
		t.Errorf("Expected 1 error, got %v", stats["total_errors"])
	}

	// Record a retry success
	persist.RecordFaultToleranceEvent("retry_success")

	stats, _ = persist.GetFaultToleranceStats()
	if stats["successful_retries"] != 1 {
		t.Errorf("Expected 1 successful retry, got %v", stats["successful_retries"])
	}
}

func TestResetFaultTolerance(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, _ := NewCLIPersistence(dbPath)
	defer persist.Close()

	// Record some events
	persist.RecordFaultToleranceEvent("error")
	persist.RecordFaultToleranceEvent("error")

	stats, _ := persist.GetFaultToleranceStats()
	if stats["total_errors"] != 2 {
		t.Errorf("Expected 2 errors before reset")
	}

	// Reset
	persist.ResetFaultTolerance()

	stats, _ = persist.GetFaultToleranceStats()
	if stats["total_errors"] != 0 {
		t.Errorf("Expected 0 errors after reset, got %v", stats["total_errors"])
	}

	if stats["circuit_state"] != "closed" {
		t.Errorf("Expected circuit_state 'closed', got %v", stats["circuit_state"])
	}
}

func TestScheduleMaintenanceTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, _ := NewCLIPersistence(dbPath)
	defer persist.Close()

	err := persist.ScheduleMaintenanceTask("task-1", "Vacuum", "Database optimization")
	if err != nil {
		t.Fatalf("Failed to schedule task: %v", err)
	}

	task, _ := persist.GetMaintenanceTaskStatus("task-1")
	if task["status"] != "running" {
		t.Errorf("Expected status 'running', got %v", task["status"])
	}
}

func TestCompleteMaintenanceTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, _ := NewCLIPersistence(dbPath)
	defer persist.Close()

	persist.ScheduleMaintenanceTask("task-1", "Vacuum", "Database optimization")
	persist.CompleteMaintenanceTask("task-1")

	task, _ := persist.GetMaintenanceTaskStatus("task-1")
	if task["status"] != "completed" {
		t.Errorf("Expected status 'completed', got %v", task["status"])
	}

	if task["progress"] != 100 {
		t.Errorf("Expected progress 100, got %v", task["progress"])
	}
}

func TestRecordOperation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, _ := NewCLIPersistence(dbPath)
	defer persist.Close()

	err := persist.RecordOperation("backup", "db", "completed", map[string]string{"id": "backup-1"})
	if err != nil {
		t.Fatalf("Failed to record operation: %v", err)
	}

	ops, _ := persist.GetOperationsLog(10)
	if len(ops) == 0 {
		t.Error("Expected operation to be logged")
	}

	if ops[0]["operation"] != "backup" {
		t.Errorf("Expected operation 'backup', got %v", ops[0]["operation"])
	}
}

func TestGetSystemStats(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, _ := NewCLIPersistence(dbPath)
	defer persist.Close()

	// Record some operations
	persist.RecordOperation("test", "target", "completed", nil)
	persist.RecordOperation("test", "target", "error", nil)

	stats, _ := persist.GetSystemStats()

	if stats["total_operations"] != 2 {
		t.Errorf("Expected 2 total operations, got %v", stats["total_operations"])
	}

	if stats["successful_ops"] != 1 {
		t.Errorf("Expected 1 successful operation, got %v", stats["successful_ops"])
	}
}

func TestCLIPersistenceConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	persist, _ := NewCLIPersistence(dbPath)
	defer persist.Close()

	// Register replicas concurrently
	done := make(chan error, 3)

	go func() {
		done <- persist.RegisterReplica("replica-1", "10.0.0.1:8080")
	}()

	go func() {
		done <- persist.RegisterReplica("replica-2", "10.0.0.2:8080")
	}()

	go func() {
		done <- persist.RegisterReplica("replica-3", "10.0.0.3:8080")
	}()

	for i := 0; i < 3; i++ {
		if err := <-done; err != nil {
			t.Fatalf("Concurrent registration failed: %v", err)
		}
	}

	replicas, _ := persist.GetReplicas()
	if len(replicas) != 3 {
		t.Errorf("Expected 3 replicas after concurrent registration, got %d", len(replicas))
	}
}

func TestCLIPersistenceMultipleInstances(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create first instance
	persist1, _ := NewCLIPersistence(dbPath)
	persist1.RegisterReplica("replica-1", "10.0.0.1:8080")
	persist1.Close()

	// Create second instance and verify data persists
	persist2, _ := NewCLIPersistence(dbPath)
	defer persist2.Close()

	replicas, _ := persist2.GetReplicas()
	if len(replicas) != 1 {
		t.Errorf("Expected 1 persisted replica, got %d", len(replicas))
	}

	if replicas[0]["replica_id"] != "replica-1" {
		t.Errorf("Expected replica_id 'replica-1', got %v", replicas[0]["replica_id"])
	}
}
