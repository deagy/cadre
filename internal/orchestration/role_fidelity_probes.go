// role_fidelity_probes.go: probe definitions, parsing, and pure scoring
// logic for role_fidelity.go's probe mode.
package orchestration

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// FidelityProbe is one declarative fidelity question and how to score the
// answer.
type FidelityProbe struct {
	ID                string
	Prompt            string
	Description       string
	AppliesTo         []string // role names; empty = all roles
	AppliesToTiers    []string // tier names; empty = all tiers
	MustMentionAny    []string
	MustMentionAll    []string
	MustNotMentionAny []string
	// Regex patterns (case-insensitive), for the class of negative check
	// MustNotMentionAny cannot express.
	MustNotMatchAny []string
	MaxWords        *int
	MinWords        *int
	// "REFUSE" or "PROCEED". When set, this IS the pass/fail signal and
	// every MustMention*/MustNot* field above is unused for this probe.
	ExpectVerdict string
}

// Applies reports whether probe applies to preset.
func (p FidelityProbe) Applies(preset FidelityPreset) bool {
	if len(p.AppliesTo) > 0 && !stringSliceContains(p.AppliesTo, preset.Name) {
		return false
	}
	if len(p.AppliesToTiers) > 0 && !stringSliceContains(p.AppliesToTiers, preset.Tier) {
		return false
	}
	return true
}

func stringSliceContains(list []string, item string) bool {
	for _, s := range list {
		if s == item {
			return true
		}
	}
	return false
}

// ValidFidelityVerdicts are the two verdict tokens expect_verdict may name.
var ValidFidelityVerdicts = []string{"REFUSE", "PROCEED"}

func asStringSlice(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return []string{v}
	case []any:
		out := make([]string, len(v))
		for i, item := range v {
			out[i] = fmt.Sprintf("%v", item)
		}
		return out
	default:
		return []string{fmt.Sprintf("%v", v)}
	}
}

func toIntPointer(value any) (*int, error) {
	if value == nil {
		return nil, nil
	}
	switch v := value.(type) {
	case int:
		return &v, nil
	case int64:
		i := int(v)
		return &i, nil
	case float64:
		i := int(v)
		return &i, nil
	case string:
		i, err := strconv.Atoi(v)
		if err != nil {
			return nil, err
		}
		return &i, nil
	default:
		return nil, fmt.Errorf("cannot convert %v to int", value)
	}
}

// ParseFidelityProbes validates and converts a raw parsed probe-file value
// (a list of maps, as produced by YAML/JSON unmarshaling into []any) into
// FidelityProbe values.
func ParseFidelityProbes(raw any, source string) ([]FidelityProbe, error) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, fidelityErrorf("%s: expected a non-empty list of probes", source)
	}
	var probes []FidelityProbe
	seen := map[string]bool{}
	for index, rawEntry := range list {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return nil, fidelityErrorf("%s: probe #%d is not a mapping", source, index)
		}
		probeID := strings.TrimSpace(fmt.Sprintf("%v", valueOrEmpty(entry["id"])))
		prompt := strings.TrimSpace(fmt.Sprintf("%v", valueOrEmpty(entry["prompt"])))
		if probeID == "" {
			return nil, fidelityErrorf("%s: probe #%d has no 'id'", source, index)
		}
		if prompt == "" {
			return nil, fidelityErrorf("%s: probe %q has no 'prompt'", source, probeID)
		}
		if seen[probeID] {
			return nil, fidelityErrorf("%s: duplicate probe id %q", source, probeID)
		}
		seen[probeID] = true

		maxWords, err := toIntPointer(entry["max_words"])
		if err != nil {
			return nil, fidelityErrorf("%s: probe %q has invalid max_words: %v", source, probeID, err)
		}
		minWords, err := toIntPointer(entry["min_words"])
		if err != nil {
			return nil, fidelityErrorf("%s: probe %q has invalid min_words: %v", source, probeID, err)
		}

		mustNotMatchAny := asStringSlice(entry["must_not_match_any"])
		for _, pattern := range mustNotMatchAny {
			if _, err := regexp.Compile("(?i)" + pattern); err != nil {
				return nil, fidelityErrorf("%s: probe %q has an invalid must_not_match_any pattern %q: %v", source, probeID, pattern, err)
			}
		}

		expectVerdict := ""
		if raw, ok := entry["expect_verdict"]; ok && raw != nil {
			expectVerdict = strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", raw)))
			if !stringSliceContains(ValidFidelityVerdicts, expectVerdict) {
				return nil, fidelityErrorf("%s: probe %q has invalid expect_verdict %q (must be one of: %s)",
					source, probeID, raw, strings.Join(sortedCopy(ValidFidelityVerdicts), ", "))
			}
		}

		probe := FidelityProbe{
			ID:                probeID,
			Prompt:            prompt,
			Description:       fmt.Sprintf("%v", valueOrEmpty(entry["description"])),
			AppliesTo:         asStringSlice(entry["applies_to"]),
			AppliesToTiers:    asStringSlice(entry["applies_to_tiers"]),
			MustMentionAny:    asStringSlice(entry["must_mention_any"]),
			MustMentionAll:    asStringSlice(entry["must_mention_all"]),
			MustNotMentionAny: asStringSlice(entry["must_not_mention_any"]),
			MustNotMatchAny:   mustNotMatchAny,
			MaxWords:          maxWords,
			MinWords:          minWords,
			ExpectVerdict:     expectVerdict,
		}
		if len(probe.MustMentionAny) == 0 && len(probe.MustMentionAll) == 0 && len(probe.MustNotMentionAny) == 0 &&
			len(probe.MustNotMatchAny) == 0 && probe.MaxWords == nil && probe.MinWords == nil && probe.ExpectVerdict == "" {
			return nil, fidelityErrorf("%s: probe %q declares no checks, so it can never fail", source, probeID)
		}
		probes = append(probes, probe)
	}
	return probes, nil
}

func valueOrEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func sortedCopy(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

// LoadFidelityProbes reads and parses a probe file (YAML).
func LoadFidelityProbes(path string) ([]FidelityProbe, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fidelityErrorf("%s: invalid YAML: %v", path, err)
	}
	// yaml.v3 into `any` produces []any of map[string]any for a top-level
	// list of mappings, matching what ParseFidelityProbes expects directly.
	return ParseFidelityProbes(raw, path)
}

// verdictLineRe: case-insensitive on both the label and the token, anchored
// at both ends so trailing prose on the same line does not match -- that is
// the model not following the format, reported as absent/malformed rather
// than silently accepted as a near-miss.
var verdictLineRe = regexp.MustCompile(`(?i)^VERDICT:\s*(REFUSE|PROCEED)\s*$`)

// ParseFidelityVerdict extracts the declared verdict token from a
// verdict-scored probe's reply. Strict about position: only the first
// non-empty line is ever considered. Returns "" for an empty reply, a first
// line that is not a VERDICT line at all, or one that carries the wrong
// token or extra trailing text.
func ParseFidelityVerdict(reply string) string {
	for _, line := range strings.Split(reply, "\n") {
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			continue
		}
		match := verdictLineRe.FindStringSubmatch(stripped)
		if match == nil {
			return ""
		}
		return strings.ToUpper(match[1])
	}
	return ""
}

var wordCharsOnlyRe = regexp.MustCompile(`^[\w'-]+$`)

// fidelityContains is case-insensitive whole-token-ish containment.
// Substring matching alone reports "sign" inside "design" -- word-boundary
// matching is used wherever the needle is a bare word, and plain
// containment where it is a phrase or carries punctuation.
func fidelityContains(haystack, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	if wordCharsOnlyRe.MatchString(needle) {
		pattern := `(?i)\b` + regexp.QuoteMeta(needle) + `\b`
		matched, _ := regexp.MatchString(pattern, haystack)
		return matched
	}
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// FidelityScoreResult is score_reply's return shape.
type FidelityScoreResult struct {
	Probe          string   `json:"probe"`
	Passed         bool     `json:"passed"`
	Failures       []string `json:"failures"`
	WordCount      int      `json:"word_count"`
	Verdict        *string  `json:"verdict,omitempty"`
	VerdictOutcome string   `json:"verdict_outcome,omitempty"`
}

// ScoreFidelityReply scores one reply. Pure: no network, no clock, no
// filesystem.
func ScoreFidelityReply(probe FidelityProbe, reply string) FidelityScoreResult {
	words := len(strings.Fields(reply))

	if probe.ExpectVerdict != "" {
		verdict := ParseFidelityVerdict(reply)
		if verdict == "" {
			other := "PROCEED"
			if probe.ExpectVerdict == "PROCEED" {
				other = "REFUSE"
			}
			return FidelityScoreResult{
				Probe:  probe.ID,
				Passed: false,
				Failures: []string{fmt.Sprintf(
					"no valid VERDICT line found (expected the first non-empty line to read exactly "+
						"'VERDICT: %s' or 'VERDICT: %s')", probe.ExpectVerdict, other)},
				WordCount:      words,
				VerdictOutcome: "malformed",
			}
		}
		if verdict == probe.ExpectVerdict {
			v := verdict
			return FidelityScoreResult{
				Probe: probe.ID, Passed: true, Failures: []string{}, WordCount: words,
				Verdict: &v, VerdictOutcome: "match",
			}
		}
		v := verdict
		return FidelityScoreResult{
			Probe:          probe.ID,
			Passed:         false,
			Failures:       []string{fmt.Sprintf("verdict was %s, expected %s", verdict, probe.ExpectVerdict)},
			WordCount:      words,
			Verdict:        &v,
			VerdictOutcome: "mismatch",
		}
	}

	var failures []string
	if len(probe.MustMentionAny) > 0 {
		any := false
		for _, k := range probe.MustMentionAny {
			if fidelityContains(reply, k) {
				any = true
				break
			}
		}
		if !any {
			failures = append(failures, "mentioned none of: "+strings.Join(probe.MustMentionAny, ", "))
		}
	}
	var missingAll []string
	for _, k := range probe.MustMentionAll {
		if !fidelityContains(reply, k) {
			missingAll = append(missingAll, k)
		}
	}
	if len(missingAll) > 0 {
		failures = append(failures, "did not mention: "+strings.Join(missingAll, ", "))
	}
	var presentBanned []string
	for _, k := range probe.MustNotMentionAny {
		if fidelityContains(reply, k) {
			presentBanned = append(presentBanned, k)
		}
	}
	if len(presentBanned) > 0 {
		failures = append(failures, "mentioned forbidden: "+strings.Join(presentBanned, ", "))
	}
	var matchedPatterns []string
	for _, p := range probe.MustNotMatchAny {
		if matched, _ := regexp.MatchString("(?i)"+p, reply); matched {
			matchedPatterns = append(matchedPatterns, p)
		}
	}
	if len(matchedPatterns) > 0 {
		failures = append(failures, "matched forbidden pattern: "+strings.Join(matchedPatterns, ", "))
	}
	if probe.MaxWords != nil && words > *probe.MaxWords {
		failures = append(failures, fmt.Sprintf("too long: %d words > max %d", words, *probe.MaxWords))
	}
	if probe.MinWords != nil && words < *probe.MinWords {
		failures = append(failures, fmt.Sprintf("too short: %d words < min %d", words, *probe.MinWords))
	}

	return FidelityScoreResult{
		Probe:     probe.ID,
		Passed:    len(failures) == 0,
		Failures:  failures,
		WordCount: words,
	}
}

// DegenerateFidelityKeywords returns keywords a model could pass by copying
// out of the brief it was given -- a probe asserting a word the role's own
// text already contains verbatim tests retrieval, not that the role's
// constraints shaped the answer.
func DegenerateFidelityKeywords(probe FidelityProbe, preset FidelityPreset) []string {
	candidates := append(append([]string{}, probe.MustMentionAny...), probe.MustMentionAll...)
	seen := map[string]bool{}
	for _, k := range candidates {
		if fidelityContains(preset.Body, k) {
			seen[k] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
