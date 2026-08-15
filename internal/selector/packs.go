package selector

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContextPack is one entry of the plan's context_packs array, bound to the
// exact bytes of the pack it names.
type ContextPack struct {
	ID             string `json:"id"`
	Version        int    `json:"version"`
	Definition     string `json:"definition"`
	Classification string `json:"classification"`
	ContentHash    string `json:"content_hash"`
}

// BuildContextPacks ports _build_context_packs: selected non-authoring
// reference packs, bound to their bytes and filtered by the task's asserted
// classification.
//
// A pack declares its own classification in frontmatter, enforced here on the
// same containment rule a dispatched child gets: material may not exceed the
// classification asserted for the work it is supplied to. So an `internal`
// pack is emitted for internal, confidential and restricted tasks, and
// withheld from a public one.
//
// With no classification asserted at all this fails closed and emits nothing,
// matching BuildKnowledgeContext's authorization-required disposition. An
// unasserted classification is not a licence to hand back internal-classified
// reference material.
//
// Definition existence and frontmatter validity are checked for *every*
// matched pack before filtering, so those repository-integrity guards still
// fire on a public or unclassified run rather than only when a pack happens
// to survive the filter.
func BuildContextPacks(matches []Match, classification string, rosterRoot string) ([]ContextPack, error) {
	if classification != "" {
		if _, known := ClassificationRank[classification]; !known {
			return nil, fmt.Errorf("Invalid classification: %s", classification) //nolint:staticcheck // ST1005: ported verbatim.
		}
	}
	packs := []ContextPack{}
	for _, match := range matches {
		definition, _ := match.Rule["definition"].(string)
		path := filepath.Join(rosterRoot, filepath.FromSlash(definition))
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Context pack definition is missing: %s", definition) //nolint:staticcheck // ST1005: ported verbatim.
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("Context pack definition is missing: %s", definition) //nolint:staticcheck // ST1005: ported verbatim.
		}
		packClassification, err := packClassificationOf(definition, string(content))
		if err != nil {
			return nil, err
		}
		if classification == "" {
			continue
		}
		if ClassificationRank[packClassification] > ClassificationRank[classification] {
			continue
		}
		version, _ := match.Rule["version"].(float64)
		id, _ := match.Rule["id"].(string)
		sum := sha256.Sum256(content)
		packs = append(packs, ContextPack{
			ID:             id,
			Version:        int(version),
			Definition:     definition,
			Classification: packClassification,
			ContentHash:    "sha256:" + hex.EncodeToString(sum[:]),
		})
	}
	return packs, nil
}

// packClassificationOf ports _pack_classification.
//
// It raises rather than defaulting: a pack with no frontmatter block, no
// classification field, or an unrecognised value is a repository-integrity
// defect, and guessing a value would be exactly the silent fail-open this
// check exists to prevent.
func packClassificationOf(definition, text string) (string, error) {
	fields, ok := parseFrontmatter(text)
	if !ok {
		return "", fmt.Errorf("Context pack has no frontmatter block: %s", definition) //nolint:staticcheck // ST1005: ported verbatim.
	}
	declared := fields["classification"]
	if _, known := ClassificationRank[declared]; !known {
		return "", fmt.Errorf( //nolint:staticcheck // ST1005: ported verbatim.
			"Context pack %s declares classification %q; must be one of %v",
			definition, declared, ClassificationOrder)
	}
	return declared, nil
}

// parseFrontmatter reads a leading `---` delimited block of `key: value`
// lines. Returns ok=false when the document has no such block at all, which
// callers must distinguish from a block missing a particular field.
func parseFrontmatter(text string) (map[string]string, bool) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return nil, false
	}
	fields := map[string]string{}
	for _, line := range lines[1:] {
		trimmed := strings.TrimRight(line, "\r")
		if trimmed == "---" {
			return fields, true
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	// An unterminated block is not a frontmatter block.
	return nil, false
}
