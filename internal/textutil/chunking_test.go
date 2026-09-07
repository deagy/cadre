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

// TestChunkTextMultiByteUTF8Boundary proves that chunking text containing
// multi-byte UTF-8 characters (accented Latin, CJK, emoji) produces valid UTF-8
// chunks with no corruption, even at boundaries that would previously have
// split mid-character due to byte-based indexing.
func TestChunkTextMultiByteUTF8Boundary(t *testing.T) {
	// Create text with CJK characters (each is 3 bytes in UTF-8)
	// and a hard boundary that forces a cut in the middle
	cjkText := "这是一些中文文本" // Each character is 3 bytes, 6 characters total = 18 bytes
	// Create a long string with CJK that will trigger chunking
	longCJKText := ""
	for i := 0; i < 10; i++ {
		longCJKText += cjkText + " "
	}

	chunks := ChunkText(longCJKText, ChunkConfig{MaxCharacters: 25, OverlapCharacters: 3})

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Verify every chunk is valid UTF-8
	for i, chunk := range chunks {
		if !isValidUTF8(chunk) {
			t.Errorf("chunk %d is not valid UTF-8: %q", i, chunk)
		}
	}
}

// TestChunkTextAccentedCharacters proves that chunking text with accented
// characters produces valid output.
func TestChunkTextAccentedCharacters(t *testing.T) {
	// Create text with accented characters
	accentedText := "Café résumé naïve cliché "
	longAccentedText := ""
	for i := 0; i < 50; i++ {
		longAccentedText += accentedText
	}

	chunks := ChunkText(longAccentedText, ChunkConfig{MaxCharacters: 40, OverlapCharacters: 5})

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Verify every chunk is valid UTF-8
	for i, chunk := range chunks {
		if !isValidUTF8(chunk) {
			t.Errorf("chunk %d is not valid UTF-8: %q", i, chunk)
		}
	}
}

// TestChunkTextEmojiCharacters proves that chunking text with emoji produces valid output.
func TestChunkTextEmojiCharacters(t *testing.T) {
	// Create text with emoji (4 bytes each in UTF-8)
	emojiText := "Hello 🚀 world 🎉 test 🌟 emoji"
	longEmojiText := ""
	for i := 0; i < 20; i++ {
		longEmojiText += emojiText + "\n"
	}

	chunks := ChunkText(longEmojiText, ChunkConfig{MaxCharacters: 30, OverlapCharacters: 3})

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Verify every chunk is valid UTF-8
	for i, chunk := range chunks {
		if !isValidUTF8(chunk) {
			t.Errorf("chunk %d is not valid UTF-8: %q", i, chunk)
		}
	}
}

// TestChunkTextMaxCharactersIsRunes proves that MaxCharacters counts runes,
// not bytes. A chunk with many multi-byte characters should not exceed
// MaxCharacters in rune count.
func TestChunkTextMaxCharactersIsRunes(t *testing.T) {
	// Each emoji is 1 rune but 4 bytes; MaxCharacters should limit runes
	emojiText := "🚀🎉🌟🎊🎈" // 5 emoji = 5 runes, 20 bytes
	longEmojiText := ""
	for i := 0; i < 50; i++ {
		longEmojiText += emojiText
	}

	maxCharacters := 10
	chunks := ChunkText(longEmojiText, ChunkConfig{MaxCharacters: maxCharacters, OverlapCharacters: 2})

	// Each chunk should have at most maxCharacters runes
	for i, chunk := range chunks {
		runeCount := len([]rune(chunk))
		if runeCount > maxCharacters {
			t.Errorf("chunk %d has %d runes, expected <= %d: %q", i, runeCount, maxCharacters, chunk)
		}
		// Also verify it's valid UTF-8
		if !isValidUTF8(chunk) {
			t.Errorf("chunk %d is not valid UTF-8", i)
		}
	}
}

// isValidUTF8 returns true if the string is valid UTF-8.
func isValidUTF8(s string) bool {
	for i := 0; i < len(s); {
		r, size := readRuneUTF8([]byte(s[i:]))
		if r == '�' && size == 1 {
			// Invalid UTF-8 sequence
			return false
		}
		i += size
	}
	return true
}

// readRuneUTF8 decodes a single rune from UTF-8 bytes.
// Returns the rune and the number of bytes consumed.
func readRuneUTF8(b []byte) (rune, int) {
	if len(b) == 0 {
		return '�', 0
	}

	first := b[0]
	if first < 0x80 {
		return rune(first), 1
	}

	if first < 0xC0 {
		return '�', 1
	}

	if first < 0xE0 {
		if len(b) < 2 {
			return '�', 1
		}
		r := rune(first&0x1F)<<6 | rune(b[1]&0x3F)
		return r, 2
	}

	if first < 0xF0 {
		if len(b) < 3 {
			return '�', 1
		}
		r := rune(first&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
		return r, 3
	}

	if first < 0xF8 {
		if len(b) < 4 {
			return '�', 1
		}
		r := rune(first&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
		return r, 4
	}

	return '�', 1
}
