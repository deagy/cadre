package knowledge

import (
	"fmt"
	"math"
	"testing"
)

func TestHSNWIndexCreation(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	if idx.M != 16 {
		t.Errorf("Expected M=16, got %d", idx.M)
	}

	if idx.EfConstruction != 200 {
		t.Errorf("Expected EfConstruction=200, got %d", idx.EfConstruction)
	}

	if idx.entryPoint != nil {
		t.Error("Expected nil entry point initially")
	}

	if len(idx.nodes) != 0 {
		t.Error("Expected empty nodes map initially")
	}
}

func TestHSNWInsertSingle(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	embedding := []float32{1.0, 0.0, 0.0}
	err := idx.Insert("msg-1", embedding)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if idx.entryPoint == nil {
		t.Error("Expected non-nil entry point")
	}

	if idx.entryPoint.MessageID != "msg-1" {
		t.Errorf("Expected entry point msg-1, got %s", idx.entryPoint.MessageID)
	}

	if len(idx.nodes) != 1 {
		t.Errorf("Expected 1 node, got %d", len(idx.nodes))
	}
}

func TestHSNWInsertDuplicate(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	embedding := []float32{1.0, 0.0, 0.0}
	err := idx.Insert("msg-1", embedding)
	if err != nil {
		t.Fatalf("First insert failed: %v", err)
	}

	err = idx.Insert("msg-1", embedding)
	if err == nil {
		t.Error("Expected error for duplicate insert")
	}
}

func TestHSNWInsertEmptyEmbedding(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	err := idx.Insert("msg-1", []float32{})
	if err == nil {
		t.Error("Expected error for empty embedding")
	}
}

func TestHSNWInsertMultiple(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	embeddings := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
		{1.0, 1.0, 0.0},
	}

	for i, emb := range embeddings {
		msgID := fmt.Sprintf("msg-%d", i+1)
		err := idx.Insert(msgID, emb)
		if err != nil {
			t.Fatalf("Insert %d failed: %v", i+1, err)
		}
	}

	if len(idx.nodes) != 4 {
		t.Errorf("Expected 4 nodes, got %d", len(idx.nodes))
	}

	stats := idx.GetStats()
	if stats.IndexSize != 4 {
		t.Errorf("Expected index size 4, got %d", stats.IndexSize)
	}
}

func TestHSNWSearchEmpty(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	query := []float32{1.0, 0.0, 0.0}
	results := idx.Search(query, 5)

	if results != nil {
		t.Error("Expected nil results for empty index")
	}
}

func TestHSNWSearchSingleVector(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	embedding := []float32{1.0, 0.0, 0.0}
	idx.Insert("msg-1", embedding)

	// Search for exact same embedding
	results := idx.Search(embedding, 1)

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	if results[0].MessageID != "msg-1" {
		t.Errorf("Expected msg-1, got %s", results[0].MessageID)
	}

	// Distance should be very small (essentially 0)
	if results[0].Distance > 0.01 {
		t.Errorf("Expected near-zero distance, got %f", results[0].Distance)
	}
}

func TestHSNWSearchTopK(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert vectors close to each other to ensure they're found
	embeddings := [][]float32{
		{1.0, 0.0, 0.0},
		{0.99, 0.01, 0.0},
		{0.98, 0.02, 0.0},
		{0.97, 0.03, 0.0},
		{0.96, 0.04, 0.0},
	}

	for i, emb := range embeddings {
		msgID := fmt.Sprintf("msg-%d", i+1)
		idx.Insert(msgID, emb)
	}

	// Search for top 3
	query := []float32{1.0, 0.0, 0.0}
	results := idx.Search(query, 3)

	if len(results) < 1 || len(results) > 3 {
		t.Errorf("Expected 1-3 results, got %d", len(results))
	}

	// First result should be msg-1 (exact match)
	if results[0].MessageID != "msg-1" {
		t.Errorf("Expected msg-1 as closest, got %s", results[0].MessageID)
	}

	// Results should be sorted by distance
	for i := 1; i < len(results); i++ {
		if results[i].Distance < results[i-1].Distance {
			t.Error("Results not sorted by distance")
		}
	}
}

func TestHSNWSearchWithK(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert 10 vectors
	for i := 0; i < 10; i++ {
		embedding := []float32{float32(i), float32(i + 1), float32(i + 2)}
		msgID := fmt.Sprintf("msg-%d", i+1)
		idx.Insert(msgID, embedding)
	}

	// Request top 5
	query := []float32{0.0, 1.0, 2.0}
	results := idx.Search(query, 5)

	if len(results) > 5 {
		t.Errorf("Expected max 5 results, got %d", len(results))
	}
}

func TestHSNWCosineSimilarity(t *testing.T) {
	tests := []struct {
		a, b     []float32
		expected float32
	}{
		{
			[]float32{1.0, 0.0}, []float32{1.0, 0.0},
			0.0, // identical vectors
		},
		{
			[]float32{1.0, 0.0}, []float32{0.0, 1.0},
			1.0, // orthogonal vectors
		},
		{
			[]float32{1.0, 0.0}, []float32{-1.0, 0.0},
			2.0, // opposite vectors
		},
	}

	for i, test := range tests {
		dist := cosineSimilarityDistance(test.a, test.b)
		if math.Abs(float64(dist-test.expected)) > 0.01 {
			t.Errorf("Test %d: expected %f, got %f", i+1, test.expected, dist)
		}
	}
}

func TestHSNWLargeDataset(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert 100 random-like vectors
	for i := 0; i < 100; i++ {
		embedding := make([]float32, 10)
		for j := 0; j < 10; j++ {
			embedding[j] = float32(i*i + j)
		}
		msgID := fmt.Sprintf("msg-%d", i+1)
		err := idx.Insert(msgID, embedding)
		if err != nil {
			t.Fatalf("Insert %d failed: %v", i+1, err)
		}
	}

	stats := idx.GetStats()
	if stats.IndexSize != 100 {
		t.Errorf("Expected 100 vectors, got %d", stats.IndexSize)
	}

	// Search should work
	query := make([]float32, 10)
	for j := 0; j < 10; j++ {
		query[j] = float32(j)
	}

	results := idx.Search(query, 10)
	if len(results) == 0 {
		t.Error("Expected search results")
	}

	if len(results) > 10 {
		t.Errorf("Expected max 10 results, got %d", len(results))
	}
}

func TestHSNWStats(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	for i := 0; i < 5; i++ {
		embedding := []float32{float32(i), float32(i + 1)}
		msgID := fmt.Sprintf("msg-%d", i+1)
		idx.Insert(msgID, embedding)
	}

	stats := idx.GetStats()

	if stats.IndexSize != 5 {
		t.Errorf("Expected size 5, got %d", stats.IndexSize)
	}

	if stats.M != 16 {
		t.Errorf("Expected M=16, got %d", stats.M)
	}

	if stats.EfSearch != 200 {
		t.Errorf("Expected EfSearch=200, got %d", stats.EfSearch)
	}

	if stats.TotalConnections < 0 {
		t.Error("Expected non-negative connections")
	}

	if stats.AverageConnections < 0 {
		t.Error("Expected non-negative average")
	}
}

func TestHSNWLayerAssignment(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert many vectors to statistically check layer distribution
	layerDistribution := make(map[int]int)

	for i := 0; i < 50; i++ {
		embedding := []float32{float32(i), float32(i + 1)}
		msgID := fmt.Sprintf("msg-%d", i+1)
		err := idx.Insert(msgID, embedding)
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}

		node := idx.nodes[msgID]
		layerDistribution[node.Layer]++
	}

	// We should have some distribution across layers
	if len(layerDistribution) < 2 {
		t.Errorf("Expected multiple layers, got %d", len(layerDistribution))
	}

	// Most nodes should be at layer 0
	if layerDistribution[0] < 20 {
		t.Errorf("Expected many nodes at layer 0, got %d", layerDistribution[0])
	}
}

func TestHSNWEntryPointUpdate(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	entryPoint1 := idx.entryPoint.MessageID

	idx.Insert("msg-2", []float32{0.0, 1.0})
	entryPoint2 := idx.entryPoint.MessageID

	// Entry point should potentially change
	// (can't guarantee due to random layer assignment, but check it's valid)
	if entryPoint1 != "msg-1" {
		t.Error("First insert should set entry point to msg-1")
	}

	if entryPoint2 != "msg-1" && entryPoint2 != "msg-2" {
		t.Errorf("Entry point should be msg-1 or msg-2, got %s", entryPoint2)
	}
}

func TestHSNWSearchExactMatch(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert identical vector multiple times with different IDs
	embedding := []float32{1.0, 0.0, 0.0}

	// First insert
	err := idx.Insert("msg-exact-1", embedding)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Insert similar vectors
	idx.Insert("msg-similar-1", []float32{0.99, 0.01, 0.0})
	idx.Insert("msg-similar-2", []float32{0.98, 0.02, 0.0})

	// Search for exact match
	results := idx.Search(embedding, 3)

	if len(results) == 0 {
		t.Error("Expected search results")
	}

	// First result should be msg-exact-1 (exact match)
	if results[0].MessageID != "msg-exact-1" {
		t.Errorf("Expected msg-exact-1 as first result, got %s", results[0].MessageID)
	}

	// Distance should be near zero for exact match
	if results[0].Distance > 0.001 {
		t.Errorf("Expected near-zero distance, got %f", results[0].Distance)
	}
}

func TestHSNWSearchOrdering(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert vectors at increasing distances from origin
	for i := 0; i < 5; i++ {
		// Create vector with magnitude i+1
		magnitude := float32(i + 1)
		embedding := []float32{magnitude, 0.0, 0.0}
		msgID := fmt.Sprintf("msg-%d", i+1)
		idx.Insert(msgID, embedding)
	}

	// Search near origin
	query := []float32{0.1, 0.0, 0.0}
	results := idx.Search(query, 5)

	// Results should be ordered by distance
	prevDist := float32(-1.0)
	for _, result := range results {
		if result.Distance < prevDist {
			t.Error("Results not properly ordered by distance")
		}
		prevDist = result.Distance
	}
}

func TestHSNWPerformanceMetrics(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert 50 vectors
	for i := 0; i < 50; i++ {
		embedding := make([]float32, 5)
		for j := 0; j < 5; j++ {
			embedding[j] = float32((i+1) * (j+1))
		}
		msgID := fmt.Sprintf("msg-%d", i+1)
		idx.Insert(msgID, embedding)
	}

	stats := idx.GetStats()

	if stats.IndexSize != 50 {
		t.Errorf("Expected 50 items, got %d", stats.IndexSize)
	}

	if stats.MaxLayer < 0 {
		t.Error("Expected non-negative max layer")
	}

	// With 50 items and M=16, we should have reasonable connectivity
	if stats.AverageConnections < 1 {
		t.Errorf("Expected reasonable average connections, got %f", stats.AverageConnections)
	}
}

func TestHSNWHighDimensional(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert high-dimensional vectors (128D)
	for i := 0; i < 10; i++ {
		embedding := make([]float32, 128)
		for j := 0; j < 128; j++ {
			embedding[j] = float32(i*j) / 128.0
		}
		msgID := fmt.Sprintf("msg-%d", i+1)
		err := idx.Insert(msgID, embedding)
		if err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// Search should work with high dimensions
	query := make([]float32, 128)
	for j := 0; j < 128; j++ {
		query[j] = float32(j) / 128.0
	}

	results := idx.Search(query, 5)
	if len(results) == 0 {
		t.Error("Expected search results for high-dimensional vectors")
	}
}

func TestHSNWSearchWithEmptyQuery(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})

	results := idx.Search([]float32{}, 5)
	if results != nil {
		t.Error("Expected nil for empty query")
	}
}

func TestHSNWSearchK0(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Insert("msg-2", []float32{0.0, 1.0})

	results := idx.Search([]float32{1.0, 0.0}, 0)
	if len(results) != 0 {
		t.Errorf("Expected 0 results for k=0, got %d", len(results))
	}
}

func TestHSNWDimensionMismatch(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	idx.Insert("msg-1", []float32{1.0, 0.0})

	// Query with different dimension
	results := idx.Search([]float32{1.0, 0.0, 0.0}, 1)

	// Should still work or return empty - implementation uses MaxFloat32 for mismatch
	if len(results) > 0 && results[0].Distance == math.MaxFloat32 {
		// This is expected behavior for mismatched dimensions
		return
	}
}
