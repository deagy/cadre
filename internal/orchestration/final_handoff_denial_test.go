package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func denialSchemaProperties(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(wd, "../..", "roster", "shared",
		"output-schemas", "denial.schema.json"))
	if err != nil {
		t.Fatalf("read denial schema: %v", err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse denial schema: %v", err)
	}
	if len(schema.Properties) == 0 {
		t.Fatal("denial schema declares no properties")
	}
	return schema.Properties
}

// TestDenialKeysCoverTheDenialSchema holds the capture allowlist and the
// documented contract together, the check whose absence let findingKeys drift.
func TestDenialKeysCoverTheDenialSchema(t *testing.T) {
	properties := denialSchemaProperties(t)
	for property := range properties {
		if _, ok := denialKeys[property]; !ok {
			t.Errorf("denial.schema.json declares %q but denialKeys rejects it", property)
		}
	}
	for key := range denialKeys {
		if _, ok := properties[key]; !ok {
			t.Errorf("denialKeys accepts %q but denial.schema.json does not declare it", key)
		}
	}
}

func denialEnvelope(denial map[string]any) map[string]any {
	return map[string]any{
		"kind":           "cadre-final-handoff",
		"schema_version": FinalHandoffSchemaVersion,
		"handoff": map[string]any{
			"summary":     "review complete",
			"disposition": "request-changes",
			"denials":     []any{denial},
		},
		"artifacts":    []any{},
		"derived_from": []any{},
	}
}

func baseDenial() map[string]any {
	return map[string]any{
		"denial_id":               "DEN-1",
		"task_id":                 "TASK-42",
		"revision":                "abc123",
		"denier":                  "code-reviewer#1",
		"author_within_authority": true,
		"invalidates":             []any{},
		"findings": []any{map[string]any{
			"id": "SEC-1", "title": "probe", "severity": "low",
			"summary": "probe", "evidence": []any{"probe"}, "owner": "probe",
		}},
	}
}

func TestAmendDenialCaptures(t *testing.T) {
	d := baseDenial()
	d["disposition"] = "amend"
	d["reentry_step"] = "acceptance-criteria"
	d["amend_attempt"] = 1
	if _, err := validateFinalHandoff(denialEnvelope(d)); err != nil {
		t.Fatalf("an amend denial must capture, got: %v", err)
	}
}

func TestAmendWithoutReentryStepIsRejected(t *testing.T) {
	d := baseDenial()
	d["disposition"] = "amend"
	d["amend_attempt"] = 1
	if _, err := validateFinalHandoff(denialEnvelope(d)); err == nil {
		t.Fatal("an amend that does not say where work resumes must be rejected")
	}
}

func TestHaltWithoutLiftConditionIsRejected(t *testing.T) {
	d := baseDenial()
	d["disposition"] = "halt"
	if _, err := validateFinalHandoff(denialEnvelope(d)); err == nil {
		t.Fatal("a halt that names no lift condition must be rejected")
	}
}

// TestObjectionCarriesNoDisposition is the case real refusals produced: two
// reviewers returned request-changes against a Product Owner decision already
// recorded. No agent could amend it, so the denial is an objection.
func TestObjectionCarriesNoDisposition(t *testing.T) {
	d := baseDenial()
	d["author_within_authority"] = false
	if _, err := validateFinalHandoff(denialEnvelope(d)); err != nil {
		t.Fatalf("an objection must capture without a disposition, got: %v", err)
	}
}

func TestObjectionWithADispositionIsRejected(t *testing.T) {
	d := baseDenial()
	d["author_within_authority"] = false
	d["disposition"] = "amend"
	d["reentry_step"] = "step-1"
	d["amend_attempt"] = 1
	if _, err := validateFinalHandoff(denialEnvelope(d)); err == nil {
		t.Fatal("an objection claiming a disposition asserts authority it does not have")
	}
}

func TestDenialWithoutFindingsIsRejected(t *testing.T) {
	d := baseDenial()
	d["disposition"] = "escalate"
	delete(d, "findings")
	if _, err := validateFinalHandoff(denialEnvelope(d)); err == nil {
		t.Fatal("a denial citing no finding is an opinion and must be rejected")
	}
}

func TestDenialMustStateWhatItInvalidates(t *testing.T) {
	d := baseDenial()
	d["disposition"] = "escalate"
	delete(d, "invalidates")
	if _, err := validateFinalHandoff(denialEnvelope(d)); err == nil {
		t.Fatal("silence and \"nothing\" must be distinguishable")
	}
}

// TestTheSchemaAndTheValidatorAgree runs the same denials through
// denial.schema.json itself and through the capture path, and fails when they
// disagree. The Go side reimplements the schema's if/then requirements rather
// than compiling it, so without this the two are free to drift -- which is the
// exact failure this contract was written after.
func TestTheSchemaAndTheValidatorAgree(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	schemaPath := filepath.Join(wd, "../..", "roster", "shared",
		"output-schemas", "denial.schema.json")

	amend := baseDenial()
	amend["disposition"] = "amend"
	amend["reentry_step"] = "acceptance-criteria"
	amend["amend_attempt"] = 1

	amendNoStep := baseDenial()
	amendNoStep["disposition"] = "amend"
	amendNoStep["amend_attempt"] = 1

	halt := baseDenial()
	halt["disposition"] = "halt"

	objection := baseDenial()
	objection["author_within_authority"] = false

	objectionWithDisposition := baseDenial()
	objectionWithDisposition["author_within_authority"] = false
	objectionWithDisposition["disposition"] = "escalate"

	noDisposition := baseDenial()

	cases := map[string]map[string]any{
		"amend":                   amend,
		"amend without step":      amendNoStep,
		"halt without lift":       halt,
		"objection":               objection,
		"objection + disposition": objectionWithDisposition,
		"within authority, none":  noDisposition,
	}

	for name, denial := range cases {
		// The schema validates a denial document on its own.
		schemaFindings, err := schemaErrors(roundTrip(t, denial), schemaPath)
		if err != nil {
			t.Fatalf("%s: compiling denial.schema.json: %v", name, err)
		}
		schemaAccepts := len(schemaFindings) == 0

		// The capture path validates it inside an envelope.
		_, captureErr := validateFinalHandoff(denialEnvelope(denial))
		captureAccepts := captureErr == nil

		if schemaAccepts != captureAccepts {
			t.Errorf("%s: denial.schema.json accepts=%v but the capture path accepts=%v\n  schema said: %v\n  capture said: %v",
				name, schemaAccepts, captureAccepts, schemaFindings, captureErr)
		}
	}
}

// roundTrip normalizes a hand-built map through JSON, so the schema validator
// sees the float64/any shapes a real decoded envelope carries.
func roundTrip(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return decoded
}
