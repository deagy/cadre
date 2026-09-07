package textutil

import "strings"

// ChunkConfig bounds ChunkText's output.
type ChunkConfig struct {
	MaxCharacters     int
	OverlapCharacters int
}

// ChunkText splits text on paragraph or sentence boundaries where one
// falls late enough. Falls back to a hard cut when no boundary sits past
// 55% of the window, so a single unbroken block still chunks rather than
// returning one oversized piece.
//
// All operations are rune-indexed, not byte-indexed, so MaxCharacters
// genuinely counts characters and no chunk boundary can land inside a
// multi-byte UTF-8 sequence.
func ChunkText(text string, config ChunkConfig) []string {
	runes := []rune(text)
	maximum := config.MaxCharacters
	overlap := config.OverlapCharacters
	if len(runes) <= maximum {
		return []string{text}
	}

	var chunks []string
	start := 0
	for start < len(runes) {
		end := start + maximum
		if end > len(runes) {
			end = len(runes)
		}
		if end < len(runes) {
			boundary := maxInt(
				lastIndexRunes(runes, "\n\n", start, end+1),
				lastIndexRunes(runes, ". ", start, end+1),
			)
			if boundary > start+int(float64(maximum)*0.55) {
				end = boundary + 1
			}
		}
		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end >= len(runes) {
			break
		}
		next := end - overlap
		if next < start+1 {
			next = start + 1
		}
		start = next
	}
	return chunks
}

// lastIndexRunes returns the last index of sep (a string) within runes[:limit],
// searching no earlier than searchFrom, or -1 if not found.
// It converts sep to runes and searches for the rune sequence.
func lastIndexRunes(runes []rune, sep string, searchFrom, limit int) int {
	sepRunes := []rune(sep)
	if limit > len(runes) {
		limit = len(runes)
	}
	if limit <= searchFrom || len(sepRunes) == 0 {
		return -1
	}
	// Search for sepRunes in runes[searchFrom:limit]
	lastIdx := -1
	for i := searchFrom; i <= limit-len(sepRunes); i++ {
		if runesEqual(runes[i:i+len(sepRunes)], sepRunes) {
			lastIdx = i
		}
	}
	return lastIdx
}

// runesEqual returns true if two rune slices are equal.
func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
