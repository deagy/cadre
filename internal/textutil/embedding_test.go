package textutil

import (
	"math"
	"testing"
)

func TestNormalizeVector(t *testing.T) {
	v, err := NormalizeVector([]float64{3, 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(v[0]-0.6) > 1e-9 || math.Abs(v[1]-0.8) > 1e-9 {
		t.Errorf("v = %v, want [0.6, 0.8]", v)
	}
}

func TestNormalizeVectorZeroMagnitude(t *testing.T) {
	v, err := NormalizeVector([]float64{0, 0, 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v[0] != 0 || v[1] != 0 || v[2] != 0 {
		t.Errorf("v = %v, want unchanged zero vector", v)
	}
}

func TestNormalizeVectorRejectsNonFinite(t *testing.T) {
	if _, err := NormalizeVector([]float64{1, math.NaN()}); err == nil {
		t.Error("expected an error for NaN")
	}
	if _, err := NormalizeVector([]float64{1, math.Inf(1)}); err == nil {
		t.Error("expected an error for +Inf")
	}
}

func TestTokens(t *testing.T) {
	toks := Tokens("Hello, World! foo_bar-baz 123")
	want := []string{"hello", "world", "foo_bar-baz", "123"}
	if len(toks) != len(want) {
		t.Fatalf("toks = %v, want %v", toks, want)
	}
	for i := range want {
		if toks[i] != want[i] {
			t.Errorf("toks[%d] = %q, want %q", i, toks[i], want[i])
		}
	}
}

func TestTokensEmpty(t *testing.T) {
	toks := Tokens("   !!! ,,, ")
	if len(toks) != 0 {
		t.Errorf("toks = %v, want none", toks)
	}
}

func TestHashingEmbeddingDeterministic(t *testing.T) {
	v1, err := HashingEmbedding("the quick brown fox", 64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v2, err := HashingEmbedding("the quick brown fox", 64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("HashingEmbedding is not deterministic at index %d: %v != %v", i, v1[i], v2[i])
		}
	}
}

func TestHashingEmbeddingDimensions(t *testing.T) {
	v, err := HashingEmbedding("some text here", 32)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(v) != 32 {
		t.Errorf("len(v) = %d, want 32", len(v))
	}
}

func TestHashingEmbeddingDifferentTextsDiffer(t *testing.T) {
	v1, _ := HashingEmbedding("apples and oranges", 64)
	v2, _ := HashingEmbedding("quantum computing research", 64)
	same := true
	for i := range v1 {
		if v1[i] != v2[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("expected different texts to produce different embeddings")
	}
}

func TestCosineSimilaritySelf(t *testing.T) {
	v, _ := HashingEmbedding("the quick brown fox jumps", 64)
	sim := CosineSimilarity(v, v)
	if math.Abs(sim-1.0) > 1e-9 {
		t.Errorf("self-similarity = %v, want ~1.0", sim)
	}
}

func TestCosineSimilarityMismatchedLength(t *testing.T) {
	sim := CosineSimilarity([]float64{1, 2}, []float64{1, 2, 3})
	if !math.IsInf(sim, -1) {
		t.Errorf("sim = %v, want -Inf for mismatched lengths", sim)
	}
}

func TestCosineSimilarityRelatedTextsScoreHigherThanUnrelated(t *testing.T) {
	base, _ := HashingEmbedding("deploying kubernetes clusters to production", 128)
	related, _ := HashingEmbedding("kubernetes cluster deployment in production", 128)
	unrelated, _ := HashingEmbedding("a recipe for chocolate chip cookies", 128)

	relatedScore := CosineSimilarity(base, related)
	unrelatedScore := CosineSimilarity(base, unrelated)
	if relatedScore <= unrelatedScore {
		t.Errorf("related score (%v) should exceed unrelated score (%v)", relatedScore, unrelatedScore)
	}
}
