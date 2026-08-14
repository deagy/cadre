package generators

import (
	"fmt"
	"strings"
)

// This file ports roster/orchestration/src/role_metadata.py's frontmatter
// primitives. They must behave identically, byte for byte, because the
// packaged plugin's generated content is compared against the Python
// generator's output by CI (`cadre generate-plugin --check --output plugin`).

// IsMigrated reports whether a role's AGENT.md carries a '---'-delimited
// frontmatter block. Mirrors role_metadata.py's is_migrated(): the file must
// start with exactly "---\n" or "---\r\n". No other heuristic counts.
func IsMigrated(text string) bool {
	return strings.HasPrefix(text, "---\n") || strings.HasPrefix(text, "---\r\n")
}

// frontmatterNewline returns the newline convention the opening delimiter used.
func frontmatterNewline(text string) string {
	if strings.HasPrefix(text, "---\r\n") {
		return "\r\n"
	}
	return "\n"
}

// FrontmatterClosingDelimiterEnd returns the byte offset immediately after the
// closing frontmatter delimiter line's "---" (not including that line's
// trailing newline), and ok=false when text is not migrated.
//
// Uses exact-line matching rather than a raw strings.Index(text, "---")
// search: a raw search would false-match a "---" substring embedded inside a
// field value appearing before the real closing delimiter line.
func FrontmatterClosingDelimiterEnd(text string) (int, bool, error) {
	if !IsMigrated(text) {
		return 0, false, nil
	}
	newline := frontmatterNewline(text)
	lines := strings.Split(text, newline)
	offset := len(lines[0]) + len(newline)
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return offset + len(lines[i]), true, nil
		}
		offset += len(lines[i]) + len(newline)
	}
	return 0, false, fmt.Errorf("frontmatter opening '---' has no matching closing '---' line")
}

// StripFrontmatter returns text with any leading frontmatter block removed,
// leaving the body byte-identical to what it would be without the block. A
// no-op when text is not migrated.
func StripFrontmatter(text string) (string, error) {
	if !IsMigrated(text) {
		return text, nil
	}
	newline := frontmatterNewline(text)
	lines := strings.Split(text, newline)
	closing := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return "", fmt.Errorf("frontmatter opening '---' has no matching closing '---' line")
	}
	return strings.Join(lines[closing+1:], newline), nil
}
