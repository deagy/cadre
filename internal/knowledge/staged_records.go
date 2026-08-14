package knowledge

// Well-formedness validation for staged proposed-knowledge records.
//
// A staged record is a frontmatter document: a `---`-delimited block of
// restricted YAML followed by a Markdown body. It is the durable, auditable
// destination for one item an agent listed under `knowledge_steward_handoffs`,
// and the place `knowledge-store-steward` records that item's disposition.
//
// This is a Go port of roster/knowledge-store/src/staged_records.py, which was
// deleted by b418031e together with the rest of the staged-records subsystem.
// The contract it implements is unchanged; see
// roster/knowledge-store/SECURITY.md ("Self-approval, and how far the guards
// against it actually reach") for the authoritative description of the
// self-approval checks this subsystem exists to enforce.
//
// What this file does NOT establish, and cannot: it does not verify that an
// agent *emitted* a `knowledge_steward_handoffs` item. A handoff is free-form
// agent output; there is no emission event to observe. An empty staging table
// is therefore valid here, and its validity says nothing about whether
// knowledge was lost.
//
// Reporting style: findings are returned, not raised, so every independent
// defect in a record surfaces in one pass rather than only the first. A
// structural failure that makes the frontmatter unreadable at all is the one
// exception -- that is a RecordFormatError, because there is nothing left to
// report findings about.
//
// No YAML dependency. The parser accepts exactly the subset the contract needs
// (top-level scalars, one level of nested mapping, one level of string list)
// and fails loudly on anything else -- block scalars, flow collections,
// anchors, three-level nesting -- so an unsupported construct is refused
// rather than silently misread.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// StagedDelimiter opens and closes a staged record's frontmatter block.
const StagedDelimiter = "---"

// StagedRequiredKeys are the frontmatter keys every staged record must carry,
// in canonical order. Serialization emits them in exactly this order so an
// export diff shows content changes rather than map ordering.
var StagedRequiredKeys = []string{
	"id",
	"title",
	"status",
	"evidence",
	"origin",
	"proposed_classification",
	"source_scope",
	"sensitivity_notes",
	"conflicts_or_staleness",
	"recommended_action",
	"untrusted_instruction_risk",
	"staged_by",
	"content_digest",
}

// StagedOptionalKeys are permitted but not required. `disposition` appears
// only once a steward has decided.
var StagedOptionalKeys = []string{"disposition"}

// StagedStatusValues, StagedRecommendedActions and StagedDispositionActions
// are the closed enumerations the contract allows.
var (
	StagedStatusValues       = []string{"proposed", "accepted", "rejected", "deferred"}
	StagedRecommendedActions = []string{"ingest", "update", "reclassify", "defer"}
	StagedDispositionActions = []string{"accepted", "rejected", "deferred"}
)

// stagedOriginKeys and stagedDispositionKeys are the required sub-keys of the
// two nested mappings in the contract.
var (
	stagedOriginKeys      = []string{"task", "artifact", "revision"}
	stagedDispositionKeys = []string{
		"action",
		"reason",
		"classification_used",
		"diverged_from_proposal",
		"decided_by",
	}
)

// stagedNonEmptyStringKeys are required non-empty plain strings with no
// further constraint.
var stagedNonEmptyStringKeys = []string{
	"title",
	"proposed_classification",
	"source_scope",
	"staged_by",
}

// stagedPossiblyEmptyStringKeys are required strings that may be empty. The
// key must still be present, so "nobody considered it" stays distinguishable
// from "considered, nothing found".
var stagedPossiblyEmptyStringKeys = []string{"sensitivity_notes", "conflicts_or_staleness"}

var (
	stagedIDPattern     = regexp.MustCompile(`^KS-\d{8}-[a-z0-9-]+$`)
	stagedDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	stagedKeyLine       = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*):(?:[ \t]+(.*))?$`)
	stagedDrivePath     = regexp.MustCompile(`[A-Za-z]:[\\/]`)
)

const stagedRedactionRule = "staged records carry the same source_uri redaction rule as knowledge citations " +
	"(roster/shared/knowledge-use-policy.md: omit or redact nested citation source_uri by default, because it " +
	"may expose a local path). Use a repository-relative path or a redacted reference instead"

const stagedSchemaPointer = "see roster/knowledge-store/proposed-knowledge.schema.json for the frontmatter contract"

// RecordFormatError means the text is not a parseable staged record at all.
//
// Returned only for structural failures that make the frontmatter unreadable
// (missing or unclosed delimiter, unsupported YAML construct, malformed line).
// Contract violations in an otherwise-parseable record are reported as
// findings, never as this error.
type RecordFormatError struct {
	Message string
}

func (e *RecordFormatError) Error() string { return e.Message }

func recordFormatErrorf(format string, args ...any) error {
	return &RecordFormatError{Message: fmt.Sprintf(format, args...)}
}

// ComputeStagedDigest returns the lowercase sha256 hex of body under the
// canonical normalization.
//
// The normalization is defined here, once, and every producer and consumer of
// `content_digest` must go through this function so the digest can never be
// computed two ways:
//
//  1. The body is everything after the closing `---` delimiter line. Amending
//     `status` or appending a `disposition` therefore never invalidates the
//     digest of unchanged prose: the digest pins the *claim*, while the
//     frontmatter records the claim's lifecycle.
//  2. CRLF and lone CR are normalized to LF, so a record checked out with CRLF
//     line endings digests identically to the same record with LF endings.
//  3. Leading and trailing whitespace is stripped.
//  4. The result is encoded UTF-8 and hashed with sha256.
//
// No other transformation is applied: interior whitespace, blank lines between
// paragraphs, and Markdown structure are all significant.
func ComputeStagedDigest(body string) string {
	normalized := strings.TrimSpace(normalizeNewlines(body))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func normalizeNewlines(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

// pendingBlock marks a key whose block body has not been seen yet.
type pendingBlock struct{}

// parseStagedScalar parses one frontmatter scalar from the text after `key:`
// or `- `.
//
// Deliberately restricted relative to YAML: true/false become booleans,
// null/~ become nil (so a wrong type is reported rather than silently accepted
// as a string), quoted strings are unquoted, and every other bare token stays
// a string -- numbers and dates are NOT converted, because no field in the
// contract is numeric and a surprising coercion is worse than a plain string.
// Constructs this parser cannot represent faithfully are refused rather than
// approximated.
func parseStagedScalar(text string, lineNumber int) (any, error) {
	value := strings.TrimSpace(text)
	switch value {
	case "|", ">", "|-", ">-", "|+", ">+":
		return nil, recordFormatErrorf(
			"line %d: block scalars (%q) are not supported by the staged-record frontmatter parser; "+
				"put long prose in the record body and keep frontmatter values on one line", lineNumber, value)
	}
	if strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") {
		return nil, recordFormatErrorf(
			"line %d: flow-style collections are not supported by the staged-record frontmatter parser; "+
				"use an indented block list (`- item`) or block mapping instead", lineNumber)
	}
	if strings.HasPrefix(value, "&") || strings.HasPrefix(value, "*") {
		return nil, recordFormatErrorf(
			"line %d: YAML anchors/aliases are not supported by the staged-record frontmatter parser; "+
				"write the value out in full", lineNumber)
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		inner := value[1 : len(value)-1]
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		inner = strings.ReplaceAll(inner, `\\`, `\`)
		return inner, nil
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), nil
	}
	switch value {
	case "true", "True", "TRUE":
		return true, nil
	case "false", "False", "FALSE":
		return false, nil
	case "null", "Null", "NULL", "~":
		return nil, nil
	}
	return value, nil
}

// ParseStagedFrontmatter parses a frontmatter block into a mapping.
//
// firstLineNumber is the file line number of the block's first line, so error
// messages point at the real location in the record rather than at an offset
// into an extracted substring.
func ParseStagedFrontmatter(block string, firstLineNumber int) (map[string]any, error) {
	result := map[string]any{}
	order := []string{}
	currentKey := ""

	for offset, line := range strings.Split(block, "\n") {
		lineNumber := firstLineNumber + offset
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			continue
		}

		indentText := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if strings.Contains(indentText, "\t") {
			return nil, recordFormatErrorf(
				"line %d: tab indentation is not valid YAML frontmatter; use spaces", lineNumber)
		}

		if len(indentText) == 0 {
			match := stagedKeyLine.FindStringSubmatch(strings.TrimRight(line, " \t"))
			if match == nil {
				return nil, recordFormatErrorf(
					"line %d: expected a top-level `key: value` or `key:` line, got %q", lineNumber, stripped)
			}
			key, rawValue := match[1], match[2]
			if _, exists := result[key]; exists {
				return nil, recordFormatErrorf(
					"line %d: duplicate frontmatter key %q; the later value would silently overwrite "+
						"the earlier one", lineNumber, key)
			}
			order = append(order, key)
			if strings.TrimSpace(rawValue) == "" {
				result[key] = pendingBlock{}
				currentKey = key
				continue
			}
			scalar, err := parseStagedScalar(rawValue, lineNumber)
			if err != nil {
				return nil, err
			}
			result[key] = scalar
			currentKey = ""
			continue
		}

		if currentKey == "" {
			return nil, recordFormatErrorf(
				"line %d: indented line %q does not belong to any preceding key", lineNumber, stripped)
		}
		if err := parseStagedNestedLine(result, currentKey, stripped, lineNumber); err != nil {
			return nil, err
		}
	}

	for _, key := range order {
		if _, pending := result[key].(pendingBlock); pending {
			return nil, recordFormatErrorf(
				"frontmatter key %q opens a block but has no entries; give it a scalar on the same "+
					"line, or an indented list/mapping beneath it", key)
		}
	}
	return result, nil
}

func parseStagedNestedLine(result map[string]any, currentKey, stripped string, lineNumber int) error {
	container := result[currentKey]
	_, pending := container.(pendingBlock)

	if stripped == "-" || strings.HasPrefix(stripped, "- ") {
		if pending {
			container = []any{}
		}
		list, ok := container.([]any)
		if !ok {
			return recordFormatErrorf(
				"line %d: key %q already has mapping entries; a key is either a list or a mapping, "+
					"not both", lineNumber, currentKey)
		}
		scalar, err := parseStagedScalar(strings.TrimPrefix(stripped, "-"), lineNumber)
		if err != nil {
			return err
		}
		result[currentKey] = append(list, scalar)
		return nil
	}

	match := stagedKeyLine.FindStringSubmatch(stripped)
	if match == nil {
		return recordFormatErrorf(
			"line %d: expected a nested `key: value` line or a `- item` list entry under %q, got %q",
			lineNumber, currentKey, stripped)
	}
	subKey, rawValue := match[1], match[2]
	if pending {
		container = map[string]any{}
	}
	mapping, ok := container.(map[string]any)
	if !ok {
		return recordFormatErrorf(
			"line %d: key %q already has list entries; a key is either a list or a mapping, not both",
			lineNumber, currentKey)
	}
	if _, exists := mapping[subKey]; exists {
		return recordFormatErrorf(
			"line %d: duplicate key %q under %q; the later value would silently overwrite the "+
				"earlier one", lineNumber, subKey, currentKey)
	}
	if strings.TrimSpace(rawValue) == "" {
		return recordFormatErrorf(
			"line %d: %s.%s has no value; the staged-record frontmatter parser supports one level of "+
				"nesting only, so a nested key must carry a scalar on the same line",
			lineNumber, currentKey, subKey)
	}
	scalar, err := parseStagedScalar(rawValue, lineNumber)
	if err != nil {
		return err
	}
	mapping[subKey] = scalar
	result[currentKey] = mapping
	return nil
}

// ParseStagedRecord splits a staged record into its frontmatter mapping and
// body. The returned body has newlines normalized to LF but is otherwise
// unmodified; ComputeStagedDigest applies the remaining normalization.
func ParseStagedRecord(text string) (map[string]any, string, error) {
	normalized := normalizeNewlines(strings.TrimPrefix(text, "\ufeff"))
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != StagedDelimiter {
		return nil, "", recordFormatErrorf(
			"record must begin with a %q frontmatter delimiter on its first line (%s)",
			StagedDelimiter, stagedSchemaPointer)
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == StagedDelimiter {
			closing = i
			break
		}
	}
	if closing < 0 {
		return nil, "", recordFormatErrorf(
			"frontmatter is never closed: no %q line after the opening delimiter (%s)",
			StagedDelimiter, stagedSchemaPointer)
	}
	frontmatter, err := ParseStagedFrontmatter(strings.Join(lines[1:closing], "\n"), 2)
	if err != nil {
		return nil, "", err
	}
	return frontmatter, strings.Join(lines[closing+1:], "\n"), nil
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

func stagedTypeName(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "bool"
	case []any:
		return "list"
	case map[string]any:
		return "mapping"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func checkStagedString(fm map[string]any, key string, findings *[]string, allowEmpty bool) (string, bool) {
	raw, present := fm[key]
	if !present {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		*findings = append(*findings, fmt.Sprintf(
			"frontmatter key %q must be a string, got %s %#v (%s)", key, stagedTypeName(raw), raw, stagedSchemaPointer))
		return "", false
	}
	if !allowEmpty && strings.TrimSpace(value) == "" {
		*findings = append(*findings, fmt.Sprintf(
			"frontmatter key %q must be a non-empty string, got %q (%s)", key, value, stagedSchemaPointer))
		return "", false
	}
	return value, true
}

func checkStagedEnum(fm map[string]any, key string, allowed []string, findings *[]string) (string, bool) {
	raw, present := fm[key]
	if !present {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		*findings = append(*findings, fmt.Sprintf(
			"frontmatter key %q must be a string, got %s %#v; allowed values are %s",
			key, stagedTypeName(raw), raw, strings.Join(allowed, ", ")))
		return "", false
	}
	if !containsString(allowed, value) {
		*findings = append(*findings, fmt.Sprintf(
			"frontmatter key %q must be one of %s; got %q", key, strings.Join(allowed, ", "), value))
		return "", false
	}
	return value, true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func checkStagedKeyPresence(fm map[string]any) []string {
	var findings []string
	known := append(append([]string{}, StagedRequiredKeys...), StagedOptionalKeys...)
	var unknown []string
	for key := range fm {
		if !containsString(known, key) {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		findings = append(findings, fmt.Sprintf(
			"unknown top-level frontmatter key %q: the staged-record frontmatter contract is closed. "+
				"Permitted keys are %s and the optional %s. Put anything else in the record body (%s)",
			key, strings.Join(StagedRequiredKeys, ", "), strings.Join(StagedOptionalKeys, ", "), stagedSchemaPointer))
	}
	for _, key := range StagedRequiredKeys {
		if _, present := fm[key]; !present {
			findings = append(findings, fmt.Sprintf(
				"missing required frontmatter key %q (%s)", key, stagedSchemaPointer))
		}
	}
	return findings
}

func checkStagedID(fm map[string]any, findings *[]string) {
	value, ok := checkStagedString(fm, "id", findings, false)
	if !ok {
		return
	}
	if !stagedIDPattern.MatchString(value) {
		*findings = append(*findings, fmt.Sprintf(
			"frontmatter key 'id' must match %s (KS-YYYYMMDD-slug, lowercase slug); got %q. The id is "+
				"immutable once assigned, because the disposition and any downstream ingestion reference it",
			stagedIDPattern.String(), value))
	}
}

func checkStagedEvidence(fm map[string]any, findings *[]string) {
	raw, present := fm["evidence"]
	if !present {
		return
	}
	list, ok := raw.([]any)
	if !ok {
		*findings = append(*findings, fmt.Sprintf(
			"frontmatter key 'evidence' must be a list of strings, got %s %#v; write it as an indented "+
				"`- item` block", stagedTypeName(raw), raw))
		return
	}
	if len(list) == 0 {
		*findings = append(*findings,
			"frontmatter key 'evidence' must be a non-empty list: a record with no citation or file:line "+
				"reference is an unsupported claim, which the steward cannot triage")
		return
	}
	for index, entry := range list {
		text, ok := entry.(string)
		if !ok || strings.TrimSpace(text) == "" {
			*findings = append(*findings, fmt.Sprintf(
				"evidence[%d] must be a non-empty string, got %s %#v", index, stagedTypeName(entry), entry))
		}
	}
}

func checkStagedOrigin(fm map[string]any, findings *[]string) {
	raw, present := fm["origin"]
	if !present {
		return
	}
	mapping, ok := raw.(map[string]any)
	if !ok {
		*findings = append(*findings, fmt.Sprintf(
			"frontmatter key 'origin' must be a mapping with keys %s, got %s %#v",
			strings.Join(stagedOriginKeys, ", "), stagedTypeName(raw), raw))
		return
	}
	for _, key := range stagedOriginKeys {
		entry, present := mapping[key]
		if !present {
			*findings = append(*findings, fmt.Sprintf(
				"origin is missing required key %q: provenance needs all of %s to stay point-in-time "+
					"attributable", key, strings.Join(stagedOriginKeys, ", ")))
			continue
		}
		text, ok := entry.(string)
		if !ok || strings.TrimSpace(text) == "" {
			*findings = append(*findings, fmt.Sprintf(
				"origin.%s must be a non-empty string, got %s %#v", key, stagedTypeName(entry), entry))
		}
	}
}

// checkStagedUntrustedInstructionRisk returns the raw risk value when it is one
// of the three the contract allows: the boolean true, the boolean false, or
// the string "unknown".
func checkStagedUntrustedInstructionRisk(fm map[string]any, findings *[]string) any {
	raw, present := fm["untrusted_instruction_risk"]
	if !present {
		return nil
	}
	if _, ok := raw.(bool); ok {
		return raw
	}
	if text, ok := raw.(string); ok && text == "unknown" {
		return raw
	}
	*findings = append(*findings, fmt.Sprintf(
		"frontmatter key 'untrusted_instruction_risk' must be the boolean true, the boolean false, or the "+
			"string 'unknown'; got %s %#v. Quote nothing for the booleans -- a quoted \"true\" is the "+
			"string, not the boolean", stagedTypeName(raw), raw))
	return nil
}

func checkStagedRecommendedAction(fm map[string]any, findings *[]string) {
	if fm["recommended_action"] == "delete" {
		*findings = append(*findings,
			"recommended_action 'delete' is not an available action: proposing a deletion and being "+
				"authorized to perform one are different acts. This validator runs on an agent's own "+
				"proposal, and an agent may propose a deletion only by escalating it, never by recording "+
				"it as a self-service staged-record action. A required deletion escalates to "+
				"knowledge-store-steward and an authorized human, with evidence-custodian coordination "+
				"(roster/knowledge-store/AGENT.md, 'Escalate when'). Use 'defer' and state the deletion "+
				"request in sensitivity_notes")
		return
	}
	checkStagedEnum(fm, "recommended_action", StagedRecommendedActions, findings)
}

func checkStagedDigest(fm map[string]any, body string, findings *[]string) {
	value, ok := checkStagedString(fm, "content_digest", findings, false)
	if !ok {
		return
	}
	if !stagedDigestPattern.MatchString(value) {
		*findings = append(*findings, fmt.Sprintf(
			"frontmatter key 'content_digest' must be 64 lowercase hex characters (a sha256 digest); got %q",
			value))
		return
	}
	expected := ComputeStagedDigest(body)
	if value != expected {
		*findings = append(*findings, fmt.Sprintf(
			"content_digest does not match the record body: declared %s, computed %s. Recompute it with "+
				"ComputeStagedDigest(body) -- the body is everything after the closing '---' line, with "+
				"CRLF normalised to LF and leading/trailing whitespace stripped. Never compute it by hand: "+
				"a second implementation of the normalisation is how a digest silently stops meaning anything",
			value, expected))
	}
}

func checkStagedDispositionShape(fm map[string]any, findings *[]string) map[string]any {
	raw, present := fm["disposition"]
	if !present {
		return nil
	}
	mapping, ok := raw.(map[string]any)
	if !ok {
		*findings = append(*findings, fmt.Sprintf(
			"frontmatter key 'disposition' must be a mapping with keys %s, got %s %#v",
			strings.Join(stagedDispositionKeys, ", "), stagedTypeName(raw), raw))
		return nil
	}
	for _, key := range stagedDispositionKeys {
		if _, present := mapping[key]; !present {
			*findings = append(*findings, fmt.Sprintf(
				"disposition is missing required key %q: a disposition without all of %s is not an audit trail",
				key, strings.Join(stagedDispositionKeys, ", ")))
		}
	}
	if action, present := mapping["action"]; present {
		text, ok := action.(string)
		if !ok || !containsString(StagedDispositionActions, text) {
			*findings = append(*findings, fmt.Sprintf(
				"disposition.action must be one of %s; got %#v",
				strings.Join(StagedDispositionActions, ", "), action))
		}
	}
	if reason, present := mapping["reason"]; present {
		text, ok := reason.(string)
		if !ok || strings.TrimSpace(text) == "" {
			*findings = append(*findings, fmt.Sprintf(
				"disposition.reason must be a non-empty string, got %s %#v: an unexplained disposition "+
					"cannot be reviewed", stagedTypeName(reason), reason))
		}
	}
	if used, present := mapping["classification_used"]; present {
		if _, ok := used.(string); !ok {
			*findings = append(*findings, fmt.Sprintf(
				"disposition.classification_used must be a string, got %s %#v", stagedTypeName(used), used))
		}
	}
	if diverged, present := mapping["diverged_from_proposal"]; present {
		if _, ok := diverged.(bool); !ok {
			*findings = append(*findings, fmt.Sprintf(
				"disposition.diverged_from_proposal must be the boolean true or false, got %s %#v",
				stagedTypeName(diverged), diverged))
		}
	}
	if decided, present := mapping["decided_by"]; present {
		if _, ok := decided.(string); !ok {
			*findings = append(*findings, fmt.Sprintf(
				"disposition.decided_by must be a string, got %s %#v", stagedTypeName(decided), decided))
		}
	}
	return mapping
}

func checkStagedStatusDispositionCoherence(
	status string, statusOK bool, disposition map[string]any, hasDispositionKey bool, findings *[]string,
) {
	if !statusOK {
		return
	}
	if status == "proposed" {
		if hasDispositionKey {
			*findings = append(*findings,
				"status 'proposed' requires 'disposition' to be absent: a proposed record has not been "+
					"dispositioned yet, and a disposition present alongside it makes the record's real state "+
					"unreadable. Set status to the disposition's action, or remove the disposition")
		}
		return
	}
	if !hasDispositionKey {
		*findings = append(*findings, fmt.Sprintf(
			"status %q requires a 'disposition' mapping: a dispositioned record must record the decision "+
				"(action, reason, classification_used, diverged_from_proposal, decided_by), otherwise the "+
				"proposal has an outcome with no audit linkage back to who decided it and why", status))
		return
	}
	if disposition == nil {
		return
	}
	if action, ok := disposition["action"].(string); ok && containsString(StagedDispositionActions, action) && action != status {
		*findings = append(*findings, fmt.Sprintf(
			"disposition.action %q does not match status %q: the two must agree, so the record's state "+
				"cannot be read two ways", action, status))
	}
}

// checkStagedAutomaticDefer enforces the automatic-defer rule: an
// injection-risk or uncertain-risk candidate is deferred and escalated, never
// accepted or rejected on the steward's discretion alone.
func checkStagedAutomaticDefer(risk any, status string, disposition map[string]any, findings *[]string) {
	if !stagedRiskIsElevated(risk) {
		return
	}
	if status == "accepted" {
		*findings = append(*findings,
			"untrusted_instruction_risk is true or unknown, so status must not be 'accepted': the "+
				"automatic-defer rule makes an injection-risk candidate a defer, not a discretionary approval "+
				"(roster/knowledge-store/AGENT.md: 'defer untrusted_instruction_risk: true or unknown'). Use "+
				"`cadre knowledge disposition-staged` to amend the disposition, not `import-staged` to import "+
				"contradictory records")
	}
	if disposition == nil {
		return
	}
	if action, ok := disposition["action"].(string); ok && containsString(StagedDispositionActions, action) && action != "deferred" {
		*findings = append(*findings, fmt.Sprintf(
			"untrusted_instruction_risk is true or unknown, so disposition.action must be 'deferred'; got %q. "+
				"This is the automatic-defer rule: an injection-risk or uncertain-risk candidate is deferred "+
				"and escalated, never accepted or rejected on the steward's discretion alone. Use "+
				"`cadre knowledge disposition-staged` to amend", action))
	}
}

// stagedRiskIsElevated reports whether untrusted_instruction_risk is the
// boolean true or the string "unknown" -- the two values the automatic-defer
// rule treats identically.
func stagedRiskIsElevated(risk any) bool {
	if flag, ok := risk.(bool); ok {
		return flag
	}
	text, ok := risk.(string)
	return ok && text == "unknown"
}

// stagedAbsolutePathHit names the absolute-local-path shape value leaks, or
// returns "" when it leaks none.
//
// The Windows form requires a single drive letter not preceded by another
// alphanumeric, and the POSIX forms require no preceding word character, so a
// `file.py:12` reference and an `https://` URL do not false-positive. Go's
// regexp has no lookbehind, so the preceding-character constraint is applied
// by hand rather than by pattern.
func stagedAbsolutePathHit(value string) string {
	for _, candidate := range []struct{ literal, label string }{
		{"/home/", "/home/"},
		{"/Users/", "/Users/"},
		{"~/", "~/"},
	} {
		if indexNotPrecededBy(value, candidate.literal, isStagedWordByte) >= 0 {
			return candidate.label
		}
	}
	for _, loc := range stagedDrivePath.FindAllStringIndex(value, -1) {
		if loc[0] == 0 || !isStagedAlphanumericByte(value[loc[0]-1]) {
			return `a Windows drive path such as C:\`
		}
	}
	return ""
}

// indexNotPrecededBy returns the index of the first occurrence of literal in
// value whose preceding byte does not satisfy excluded, or -1.
func indexNotPrecededBy(value, literal string, excluded func(byte) bool) int {
	for offset := 0; offset < len(value); {
		found := strings.Index(value[offset:], literal)
		if found < 0 {
			return -1
		}
		at := offset + found
		if at == 0 || !excluded(value[at-1]) {
			return at
		}
		offset = at + 1
	}
	return -1
}

func isStagedWordByte(b byte) bool {
	return isStagedAlphanumericByte(b) || b == '_' || b == '.' || b == '-'
}

func isStagedAlphanumericByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func checkStagedAbsolutePaths(fm map[string]any, findings *[]string) {
	if evidence, ok := fm["evidence"].([]any); ok {
		for index, entry := range evidence {
			text, ok := entry.(string)
			if !ok {
				continue
			}
			if label := stagedAbsolutePathHit(text); label != "" {
				*findings = append(*findings, fmt.Sprintf(
					"evidence[%d] contains an absolute local path (%s): %q. %s",
					index, label, text, stagedRedactionRule))
			}
		}
	}
	if origin, ok := fm["origin"].(map[string]any); ok {
		for _, key := range stagedOriginKeys {
			text, ok := origin[key].(string)
			if !ok {
				continue
			}
			if label := stagedAbsolutePathHit(text); label != "" {
				*findings = append(*findings, fmt.Sprintf(
					"origin.%s contains an absolute local path (%s): %q. %s",
					key, label, text, stagedRedactionRule))
			}
		}
	}
}

// ValidateStagedRecord validates an already-parsed record. An empty result
// means valid; every independent defect surfaces in one pass.
func ValidateStagedRecord(fm map[string]any, body string) []string {
	findings := checkStagedKeyPresence(fm)

	checkStagedID(fm, &findings)
	for _, key := range stagedNonEmptyStringKeys {
		checkStagedString(fm, key, &findings, false)
	}
	for _, key := range stagedPossiblyEmptyStringKeys {
		checkStagedString(fm, key, &findings, true)
	}
	status, statusOK := checkStagedEnum(fm, "status", StagedStatusValues, &findings)
	checkStagedEvidence(fm, &findings)
	checkStagedOrigin(fm, &findings)
	checkStagedRecommendedAction(fm, &findings)
	risk := checkStagedUntrustedInstructionRisk(fm, &findings)
	checkStagedDigest(fm, body, &findings)

	_, hasDisposition := fm["disposition"]
	disposition := checkStagedDispositionShape(fm, &findings)
	checkStagedStatusDispositionCoherence(status, statusOK, disposition, hasDisposition, &findings)
	checkStagedAutomaticDefer(risk, status, disposition, &findings)
	checkStagedAbsolutePaths(fm, &findings)
	return findings
}

// ValidateStagedRecordText parses and validates one staged record's text.
// A parse failure is reported as a single finding, so a caller that only cares
// whether a record is acceptable does not have to distinguish the two error
// shapes.
func ValidateStagedRecordText(text string) []string {
	fm, body, err := ParseStagedRecord(text)
	if err != nil {
		return []string{err.Error()}
	}
	return ValidateStagedRecord(fm, body)
}

// ---------------------------------------------------------------------------
// Serialisation
// ---------------------------------------------------------------------------

// stagedTrailingKeys are serialised last, after every required key. Since
// `disposition` is not required (it is absent until a steward acts), it needs
// a fixed position of its own.
var stagedTrailingKeys = []string{"disposition"}

// stagedScalar renders one frontmatter scalar in a form parseStagedScalar
// reads back exactly.
//
// Booleans and nil are emitted bare, because the parser converts those tokens
// to their typed values and quoting them would round-trip a bool into the
// string "true". Everything else is double-quoted and escaped, which is always
// safe: quoting removes every ambiguity the parser would otherwise have to
// resolve (a leading `-`, an interior `: `, a value that looks like `null`,
// leading or trailing spaces).
func stagedScalar(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "null", nil
	case bool:
		if typed {
			return "true", nil
		}
		return "false", nil
	case string:
		escaped := strings.ReplaceAll(typed, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`, nil
	default:
		return "", &StagedRecordError{Message: fmt.Sprintf(
			"cannot serialise frontmatter value of type %s: the staged-record contract has only strings, "+
				"booleans, string lists, and one level of nested mapping", stagedTypeName(value))}
	}
}

// stagedMappingOrder returns the sub-keys of a nested mapping in a fixed
// order: the contract's own order first, then anything else sorted. Go map
// iteration order is randomised, so an explicit order is what makes
// serialisation deterministic and an export diff meaningful.
func stagedMappingOrder(key string, mapping map[string]any) []string {
	var preferred []string
	switch key {
	case "origin":
		preferred = stagedOriginKeys
	case "disposition":
		preferred = stagedDispositionKeys
	}
	order := make([]string, 0, len(mapping))
	for _, candidate := range preferred {
		if _, present := mapping[candidate]; present {
			order = append(order, candidate)
		}
	}
	var rest []string
	for candidate := range mapping {
		if !containsString(preferred, candidate) {
			rest = append(rest, candidate)
		}
	}
	sort.Strings(rest)
	return append(order, rest...)
}

func emitStagedKey(key string, value any, lines *[]string) error {
	switch typed := value.(type) {
	case []any:
		*lines = append(*lines, key+":")
		for _, item := range typed {
			rendered, err := stagedScalar(item)
			if err != nil {
				return err
			}
			*lines = append(*lines, "  - "+rendered)
		}
		return nil
	case map[string]any:
		*lines = append(*lines, key+":")
		for _, subKey := range stagedMappingOrder(key, typed) {
			subValue := typed[subKey]
			switch subValue.(type) {
			case []any, map[string]any:
				return &StagedRecordError{Message: fmt.Sprintf(
					"%s.%s: the staged-record frontmatter parser supports one level of nesting, so a "+
						"nested list or mapping cannot be represented", key, subKey)}
			}
			rendered, err := stagedScalar(subValue)
			if err != nil {
				return err
			}
			*lines = append(*lines, "  "+subKey+": "+rendered)
		}
		return nil
	default:
		rendered, err := stagedScalar(value)
		if err != nil {
			return err
		}
		*lines = append(*lines, key+": "+rendered)
		return nil
	}
}

// SerializeStagedRecord renders (frontmatter, body) back to staged-record text.
//
// Key order is fixed -- StagedRequiredKeys order, then `disposition`, then any
// remaining key sorted -- so the same record always serialises identically and
// an export diff shows content changes rather than map ordering. A key outside
// the contract is emitted after the known ones rather than dropped: silently
// discarding an unrecognised key is how an export loses data, and the
// validator rejects it on the way back in anyway.
func SerializeStagedRecord(fm map[string]any, body string) (string, error) {
	known := append(append([]string{}, StagedRequiredKeys...), stagedTrailingKeys...)
	lines := []string{StagedDelimiter}
	for _, key := range known {
		if value, present := fm[key]; present {
			if err := emitStagedKey(key, value, &lines); err != nil {
				return "", err
			}
		}
	}
	var extras []string
	for key := range fm {
		if !containsString(known, key) {
			extras = append(extras, key)
		}
	}
	sort.Strings(extras)
	for _, key := range extras {
		if err := emitStagedKey(key, fm[key], &lines); err != nil {
			return "", err
		}
	}
	lines = append(lines, StagedDelimiter)

	text := strings.Join(lines, "\n") + "\n"
	normalizedBody := normalizeNewlines(body)
	if !strings.HasPrefix(normalizedBody, "\n") {
		text += "\n"
	}
	return text + normalizedBody, nil
}
