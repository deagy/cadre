package knowledge

import (
	"testing"
)

func TestMigrationExecutorPrepareMigration(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	executor := NewMigrationExecutor(registry)

	// Prepare migration
	txID, err := executor.PrepareMigration("shard-0", "shard-1", []string{"msg-1", "msg-2"}, "test-user")
	if err != nil {
		t.Fatalf("Cannot prepare migration: %v", err)
	}

	if txID == "" {
		t.Error("Expected non-empty transaction ID")
	}

	// Get status
	tx, err := executor.GetMigrationStatus(txID)
	if err != nil {
		t.Fatalf("Cannot get status: %v", err)
	}

	if tx.State != MigrationStatePrepared {
		t.Errorf("Expected prepared state, got %s", tx.State)
	}

	if len(tx.Steps) != 2 {
		t.Errorf("Expected 2 steps, got %d", len(tx.Steps))
	}
}

func TestMigrationExecutorPrepareSameShard(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &ClassificationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	executor := NewMigrationExecutor(registry)

	// Try to prepare migration to same shard
	_, err := executor.PrepareMigration("shard-0", "shard-0", []string{"msg-1"}, "test-user")
	if err == nil {
		t.Error("Expected error for same shard migration")
	}
}

func TestMigrationExecutorPrepareNoMessages(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &ConversationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	executor := NewMigrationExecutor(registry)

	// Try to prepare migration with no messages
	_, err := executor.PrepareMigration("shard-0", "shard-1", []string{}, "test-user")
	if err == nil {
		t.Error("Expected error for empty message list")
	}
}

func TestMigrationExecutorExecuteMigration(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	executor := NewMigrationExecutor(registry)

	// Prepare migration
	txID, _ := executor.PrepareMigration("shard-0", "shard-1", []string{"msg-1"}, "test-user")

	// Execute migration
	err := executor.ExecuteMigration(txID)
	if err != nil {
		t.Fatalf("Cannot execute migration: %v", err)
	}

	// Migration stays in executing state until committed
	tx, _ := executor.GetMigrationStatus(txID)
	if tx.State != MigrationStateExecuting {
		t.Errorf("Expected executing state after execute, got %s", tx.State)
	}

	// Commit migration
	err = executor.CommitMigration(txID)
	if err != nil {
		t.Fatalf("Cannot commit migration: %v", err)
	}

	// Now check committed state
	tx, _ = executor.GetMigrationStatus(txID)
	if tx.State != MigrationStateCommitted {
		t.Errorf("Expected committed state after commit, got %s", tx.State)
	}

	if tx.CompletedAt == nil {
		t.Error("Expected completion timestamp")
	}
}

func TestMigrationExecutorRollback(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &CompositeShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	executor := NewMigrationExecutor(registry)

	// Prepare and execute (stays in executing state)
	txID, _ := executor.PrepareMigration("shard-0", "shard-1", []string{"msg-1"}, "test-user")
	executor.ExecuteMigration(txID)

	// Can still rollback when executing
	err := executor.RollbackMigration(txID)
	if err != nil {
		t.Fatalf("Cannot rollback migration: %v", err)
	}

	// Check status
	tx, _ := executor.GetMigrationStatus(txID)
	if tx.State != MigrationStateRolledBack {
		t.Errorf("Expected rolled_back state, got %s", tx.State)
	}
}

func TestMigrationExecutorRollbackNotExecuting(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	executor := NewMigrationExecutor(registry)

	// Prepare but don't execute
	txID, _ := executor.PrepareMigration("shard-0", "shard-1", []string{"msg-1"}, "test-user")

	// Try to rollback prepared transaction
	err := executor.RollbackMigration(txID)
	if err == nil {
		t.Error("Expected error for rollback of prepared transaction")
	}
}

func TestMigrationExecutorProgress(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &ClassificationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	executor := NewMigrationExecutor(registry)

	// Prepare migration
	txID, _ := executor.PrepareMigration("shard-0", "shard-1", []string{"msg-1", "msg-2", "msg-3"}, "test-user")

	// Get progress before execution
	progress, err := executor.GetMigrationProgress(txID)
	if err != nil {
		t.Fatalf("Cannot get progress: %v", err)
	}

	if progress.TotalSteps != 3 {
		t.Errorf("Expected 3 steps, got %d", progress.TotalSteps)
	}

	if progress.PercentComplete != 0 {
		t.Errorf("Expected 0%% progress before execution, got %.1f%%", progress.PercentComplete)
	}

	// Execute
	executor.ExecuteMigration(txID)

	// Get progress after execution
	progress, _ = executor.GetMigrationProgress(txID)
	if progress.PercentComplete != 100 {
		t.Errorf("Expected 100%% progress after execution, got %.1f%%", progress.PercentComplete)
	}
}

func TestMigrationExecutorStats(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &ConversationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	executor := NewMigrationExecutor(registry)

	// Create multiple migrations
	txID1, _ := executor.PrepareMigration("shard-0", "shard-1", []string{"msg-1", "msg-2"}, "test-user")
	_, _ = executor.PrepareMigration("shard-1", "shard-0", []string{"msg-3"}, "test-user")

	// Execute first
	executor.ExecuteMigration(txID1)
	executor.CommitMigration(txID1)

	// Get stats
	stats := executor.GetMigrationStats()

	if stats.TotalMigrations != 2 {
		t.Errorf("Expected 2 total migrations, got %d", stats.TotalMigrations)
	}

	if stats.CommittedMigrations != 1 {
		t.Errorf("Expected 1 committed, got %d", stats.CommittedMigrations)
	}

	if stats.PreparedMigrations != 1 {
		t.Errorf("Expected 1 prepared, got %d", stats.PreparedMigrations)
	}
}

func TestMigrationExecutorCanRollback(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	executor := NewMigrationExecutor(registry)

	txID, _ := executor.PrepareMigration("shard-0", "shard-1", []string{"msg-1"}, "test-user")

	// Prepared migration cannot be rolled back
	canRollback, _ := executor.CanRollback(txID)
	if canRollback {
		t.Error("Expected cannot rollback prepared migration")
	}

	// Execute migration
	executor.ExecuteMigration(txID)

	// Executed migration can be rolled back
	canRollback, _ = executor.CanRollback(txID)
	if !canRollback {
		t.Error("Expected can rollback executing migration")
	}

	// Rollback the migration
	executor.RollbackMigration(txID)

	// After rollback, cannot rollback again
	canRollback, _ = executor.CanRollback(txID)
	if canRollback {
		t.Error("Expected cannot rollback rolled back migration")
	}
}

func TestMigrationExecutorStatusNotFound(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &ClassificationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	executor := NewMigrationExecutor(registry)

	// Try to get status of non-existent migration
	_, err := executor.GetMigrationStatus("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent migration")
	}
}

func TestMigrationExecutorProgressNotFound(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	executor := NewMigrationExecutor(registry)

	// Try to get progress of non-existent migration
	_, err := executor.GetMigrationProgress("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent migration")
	}
}

func TestMigrationExecutorMultipleMigrations(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	store2 := setupTestDB(t)
	defer store2.Close()

	strategy := &CompositeShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)
	registry.AddStore("shard-2", store2)

	executor := NewMigrationExecutor(registry)

	// Create 3 migrations
	txID1, _ := executor.PrepareMigration("shard-0", "shard-1", []string{"msg-1"}, "test-user")
	txID2, _ := executor.PrepareMigration("shard-1", "shard-2", []string{"msg-2"}, "test-user")
	txID3, _ := executor.PrepareMigration("shard-2", "shard-0", []string{"msg-3"}, "test-user")

	// Execute and commit all
	executor.ExecuteMigration(txID1)
	executor.CommitMigration(txID1)
	executor.ExecuteMigration(txID2)
	executor.CommitMigration(txID2)
	executor.ExecuteMigration(txID3)
	executor.CommitMigration(txID3)

	// Check stats
	stats := executor.GetMigrationStats()

	if stats.TotalMigrations != 3 {
		t.Errorf("Expected 3 migrations, got %d", stats.TotalMigrations)
	}

	if stats.CommittedMigrations != 3 {
		t.Errorf("Expected 3 committed, got %d", stats.CommittedMigrations)
	}
}

func TestMigrationExecutorLargeMessageSet(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	executor := NewMigrationExecutor(registry)

	// Create migration with 100 messages
	messageIDs := make([]string, 100)
	for i := 0; i < 100; i++ {
		messageIDs[i] = string(rune(48 + (i % 10)))
	}

	txID, err := executor.PrepareMigration("shard-0", "shard-1", messageIDs, "test-user")
	if err != nil {
		t.Fatalf("Cannot prepare large migration: %v", err)
	}

	// Execute and commit
	err = executor.ExecuteMigration(txID)
	if err != nil {
		t.Fatalf("Cannot execute large migration: %v", err)
	}
	executor.CommitMigration(txID)

	// Check progress
	progress, _ := executor.GetMigrationProgress(txID)
	if progress.TotalSteps != 100 {
		t.Errorf("Expected 100 steps, got %d", progress.TotalSteps)
	}

	if progress.PercentComplete != 100 {
		t.Errorf("Expected 100%% complete, got %.1f%%", progress.PercentComplete)
	}
}

func TestMigrationExecutorTimingEstimate(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &ClassificationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	executor := NewMigrationExecutor(registry)

	// Create, execute, and commit migration
	txID, _ := executor.PrepareMigration("shard-0", "shard-1", []string{"msg-1"}, "test-user")
	executor.ExecuteMigration(txID)
	executor.CommitMigration(txID)

	// Get progress
	progress, _ := executor.GetMigrationProgress(txID)

	// Should have completed timestamp
	if progress.CompletedAt == nil {
		t.Error("Expected completion timestamp")
	}

	// Should show completion
	if progress.State != "committed" {
		t.Errorf("Expected committed state, got %s", progress.State)
	}
}
