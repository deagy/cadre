package selector

import (
	"strings"
	"testing"
)

func TestParseCatalogIDsKeepsFileOrder(t *testing.T) {
	// File order is not cosmetic: Ordered sorts every agents list in every
	// plan by catalog position, so a different traversal order silently
	// reorders published output.
	content := strings.Join([]string{
		"agents:",
		"  zebra-role:",
		"    definition: z/AGENT.md",
		"    phase: build",
		"  alpha-role:",
		"    definition: a/AGENT.md",
		"  middle-role:",
		"    definition: m/AGENT.md",
	}, "\n")

	ids, err := ParseCatalogIDs(content)
	if err != nil {
		t.Fatalf("ParseCatalogIDs: %v", err)
	}
	want := []string{"zebra-role", "alpha-role", "middle-role"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v (file order, not sorted)", ids, want)
		}
	}
}

func TestParseCatalogIDsRejectsADuplicate(t *testing.T) {
	// Last-one-wins would make catalog position ambiguous, and position is
	// exactly what agent ordering depends on.
	content := "agents:\n  role-a:\n    definition: a\n  role-a:\n    definition: b\n"
	if _, err := ParseCatalogIDs(content); err == nil {
		t.Fatal("expected a duplicate id to be refused")
	}
}

func TestParseCatalogIDsRefusesAnEmptyCatalog(t *testing.T) {
	if _, err := ParseCatalogIDs("agents:\n"); err == nil {
		t.Fatal("expected an empty catalog to be refused")
	}
}

func TestOrderedSortsByCatalogPositionAndDedupes(t *testing.T) {
	catalog := []string{"first", "second", "third"}
	got := Ordered([]string{"third", "first", "third", "second"}, catalog)
	want := []string{"first", "second", "third"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Ordered = %v, want %v", got, want)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("Ordered = %v, want de-duplicated %v", got, want)
	}
}

func TestOrderedKeepsUnknownAgentsInFirstSeenOrder(t *testing.T) {
	// Every agent absent from the catalog shares the sort key len(catalog).
	// Python's sorted() is stable, so they keep their input order; an
	// unstable sort in Go would be free to permute them, and the difference
	// only shows up for agents the catalog does not know -- exactly the case
	// nobody constructs by hand.
	catalog := []string{"known"}
	got := Ordered([]string{"zeta", "alpha", "known", "mid"}, catalog)
	want := []string{"known", "zeta", "alpha", "mid"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Ordered = %v, want %v (unknown agents keep first-seen order)", got, want)
		}
	}
}

func TestBuildDispatchDispositionBranches(t *testing.T) {
	staffedByPrimary := BuildDispatchDisposition(AgentGroups{Primary: []string{"a"}})
	if staffedByPrimary.Status != "staffed" {
		t.Errorf("primary alone should be staffed, got %q", staffedByPrimary.Status)
	}

	staffedByReviewer := BuildDispatchDisposition(AgentGroups{Reviewers: []string{"r"}})
	if staffedByReviewer.Status != "staffed" {
		t.Errorf("a reviewer alone should be staffed, got %q", staffedByReviewer.Status)
	}

	advisory := BuildDispatchDisposition(AgentGroups{Support: []string{"s1", "s2"}})
	if advisory.Status != "advisory-only" {
		t.Fatalf("support alone should be advisory-only, got %q", advisory.Status)
	}
	// The reason names the roles: an orchestrator surfaces this text to a
	// human before deciding whether to act without a dispatch.
	if !strings.Contains(advisory.Reason, "(s1, s2)") {
		t.Errorf("advisory reason must name the support roles, got: %s", advisory.Reason)
	}

	empty := BuildDispatchDisposition(AgentGroups{})
	if empty.Status != "no-agents-selected" {
		t.Errorf("no agents should be no-agents-selected, got %q", empty.Status)
	}
}

func TestApplyCrossStackHonoursItsMinimum(t *testing.T) {
	config := map[string]any{
		"cross_stack": map[string]any{
			"route_ids":       []any{"backend", "frontend", "pipeline"},
			"minimum_matches": float64(2),
			"support":         []any{"integration-reviewer"},
		},
	}

	below := ApplyCrossStack(config, []Match{{ID: "backend"}})
	if len(below) != 0 {
		t.Errorf("one matching route is below the minimum, got %v", below)
	}

	at := ApplyCrossStack(config, []Match{{ID: "backend"}, {ID: "pipeline"}})
	if len(at) != 1 || at[0] != "integration-reviewer" {
		t.Errorf("two matching routes should add the support role, got %v", at)
	}

	// Routes outside route_ids do not count toward the minimum.
	unrelated := ApplyCrossStack(config, []Match{{ID: "backend"}, {ID: "documentation"}})
	if len(unrelated) != 0 {
		t.Errorf("an unrelated route must not count toward the minimum, got %v", unrelated)
	}

	if got := ApplyCrossStack(map[string]any{}, []Match{{ID: "backend"}}); got != nil {
		t.Errorf("no cross_stack block should yield nothing, got %v", got)
	}
}
