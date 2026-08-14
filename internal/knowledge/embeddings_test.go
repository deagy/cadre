package knowledge

import (
	"math"
	"testing"
)

func TestLocalHashingEmbedder(t *testing.T) {
	embedder := NewLocalHashingEmbedder(128)

	if embedder.Name() != "local-hashing" {
		t.Errorf("Expected name 'local-hashing', got %s", embedder.Name())
	}

	if embedder.Dimensions() != 128 {
		t.Errorf("Expected dimensions 128, got %d", embedder.Dimensions())
	}
}

func TestEmbedDeterministic(t *testing.T) {
	embedder := NewLocalHashingEmbedder(128)

	text := "hello world"
	vec1, err := embedder.Embed([]string{text})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	vec2, err := embedder.Embed([]string{text})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	// Embeddings should be identical
	if len(vec1) != len(vec2) {
		t.Fatalf("Length mismatch: %d vs %d", len(vec1), len(vec2))
	}

	for i := range vec1[0] {
		if vec1[0][i] != vec2[0][i] {
			t.Errorf("Embeddings differ at index %d: %f vs %f", i, vec1[0][i], vec2[0][i])
		}
	}
}

func TestNormalization(t *testing.T) {
	embedder := NewLocalHashingEmbedder(128)
	vec, err := embedder.Embed([]string{"test"})
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	// Check that embedding is normalized (L2 norm = 1)
	var norm float64
	for _, v := range vec[0] {
		norm += v * v
	}
	norm = math.Sqrt(norm)

	if math.Abs(norm-1.0) > 0.0001 {
		t.Errorf("Expected norm 1.0, got %f", norm)
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		a, b     []float64
		expected float64
	}{
		{
			// Identical vectors
			[]float64{1, 0, 0},
			[]float64{1, 0, 0},
			1.0,
		},
		{
			// Orthogonal vectors
			[]float64{1, 0},
			[]float64{0, 1},
			0.0,
		},
		{
			// Opposite vectors
			[]float64{1, 0},
			[]float64{-1, 0},
			-1.0,
		},
	}

	for i, tt := range tests {
		got := CosineSimilarity(tt.a, tt.b)
		if math.Abs(got-tt.expected) > 0.0001 {
			t.Errorf("Test %d: expected %f, got %f", i, tt.expected, got)
		}
	}
}

func TestSearchIndex(t *testing.T) {
	idx := NewSearchIndex()

	// Add some vectors
	idx.Add("a", []float64{1, 0, 0})
	idx.Add("b", []float64{0.9, 0.1, 0})
	idx.Add("c", []float64{0, 1, 0})

	// Search for vector similar to "a"
	results := idx.Search([]float64{1, 0, 0}, 2)

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}

	if results[0].ID != "a" {
		t.Errorf("Expected first result 'a', got %s", results[0].ID)
	}

	if math.Abs(results[0].Similarity-1.0) > 0.0001 {
		t.Errorf("Expected similarity 1.0 for identical vector, got %f", results[0].Similarity)
	}
}

func TestVectorSerialization(t *testing.T) {
	original := []float64{0.1, 0.2, 0.3}

	jsonStr := VectorToJSON(original)
	recovered, err := JSONToVector(jsonStr)

	if err != nil {
		t.Fatalf("JSONToVector failed: %v", err)
	}

	if len(original) != len(recovered) {
		t.Errorf("Length mismatch: %d vs %d", len(original), len(recovered))
	}

	for i := range original {
		if math.Abs(original[i]-recovered[i]) > 0.0001 {
			t.Errorf("Value mismatch at index %d: %f vs %f", i, original[i], recovered[i])
		}
	}
}
