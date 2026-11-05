package slop

import (
	"regexp"
	"strings"
)

// Surface names a kind of text and decides which parts of it are the author's
// prose. Everything else is masked before any rule runs.
type Surface string

const (
	// SurfaceMarkdown scans prose. Fenced code, inline code, link targets and
	// quoted spans are masked first, so a document may quote a tell as an
	// exhibit without failing on its own example.
	SurfaceMarkdown Surface = "markdown"
	// SurfaceCommit scans a commit message: subject and body. The
	// conventional-commit prefix and the trailer block are masked, then the
	// markdown rules apply.
	SurfaceCommit Surface = "commit"
)

// ParseSurface maps a flag value to a Surface.
func ParseSurface(name string) (Surface, bool) {
	switch Surface(name) {
	case SurfaceMarkdown, SurfaceCommit:
		return Surface(name), true
	}
	return "", false
}

var (
	fenceOpen     = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	linkTarget    = regexp.MustCompile(`\]\(([^)\n]*)\)`)
	autolink      = regexp.MustCompile(`<[a-zA-Z][a-zA-Z0-9+.-]*:[^>\s]*>`)
	bareURL       = regexp.MustCompile(`https?://[^\s<>)\]]+`)
	refDefinition = regexp.MustCompile(`(?m)^ {0,3}\[[^\]\n]+\]:[ \t]*\S+`)
	quotedSpan    = regexp.MustCompile("\"[^\"\\n]{0,160}\"|\u201c[^\u201d\\n]{0,160}\u201d")

	conventionalPrefix = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*(\([^)\n]*\))?!?:[ \t]+`)
	trailerLine        = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*:[ \t]+\S`)
)

// Mask returns text with every byte outside the surface's prose replaced by a
// space. Newlines are kept and the length is unchanged, so a byte offset or a
// line number in the result is the same in the original.
func Mask(text string, surface Surface) string {
	return string(mask([]byte(text), surface, true))
}

// maskCode masks only fenced and inline code (and, for commits, the prefix and
// trailers). The slop-ok marker is searched here so that its reason may be
// written in quotes.
func maskCode(text string, surface Surface) string {
	return string(mask([]byte(text), surface, false))
}

func mask(buf []byte, surface Surface, full bool) []byte {
	if surface == SurfaceCommit {
		maskCommitFraming(buf)
	}
	maskFences(buf)
	maskInlineCode(buf)
	if full {
		for _, re := range []*regexp.Regexp{refDefinition, autolink, bareURL} {
			for _, loc := range re.FindAllIndex(buf, -1) {
				blank(buf, loc[0], loc[1])
			}
		}
		for _, loc := range linkTarget.FindAllSubmatchIndex(buf, -1) {
			blank(buf, loc[2], loc[3])
		}
		for _, loc := range quotedSpan.FindAllIndex(buf, -1) {
			blank(buf, loc[0], loc[1])
		}
	}
	return buf
}

func blank(buf []byte, start, end int) {
	for i := start; i < end; i++ {
		if buf[i] != '\n' {
			buf[i] = ' '
		}
	}
}

// maskCommitFraming blanks the conventional-commit prefix on the subject and
// a trailing trailer block (Signed-off-by, Co-authored-by, Refs, ...).
func maskCommitFraming(buf []byte) {
	text := string(buf)
	if loc := conventionalPrefix.FindStringIndex(firstLine(text)); loc != nil {
		blank(buf, loc[0], loc[1])
	}

	lines := strings.Split(text, "\n")
	offsets := make([]int, len(lines))
	position := 0
	for i, line := range lines {
		offsets[i] = position
		position += len(line) + 1
	}
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	// The last paragraph is a trailer block only when it is not the subject
	// paragraph and every line has the Key: value shape.
	if start == 0 || start == end {
		return
	}
	for i := start; i < end; i++ {
		if !trailerLine.MatchString(lines[i]) {
			return
		}
	}
	blank(buf, offsets[start], offsets[end-1]+len(lines[end-1]))
}

func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

// maskFences blanks fenced code blocks, including their fence lines. An
// unclosed fence runs to the end of the text, as in CommonMark.
func maskFences(buf []byte) {
	position := 0
	var fenceChar byte
	fenceLen := 0
	for position < len(buf) {
		next := position
		for next < len(buf) && buf[next] != '\n' {
			next++
		}
		line := string(buf[position:next])
		switch {
		case fenceChar == 0:
			if match := fenceOpen.FindStringSubmatch(line); match != nil {
				fenceChar, fenceLen = match[1][0], len(match[1])
				blank(buf, position, next)
			}
		default:
			blank(buf, position, next)
			if closesFence(line, fenceChar, fenceLen) {
				fenceChar, fenceLen = 0, 0
			}
		}
		position = next + 1
	}
}

func closesFence(line string, char byte, length int) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return false
	}
	run := 0
	for run < len(trimmed) && trimmed[run] == char {
		run++
	}
	return run >= length && strings.TrimSpace(trimmed[run:]) == ""
}

// maskInlineCode blanks code spans: a run of n backticks, up to the next run
// of exactly n on the same line. A run with no partner is left alone.
func maskInlineCode(buf []byte) {
	lineStart := 0
	for lineStart < len(buf) {
		lineEnd := lineStart
		for lineEnd < len(buf) && buf[lineEnd] != '\n' {
			lineEnd++
		}
		maskInlineCodeLine(buf, lineStart, lineEnd)
		lineStart = lineEnd + 1
	}
}

func maskInlineCodeLine(buf []byte, start, end int) {
	for i := start; i < end; {
		if buf[i] != '`' {
			i++
			continue
		}
		runStart := i
		for i < end && buf[i] == '`' {
			i++
		}
		width := i - runStart
		closer := -1
		for j := i; j < end; {
			if buf[j] != '`' {
				j++
				continue
			}
			k := j
			for k < end && buf[k] == '`' {
				k++
			}
			if k-j == width {
				closer = k
				break
			}
			j = k
		}
		if closer < 0 {
			continue
		}
		blank(buf, runStart, closer)
		i = closer
	}
}
