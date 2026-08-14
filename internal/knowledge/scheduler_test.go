package knowledge

import (
	"testing"
)

func TestDefaultPolicy(t *testing.T) {
	policy := DefaultRebalancingPolicy()

	if !policy.Enabled {
		t.Error("Policy should be enabled by default")
	}

	if policy.CheckIntervalSeconds != 3600 {
		t.Errorf("Expected 3600s interval, got %d", policy.CheckIntervalSeconds)
	}

	if policy.ImbalanceThreshold != 20.0 {
		t.Errorf("Expected 20%% threshold, got %.1f", policy.ImbalanceThreshold)
	}

	if policy.MaxConcurrentMigrations != 2 {
		t.Errorf("Expected 2 max concurrent, got %d", policy.MaxConcurrentMigrations)
	}
}

func TestSchedulerCreation(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	rebalancer := NewShardRebalancer(registry, strategy)
	executor := NewMigrationExecutor(registry)
	policy := DefaultRebalancingPolicy()

	scheduler := NewRebalancingScheduler(policy, rebalancer, executor)

	if scheduler.policy == nil {
		t.Error("Scheduler policy should not be nil")
	}

	if scheduler.rebalancer == nil {
		t.Error("Scheduler rebalancer should not be nil")
	}

	if scheduler.executor == nil {
		t.Error("Scheduler executor should not be nil")
	}
}

func TestSchedulerStart(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &ClassificationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	rebalancer := NewShardRebalancer(registry, strategy)
	executor := NewMigrationExecutor(registry)

	scheduler := NewRebalancingScheduler(DefaultRebalancingPolicy(), rebalancer, executor)

	// Start scheduler
	err := scheduler.Start()
	if err != nil {
		t.Fatalf("Cannot start scheduler: %v", err)
	}

	// Check status
	status := scheduler.GetSchedulerStatus()
	if !status.IsRunning {
		t.Error("Scheduler should be running")
	}

	// Stop scheduler
	err = scheduler.Stop()
	if err != nil {
		t.Fatalf("Cannot stop scheduler: %v", err)
	}
}

func TestSchedulerStartAlreadyRunning(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &ConversationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	rebalancer := NewShardRebalancer(registry, strategy)
	executor := NewMigrationExecutor(registry)

	scheduler := NewRebalancingScheduler(DefaultRebalancingPolicy(), rebalancer, executor)

	// Start first time
	err := scheduler.Start()
	if err != nil {
		t.Fatalf("Cannot start scheduler: %v", err)
	}
	defer scheduler.Stop()

	// Try to start again
	err = scheduler.Start()
	if err == nil {
		t.Error("Expected error when starting already-running scheduler")
	}
}

func TestSchedulerStatus(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	rebalancer := NewShardRebalancer(registry, strategy)
	executor := NewMigrationExecutor(registry)
	policy := DefaultRebalancingPolicy()

	scheduler := NewRebalancingScheduler(policy, rebalancer, executor)

	// Check initial status
	status := scheduler.GetSchedulerStatus()

	if status.IsRunning {
		t.Error("Scheduler should not be running initially")
	}

	if status.CheckIntervalSeconds != 3600 {
		t.Errorf("Expected 3600s check interval, got %d", status.CheckIntervalSeconds)
	}

	if status.TotalJobsExecuted != 0 {
		t.Errorf("Expected 0 jobs executed, got %d", status.TotalJobsExecuted)
	}
}

func TestSchedulerPolicy(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &CompositeShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	rebalancer := NewShardRebalancer(registry, strategy)
	executor := NewMigrationExecutor(registry)

	policy := &RebalancingPolicy{
		Enabled:                 true,
		CheckIntervalSeconds:    1800,
		ImbalanceThreshold:      15.0,
		MaxConcurrentMigrations: 1,
	}

	scheduler := NewRebalancingScheduler(policy, rebalancer, executor)

	if scheduler.policy.CheckIntervalSeconds != 1800 {
		t.Errorf("Expected 1800s interval, got %d", scheduler.policy.CheckIntervalSeconds)
	}

	if scheduler.policy.ImbalanceThreshold != 15.0 {
		t.Errorf("Expected 15%% threshold, got %.1f", scheduler.policy.ImbalanceThreshold)
	}
}

func TestSchedulerWithNilPolicy(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	rebalancer := NewShardRebalancer(registry, strategy)
	executor := NewMigrationExecutor(registry)

	// Create with nil policy (should use default)
	scheduler := NewRebalancingScheduler(nil, rebalancer, executor)

	if scheduler.policy == nil {
		t.Error("Scheduler should have default policy")
	}

	if scheduler.policy.CheckIntervalSeconds != 3600 {
		t.Errorf("Expected default interval 3600, got %d", scheduler.policy.CheckIntervalSeconds)
	}
}

func TestMaintenanceWindow(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &ClassificationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	rebalancer := NewShardRebalancer(registry, strategy)
	executor := NewMigrationExecutor(registry)

	// Policy with maintenance window
	policy := &RebalancingPolicy{
		Enabled:                true,
		CheckIntervalSeconds:   3600,
		MaintenanceWindowStart: "02:00", // 2 AM UTC
		MaintenanceWindowEnd:   "03:00", // 3 AM UTC
	}

	scheduler := NewRebalancingScheduler(policy, rebalancer, executor)

	// Should have maintenance window configured
	if scheduler.policy.MaintenanceWindowStart != "02:00" {
		t.Error("Maintenance window not set correctly")
	}
}

func TestParseTimeString(t *testing.T) {
	tests := []struct {
		input    string
		expected []int
	}{
		{"02:00", []int{2, 0}},
		{"14:30", []int{14, 30}},
		{"00:00", []int{0, 0}},
		{"23:59", []int{23, 59}},
		{"invalid", []int{}},
	}

	for _, test := range tests {
		result := parseTimeString(test.input)

		if len(result) != len(test.expected) {
			t.Errorf("Parse %s: expected length %d, got %d", test.input, len(test.expected), len(result))
			continue
		}

		for i := range result {
			if result[i] != test.expected[i] {
				t.Errorf("Parse %s: expected %v, got %v", test.input, test.expected, result)
				break
			}
		}
	}
}

func TestSchedulerGetJobs(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	rebalancer := NewShardRebalancer(registry, strategy)
	executor := NewMigrationExecutor(registry)

	scheduler := NewRebalancingScheduler(DefaultRebalancingPolicy(), rebalancer, executor)

	// Initially no jobs
	jobs := scheduler.GetScheduledJobs()
	if len(jobs) != 0 {
		t.Errorf("Expected 0 initial jobs, got %d", len(jobs))
	}
}

func TestSchedulerStop(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &ConversationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	rebalancer := NewShardRebalancer(registry, strategy)
	executor := NewMigrationExecutor(registry)

	scheduler := NewRebalancingScheduler(DefaultRebalancingPolicy(), rebalancer, executor)

	// Start and verify
	scheduler.Start()
	status := scheduler.GetSchedulerStatus()
	if !status.IsRunning {
		t.Error("Scheduler should be running after start")
	}

	// Stop and verify
	scheduler.Stop()
	status = scheduler.GetSchedulerStatus()
	if status.IsRunning {
		t.Error("Scheduler should not be running after stop")
	}
}

func TestSchedulerStopNotRunning(t *testing.T) {
	requireSQLite(t)
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	rebalancer := NewShardRebalancer(registry, strategy)
	executor := NewMigrationExecutor(registry)

	scheduler := NewRebalancingScheduler(DefaultRebalancingPolicy(), rebalancer, executor)

	// Try to stop without starting
	err := scheduler.Stop()
	if err == nil {
		t.Error("Expected error when stopping non-running scheduler")
	}
}
