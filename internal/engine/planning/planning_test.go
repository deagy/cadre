package planning

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/engine/contracts"
)

func fixtures(t *testing.T) ([]contracts.Gate, []contracts.Route) {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(working)))

	gates, err := contracts.LoadLifecycleGates(filepath.Join(root, "kernel-contracts", "lifecycle-gates.json"))
	if err != nil {
		t.Fatalf("lifecycle gates: %v", err)
	}
	profile, err := contracts.LoadProfile(filepath.Join(root,
		"providers", "agentic-sdlc-defaults", "profiles", "generic", "profile.json"))
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	return gates, profile.Routing
}

// The Python's sequences, pinned, over the real contract and profile.
func TestSequencesMatchThePython(t *testing.T) {
	contentsPath := filepath.Join("testdata", "python_sequences.json")
	contents, err := os.ReadFile(contentsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", contentsPath, err)
	}
	var pinned map[string]any
	if err := json.Unmarshal(contents, &pinned); err != nil {
		t.Fatalf("parsing %s: %v", contentsPath, err)
	}

	gates, routes := fixtures(t)
	cases := map[string]struct {
		text    string
		ignored []string
	}{
		"architecture": {"refactor the architecture of the billing service", nil},
		"no_match":     {"completely unrelated text", nil},
		"uppercase":    {"ARCHITECTURE review", nil},
		"with_ignores": {"refactor the architecture of the billing service", []string{"G2"}},
		"empty_text":   {"", nil},
	}

	for name, scenario := range cases {
		expected, present := pinned[name].([]any)
		if !present {
			t.Fatalf("no pinned sequence named %q", name)
		}
		want := make([]string, 0, len(expected))
		for _, id := range expected {
			want = append(want, id.(string))
		}

		sequence, err := DeriveGateSequence(scenario.text, routes, scenario.ignored, gates)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		got := make([]string, 0, len(sequence))
		for _, gate := range sequence {
			got = append(got, gate.ID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v, python produced %v", name, got, want)
		}
	}

	// The error case is pinned too, message included.
	if _, err := DeriveGateSequence("x", routes, []string{"G99"}, gates); err == nil {
		t.Error("an unknown ignored gate id was accepted")
	} else if !strings.Contains(err.Error(), "unknown lifecycle gates") {
		t.Errorf("error was %q", err)
	}
}

// The sequence is a prefix, not the matched set.
//
// A route that references only G3 still yields G1, G2, G3: a later gate cannot
// be approved while an earlier applicable one is not, so returning the matched
// gates alone would describe a run that could never validate.
func TestTheSequenceIsACumulativePrefix(t *testing.T) {
	gates, _ := fixtures(t)
	routes := []contracts.Route{{ID: "only-g3", Phrases: []string{"deploy"}, Gates: []string{"G3"}}}

	sequence, err := DeriveGateSequence("deploy something", routes, nil, gates)
	if err != nil {
		t.Fatalf("DeriveGateSequence: %v", err)
	}
	if len(sequence) != 3 || sequence[0].ID != "G1" || sequence[2].ID != "G3" {
		t.Errorf("got %d gates starting at %s, want G1..G3", len(sequence), sequence[0].ID)
	}
}

// An ignored gate is removed from the prefix without shortening it.
func TestIgnoringAGateDoesNotTruncateTheSequence(t *testing.T) {
	gates, _ := fixtures(t)
	routes := []contracts.Route{{ID: "to-g3", Phrases: []string{"deploy"}, Gates: []string{"G3"}}}

	sequence, err := DeriveGateSequence("deploy something", routes, []string{"G2"}, gates)
	if err != nil {
		t.Fatalf("DeriveGateSequence: %v", err)
	}
	ids := []string{}
	for _, gate := range sequence {
		ids = append(ids, gate.ID)
	}
	if strings.Join(ids, ",") != "G1,G3" {
		t.Errorf("got %v, want G1,G3 -- ignoring G2 must not stop the prefix at G1", ids)
	}
}

// A route naming a gate the contract does not carry is skipped, not fatal:
// profiles and contracts version independently.
func TestAnUnknownGateInARouteIsSkipped(t *testing.T) {
	gates, _ := fixtures(t)
	routes := []contracts.Route{{ID: "mixed", Phrases: []string{"deploy"}, Gates: []string{"G2", "G99"}}}

	sequence, err := DeriveGateSequence("deploy something", routes, nil, gates)
	if err != nil {
		t.Fatalf("a route referencing an unknown gate should not be fatal: %v", err)
	}
	if len(sequence) != 2 {
		t.Errorf("got %d gates, want G1..G2 -- the unknown id should be ignored", len(sequence))
	}
}
