// Package selector is a Go port of `cadre select`'s deterministic matching
// and plan assembly, ported from roster/orchestration/src.
//
// It is gated by roster/orchestration/test/test_select_differential.py: this
// package may only replace the Python implementation when it produces a
// byte-identical plan for every corpus case, dispatch_fingerprint included.
// A previous Go selector in this repository diverged from that contract and
// had to be removed; the harness exists so that cannot recur unnoticed.
//
// Deliberately a new package rather than an addition to
// internal/orchestration. That package held the divergent selector, and
// keeping the port separate makes "which implementation is this?" answerable
// by import path.
package selector

import (
	"regexp"
	"strings"
)

// Glob token kinds, mirroring routing.py's iter_glob_tokens. The dialect is
// small and closed; each kind maps to exactly one regex fragment.
const (
	globDoubleStarSlash = "doublestar_slash" // `**/` -- any number of leading segments
	globDoubleStar      = "doublestar"       // `**`  -- anything, `/` included
	globStar            = "star"             // `*`   -- anything within one segment
	globQuestion        = "question"         // `?`   -- one character, not `/`
	globLiteral         = "literal"          // matched exactly
)

var globTokenRegex = map[string]string{
	globDoubleStarSlash: "(?:.*/)?",
	globDoubleStar:      ".*",
	globStar:            "[^/]*",
	globQuestion:        "[^/]",
}

type globToken struct {
	kind string
	text string
}

// iterGlobTokens ports routing.py's iter_glob_tokens. Backslashes are
// normalised to `/` first, so a Windows-style pattern tokenises identically
// to its POSIX spelling.
func iterGlobTokens(pattern string) []globToken {
	normalized := strings.ReplaceAll(pattern, `\`, "/")
	tokens := make([]globToken, 0, len(normalized))
	for index := 0; index < len(normalized); index++ {
		character := normalized[index]
		switch {
		case character == '*' && index+1 < len(normalized) && normalized[index+1] == '*':
			index++
			if index+1 < len(normalized) && normalized[index+1] == '/' {
				index++
				tokens = append(tokens, globToken{globDoubleStarSlash, "**/"})
			} else {
				tokens = append(tokens, globToken{globDoubleStar, "**"})
			}
		case character == '*':
			tokens = append(tokens, globToken{globStar, "*"})
		case character == '?':
			tokens = append(tokens, globToken{globQuestion, "?"})
		default:
			tokens = append(tokens, globToken{globLiteral, string(character)})
		}
	}
	return tokens
}

var globCache = map[string]*regexp.Regexp{}

// GlobToRegex translates the selector's glob dialect to an anchored,
// case-insensitive regex, mirroring routing.py's glob_to_regex.
func GlobToRegex(pattern string) *regexp.Regexp {
	if cached, ok := globCache[pattern]; ok {
		return cached
	}
	var builder strings.Builder
	builder.WriteString("(?i)^")
	for _, token := range iterGlobTokens(pattern) {
		if fragment, ok := globTokenRegex[token.kind]; ok {
			builder.WriteString(fragment)
			continue
		}
		builder.WriteString(regexp.QuoteMeta(token.text))
	}
	builder.WriteString("$")
	compiled := regexp.MustCompile(builder.String())
	globCache[pattern] = compiled
	return compiled
}

var keywordCache = map[string]*regexp.Regexp{}

// keywordBody compiles a keyword's literal body -- *without* the word
// boundaries. Internal whitespace matches any run of whitespace, exactly as
// Python's `re.escape(keyword).replace(r"\ ", r"\s+")` does.
func keywordBody(keyword string) *regexp.Regexp {
	if cached, ok := keywordCache[keyword]; ok {
		return cached
	}
	parts := strings.Fields(strings.ToLower(keyword))
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		quoted = append(quoted, regexp.QuoteMeta(part))
	}
	compiled := regexp.MustCompile(`(?i)` + strings.Join(quoted, `\s+`))
	keywordCache[keyword] = compiled
	return compiled
}

// isKeywordBoundaryChar reports whether c is part of a word for keyword
// matching. Mirrors Python's `[a-z0-9-]` boundary class under IGNORECASE:
// a hyphen counts as a word character, so "runner" does not match
// "cross-runner"; underscore and `.` do NOT, which is why routing.json's
// `bootstrap_sdlc.py` keyword can match embedded in a longer token.
func isKeywordBoundaryChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '-':
		return true
	}
	return false
}

// KeywordMatches is a case-insensitive whole-word match, porting routing.py's
// _keyword_matches.
//
// Python expresses the boundaries as lookarounds --
// `(?<![a-z0-9-])body(?![a-z0-9-])`. Go's regexp is RE2 and has neither
// lookbehind nor lookahead, so the boundaries are checked explicitly against
// each candidate span instead. That is a reimplementation of the same
// predicate rather than a transliteration, which is precisely the kind of
// difference the differential harness exists to catch.
//
// The substring gate is Python's compile-avoidance optimisation, kept because
// it is also a correctness-neutral fast path here: every whitespace-separated
// token of the keyword must appear in the text before the regex is consulted.
// It can only skip work that would have returned false.
func KeywordMatches(text, keyword string) bool {
	lowered := strings.ToLower(text)
	for _, token := range strings.Fields(strings.ToLower(keyword)) {
		if !strings.Contains(lowered, token) {
			return false
		}
	}
	for _, span := range keywordBody(keyword).FindAllStringIndex(text, -1) {
		start, end := span[0], span[1]
		if start > 0 && isKeywordBoundaryChar(text[start-1]) {
			continue
		}
		if end < len(text) && isKeywordBoundaryChar(text[end]) {
			continue
		}
		return true
	}
	return false
}

// PathMatch is one (pattern, file) pair that matched, in the order the plan
// records them.
type PathMatch struct {
	Pattern string `json:"pattern"`
	File    string `json:"file"`
}

// RuleMatch is match_rule's return shape.
type RuleMatch struct {
	Matched       bool
	Keywords      []string
	KeywordGroups [][]string
	Paths         []PathMatch
}

// MatchRule ports routing.py's match_rule.
//
// exclude_paths subtracts at the *file* level, not the rule level: a route
// whose include glob is deliberately broad can carve out the paths that glob
// was never meant to reach while still matching on any other changed file.
func MatchRule(rule map[string]any, taskText string, changedFiles []string) RuleMatch {
	normalizedTask := strings.ToLower(taskText)

	matchedKeywords := []string{}
	for _, keyword := range stringSlice(rule["keywords"]) {
		if KeywordMatches(normalizedTask, keyword) {
			matchedKeywords = append(matchedKeywords, keyword)
		}
	}

	matchedGroups := [][]string{}
	conjunctive := false
	if groups, ok := rule["keyword_groups"].([]any); ok && len(groups) > 0 {
		allNonEmpty := true
		for _, rawGroup := range groups {
			hits := []string{}
			for _, keyword := range stringSlice(rawGroup) {
				if KeywordMatches(normalizedTask, keyword) {
					hits = append(hits, keyword)
				}
			}
			matchedGroups = append(matchedGroups, hits)
			if len(hits) == 0 {
				allNonEmpty = false
			}
		}
		conjunctive = allNonEmpty
	}

	excluders := make([]*regexp.Regexp, 0)
	for _, pattern := range stringSlice(rule["exclude_paths"]) {
		excluders = append(excluders, GlobToRegex(pattern))
	}

	matchedPaths := []PathMatch{}
	for _, pattern := range stringSlice(rule["paths"]) {
		matcher := GlobToRegex(pattern)
		for _, fileName := range changedFiles {
			normalizedFile := strings.ReplaceAll(fileName, `\`, "/")
			excluded := false
			for _, excluder := range excluders {
				if excluder.MatchString(normalizedFile) {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
			if matcher.MatchString(normalizedFile) {
				matchedPaths = append(matchedPaths, PathMatch{Pattern: pattern, File: fileName})
			}
		}
	}

	if !conjunctive {
		matchedGroups = [][]string{}
	}
	return RuleMatch{
		Matched:       len(matchedKeywords) > 0 || conjunctive || len(matchedPaths) > 0,
		Keywords:      matchedKeywords,
		KeywordGroups: matchedGroups,
		Paths:         matchedPaths,
	}
}

// stringSlice reads a JSON array of strings out of a decoded rule, tolerating
// absence. A non-string element is skipped rather than panicking: routing.json
// is validated elsewhere, and this package is not the place to re-litigate its
// schema.
func stringSlice(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, element := range raw {
		if text, ok := element.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
