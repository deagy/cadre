package generators

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Port of roster/orchestration/src/generate_role_metadata.py's catalog.yaml
// renderer and routing.json knowledge_focus splicer. Both must be byte-exact:
// the committed catalog.yaml and routing.json are compared against them by
// `cadre generate-role-metadata --check`.

// catalogFieldOrder is the fixed per-role field order in catalog.yaml.
// knowledge_focus is deliberately absent: it lives in routing.json.
var catalogFieldOrder = []string{"definition", "phase", "capability", "model", "codex_model", "reasoning_effort"}

// rolePrefixComments is the historic hand-authored comment that sits directly
// above the first `phase: authority` role block. It documents authority-aide
// policy, not any one role's metadata, so it does not belong in frontmatter --
// it is reproduced here verbatim so the rendered catalog.yaml stays
// byte-identical to the hand-authored original. It is pinned to a specific id
// only because that is where it already lives today; if product-owner-aide is
// ever reordered behind another authority role, this must move to whichever
// role becomes the first `phase: authority` entry.
var rolePrefixComments = map[string]string{
	"product-owner-aide": "  # `phase: authority` roles below prepare the decision package a human\n" +
		"  # lifecycle authority needs for their assigned gate(s); they never approve,\n" +
		"  # recommend a disposition, or hold delegated authority themselves (see\n" +
		"  # docs/proposals/human-authority-role-agents.md). All read_only/opus per the\n" +
		"  # design doc's rationale: these support high-blast-radius, hard-to-reverse\n" +
		"  # human judgment calls even though the aide itself only assembles evidence.\n",
}

const knowledgeFocusAnchor = `  "knowledge_focus": {`

// RenderCatalog generates the complete catalog.yaml content. The header
// template already ends with the `agents:` line, so this appends role blocks
// directly to it.
func RenderCatalog(roles []RoleMetadata, headerTemplate string) (string, error) {
	var b strings.Builder
	b.WriteString(headerTemplate)
	for _, role := range roles {
		b.WriteString(rolePrefixComments[role.ID])
		fields := map[string]string{
			"definition":       role.Definition,
			"phase":            role.Phase,
			"capability":       role.Capability,
			"model":            role.Model,
			"codex_model":      role.CodexModel,
			"reasoning_effort": role.ReasoningEffort,
		}
		lines := []string{"  " + role.ID + ":"}
		for _, field := range catalogFieldOrder {
			lines = append(lines, "    "+field+": "+fields[field])
		}
		b.WriteString(strings.Join(lines, "\n") + "\n")
	}
	return b.String(), nil
}

// LoadCatalogHeader loads the catalog header template.
func LoadCatalogHeader(templatePath string) (string, error) {
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("cannot read catalog header template: %w", err)
	}
	return string(content), nil
}

// pyJSONStringUnicode renders s the way Python's
// json.dumps(s, ensure_ascii=False) does: escape only the characters JSON
// requires, leaving non-ASCII prose as its literal character. Used for
// routing.json's knowledge_focus rows, where today's prose is all-ASCII but a
// future em dash should render as itself rather than —.
func pyJSONStringUnicode(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// findKnowledgeFocusBlock locates the byte offsets of the `{` and its matching
// `}` for routing.json's knowledge_focus object.
func findKnowledgeFocusBlock(text string) (int, int, error) {
	anchor := regexp.QuoteMeta(knowledgeFocusAnchor)
	occurrences := regexp.MustCompile(anchor).FindAllStringIndex(text, -1)
	if len(occurrences) != 1 {
		return 0, 0, fmt.Errorf(
			"expected exactly one %q anchor line in routing.json, found %d",
			knowledgeFocusAnchor, len(occurrences))
	}
	openBrace := strings.Index(text[occurrences[0][0]:], "{")
	if openBrace < 0 {
		return 0, 0, fmt.Errorf("no '{' after the knowledge_focus anchor")
	}
	openBrace += occurrences[0][0]

	depth := 0
	inString := false
	escape := false
	for index := openBrace; index < len(text); index++ {
		character := text[index]
		if inString {
			switch {
			case escape:
				escape = false
			case character == '\\':
				escape = true
			case character == '"':
				inString = false
			}
			continue
		}
		switch character {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return openBrace, index, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("could not find a matching closing '}' for the knowledge_focus block")
}

// SpliceKnowledgeFocus surgically replaces only the `"knowledge_focus": { ... }`
// region of routing.json's raw source, leaving every other byte untouched.
//
// Row order within the rebuilt block preserves each already-present role id's
// existing position, so an unchanged role set reproduces the original bytes
// exactly -- today's routing.json key order does not match catalog-order.txt's
// dispatch-precedence order, the two orders are independent, and this
// generator does not try to force them to match. Any role id newly present
// that was not already in the block is appended in catalog-order.txt order.
func SpliceKnowledgeFocus(originalText string, roles []RoleMetadata) (string, error) {
	openBrace, closeBrace, err := findKnowledgeFocusBlock(originalText)
	if err != nil {
		return "", err
	}

	focus := map[string]string{}
	orderIDs := make([]string, 0, len(roles))
	for _, role := range roles {
		focus[role.ID] = role.KnowledgeFocus
		orderIDs = append(orderIDs, role.ID)
	}

	original, err := parseOrderedJSON([]byte(originalText[openBrace : closeBrace+1]))
	if err != nil {
		return "", fmt.Errorf("knowledge_focus block is not a JSON object: %w", err)
	}
	present := map[string]bool{}
	var ordered []string
	for _, roleID := range original.Keys() {
		present[roleID] = true
		if _, known := focus[roleID]; known {
			ordered = append(ordered, roleID)
		}
	}
	for _, roleID := range orderIDs {
		if !present[roleID] {
			ordered = append(ordered, roleID)
		}
	}

	var body strings.Builder
	for position, roleID := range ordered {
		comma := ","
		if position == len(ordered)-1 {
			comma = ""
		}
		body.WriteString("    " + pyJSONStringUnicode(roleID) + ": " +
			pyJSONStringUnicode(focus[roleID]) + comma + "\n")
	}
	newBlock := knowledgeFocusAnchor + "\n" + body.String() + "  }"

	anchorStart := strings.LastIndex(originalText[:openBrace+1], knowledgeFocusAnchor)
	if anchorStart < 0 {
		return "", fmt.Errorf("knowledge_focus anchor vanished between locate and splice")
	}
	spliced := originalText[:anchorStart] + newBlock + originalText[closeBrace+1:]

	if err := verifySpliceLeftEverythingElseAlone(originalText, spliced, focus); err != nil {
		return "", err
	}
	return spliced, nil
}

// verifySpliceLeftEverythingElseAlone re-parses both sides and fails closed if
// the splice altered any key other than knowledge_focus, or produced a
// knowledge_focus whose id set is not exactly the role set.
func verifySpliceLeftEverythingElseAlone(originalText, spliced string, focus map[string]string) error {
	var before, after map[string]json.RawMessage
	if err := json.Unmarshal([]byte(originalText), &before); err != nil {
		return fmt.Errorf("routing.json is not valid JSON: %w", err)
	}
	if err := json.Unmarshal([]byte(spliced), &after); err != nil {
		return fmt.Errorf("spliced routing.json is not valid JSON: %w", err)
	}
	for key, value := range before {
		if key == "knowledge_focus" {
			continue
		}
		other, present := after[key]
		if !present || !jsonEquivalent(value, other) {
			return fmt.Errorf("splice unexpectedly altered routing.json key %q", key)
		}
	}
	var splicedFocus map[string]string
	if raw, present := after["knowledge_focus"]; present {
		if err := json.Unmarshal(raw, &splicedFocus); err != nil {
			return fmt.Errorf("spliced knowledge_focus is not a string map: %w", err)
		}
	}
	if len(splicedFocus) != len(focus) {
		return fmt.Errorf("knowledge_focus id-set mismatch after splice")
	}
	for roleID := range focus {
		if _, present := splicedFocus[roleID]; !present {
			return fmt.Errorf("knowledge_focus id-set mismatch after splice")
		}
	}
	return nil
}

func jsonEquivalent(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return false
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false
	}
	leftCanonical, err := json.Marshal(leftValue)
	if err != nil {
		return false
	}
	rightCanonical, err := json.Marshal(rightValue)
	if err != nil {
		return false
	}
	return string(leftCanonical) == string(rightCanonical)
}
