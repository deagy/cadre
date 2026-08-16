package generators

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const GeneratedMarker = "<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->"

// AideData represents one authority-aide role from aides.yaml.
type AideData struct {
	ID             string `yaml:""`
	Title          string `yaml:"title"`
	Gates          []int  `yaml:"gates"`
	KnowledgeFocus string `yaml:"knowledge_focus"`
}

// LoadAides reads and parses aides.yaml.
func LoadAides(aidesPath string) ([]AideData, error) {
	raw, err := os.ReadFile(aidesPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read aides.yaml: %w", err)
	}

	// Parse YAML structure: aides: { <id>: { title, gates, knowledge_focus } }
	// Gates is a YAML sequence [1, 2, 3] in the actual file
	var doc struct {
		Aides map[string]struct {
			Title          string `yaml:"title"`
			Gates          []int  `yaml:"gates"` // Already a list in YAML
			KnowledgeFocus string `yaml:"knowledge_focus"`
		} `yaml:"aides"`
	}

	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("aides.yaml: invalid YAML: %w", err)
	}

	var aides []AideData
	for id, fields := range doc.Aides {
		if fields.Title == "" {
			return nil, fmt.Errorf("aides.yaml: aide %q is missing required field 'title'", id)
		}
		if len(fields.Gates) == 0 {
			return nil, fmt.Errorf("aides.yaml: aide %q is missing required field 'gates'", id)
		}
		if fields.KnowledgeFocus == "" {
			return nil, fmt.Errorf("aides.yaml: aide %q is missing required field 'knowledge_focus'", id)
		}

		// A gate listed twice renders into the aide's own brief -- gatePhrase
		// produces "gates G1, G1, and G2" -- so it is refused rather than
		// silently de-duplicated. De-duplicating would hide a typo in a
		// hand-edited file whose whole purpose is to say which gates an
		// authority prepares for.
		gates := append([]int(nil), fields.Gates...)
		sort.Ints(gates)
		var duplicates []int
		for index := 1; index < len(gates); index++ {
			if gates[index] == gates[index-1] &&
				(len(duplicates) == 0 || duplicates[len(duplicates)-1] != gates[index]) {
				duplicates = append(duplicates, gates[index])
			}
		}
		if len(duplicates) > 0 {
			listed := make([]string, 0, len(duplicates))
			for _, gate := range duplicates {
				listed = append(listed, strconv.Itoa(gate))
			}
			return nil, fmt.Errorf("aides.yaml: aide %q has duplicate gate(s): %s",
				id, strings.Join(listed, ", "))
		}

		aides = append(aides, AideData{
			ID:             id,
			Title:          stripInlineComment(fields.Title),
			Gates:          gates,
			KnowledgeFocus: stripInlineComment(fields.KnowledgeFocus),
		})
	}

	// Sort by ID for deterministic order
	sort.Slice(aides, func(i, j int) bool {
		return aides[i].ID < aides[j].ID
	})

	return aides, nil
}

// parseGates parses a YAML flow-style list like "[1, 2, 3]".
func parseGates(raw string) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
		return nil, fmt.Errorf("gates must be a flow-style list like '[1, 2]', got %q", raw)
	}

	inner := strings.TrimPrefix(strings.TrimSuffix(raw, "]"), "[")
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return nil, fmt.Errorf("gates list is empty")
	}

	parts := strings.Split(inner, ",")
	var gates []int
	seen := make(map[int]bool)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var gate int
		if _, err := fmt.Sscanf(part, "%d", &gate); err != nil {
			return nil, fmt.Errorf("non-integer gate in %q", raw)
		}
		if seen[gate] {
			return nil, fmt.Errorf("duplicate gate in %q: %d", raw, gate)
		}
		seen[gate] = true
		gates = append(gates, gate)
	}

	if len(gates) == 0 {
		return nil, fmt.Errorf("no valid gates in %q", raw)
	}

	// Sort for consistent output
	sort.Ints(gates)
	return gates, nil
}

// stripInlineComment removes YAML inline comments (# preceded by whitespace).
func stripInlineComment(value string) string {
	// Match whitespace followed by # and everything after
	re := regexp.MustCompile(`(?:^|\s)#.*$`)
	return strings.TrimSpace(re.ReplaceAllString(value, ""))
}

// gatePhrase formats gates as human-readable phrase.
// E.g., [1] -> "gate G1", [1, 2] -> "gates G1 and G2", [1, 2, 3] -> "gates G1, G2, and G3"
func gatePhrase(gates []int) string {
	labels := make([]string, len(gates))
	for i, g := range gates {
		labels[i] = fmt.Sprintf("G%d", g)
	}

	if len(labels) == 1 {
		return fmt.Sprintf("gate %s", labels[0])
	}
	if len(labels) == 2 {
		return fmt.Sprintf("gates %s and %s", labels[0], labels[1])
	}

	// Three or more: "gates G1, G2, and G3"
	return fmt.Sprintf("gates %s, and %s", strings.Join(labels[:len(labels)-1], ", "), labels[len(labels)-1])
}

// gateList formats gates as simple comma-separated string.
// E.g., [1, 2, 3] -> "G1, G2, G3"
func gateList(gates []int) string {
	labels := make([]string, len(gates))
	for i, g := range gates {
		labels[i] = fmt.Sprintf("G%d", g)
	}
	return strings.Join(labels, ", ")
}

// RenderAide renders the template for a single aide.
func RenderAide(template string, aide AideData, generatedMarker string) (string, error) {
	// Replace placeholders
	replacements := map[string]string{
		"{id}":              aide.ID,
		"{title}":           aide.Title,
		"{knowledge_focus}": aide.KnowledgeFocus,
		"{gate_phrase}":     gatePhrase(aide.Gates),
		"{gate_list}":       gateList(aide.Gates),
	}

	rendered := template
	for placeholder, value := range replacements {
		rendered = strings.ReplaceAll(rendered, placeholder, value)
	}

	// Insert generated marker after frontmatter closing delimiter (---)
	// Find the closing --- line
	lines := strings.Split(rendered, "\n")
	closingLineIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closingLineIdx = i
			break
		}
	}
	if closingLineIdx == -1 {
		return "", fmt.Errorf("template missing frontmatter closing delimiter (---)")
	}

	// Reconstruct with marker after closing ---
	lines[closingLineIdx] = lines[closingLineIdx] + "\n\n" + generatedMarker
	return strings.Join(lines, "\n"), nil
}

// GenerateAuthorityAides generates all aide AGENT.md files.
func GenerateAuthorityAides(authorityRoot, aidesPath, templatePath string, generatedMarker string) (map[string]string, error) {
	aides, err := LoadAides(aidesPath)
	if err != nil {
		return nil, err
	}

	templateBytes, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, fmt.Errorf("cannot read template: %w", err)
	}
	template := string(templateBytes)

	result := make(map[string]string)
	for _, aide := range aides {
		rendered, err := RenderAide(template, aide, generatedMarker)
		if err != nil {
			return nil, fmt.Errorf("cannot render aide %q: %w", aide.ID, err)
		}

		path := filepath.Join(authorityRoot, aide.ID, "AGENT.md")
		result[path] = rendered
	}

	return result, nil
}

// WriteAideFiles writes all generated aide files to disk.
func WriteAideFiles(files map[string]string) error {
	for path, content := range files {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("cannot create directory %s: %w", dir, err)
		}

		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("cannot write %s: %w", path, err)
		}
	}
	return nil
}

// CheckAides validates that generated files are current without writing.
// ValidateAideGatesAgainstContract cross-checks every gate number in
// aides.yaml against the lifecycle-gates contract.
//
// aides.yaml hardcodes each aide's gate numbers, and the kernel owns gate
// numbering permanently. Nothing else constrains them: parseGates checks they
// are integers and not duplicated, not that they exist. So a typo, or a
// kernel-side renumber, ships an authority aide telling a human to prepare a
// decision package for a gate that is not there.
//
// The contract is read as data, which is one of exactly two couplings the
// kernel boundary permits. That differs from the Python generator, which
// shelled out to whichever kernel was on PATH: this reads the in-tree
// contract, so it catches in-tree drift deterministically and would not
// notice a *separately installed* kernel disagreeing. In this repository the
// kernel is in-tree, so the two coincide.
//
// A contract that is not there is not an error. Standalone operation is
// supported everywhere else in this suite, and a generator that refused to
// run without a kernel would be a new dependency rather than a check.
func ValidateAideGatesAgainstContract(aides []AideData, contractPath string) error {
	raw, err := os.ReadFile(contractPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot read %s: %w", contractPath, err)
	}
	var contract struct {
		Gates []struct {
			ID string `json:"id"`
		} `json:"gates"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		return fmt.Errorf("%s: invalid JSON: %w", contractPath, err)
	}
	if len(contract.Gates) == 0 {
		return fmt.Errorf("%s declares no gates", contractPath)
	}
	known := make(map[string]bool, len(contract.Gates))
	var knownIDs []string
	for _, gate := range contract.Gates {
		known[gate.ID] = true
		knownIDs = append(knownIDs, gate.ID)
	}
	sort.Strings(knownIDs)

	for _, aide := range aides {
		for _, gate := range aide.Gates {
			id := "G" + strconv.Itoa(gate)
			if !known[id] {
				return fmt.Errorf(
					"aide %q references %s, which is not in the lifecycle-gates "+
						"contract (%s)", aide.ID, id, strings.Join(knownIDs, ", "))
			}
		}
	}
	return nil
}

// OrphanedAideFiles lists every <authorityRoot>/*/AGENT.md the current aide
// set no longer generates.
//
// Scoped to exactly that shape on purpose: this feeds a delete, and a wider
// walk would eventually find something it should not.
func OrphanedAideFiles(authorityRoot string, generated map[string]string) ([]string, error) {
	entries, err := os.ReadDir(authorityRoot)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", authorityRoot, err)
	}
	var orphans []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(authorityRoot, entry.Name(), "AGENT.md")
		if _, generatedHere := generated[path]; generatedHere {
			continue
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			continue
		}
		orphans = append(orphans, path)
	}
	sort.Strings(orphans)
	return orphans, nil
}

// RemoveOrphanedAides deletes the AGENT.md of every aide the current set no
// longer generates, then removes each now-empty directory.
//
// The directory removal is deliberately non-recursive. An orphaned directory
// holding anything else -- a note, a half-written sibling file -- keeps that
// content and is reported instead, because a generator that recursively
// deletes a directory it did not wholly create is one bad path away from
// removing work nobody asked it to touch.
func RemoveOrphanedAides(authorityRoot string, generated map[string]string) ([]string, error) {
	orphans, err := OrphanedAideFiles(authorityRoot, generated)
	if err != nil {
		return nil, err
	}
	var kept []string
	for _, path := range orphans {
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("cannot remove %s: %w", path, err)
		}
		if err := os.Remove(filepath.Dir(path)); err != nil {
			kept = append(kept, filepath.Dir(path))
		}
	}
	return kept, nil
}

func CheckAides(authorityRoot string, generated map[string]string) (bool, []string, error) {
	var stale []string

	// Check if each generated file is current
	for path, expectedContent := range generated {
		if existing, err := os.ReadFile(path); err != nil || string(existing) != expectedContent {
			relPath, _ := filepath.Rel(filepath.Dir(filepath.Dir(filepath.Dir(authorityRoot))), path)
			stale = append(stale, relPath)
		}
	}

	// Check for orphaned files (exist but shouldn't)
	entries, _ := os.ReadDir(authorityRoot)
	for _, entry := range entries {
		if entry.IsDir() {
			agentMdPath := filepath.Join(authorityRoot, entry.Name(), "AGENT.md")
			if _, exists := generated[agentMdPath]; !exists {
				relPath, _ := filepath.Rel(filepath.Dir(filepath.Dir(filepath.Dir(authorityRoot))), agentMdPath)
				stale = append(stale, relPath)
			}
		}
	}

	sort.Strings(stale)
	return len(stale) == 0, stale, nil
}
