package textutil

import "testing"

func TestChunkTextShortTextSingleChunk(t *testing.T) {
	chunks := ChunkText("short text", ChunkConfig{MaxCharacters: 100, OverlapCharacters: 10})
	if len(chunks) != 1 || chunks[0] != "short text" {
		t.Errorf("chunks = %v", chunks)
	}
}

func TestChunkTextSplitsOnParagraphBoundary(t *testing.T) {
	text := "First paragraph is here and reasonably long to fill space.\n\nSecond paragraph continues on from there with more words."
	chunks := ChunkText(text, ChunkConfig{MaxCharacters: 80, OverlapCharacters: 5})
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d: %v", len(chunks), chunks)
	}
}

func TestChunkTextHardCutFallback(t *testing.T) {
	// A single unbroken block with no boundary at all -- must still chunk
	// via a hard cut, not return one oversized piece.
	text := ""
	for i := 0; i < 200; i++ {
		text += "x"
	}
	chunks := ChunkText(text, ChunkConfig{MaxCharacters: 50, OverlapCharacters: 5})
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks from a hard cut, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len(c) > 50 {
			t.Errorf("chunk exceeds max_characters: len=%d", len(c))
		}
	}
}

func TestChunkTextProgressGuaranteed(t *testing.T) {
	// Regression guard: even with overlap >= max_characters (a
	// pathological config), chunking must terminate.
	text := ""
	for i := 0; i < 500; i++ {
		text += "a"
	}
	chunks := ChunkText(text, ChunkConfig{MaxCharacters: 20, OverlapCharacters: 25})
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
}

func TestChunkTextEmptyString(t *testing.T) {
	chunks := ChunkText("", ChunkConfig{MaxCharacters: 100, OverlapCharacters: 10})
	if len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("chunks = %v", chunks)
	}
}
