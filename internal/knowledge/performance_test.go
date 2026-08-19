package knowledge

import (
	"testing"
	"time"
)

func TestQueryCacheCreation(t *testing.T) {
	cache := NewQueryCache(100, 60)

	if cache.maxSize != 100 {
		t.Errorf("Expected max size 100, got %d", cache.maxSize)
	}

	if cache.defaultTTL != 60*time.Minute {
		t.Errorf("Expected TTL 60m, got %v", cache.defaultTTL)
	}
}

func TestQueryCacheKey(t *testing.T) {
	cache := NewQueryCache(100, 60)

	key1 := cache.QueryKey("test", "general", []string{"app-1", "app-2"})
	key2 := cache.QueryKey("test", "general", []string{"app-1", "app-2"})

	if key1 != key2 {
		t.Error("Same query should produce same key")
	}

	key3 := cache.QueryKey("other", "general", []string{"app-1", "app-2"})
	if key1 == key3 {
		t.Error("Different query should produce different key")
	}
}

func TestQueryCacheSetGet(t *testing.T) {
	cache := NewQueryCache(100, 60)

	results := []*SearchResult{
		{Message: &Message{SourceMessageID: "msg-1"}},
		{Message: &Message{SourceMessageID: "msg-2"}},
	}

	key := cache.QueryKey("test", "general", []string{})

	// Set
	cache.Set(key, results, 10)

	// Get
	cached, ok := cache.Get(key)
	if !ok {
		t.Error("Expected cache hit")
	}

	if cached.MessageCount != 2 {
		t.Errorf("Expected 2 cached messages, got %d", cached.MessageCount)
	}

	if cached.SearchTimeMs != 10 {
		t.Errorf("Expected search time 10ms, got %d", cached.SearchTimeMs)
	}
}

func TestQueryCacheMiss(t *testing.T) {
	cache := NewQueryCache(100, 60)

	_, ok := cache.Get("nonexistent")
	if ok {
		t.Error("Expected cache miss")
	}

	stats := cache.GetStats()
	if stats.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.Misses)
	}
}

func TestQueryCacheHitRate(t *testing.T) {
	cache := NewQueryCache(100, 60)

	results := []*SearchResult{}
	key := cache.QueryKey("test", "general", []string{})

	// Set
	cache.Set(key, results, 5)

	// Multiple gets
	cache.Get(key)
	cache.Get(key)
	cache.Get("nonexistent")

	stats := cache.GetStats()
	if stats.Hits != 2 {
		t.Errorf("Expected 2 hits, got %d", stats.Hits)
	}

	if stats.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.Misses)
	}

	if stats.HitRate < 66 || stats.HitRate > 67 {
		t.Errorf("Expected ~66.7%% hit rate, got %.1f%%", stats.HitRate)
	}
}

func TestQueryCacheExpiration(t *testing.T) {
	cache := NewQueryCache(100, 1) // 1 minute TTL

	results := []*SearchResult{}
	key := cache.QueryKey("test", "general", []string{})

	cache.Set(key, results, 5)

	// Manually expire the result
	cache.mu.Lock()
	cached := cache.cache[key]
	cached.ExpiresAt = time.Now().Add(-1 * time.Second)
	cache.mu.Unlock()

	// Try to get expired result
	_, ok := cache.Get(key)
	if ok {
		t.Error("Expected cache miss for expired result")
	}
}

func TestQueryCacheEviction(t *testing.T) {
	cache := NewQueryCache(3, 60) // Small cache for easy testing

	results := []*SearchResult{}

	// Add 3 items
	for i := 0; i < 3; i++ {
		key := cache.QueryKey(string(rune(48+i)), "general", []string{})
		cache.Set(key, results, int64(i+1))
	}

	stats := cache.GetStats()
	if stats.Size != 3 {
		t.Errorf("Expected cache size 3, got %d", stats.Size)
	}

	// Add 4th item (should evict oldest)
	key4 := cache.QueryKey("3", "general", []string{})
	cache.Set(key4, results, 4)

	stats = cache.GetStats()
	if stats.Size != 3 {
		t.Errorf("Expected cache size 3 after eviction, got %d", stats.Size)
	}

	if stats.Evictions != 1 {
		t.Errorf("Expected 1 eviction, got %d", stats.Evictions)
	}
}

func TestQueryCacheClear(t *testing.T) {
	cache := NewQueryCache(100, 60)

	results := []*SearchResult{}
	key := cache.QueryKey("test", "general", []string{})

	cache.Set(key, results, 5)

	stats := cache.GetStats()
	if stats.Size != 1 {
		t.Errorf("Expected 1 item before clear, got %d", stats.Size)
	}

	cache.Clear()

	stats = cache.GetStats()
	if stats.Size != 0 {
		t.Errorf("Expected 0 items after clear, got %d", stats.Size)
	}
}

func TestPerformanceMetrics(t *testing.T) {
	pm := NewPerformanceMetrics()

	// Record some queries
	pm.RecordQuery(10, true, 5)  // 10ms vector search, 5 results
	pm.RecordQuery(20, false, 3) // 20ms text search, 3 results
	pm.RecordQuery(15, true, 4)  // 15ms vector search, 4 results

	stats := pm.GetMetrics()

	if stats.TotalQueries != 3 {
		t.Errorf("Expected 3 queries, got %d", stats.TotalQueries)
	}

	if stats.VectorSearchQueries != 2 {
		t.Errorf("Expected 2 vector searches, got %d", stats.VectorSearchQueries)
	}

	if stats.TextSearchQueries != 1 {
		t.Errorf("Expected 1 text search, got %d", stats.TextSearchQueries)
	}

	if stats.TotalResultsReturned != 12 {
		t.Errorf("Expected 12 results total, got %d", stats.TotalResultsReturned)
	}

	expectedAvg := (10.0 + 20.0 + 15.0) / 3.0
	if stats.AverageQueryTimeMs != expectedAvg {
		t.Errorf("Expected avg %.1fms, got %.1fms", expectedAvg, stats.AverageQueryTimeMs)
	}

	if stats.MinQueryTimeMs != 10 {
		t.Errorf("Expected min 10ms, got %d", stats.MinQueryTimeMs)
	}

	if stats.MaxQueryTimeMs != 20 {
		t.Errorf("Expected max 20ms, got %d", stats.MaxQueryTimeMs)
	}
}

func TestPerformanceMetricsEmpty(t *testing.T) {
	pm := NewPerformanceMetrics()

	stats := pm.GetMetrics()

	if stats.TotalQueries != 0 {
		t.Errorf("Expected 0 queries initially, got %d", stats.TotalQueries)
	}

	if stats.AverageQueryTimeMs != 0 {
		t.Errorf("Expected 0 avg time, got %.1f", stats.AverageQueryTimeMs)
	}
}

func TestIndexOptimizer(t *testing.T) {
	pm := NewPerformanceMetrics()
	optimizer := NewIndexOptimizer(pm, 50) // 50ms threshold

	// Record slow queries
	pm.RecordQuery(100, true, 5)
	pm.RecordQuery(120, true, 3)

	report := optimizer.AnalyzePerformance()

	if !report.NeedsIndexing {
		t.Error("Should recommend indexing for slow queries")
	}

	if !report.NeedsHSNW {
		t.Error("Should recommend HNSW for slow vector searches")
	}

	if len(report.Recommendations) == 0 {
		t.Error("Expected recommendations")
	}
}

func TestIndexOptimizerCaching(t *testing.T) {
	pm := NewPerformanceMetrics()
	optimizer := NewIndexOptimizer(pm, 50)

	// Record many queries
	for i := 0; i < 1001; i++ {
		pm.RecordQuery(10, true, 5)
	}

	report := optimizer.AnalyzePerformance()

	if !report.NeedsCaching {
		t.Error("Should recommend caching for high query volume")
	}
}

func TestIndexOptimizerFastQueries(t *testing.T) {
	pm := NewPerformanceMetrics()
	optimizer := NewIndexOptimizer(pm, 50)

	// Record fast queries
	pm.RecordQuery(5, true, 3)
	pm.RecordQuery(8, true, 2)
	pm.RecordQuery(10, true, 4)

	report := optimizer.AnalyzePerformance()

	if report.NeedsIndexing {
		t.Error("Should not recommend indexing for fast queries")
	}

	if report.NeedsHSNW {
		t.Error("Should not recommend HNSW for fast queries")
	}
}

// Two different (query, classification) pairs must never share a cache key.
//
// The original key joined the fields with ":", so QueryKey("a", "b:c", nil) and
// QueryKey("a:b", "c", nil) collided. classification is an access-control
// boundary in this store: a collision across it would serve one
// classification's cached results to a query made at another. The same applies
// to the source list, where "a,b" as one filter joined identically to "a" and
// "b" as two.
func TestQueryKeyDoesNotCollideAcrossFieldBoundaries(t *testing.T) {
	cache := NewQueryCache(10, 5)

	collisions := []struct {
		name string
		a, b string
	}{
		{"query absorbs the classification",
			cache.QueryKey("a", "b:c", nil),
			cache.QueryKey("a:b", "c", nil)},
		{"query absorbs an empty classification",
			cache.QueryKey("a:b", "", nil),
			cache.QueryKey("a", "b", nil)},
	}
	for _, c := range collisions {
		if c.a == c.b {
			t.Errorf("%s: distinct inputs share cache key %s", c.name, c.a)
		}
	}

	if one, two := cache.QueryKey("q", "c", []string{"a,b"}), cache.QueryKey("q", "c", []string{"a", "b"}); one == two {
		t.Errorf("one source filter %q and two %q share cache key %s", "a,b", []string{"a", "b"}, one)
	}

	// Still stable for equal inputs, which is the point of a cache.
	if one, two := cache.QueryKey("q", "c", []string{"a"}), cache.QueryKey("q", "c", []string{"a"}); one != two {
		t.Errorf("equal inputs produced different keys: %s != %s", one, two)
	}
}
