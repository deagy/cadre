// glob_containment.go ports roster/orchestration/src/glob_containment.py:
// exact language containment for the selector's glob dialect.
//
// Answers one question: is every path matched by glob `include` also
// matched by at least one glob in `excludes`? That is language containment,
// L(include) ⊆ ⋃L(excludes), and it is decidable here -- the dialect
// (**, *, ?, literals) describes regular languages, so the question has an
// exact yes/no answer.
//
// It is used to detect a routing rule whose exclude_paths fully shadow one
// of its own paths globs -- where the answer must be trustworthy in both
// directions: a false "shadowed" verdict fails a check on a correct
// routing.json, and a false "alive" verdict is the silent coverage loss the
// check exists to catch. Sampling synthesized probe paths cannot support
// that verdict (it is a *universal* claim -- an incomplete sample makes it
// strictly easier to satisfy, which is the false-accusation direction), so
// this is a decision procedure, not a heuristic.
//
// This is a from-scratch port, not a reuse of the Python module's NFA
// construction: it must model route_matching.go's actual globToRegex
// tokenization and regexp.Regexp semantics (Go's RE2-backed package),
// which differ from Python's re-backed dialect in two ways that matter for
// correctness:
//
//   - Case sensitivity: globToRegex never sets an IGNORECASE-equivalent
//     flag, so Go's matcher (and this containment check) is case-sensitive.
//     Python's re.IGNORECASE made its version case-insensitive.
//   - `**` semantics: globToRegex always compiles `**` (with or without a
//     following `/`) to `(?:.*/)?` -- there is only one doublestar
//     construct in this dialect, unlike Python's dialect which distinguishes
//     a bare `**` (compiles to `.`) from `**/ ` (compiles to
//     `(?:.*/)?`).
//   - Anchoring: Go's regexp `$` (no `(?m)` flag) matches only the absolute
//     end of text, unlike Python's `$` which also matches just before a
//     single trailing newline -- so no extra "trailing newline" acceptance
//     state is needed here, unlike the Python original.
//
// Method: each glob becomes an NFA over a finite abstract alphabet: the
// distinct literal bytes appearing in the globs under comparison, plus `/`,
// plus one sentinel standing for every other byte, plus `\n` (which needs
// its own symbol because `[^/]` includes it while `.` excludes it -- the
// same asymmetry the Python original documents). The answer is computed by
// searching the product of the include NFA with the exclude NFAs for a
// reachable state that accepts the include and no exclude; such a state
// yields a concrete witness path. No witness anywhere in the (finite,
// bounded) product means containment holds.
//
// A `[...]` character class anywhere in a pattern under comparison forces
// Undetermined rather than attempting to model arbitrary regexp
// character-class semantics -- routing.json's real patterns never use one,
// and a false verdict would be strictly worse than declining to decide.
package orchestration

import (
	"regexp"
	"sort"
	"strings"
)

// Containment verdicts.
const (
	Contained    = "contained"
	NotContained = "not-contained"
	Undetermined = "undetermined"
)

// maxContainmentProductStates bounds explored product states during the
// containment search. Reaching it yields Undetermined rather than a
// verdict, so a caller that reports only on Contained cannot be made to
// accuse falsely by a pathological pattern. routing.json's real patterns
// explore a few dozen.
const maxContainmentProductStates = 50_000

const (
	symOther = "\x00other" // Sentinel: any byte that appears in no pattern under comparison.
	symSep   = "/"
	symNL    = "\n"
)

type globTokenKind int

const (
	globLiteral globTokenKind = iota
	globQuestion
	globStar
	globDoubleStar
)

type globToken struct {
	kind globTokenKind
	lit  string // one-byte string; valid when kind == globLiteral
}

// tokenizeGlobForContainment re-derives the token stream that
// route_matching.go's globToRegex would compile glob into, without building
// the regexp.Regexp itself. Returns ok=false if the pattern contains a `[`
// (a character class) -- see package doc.
func tokenizeGlobForContainment(glob string) ([]globToken, bool) {
	var tokens []globToken
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				tokens = append(tokens, globToken{kind: globDoubleStar})
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
				}
			} else {
				tokens = append(tokens, globToken{kind: globStar})
			}
		case '?':
			tokens = append(tokens, globToken{kind: globQuestion})
		case '[':
			return nil, false
		default:
			// Regex-special characters (.+^$()|{}) are escaped by
			// globToRegex to match literally, same as any other byte --
			// the token stream doesn't distinguish them.
			tokens = append(tokens, globToken{kind: globLiteral, lit: string(c)})
		}
	}
	return tokens, true
}

type globMove struct {
	kind   globTokenKind // globLiteral or globQuestion
	lit    string        // valid when kind == globLiteral
	target int
}

// containmentNFA is an epsilon-NFA whose transitions are predicates over
// abstract symbols, mirroring the shape of the Python original's `_Nfa`.
type containmentNFA struct {
	moves   map[int][]globMove
	loops   map[int]globTokenKind // globStar or globDoubleStar
	epsilon map[int][]int
	accept  int
	next    int
}

func newContainmentNFA(tokens []globToken) *containmentNFA {
	n := &containmentNFA{
		moves:   map[int][]globMove{},
		loops:   map[int]globTokenKind{},
		epsilon: map[int][]int{},
		next:    1,
	}
	state := 0
	for _, tok := range tokens {
		target := n.allocate()
		switch tok.kind {
		case globLiteral:
			n.moves[state] = append(n.moves[state], globMove{kind: globLiteral, lit: tok.lit, target: target})
		case globQuestion:
			n.moves[state] = append(n.moves[state], globMove{kind: globQuestion, target: target})
		case globStar:
			// [^/]*: consume non-separators in place, then move on.
			n.loops[state] = globStar
			n.epsilon[state] = append(n.epsilon[state], target)
		case globDoubleStar:
			// (?:.*/)?: a choice between nothing and "anything but \n,
			// then /" -- the second branch, once entered, is committed to
			// ending on a separator, so it gets its own state (a self-loop
			// plus a skip on one state would wrongly let the loop continue
			// after a literal '/' was already consumed as the "then /"
			// step).
			consumed := n.allocate()
			n.epsilon[state] = append(n.epsilon[state], consumed, target)
			n.loops[consumed] = globDoubleStar
			n.moves[consumed] = append(n.moves[consumed], globMove{kind: globLiteral, lit: symSep, target: target})
		}
		state = target
	}
	n.accept = state
	return n
}

func (n *containmentNFA) allocate() int {
	s := n.next
	n.next++
	return s
}

func (n *containmentNFA) closureSet(states map[int]bool) (string, map[int]bool) {
	seen := map[int]bool{}
	pending := []int{}
	for s := range states {
		seen[s] = true
		pending = append(pending, s)
	}
	for len(pending) > 0 {
		s := pending[0]
		pending = pending[1:]
		for _, t := range n.epsilon[s] {
			if !seen[t] {
				seen[t] = true
				pending = append(pending, t)
			}
		}
	}
	return stateSetKey(seen), seen
}

func (n *containmentNFA) step(states map[int]bool, symbol string) (string, map[int]bool) {
	next := map[int]bool{}
	for s := range states {
		loop, hasLoop := n.loops[s]
		if hasLoop {
			if loop == globDoubleStar && symbol != symNL {
				next[s] = true
			} else if loop == globStar && symbol != symSep {
				next[s] = true
			}
		}
		for _, mv := range n.moves[s] {
			switch mv.kind {
			case globLiteral:
				if symbol == mv.lit {
					next[mv.target] = true
				}
			case globQuestion:
				if symbol != symSep {
					next[mv.target] = true
				}
			}
		}
	}
	return n.closureSet(next)
}

func (n *containmentNFA) accepts(states map[int]bool) bool {
	return states[n.accept]
}

func stateSetKey(states map[int]bool) string {
	ids := make([]int, 0, len(states))
	for s := range states {
		ids = append(ids, s)
	}
	sort.Ints(ids)
	key := ""
	for _, id := range ids {
		key += string(rune(id)) + ","
	}
	return key
}

// containmentAlphabet is the finite abstract alphabet sufficient to decide
// these patterns: the distinct literal bytes in patterns, plus / and \n and
// the OTHER sentinel.
func containmentAlphabet(patternsTokens [][]globToken) []string {
	symbols := map[string]bool{symSep: true, symOther: true, symNL: true}
	for _, tokens := range patternsTokens {
		for _, tok := range tokens {
			if tok.kind == globLiteral {
				symbols[tok.lit] = true
			}
		}
	}
	out := make([]string, 0, len(symbols))
	for s := range symbols {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func concreteSymbol(symbol string, literals map[string]bool) string {
	if symbol != symOther {
		return symbol
	}
	for _, candidate := range "zqxjkvwy0123456789" {
		c := string(candidate)
		if !literals[c] {
			return c
		}
	}
	return "￿"
}

// GlobContains returns Contained, NotContained, or Undetermined for
// L(include) ⊆ ⋃L(excludes), under route_matching.go's actual glob dialect.
func GlobContains(include string, excludes []string) string {
	verdict, _ := GlobContainsWithWitness(include, excludes)
	return verdict
}

// GlobContainsWithWitness is GlobContains, but also returns a concrete
// counterexample path. On NotContained, the witness is a path that include
// matches and no exclude does -- independently checkable against
// route_matching.go's globToRegex. It is "" for Contained and Undetermined.
func GlobContainsWithWitness(include string, excludes []string) (string, string) {
	includeTokens, ok := tokenizeGlobForContainment(include)
	if !ok {
		return Undetermined, ""
	}
	if len(excludes) == 0 {
		return notContainedSample(include, includeTokens)
	}

	excludeTokensList := make([][]globToken, 0, len(excludes))
	for _, pattern := range excludes {
		tokens, ok := tokenizeGlobForContainment(pattern)
		if !ok {
			return Undetermined, ""
		}
		excludeTokensList = append(excludeTokensList, tokens)
	}

	includeNFA := newContainmentNFA(includeTokens)
	excludeNFAs := make([]*containmentNFA, len(excludeTokensList))
	for i, tokens := range excludeTokensList {
		excludeNFAs[i] = newContainmentNFA(tokens)
	}

	allTokens := append([][]globToken{includeTokens}, excludeTokensList...)
	alphabet := containmentAlphabet(allTokens)
	literals := map[string]bool{}
	for _, s := range alphabet {
		if s != symOther {
			literals[s] = true
		}
	}

	type productState struct {
		include  map[int]bool
		excludes []map[int]bool
	}
	type productKey struct {
		include  string
		excludes string
	}

	_, startInclude := includeNFA.closureSet(map[int]bool{0: true})
	startExcludes := make([]map[int]bool, len(excludeNFAs))
	startExcludeKeys := make([]string, len(excludeNFAs))
	for i, nfa := range excludeNFAs {
		_, s := nfa.closureSet(map[int]bool{0: true})
		startExcludes[i] = s
		startExcludeKeys[i] = stateSetKey(s)
	}

	makeKey := func(includeStates map[int]bool, excludeStates []map[int]bool) productKey {
		exKeys := ""
		for _, s := range excludeStates {
			exKeys += stateSetKey(s) + "|"
		}
		return productKey{include: stateSetKey(includeStates), excludes: exKeys}
	}

	start := productState{include: startInclude, excludes: startExcludes}
	startKey := makeKey(start.include, start.excludes)

	seen := map[productKey]bool{startKey: true}
	type parentEdge struct {
		key    productKey
		symbol string
	}
	parents := map[productKey]*parentEdge{startKey: nil}
	pending := []productState{start}
	pendingKeys := []productKey{startKey}

	for len(pending) > 0 {
		if len(seen) > maxContainmentProductStates {
			return Undetermined, ""
		}
		current := pending[0]
		currentKey := pendingKeys[0]
		pending = pending[1:]
		pendingKeys = pendingKeys[1:]

		if includeNFA.accepts(current.include) {
			anyExcludeAccepts := false
			for i, nfa := range excludeNFAs {
				if nfa.accepts(current.excludes[i]) {
					anyExcludeAccepts = true
					break
				}
			}
			if !anyExcludeAccepts {
				// Reconstruct the witness path by walking parent links.
				var symbols []string
				walkKey := currentKey
				for parents[walkKey] != nil {
					edge := parents[walkKey]
					symbols = append(symbols, edge.symbol)
					walkKey = edge.key
				}
				witness := ""
				for i := len(symbols) - 1; i >= 0; i-- {
					witness += concreteSymbol(symbols[i], literals)
				}
				return NotContained, witness
			}
		}

		for _, symbol := range alphabet {
			_, nextInclude := includeNFA.step(current.include, symbol)
			if len(nextInclude) == 0 {
				// The include can no longer match; this branch proves nothing.
				continue
			}
			nextExcludes := make([]map[int]bool, len(excludeNFAs))
			for i, nfa := range excludeNFAs {
				_, ns := nfa.step(current.excludes[i], symbol)
				nextExcludes[i] = ns
			}
			nextKey := makeKey(nextInclude, nextExcludes)
			if !seen[nextKey] {
				seen[nextKey] = true
				parents[nextKey] = &parentEdge{key: currentKey, symbol: symbol}
				pending = append(pending, productState{include: nextInclude, excludes: nextExcludes})
				pendingKeys = append(pendingKeys, nextKey)
			}
		}
	}
	return Contained, ""
}

// notContainedSample finds a concrete path matching include, used when
// there are no excludes at all (trivially NotContained).
func notContainedSample(include string, includeTokens []globToken) (string, string) {
	// An exclude pattern built only from literal bytes that cannot equal
	// any witness this search would produce -- the same trick the Python
	// original uses ("\0impossible-exclude") to reuse the general search
	// for the "no excludes at all" case instead of a separate code path.
	verdict, witness := GlobContainsWithWitness(include, []string{"\x00impossible-exclude"})
	if verdict == NotContained {
		return NotContained, witness
	}
	return NotContained, ""
}

// CheckRouteExcludeShadowing checks whether route's exclude_paths fully
// shadow any one of its own paths globs -- the routing.json correctness bug
// this file exists to catch. Returns, per paths entry: Contained (fully
// shadowed -- a real bug), NotContained (fine), or Undetermined.
func CheckRouteExcludeShadowing(route Route) map[string]string {
	results := make(map[string]string, len(route.Paths))
	for _, path := range route.Paths {
		results[path] = GlobContains(path, route.ExcludePaths)
	}
	return results
}

// globToRegex moved here from the removed route_matching.go. It is a
// general glob-to-regex utility, not part of the divergent selector that
// file carried, and glob_containment_test.go's assertWitnessIsValid uses
// it to check a NotContained witness independently rather than trusting
// the verdict -- the verification property this file exists to provide.
// globToRegex converts a shell glob pattern to a regex.
// Supports *, ?, and [...] patterns.
func globToRegex(glob string) *regexp.Regexp {
	var pattern strings.Builder
	pattern.WriteString("^")

	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				// ** matches anything including / (with optional /)
				// src/**/*.go should match src/main.go and src/cmd/main.go
				pattern.WriteString("(?:.*/)?")
				i++ // Skip the next *
				// Skip any following / character
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
				}
			} else {
				// * matches anything except /
				pattern.WriteString("[^/]*")
			}
		case '?':
			// ? matches any single character except /
			pattern.WriteString("[^/]")
		case '[':
			// Character class [...] - find the closing ]
			j := strings.IndexByte(glob[i:], ']')
			if j == -1 {
				pattern.WriteByte('[')
			} else {
				pattern.WriteString(glob[i : i+j+1])
				i += j
			}
		case '.', '+', '^', '$', '(', ')', '|', '{', '}':
			// Escape regex special characters
			pattern.WriteByte('\\')
			pattern.WriteByte(c)
		default:
			pattern.WriteByte(c)
		}
	}

	pattern.WriteString("$")

	re, err := regexp.Compile(pattern.String())
	if err != nil {
		// Fallback to exact match if regex compilation fails
		re, _ = regexp.Compile("^" + regexp.QuoteMeta(glob) + "$")
	}
	return re
}
