package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderCatalog(t *testing.T) {
	header := "# Generated catalog\n"
	roles := []RoleMetadata{
		{
			ID:              "product-intent-agent",
			Definition:      "roster/planning/product-intent-agent/AGENT.md",
			Phase:           "planning",
			Capability:      "document_author",
			Model:           "sonnet",
			CodexModel:      "gpt-5.6-terra",
			ReasoningEffort: "medium",
			KnowledgeFocus:  "product objectives",
		},
		{
			ID:              "backend-engineer",
			Definition:      "roster/build/backend-engineer/AGENT.md",
			Phase:           "build",
			Capability:      "code_author",
			Model:           "sonnet",
			CodexModel:      "gpt-5.6-terra",
			ReasoningEffort: "medium",
			KnowledgeFocus:  "backend implementation",
		},
	}

	catalog, err := RenderCatalog(roles, header)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	// Check structure
	checks := map[string]bool{
		"agents:":                     false,
		"product-intent-agent:":       false,
		"definition: roster/planning": false,
		"phase: planning":             false,
		"model: sonnet":               false,
		"backend-engineer:":           false,
	}

	for check := range checks {
		if strings.Contains(catalog, check) {
			checks[check] = true
		}
	}

	for check, found := range checks {
		if !found {
			t.Errorf("missing expected content: %q", check)
		}
	}

	// Check ordering
	pos1 := strings.Index(catalog, "product-intent-agent:")
	pos2 := strings.Index(catalog, "backend-engineer:")
	if pos1 >= pos2 {
		t.Errorf("entries not in correct order")
	}
}

func TestLoadCatalogHeader(t *testing.T) {
	wd, _ := os.Getwd()
	headerPath := filepath.Join(wd, "../../roster/_catalog_header.yaml.tmpl")

	header, err := LoadCatalogHeader(headerPath)
	if err != nil {
		t.Skipf("cannot load header: %v", err)
	}

	if header == "" {
		t.Errorf("header is empty")
	}

	// Should contain YAML comment structure
	if !strings.Contains(header, "#") {
		t.Errorf("header doesn't contain expected comments")
	}

	t.Logf("loaded header: %d bytes", len(header))
}

func TestRenderCodexWrapper(t *testing.T) {
	role := RoleMetadata{
		ID:         "test-role",
		CodexModel: "gpt-5.6-terra",
	}

	wrapper := RenderCodexWrapper(role)

	if !strings.Contains(wrapper, "[role]") {
		t.Errorf("missing [role] section")
	}
	if !strings.Contains(wrapper, `name = "test-role"`) {
		t.Errorf("missing role name")
	}
	if !strings.Contains(wrapper, `model = "gpt-5.6-terra"`) {
		t.Errorf("missing model")
	}
}

func TestExportAgentCatalogJSON(t *testing.T) {
	roles := []RoleMetadata{
		{
			ID:              "role1",
			Definition:      "def1",
			Phase:           "planning",
			Capability:      "read_only",
			Model:           "sonnet",
			CodexModel:      "gpt-5.6-terra",
			ReasoningEffort: "medium",
			KnowledgeFocus:  "focus1",
		},
		{
			ID:              "role2",
			Definition:      "def2",
			Phase:           "build",
			Capability:      "code_author",
			Model:           "opus",
			CodexModel:      "gpt-5.6-sol",
			ReasoningEffort: "high",
			KnowledgeFocus:  "focus2",
		},
	}

	catalog := ExportAgentCatalogJSON(roles)

	if len(catalog) != 2 {
		t.Errorf("expected 2 roles in export, got %d", len(catalog))
	}

	if catalog["role1"]["model"] != "sonnet" {
		t.Errorf("role1 model mismatch")
	}
	if catalog["role2"]["reasoning_effort"] != "high" {
		t.Errorf("role2 reasoning_effort mismatch")
	}
}
