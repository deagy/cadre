package generators

import (
	"encoding/json"
	"fmt"
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

// DiscoverRoles walks the roster directory and discovers all role AGENT.md files.
// Returns a map of role_id -> AGENT.md path.
func DiscoverRoles(rosterRoot string) (map[string]string, error) {
	roles := make(map[string]string)

	// Walk each phase directory
	phases, err := os.ReadDir(rosterRoot)
	if err != nil {
		return nil, fmt.Errorf("cannot read roster root: %w", err)
	}

	for _, phaseEntry := range phases {
		if !phaseEntry.IsDir() {
			continue
		}
		phasePath := filepath.Join(rosterRoot, phaseEntry.Name())

		// Walk each role directory
		roleEntries, err := os.ReadDir(phasePath)
		if err != nil {
			continue
		}

		for _, roleEntry := range roleEntries {
			if !roleEntry.IsDir() {
				continue
			}
			rolePath := filepath.Join(phasePath, roleEntry.Name())
			agentMdPath := filepath.Join(rolePath, "AGENT.md")

			// Check if AGENT.md exists
			if _, err := os.Stat(agentMdPath); err == nil {
				roles[roleEntry.Name()] = agentMdPath
			}
		}
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

// BuildKnowledgeFocus creates a knowledge_focus block from all roles.
// Returns a map suitable for JSON marshaling.
func BuildKnowledgeFocus(roles []RoleMetadata) map[string]string {
	kf := make(map[string]string)
	for _, role := range roles {
		kf[role.ID] = role.KnowledgeFocus
	}
	return kf
}

// SortedRoleIDs returns the role IDs in sorted order (for deterministic output).
func SortedRoleIDs(roles []RoleMetadata) []string {
	var ids []string
	for _, role := range roles {
		ids = append(ids, role.ID)
	}
	sort.Strings(ids)
	return ids
}
