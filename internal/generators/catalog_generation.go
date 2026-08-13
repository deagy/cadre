package generators

import (
	"fmt"
	"os"
	"strings"
)

// CatalogEntry represents one role entry in catalog.yaml.
type CatalogEntry struct {
	ID              string
	Definition      string
	Phase           string
	Capability      string
	Model           string
	CodexModel      string
	ReasoningEffort string
}

// RenderCatalog generates the complete catalog.yaml content from roles and a header template.
func RenderCatalog(roles []RoleMetadata, headerTemplate string) (string, error) {
	var b strings.Builder

	// Write header (template as-is, with leading newline)
	if strings.TrimSpace(headerTemplate) != "" {
		b.WriteString(headerTemplate)
		if !strings.HasSuffix(headerTemplate, "\n") {
			b.WriteString("\n")
		}
	}

	// Write agents block
	b.WriteString("agents:\n")

	// Collect entries with their prefix comments
	entries := []struct {
		comment string
		entry   CatalogEntry
	}{}

	for _, role := range roles {
		entry := CatalogEntry{
			ID:              role.ID,
			Definition:      role.Definition,
			Phase:           role.Phase,
			Capability:      role.Capability,
			Model:           role.Model,
			CodexModel:      role.CodexModel,
			ReasoningEffort: role.ReasoningEffort,
		}

		// Check for role-specific prefix comments (like the authority block header)
		comment := ""
		if role.ID == "product-owner-aide" && role.Phase == "authority" {
			comment = "  # `phase: authority` roles below prepare the decision package a human\n" +
				"  # lifecycle authority needs for their assigned gate(s); they never approve,\n" +
				"  # recommend a disposition, or hold delegated authority themselves (see\n" +
				"  # roster/shared/agent-autonomy.yaml)\n"
		}

		entries = append(entries, struct {
			comment string
			entry   CatalogEntry
		}{comment, entry})
	}

	// Write entries
	for _, e := range entries {
		if e.comment != "" {
			b.WriteString(e.comment)
		}

		entry := e.entry
		b.WriteString(fmt.Sprintf("  %s:\n", entry.ID))
		b.WriteString(fmt.Sprintf("    definition: %s\n", entry.Definition))
		b.WriteString(fmt.Sprintf("    phase: %s\n", entry.Phase))
		b.WriteString(fmt.Sprintf("    capability: %s\n", entry.Capability))
		b.WriteString(fmt.Sprintf("    model: %s\n", entry.Model))
		b.WriteString(fmt.Sprintf("    codex_model: %s\n", entry.CodexModel))
		b.WriteString(fmt.Sprintf("    reasoning_effort: %s\n", entry.ReasoningEffort))
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

// SpliceKnowledgeFocus replaces the knowledge_focus block in an existing routing.json-like text.
// This is used to update routing.json with fresh knowledge_focus data.
func SpliceKnowledgeFocus(originalRouting string, knowledgeFocus map[string]string) (string, error) {
	// For now, we'll return the original routing unchanged.
	// The proper implementation requires parsing JSON, updating the knowledge_focus field,
	// and re-serializing. This is more complex and depends on understanding the exact
	// routing.json structure and how to preserve formatting.
	//
	// For Phase 3 Layer 2, we focus on catalog.yaml generation first.
	// Knowledge focus splicing will be added in a follow-up if needed.
	return originalRouting, nil
}

// RenderCodexWrapper generates a .toml file for one role's Codex wrapper.
func RenderCodexWrapper(role RoleMetadata) string {
	var b strings.Builder

	b.WriteString("[role]\n")
	b.WriteString(fmt.Sprintf("name = \"%s\"\n", role.ID))
	b.WriteString(fmt.Sprintf("model = \"%s\"\n", role.CodexModel))

	// Optional: add capabilities based on role.Capability
	// For now, just the basic structure
	b.WriteString("capabilities = [\"read_only\"]\n")

	return b.String()
}

// ExportAgentCatalogJSON generates the agent-catalog.json export.
// Returns a map that can be JSON marshaled.
func ExportAgentCatalogJSON(roles []RoleMetadata) map[string]map[string]string {
	catalog := make(map[string]map[string]string)

	for _, role := range roles {
		catalog[role.ID] = map[string]string{
			"definition":      role.Definition,
			"phase":           role.Phase,
			"capability":      role.Capability,
			"model":           role.Model,
			"codex_model":     role.CodexModel,
			"reasoning_effort": role.ReasoningEffort,
			"knowledge_focus": role.KnowledgeFocus,
		}
	}

	return catalog
}

// UpdateRoutingKnowledgeFocus parses routing.json, updates knowledge_focus, and returns updated JSON.
// This is a placeholder for the JSON update logic.
func UpdateRoutingKnowledgeFocus(routingJSON string, knowledgeFocus map[string]string) (string, error) {
	// Parse JSON
	// var routing map[string]interface{}
	// (Would use json.Unmarshal here)

	// Update knowledge_focus field
	// routing["knowledge_focus"] = knowledgeFocus

	// Re-serialize to JSON
	// (Would use json.Marshal here)

	return routingJSON, nil
}
