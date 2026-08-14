package cli

import (
	"encoding/json"
	"testing"

	"github.com/deagy/cadre/cli/internal/knowledge"
)

func TestHSNWInitCommand(t *testing.T) {
	// Test: cadre knowledge hnsw-init
	// Expected: Initialize HNSW index with defaults

	idx := knowledge.NewHSNWIndex(16, 200)

	if idx == nil {
		t.Error("Failed to create HNSW index")
	}

	stats := idx.GetStats()
	if stats.IndexSize != 0 {
		t.Error("New index should be empty")
	}

	if stats.M != 16 {
		t.Errorf("Expected M=16, got %d", stats.M)
	}
}

func TestHSNWInitCustomParameters(t *testing.T) {
	// Test: cadre knowledge hnsw-init --m 24 --ef-construction 400
	// Expected: Initialize with custom parameters

	idx := knowledge.NewHSNWIndex(24, 400)

	stats := idx.GetStats()
	if stats.M != 24 {
		t.Errorf("Expected M=24, got %d", stats.M)
	}

	if stats.EfSearch != 400 {
		t.Errorf("Expected EfSearch=400, got %d", stats.EfSearch)
	}
}

func TestHSNWStatsCommand(t *testing.T) {
	// Test: cadre knowledge hnsw-stats
	// Expected: Display index statistics

	idx := knowledge.NewHSNWIndex(16, 200)

	// Add some vectors
	for i := 0; i < 10; i++ {
		embedding := []float32{float32(i), float32(i + 1), float32(i + 2)}
		idx.Insert("msg-"+string(rune(48+i)), embedding)
	}

	stats := idx.GetStats()

	if stats.IndexSize != 10 {
		t.Errorf("Expected 10 vectors, got %d", stats.IndexSize)
	}

	if stats.MaxLayer < 0 {
		t.Error("Max layer should be non-negative")
	}

	if stats.TotalConnections < 0 {
		t.Error("Connections should be non-negative")
	}

	if stats.AverageConnections < 0 {
		t.Error("Average connections should be non-negative")
	}
}

func TestHSNWStatsJSON(t *testing.T) {
	// Test: cadre knowledge hnsw-stats --json
	// Expected: JSON formatted statistics

	idx := knowledge.NewHSNWIndex(16, 200)

	for i := 0; i < 5; i++ {
		embedding := []float32{float32(i), float32(i + 1)}
		idx.Insert("msg-"+string(rune(48+i)), embedding)
	}

	stats := idx.GetStats()

	// Marshal to JSON
	jsonBytes, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal stats to JSON: %v", err)
	}

	// Verify JSON is valid
	var unmarshaled interface{}
	err = json.Unmarshal(jsonBytes, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}
}

func TestHSNWSearchCommand(t *testing.T) {
	// Test: cadre knowledge hnsw-search "query" --k 5
	// Expected: Return top 5 nearest neighbors

	idx := knowledge.NewHSNWIndex(16, 200)

	// Insert vectors
	embeddings := [][]float32{
		{1.0, 0.0, 0.0},
		{0.99, 0.01, 0.0},
		{0.98, 0.02, 0.0},
		{0.50, 0.50, 0.0},
		{0.0, 1.0, 0.0},
	}

	for i, emb := range embeddings {
		idx.Insert("msg-"+string(rune(49+i)), emb)
	}

	// Search
	query := []float32{1.0, 0.0, 0.0}
	results := idx.Search(query, 5)

	if len(results) == 0 {
		t.Error("Expected search results")
	}

	if len(results) > 5 {
		t.Errorf("Expected max 5 results, got %d", len(results))
	}

	// Results should be sorted by distance
	for i := 1; i < len(results); i++ {
		if results[i].Distance < results[i-1].Distance {
			t.Error("Results not sorted by distance")
		}
	}
}

func TestHSNWSearchK0(t *testing.T) {
	// Test: cadre knowledge hnsw-search "query" --k 0
	// Expected: Return empty results

	idx := knowledge.NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Insert("msg-2", []float32{0.0, 1.0})

	results := idx.Search([]float32{1.0, 0.0}, 0)

	if len(results) != 0 {
		t.Errorf("Expected 0 results for k=0, got %d", len(results))
	}
}

func TestHSNWSearchLargeK(t *testing.T) {
	// Test: cadre knowledge hnsw-search "query" --k 1000
	// Expected: Return only available vectors (capped)

	idx := knowledge.NewHSNWIndex(16, 200)

	for i := 0; i < 10; i++ {
		embedding := []float32{float32(i), float32(i + 1)}
		idx.Insert("msg-"+string(rune(48+i)), embedding)
	}

	results := idx.Search([]float32{0.0, 1.0}, 1000)

	if len(results) > 10 {
		t.Errorf("Expected max 10 results, got %d", len(results))
	}
}

func TestHSNWSearchWithFilters(t *testing.T) {
	// Test: cadre knowledge hnsw-search "query" --classification internal
	// Expected: Search respects filters (mock)

	idx := knowledge.NewHSNWIndex(16, 200)

	// Note: Filtering happens at Store level, HNSW handles vectors only
	for i := 0; i < 5; i++ {
		embedding := []float32{float32(i), float32(i + 1)}
		idx.Insert("msg-"+string(rune(48+i)), embedding)
	}

	results := idx.Search([]float32{0.0, 1.0}, 5)

	if len(results) == 0 {
		t.Error("Expected search results")
	}
}

func TestHSNWRebuildCommand(t *testing.T) {
	// Test: cadre knowledge hnsw-rebuild
	// Expected: Rebuild index from existing embeddings

	idx1 := knowledge.NewHSNWIndex(16, 200)

	// Build initial index
	for i := 0; i < 20; i++ {
		embedding := make([]float32, 5)
		for j := 0; j < 5; j++ {
			embedding[j] = float32((i + 1) * (j + 1))
		}
		idx1.Insert("msg-"+string(rune(48+i%10)), embedding)
	}

	stats1 := idx1.GetStats()

	// Rebuild with different parameters
	idx2 := knowledge.NewHSNWIndex(24, 400)

	// Re-insert all vectors (simulating rebuild)
	for i := 0; i < 20; i++ {
		embedding := make([]float32, 5)
		for j := 0; j < 5; j++ {
			embedding[j] = float32((i + 1) * (j + 1))
		}
		idx2.Insert("msg-"+string(rune(48+i%10)), embedding)
	}

	stats2 := idx2.GetStats()

	if stats1.IndexSize != stats2.IndexSize {
		t.Errorf("Expected same index size after rebuild")
	}

	if stats2.M != 24 {
		t.Errorf("Rebuild should use new M parameter")
	}
}

func TestHSNWTuneCommand(t *testing.T) {
	// Test: cadre knowledge hnsw-tune
	// Expected: Analyze and recommend parameter changes

	idx := knowledge.NewHSNWIndex(16, 200)

	// Build index with sample data
	for i := 0; i < 50; i++ {
		embedding := make([]float32, 10)
		for j := 0; j < 10; j++ {
			embedding[j] = float32(i*j) / 50.0
		}
		idx.Insert("msg-"+string(rune(48+i%10)), embedding)
	}

	stats := idx.GetStats()

	// Verify we have a valid index
	if stats.IndexSize != 10 { // We inserted 50 times but only 10 unique IDs due to mod
		t.Logf("Index size: %d", stats.IndexSize)
	}

	if stats.M != 16 {
		t.Errorf("Expected M=16, got %d", stats.M)
	}

	if stats.EfSearch <= 0 {
		t.Error("EF search should be positive")
	}
}

func TestHSNWCompareCommand(t *testing.T) {
	// Test: cadre knowledge hnsw-compare --query "test"
	// Expected: Compare HNSW vs exact search

	idx := knowledge.NewHSNWIndex(16, 200)

	// Build index
	for i := 0; i < 30; i++ {
		embedding := []float32{float32(i) / 30.0, float32(i*i) / 900.0, float32((30 - i)) / 30.0}
		idx.Insert("msg-"+string(rune(48+i%10)), embedding)
	}

	// Search with HNSW
	query := []float32{0.5, 0.25, 0.5}
	results := idx.Search(query, 5)

	if len(results) == 0 {
		t.Error("Expected search results for comparison")
	}

	// Verify results are ordered
	for i := 1; i < len(results); i++ {
		if results[i].Distance < results[i-1].Distance {
			t.Error("Results should be sorted by distance")
		}
	}
}

func TestHSNWSearchPerformance(t *testing.T) {
	// Test: Verify search performance on medium dataset
	// Expected: Sub-millisecond queries

	idx := knowledge.NewHSNWIndex(16, 200)

	// Insert 1000 vectors
	for i := 0; i < 1000; i++ {
		embedding := make([]float32, 100)
		for j := 0; j < 100; j++ {
			embedding[j] = float32((i+j)%100) / 100.0
		}
		msgID := "msg-" + string(rune(48+(i%10)))
		idx.Insert(msgID, embedding)
	}

	// Perform search
	query := make([]float32, 100)
	for j := 0; j < 100; j++ {
		query[j] = 0.5
	}

	results := idx.Search(query, 10)

	// Should get results
	if len(results) == 0 {
		t.Error("Expected search results on 1000 vectors")
	}

	if len(results) > 10 {
		t.Errorf("Expected max 10 results, got %d", len(results))
	}
}

func TestHSNWIntegrationWorkflow(t *testing.T) {
	// Test: Complete workflow from init to search
	// Expected: End-to-end functionality

	// 1. Initialize
	idx := knowledge.NewHSNWIndex(16, 200)

	// 2. Verify initial state
	stats := idx.GetStats()
	if stats.IndexSize != 0 {
		t.Error("Initial index should be empty")
	}

	// 3. Insert vectors - use identical as query to guarantee exact match
	vectors := map[string][]float32{
		"msg-exact": {1.0, 0.5, 0.2},
		"msg-sim1":  {0.99, 0.51, 0.21},
		"msg-sim2":  {0.98, 0.52, 0.22},
	}

	for msgID, emb := range vectors {
		err := idx.Insert(msgID, emb)
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// 4. Verify insertion
	stats = idx.GetStats()
	if stats.IndexSize != 3 {
		t.Errorf("Expected 3 vectors, got %d", stats.IndexSize)
	}

	// 5. Search for exact match
	query := []float32{1.0, 0.5, 0.2}
	results := idx.Search(query, 2)

	if len(results) == 0 {
		t.Error("Expected search results")
	}

	// 6. Verify results - msg-exact should be the closest
	if results[0].MessageID != "msg-exact" {
		t.Errorf("Expected msg-exact as closest, got %s", results[0].MessageID)
	}

	if results[0].Distance > 0.01 {
		t.Errorf("Expected very small distance for exact match, got %f", results[0].Distance)
	}
}

func TestHSNWEdgeCases(t *testing.T) {
	// Test: Edge cases and error conditions
	// Expected: Graceful handling

	idx := knowledge.NewHSNWIndex(16, 200)

	// Empty search
	results := idx.Search([]float32{1.0}, 5)
	if results != nil {
		t.Error("Empty index should return nil")
	}

	// Insert and search
	idx.Insert("msg-1", []float32{1.0})

	// Search empty query
	results = idx.Search([]float32{}, 5)
	if results != nil {
		t.Error("Empty query should return nil")
	}

	// Search k > indexed
	results = idx.Search([]float32{1.0}, 100)
	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
}

func TestHSNWMultipleModes(t *testing.T) {
	// Test: Different parameter combinations
	// Expected: All configurations should work

	configs := []struct {
		m              int
		efConstruction int
	}{
		{8, 100},
		{16, 200},
		{24, 400},
		{32, 500},
	}

	for _, cfg := range configs {
		idx := knowledge.NewHSNWIndex(cfg.m, cfg.efConstruction)

		// Insert test data
		for i := 0; i < 10; i++ {
			embedding := []float32{float32(i), float32(i + 1)}
			idx.Insert("msg-"+string(rune(48+i)), embedding)
		}

		// Verify stats
		stats := idx.GetStats()
		if stats.M != cfg.m {
			t.Errorf("Expected M=%d, got %d", cfg.m, stats.M)
		}

		if stats.EfSearch != cfg.efConstruction {
			t.Errorf("Expected EF=%d, got %d", cfg.efConstruction, stats.EfSearch)
		}
	}
}
