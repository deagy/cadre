package slop

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// ExemptionRuleID is the synthetic rule id reported when a slop-ok marker
// carries no reason. It is not a data rule and cannot be disabled: an
// exemption without a stated reason would be an unexplained hole.
const ExemptionRuleID = "slop-ok-reason-missing"

// Limitation is printed with every human report.
const Limitation = "This scan flags tells with fixed patterns. It cannot see structural slop " +
	"(invented frameworks, restatement, uniform rhythm) and is not evidence of authorship."

var exemptionMarker = regexp.MustCompile(`slop-ok:[ \t]*([^\n]*)`)

// Unit is one piece of text to scan: a file, standard input, or one commit
// message.
type Unit struct {
	Name    string // file path, "stdin", or "commit-message"
	Commit  string // full commit SHA for commit messages
	Surface Surface
	Text    string
}

// Options tunes a scan.
type Options struct {
	// Disabled switches rules off by id, each with a recorded reason.
	Disabled []Disabled
	// CountedLimit overrides the rules file's counted_limit when positive.
	CountedLimit int
}

// Hit is one tell found in a unit.
type Hit struct {
	File      string `json:"file"`
	Commit    string `json:"commit,omitempty"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	RuleID    string `json:"rule"`
	Class     Class  `json:"class"`
	Span      string `json:"span"`
	Context   string `json:"context"`
	Rationale string `json:"rationale"`
}

// Exemption is a slop-ok marker that exempted a whole unit.
type Exemption struct {
	File   string `json:"file"`
	Commit string `json:"commit,omitempty"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// UnitResult is the outcome for one unit.
type UnitResult struct {
	File    string  `json:"file"`
	Commit  string  `json:"commit,omitempty"`
	Surface Surface `json:"surface"`
	// Hits are the tells that fail the unit: every hard hit, plus the counted
	// hits when they reach the limit.
	Hits []Hit `json:"hits"`
	// CountedHits lists every counted hit, failing or not, so a unit under the
	// limit can still be inspected.
	CountedHits     []Hit      `json:"counted_hits"`
	CountedTotal    int        `json:"counted_total"`
	CountedExceeded bool       `json:"counted_exceeded"`
	Exempt          *Exemption `json:"exempt,omitempty"`
}

// Failed reports whether the unit has tells that fail it.
func (u UnitResult) Failed() bool { return len(u.Hits) > 0 }

// Report is the outcome of a scan.
type Report struct {
	RulesVersion int          `json:"rules_version"`
	CountedLimit int          `json:"counted_limit"`
	Units        []UnitResult `json:"units"`
	Disabled     []Disabled   `json:"disabled_rules"`
	Failed       bool         `json:"failed"`
	TotalHits    int          `json:"total_hits"`
	Limitation   string       `json:"limitation"`
}

// Exemptions lists every slop-ok exemption applied, for the report.
func (r *Report) Exemptions() []Exemption {
	var out []Exemption
	for _, unit := range r.Units {
		if unit.Exempt != nil {
			out = append(out, *unit.Exempt)
		}
	}
	return out
}

// Scan runs the rule set over the units. Units are reported in the order
// given. An invalid Options (unknown rule id, missing reason) is an error.
func Scan(set *RuleSet, units []Unit, opts Options) (*Report, error) {
	off, disabled, err := resolveDisabled(set, opts.Disabled)
	if err != nil {
		return nil, err
	}
	limit := set.CountedLimit
	if opts.CountedLimit > 0 {
		limit = opts.CountedLimit
	}

	report := &Report{
		RulesVersion: set.Version,
		CountedLimit: limit,
		Disabled:     disabled,
		Limitation:   Limitation,
		Units:        []UnitResult{},
	}
	for _, unit := range units {
		result := scanUnit(set, unit, off, limit)
		report.TotalHits += len(result.Hits)
		if result.Failed() {
			report.Failed = true
		}
		report.Units = append(report.Units, result)
	}
	return report, nil
}

func resolveDisabled(set *RuleSet, requested []Disabled) (map[string]bool, []Disabled, error) {
	off := map[string]bool{}
	out := []Disabled{}
	for _, entry := range requested {
		if entry.ID == ExemptionRuleID {
			return nil, nil, fmt.Errorf("slop: rule %q cannot be disabled", entry.ID)
		}
		if _, ok := set.Rule(entry.ID); !ok {
			return nil, nil, fmt.Errorf("slop: cannot disable unknown rule %q", entry.ID)
		}
		if strings.TrimSpace(entry.Reason) == "" {
			return nil, nil, fmt.Errorf("slop: disabling rule %q needs a reason", entry.ID)
		}
		if off[entry.ID] {
			return nil, nil, fmt.Errorf("slop: rule %q is disabled twice", entry.ID)
		}
		off[entry.ID] = true
		out = append(out, Disabled{ID: entry.ID, Reason: strings.TrimSpace(entry.Reason)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return off, out, nil
}

func scanUnit(set *RuleSet, unit Unit, off map[string]bool, limit int) UnitResult {
	result := UnitResult{
		File: unit.Name, Commit: unit.Commit, Surface: unit.Surface,
		Hits: []Hit{}, CountedHits: []Hit{},
	}
	index := newLineIndex(unit.Text)

	exemption, missing := findExemption(maskCode(unit.Text, unit.Surface))
	if missing >= 0 {
		line, column := index.position(missing)
		result.Hits = append(result.Hits, Hit{
			File: unit.Name, Commit: unit.Commit, Line: line, Column: column,
			RuleID: ExemptionRuleID, Class: ClassHard, Span: "slop-ok:",
			Context:   index.context(unit.Text, missing),
			Rationale: "An exemption must say why: write `slop-ok: <reason>`.",
		})
		return result
	}
	if exemption != nil {
		line, _ := index.position(exemption.offset)
		result.Exempt = &Exemption{File: unit.Name, Commit: unit.Commit, Line: line, Reason: exemption.reason}
		return result
	}

	masked := Mask(unit.Text, unit.Surface)
	for _, rule := range set.Rules {
		if off[rule.ID] {
			continue
		}
		for _, loc := range rule.re.FindAllStringIndex(masked, -1) {
			start, end := trimSpan(masked, loc[0], loc[1])
			line, column := index.position(start)
			hit := Hit{
				File: unit.Name, Commit: unit.Commit, Line: line, Column: column,
				RuleID: rule.ID, Class: rule.Class,
				Span:      truncate(strings.Join(strings.Fields(masked[start:end]), " "), 120),
				Context:   index.context(unit.Text, start),
				Rationale: rule.Rationale,
			}
			if rule.Class == ClassHard {
				result.Hits = append(result.Hits, hit)
			} else {
				result.CountedHits = append(result.CountedHits, hit)
			}
		}
	}

	result.CountedTotal = len(result.CountedHits)
	result.CountedExceeded = result.CountedTotal >= limit
	if result.CountedExceeded {
		result.Hits = append(result.Hits, result.CountedHits...)
	}
	sortHits(result.Hits)
	sortHits(result.CountedHits)
	return result
}

type foundExemption struct {
	offset int
	reason string
}

// findExemption returns the first slop-ok marker that carries a reason, or
// the offset of the first one that does not. missing is -1 when there is no
// reasonless marker. A reasonless marker wins: it is an error, never a pass.
func findExemption(maskedCode string) (found *foundExemption, missing int) {
	missing = -1
	for _, loc := range exemptionMarker.FindAllStringSubmatchIndex(maskedCode, -1) {
		reason := cleanReason(maskedCode[loc[2]:loc[3]])
		if reason == "" {
			if missing < 0 {
				missing = loc[0]
			}
			continue
		}
		if found == nil {
			found = &foundExemption{offset: loc[0], reason: reason}
		}
	}
	return found, missing
}

// cleanReason drops the closing token of an HTML or block comment around the
// reason, so `<!-- slop-ok: quoting an exhibit -->` yields the reason alone.
func cleanReason(raw string) string {
	reason := strings.TrimSpace(raw)
	for _, closer := range []string{"-->", "*/", "}}", "%>"} {
		reason = strings.TrimSpace(strings.TrimSuffix(reason, closer))
	}
	return reason
}

func trimSpan(text string, start, end int) (int, int) {
	for start < end && strings.ContainsRune(" \t\r\n", rune(text[start])) {
		start++
	}
	for end > start && strings.ContainsRune(" \t\r\n", rune(text[end-1])) {
		end--
	}
	return start, end
}

func sortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.RuleID < b.RuleID
	})
}

func truncate(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit-1]) + "…"
}

// lineIndex maps byte offsets to a 1-based line and a 1-based byte column.
type lineIndex struct {
	starts []int
}

func newLineIndex(text string) lineIndex {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return lineIndex{starts: starts}
}

func (l lineIndex) lineOf(offset int) int {
	return sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > offset }) - 1
}

func (l lineIndex) position(offset int) (line, column int) {
	row := l.lineOf(offset)
	return row + 1, offset - l.starts[row] + 1
}

// context returns the source line holding offset, trimmed and shortened
// around the offset.
func (l lineIndex) context(text string, offset int) string {
	row := l.lineOf(offset)
	start := l.starts[row]
	end := len(text)
	if row+1 < len(l.starts) {
		end = l.starts[row+1] - 1
	}
	line := strings.TrimRight(text[start:end], "\r")
	const window = 160
	if utf8.RuneCountInString(line) <= window {
		return strings.TrimSpace(line)
	}
	at := utf8.RuneCountInString(text[start:offset])
	runes := []rune(line)
	from := max(0, at-window/2)
	to := min(len(runes), from+window)
	out := strings.TrimSpace(string(runes[from:to]))
	if from > 0 {
		out = "…" + out
	}
	if to < len(runes) {
		out += "…"
	}
	return out
}
