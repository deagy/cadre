package guard

import (
	"errors"
	"regexp"
	"strings"
)

// Shell parsing, good enough to find each independent `git ...` invocation in
// a chained command line without being fooled by an operator inside a quoted
// string. Not a full shell parser, and deliberately not: every limit below
// fails open.

// heredocDelimiter matches the delimiter after a `<<`, in any of its spellings.
// Anchored because Go has no "match at offset" -- the caller slices instead.
var heredocDelimiter = regexp.MustCompile(`^(-?)[ \t]*(?:'([^']*)'|"([^"]*)"|([A-Za-z0-9_.\-]+))`)

// joinLineContinuations removes backslash-newline continuations, as the shell
// does.
//
// `git push \<newline> origin main --force` is one command, not two. Without
// this, the newline splitting below turns it into `git push \` (no --force,
// allowed) and `origin main --force` (not a git invocation, ignored), so a
// force-push sails through a guard that catches the single-line spelling.
// Long commands are written this way routinely; this is not a corner case.
//
// Quote-aware, because the shell is: inside SINGLE quotes a backslash-newline
// is literal text and must be preserved, while unquoted and inside double
// quotes it is a continuation. A backslash that is itself escaped
// (`\\<newline>` inside double quotes) is a known edge this does not model.
func joinLineContinuations(command string) string {
	var out strings.Builder
	var quote byte
	for i := 0; i < len(command); {
		ch := command[i]
		// Single quotes first: inside them a backslash is literal, so the
		// continuation branch below must not see it.
		if quote == '\'' {
			out.WriteByte(ch)
			if ch == '\'' {
				quote = 0
			}
			i++
			continue
		}
		if ch == '\\' && strings.HasPrefix(command[i+1:], "\n") {
			i += 2
			continue
		}
		if ch == '\\' && strings.HasPrefix(command[i+1:], "\r\n") {
			i += 3
			continue
		}
		if quote == '"' {
			out.WriteByte(ch)
			if ch == '"' && (i == 0 || command[i-1] != '\\') {
				quote = 0
			}
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			out.WriteByte(ch)
			i++
			continue
		}
		out.WriteByte(ch)
		i++
	}
	return out.String()
}

// heredocOpener is one delimiter opened by a segment.
type heredocOpener struct {
	delimiter        string
	allowsLeadingTab bool // the `<<-` form
}

// segment is one top-level piece plus what the shell knew at the time.
//
// Discarding these three facts and re-deriving them from segment text is what
// produced the findings this scanner exists to prevent: a chained command
// after a heredoc opener being swallowed, a quoted mention of `<<EOF` being
// read as a real redirection, and `$(( x << 2 ))` being read as one.
type segment struct {
	// raw is NOT stripped. Leading and trailing whitespace is load-bearing for
	// heredoc terminator matching -- a terminator line must be exactly the
	// delimiter, unless the `<<-` form allows leading tabs.
	raw string
	// newlineBefore records whether this began a new LINE rather than
	// following &&/||/;/| on the previous one. A heredoc body starts on the
	// next line, so a command chained onto the opener's own line is a command.
	newlineBefore bool
	heredocs      []heredocOpener
}

func scanSegments(command string) []segment {
	var segments []segment
	var buf strings.Builder
	var heredocs []heredocOpener
	var quote byte
	arithmeticDepth := 0
	newlineBefore := false

	flush := func(nextStartsALine bool) {
		segments = append(segments, segment{
			raw: buf.String(), newlineBefore: newlineBefore, heredocs: heredocs,
		})
		buf.Reset()
		heredocs = nil
		newlineBefore = nextStartsALine
	}

	for i := 0; i < len(command); {
		ch := command[i]
		if quote != 0 {
			buf.WriteByte(ch)
			if ch == quote && (i == 0 || command[i-1] != '\\') {
				quote = 0
			}
			i++
			continue
		}
		// An unquoted backslash escapes the next character, so `\;` is a
		// LITERAL semicolon and not a separator. Without this, `find . -exec
		// git worktree remove {} \;` -- the ordinary spelling, since the shell
		// would otherwise eat the `;` -- split at the `;` into a segment
		// ending in a dangling backslash, and the git invocation inside it was
		// never evaluated as one.
		if ch == '\\' && i+1 < len(command) {
			buf.WriteByte(ch)
			buf.WriteByte(command[i+1])
			i += 2
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			buf.WriteByte(ch)
			i++
			continue
		}
		// `$(( ... ))`: a `<<` in here is a left shift, not a redirection.
		// Other arithmetic contexts (a bare `(( ))`, `let`) are not modelled --
		// a known limit, in the fail-open direction only.
		if strings.HasPrefix(command[i:], "$((") {
			arithmeticDepth++
			buf.WriteString("$((")
			i += 3
			continue
		}
		if arithmeticDepth > 0 && strings.HasPrefix(command[i:], "))") {
			arithmeticDepth--
			buf.WriteString("))")
			i += 2
			continue
		}
		if strings.HasPrefix(command[i:], "&&") || strings.HasPrefix(command[i:], "||") {
			flush(false)
			i += 2
			continue
		}
		if ch == ';' || ch == '|' {
			flush(false)
			i++
			continue
		}
		if ch == '\n' {
			flush(true)
			i++
			continue
		}
		if arithmeticDepth == 0 &&
			strings.HasPrefix(command[i:], "<<") &&
			!strings.HasPrefix(command[i:], "<<<") && // here-STRING: no body, no terminator
			(i == 0 || command[i-1] != '<') { // not the tail of a `<<<`
			if location := heredocDelimiter.FindStringSubmatchIndex(command[i+2:]); location != nil {
				// Explicit first-set selection rather than a coalesce, so this
				// and the TypeScript mirror mean the same thing for an empty
				// delimiter (`<<''`), which `or` would treat as "no match".
				delimiter, found := "", false
				for _, group := range []int{2, 3, 4} {
					if location[2*group] != -1 {
						delimiter = command[i+2:][location[2*group]:location[2*group+1]]
						found = true
						break
					}
				}
				if found {
					dash := location[2] != -1 && location[3] > location[2]
					heredocs = append(heredocs, heredocOpener{delimiter, dash})
					end := i + 2 + location[1]
					buf.WriteString(command[i:end])
					i = end
					continue
				}
			}
		}
		buf.WriteByte(ch)
		i++
	}
	flush(false)
	return segments
}

// stripHeredocBodies drops heredoc body lines and their terminator.
//
// Without it, the BODY of `cat > note.md <<'EOF' / git reset --hard / EOF`
// parses as a command and is blocked, even though it is text being written to
// a file. That is a false positive of exactly the kind this package's design
// stance treats as the real risk -- documentation quoting a destructive
// command is routine, and in this repository especially so.
//
// Three things are deliberately KEPT, each of which a naive
// consume-forward-to-the-delimiter pass gets wrong: the opening segment (a
// real command), every remaining segment on the opener's OWN line (`cat > f
// <<EOF && git worktree remove <path>` runs that git command before a single
// body line is read), and everything after the terminator.
//
// Best-effort: an unterminated heredoc swallows the remainder, which is what
// the shell itself does. A delimiter containing ;/|/& is not matched, because
// the terminator line is split before this pass sees it -- the heredoc then
// reads as unterminated, i.e. fails open.
func stripHeredocBodies(records []segment) []segment {
	var out []segment
	for i := 0; i < len(records); {
		record := records[i]
		out = append(out, record)
		i++
		if len(record.heredocs) == 0 {
			continue
		}
		// The rest of the opener's own line is commands, not body.
		for i < len(records) && !records[i].newlineBefore {
			out = append(out, records[i])
			i++
		}
		// Bodies begin on the following line, one per delimiter, in order.
		for _, opener := range record.heredocs {
			for i < len(records) {
				candidateSegment := records[i]
				aloneOnItsLine := candidateSegment.newlineBefore &&
					(i+1 >= len(records) || records[i+1].newlineBefore)
				i++
				if !aloneOnItsLine {
					continue
				}
				// Exact against the unstripped text, as the shell requires:
				// `EOF` terminates, `    EOF` does not, and only `<<-` accepts
				// leading TABS. A trailing \r is tolerated so CRLF behaves.
				candidate := strings.TrimRight(candidateSegment.raw, "\r")
				if opener.allowsLeadingTab {
					candidate = strings.TrimLeft(candidate, "\t")
				}
				if candidate == opener.delimiter {
					break
				}
			}
		}
	}
	return out
}

// splitTopLevel splits a command line on &&, ||, ;, | and NEWLINES,
// respecting quoting.
//
// Newline is a separator for the same reason `;` is: the shell treats them
// identically. Omitting it silently defeated EVERY handler once -- a shell
// word splitter treats a newline as ordinary whitespace, so a two-line command
// collapsed into one token list whose first token was the first line's
// program, the git parse returned nothing, and the destructive second line was
// never inspected. That needed no adversarial intent: multi-line Bash tool
// calls are routine.
func splitTopLevel(command string) []string {
	var out []string
	for _, record := range stripHeredocBodies(scanSegments(joinLineContinuations(command))) {
		if trimmed := strings.TrimSpace(record.raw); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// errUnbalancedQuote is what splitWords reports for input the shell itself
// would reject. Callers skip the segment rather than guess at it.
var errUnbalancedQuote = errors.New("unbalanced quoting")

// splitWords is POSIX shell word splitting, equivalent to Python's
// shlex.split(posix=True) for the shapes a command line reaches here in.
//
// Go has no standard equivalent, and the details matter: a wrong one changes
// which token is read as the git subcommand.
func splitWords(input string) ([]string, error) {
	var words []string
	var current strings.Builder
	started := false
	var quote byte

	for i := 0; i < len(input); i++ {
		ch := input[i]
		switch {
		case quote == '\'':
			if ch == '\'' {
				quote = 0
				continue
			}
			current.WriteByte(ch)
		case quote == '"':
			// Inside double quotes a backslash escapes only these four; before
			// anything else it stays literal, which is what the shell does.
			if ch == '\\' && i+1 < len(input) {
				next := input[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					current.WriteByte(next)
					i++
					continue
				}
			}
			if ch == '"' {
				quote = 0
				continue
			}
			current.WriteByte(ch)
		case ch == '\\':
			if i+1 < len(input) {
				current.WriteByte(input[i+1])
				i++
				started = true
				continue
			}
			// A trailing backslash: the shell would continue the line, and the
			// continuation join already ran, so this is malformed.
			return nil, errUnbalancedQuote
		case ch == '\'' || ch == '"':
			quote = ch
			started = true
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			if started || current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteByte(ch)
			started = true
		}
		if quote != 0 {
			started = true
		}
	}
	if quote != 0 {
		return nil, errUnbalancedQuote
	}
	if started || current.Len() > 0 {
		words = append(words, current.String())
	}
	return words, nil
}
