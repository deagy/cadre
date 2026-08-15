package selector

import (
	"strings"
	"unicode/utf8"
)

// A port of Python's textwrap.fill for the one configuration the plan
// formatter uses:
//
//	textwrap.fill(text, width=W, initial_indent=..., subsequent_indent=...,
//	              break_on_hyphens=False, break_long_words=False)
//
// Go has nothing equivalent, and every line break in `--format text` comes
// from this function, so an approximation would produce a rendering that is
// plausible on inspection and different on every long line.
//
// Why those two flags are off, and why that has to survive the port: role ids
// are hyphenated (kubernetes-manifest-implementer), and textwrap's defaults
// break on hyphens -- wrapping one role across two lines, where it reads as
// two different roles that do not exist. Long ids overrun the width instead.

const (
	textWrapTabSize = 8
)

// textwrapFill reproduces the algorithm rather than the shape of the output:
// normalise whitespace, split into chunks on whitespace runs keeping the
// separators, then greedily fill lines.
func textwrapFill(text string, width int, initialIndent, subsequentIndent string) string {
	return strings.Join(textwrapWrap(text, width, initialIndent, subsequentIndent), "\n")
}

// textwrapWrap is fill without the final join, which is how the formatter
// needs it in a couple of places.
func textwrapWrap(text string, width int, initialIndent, subsequentIndent string) []string {
	normalized := mungeWhitespace(text)
	chunks := splitChunks(normalized)
	return wrapChunks(chunks, width, initialIndent, subsequentIndent)
}

// mungeWhitespace is expand_tabs + replace_whitespace, both of which default
// to True. Every whitespace character other than a space becomes a space
// *after* tabs are expanded, so a tab's alignment is computed before it is
// flattened.
func mungeWhitespace(text string) string {
	var builder strings.Builder
	column := 0
	for _, character := range text {
		if character == '\t' {
			spaces := textWrapTabSize - (column % textWrapTabSize)
			builder.WriteString(strings.Repeat(" ", spaces))
			column += spaces
			continue
		}
		builder.WriteRune(character)
		if character == '\n' {
			column = 0
		} else {
			column++
		}
	}

	// Python translates \t\n\v\f\r and \x1d\x1e\x1c\x85 to spaces. Tabs are
	// already gone above.
	replacer := strings.NewReplacer(
		"\n", " ", "\v", " ", "\f", " ", "\r", " ",
		"\x1c", " ", "\x1d", " ", "\x1e", " ", "\x85", " ",
	)
	return replacer.Replace(builder.String())
}

// splitChunks is Python's wordsep_simple_re -- `(\s+)` with the separators
// kept -- which is the pattern textwrap uses when break_on_hyphens is False.
// Splitting on anything smarter is exactly the hyphen-breaking this
// configuration exists to disable.
func splitChunks(text string) []string {
	if text == "" {
		return nil
	}
	var chunks []string
	var current strings.Builder
	inWhitespace := isASCIISpace(rune(text[0]))
	for _, character := range text {
		characterIsSpace := isASCIISpace(character)
		if characterIsSpace != inWhitespace {
			chunks = append(chunks, current.String())
			current.Reset()
			inWhitespace = characterIsSpace
		}
		current.WriteRune(character)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

// isASCIISpace matches what remains after mungeWhitespace: only the space
// character survives, but the check stays explicit so a caller passing raw
// text still chunks sensibly.
func isASCIISpace(character rune) bool {
	switch character {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

// wrapChunks is textwrap's _wrap_chunks with drop_whitespace=True and
// break_long_words=False.
func wrapChunks(chunks []string, width int, initialIndent, subsequentIndent string) []string {
	if width <= 0 {
		width = 1
	}
	var lines []string

	// Python consumes the chunk list from the end; reversing once keeps the
	// index arithmetic here the same shape as the original.
	remaining := make([]string, len(chunks))
	for index, chunk := range chunks {
		remaining[len(chunks)-1-index] = chunk
	}

	for len(remaining) > 0 {
		indent := subsequentIndent
		if len(lines) == 0 {
			indent = initialIndent
		}
		available := width - runeLength(indent)
		if available < 1 {
			available = 1
		}

		// A whitespace chunk at the start of a continuation line is dropped,
		// which is what keeps a wrapped line from beginning with a space.
		if len(lines) > 0 && strings.TrimSpace(remaining[len(remaining)-1]) == "" {
			remaining = remaining[:len(remaining)-1]
			if len(remaining) == 0 {
				break
			}
		}

		var current []string
		length := 0
		for len(remaining) > 0 {
			chunk := remaining[len(remaining)-1]
			chunkLength := runeLength(chunk)
			if length+chunkLength > available {
				break
			}
			current = append(current, chunk)
			length += chunkLength
			remaining = remaining[:len(remaining)-1]
		}

		// A single chunk longer than the whole line: with break_long_words
		// off it goes on its own line and overruns, rather than being cut in
		// half. This is the branch that keeps a long role id readable.
		if len(remaining) > 0 && runeLength(remaining[len(remaining)-1]) > available && len(current) == 0 {
			current = append(current, remaining[len(remaining)-1])
			remaining = remaining[:len(remaining)-1]
		}

		// Trailing whitespace on a wrapped line is dropped.
		for len(current) > 0 && strings.TrimSpace(current[len(current)-1]) == "" {
			current = current[:len(current)-1]
		}

		if len(current) > 0 {
			lines = append(lines, indent+strings.Join(current, ""))
		}
	}

	// textwrap.fill on empty text yields one empty string, not zero lines.
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// runeLength counts characters, not bytes: Python measures width in
// characters, so a task containing any non-ASCII would otherwise wrap early
// in Go by however many continuation bytes it contained.
func runeLength(text string) int {
	return utf8.RuneCountInString(text)
}
