package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func schemaPath(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(working)))
	return filepath.Join(root, "kernel", "contracts", "run-record.schema.json")
}

func gateSchema(t *testing.T) (properties map[string]any, required []string) {
	t.Helper()
	contents, err := os.ReadFile(schemaPath(t))
	if err != nil {
		t.Fatalf("reading the run-record schema: %v", err)
	}
	var document struct {
		Defs struct {
			Gate struct {
				Properties map[string]any `json:"properties"`
				Required   []string       `json:"required"`
			} `json:"gate"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("parsing the run-record schema: %v", err)
	}
	if len(document.Defs.Gate.Properties) == 0 {
		t.Fatal("the schema's gate definition has no properties; this guard read nothing")
	}
	return document.Defs.Gate.Properties, document.Defs.Gate.Required
}

func jsonTags(t *testing.T, value any) []string {
	t.Helper()
	typed := reflect.TypeOf(value)
	tags := make([]string, 0, typed.NumField())
	for i := 0; i < typed.NumField(); i++ {
		tag := typed.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		tags = append(tags, strings.Split(tag, ",")[0])
	}
	sort.Strings(tags)
	return tags
}

// GateState is a field-for-field port of the schema's gate definition, and
// this is what makes that sentence checkable.
//
// The checkpointed state for a task *is* its run record, so a field that
// drifts -- renamed, added, dropped -- produces a record the kernel rejects at
// the point someone tries to export a real run. Deriving the expected set from
// the schema means the schema changing is what fails, rather than a hand-typed
// list nobody revisits.
func TestGateStateMatchesTheRunRecordSchema(t *testing.T) {
	properties, _ := gateSchema(t)

	want := make([]string, 0, len(properties))
	for name := range properties {
		want = append(want, name)
	}
	sort.Strings(want)

	got := jsonTags(t, GateState{})

	if strings.Join(got, ",") != strings.Join(want, ",") {
		missing, extra := difference(want, got), difference(got, want)
		t.Errorf("GateState does not match run-record.schema.json's gate definition\n"+
			"  in the schema but not in GateState: %v\n"+
			"  in GateState but not in the schema: %v", missing, extra)
	}
}

func difference(from, minus []string) []string {
	present := make(map[string]bool, len(minus))
	for _, name := range minus {
		present[name] = true
	}
	var only []string
	for _, name := range from {
		if !present[name] {
			only = append(only, name)
		}
	}
	return only
}

// Every gate property is required, so every key must appear -- including the
// nullable ones, which must render as null rather than vanish.
//
// This is why nothing in GateState carries omitempty and why the nullable
// fields are pointers. A zero value is the easiest thing to construct and the
// most likely to be exported by mistake, so it is what gets checked.
func TestAZeroGateStateEmitsEveryRequiredKey(t *testing.T) {
	_, required := gateSchema(t)
	if len(required) == 0 {
		t.Fatal("the schema marks no gate property required; this guard checked nothing")
	}

	encoded, err := json.Marshal(GateState{})
	if err != nil {
		t.Fatalf("marshalling a zero GateState: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	for _, name := range required {
		if _, present := document[name]; !present {
			t.Errorf("a zero GateState omits %q, which the schema requires", name)
		}
	}
}

// The nullable fields must be null, not "" or absent.
func TestNullableGateFieldsRenderAsNull(t *testing.T) {
	encoded, err := json.Marshal(GateState{})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, name := range []string{
		"applicability_rationale", "decided_at", "required_reentry_gate", "independent_verifier",
	} {
		value, present := document[name]
		if !present {
			t.Errorf("%s is absent; the schema requires the key", name)
			continue
		}
		if value != nil {
			t.Errorf("%s rendered as %#v, want null", name, value)
		}
	}
}

func TestMergeGateUpdatesReplacesPerKeyAndLeavesOthers(t *testing.T) {
	current := map[string]GateState{
		"G1": {GateID: "G1", Status: "approved"},
		"G2": {GateID: "G2", Status: "pending"},
	}
	update := map[string]GateState{
		"G2": {GateID: "G2", Status: "approved"},
		"G3": {GateID: "G3", Status: "pending"},
	}

	merged := MergeGateUpdates(current, update)

	if merged["G1"].Status != "approved" {
		t.Errorf("G1 = %q, want the untouched entry to survive", merged["G1"].Status)
	}
	if merged["G2"].Status != "approved" {
		t.Errorf("G2 = %q, want the update to win", merged["G2"].Status)
	}
	if merged["G3"].GateID != "G3" {
		t.Error("a gate present only in the update was dropped")
	}
	// The reducer runs on parallel branches; mutating current in place would
	// be visible to a branch holding the same map, which is the clobbering it
	// exists to prevent.
	if current["G2"].Status != "pending" {
		t.Error("MergeGateUpdates mutated its input")
	}
	if _, leaked := current["G3"]; leaked {
		t.Error("MergeGateUpdates added a key to its input")
	}
}

func TestMergeGateUpdatesHandlesNilInputs(t *testing.T) {
	if merged := MergeGateUpdates(nil, nil); merged == nil || len(merged) != 0 {
		t.Errorf("MergeGateUpdates(nil, nil) = %v, want an empty map", merged)
	}
	merged := MergeGateUpdates(nil, map[string]GateState{"G1": {GateID: "G1"}})
	if merged["G1"].GateID != "G1" {
		t.Error("an update against nil current was lost")
	}
}

// The slot keying is what stops a redispatch duplicating a gate's outputs.
func TestMergeAgentOutputsOverwritesTheSameSlot(t *testing.T) {
	slot := AgentOutputSlot("G2", "preparer", "requirements-agent")
	current := map[string]map[string]any{slot: {"revision": "stale"}}
	update := map[string]map[string]any{slot: {"revision": "fresh"}}

	merged := MergeAgentOutputs(current, update)

	if len(merged) != 1 {
		t.Errorf("merged holds %d slots, want 1: a redispatch must overwrite, not append", len(merged))
	}
	if merged[slot]["revision"] != "fresh" {
		t.Errorf("slot holds %v, want the fresh output", merged[slot]["revision"])
	}
}

func TestAgentOutputSlotsAreDistinctPerAgentAndKind(t *testing.T) {
	first := AgentOutputSlot("G2", "preparer", "agent-a")
	second := AgentOutputSlot("G2", "preparer", "agent-b")
	third := AgentOutputSlot("G2", "verifier", "agent-a")
	if first == second || first == third || second == third {
		t.Errorf("slots collide: %q %q %q", first, second, third)
	}

	merged := MergeAgentOutputs(
		map[string]map[string]any{first: {"n": 1}},
		map[string]map[string]any{second: {"n": 2}, third: {"n": 3}},
	)
	if len(merged) != 3 {
		t.Errorf("merged holds %d slots, want 3 distinct ones", len(merged))
	}
}
