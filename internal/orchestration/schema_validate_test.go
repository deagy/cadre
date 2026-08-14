package orchestration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindDuplicateCatalogIDs(t *testing.T) {
	text := "agents:\n  backend-engineer:\n    phase: build\n  code-reviewer:\n    phase: review\n  backend-engineer:\n    phase: build\n"
	findings := findDuplicateCatalogIDs(text)
	if len(findings) != 1 {
		t.Fatalf("expected 1 duplicate finding, got %d: %v", len(findings), findings)
	}
}

func TestFindDuplicateCatalogIDsNoDuplicates(t *testing.T) {
	text := "agents:\n  backend-engineer:\n    phase: build\n  code-reviewer:\n    phase: review\n"
	findings := findDuplicateCatalogIDs(text)
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %v", findings)
	}
}

func TestFindDuplicateCatalogIDsOutsideAgentsBlock(t *testing.T) {
	// id-shaped lines before "agents:" must not be flagged.
	text := "other:\n  backend-engineer:\n    x: 1\nagents:\n  backend-engineer:\n    phase: build\n"
	findings := findDuplicateCatalogIDs(text)
	if len(findings) != 0 {
		t.Fatalf("expected no findings (pre-agents-block lines ignored), got %v", findings)
	}
}

func TestFindMissingDefinitions(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "build", "backend-engineer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build", "backend-engineer", "AGENT.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog := map[string]any{
		"agents": map[string]any{
			"backend-engineer": map[string]any{"definition": "build/backend-engineer/AGENT.md"},
			"missing-role":     map[string]any{"definition": "build/missing-role/AGENT.md"},
		},
	}
	findings := findMissingDefinitions(catalog, dir)
	if len(findings) != 1 {
		t.Fatalf("expected 1 missing-definition finding, got %d: %v", len(findings), findings)
	}
}

func TestFindCrossStackInconsistency(t *testing.T) {
	routing := map[string]any{
		"cross_stack": map[string]any{
			"route_ids":       []any{"a", "b"},
			"minimum_matches": float64(3),
		},
	}
	findings := findCrossStackInconsistency(routing)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %v", findings)
	}
}

func TestFindCrossStackInconsistencyOK(t *testing.T) {
	routing := map[string]any{
		"cross_stack": map[string]any{
			"route_ids":       []any{"a", "b"},
			"minimum_matches": float64(2),
		},
	}
	if findings := findCrossStackInconsistency(routing); len(findings) != 0 {
		t.Fatalf("expected no findings, got %v", findings)
	}
}

func TestFindTeamRecipeInconsistencies(t *testing.T) {
	routing := map[string]any{
		"team_recipes": []any{
			map[string]any{
				"id": "core", "type": "fixed",
				"route_ids":       []any{"a"},
				"minimum_matches": float64(2),
			},
			map[string]any{
				"id": "extended", "type": "fixed",
				"members":                  []any{"a", "b"},
				"minimum_members_selected": float64(5),
			},
		},
	}
	findings := findTeamRecipeInconsistencies(routing)
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %v", len(findings), findings)
	}
}

func TestFindTeamRecipeInconsistenciesIgnoresDynamic(t *testing.T) {
	routing := map[string]any{
		"team_recipes": []any{
			map[string]any{"id": "dyn", "type": "dynamic", "minimum_matches": float64(99)},
		},
	}
	if findings := findTeamRecipeInconsistencies(routing); len(findings) != 0 {
		t.Fatalf("expected no findings for a dynamic recipe, got %v", findings)
	}
}

func TestFindDuplicateArrayIDs(t *testing.T) {
	items := []any{
		map[string]any{"id": "a"},
		map[string]any{"id": "b"},
		map[string]any{"id": "a"},
	}
	findings := findDuplicateArrayIDs(items, "routes")
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %v", findings)
	}
}

func TestValidateCatalogSchemaAgainstRealSchema(t *testing.T) {
	repoRoot := realRepoRootForTest(t)
	catalogPath := filepath.Join(repoRoot, "roster", "catalog.yaml")
	schemaPath := filepath.Join(repoRoot, "roster", "catalog.schema.json")
	if _, err := os.Stat(catalogPath); err != nil {
		t.Skip("not running inside the cadre checkout")
	}

	catalogText, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := loadCatalogYAML(catalogPath)
	if err != nil {
		t.Fatalf("loadCatalogYAML: %v", err)
	}
	findings, err := ValidateCatalogSchema(string(catalogText), catalog, schemaPath, filepath.Join(repoRoot, "roster"))
	if err != nil {
		t.Fatalf("ValidateCatalogSchema: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected the real, committed catalog.yaml to be schema-valid; findings: %v", findings)
	}
}

func TestValidateRoutingSchemaAgainstRealSchema(t *testing.T) {
	repoRoot := realRepoRootForTest(t)
	routingPath := filepath.Join(repoRoot, "roster", "orchestration", "routing.json")
	schemaPath := filepath.Join(repoRoot, "roster", "orchestration", "routing.schema.json")
	if _, err := os.Stat(routingPath); err != nil {
		t.Skip("not running inside the cadre checkout")
	}

	routing, err := loadRoutingJSONRaw(routingPath)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := ValidateRoutingSchema(routing, schemaPath)
	if err != nil {
		t.Fatalf("ValidateRoutingSchema: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected the real, committed routing.json to be schema-valid; findings: %v", findings)
	}
}

func TestRunSchemaValidationAgainstRealRepo(t *testing.T) {
	repoRoot := realRepoRootForTest(t)
	if _, err := os.Stat(filepath.Join(repoRoot, "roster", "catalog.yaml")); err != nil {
		t.Skip("not running inside the cadre checkout")
	}
	findings, err := RunSchemaValidation(DefaultSchemaValidationPaths(repoRoot))
	if err != nil {
		t.Fatalf("RunSchemaValidation: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected the real, committed roster/ files to be schema-valid; findings: %v", findings)
	}
}

func TestValidateCatalogSchemaCatchesTypeViolation(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.json")
	schema := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {"agents": {"type": "object"}},
		"required": ["agents"]
	}`
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}

	// "agents" as a string instead of an object -- a type violation.
	catalog := map[string]any{"agents": "not-an-object"}
	findings, err := ValidateCatalogSchema("agents: not-an-object\n", catalog, schemaPath, dir)
	if err != nil {
		t.Fatalf("ValidateCatalogSchema: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one schema-violation finding")
	}
}

func realRepoRootForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..")
}
