package selector

import (
	"strings"
	"testing"
)

// When a team recipe fires, and when it must not.
//
// A recipe turns a selection into a team: several agents working the same task
// with a communication mode between them. Firing when it should not is not a
// cosmetic error -- it dispatches agents nobody selected, and the plan reads as
// though the routing asked for them.
//
// stage3_test.go covers three conditions: an unselected agent never becomes a
// member, two members are required by default, and a dynamic recipe needs both
// its role and a keyword. This covers the rest of what
// roster/orchestration/test/test_team_recipe_dryrun.py's firing tests assert,
// which is most of them.

func fixedRecipe(overrides map[string]any) map[string]any {
	recipe := map[string]any{
		"id": "parallel-review", "type": "fixed",
		"route_ids":          []any{"backend", "frontend", "pipeline"},
		"minimum_matches":    float64(2),
		"members":            []any{"code-reviewer", "security-reviewer"},
		"communication_mode": "peer", "fallback": "orchestrator-relayed",
		"description": "a review team",
	}
	for key, value := range overrides {
		recipe[key] = value
	}
	return recipe
}

func dynamicRecipe(overrides map[string]any) map[string]any {
	recipe := map[string]any{
		"id": "shard-review", "type": "dynamic",
		"role": "code-reviewer", "instances": float64(3),
		"requires_route":     "backend",
		"keywords":           []any{"shard", "partition"},
		"communication_mode": "peer", "fallback": "orchestrator-relayed",
		"description": "a sharded review",
	}
	for key, value := range overrides {
		recipe[key] = value
	}
	return recipe
}

func recipeConfig(recipe map[string]any) map[string]any {
	return map[string]any{"team_recipes": []any{recipe}}
}

func matches(ids ...string) []Match {
	out := make([]Match, 0, len(ids))
	for _, id := range ids {
		out = append(out, Match{ID: id})
	}
	return out
}

func TestAFixedRecipeNeedsEnoughRoutesNotJustEnoughMembers(t *testing.T) {
	// minimum_matches is about the *routes* that triggered the selection, and
	// it is the condition stage3_test.go does not reach: its cases vary the
	// members while always supplying enough routes. A recipe firing on one
	// route assembles a cross-cutting team for a change that touched one area.
	config := recipeConfig(fixedRecipe(nil))
	members := []string{"code-reviewer", "security-reviewer"}

	if teams := BuildTeams(config, matches("backend"), members, ""); len(teams) != 0 {
		t.Errorf("one matched route satisfied a minimum_matches of 2: %+v", teams)
	}
	teams := BuildTeams(config, matches("backend", "frontend"), members, "")
	if len(teams) != 1 {
		t.Fatalf("two matched routes did not fire the recipe: %+v", teams)
	}
	// And the team says which routes triggered it, so a reader can check the
	// decision rather than take it.
	triggering := strings.Join(teams[0].TriggerReason.Routes, ",")
	if triggering != "backend,frontend" {
		t.Errorf("trigger routes = %q, want the two that matched", triggering)
	}
}

func TestAFixedRecipeCountsOnlyItsOwnRoutes(t *testing.T) {
	// Routes the recipe does not name must not count toward its minimum. A
	// recipe listing three areas fires because two of *those* were touched,
	// not because two routes matched somewhere in the plan.
	config := recipeConfig(fixedRecipe(nil))
	members := []string{"code-reviewer", "security-reviewer"}

	if teams := BuildTeams(config, matches("backend", "documentation", "testing"),
		members, ""); len(teams) != 0 {
		t.Errorf("routes outside the recipe counted toward minimum_matches: %+v",
			teams[0].TriggerReason.Routes)
	}
}

func TestADynamicRecipeNeedsItsRequiredRoute(t *testing.T) {
	// requires_route scopes a dynamic recipe to a context. Without it, a task
	// merely mentioning the keyword spawns instances in an unrelated change.
	config := recipeConfig(dynamicRecipe(nil))
	selected := []string{"code-reviewer"}

	if teams := BuildTeams(config, matches("frontend"), selected,
		"shard the partition"); len(teams) != 0 {
		t.Errorf("the recipe fired without its required route: %+v", teams)
	}
	if teams := BuildTeams(config, matches("backend"), selected,
		"shard the partition"); len(teams) != 1 {
		t.Errorf("the recipe did not fire with its required route: %+v", teams)
	}
}

func TestADynamicRecipeWithNoKeywordsCanNeverFire(t *testing.T) {
	// An empty keyword list is a recipe with no trigger. Reading it as
	// "nothing to check, therefore satisfied" would make it fire on every task
	// whose role and route line up -- the opposite of what an empty list of
	// conditions should mean here, and a recipe nobody could switch off.
	config := recipeConfig(dynamicRecipe(map[string]any{"keywords": []any{}}))
	teams := BuildTeams(config, matches("backend"), []string{"code-reviewer"},
		"anything at all, shard partition included")
	if len(teams) != 0 {
		t.Errorf("a recipe with no keywords fired: %+v", teams)
	}

	// The same for a missing keywords key, which is how a hand-written recipe
	// most often expresses it.
	withoutKey := dynamicRecipe(nil)
	delete(withoutKey, "keywords")
	if teams := BuildTeams(recipeConfig(withoutKey), matches("backend"),
		[]string{"code-reviewer"}, "shard the partition"); len(teams) != 0 {
		t.Errorf("a recipe with no keywords key fired: %+v", teams)
	}
}

func TestADynamicRecipeReportsTheKeywordsThatFiredIt(t *testing.T) {
	// The trigger reason is the audit trail. A team that cannot say why it
	// exists is one a reviewer has to re-derive from routing.json.
	config := recipeConfig(dynamicRecipe(nil))
	teams := BuildTeams(config, matches("backend"), []string{"code-reviewer"},
		"shard the index and partition the load")
	if len(teams) != 1 {
		t.Fatalf("the recipe did not fire: %+v", teams)
	}
	fired := strings.Join(teams[0].TriggerReason.Keywords, ",")
	if fired != "shard,partition" {
		t.Errorf("trigger keywords = %q, want both that appear in the task", fired)
	}
	if teams[0].Instances != 3 {
		t.Errorf("instances = %d, want the declared 3", teams[0].Instances)
	}
	if teams[0].Role != "code-reviewer" {
		t.Errorf("role = %q", teams[0].Role)
	}
}

func TestARecipeOfAnUnknownTypeDoesNotFire(t *testing.T) {
	// A recipe type nothing implements must produce no team rather than
	// falling through to whichever branch happens to be last. A typo in
	// `type` would otherwise assemble a team on rules nobody wrote.
	for _, declared := range []any{"parallel", "", nil, float64(1)} {
		recipe := fixedRecipe(map[string]any{"type": declared})
		teams := BuildTeams(recipeConfig(recipe), matches("backend", "frontend"),
			[]string{"code-reviewer", "security-reviewer"}, "")
		if len(teams) != 0 {
			t.Errorf("a recipe declaring type %v fired: %+v", declared, teams)
		}
	}
}
