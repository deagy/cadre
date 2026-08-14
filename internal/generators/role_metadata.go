package generators

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RoleMetadata represents a role's complete metadata from AGENT.md frontmatter.
type RoleMetadata struct {
	ID              string `yaml:"id"`
	Phase           string `yaml:"phase"`
	Capability      string `yaml:"capability"`
	Model           string `yaml:"model"`
	CodexModel      string `yaml:"codex_model"`
	ReasoningEffort string `yaml:"reasoning_effort"`
	KnowledgeFocus  string `yaml:"knowledge_focus"`
	Definition      string // Relative path to AGENT.md (computed)
}

// ModelTierInfo contains the mapping for a model tier.
type ModelTierInfo struct {
	CodexModel      string `json:"codex_model"`
	ReasoningEffort string `json:"reasoning_effort"`
}

// CapabilitiesManifest represents runner-capabilities.json structure.
type CapabilitiesManifest struct {
	ModelTiers map[string]ModelTierInfo `json:"model_tiers"`
}

// LoadCatalogOrder reads catalog-order.txt and returns the ordered role IDs.
func LoadCatalogOrder(path string) ([]string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read catalog-order.txt: %w", err)
	}

	var roles []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		// Skip blank lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		roles = append(roles, line)
	}

	if len(roles) == 0 {
		return nil, fmt.Errorf("catalog-order.txt contains no role ids")
	}

	return roles, nil
}

// LoadModelTiers reads runner-capabilities.json and extracts model tier mappings.
func LoadModelTiers(path string) (map[string]ModelTierInfo, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read runner-capabilities.json: %w", err)
	}

	var manifest CapabilitiesManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, fmt.Errorf("runner-capabilities.json: invalid JSON: %w", err)
	}

	if manifest.ModelTiers == nil {
		return nil, fmt.Errorf("runner-capabilities.json: missing model_tiers")
	}

	return manifest.ModelTiers, nil
}

// nonRosterDirectoryParts mirrors generate_role_metadata.py's
// NON_ROSTER_DIRECTORY_PARTS: directories whose AGENT.md files describe some
// *other* roster (the orchestration selector's fixture roster under
// roster/orchestration/test/fixtures/) and must never enter this one's
// inventory. A recursive walk without this exclusion silently claims those
// three fixture roles as Cadre's own.
var nonRosterDirectoryParts = map[string]bool{"test": true, "fixtures": true}

// isRoleDefinition reports whether an AGENT.md at rel (a slash-separated path
// relative to the roster root) belongs to this roster's inventory.
func isRoleDefinition(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if nonRosterDirectoryParts[part] {
			return false
		}
	}
	return true
}

// DiscoverRoles walks the roster directory recursively and discovers every
// role AGENT.md. Returns a map of role id -> AGENT.md path.
//
// Roles are keyed by the frontmatter `id` field, not by directory name: the
// knowledge-store steward's definition lives at roster/knowledge-store/AGENT.md
// (depth 2, directory "knowledge-store") while its id is
// "knowledge-store-steward". Keying by directory name made that role
// undiscoverable and every generator crash on it. Mirrors
// generate_role_metadata.py's build_role_model().
func DiscoverRoles(rosterRoot string) (map[string]string, error) {
	roles := make(map[string]string)
	seen := make(map[string]string) // role id -> relative path, for duplicate reporting

	var paths []string
	err := filepath.WalkDir(rosterRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "AGENT.md" {
			return nil
		}
		rel, relErr := filepath.Rel(rosterRoot, path)
		if relErr != nil {
			return relErr
		}
		if !isRoleDefinition(filepath.ToSlash(rel)) {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cannot read roster root: %w", err)
	}
	sort.Strings(paths)

	for _, path := range paths {
		rel, _ := filepath.Rel(rosterRoot, path)
		rel = filepath.ToSlash(rel)
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", rel, err)
		}
		text := string(content)
		if !IsMigrated(text) {
			return nil, fmt.Errorf("%s: AGENT.md does not carry '---'-delimited frontmatter", rel)
		}
		frontmatter, err := extractFrontmatter(text)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		var fields struct {
			ID string `yaml:"id"`
		}
		if err := yaml.Unmarshal([]byte(frontmatter), &fields); err != nil {
			return nil, fmt.Errorf("%s: cannot parse frontmatter: %w", rel, err)
		}
		if fields.ID == "" {
			return nil, fmt.Errorf("%s: frontmatter is missing required field 'id'", rel)
		}
		if previous, exists := seen[fields.ID]; exists {
			return nil, fmt.Errorf("duplicate role id %q: %q and %q", fields.ID, previous, rel)
		}
		seen[fields.ID] = rel
		roles[fields.ID] = path
	}

	if len(roles) == 0 {
		return nil, fmt.Errorf("no roles discovered in %s", rosterRoot)
	}

	return roles, nil
}

// LoadRoleMetadata loads and parses a single role's AGENT.md frontmatter.
func LoadRoleMetadata(agentMdPath string, definition string) (*RoleMetadata, error) {
	content, err := os.ReadFile(agentMdPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", agentMdPath, err)
	}

	// Extract frontmatter (between --- delimiters)
	frontmatter, err := extractFrontmatter(string(content))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", agentMdPath, err)
	}

	var metadata RoleMetadata
	if err := yaml.Unmarshal([]byte(frontmatter), &metadata); err != nil {
		return nil, fmt.Errorf("%s: cannot parse frontmatter: %w", agentMdPath, err)
	}

	// Validate required fields
	if metadata.ID == "" {
		return nil, fmt.Errorf("%s: missing required field 'id'", agentMdPath)
	}
	if metadata.Phase == "" {
		return nil, fmt.Errorf("%s: missing required field 'phase'", agentMdPath)
	}
	if metadata.Capability == "" {
		return nil, fmt.Errorf("%s: missing required field 'capability'", agentMdPath)
	}
	if metadata.Model == "" {
		return nil, fmt.Errorf("%s: missing required field 'model'", agentMdPath)
	}
	if metadata.CodexModel == "" {
		return nil, fmt.Errorf("%s: missing required field 'codex_model'", agentMdPath)
	}
	if metadata.ReasoningEffort == "" {
		return nil, fmt.Errorf("%s: missing required field 'reasoning_effort'", agentMdPath)
	}
	if metadata.KnowledgeFocus == "" {
		return nil, fmt.Errorf("%s: missing required field 'knowledge_focus'", agentMdPath)
	}

	metadata.Definition = definition
	return &metadata, nil
}

// extractFrontmatter extracts the YAML frontmatter from an AGENT.md file.
func extractFrontmatter(content string) (string, error) {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return "", fmt.Errorf("missing frontmatter opening delimiter (---)")
	}

	// Find closing ---
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return strings.Join(lines[1:i], "\n"), nil
		}
	}

	return "", fmt.Errorf("missing frontmatter closing delimiter (---)")
}

// ValidateModelTier validates that a role's model, codex_model, and reasoning_effort are consistent.
func ValidateModelTier(model, codexModel, effort string, tiers map[string]ModelTierInfo) error {
	tierInfo, exists := tiers[model]
	if !exists {
		return fmt.Errorf("unknown model tier: %q (allowed: opus, sonnet, haiku)", model)
	}

	if tierInfo.CodexModel != codexModel {
		return fmt.Errorf("model %q maps to codex_model %q, but got %q", model, tierInfo.CodexModel, codexModel)
	}

	if tierInfo.ReasoningEffort != effort {
		return fmt.Errorf("model %q maps to reasoning_effort %q, but got %q", model, tierInfo.ReasoningEffort, effort)
	}

	return nil
}

// LoadAllRoles loads all roles in the specified order, validating model tiers.
func LoadAllRoles(rosterRoot string, orderIDs []string, discovered map[string]string, tiers map[string]ModelTierInfo) ([]RoleMetadata, error) {
	var roles []RoleMetadata

	for _, roleID := range orderIDs {
		agentMdPath, exists := discovered[roleID]
		if !exists {
			return nil, fmt.Errorf("catalog-order.txt references %q but no AGENT.md found for it", roleID)
		}

		// Calculate definition path (relative to roster root)
		relPath, err := filepath.Rel(rosterRoot, agentMdPath)
		if err != nil {
			relPath = agentMdPath
		}

		metadata, err := LoadRoleMetadata(agentMdPath, relPath)
		if err != nil {
			return nil, err
		}

		// Validate model tier consistency
		if err := ValidateModelTier(metadata.Model, metadata.CodexModel, metadata.ReasoningEffort, tiers); err != nil {
			return nil, fmt.Errorf("%s: %w", roleID, err)
		}

		roles = append(roles, *metadata)
	}

	return roles, nil
}
