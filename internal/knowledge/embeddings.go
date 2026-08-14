package knowledge

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
)

// LocalHashingEmbedder implements EmbeddingProvider using deterministic feature hashing.
// This is a lightweight, offline embedding suitable for local retrieval. It provides
// deterministic embeddings for development/testing and is compatible with the Python
// `text_embedding.hashing_embedding()` implementation.
type LocalHashingEmbedder struct {
	dimensions int
}

// NewLocalHashingEmbedder creates a new local hashing embedder with given dimensions.
// Default is 128 for compatibility with Python implementation.
func NewLocalHashingEmbedder(dimensions int) *LocalHashingEmbedder {
	if dimensions <= 0 {
		dimensions = 128
	}
	return &LocalHashingEmbedder{dimensions: dimensions}
}

// Name returns the provider identifier.
func (l *LocalHashingEmbedder) Name() string {
	return "local-hashing"
}

// Model returns the model identifier.
func (l *LocalHashingEmbedder) Model() string {
	return fmt.Sprintf("fnv1a-d%d", l.dimensions)
}

// Dimensions returns the vector dimension size.
func (l *LocalHashingEmbedder) Dimensions() int {
	return l.dimensions
}

// Embed computes embeddings for the given texts using feature hashing.
// Returns vectors in parallel order with input texts.
func (l *LocalHashingEmbedder) Embed(texts []string) ([][]float64, error) {
	result := make([][]float64, len(texts))
	for i, text := range texts {
		result[i] = l.hashEmbed(text)
	}
	return result, nil
}

// hashEmbed computes a single hash-based embedding for a text string.
// Uses FNV-1a hash function to deterministically map text to a feature vector.
func (l *LocalHashingEmbedder) hashEmbed(text string) []float64 {
	// Initialize feature vector
	features := make([]float64, l.dimensions)

	// Hash-based feature generation: each token contributes to multiple hash buckets
	tokens := tokenize(text)
	for _, token := range tokens {
		// Hash the token to multiple buckets
		for offset := 0; offset < 3; offset++ {
			h := fnv1a(token + fmt.Sprintf("_%d", offset))
			bucketIdx := int(h % uint64(l.dimensions))
			features[bucketIdx] += 1.0
		}
	}

	// Normalize vector to unit length (L2 norm)
	return normalize(features)
}

// tokenize splits text into tokens (simple whitespace-based tokenization).
func tokenize(text string) []string {
	// In production, use a proper tokenizer. This is simplified for foundation.
	// Python uses text_embedding.tokens() which provides similar behavior.
	tokens := make([]string, 0)
	word := ""
	for _, ch := range text {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			word += string(ch)
		} else if word != "" {
			tokens = append(tokens, word)
			word = ""
		}
	}
	if word != "" {
		tokens = append(tokens, word)
	}
	return tokens
}

// fnv1a computes FNV-1a hash of a string.
func fnv1a(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// normalize returns a copy of the vector normalized to unit length (L2 norm).
func normalize(v []float64) []float64 {
	result := make([]float64, len(v))
	copy(result, v)

	// Compute L2 norm
	var norm float64
	for _, val := range result {
		norm += val * val
	}
	norm = math.Sqrt(norm)

	// Avoid division by zero
	if norm == 0 {
		norm = 1
	}

	// Normalize
	for i := range result {
		result[i] /= norm
	}

	return result
}

// CosineSimilarity computes the cosine similarity between two vectors.
// Returns a value in [-1, 1] where 1 is identical and -1 is opposite.
// Returns 0 for zero vectors.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	normA = math.Sqrt(normA)
	normB = math.Sqrt(normB)

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (normA * normB)
}

// VectorToJSON serializes a vector to JSON.
func VectorToJSON(v []float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// JSONToVector deserializes a vector from JSON.
func JSONToVector(s string) ([]float64, error) {
	var v []float64
	err := json.Unmarshal([]byte(s), &v)
	return v, err
}

// SearchIndex provides efficient cosine similarity search over vectors.
type SearchIndex struct {
	vectors map[string][]float64 // ID -> vector
	ids     []string             // Sorted IDs for stable ordering
}

// NewSearchIndex creates a new search index.
func NewSearchIndex() *SearchIndex {
	return &SearchIndex{
		vectors: make(map[string][]float64),
		ids:     make([]string, 0),
	}
}

// Add adds a vector to the index.
func (s *SearchIndex) Add(id string, vector []float64) {
	if _, exists := s.vectors[id]; !exists {
		s.ids = append(s.ids, id)
		sort.Strings(s.ids)
	}
	s.vectors[id] = vector
}

// Search finds the top-k most similar vectors to the query.
// Returns results sorted by similarity (highest first).
func (s *SearchIndex) Search(query []float64, k int) []struct {
	ID         string
	Similarity float64
} {
	type result struct {
		id         string
		similarity float64
	}

	results := make([]result, 0, len(s.vectors))
	for _, id := range s.ids {
		sim := CosineSimilarity(query, s.vectors[id])
		results = append(results, result{id, sim})
	}

	// Sort by similarity (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].similarity > results[j].similarity
	})

	// Limit to top k
	if k > 0 && len(results) > k {
		results = results[:k]
	}

	// Convert to output format
	output := make([]struct {
		ID         string
		Similarity float64
	}, len(results))
	for i, r := range results {
		output[i].ID = r.id
		output[i].Similarity = r.similarity
	}

	return output
}
