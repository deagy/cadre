package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// internal/engine/contracts -> repository root
	return filepath.Dir(filepath.Dir(filepath.Dir(working)))
}

// Loaded against the real contracts, not a fixture.
//
// A fixture would keep passing after the shipped contract changed shape,
// which is the drift this package exists in the middle of.
func TestTheRealContractsLoad(t *testing.T) {
	root := repoRoot(t)

	gates, err := LoadLifecycleGates(filepath.Join(root, "kernel", "contracts", "lifecycle-gates.json"))
	if err != nil {
		t.Fatalf("lifecycle gates: %v", err)
	}
	if len(gates) == 0 {
		t.Fatal("no lifecycle gates loaded")
	}
	if gates[0].ID != "G1" {
		t.Errorf("first gate is %q, want G1", gates[0].ID)
	}
	for _, gate := range gates {
		if gate.ID == "" || gate.Name == "" {
			t.Errorf("gate with an empty id or name: %+v", gate)
		}
	}

	mutation, err := LoadMutationGates(filepath.Join(root, "kernel", "contracts", "mutation-gates.json"))
	if err != nil {
		t.Fatalf("mutation gates: %v", err)
	}
	if len(mutation) == 0 {
		t.Fatal("no mutation gates loaded")
	}

	catalog, err := LoadAgentCatalog(filepath.Join(root, "providers", "agentic-sdlc-defaults", "agent-catalog.json"))
	if err != nil {
		t.Fatalf("agent catalog: %v", err)
	}
	if len(catalog) == 0 {
		t.Fatal("no agents loaded")
	}

	profile, err := LoadProfile(filepath.Join(root,
		"providers", "agentic-sdlc-defaults", "profiles", "generic", "profile.json"))
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if len(profile.GateBindings) == 0 {
		t.Fatal("profile has no gate_bindings")
	}
	if len(profile.Routing) == 0 {
		t.Fatal("profile has no routing")
	}
}

// Every gate the profile binds must resolve to something.
//
// A binding that resolves to nothing is indistinguishable from an unbound
// gate at the call site, so a typo in a slot name would silently produce an
// empty dispatch rather than an error.
func TestTheGenericProfileBindsEveryGateItClaimsTo(t *testing.T) {
	root := repoRoot(t)
	gates, err := LoadLifecycleGates(filepath.Join(root, "kernel", "contracts", "lifecycle-gates.json"))
	if err != nil {
		t.Fatalf("lifecycle gates: %v", err)
	}
	profile, err := LoadProfile(filepath.Join(root,
		"providers", "agentic-sdlc-defaults", "profiles", "generic", "profile.json"))
	if err != nil {
		t.Fatalf("profile: %v", err)
	}

	resolved := 0
	for _, gate := range gates {
		if _, bound := profile.GateBindings[gate.ID]; !bound {
			continue
		}
		dispatch := GateDispatchBinding(gate, profile.GateBindings)
		if len(dispatch.Agents) == 0 && len(dispatch.Tasks) == 0 && len(dispatch.Artifacts) == 0 {
			t.Errorf("gate %s is bound in the generic profile but resolves to nothing; "+
				"a required_contributions slot most likely does not match a contributions key",
				gate.ID)
			continue
		}
		resolved++
	}
	if resolved == 0 {
		t.Fatal("no gate resolved to a dispatch; this test checked nothing")
	}
}

func TestGateDispatchBindingOrdersAndDeduplicates(t *testing.T) {
	gate := Gate{ID: "G1", RequiredContributions: []string{"first", "second", "absent"}}
	bindings := map[string]GateBinding{
		"G1": {Contributions: map[string]Contribution{
			"first":  {Agents: []string{"a", "b"}, Tasks: []string{"t1"}},
			"second": {Agents: []string{"b", "c"}, Artifacts: []string{"doc"}},
			// "absent" is declared on the gate but not bound here.
			"unused": {Agents: []string{"never"}},
		}},
	}

	dispatch := GateDispatchBinding(gate, bindings)

	if got := strings.Join(dispatch.Agents, ","); got != "a,b,c" {
		t.Errorf("agents = %q, want a,b,c (slot order, first occurrence wins)", got)
	}
	if got := strings.Join(dispatch.Tasks, ","); got != "t1" {
		t.Errorf("tasks = %q, want t1", got)
	}
	if got := strings.Join(dispatch.Artifacts, ","); got != "doc" {
		t.Errorf("artifacts = %q, want doc", got)
	}
	for _, agent := range dispatch.Agents {
		if agent == "never" {
			t.Error("a contribution the gate does not require leaked into the dispatch")
		}
	}
}

func TestGateDispatchBindingOnAnUnboundGate(t *testing.T) {
	dispatch := GateDispatchBinding(Gate{ID: "G9"}, map[string]GateBinding{})
	if len(dispatch.Agents)+len(dispatch.Tasks)+len(dispatch.Artifacts) != 0 {
		t.Errorf("an unbound gate resolved to %+v, want nothing", dispatch)
	}
}

func TestChooseRouteMatchesCaseInsensitively(t *testing.T) {
	routes := []Route{
		{ID: "arch", Phrases: []string{"Architecture", "service"}},
		{ID: "support", Phrases: []string{"incident"}},
	}

	matched := ChooseRoute("Refactor the ARCHITECTURE of the billing service", routes)
	if len(matched) != 1 || matched[0].ID != "arch" {
		t.Fatalf("matched %+v, want exactly the arch route", matched)
	}
	if matched := ChooseRoute("unrelated text", routes); matched != nil {
		t.Errorf("matched %+v on text with no phrase, want nothing", matched)
	}
}

// A route matching two of its own phrases is still one match.
func TestChooseRouteReturnsARouteOnce(t *testing.T) {
	routes := []Route{{ID: "arch", Phrases: []string{"architecture", "service"}}}
	matched := ChooseRoute("architecture of the service", routes)
	if len(matched) != 1 {
		t.Errorf("matched %d times, want 1", len(matched))
	}
}

func TestMutationGateGuard(t *testing.T) {
	gates := []MutationGate{
		{ID: "production-deployment", Phrases: []string{"deploy to production", "production deployment"}},
		{ID: "key-rotation", Phrases: []string{"rotate the signing key"}},
	}

	if matched := MutationGateGuard("add a unit test", gates); matched != nil {
		t.Errorf("matched %+v on innocuous text, want nothing", matched)
	}

	matched := MutationGateGuard("please deploy to production tonight", gates)
	if len(matched) != 1 {
		t.Fatalf("matched %+v, want one gate", matched)
	}
	if matched[0].ID != "production-deployment" || matched[0].Phrase != "deploy to production" {
		t.Errorf("matched %+v, want production-deployment on its first phrase", matched[0])
	}
	if !strings.Contains(matched[0].Reason, "deploy to production") {
		t.Errorf("reason %q does not name the phrase that matched", matched[0].Reason)
	}
}

// One match per gate, even when several of its phrases hit.
func TestMutationGateGuardRecordsAGateOnce(t *testing.T) {
	gates := []MutationGate{
		{ID: "production-deployment", Phrases: []string{"deploy to production", "production deployment"}},
	}
	matched := MutationGateGuard("deploy to production: a production deployment", gates)
	if len(matched) != 1 {
		t.Fatalf("matched %d entries, want 1 per gate", len(matched))
	}
	if matched[0].Phrase != "deploy to production" {
		t.Errorf("recorded %q, want the first phrase that matched", matched[0].Phrase)
	}
}

// The asymmetry the Python carried: it lowered the task text but compared the
// phrase verbatim, so a phrase with a capital letter could never match. That
// is a human-only gate that silently never fires.
//
// Latent rather than live -- every phrase in mutation-gates.json is lowercase
// -- but porting it faithfully would hand the trap to whoever writes the first
// phrase with a capital in it.
func TestMutationGateGuardMatchesAnUppercasePhrase(t *testing.T) {
	gates := []MutationGate{{ID: "prod", Phrases: []string{"Deploy To Production"}}}

	matched := MutationGateGuard("please deploy to production", gates)
	if len(matched) != 1 {
		t.Fatalf("an uppercase phrase did not match lowered text: %+v", matched)
	}
	if matched[0].Phrase != "Deploy To Production" {
		t.Errorf("reported phrase %q, want the phrase as written in the contract", matched[0].Phrase)
	}
}

// The contract data must stay in the case the matcher expects, so that a
// future reader is not relying on the fix above without knowing it.
func TestShippedMutationPhrasesAreLowercase(t *testing.T) {
	root := repoRoot(t)
	gates, err := LoadMutationGates(filepath.Join(root, "kernel", "contracts", "mutation-gates.json"))
	if err != nil {
		t.Fatalf("mutation gates: %v", err)
	}
	checked := 0
	for _, gate := range gates {
		for _, phrase := range gate.Phrases {
			checked++
			if phrase != strings.ToLower(phrase) {
				t.Errorf("mutation gate %s has a non-lowercase phrase %q. Matching handles it, "+
					"but the convention is lowercase; confirm this is deliberate", gate.ID, phrase)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no phrases checked")
	}
}

// Every key the shipped contract uses must have somewhere to land.
//
// Go's json decoder discards unknown keys in silence, so a field the
// contract carries and the struct omits is not an error at any point -- it is
// simply absent, and whatever depended on it behaves as though the contract
// never said it.
//
// That is not hypothetical here. Gate.HumanOnly was missing from the first
// version of this package. G9 sets human_only, and the validator uses it to
// exempt a human-decision gate from the "approved gates need evidence_refs and
// artifact_bindings" rule -- so a perfectly legitimate G9 approval would have
// been reported as a hard error. It surfaced only when the module that
// consumes the field was ported.
func TestGateModelsEveryKeyTheContractUses(t *testing.T) {
	root := repoRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "kernel", "contracts", "lifecycle-gates.json"))
	if err != nil {
		t.Fatalf("reading the lifecycle contract: %v", err)
	}
	var document struct {
		Gates []map[string]any `json:"gates"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("parsing the lifecycle contract: %v", err)
	}
	if len(document.Gates) == 0 {
		t.Fatal("the contract declares no gates; this guard read nothing")
	}

	modelled := map[string]bool{}
	structType := reflect.TypeOf(Gate{})
	for i := 0; i < structType.NumField(); i++ {
		tag := structType.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		modelled[strings.Split(tag, ",")[0]] = true
	}

	used := map[string]bool{}
	for _, gate := range document.Gates {
		for key := range gate {
			used[key] = true
		}
	}

	var missing []string
	for key := range used {
		if !modelled[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("lifecycle-gates.json uses keys Gate does not model: %v.\n"+
			"They decode to nothing, so anything depending on them sees the zero value.", missing)
	}
}
