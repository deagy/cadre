//nolint:errcheck
package knowledge

import (
	"crypto/md5"
	"fmt"
	"sync"
	"time"
)

// QueryCache provides LRU caching for search results.
type QueryCache struct {
	mu              sync.RWMutex
	cache           map[string]*CachedResult
	accessOrder     []*CacheEntry
	maxSize         int
	defaultTTL      time.Duration
	hits            int64
	misses          int64
	evictions       int64
}

// CacheEntry tracks cache entry metadata.
type CacheEntry struct {
	Key          string
	LastAccessed time.Time
}

// CachedResult wraps a search result with metadata.
type CachedResult struct {
	Results       []*SearchResult
	MessageCount  int64
	SearchTimeMs  int64
	CachedAt      time.Time
	ExpiresAt     time.Time
}

// NewQueryCache creates a new query cache.
func NewQueryCache(maxSize int, defaultTTLMinutes int) *QueryCache {
	return &QueryCache{
		cache:      make(map[string]*CachedResult),
		accessOrder: make([]*CacheEntry, 0, maxSize),
		maxSize:    maxSize,
		defaultTTL: time.Duration(defaultTTLMinutes) * time.Minute,
	}
}

// QueryKey generates a cache key from search options.
func (qc *QueryCache) QueryKey(query, classification string, sourceFilters []string) string {
	h := md5.New()
	fmt.Fprintf(h, "%s:%s:", query, classification)
	for _, s := range sourceFilters {
		fmt.Fprintf(h, "%s,", s)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Get retrieves a cached result if not expired.
func (qc *QueryCache) Get(key string) (*CachedResult, bool) {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	result, ok := qc.cache[key]
	if !ok {
		qc.misses++
		return nil, false
	}

	// Check expiration
	if time.Now().After(result.ExpiresAt) {
		qc.mu.Unlock()
		qc.mu.Lock()
		delete(qc.cache, key)
		qc.evictions++
		return nil, false
	}

	// Update access time (evict oldest)
	qc.updateAccessTime(key)
	qc.hits++

	return result, true
}

// Set caches a search result.
func (qc *QueryCache) Set(key string, results []*SearchResult, searchTimeMs int64) {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	// Check if we need to evict
	if len(qc.cache) >= qc.maxSize {
		qc.evictOldest()
	}

	qc.cache[key] = &CachedResult{
		Results:      results,
		MessageCount: int64(len(results)),
		SearchTimeMs: searchTimeMs,
		CachedAt:     time.Now(),
		ExpiresAt:    time.Now().Add(qc.defaultTTL),
	}

	qc.accessOrder = append(qc.accessOrder, &CacheEntry{
		Key:          key,
		LastAccessed: time.Now(),
	})
}

// updateAccessTime marks an entry as recently used.
func (qc *QueryCache) updateAccessTime(key string) {
	for i, entry := range qc.accessOrder {
		if entry.Key == key {
			entry.LastAccessed = time.Now()
			// Move to end (most recently used)
			copy(qc.accessOrder[i:], qc.accessOrder[i+1:])
			qc.accessOrder[len(qc.accessOrder)-1] = entry
			break
		}
	}
}

// evictOldest removes the least recently used entry.
func (qc *QueryCache) evictOldest() {
	if len(qc.accessOrder) == 0 {
		return
	}

	oldest := qc.accessOrder[0]
	delete(qc.cache, oldest.Key)
	qc.accessOrder = qc.accessOrder[1:]
	qc.evictions++
}

// Clear empties the cache.
func (qc *QueryCache) Clear() {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	qc.cache = make(map[string]*CachedResult)
	qc.accessOrder = make([]*CacheEntry, 0, qc.maxSize)
}

// GetStats returns cache statistics.
func (qc *QueryCache) GetStats() *CacheStats {
	qc.mu.RLock()
	defer qc.mu.RUnlock()

	var hitRate float64
	totalRequests := qc.hits + qc.misses
	if totalRequests > 0 {
		hitRate = float64(qc.hits) / float64(totalRequests) * 100
	}

	return &CacheStats{
		Size:        len(qc.cache),
		Hits:        qc.hits,
		Misses:      qc.misses,
		Evictions:   qc.evictions,
		HitRate:     hitRate,
		MaxSize:     qc.maxSize,
		TTLMinutes:  int(qc.defaultTTL.Minutes()),
	}
}

// CacheStats provides cache performance metrics.
type CacheStats struct {
	Size       int
	Hits       int64
	Misses     int64
	Evictions  int64
	HitRate    float64
	MaxSize    int
	TTLMinutes int
}

// PerformanceMetrics tracks query performance across the system.
type PerformanceMetrics struct {
	mu                  sync.RWMutex
	totalQueries        int64
	totalQueryTimeMs    int64
	minQueryTimeMs      int64
	maxQueryTimeMs      int64
	vectorSearchQueries int64
	textSearchQueries   int64
	totalResultsReturned int64
}

// NewPerformanceMetrics creates a new metrics tracker.
func NewPerformanceMetrics() *PerformanceMetrics {
	return &PerformanceMetrics{
		minQueryTimeMs: 999999999,
	}
}

// RecordQuery records a query execution.
func (pm *PerformanceMetrics) RecordQuery(queryTimeMs int64, isVector bool, resultCount int64) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.totalQueries++
	pm.totalQueryTimeMs += queryTimeMs
	pm.totalResultsReturned += resultCount

	if queryTimeMs < pm.minQueryTimeMs {
		pm.minQueryTimeMs = queryTimeMs
	}
	if queryTimeMs > pm.maxQueryTimeMs {
		pm.maxQueryTimeMs = queryTimeMs
	}

	if isVector {
		pm.vectorSearchQueries++
	} else {
		pm.textSearchQueries++
	}
}

// GetMetrics returns current performance metrics.
func (pm *PerformanceMetrics) GetMetrics() *PerformanceStats {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var avgQueryTimeMs float64
	if pm.totalQueries > 0 {
		avgQueryTimeMs = float64(pm.totalQueryTimeMs) / float64(pm.totalQueries)
	}

	return &PerformanceStats{
		TotalQueries:          pm.totalQueries,
		AverageQueryTimeMs:    avgQueryTimeMs,
		MinQueryTimeMs:        pm.minQueryTimeMs,
		MaxQueryTimeMs:        pm.maxQueryTimeMs,
		VectorSearchQueries:   pm.vectorSearchQueries,
		TextSearchQueries:     pm.textSearchQueries,
		TotalResultsReturned:  pm.totalResultsReturned,
	}
}

// PerformanceStats provides performance statistics.
type PerformanceStats struct {
	TotalQueries          int64
	AverageQueryTimeMs    float64
	MinQueryTimeMs        int64
	MaxQueryTimeMs        int64
	VectorSearchQueries   int64
	TextSearchQueries     int64
	TotalResultsReturned  int64
}

// IndexOptimizer analyzes query patterns to suggest optimizations.
type IndexOptimizer struct {
	mu             sync.RWMutex
	metrics        *PerformanceMetrics
	slowQueryMs    int64 // Queries slower than this threshold
	slowQueryCount int64
}

// NewIndexOptimizer creates a new optimization analyzer.
func NewIndexOptimizer(metrics *PerformanceMetrics, slowQueryMs int64) *IndexOptimizer {
	return &IndexOptimizer{
		metrics:     metrics,
		slowQueryMs: slowQueryMs,
	}
}

// AnalyzePerformance checks if queries need optimization.
func (io *IndexOptimizer) AnalyzePerformance() *OptimizationReport {
	io.mu.RLock()
	defer io.mu.RUnlock()

	stats := io.metrics.GetMetrics()

	report := &OptimizationReport{
		TotalQueries: stats.TotalQueries,
		AvgTimeMs:    stats.AverageQueryTimeMs,
	}

	// Check if queries are slow
	if stats.AverageQueryTimeMs > float64(io.slowQueryMs) {
		report.NeedsIndexing = true
		report.Recommendations = append(report.Recommendations,
			fmt.Sprintf("Average query time (%.1fms) exceeds threshold (%dms)", stats.AverageQueryTimeMs, io.slowQueryMs))
	}

	// Check vector search performance
	if stats.VectorSearchQueries > 0 {
		if stats.MaxQueryTimeMs > io.slowQueryMs*2 {
			report.NeedsHSNW = true
			report.Recommendations = append(report.Recommendations,
				"HNSW indexing recommended for vector search performance")
		}
	}

	// Check if caching would help
	if stats.TotalQueries > 1000 {
		report.NeedsCaching = true
		report.Recommendations = append(report.Recommendations,
			"Query result caching recommended (high query volume)")
	}

	return report
}

// OptimizationReport provides optimization recommendations.
type OptimizationReport struct {
	TotalQueries       int64
	AvgTimeMs          float64
	NeedsIndexing      bool
	NeedsHSNW          bool
	NeedsCaching       bool
	Recommendations    []string
}
