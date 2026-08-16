package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A second roster exists, and is exercised.
//
// Every other guard on roster-agnosticism is indirect. The role-id scan looks
// for Cadre role names in platform source; the manifest loader checks that a
// declared layout is honoured. Both approximate the question this file asks
// directly: point the selector at a roster that shares nothing with Cadre's,
// and see whether a usable plan comes back naming only that roster's roles.
//
// The fixture was authored fresh rather than subset from Cadre's, and that is
// asserted below rather than promised. A copy would satisfy every assumption
// Cadre happens to satisfy, which is exactly the blindness having a second
// roster is meant to remove -- and the first foreign roster this repository
// ever had immediately hit an undeclared format assumption nobody knew was
// being made.
//
// Ported from roster/orchestration/test/test_roster_package.py. Reachable in
// Go only since the selector began resolving a package through its manifest.

func foreignRoster(t *testing.T) *RosterManifest {
	t.Helper()
	manifest, err := LoadRosterManifest(filepath.Join(selectorRepoRoot(t),
		"roster", "orchestration", "test", "fixtures", "minimal-roster"))
	if err != nil {
		t.Fatalf("the fixture roster is not a usable package: %v", err)
	}
	return manifest
}

func catalogIDsAt(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	ids, err := ParseCatalogIDs(string(content))
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return ids
}

func foreignRouting(t *testing.T, manifest *RosterManifest) map[string]any {
	t.Helper()
	content, err := os.ReadFile(manifest.Routing)
	if err != nil {
		t.Fatalf("reading the fixture routing: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("parsing the fixture routing: %v", err)
	}
	return config
}

func TestTheFixtureRosterSharesNoRoleWithCadres(t *testing.T) {
	// Guards every other test in this file. If the fixture were a subset of
	// Cadre's catalog, "the plan names only fixture roles" would be satisfied
	// by a selector that had leaked a Cadre role -- the assertion could not
	// tell the two apart.
	fixture := catalogIDsAt(t, foreignRoster(t).Catalog)
	if len(fixture) == 0 {
		t.Fatal("the fixture catalog is empty")
	}
	cadre := setOf(loadCatalogIDs(t))
	var shared []string
	for _, id := range fixture {
		if cadre[id] {
			shared = append(shared, id)
		}
	}
	if len(shared) > 0 {
		t.Errorf("the fixture roster shares %d role(s) with Cadre's: %v\n"+
			"A roster authored by subsetting Cadre's satisfies every assumption "+
			"Cadre happens to satisfy, which is the blindness a second roster "+
			"exists to remove.", len(shared), shared)
	}
}

func TestAPlanAgainstAForeignRosterNamesOnlyItsRoles(t *testing.T) {
	// The direct question. A Cadre role appearing here is a hardcoded default
	// that the role-id scan would have to have missed -- and for a consuming
	// project it is an "unknown agent" at dispatch time, or worse, a plan that
	// dispatches to a specialist their roster never declared.
	manifest := foreignRoster(t)
	fixtureRoles := catalogIDsAt(t, manifest.Catalog)

	plan, err := BuildDispatchPlan(foreignRouting(t, manifest), PlanInput{
		Task: "forge a sprocket flange", TaskID: "FOREIGN-1",
		ChangedFiles: []string{"sprockets/widget.md"}, Classification: "internal",
		RepositoryRoot: "<REPO_ROOT>", ChangedFileSource: "explicit",
	}, PlanOptions{Catalog: fixtureRoles, RosterRoot: manifest.Root})
	if err != nil {
		t.Fatalf("BuildDispatchPlan against the fixture roster: %v", err)
	}

	agents := planAgents(t, plan)
	selected := append(append(append([]string{},
		agents.Primary...), agents.Reviewers...), agents.Support...)
	if len(selected) == 0 {
		t.Fatal("no agent was selected against the fixture, so nothing below is checked")
	}
	permitted := setOf(fixtureRoles)
	for _, agent := range selected {
		if !permitted[agent] {
			t.Errorf("the plan names %q, which the fixture roster does not declare. "+
				"Selected: %v\nDeclared: %v", agent, selected, fixtureRoles)
		}
	}
	if plan["status"] != "ready" {
		t.Errorf("status = %v, want ready", plan["status"])
	}
}

func TestAForeignRosterWithNoMatchReturnsNeedsTriageRatherThanAGuess(t *testing.T) {
	// The failure mode a roster-agnostic selector must not have: reaching for
	// a familiar default when a foreign roster offers nothing. Returning
	// needs-triage is the honest answer; naming any agent here would mean the
	// selector has an opinion independent of the roster it was given.
	manifest := foreignRoster(t)
	plan, err := BuildDispatchPlan(foreignRouting(t, manifest), PlanInput{
		Task: "Recalibrate the quantum tachyon manifold", TaskID: "FOREIGN-2",
		ChangedFiles: []string{"unrelated/thing.txt"}, Classification: "internal",
		RepositoryRoot: "<REPO_ROOT>", ChangedFileSource: "explicit",
	}, PlanOptions{Catalog: catalogIDsAt(t, manifest.Catalog), RosterRoot: manifest.Root})
	if err != nil {
		t.Fatalf("BuildDispatchPlan: %v", err)
	}
	if plan["status"] != "needs-triage" {
		t.Errorf("status = %v, want needs-triage", plan["status"])
	}
	agents := planAgents(t, plan)
	selected := append(append(append([]string{},
		agents.Primary...), agents.Reviewers...), agents.Support...)
	if len(selected) != 0 {
		t.Errorf("needs-triage still named agents: %v", selected)
	}
}

func TestAGateBearingForeignRosterStillGetsAPlan(t *testing.T) {
	// Lifecycle gates come from the kernel, which knows nothing about any
	// roster. Supplying them must not introduce a role the foreign roster
	// never declared -- the gate contracts name authority requirements
	// (product_owner, security_lead) rather than role ids, and this is what
	// would notice that changing.
	manifest := foreignRoster(t)
	fixtureRoles := catalogIDsAt(t, manifest.Catalog)
	contract := loadLifecycleContract(t)

	plan, err := BuildDispatchPlan(foreignRouting(t, manifest), PlanInput{
		Task: "forge a sprocket flange", TaskID: "FOREIGN-3",
		ChangedFiles: []string{"sprockets/widget.md"}, Classification: "internal",
		RepositoryRoot: "<REPO_ROOT>", ChangedFileSource: "explicit",
	}, PlanOptions{
		Catalog: fixtureRoles, RosterRoot: manifest.Root,
		Gates: contract.Gates, ContractVer: contract.Version,
	})
	if err != nil {
		t.Fatalf("BuildDispatchPlan with gates: %v", err)
	}

	tracking := objectOf(plan["lifecycle_tracking"])
	if tracking["status"] != "integrated" {
		t.Errorf("lifecycle_tracking = %v, want integrated -- gates were supplied", tracking)
	}
	if plan["status"] != "ready" {
		t.Errorf("status = %v, want ready", plan["status"])
	}
	agents := planAgents(t, plan)
	selected := append(append(append([]string{},
		agents.Primary...), agents.Reviewers...), agents.Support...)
	permitted := setOf(fixtureRoles)
	for _, agent := range selected {
		if !permitted[agent] {
			t.Errorf("supplying lifecycle gates introduced %q, which the fixture "+
				"roster does not declare: %v", agent, selected)
		}
	}
	// Read as the typed []QualityGate it is. anyList returns empty for a typed
	// slice, which here would read as "no gates were carried" -- the exact
	// shape of the failure, so the assertion would have looked correct while
	// checking nothing. It cost a debugging round to notice, and it is the
	// fourth time this trap has appeared in this port.
	gates, ok := plan["required_quality_gates"].([]QualityGate)
	if !ok {
		t.Fatalf("required_quality_gates is %T, not []QualityGate",
			plan["required_quality_gates"])
	}
	if len(gates) == 0 {
		t.Error("no quality gate was carried, so the integrated path is not exercised")
	}
	// And the gates name lifecycle ids, never a role -- the kernel supplies
	// them and knows nothing about any roster.
	for _, gate := range gates {
		if permitted[gate.ID] {
			t.Errorf("quality gate %q is also a fixture role id; a gate list and a "+
				"role list have been confused", gate.ID)
		}
	}
}
