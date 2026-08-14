// embedding.go: offline, deterministic embedding and similarity. Pure
// functions with no network, no credentials, and no configuration beyond a
// dimension count -- the openai-compatible provider deliberately did not
// move here; see this package's doc comment.
package textutil

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unicode"
)

// NormalizeVector L2-normalizes vector, returning an error if any element
// is non-finite.
func NormalizeVector(vector []float64) ([]float64, error) {
	for _, v := range vector {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("embedding vector must contain only finite numbers")
		}
	}
	var sumSquares float64
	for _, v := range vector {
		sumSquares += v * v
	}
	magnitude := math.Sqrt(sumSquares)
	out := make([]float64, len(vector))
	if magnitude == 0 {
		copy(out, vector)
		return out, nil
	}
	for i, v := range vector {
		out[i] = v / magnitude
	}
	return out, nil
}

// Tokens splits text into lowercase word tokens, treating any Unicode
// letter, digit, underscore, or hyphen as a word character and everything
// else as a separator.
func Tokens(text string) []string {
	var words []string
	var current strings.Builder
	for _, r := range strings.ToLower(text) {
		if r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsNumber(r) {
			current.WriteRune(r)
		} else if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

// HashingEmbedding is a deterministic, offline feature-hashing vector.
// Approximates lexical similarity rather than full semantic similarity --
// good enough to find the entry you half-remember, not a substitute for a
// real embedding model.
func HashingEmbedding(text string, dimensions int) ([]float64, error) {
	words := Tokens(text)
	features := make([]string, 0, len(words)*2)
	features = append(features, words...)
	for i := 0; i+1 < len(words); i++ {
		features = append(features, words[i]+"::"+words[i+1])
	}

	vector := make([]float64, dimensions)
	for _, feature := range features {
		digest := sha256.Sum256([]byte(feature))
		position := binary.LittleEndian.Uint32(digest[0:4]) % uint32(dimensions)
		sign := 1.0
		if binary.LittleEndian.Uint32(digest[4:8])%2 != 0 {
			sign = -1.0
		}
		vector[position] += sign
	}
	return NormalizeVector(vector)
}

// CosineSimilarity returns the dot product of left and right (both assumed
// already L2-normalized, matching the Python original's contract), or
// negative infinity if the vectors have mismatched lengths or right
// contains a non-finite value.
func CosineSimilarity(left, right []float64) float64 {
	if len(left) != len(right) {
		return math.Inf(-1)
	}
	for _, v := range right {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return math.Inf(-1)
		}
	}
	var sum float64
	for i := range left {
		sum += left[i] * right[i]
	}
	return sum
}
