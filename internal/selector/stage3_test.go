package selector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchesChangeIntakeUsesADifferentBoundaryFromKeywordMatching(t *testing.T) {
	// This is the trap. _matches_change_intake's boundary class is
	// `[^a-z0-9]`, so a hyphen IS a boundary; KeywordMatches' is `[a-z0-9-]`,
	// so a hyphen is a word character. The same text therefore matches under
	// one and not the other, and "unifying" them would silently change which
	// tasks are treated as change intake.
	config := map[string]any{
		"change_intake": map[string]any{"keywords": []any{"add"}},
	}
	if !MatchesChangeIntake(config, "re-add the handler") {
		t.Error("change intake treats a hyphen as a boundary, so 're-add' contains 'add'")
	}
	if KeywordMatches("re-add the handler", "add") {
		t.Error("keyword matching treats a hyphen as a word character, so 're-add' is not 'add'")
	}
}

func TestBuildTeamsNeverSurfacesAnUnselectedAgent(t *testing.T) {
	// A team may only surface agents routing already selected; otherwise the
	// teams array would be a second, quieter dispatch decision.
	config := map[string]any{"team_recipes": []any{map[string]any{
		"id": "parallel-review", "type": "fixed",
		"route_ids": []any{"backend", "pipeline"}, "minimum_matches": float64(2),
		"members":            []any{"code-reviewer", "pipeline-security-reviewer", "absent-role"},
		"communication_mode": "peer", "fallback": "orchestrator-relayed", "description": "d",
	}}}
	routes := []Match{{ID: "backend"}, {ID: "pipeline"}}

	teams := BuildTeams(config, routes, []string{"code-reviewer", "pipeline-security-reviewer"}, "")
	if len(teams) != 1 {
		t.Fatalf("built %d teams, want 1", len(teams))
	}
	if strings.Join(teams[0].Members, ",") != "code-reviewer,pipeline-security-reviewer" {
		t.Errorf("members = %v, want only the selected agents", teams[0].Members)
	}
	if strings.Join(teams[0].TriggerReason.Routes, ",") != "backend,pipeline" {
		t.Errorf("trigger routes = %v, want the sorted triggering routes", teams[0].TriggerReason.Routes)
	}
}

func TestBuildTeamsRequiresTwoSelectedMembersByDefault(t *testing.T) {
	// A "team" of one is a single dispatch, not a team.
	config := map[string]any{"team_recipes": []any{map[string]any{
		"id": "t", "type": "fixed",
		"route_ids": []any{"backend"}, "minimum_matches": float64(1),
		"members":            []any{"code-reviewer", "other"},
		"communication_mode": "peer", "fallback": "f", "description": "d",
	}}}
	routes := []Match{{ID: "backend"}}

	if teams := BuildTeams(config, routes, []string{"code-reviewer"}, ""); len(teams) != 0 {
		t.Errorf("one selected member should not form a team, got %v", teams)
	}
	if teams := BuildTeams(config, routes, []string{"code-reviewer", "other"}, ""); len(teams) != 1 {
		t.Error("two selected members should form a team")
	}
}

func TestBuildTeamsDynamicRequiresRoleAndKeyword(t *testing.T) {
	config := map[string]any{"team_recipes": []any{map[string]any{
		"id": "debate", "type": "dynamic", "role": "debugging-engineer",
		"instances": float64(3), "keywords": []any{"competing hypotheses"},
		"communication_mode": "peer", "fallback": "f", "description": "d",
	}}}

	if teams := BuildTeams(config, nil, []string{"other"}, "competing hypotheses"); len(teams) != 0 {
		t.Error("the role must be selected for a dynamic team to form")
	}
	if teams := BuildTeams(config, nil, []string{"debugging-engineer"}, "unrelated task"); len(teams) != 0 {
		t.Error("a keyword must fire for a dynamic team to form")
	}
	teams := BuildTeams(config, nil, []string{"debugging-engineer"}, "weigh competing hypotheses")
	if len(teams) != 1 || teams[0].Instances != 3 || teams[0].Role != "debugging-engineer" {
		t.Fatalf("dynamic team = %+v, want role and instances carried", teams)
	}
	if strings.Join(teams[0].TriggerReason.Keywords, ",") != "competing hypotheses" {
		t.Errorf("trigger keywords = %v", teams[0].TriggerReason.Keywords)
	}
}

func TestKnowledgeContextPutsTheQueryLast(t *testing.T) {
	// The query is a trailing positional. Go's flag package stops parsing at
	// the first non-flag argument, so a query placed earlier would turn the
	// remaining --source scoping into positional junk -- retrieval reading
	// more than it was scoped to, which is the one direction it must never
	// fail in.
	focus := map[string]any{"agent-a": "prior defects"}
	got, err := BuildKnowledgeContext(focus, []string{"agent-a"}, KnowledgeInput{
		Task: "do  a   thing", TaskID: "T1", Classification: "internal",
		Sources: []string{"one", "two"}, Top: knowledgeTop(5), KnowledgeCLI: "/bin/cadre",
	})
	if err != nil {
		t.Fatal(err)
	}
	args := got.Requests[0].Invocation.Args
	if args[len(args)-1] != got.Requests[0].Query {
		t.Errorf("last arg = %q, want the query", args[len(args)-1])
	}
	// Whitespace in the task is collapsed before it reaches the query.
	if !strings.Contains(got.Requests[0].Query, "do a thing") {
		t.Errorf("query = %q, want collapsed whitespace", got.Requests[0].Query)
	}
	// Each source is named separately; --all-sources is never emitted.
	if strings.Contains(strings.Join(args, " "), "--all-sources") {
		t.Error("retrieval must never be planned with --all-sources")
	}
	if count := strings.Count(strings.Join(args, " "), "--source"); count != 2 {
		t.Errorf("--source appeared %d times, want one per source", count)
	}
}

func TestKnowledgeContextFailsClosedWithoutClassification(t *testing.T) {
	got, err := BuildKnowledgeContext(map[string]any{"a": "f"}, []string{"a"}, KnowledgeInput{Task: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "authorization-required" {
		t.Errorf("status = %q, want authorization-required", got.Status)
	}
	if len(got.Requests) != 0 {
		t.Error("no retrieval may be planned without an asserted classification")
	}

	none, err := BuildKnowledgeContext(nil, nil, KnowledgeInput{})
	if err != nil {
		t.Fatal(err)
	}
	if none.Status != "not-applicable" {
		t.Errorf("status = %q, want not-applicable when nothing was selected", none.Status)
	}
}

func TestKnowledgeContextRejectsAnOutOfRangeTop(t *testing.T) {
	for _, top := range []int{-1, 21} {
		_, err := BuildKnowledgeContext(map[string]any{"a": "f"}, []string{"a"}, KnowledgeInput{
			Task: "t", TaskID: "T", Classification: "internal", Top: knowledgeTop(top),
		})
		if err == nil {
			t.Errorf("top=%d must be refused", top)
		}
	}
}

func writePack(t *testing.T, root, relative, classification string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nclassification: " + classification + "\n---\n\nbody\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func packMatch(id, definition string) Match {
	return Match{ID: id, Rule: map[string]any{
		"id": id, "definition": definition, "version": float64(1),
	}}
}

func TestBuildContextPacksEnforcesClassificationContainment(t *testing.T) {
	root := t.TempDir()
	writePack(t, root, "context-packs/internal/CONTEXT.md", "internal")
	match := []Match{packMatch("internal-pack", "context-packs/internal/CONTEXT.md")}

	// Material may not exceed the classification asserted for the work.
	for _, asserted := range []string{"internal", "confidential", "restricted"} {
		packs, err := BuildContextPacks(match, asserted, root)
		if err != nil {
			t.Fatal(err)
		}
		if len(packs) != 1 {
			t.Errorf("an internal pack must be emitted for a %s task", asserted)
		}
	}
	packs, err := BuildContextPacks(match, "public", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 0 {
		t.Error("an internal pack must be withheld from a public task")
	}
}

func TestBuildContextPacksFailsClosedWithNoClassification(t *testing.T) {
	// An unasserted classification is not a licence to hand back
	// internal-classified reference material.
	root := t.TempDir()
	writePack(t, root, "p/CONTEXT.md", "internal")
	packs, err := BuildContextPacks([]Match{packMatch("p", "p/CONTEXT.md")}, "", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 0 {
		t.Errorf("packs = %v, want none emitted without an asserted classification", packs)
	}
}

func TestBuildContextPacksChecksIntegrityEvenWhenFiltered(t *testing.T) {
	// Definition existence and frontmatter validity are repository-integrity
	// guards, and they must fire on a public or unclassified run rather than
	// only when a pack happens to survive the classification filter.
	root := t.TempDir()
	missing := []Match{packMatch("gone", "does/not/exist.md")}
	if _, err := BuildContextPacks(missing, "public", root); err == nil {
		t.Error("a missing definition must be reported even when the filter would drop it")
	}
	if _, err := BuildContextPacks(missing, "", root); err == nil {
		t.Error("a missing definition must be reported with no classification asserted")
	}

	path := filepath.Join(root, "nofm.md")
	if err := os.WriteFile(path, []byte("no frontmatter here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildContextPacks([]Match{packMatch("nofm", "nofm.md")}, "public", root); err == nil {
		t.Error("a pack with no frontmatter block must be refused rather than defaulted")
	}
}

// knowledgeTop makes an explicit Top value addressable. A nil Top means the
// caller expressed no preference; these tests are about callers that did.
func knowledgeTop(value int) *int { return &value }
