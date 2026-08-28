package orchestration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var runRecordDisposition = regexp.MustCompile(`(?m)^disposition: <([a-z|-]+)>$`)

// TestEnvelopeDispositionsCoverTheRunRecord holds handoffDispositions as a
// superset of the consolidated run record's vocabulary. The two describe
// different artifacts, so they are allowed to differ -- but an agent given the
// run record's list and then asked for a final handoff will reach for a value
// from it, and anything this map rejects discards the whole envelope silently.
func TestEnvelopeDispositionsCoverTheRunRecord(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	contract := filepath.Join(wd, "../..", ".agents", "skills", "run-agent-orchestration",
		"references", "dispatch-contract.md")
	raw, err := os.ReadFile(contract)
	if err != nil {
		t.Fatalf("read dispatch contract: %v", err)
	}
	match := runRecordDisposition.FindSubmatch(raw)
	if match == nil {
		t.Fatal("no `disposition: <a|b|c>` line in dispatch-contract.md; if the run " +
			"record's shape changed, update this test to read it from wherever it now lives")
	}
	for _, value := range strings.Split(string(match[1]), "|") {
		if !handoffDispositions[value] {
			t.Errorf("dispatch-contract.md offers disposition %q but handoffDispositions rejects it, "+
				"so an envelope carrying it is silently dropped", value)
		}
	}
}

// TestPlanOnlyIsCaptured is the case that motivated the alignment: an agent
// that produced a plan and stopped.
func TestPlanOnlyIsCaptured(t *testing.T) {
	_, err := validateFinalHandoff(map[string]any{
		"kind":           "cadre-final-handoff",
		"schema_version": FinalHandoffSchemaVersion,
		"handoff": map[string]any{
			"summary":     "produced a dispatch plan, executed nothing",
			"disposition": "plan-only",
		},
		"artifacts":    []any{},
		"derived_from": []any{},
	})
	if err != nil {
		t.Fatalf("plan-only must capture, got: %v", err)
	}
}

// TestGateDecisionSpellingsAreStillRejected keeps the widening honest. The
// kernel's approved/rejected are a different vocabulary for a different
// artifact and must not quietly become valid envelope dispositions.
func TestGateDecisionSpellingsAreStillRejected(t *testing.T) {
	for _, value := range []string{"approved", "rejected", "invented"} {
		_, err := validateFinalHandoff(map[string]any{
			"kind":           "cadre-final-handoff",
			"schema_version": FinalHandoffSchemaVersion,
			"handoff": map[string]any{
				"summary":     "probe",
				"disposition": value,
			},
			"artifacts":    []any{},
			"derived_from": []any{},
		})
		if err == nil {
			t.Errorf("disposition %q must be rejected", value)
		}
	}
}

// TestArtifactKeysMatchTheDocumentedManifest pins the manifest's field set to
// what handoff-contracts.md tells an agent to send. There is no machine-readable
// counterpart to diff against, so this is the canary: changing the map without
// changing the prose fails here, and an entry an agent writes from the prose is
// what the validator accepts. The kernel's artifact record is a different object
// and spells its identifier artifact_id, which must stay rejected.
func TestArtifactKeysMatchTheDocumentedManifest(t *testing.T) {
	documented := map[string]bool{
		"id": true, "kind": true, "revision": true, "digest": true, "uri": true,
	}
	for key := range artifactKeys {
		if !documented[key] {
			t.Errorf("artifactKeys accepts %q, which handoff-contracts.md does not document", key)
		}
	}
	for key := range documented {
		if !artifactKeys[key] {
			t.Errorf("handoff-contracts.md documents %q, which artifactKeys rejects", key)
		}
	}
	if artifactKeys["artifact_id"] {
		t.Error("artifact_id is the kernel's spelling for a different object and must stay rejected")
	}
}
