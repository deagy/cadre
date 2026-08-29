package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Aide gate numbers against the lifecycle-gates contract.
//
// aides.yaml hardcodes each aide's gate numbers; the kernel owns gate
// numbering permanently. Nothing else constrains them -- parseGates checks
// they are integers and not duplicated, not that they exist -- so a typo, or a
// kernel-side renumber, ships an authority aide telling a human to prepare a
// decision package for a gate that is not there.
//
// The Python generator cross-checked this and the Go port did not. Every gate
// in the shipped aides.yaml is valid today, so nothing was broken; the guard
// was simply absent, and the file it guards is edited by hand.

func writeContract(t *testing.T, gateIDs ...string) string {
	t.Helper()
	var entries []string
	for _, id := range gateIDs {
		entries = append(entries, `{"id": "`+id+`", "name": "n", "phase": "p"}`)
	}
	body := `{"version": 1, "gates": [` + strings.Join(entries, ", ") + `]}`
	path := filepath.Join(t.TempDir(), "lifecycle-gates.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnAideCitingAGateTheContractLacksIsRefused(t *testing.T) {
	contract := writeContract(t, "G1", "G2", "G3")
	aides := []AideData{
		{ID: "product-owner-aide", Gates: []int{1, 2}},
		{ID: "future-aide", Gates: []int{3, 11}},
	}
	err := ValidateAideGatesAgainstContract(aides, contract)
	if err == nil {
		t.Fatal("an aide citing G11 against a G1-G3 contract was accepted")
	}
	// The message has to carry all three things a reader needs: which aide,
	// which gate, and what the contract actually declares.
	for _, want := range []string{"future-aide", "G11", "G1, G2, G3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// And the aide that is fine is not implicated.
	if strings.Contains(err.Error(), "product-owner-aide") {
		t.Errorf("the refusal names an aide that is valid: %v", err)
	}
}

func TestEveryDeclaredGateIsAccepted(t *testing.T) {
	// The other side. A validator refusing everything would satisfy the case
	// above, and the boundary values are where an off-by-one lands: G1 is the
	// first gate and G10 is the only two-digit one, so a check built on string
	// prefixes rather than equality gets G1 and G10 confused.
	contract := writeContract(t, "G1", "G2", "G3", "G4", "G5", "G6", "G7", "G8", "G9", "G10")
	aides := []AideData{
		{ID: "first", Gates: []int{1}},
		{ID: "last", Gates: []int{10}},
		{ID: "several", Gates: []int{1, 2, 6}},
		{ID: "none", Gates: nil},
	}
	if err := ValidateAideGatesAgainstContract(aides, contract); err != nil {
		t.Errorf("a fully valid aide set was refused: %v", err)
	}

	// G10 declared but G1 absent: a prefix-based check would accept a citation
	// of G1 here.
	partial := writeContract(t, "G10")
	if err := ValidateAideGatesAgainstContract(
		[]AideData{{ID: "first", Gates: []int{1}}}, partial); err == nil {
		t.Error("G1 was accepted against a contract declaring only G10; the check " +
			"is matching by prefix rather than by id")
	}
}

func TestAnAbsentContractIsNotAnError(t *testing.T) {
	// Standalone operation is supported everywhere else in this suite, and a
	// generator that refused to run without a kernel would be a new dependency
	// rather than a check. The Python original degraded the same way.
	if err := ValidateAideGatesAgainstContract(
		[]AideData{{ID: "any", Gates: []int{99}}},
		filepath.Join(t.TempDir(), "absent.json")); err != nil {
		t.Errorf("a missing contract was treated as a failure: %v", err)
	}
}

func TestAContractThatCannotBeTrustedIsAnError(t *testing.T) {
	// Distinct from absent. A contract that is present but unreadable must not
	// be treated as "no kernel here" -- that would turn a corrupted file into
	// a silently skipped check, which is the failure mode the degradation
	// above could otherwise be stretched into.
	directory := t.TempDir()
	for _, probe := range []struct{ name, body, wants string }{
		{"not JSON", "{not json", "invalid JSON"},
		{"no gates", `{"version": 1}`, "declares no gates"},
		{"an empty gate list", `{"version": 1, "gates": []}`, "declares no gates"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			path := filepath.Join(directory, "contract.json")
			if err := os.WriteFile(path, []byte(probe.body), 0o644); err != nil {
				t.Fatal(err)
			}
			err := ValidateAideGatesAgainstContract([]AideData{{ID: "a", Gates: []int{1}}}, path)
			if err == nil {
				t.Fatalf("an unusable contract was treated as no contract: %s", probe.body)
			}
			if !strings.Contains(err.Error(), probe.wants) {
				t.Errorf("refused for a different reason than this case is about.\n"+
					"wanted something naming %q, got: %v", probe.wants, err)
			}
		})
	}
}

func TestTheShippedAidesAgreeWithTheShippedContract(t *testing.T) {
	// Against the real files, because that is where the property has to hold.
	// This is what would notice a kernel renumber landing before aides.yaml
	// caught up -- the case the guard exists for, and one no fixture reaches.
	root := repositoryRoot(t)
	aides, err := LoadAides(filepath.Join(root, "roster", "authority", "aides.yaml"))
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	if len(aides) == 0 {
		t.Fatal("no aides were loaded; this test would prove nothing")
	}
	cited := 0
	for _, aide := range aides {
		cited += len(aide.Gates)
	}
	if cited == 0 {
		t.Fatal("no aide cites a gate; this test would prove nothing")
	}

	contract := filepath.Join(root, "kernel-contracts", "lifecycle-gates.json")
	if _, err := os.Stat(contract); err != nil {
		t.Skipf("no in-tree lifecycle contract: %v", err)
	}
	if err := ValidateAideGatesAgainstContract(aides, contract); err != nil {
		t.Errorf("the shipped aides cite a gate the shipped contract does not "+
			"declare: %v", err)
	}
}
