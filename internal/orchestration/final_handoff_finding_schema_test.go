package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// findingSchemaPath is the contract roles are told to emit findings against.
// The capture validator has to accept everything it describes, or a role that
// follows the documentation produces an envelope this package silently drops.
func findingSchemaPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	return filepath.Join(wd, "../..", "roster", "shared", "output-schemas", "finding.schema.json")
}

// TestFindingKeysCoverTheFindingSchema is the check whose absence let the two
// contracts drift: findingKeys once allowed six of the schema's twelve
// properties, so status and recommendation -- both required by the schema --
// were rejected here.
func TestFindingKeysCoverTheFindingSchema(t *testing.T) {
	raw, err := os.ReadFile(findingSchemaPath(t))
	if err != nil {
		t.Fatalf("read finding schema: %v", err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse finding schema: %v", err)
	}
	if len(schema.Properties) == 0 {
		t.Fatal("finding schema declares no properties")
	}
	for property := range schema.Properties {
		if _, ok := findingKeys[property]; !ok {
			t.Errorf("finding.schema.json declares %q but findingKeys rejects it", property)
		}
	}
	for key := range findingKeys {
		if _, ok := schema.Properties[key]; !ok {
			t.Errorf("findingKeys accepts %q but finding.schema.json does not declare it", key)
		}
	}
}

// TestSchemaConformantFindingIsCaptured is the defect itself: an envelope whose
// finding carries the eight properties finding.schema.json marks required.
// Before the fix this returned "entries must use the structured finding fields
// only" and the whole handoff was discarded.
func TestSchemaConformantFindingIsCaptured(t *testing.T) {
	captured, err := validateFinalHandoff(finalHandoffWithFinding(map[string]any{
		"id":             "SEC-1",
		"title":          "hardcoded credential",
		"severity":       "high",
		"status":         "open",
		"summary":        "a credential is committed to the repository",
		"evidence":       []any{"config/app.yaml:12"},
		"recommendation": "move it to the secret store",
		"owner":          "security-lead",
	}))
	if err != nil {
		t.Fatalf("a schema-conformant finding must capture, got: %v", err)
	}
	findings := capturedFindings(t, captured)
	if got := findings[0]["recommendation"]; got != "move it to the secret store" {
		t.Errorf("recommendation did not survive capture: %v", got)
	}
	if got := findings[0]["status"]; got != "open" {
		t.Errorf("status did not survive capture: %v", got)
	}
}

// TestCoverageFindingCarriesNullOwner covers the case the denial contract needs:
// no role's remit covered the defect, so the finding names no owner and the
// roster owns it.
func TestCoverageFindingCarriesNullOwner(t *testing.T) {
	captured, err := validateFinalHandoff(finalHandoffWithFinding(map[string]any{
		"id":              "COV-1",
		"title":           "no role reviews migration reversibility",
		"severity":        "medium",
		"status":          "open",
		"summary":         "the escape fell outside every role's declared remit",
		"evidence":        []any{"gate G7 finding with no upstream reviewer"},
		"recommendation":  "add reversibility to a review role's remit",
		"affected_assets": []any{"db/migrations"},
		"owner":           nil,
	}))
	if err != nil {
		t.Fatalf("a coverage finding must capture, got: %v", err)
	}
	findings := capturedFindings(t, captured)
	if owner, present := findings[0]["owner"]; !present || owner != nil {
		t.Errorf("a null owner must survive capture as null, got %#v (present=%v)", owner, present)
	}
}

// TestUnknownFindingFieldIsStillRejected keeps the widening honest: the
// allowlist grew to the schema, not to anything a child invents.
func TestUnknownFindingFieldIsStillRejected(t *testing.T) {
	_, err := validateFinalHandoff(finalHandoffWithFinding(map[string]any{
		"id": "SEC-1", "title": "probe", "severity": "low",
		"summary": "probe", "evidence": []any{"probe"}, "owner": "probe",
		"invented_field": "probe",
	}))
	if err == nil {
		t.Fatal("a finding field outside the schema must still be rejected")
	}
}

func finalHandoffWithFinding(finding map[string]any) map[string]any {
	return map[string]any{
		"kind":           "cadre-final-handoff",
		"schema_version": FinalHandoffSchemaVersion,
		"handoff": map[string]any{
			"summary":     "review complete",
			"disposition": "request-changes",
			"findings":    []any{finding},
		},
		"artifacts":    []any{},
		"derived_from": []any{},
	}
}

func capturedFindings(t *testing.T, captured map[string]any) []map[string]any {
	t.Helper()
	handoff, ok := captured["handoff"].(map[string]any)
	if !ok {
		t.Fatalf("captured envelope has no handoff body: %#v", captured)
	}
	findings, ok := handoff["findings"].([]map[string]any)
	if !ok || len(findings) == 0 {
		t.Fatalf("captured handoff has no findings: %#v", handoff["findings"])
	}
	return findings
}
