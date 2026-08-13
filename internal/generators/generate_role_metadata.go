package generators

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// GeneratedRoleMetadataFiles holds all generated outputs for role metadata.
type GeneratedRoleMetadataFiles struct {
	CatalogYAML     string            // roster/catalog.yaml
	CatalogJSON     string            // provider/agent-catalog.json
	CodexWrappers   map[string]string // provider/wrappers/<id>.toml (indexed by path)
	UpdatedRouting  string            // routing.json with updated knowledge_focus
	RoutingJSONPath string            // Path to write routing.json
}

// GenerateRoleMetadata orchestrates the complete role metadata generation pipeline.
// It loads all roles, validates them, renders catalog.yaml, exports agent-catalog.json,
// generates Codex wrappers, and updates routing.json with knowledge focus.
func GenerateRoleMetadata(manifestRoot string) (*GeneratedRoleMetadataFiles, error) {
	rosterRoot := filepath.Join(manifestRoot, "roster")

	// Load catalog order
	orderIDs, err := LoadCatalogOrder(filepath.Join(rosterRoot, "catalog-order.txt"))
	if err != nil {
		return nil, fmt.Errorf("cannot load catalog order: %w", err)
	}

	// Load model tiers
	tiers, err := LoadModelTiers(filepath.Join(rosterRoot, "runner-capabilities.json"))
	if err != nil {
		return nil, fmt.Errorf("cannot load model tiers: %w", err)
	}

	// Discover roles
	discovered, err := DiscoverRoles(rosterRoot)
	if err != nil {
		return nil, fmt.Errorf("cannot discover roles: %w", err)
	}

	// Load all roles in deterministic order
	allRoles, err := LoadAllRoles(rosterRoot, orderIDs, discovered, tiers)
	if err != nil {
		return nil, fmt.Errorf("cannot load roles: %w", err)
	}

	if len(allRoles) == 0 {
		return nil, fmt.Errorf("no roles discovered")
	}

	// Load catalog header template
	headerPath := filepath.Join(manifestRoot, "roster", "_catalog_header.yaml.tmpl")
	header, err := LoadCatalogHeader(headerPath)
	if err != nil {
		return nil, fmt.Errorf("cannot load catalog header: %w", err)
	}

	// Render catalog.yaml
	catalogYAML, err := RenderCatalog(allRoles, header)
	if err != nil {
		return nil, fmt.Errorf("cannot render catalog: %w", err)
	}

	// Export agent-catalog.json
	catalogJSON := ExportAgentCatalogJSON(allRoles)
	catalogJSONBytes, err := json.MarshalIndent(catalogJSON, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("cannot marshal agent-catalog.json: %w", err)
	}

	// Generate Codex wrappers
	codexWrappers := make(map[string]string)
	for _, role := range allRoles {
		wrapper := RenderCodexWrapper(role)
		wrapperPath := filepath.Join(manifestRoot, "provider", "wrappers", role.ID+".toml")
		codexWrappers[wrapperPath] = wrapper
	}

	// Build knowledge focus map
	knowledgeFocus := BuildKnowledgeFocus(allRoles)

	// Load and update routing.json
	routingPath := filepath.Join(manifestRoot, "roster", "orchestration", "routing.json")
	routingContent, err := os.ReadFile(routingPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read routing.json: %w", err)
	}

	// Parse and update routing.json with knowledge_focus
	var routingData map[string]interface{}
	if err := json.Unmarshal(routingContent, &routingData); err != nil {
		return nil, fmt.Errorf("cannot parse routing.json: %w", err)
	}

	// Update knowledge_focus field
	routingData["knowledge_focus"] = knowledgeFocus

	// Re-serialize routing.json
	updatedRoutingBytes, err := json.MarshalIndent(routingData, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("cannot marshal updated routing.json: %w", err)
	}

	return &GeneratedRoleMetadataFiles{
		CatalogYAML:     catalogYAML,
		CatalogJSON:     string(catalogJSONBytes),
		CodexWrappers:   codexWrappers,
		UpdatedRouting:  string(updatedRoutingBytes),
		RoutingJSONPath: routingPath,
	}, nil
}

// WriteRoleMetadataFiles writes all generated files to disk.
func WriteRoleMetadataFiles(manifestRoot string, generated *GeneratedRoleMetadataFiles) error {
	// Write catalog.yaml
	catalogPath := filepath.Join(manifestRoot, "roster", "catalog.yaml")
	if err := os.WriteFile(catalogPath, []byte(generated.CatalogYAML), 0644); err != nil {
		return fmt.Errorf("cannot write catalog.yaml: %w", err)
	}

	// Write agent-catalog.json
	catalogJSONPath := filepath.Join(manifestRoot, "provider", "agent-catalog.json")
	if err := os.WriteFile(catalogJSONPath, []byte(generated.CatalogJSON), 0644); err != nil {
		return fmt.Errorf("cannot write agent-catalog.json: %w", err)
	}

	// Write Codex wrappers
	for wrapperPath, content := range generated.CodexWrappers {
		dir := filepath.Dir(wrapperPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("cannot create wrapper directory %s: %w", dir, err)
		}
		if err := os.WriteFile(wrapperPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("cannot write wrapper %s: %w", wrapperPath, err)
		}
	}

	// Write updated routing.json
	if err := os.WriteFile(generated.RoutingJSONPath, []byte(generated.UpdatedRouting), 0644); err != nil {
		return fmt.Errorf("cannot write routing.json: %w", err)
	}

	return nil
}

// CheckRoleMetadata validates that all generated files are current without writing.
func CheckRoleMetadata(manifestRoot string, generated *GeneratedRoleMetadataFiles) (bool, []string, error) {
	var staleFiles []string

	checks := []struct {
		name     string
		path     string
		expected string
	}{
		{"catalog.yaml", filepath.Join(manifestRoot, "roster", "catalog.yaml"), generated.CatalogYAML},
		{"agent-catalog.json", filepath.Join(manifestRoot, "provider", "agent-catalog.json"), generated.CatalogJSON},
		{"routing.json", generated.RoutingJSONPath, generated.UpdatedRouting},
	}

	// Check primary files
	for _, check := range checks {
		actual, err := os.ReadFile(check.path)
		if err != nil || string(actual) != check.expected {
			relPath, _ := filepath.Rel(filepath.Dir(filepath.Dir(filepath.Dir(manifestRoot))), check.path)
			staleFiles = append(staleFiles, relPath)
		}
	}

	// Check Codex wrappers
	for wrapperPath, expectedContent := range generated.CodexWrappers {
		actual, err := os.ReadFile(wrapperPath)
		if err != nil || string(actual) != expectedContent {
			relPath, _ := filepath.Rel(filepath.Dir(filepath.Dir(filepath.Dir(manifestRoot))), wrapperPath)
			staleFiles = append(staleFiles, relPath)
		}
	}

	// Check for orphaned wrapper files
	wrapperDir := filepath.Join(manifestRoot, "provider", "wrappers")
	if entries, err := os.ReadDir(wrapperDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".toml" {
				wrapperPath := filepath.Join(wrapperDir, entry.Name())
				if _, exists := generated.CodexWrappers[wrapperPath]; !exists {
					relPath, _ := filepath.Rel(filepath.Dir(filepath.Dir(filepath.Dir(manifestRoot))), wrapperPath)
					staleFiles = append(staleFiles, relPath)
				}
			}
		}
	}

	sort.Strings(staleFiles)
	return len(staleFiles) == 0, staleFiles, nil
}
