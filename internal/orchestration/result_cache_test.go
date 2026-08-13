package orchestration

import (
	"testing"
	"time"
)

func TestGenerateCacheKey(t *testing.T) {
	input := &WorkflowInput{
		TaskID:         "TASK-001",
		Task:           "Test task",
		Classification: "internal",
		ChangedFiles:   []string{"file1.go", "file2.go"},
	}

	key1 := GenerateCacheKey(input)
	key2 := GenerateCacheKey(input)

	if key1 != key2 {
		t.Errorf("Same input should generate same key")
	}

	input.Task = "Different task"
	key3 := GenerateCacheKey(input)

	if key1 == key3 {
		t.Errorf("Different input should generate different key")
	}
}

func TestResultCacheGet(t *testing.T) {
	cache := NewResultCache(10, 1*time.Hour)
	result := &ConsolidatedResult{TaskID: "TASK-001"}

	// Cache miss
	_, found := cache.Get("nonexistent")
	if found {
		t.Errorf("Should not find nonexistent key")
	}

	// Cache hit
	cache.Put("test-key", result)
	retrieved, found := cache.Get("test-key")
	if !found {
		t.Errorf("Should find cached key")
	}

	if retrieved.TaskID != "TASK-001" {
		t.Errorf("Retrieved result mismatch")
	}
}

func TestResultCacheExpiration(t *testing.T) {
	cache := NewResultCache(10, 100*time.Millisecond)
	result := &ConsolidatedResult{TaskID: "TASK-001"}

	cache.Put("test-key", result)

	// Should be cached
	_, found := cache.Get("test-key")
	if !found {
		t.Errorf("Should find fresh cached key")
	}

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Should be expired
	_, found = cache.Get("test-key")
	if found {
		t.Errorf("Should not find expired key")
	}
}

func TestResultCacheInvalidation(t *testing.T) {
	cache := NewResultCache(10, 1*time.Hour)
	result1 := &ConsolidatedResult{TaskID: "TASK-001"}
	result2 := &ConsolidatedResult{TaskID: "TASK-002"}

	cache.Put("key1", result1, "task:001", "all")
	cache.Put("key2", result2, "task:002", "all")

	// Invalidate by tag
	count := cache.Invalidate("task:001")
	if count != 1 {
		t.Errorf("Should invalidate 1 entry, got %d", count)
	}

	// key1 should be gone
	_, found := cache.Get("key1")
	if found {
		t.Errorf("Invalidated key should not be found")
	}

	// key2 should still exist
	_, found = cache.Get("key2")
	if !found {
		t.Errorf("Non-invalidated key should still exist")
	}
}

func TestResultCacheLRUEviction(t *testing.T) {
	cache := NewResultCache(2, 1*time.Hour)

	r1 := &ConsolidatedResult{TaskID: "TASK-001"}
	r2 := &ConsolidatedResult{TaskID: "TASK-002"}
	r3 := &ConsolidatedResult{TaskID: "TASK-003"}

	cache.Put("key1", r1)
	cache.Put("key2", r2)

	// Access key1 to make it more recent
	cache.Get("key1")

	// Add key3, should evict key2 (LRU)
	cache.Put("key3", r3)

	stats := cache.GetStats()
	if stats.CurrentSize != 2 {
		t.Errorf("Cache size should be 2, got %d", stats.CurrentSize)
	}

	// key2 should be evicted
	_, found := cache.Get("key2")
	if found {
		t.Errorf("LRU key should be evicted")
	}

	// key1 and key3 should exist
	_, found1 := cache.Get("key1")
	_, found3 := cache.Get("key3")
	if !found1 || !found3 {
		t.Errorf("Non-LRU keys should still exist")
	}
}

func TestResultCacheStats(t *testing.T) {
	cache := NewResultCache(10, 1*time.Hour)
	result := &ConsolidatedResult{TaskID: "TASK-001"}

	cache.Put("key1", result)

	// Hits
	cache.Get("key1")
	cache.Get("key1")

	// Misses
	cache.Get("nonexistent")
	cache.Get("nonexistent2")

	stats := cache.GetStats()
	if stats.Hits != 2 {
		t.Errorf("Hits should be 2, got %d", stats.Hits)
	}

	if stats.Misses != 2 {
		t.Errorf("Misses should be 2, got %d", stats.Misses)
	}

	if stats.HitRate != 0.5 {
		t.Errorf("Hit rate should be 0.5, got %f", stats.HitRate)
	}
}

func TestResultCacheClear(t *testing.T) {
	cache := NewResultCache(10, 1*time.Hour)
	result := &ConsolidatedResult{TaskID: "TASK-001"}

	cache.Put("key1", result)
	cache.Put("key2", result)

	stats := cache.GetStats()
	if stats.CurrentSize != 2 {
		t.Errorf("Should have 2 entries before clear")
	}

	cache.Clear()

	stats = cache.GetStats()
	if stats.CurrentSize != 0 {
		t.Errorf("Should have 0 entries after clear")
	}

	_, found := cache.Get("key1")
	if found {
		t.Errorf("Should not find entries after clear")
	}
}

func TestResultCacheSetTTL(t *testing.T) {
	cache := NewResultCache(10, 1*time.Hour)
	result := &ConsolidatedResult{TaskID: "TASK-001"}

	cache.Put("key1", result)

	// Set shorter TTL
	success := cache.SetTTL("key1", 50*time.Millisecond)
	if !success {
		t.Errorf("SetTTL should succeed for existing key")
	}

	// Should still be cached
	_, found := cache.Get("key1")
	if !found {
		t.Errorf("Should find key with new TTL")
	}

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	_, found = cache.Get("key1")
	if found {
		t.Errorf("Should expire with new TTL")
	}
}

func TestCachedOrchestrationWorkflow(t *testing.T) {
	// Create a mock workflow
	routing := &RoutingConfig{Routes: []Route{}}
	executor := NewExecutor(&mockAgentRunner{}, 4, 0)
	workflow := NewOrchestrationWorkflow(routing, executor, nil)

	cached := NewCachedOrchestrationWorkflow(workflow, 10, 1*time.Hour, InvalidateOnFileChange)

	// Disable caching for this test (workflow will fail anyway due to no routing)
	cached.enableByDefault = false

	stats := cached.GetCacheStats()
	if stats.MaxSize != 10 {
		t.Errorf("Cache max size should be 10, got %d", stats.MaxSize)
	}
}
