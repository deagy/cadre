package generators

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateRoleMetadata(t *testing.T) {
	wd, _ := os.Getwd()
	repoRoot := filepath.Join(wd, "../..")

	generated, err := GenerateRoleMetadata(repoRoot)
	if err != nil {
		t.Skipf("cannot generate role metadata (real data test): %v", err)
	}

	if generated == nil {
		t.Fatalf("generated is nil")
		return
	}

	// Check catalog.yaml
	if generated.CatalogYAML == "" {
		t.Errorf("CatalogYAML is empty")
	}
	if !contains(generated.CatalogYAML, "agents:") {
		t.Errorf("CatalogYAML missing 'agents:' section")
	}

	// Check agent-catalog.json
	if generated.CatalogJSON == "" {
		t.Errorf("CatalogJSON is empty")
	}
	var catalogData map[string]interface{}
	if err := json.Unmarshal([]byte(generated.CatalogJSON), &catalogData); err != nil {
		t.Errorf("CatalogJSON is not valid JSON: %v", err)
	}
	if len(catalogData) == 0 {
		t.Errorf("CatalogJSON has no roles")
	}

	// Check the generated provider/ bundle: one Codex .toml wrapper and one
	// verbatim role copy per role.
	if len(generated.ProviderContent) == 0 {
		t.Errorf("ProviderContent is empty")
	}
	wrappers, roleCopies := 0, 0
	for path, content := range generated.ProviderContent {
		if content == "" {
			t.Errorf("provider file at %s is empty", path)
		}
		switch {
		case contains(path, filepath.Join("provider", "codex-agents")):
			wrappers++
			if !contains(content, "developer_instructions = ") {
				t.Errorf("Codex wrapper at %s carries no developer_instructions", path)
			}
			if !contains(filepath.Base(path), "agents-") {
				t.Errorf("Codex wrapper %s is not namespaced with the agents- prefix", path)
			}
		case contains(path, filepath.Join("provider", "roles")):
			roleCopies++
		default:
			t.Errorf("unexpected provider file: %s", path)
		}
	}
	if wrappers == 0 || wrappers != roleCopies {
		t.Errorf("expected one wrapper and one role copy per role, got %d and %d", wrappers, roleCopies)
	}

	// Check routing.json
	if generated.UpdatedRouting == "" {
		t.Errorf("UpdatedRouting is empty")
	}
	var routingData map[string]interface{}
	if err := json.Unmarshal([]byte(generated.UpdatedRouting), &routingData); err != nil {
		t.Errorf("UpdatedRouting is not valid JSON: %v", err)
	}

	// Check that knowledge_focus was added/updated
	if kf, ok := routingData["knowledge_focus"]; !ok || kf == nil {
		t.Errorf("UpdatedRouting missing knowledge_focus field")
	}
}

func TestWriteRoleMetadataFiles(t *testing.T) {
	// Use temp directory for test
	tmpDir := t.TempDir()

	// Create minimal directory structure
	for _, dir := range []string{"roster", "provider/codex-agents"} {
		if err := os.MkdirAll(filepath.Join(tmpDir, dir), 0755); err != nil {
			t.Fatalf("cannot create directory: %v", err)
		}
	}

	// Create test files
	generated := &GeneratedRoleMetadataFiles{
		CatalogYAML: "agents:\n  test-role:\n    definition: test\n",
		CatalogJSON: `{"test-role": {"model": "sonnet"}}`,
		ProviderContent: map[string]string{
			filepath.Join(tmpDir, "provider", "codex-agents", "agents-test-role.toml"): "name = \"agents-test-role\"\n",
		},
		UpdatedRouting:  `{"knowledge_focus": {"test-role": "test"}}`,
		RoutingJSONPath: filepath.Join(tmpDir, "routing.json"),
	}

	err := WriteRoleMetadataFiles(tmpDir, generated)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Verify files were written
	catalogPath := filepath.Join(tmpDir, "roster", "catalog.yaml")
	if _, err := os.Stat(catalogPath); err != nil {
		t.Errorf("catalog.yaml not written: %v", err)
	}

	jsonPath := filepath.Join(tmpDir, "provider", "agent-catalog.json")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("agent-catalog.json not written: %v", err)
	}

	wrapperPath := filepath.Join(tmpDir, "provider", "codex-agents", "agents-test-role.toml")
	if _, err := os.Stat(wrapperPath); err != nil {
		t.Errorf("codex wrapper not written: %v", err)
	}
}

func TestCheckRoleMetadata(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory structure
	for _, dir := range []string{"roster", "provider/codex-agents"} {
		if err := os.MkdirAll(filepath.Join(tmpDir, dir), 0755); err != nil {
			t.Fatalf("cannot create directory: %v", err)
		}
	}

	// Create test files that match
	generated := &GeneratedRoleMetadataFiles{
		CatalogYAML: "agents:\n  test:\n",
		CatalogJSON: `{"test": {}}`,
		ProviderContent: map[string]string{
			filepath.Join(tmpDir, "provider", "codex-agents", "agents-test.toml"): "name = \"agents-test\"\n",
		},
		UpdatedRouting:  `{"knowledge_focus": {}}`,
		RoutingJSONPath: filepath.Join(tmpDir, "routing.json"),
	}

	// Write the files
	if err := WriteRoleMetadataFiles(tmpDir, generated); err != nil {
		t.Fatalf("cannot write files: %v", err)
	}

	// Check should pass
	current, stale, err := CheckRoleMetadata(tmpDir, generated)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if !current {
		t.Errorf("check should pass, got stale files: %v", stale)
	}
	if len(stale) > 0 {
		t.Errorf("expected no stale files, got: %v", stale)
	}

	// Modify a file and check again
	catalogPath := filepath.Join(tmpDir, "roster", "catalog.yaml")
	if err := os.WriteFile(catalogPath, []byte("modified"), 0644); err != nil {
		t.Fatalf("cannot modify file: %v", err)
	}

	current, stale, err = CheckRoleMetadata(tmpDir, generated)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if current {
		t.Errorf("check should fail after modification")
	}
	if len(stale) == 0 {
		t.Errorf("expected stale files, got none")
	}
}

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
