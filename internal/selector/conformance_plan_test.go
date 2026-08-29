package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The golden corpus pins plan shape. This pins plan validity, and the two are
// not the same guarantee.
//
// select_golden.json records each case's *canonical* plan, which strips
// dispatch_fingerprint, generated_at and provenance -- the keys excluded from
// the fingerprint, because a golden that carried them would change on every
// run. Two of those three are required by selection.schema.json. So a producer
// can reproduce all twenty-five golden cases exactly and still emit documents
// the schema rejects, and nothing in the corpus would notice.
//
// conformance-plan.json is one complete plan, as emitted, kept whole. It is the
// oracle for "does this producer emit a valid plan at all", which matters most
// to a producer that is not this one: selection.schema.json is closed,
// vendored into the wheel and the plugin, and pinned by consumers at whatever
// release they installed.
func conformanceFixture(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "roster", "orchestration", "test", "conformance-plan.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var fixture struct {
		SchemaVersionAtCapture int            `json:"schema_version_at_capture"`
		Plan                   map[string]any `json:"plan"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(fixture.Plan) == 0 {
		t.Fatalf("%s carries no plan; this guard would assert nothing", path)
	}
	if fixture.SchemaVersionAtCapture != SchemaVersion {
		t.Fatalf("the conformance plan was captured at schema_version %d and this producer emits %d.\n"+
			"A schema bump makes the fixture stale: regenerate it, or the oracle certifies a contract "+
			"nobody emits any more.", fixture.SchemaVersionAtCapture, SchemaVersion)
	}
	return fixture.Plan
}

// TestTheConformancePlanCarriesEveryRequiredField is the check the golden
// corpus structurally cannot make.
func TestTheConformancePlanCarriesEveryRequiredField(t *testing.T) {
	plan := conformanceFixture(t)

	schemaPath := filepath.Join("..", "..", "roster", "orchestration", "selection.schema.json")
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read %s: %v", schemaPath, err)
	}
	var schema struct {
		Required   []string       `json:"required"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse %s: %v", schemaPath, err)
	}
	if len(schema.Required) == 0 {
		t.Fatal("the schema declares no required fields; this guard would assert nothing")
	}

	for _, field := range schema.Required {
		if _, present := plan[field]; !present {
			t.Errorf("the conformance plan omits %q, which selection.schema.json requires", field)
		}
	}
	// The schema is closed. A field the schema does not declare would be
	// rejected by a consumer validating against a pinned copy, which is the
	// failure this fixture exists to make visible before it reaches one.
	for field := range plan {
		if _, declared := schema.Properties[field]; !declared {
			t.Errorf("the conformance plan carries %q, which selection.schema.json does not declare", field)
		}
	}
}

// TestTheGoldenCorpusCannotStandInForIt records why both fixtures exist, as a
// test rather than a comment: if the canonical form ever did carry every
// required field, this fails and one of the two becomes redundant.
func TestTheGoldenCorpusCannotStandInForIt(t *testing.T) {
	plan := conformanceFixture(t)
	for _, stripped := range []string{"dispatch_fingerprint", "generated_at"} {
		if _, present := plan[stripped]; !present {
			t.Errorf("the conformance plan is missing %q, so it no longer differs from a "+
				"canonical golden plan and pins nothing the corpus does not", stripped)
		}
	}
}
