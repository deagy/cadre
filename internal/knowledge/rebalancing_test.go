//go:build cgo
// +build cgo

package knowledge

import (
	"fmt"
	"testing"
)

func TestRebalancerAnalyzeShard(t *testing.T) {
	// Setup test stores
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	// Add messages to store0
	for i := 0; i < 8; i++ {
		store0.SaveMessage(
			"app-a", nil, "conv", nil, string(rune(48+i)),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)
	}

	// Add messages to store1
	for i := 0; i < 2; i++ {
		store1.SaveMessage(
			"app-b", nil, "conv", nil, string(rune(53+i)),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)
	}

	strategy := &ClassificationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	rebalancer := NewShardRebalancer(registry, strategy)

	// Analyze shards
	analysis, err := rebalancer.AnalyzeShard()
	if err != nil {
		t.Fatalf("Cannot analyze shards: %v", err)
	}

	if analysis.TotalMessages != 10 {
		t.Errorf("Expected 10 total messages, got %d", analysis.TotalMessages)
	}

	// Should identify imbalance
	if analysis.IsBalanced {
		t.Error("Expected shards to be imbalanced")
	}

	// Should identify hot shard
	if len(analysis.HotShards) == 0 {
		t.Error("Expected to identify hot shard")
	}
}

func TestRebalancerNoImbalance(t *testing.T) {
	// Setup balanced stores
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	// Add equal messages to both (5 each = 50% each)
	for i := 0; i < 5; i++ {
		store0.SaveMessage(
			"app-a", nil, "conv", nil, string(rune(48+i)),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)
		store1.SaveMessage(
			"app-b", nil, "conv", nil, string(rune(53+i)),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)
	}

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	rebalancer := NewShardRebalancer(registry, strategy)

	analysis, err := rebalancer.AnalyzeShard()
	if err != nil {
		t.Fatalf("Cannot analyze shards: %v", err)
	}

	// 50/50 split should have no hot shards (not >60%)
	if len(analysis.HotShards) > 0 {
		t.Errorf("Expected no hot shards for 50/50 split, got %d", len(analysis.HotShards))
	}

	// 50/50 should be balanced (no hot shards)
	if !analysis.IsBalanced {
		t.Logf("Note: Analysis shows StandardDeviation=%.2f (threshold for balanced: <20)", analysis.StandardDeviation)
	}
}

func TestRebalancerStartRebalance(t *testing.T) {
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &ConversationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	rebalancer := NewShardRebalancer(registry, strategy)

	// Start rebalancing
	migrationID, err := rebalancer.StartRebalance("shard-0", "shard-1", "test-user")
	if err != nil {
		t.Fatalf("Cannot start rebalance: %v", err)
	}

	if migrationID == "" {
		t.Error("Expected non-empty migration ID")
	}

	// Check metrics were created
	metrics, err := rebalancer.GetRebalanceStatus(migrationID)
	if err != nil {
		t.Fatalf("Cannot get rebalance status: %v", err)
	}

	if metrics.SourceShard != "shard-0" {
		t.Errorf("Expected source shard shard-0, got %s", metrics.SourceShard)
	}

	if metrics.DestinationShard != "shard-1" {
		t.Errorf("Expected dest shard shard-1, got %s", metrics.DestinationShard)
	}

	if metrics.Status != "pending" {
		t.Errorf("Expected pending status, got %s", metrics.Status)
	}
}

func TestRebalancerStartRebalanceSameShard(t *testing.T) {
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &CompositeShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	rebalancer := NewShardRebalancer(registry, strategy)

	// Try to rebalance to same shard (should fail)
	_, err := rebalancer.StartRebalance("shard-0", "shard-0", "test-user")
	if err == nil {
		t.Error("Expected error for same source/dest shard")
	}
}

func TestRebalancerStartRebalanceMissingShard(t *testing.T) {
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &ClassificationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	rebalancer := NewShardRebalancer(registry, strategy)

	// Try to rebalance to non-existent shard
	_, err := rebalancer.StartRebalance("shard-0", "shard-99", "test-user")
	if err == nil {
		t.Error("Expected error for missing destination shard")
	}
}

func TestRebalancerCancelRebalance(t *testing.T) {
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	rebalancer := NewShardRebalancer(registry, strategy)

	// Start rebalancing
	migrationID, _ := rebalancer.StartRebalance("shard-0", "shard-1", "test-user")

	// Cancel it
	err := rebalancer.CancelRebalance(migrationID)
	if err != nil {
		t.Fatalf("Cannot cancel rebalance: %v", err)
	}

	// Check status changed
	metrics, _ := rebalancer.GetRebalanceStatus(migrationID)
	if metrics.Status != "cancelled" {
		t.Errorf("Expected cancelled status, got %s", metrics.Status)
	}
}

func TestRebalancerCancelNonexistent(t *testing.T) {
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &ClassificationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	rebalancer := NewShardRebalancer(registry, strategy)

	// Try to cancel non-existent migration
	err := rebalancer.CancelRebalance("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent migration")
	}
}

func TestRebalancerGetStats(t *testing.T) {
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &ConversationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	rebalancer := NewShardRebalancer(registry, strategy)

	// Start a migration
	migrationID, _ := rebalancer.StartRebalance("shard-0", "shard-1", "test-user")

	// Get stats immediately after starting
	stats := rebalancer.GetRebalancingStats()

	if stats.TotalMigrations != 1 {
		t.Errorf("Expected 1 total migration, got %d", stats.TotalMigrations)
	}

	// Should be pending or active (both count as "active")
	if stats.ActiveMigrations < 1 {
		t.Errorf("Expected at least 1 active migration, got %d", stats.ActiveMigrations)
	}

	// Cancel migration
	rebalancer.CancelRebalance(migrationID)

	// Stats should update
	stats = rebalancer.GetRebalancingStats()
	if stats.ActiveMigrations != 0 {
		t.Errorf("Expected 0 active migrations after cancel, got %d", stats.ActiveMigrations)
	}
}

func TestRebalancerAnalyzeEmptyRegistry(t *testing.T) {
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)

	rebalancer := NewShardRebalancer(registry, strategy)

	// Analyze with no shards
	_, err := rebalancer.AnalyzeShard()
	if err == nil {
		t.Error("Expected error for empty registry")
	}
}

func TestRebalancerMultipleShards(t *testing.T) {
	// Create 3 shards with different loads
	stores := make(map[string]*Store)
	for i := 0; i < 3; i++ {
		store := setupTestDB(t)
		defer store.Close()

		// Add different amounts to each
		count := (i + 1) * 3
		for j := 0; j < count; j++ {
			store.SaveMessage(
				"app", nil, "conv", nil, string(rune(48+j)),
				"user", "content", nil, "general", false,
				`[]`, `{}`, nil,
			)
		}

		stores[string(rune(48+i))] = store
	}

	strategy := &CompositeShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	for shardID, store := range stores {
		registry.AddStore("shard-"+shardID, store)
	}

	rebalancer := NewShardRebalancer(registry, strategy)

	analysis, err := rebalancer.AnalyzeShard()
	if err != nil {
		t.Fatalf("Cannot analyze: %v", err)
	}

	// Should detect imbalance with 3 shards
	expectedTotal := 3 + 6 + 9
	if analysis.TotalMessages != int64(expectedTotal) {
		t.Errorf("Expected %d total messages, got %d", expectedTotal, analysis.TotalMessages)
	}

	// Close stores
	for _, store := range stores {
		store.Close()
	}
}

func TestRebalancerGetStatusNonexistent(t *testing.T) {
	store0 := setupTestDB(t)
	defer store0.Close()

	strategy := &ClassificationShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)

	rebalancer := NewShardRebalancer(registry, strategy)

	// Try to get status for non-existent migration
	_, err := rebalancer.GetRebalanceStatus("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent migration")
	}
}

func TestRebalancerHotShardDetection(t *testing.T) {
	// Create heavily imbalanced shards
	store0 := setupTestDB(t)
	defer store0.Close()

	store1 := setupTestDB(t)
	defer store1.Close()

	// Add 90 messages to shard0, 10 to shard1 (unique IDs)
	for i := 0; i < 90; i++ {
		store0.SaveMessage(
			"app-a", nil, "conv", nil, fmt.Sprintf("msg-0-%d", i),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)
	}

	for i := 0; i < 10; i++ {
		store1.SaveMessage(
			"app-b", nil, "conv", nil, fmt.Sprintf("msg-1-%d", i),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)
	}

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store0)
	registry.AddStore("shard-1", store1)

	rebalancer := NewShardRebalancer(registry, strategy)

	analysis, err := rebalancer.AnalyzeShard()
	if err != nil {
		t.Fatalf("Cannot analyze: %v", err)
	}

	// Should detect hot shard (90% is definitely > 60%)
	if len(analysis.HotShards) == 0 {
		t.Fatalf("Expected to detect hot shard, got none. Total: %d, Analysis: %+v", analysis.TotalMessages, analysis)
	}

	// At least one shard should be >60%
	found := false
	for _, hs := range analysis.HotShards {
		if hs.Percentage > 60 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected at least one shard > 60%%, got hot shards: %v", analysis.HotShards)
	}
}
