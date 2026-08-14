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
func ChunkText(text string, config ChunkConfig) []string {
	maximum := config.MaxCharacters
	overlap := config.OverlapCharacters
	if len(text) <= maximum {
		return []string{text}
	}

	var chunks []string
	start := 0
	for start < len(text) {
		end := start + maximum
		if end > len(text) {
			end = len(text)
		}
		if end < len(text) {
			boundary := maxInt(
				lastIndex(text, "\n\n", start, end+1),
				lastIndex(text, ". ", start, end+1),
			)
			if boundary > start+int(float64(maximum)*0.55) {
				end = boundary + 1
			}
		}
		chunk := strings.TrimSpace(text[start:end])
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end >= len(text) {
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

// lastIndex returns the last index of sep within text[:limit], searching
// no earlier than searchFrom, or -1 if not found -- mirrors Python's
// str.rfind(sep, searchFrom, limit).
func lastIndex(text, sep string, searchFrom, limit int) int {
	if limit > len(text) {
		limit = len(text)
	}
	if limit <= searchFrom {
		return -1
	}
	window := text[searchFrom:limit]
	idx := strings.LastIndex(window, sep)
	if idx < 0 {
		return -1
	}
	return searchFrom + idx
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
