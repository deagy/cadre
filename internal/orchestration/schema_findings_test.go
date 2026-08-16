package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What schema validation reports, and how much of it.
//
// schema_validate_test.go covers the individual checks. This covers two
// properties of the run as a whole:
//
//   - it reports every defect it finds, not the first. A validator that stops
//     at one turns fixing a hand-edited file into a sequence of edit-and-rerun
//     rounds, and the person doing it has no idea how many are left.
//   - a finding names where the defect is. "expected array, but got string" is
//     a search across a hundred routes; the same finding with a JSON pointer
//     is an edit.
//
// Both are why the Python validator existed alongside the schema libraries,
// and neither was asserted on the Go side.

func schemaFixture(t *testing.T, catalog string, routing map[string]any) SchemaValidationPaths {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "orchestration"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "catalog.yaml"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	// The definition wellFormedCatalog names, so a case injecting a defect
	// elsewhere is not also reporting a missing role file.
	if err := os.MkdirAll(filepath.Join(root, "a-role"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a-role", "AGENT.md"),
		[]byte("# A role\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(routing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "orchestration", "routing.json"),
		encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	checkout := checkoutRoot(t)
	return SchemaValidationPaths{
		CatalogPath:       filepath.Join(root, "catalog.yaml"),
		RoutingPath:       filepath.Join(root, "orchestration", "routing.json"),
		CatalogSchemaPath: filepath.Join(checkout, "roster", "catalog.schema.json"),
		RoutingSchemaPath: filepath.Join(checkout, "roster", "orchestration", "routing.schema.json"),
		AgentsRoot:        root,
	}
}

// wellFormedRouting is a document the schema accepts, so a defect injected
// into it is the only reason a finding appears.
//
// Minimal but genuinely valid, which took a correction: the first version
// used empty lists throughout and produced five findings of its own -- the
// schemas require a version, at least one agent, a non-empty
// change_intake.agents and cross_stack.route_ids, and a minimum_matches that
// does not exceed the route_ids it counts.
func wellFormedRouting() map[string]any {
	return map[string]any{
		"version":         1,
		"routes":          []any{},
		"risk_rules":      []any{},
		"team_recipes":    []any{},
		"knowledge_focus": map[string]any{"a-role": "prior defects"},
		"change_intake": map[string]any{
			"keywords": []any{"add"}, "agents": []any{"a-role"},
		},
		"cross_stack": map[string]any{
			"route_ids": []any{"alpha", "beta"}, "support": []any{"a-role"},
			"minimum_matches": 2,
		},
	}
}

// wellFormedCatalog names one role whose definition exists on disk, written
// into the fixture root by schemaFixture's caller when the case needs it.
const wellFormedCatalog = "version: 1\nagents:\n  a-role:\n    definition: a-role/AGENT.md\n    phase: planning\n    capability: read_only\n    model: haiku\n    codex_model: gpt-5.6-luna\n    reasoning_effort: low\n"

func TestACleanTreeProducesNoFindings(t *testing.T) {
	// The baseline. Without it, a validator that reported something about
	// everything would satisfy each case below.
	findings, err := RunSchemaValidation(schemaFixture(t,
		wellFormedCatalog, wellFormedRouting()))
	if err != nil {
		t.Fatalf("RunSchemaValidation: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("a well-formed pair produced %d finding(s): %v", len(findings), findings)
	}
}

func TestTwoIndependentDefectsAreBothReportedInOneRun(t *testing.T) {
	// One defect per file. A validator that returned after the first would
	// report the catalog and stay silent about routing, so the person fixing
	// it finds the second only after fixing the first.
	routing := wellFormedRouting()
	routing["routes"] = []any{map[string]any{"id": "r1", "keywords": "not-a-list"}}

	findings, err := RunSchemaValidation(schemaFixture(t,
		strings.Replace(wellFormedCatalog, "a-role/AGENT.md", "missing/AGENT.md", 1), routing))
	if err != nil {
		t.Fatalf("RunSchemaValidation: %v", err)
	}
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "catalog.yaml") {
		t.Errorf("the catalog defect was not reported:\n%s", joined)
	}
	if !strings.Contains(joined, "routing.json") {
		t.Errorf("the routing defect was not reported; validation stopped at the "+
			"first file:\n%s", joined)
	}
}

func TestTwoDefectsInOneFileAreBothReported(t *testing.T) {
	// The same property within a single document, which is the harder half: a
	// schema library asked for one error returns one, and a caller that does
	// not ask for all of them never learns there were more.
	routing := wellFormedRouting()
	routing["routes"] = []any{
		map[string]any{"id": "first-route", "keywords": "not-a-list"},
		map[string]any{"id": "second-route", "paths": 42},
	}

	findings, err := RunSchemaValidation(schemaFixture(t, wellFormedCatalog, routing))
	if err != nil {
		t.Fatalf("RunSchemaValidation: %v", err)
	}
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "routes/0") {
		t.Errorf("the first route's defect was not reported:\n%s", joined)
	}
	if !strings.Contains(joined, "routes/1") {
		t.Errorf("the second route's defect was not reported; validation stopped "+
			"at the first defect in the file:\n%s", joined)
	}
}

func TestAFindingSaysWhereTheDefectIs(t *testing.T) {
	// "expected array, but got string" is a search across a hundred routes.
	// The same finding with a location is an edit.
	routing := wellFormedRouting()
	routing["routes"] = []any{
		map[string]any{"id": "alpha"},
		map[string]any{"id": "beta"},
		map[string]any{"id": "the-broken-one", "keywords": "not-a-list"},
	}

	findings, err := RunSchemaValidation(schemaFixture(t, wellFormedCatalog, routing))
	if err != nil {
		t.Fatalf("RunSchemaValidation: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("a type violation produced no finding")
	}
	joined := strings.Join(findings, "\n")
	for _, want := range []string{"routing.json", "routes/2", "keywords"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no finding names %q:\n%s", want, joined)
		}
	}
	// And the well-formed routes are not implicated.
	for _, quiet := range []string{"routes/0", "routes/1"} {
		if strings.Contains(joined, quiet) {
			t.Errorf("a well-formed route was reported (%s):\n%s", quiet, joined)
		}
	}
}

func TestAMissingDefinitionNamesTheRoleAndThePath(t *testing.T) {
	// A definition pointing at a file that is not there is the defect a schema
	// cannot see -- the value is a well-formed string. The finding has to
	// carry both halves, because the role id alone does not say what to create
	// and the path alone does not say who wants it.
	findings, err := RunSchemaValidation(schemaFixture(t,
		strings.NewReplacer(
			"a-role:", "sample-role:",
			"a-role/AGENT.md", "engineering/sample-role/AGENT.md",
		).Replace(wellFormedCatalog),
		wellFormedRouting()))
	if err != nil {
		t.Fatalf("RunSchemaValidation: %v", err)
	}
	joined := strings.Join(findings, "\n")
	for _, want := range []string{"sample-role", "engineering/sample-role/AGENT.md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the finding does not name %q:\n%s", want, joined)
		}
	}
}

func TestTheSchemasDeclareTheDialectTheyAreValidatedUnder(t *testing.T) {
	// The schemas are compiled by a library that picks its behaviour from
	// $schema. A file that declared an older draft, or none, would validate
	// under different rules than it was written for -- and the difference is
	// quiet: most keywords mean the same thing in both, so it surfaces only on
	// the ones that changed.
	checkout := checkoutRoot(t)
	for _, relative := range []string{
		filepath.Join("roster", "catalog.schema.json"),
		filepath.Join("roster", "orchestration", "routing.schema.json"),
		filepath.Join("roster", "orchestration", "selection.schema.json"),
	} {
		path := filepath.Join(checkout, relative)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Skipf("not running inside a source checkout: %v", err)
		}
		var document struct {
			Schema string `json:"$schema"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Errorf("%s does not parse: %v", relative, err)
			continue
		}
		if document.Schema != "https://json-schema.org/draft/2020-12/schema" {
			t.Errorf("%s declares $schema %q, not draft 2020-12", relative, document.Schema)
		}
	}
}
