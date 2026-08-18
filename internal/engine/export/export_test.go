package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/deagy/cadre/cli/internal/engine/contracts"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(working)))
}

func loadPinnedRecords(t *testing.T) map[string]map[string]any {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "python_records.json"))
	if err != nil {
		t.Fatalf("reading the pinned records: %v", err)
	}
	var pinned map[string]map[string]any
	if err := json.Unmarshal(contents, &pinned); err != nil {
		t.Fatalf("parsing the pinned records: %v", err)
	}
	if len(pinned) == 0 {
		t.Fatal("no pinned records; this guard checked nothing")
	}
	return pinned
}

// roundTrip renders a record through JSON so the comparison is between two
// decoded documents rather than between Go types and decoded JSON -- ints
// against float64 would otherwise differ for no real reason.
func roundTrip(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	delete(decoded, "recorded_at") // wall-clock; the pinned side drops it too
	return decoded
}

// The Python's records, pinned.
//
// recorded_at is dropped on both sides: it is datetime.now(), so it is the one
// field that cannot agree.
func TestRecordsMatchThePython(t *testing.T) {
	profile, err := contracts.LoadProfile(filepath.Join(repoRoot(t),
		"providers", "agentic-sdlc-defaults", "profiles", "generic", "profile.json"))
	if err != nil {
		t.Fatalf("loading the generic profile: %v", err)
	}

	approvedG1 := func(gateID string) map[string]any {
		gate := basePlaceholderGate(gateID)
		gate["status"] = "approved"
		return gate
	}

	// The python fixture was `dict(approved_g1, gate_id=g)`: it overrides only
	// gate_id, so every gate keeps G1's name. Reproduced exactly -- a
	// differential is only meaningful when both sides get the same input,
	// however odd that input is.
	allApproved := map[string]any{}
	for _, gateID := range AllGateIDs {
		gate := approvedG1("G1")
		gate["gate_id"] = gateID
		allApproved[gateID] = gate
	}

	cases := map[string]struct {
		state   map[string]any
		options Options
	}{
		"empty_state":   {map[string]any{}, Options{}},
		"empty_strings": {map[string]any{"task_id": "", "classification": "", "scope": ""}, Options{}},
		"one_modelled_gate": {
			map[string]any{"task_id": "t", "lifecycle_gates": map[string]any{"G1": approvedG1("G1")}},
			Options{SequenceGateIDs: []string{"G1", "G2"}},
		},
		"ignored_gates": {
			map[string]any{"task_id": "t"},
			Options{SequenceGateIDs: []string{"G1", "G2", "G3"}, IgnoredGateIDs: []string{"G2"}},
		},
		// An explicitly empty sequence is not the same as none supplied.
		"empty_sequence": {map[string]any{"task_id": "t"}, Options{SequenceGateIDs: []string{}}},
		"with_bindings": {
			map[string]any{"task_id": "t"},
			Options{SequenceGateIDs: []string{"G1"}, GateBindings: profile.GateBindings},
		},
		"all_approved": {
			map[string]any{"task_id": "t", "lifecycle_gates": allApproved},
			Options{},
		},
	}

	pinned := loadPinnedRecords(t)
	for name, scenario := range cases {
		t.Run(name, func(t *testing.T) {
			want, present := pinned[name]
			if !present {
				t.Fatalf("no pinned record named %q", name)
			}
			delete(want, "recorded_at")

			got := roundTrip(t, RunRecord(scenario.state, scenario.options))

			for _, field := range sortedFields(want) {
				if !reflect.DeepEqual(got[field], want[field]) {
					gotJSON, _ := json.Marshal(got[field])
					wantJSON, _ := json.Marshal(want[field])
					t.Errorf("%s differs\n  go:     %s\n  python: %s", field, gotJSON, wantJSON)
				}
			}
			for _, field := range sortedFields(got) {
				if _, expected := want[field]; !expected {
					t.Errorf("go emitted %q, which the python record does not have", field)
				}
			}
		})
	}
}

func sortedFields(from map[string]any) []string {
	fields := make([]string, 0, len(from))
	for field := range from {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// The hardcoded gate names and phases are a second source of truth.
//
// They agree with lifecycle-gates.json today. This exists to find out when
// they stop: a record naming G6 "Verification and Test" while the contract
// calls it something else is wrong in a way nothing else here would notice.
func TestGateNamesAndPhasesMatchTheContract(t *testing.T) {
	gates, err := contracts.LoadLifecycleGates(
		filepath.Join(repoRoot(t), "kernel", "contracts", "lifecycle-gates.json"))
	if err != nil {
		t.Fatalf("loading the lifecycle contract: %v", err)
	}
	if len(gates) == 0 {
		t.Fatal("the contract declares no gates; this guard read nothing")
	}

	for _, gate := range gates {
		if name := gateNames[gate.ID]; name != gate.Name {
			t.Errorf("gateNames[%s] = %q, the contract says %q", gate.ID, name, gate.Name)
		}
		if phase := phaseByGateID[gate.ID]; phase != gate.Phase {
			t.Errorf("phaseByGateID[%s] = %q, the contract says %q", gate.ID, phase, gate.Phase)
		}
	}
	if len(gateNames) != len(gates) {
		t.Errorf("gateNames holds %d entries, the contract declares %d gates", len(gateNames), len(gates))
	}
}

// An in-sequence gate that has not been reached must never export as
// not-applicable: "still to do" and "out of scope" are opposite claims, and an
// earlier version of this pipeline conflated them.
func TestAnUnreachedInSequenceGateIsPendingNotNotApplicable(t *testing.T) {
	record := RunRecord(map[string]any{"task_id": "t"}, Options{SequenceGateIDs: []string{"G1", "G2"}})
	gates, _ := record["lifecycle_gates"].([]any)
	if len(gates) != 10 {
		t.Fatalf("record carries %d gates, want 10", len(gates))
	}

	first, _ := gates[0].(map[string]any)
	if first["applicability"] != "applicable" || first["status"] != "pending" {
		t.Errorf("G1 exported as %v/%v, want applicable/pending",
			first["applicability"], first["status"])
	}
	third, _ := gates[2].(map[string]any)
	if third["applicability"] != "not-applicable" {
		t.Errorf("G3 is outside the sequence but exported as %v", third["applicability"])
	}
}

// nil and empty SequenceGateIDs mean different things.
func TestAnEmptySequenceIsNotTheSameAsNone(t *testing.T) {
	none := RunRecord(map[string]any{"task_id": "t"}, Options{})
	empty := RunRecord(map[string]any{"task_id": "t"}, Options{SequenceGateIDs: []string{}})

	applicable := func(record map[string]any) int {
		count := 0
		gates, _ := record["lifecycle_gates"].([]any)
		for _, entry := range gates {
			gate, _ := entry.(map[string]any)
			if gate["applicability"] == "applicable" {
				count++
			}
		}
		return count
	}

	if applicable(none) != 10 {
		t.Errorf("no sequence supplied: %d applicable gates, want all 10", applicable(none))
	}
	if applicable(empty) != 0 {
		t.Errorf("an explicitly empty sequence: %d applicable gates, want 0", applicable(empty))
	}
}
