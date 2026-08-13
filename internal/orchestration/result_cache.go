package orchestration

import (
	"context"
	"crypto/md5"
	"fmt"
	"sync"
	"time"
)

// CacheEntry holds a cached orchestration result.
type CacheEntry struct {
	Key              string
	Result           *ConsolidatedResult
	CreatedAt        time.Time
	ExpiresAt        time.Time
	AccessCount      int
	LastAccessedAt   time.Time
	InvalidationTags []string
	Metadata         map[string]string
}

// CacheStats tracks cache performance metrics.
type CacheStats struct {
	Hits          int64
	Misses        int64
	Evictions     int64
	Invalidations int64
	CurrentSize   int
	MaxSize       int
	HitRate       float64
	AvgAccessTime time.Duration
}

// ResultCache provides memoization for orchestration results.
type ResultCache struct {
	mu              sync.RWMutex
	entries         map[string]*CacheEntry
	tags            map[string][]string // tag -> entry keys
	maxSize         int
	defaultTTL      time.Duration
	stats           CacheStats
	lastCleanupTime time.Time
}

// NewResultCache creates a new result cache.
func NewResultCache(maxSize int, defaultTTL time.Duration) *ResultCache {
	cache := &ResultCache{
		entries:    make(map[string]*CacheEntry),
		tags:       make(map[string][]string),
		maxSize:    maxSize,
		defaultTTL: defaultTTL,
		stats:      CacheStats{MaxSize: maxSize},
	}
	// Start cleanup goroutine
	go cache.backgroundCleanup()
	return cache
}

// GenerateCacheKey creates a deterministic cache key from workflow input.
func GenerateCacheKey(input *WorkflowInput) string {
	data := fmt.Sprintf("%s:%s:%s:%v", input.TaskID, input.Task, input.Classification, input.ChangedFiles)
	hash := md5.Sum([]byte(data))
	return fmt.Sprintf("cache:%x", hash)
}

// Get retrieves a cached result if it exists and is valid.
func (rc *ResultCache) Get(key string) (*ConsolidatedResult, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	entry, exists := rc.entries[key]
	if !exists {
		rc.stats.Misses++
		rc.updateHitRate()
		return nil, false
	}

	// Check expiration
	if time.Now().After(entry.ExpiresAt) {
		rc.evictEntry(key)
		rc.stats.Misses++
		rc.updateHitRate()
		return nil, false
	}

	// Update access metrics
	entry.AccessCount++
	entry.LastAccessedAt = time.Now()
	rc.stats.Hits++
	rc.updateHitRate()

	return entry.Result, true
}

// Put stores a result in the cache with optional tags.
func (rc *ResultCache) Put(key string, result *ConsolidatedResult, tags ...string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	// Evict if at capacity
	if len(rc.entries) >= rc.maxSize {
		rc.evictLRU()
	}

	now := time.Now()
	entry := &CacheEntry{
		Key:              key,
		Result:           result,
		CreatedAt:        now,
		ExpiresAt:        now.Add(rc.defaultTTL),
		AccessCount:      0,
		LastAccessedAt:   now,
		InvalidationTags: tags,
		Metadata:         make(map[string]string),
	}

	rc.entries[key] = entry

	// Index by tags
	for _, tag := range tags {
		rc.tags[tag] = append(rc.tags[tag], key)
	}

	rc.stats.CurrentSize = len(rc.entries)
}

// Invalidate removes entries with a specific tag.
func (rc *ResultCache) Invalidate(tag string) int {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	keys, exists := rc.tags[tag]
	if !exists {
		return 0
	}

	count := 0
	for _, key := range keys {
		if rc.evictEntry(key) {
			count++
		}
	}

	rc.stats.Invalidations += int64(count)
	delete(rc.tags, tag)
	rc.stats.CurrentSize = len(rc.entries)

	return count
}

// InvalidateByPattern removes entries matching a pattern.
func (rc *ResultCache) InvalidateByPattern(pattern string) int {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	count := 0
	for key := range rc.entries {
		// Simple prefix matching
		if len(key) >= len(pattern) && key[:len(pattern)] == pattern {
			if rc.evictEntry(key) {
				count++
			}
		}
	}

	rc.stats.Invalidations += int64(count)
	rc.stats.CurrentSize = len(rc.entries)

	return count
}

// Clear removes all cached entries.
func (rc *ResultCache) Clear() {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rc.entries = make(map[string]*CacheEntry)
	rc.tags = make(map[string][]string)
	rc.stats.CurrentSize = 0
}

// GetStats returns current cache statistics.
func (rc *ResultCache) GetStats() CacheStats {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	return rc.stats
}

// SetTTL updates the TTL for a cached entry.
func (rc *ResultCache) SetTTL(key string, ttl time.Duration) bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	entry, exists := rc.entries[key]
	if !exists {
		return false
	}

	entry.ExpiresAt = time.Now().Add(ttl)
	return true
}

// Private helper methods

// evictEntry removes an entry from the cache.
func (rc *ResultCache) evictEntry(key string) bool {
	entry, exists := rc.entries[key]
	if !exists {
		return false
	}

	delete(rc.entries, key)
	rc.stats.Evictions++

	// Remove from tag indexes
	for _, tag := range entry.InvalidationTags {
		if keys, exists := rc.tags[tag]; exists {
			for i, k := range keys {
				if k == key {
					rc.tags[tag] = append(keys[:i], keys[i+1:]...)
					break
				}
			}
			if len(rc.tags[tag]) == 0 {
				delete(rc.tags, tag)
			}
		}
	}

	return true
}

// evictLRU removes the least recently used entry.
func (rc *ResultCache) evictLRU() {
	var lruKey string
	var lruTime time.Time

	for key, entry := range rc.entries {
		if lruTime.IsZero() || entry.LastAccessedAt.Before(lruTime) {
			lruKey = key
			lruTime = entry.LastAccessedAt
		}
	}

	if lruKey != "" {
		rc.evictEntry(lruKey)
	}
}

// updateHitRate calculates current hit rate.
func (rc *ResultCache) updateHitRate() {
	total := rc.stats.Hits + rc.stats.Misses
	if total > 0 {
		rc.stats.HitRate = float64(rc.stats.Hits) / float64(total)
	}
}

// backgroundCleanup periodically removes expired entries.
func (rc *ResultCache) backgroundCleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rc.cleanupExpired()
	}
}

// cleanupExpired removes all expired entries.
func (rc *ResultCache) cleanupExpired() {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	now := time.Now()
	for key, entry := range rc.entries {
		if now.After(entry.ExpiresAt) {
			rc.evictEntry(key)
		}
	}

	rc.lastCleanupTime = now
	rc.stats.CurrentSize = len(rc.entries)
}

// InvalidationStrategy defines cache invalidation policies.
type InvalidationStrategy int

const (
	// InvalidateNone: no automatic invalidation
	InvalidateNone InvalidationStrategy = iota
	// InvalidateOnFileChange: invalidate when files change
	InvalidateOnFileChange
	// InvalidateOnAgentUpdate: invalidate when agent definitions change
	InvalidateOnAgentUpdate
	// InvalidateOnRouteChange: invalidate when routing changes
	InvalidateOnRouteChange
	// InvalidateAll: invalidate on any significant change
	InvalidateAll
)

// CachedOrchestrationWorkflow wraps a workflow with caching.
type CachedOrchestrationWorkflow struct {
	workflow        *OrchestrationWorkflow
	cache           *ResultCache
	strategy        InvalidationStrategy
	enableByDefault bool
}

// NewCachedOrchestrationWorkflow creates a cached workflow wrapper.
func NewCachedOrchestrationWorkflow(workflow *OrchestrationWorkflow, cacheSize int, ttl time.Duration, strategy InvalidationStrategy) *CachedOrchestrationWorkflow {
	return &CachedOrchestrationWorkflow{
		workflow:        workflow,
		cache:           NewResultCache(cacheSize, ttl),
		strategy:        strategy,
		enableByDefault: true,
	}
}

// Execute runs the workflow, using cache if available.
func (cw *CachedOrchestrationWorkflow) ExecuteWithContext(workflowCtx context.Context, input *WorkflowInput) (*WorkflowOutput, error) {
	if !cw.enableByDefault {
		return cw.workflow.Execute(workflowCtx, input)
	}

	// Generate cache key
	cacheKey := GenerateCacheKey(input)

	// Check cache
	if cachedResult, found := cw.cache.Get(cacheKey); found {
		// Return cached result wrapped in output
		return &WorkflowOutput{
			ConsolidatedResult: cachedResult,
			Status:             "cached",
		}, nil
	}

	// Execute workflow
	output, err := cw.workflow.Execute(workflowCtx, input)
	if err != nil {
		return output, err
	}

	// Cache result with invalidation tags
	tags := []string{
		fmt.Sprintf("task:%s", input.TaskID),
		fmt.Sprintf("classification:%s", input.Classification),
		"all-results",
	}
	cw.cache.Put(cacheKey, output.ConsolidatedResult, tags...)

	return output, nil
}

// InvalidateTask invalidates cache entries for a specific task.
func (cw *CachedOrchestrationWorkflow) InvalidateTask(taskID string) int {
	return cw.cache.Invalidate(fmt.Sprintf("task:%s", taskID))
}

// InvalidateAll clears the entire cache.
func (cw *CachedOrchestrationWorkflow) InvalidateAll() {
	cw.cache.Clear()
}

// GetCacheStats returns cache performance metrics.
func (cw *CachedOrchestrationWorkflow) GetCacheStats() CacheStats {
	return cw.cache.GetStats()
}
