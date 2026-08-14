package generators

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// GeneratedRoleMetadataFiles holds all generated outputs for role metadata.
type GeneratedRoleMetadataFiles struct {
	CatalogYAML     string // roster/catalog.yaml
	CatalogJSON     string // provider/agent-catalog.json
	UpdatedRouting  string // routing.json with updated knowledge_focus
	RoutingJSONPath string // Path to write routing.json
	// ProviderContent is the rest of provider/'s generated bundle, keyed by
	// absolute path: the Codex .toml wrappers under codex-agents/ and the
	// verbatim role copies under roles/.
	ProviderContent map[string]string
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

	// Render the generated members of provider/: agent-catalog.json, the Codex
	// .toml wrappers, and the verbatim role copies. Rendered from allRoles (the
	// freshly read frontmatter) rather than the committed catalog.yaml, so a
	// stale catalog can never make these look current.
	providerContent, err := RenderProviderContent(manifestRoot, allRoles)
	if err != nil {
		return nil, fmt.Errorf("cannot render provider content: %w", err)
	}
	catalogJSONPath := filepath.Join(manifestRoot, "provider", "agent-catalog.json")
	catalogJSON := providerContent[catalogJSONPath]
	delete(providerContent, catalogJSONPath)

	// Splice routing.json's knowledge_focus block in place. A full re-serialize
	// would reformat the entire file; only the knowledge_focus region is this
	// generator's to own.
	routingPath := filepath.Join(manifestRoot, "roster", "orchestration", "routing.json")
	routingContent, err := os.ReadFile(routingPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read routing.json: %w", err)
	}
	updatedRouting, err := SpliceKnowledgeFocus(string(routingContent), allRoles)
	if err != nil {
		return nil, fmt.Errorf("cannot update routing.json knowledge_focus: %w", err)
	}

	return &GeneratedRoleMetadataFiles{
		CatalogYAML:     catalogYAML,
		CatalogJSON:     catalogJSON,
		UpdatedRouting:  updatedRouting,
		RoutingJSONPath: routingPath,
		ProviderContent: providerContent,
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

	// Write the rest of the generated provider/ bundle
	for providerPath, content := range generated.ProviderContent {
		if err := writeFile(providerPath, content); err != nil {
			return err
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
			relPath, _ := filepath.Rel(manifestRoot, check.path)
			staleFiles = append(staleFiles, relPath)
		}
	}

	// Check the rest of the generated provider/ bundle
	for providerPath, expectedContent := range generated.ProviderContent {
		actual, err := os.ReadFile(providerPath)
		if err != nil || string(actual) != expectedContent {
			relPath, _ := filepath.Rel(manifestRoot, providerPath)
			staleFiles = append(staleFiles, relPath)
		}
	}

	// Orphans: a removed role leaves a stale wrapper or role copy that no
	// rendered entry covers, and nothing else would ever delete it.
	for _, subdirectory := range []string{"codex-agents", providerRolesDirname} {
		root := filepath.Join(manifestRoot, "provider", subdirectory)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error { //nolint:errcheck // absence is not an error
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // a missing directory is simply no orphans
			}
			if _, expected := generated.ProviderContent[p]; !expected {
				relPath, _ := filepath.Rel(manifestRoot, p)
				staleFiles = append(staleFiles, relPath)
			}
			return nil
		})
	}

	sort.Strings(staleFiles)
	return len(staleFiles) == 0, staleFiles, nil
}
