package knowledge

import (
	"testing"
)

// Sharding strategy tests

func TestClassificationShardingStrategy(t *testing.T) {
	strategy := NewClassificationShardingStrategy("public", "general", "secret")

	// Test shard key generation
	key := strategy.GetShardKey("app-1", "general", "conv-1")
	if key != "general" {
		t.Errorf("Expected shard key 'general', got '%s'", key)
	}

	// Test shard ID assignment (consistent)
	id1 := strategy.GetShardID("general", 3)
	id2 := strategy.GetShardID("general", 3)
	if id1 != id2 {
		t.Errorf("Shard IDs not consistent: %d != %d", id1, id2)
	}

	// Test name
	if strategy.Name() != "classification" {
		t.Errorf("Expected name 'classification', got '%s'", strategy.Name())
	}
}

func TestSourceShardingStrategy(t *testing.T) {
	strategy := &SourceShardingStrategy{}

	// Test shard key generation
	key := strategy.GetShardKey("app-a", "general", "conv-1")
	if key != "app-a" {
		t.Errorf("Expected shard key 'app-a', got '%s'", key)
	}

	// Test consistency
	id1 := strategy.GetShardID("app-a", 3)
	id2 := strategy.GetShardID("app-a", 3)
	if id1 != id2 {
		t.Errorf("Shard IDs not consistent")
	}

	// Different sources should potentially map to different shards
	_ = strategy.GetShardID("app-b", 3)
	// Note: Not asserting different IDs as hashing could collide

	if strategy.Name() != "source" {
		t.Errorf("Expected name 'source'")
	}
}

func TestConversationShardingStrategy(t *testing.T) {
	strategy := &ConversationShardingStrategy{}

	// Test shard key generation
	key := strategy.GetShardKey("app-1", "general", "conv-123")
	if key != "conv-123" {
		t.Errorf("Expected shard key 'conv-123', got '%s'", key)
	}

	// Same conversation should hash to same shard
	id1 := strategy.GetShardID("conv-123", 4)
	id2 := strategy.GetShardID("conv-123", 4)
	if id1 != id2 {
		t.Errorf("Same conversation should map to same shard")
	}

	if strategy.Name() != "conversation" {
		t.Errorf("Expected name 'conversation'")
	}
}

func TestCompositeShardingStrategy(t *testing.T) {
	strategy := NewCompositeShardingStrategy(
		NewClassificationShardingStrategy("general", "secret"),
		&SourceShardingStrategy{},
	)

	// Shard key should combine both
	key := strategy.GetShardKey("app-1", "secret", "conv-1")
	if key != "|secret|app-1" {
		t.Errorf("Expected composite key '|secret|app-1', got '%s'", key)
	}

	// Should produce valid shard IDs
	id := strategy.GetShardID(key, 4)
	if id < 0 || id >= 4 {
		t.Errorf("Invalid shard ID: %d", id)
	}

	if strategy.Name() != "composite" {
		t.Errorf("Expected name 'composite'")
	}
}

// Consistent hash ring tests

// Store registry tests

func TestStoreRegistryBasic(t *testing.T) {
	requireSQLite(t)
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)

	// Create test stores
	store1 := setupTestDB(t)
	defer store1.Close()

	store2 := setupTestDB(t)
	defer store2.Close()

	// Add stores
	err := registry.AddStore("shard-0", store1)
	if err != nil {
		t.Fatalf("Failed to add store: %v", err)
	}

	err = registry.AddStore("shard-1", store2)
	if err != nil {
		t.Fatalf("Failed to add store: %v", err)
	}

	// Get store
	store, shardID, err := registry.GetStore("app-1", "general", "conv-1")
	if err != nil {
		t.Fatalf("Failed to get store: %v", err)
	}

	if store == nil {
		t.Error("Expected non-nil store")
	}

	if shardID == "" {
		t.Error("Expected non-empty shard ID")
	}
}

func TestStoreRegistryConsistency(t *testing.T) {
	requireSQLite(t)
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)

	store := setupTestDB(t)
	defer store.Close()

	registry.AddStore("shard-0", store)

	// Same message should always map to same store
	store1, shard1, _ := registry.GetStore("app-1", "general", "conv-1")
	store2, shard2, _ := registry.GetStore("app-1", "general", "conv-1")

	if store1 != store2 {
		t.Error("Inconsistent store assignment")
	}

	if shard1 != shard2 {
		t.Error("Inconsistent shard assignment")
	}
}

func TestStoreRegistryGetAll(t *testing.T) {
	requireSQLite(t)
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)

	store1 := setupTestDB(t)
	defer store1.Close()

	store2 := setupTestDB(t)
	defer store2.Close()

	registry.AddStore("shard-0", store1)
	registry.AddStore("shard-1", store2)

	stores := registry.GetStores()
	if len(stores) != 2 {
		t.Errorf("Expected 2 stores, got %d", len(stores))
	}

	shardIDs := registry.GetShardIDs()
	if len(shardIDs) != 2 {
		t.Errorf("Expected 2 shard IDs, got %d", len(shardIDs))
	}
}

func TestStoreRegistryRemove(t *testing.T) {
	requireSQLite(t)
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)

	store := setupTestDB(t)
	defer store.Close()

	registry.AddStore("shard-0", store)

	// Remove store
	err := registry.RemoveStore("shard-0")
	if err != nil {
		t.Fatalf("Failed to remove store: %v", err)
	}

	// Verify store is gone
	stores := registry.GetStores()
	if len(stores) != 0 {
		t.Errorf("Expected 0 stores after removal, got %d", len(stores))
	}

	// Try to remove non-existent store
	err = registry.RemoveStore("shard-0")
	if err == nil {
		t.Error("Expected error when removing non-existent store")
	}
}

func TestStoreRegistryNoStores(t *testing.T) {
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)

	// Try to get store when none registered
	_, _, err := registry.GetStore("app-1", "general", "conv-1")
	if err == nil {
		t.Error("Expected error when no stores registered")
	}
}

func TestStoreRegistryAddNilStore(t *testing.T) {
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)

	// Try to add nil store
	err := registry.AddStore("shard-0", nil)
	if err == nil {
		t.Error("Expected error when adding nil store")
	}
}

// Sharding strategy with registry integration

func TestRegistryWithClassificationSharding(t *testing.T) {
	requireSQLite(t)
	strategy := NewClassificationShardingStrategy("public", "secret")
	registry := NewStoreRegistry(strategy)

	storePublic := setupTestDB(t)
	defer storePublic.Close()

	storeSecret := setupTestDB(t)
	defer storeSecret.Close()

	registry.AddStore("public", storePublic)
	registry.AddStore("secret", storeSecret)

	// Messages with different classifications should map to different shards
	store1, shard1, _ := registry.GetStore("app", "public", "conv-1")
	store2, shard2, _ := registry.GetStore("app", "secret", "conv-1")

	if shard1 == shard2 {
		t.Logf("Warning: different classifications mapped to same shard")
	}

	if store1 == nil || store2 == nil {
		t.Error("Expected non-nil stores")
	}
}

func TestHashShardKey(t *testing.T) {
	// Same key should hash to same value
	hash1 := hashShardKey("test-key")
	hash2 := hashShardKey("test-key")

	if hash1 != hash2 {
		t.Error("Hash function not consistent")
	}

	// Different keys should (usually) hash to different values
	hash3 := hashShardKey("different-key")
	if hash1 == hash3 {
		t.Logf("Hash collision (expected rare event)")
	}

	// Hash should be non-negative
	if hash1 < 0 {
		t.Errorf("Negative hash value: %d", hash1)
	}
}
