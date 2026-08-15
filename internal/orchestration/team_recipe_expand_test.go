package orchestration

import (
	"encoding/json"
	"strings"
	"testing"
)

// Expansion exists so a session with a real plan can dispatch one of its
// teams without hand-building the member list. The value is entirely in what
// it refuses: without these checks it would be a way to assemble a team the
// plan never proposed.

func recipeConfig(t *testing.T) map[string]any {
	t.Helper()
	const raw = `{
	  "version": 1,
	  "routes": [{"id": "backend"}, {"id": "pipeline"}],
	  "risk_rules": [],
	  "team_recipes": [
	    {"id": "parallel-review", "type": "fixed",
	     "route_ids": ["backend", "pipeline"], "minimum_matches": 2,
	     "members": ["code-reviewer", "security-reviewer"],
	     "communication_mode": "peer", "fallback": "orchestrator-relayed",
	     "description": "two reviewers in parallel"},
	    {"id": "competing-hypotheses", "type": "dynamic",
	     "role": "debugging-engineer", "instances": {"min": 2, "max": 4},
	     "keywords": ["competing hypotheses"],
	     "communication_mode": "peer", "fallback": "orchestrator-relayed",
	     "description": "distinct hypotheses in parallel"}
	  ]
	}`
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestAnUnknownRecipeNamesTheOnesThatExist(t *testing.T) {
	_, err := ExpandRecipeToMembers(recipeConfig(t), ExpandRecipeInput{RecipeID: "no-such-recipe"})
	if err == nil {
		t.Fatal("an unknown recipe id must be refused")
	}
	// The known ids are listed, because the likely cause is a typo and the
	// caller cannot see routing.json from where it is standing.
	if !strings.Contains(err.Error(), "parallel-review") {
		t.Errorf("the refusal must list the known ids: %v", err)
	}
}

func TestARecipeThatWouldNotHaveFiredIsRefused(t *testing.T) {
	// The refusal that matters. Expanding here would produce a member list
	// the selector never proposed for these signals -- which is precisely the
	// hand-built team this tool exists to avoid.
	config := recipeConfig(t)

	// parallel-review needs two matched routes; only one is supplied.
	_, err := ExpandRecipeToMembers(config, ExpandRecipeInput{
		RecipeID:        "parallel-review",
		MatchedRouteIDs: []string{"backend"},
		SelectedAgents:  []string{"code-reviewer", "security-reviewer"},
		SharedBrief:     "review it",
	})
	if err == nil {
		t.Fatal("a recipe below its minimum_matches must be refused")
	}
	if !strings.Contains(err.Error(), "did not fire") {
		t.Errorf("the refusal must say it would not have triggered: %v", err)
	}

	// ...and with both routes it expands.
	members, err := ExpandRecipeToMembers(config, ExpandRecipeInput{
		RecipeID:        "parallel-review",
		MatchedRouteIDs: []string{"backend", "pipeline"},
		SelectedAgents:  []string{"code-reviewer", "security-reviewer"},
		SharedBrief:     "review it",
	})
	if err != nil {
		t.Fatalf("a recipe that fires must expand: %v", err)
	}
	if len(members) != 2 {
		t.Errorf("members = %+v, want both reviewers", members)
	}
}

func TestOnlySelectedAgentsBecomeMembers(t *testing.T) {
	// A recipe names candidate members; the plan decides which were actually
	// selected. Expanding to a role the plan did not select would dispatch an
	// agent nothing chose.
	config := recipeConfig(t)
	members, err := ExpandRecipeToMembers(config, ExpandRecipeInput{
		RecipeID:        "parallel-review",
		MatchedRouteIDs: []string{"backend", "pipeline"},
		SelectedAgents:  []string{"code-reviewer", "security-reviewer", "unrelated-role"},
		SharedBrief:     "review it",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		if member.RoleID == "unrelated-role" {
			t.Error("a role outside the recipe must not become a member")
		}
	}
}

func TestEveryMemberNeedsABrief(t *testing.T) {
	config := recipeConfig(t)
	_, err := ExpandRecipeToMembers(config, ExpandRecipeInput{
		RecipeID:        "parallel-review",
		MatchedRouteIDs: []string{"backend", "pipeline"},
		SelectedAgents:  []string{"code-reviewer", "security-reviewer"},
		MemberBriefs:    map[string]string{"code-reviewer": "look at the code"},
		// security-reviewer has neither a per-member brief nor a shared one.
	})
	if err == nil {
		t.Fatal("a member with no brief must be refused, not dispatched with none")
	}
	if !strings.Contains(err.Error(), "security-reviewer") {
		t.Errorf("the refusal must name the member: %v", err)
	}

	// A per-member brief overrides the shared one.
	members, err := ExpandRecipeToMembers(config, ExpandRecipeInput{
		RecipeID:        "parallel-review",
		MatchedRouteIDs: []string{"backend", "pipeline"},
		SelectedAgents:  []string{"code-reviewer", "security-reviewer"},
		SharedBrief:     "shared",
		MemberBriefs:    map[string]string{"code-reviewer": "specific"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		if member.RoleID == "code-reviewer" && member.Brief != "specific" {
			t.Errorf("a per-member brief must win: %+v", member)
		}
		if member.RoleID == "security-reviewer" && member.Brief != "shared" {
			t.Errorf("the shared brief must fill in: %+v", member)
		}
	}
}

func TestADynamicRecipeRequiresOneBriefPerInstance(t *testing.T) {
	// A dynamic recipe like competing-hypotheses exists to run *distinct*
	// hypotheses in parallel. Repeating one brief across instances spends the
	// same dispatch budget to get the same answer several times, so a shared
	// brief is refused rather than silently reused.
	config := recipeConfig(t)
	base := ExpandRecipeInput{
		RecipeID:       "competing-hypotheses",
		SelectedAgents: []string{"debugging-engineer"},
		TaskText:       "weigh competing hypotheses about the failure",
		SharedBrief:    "one brief for all",
	}

	if _, err := ExpandRecipeToMembers(config, base); err == nil {
		t.Error("a shared brief must not stand in for per-instance briefs")
	}

	withBriefs := base
	withBriefs.InstanceBriefs = []string{"hypothesis A", "hypothesis B"}
	members, err := ExpandRecipeToMembers(config, withBriefs)
	if err != nil {
		t.Fatalf("one brief per instance must expand: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %+v, want the declared minimum of 2", members)
	}
	if members[0].Brief == members[1].Brief {
		t.Error("instances must carry their own distinct briefs")
	}
	for _, member := range members {
		if member.RoleID != "debugging-engineer" {
			t.Errorf("member role = %q, want the recipe's declared role", member.RoleID)
		}
	}
}

func TestAnInstanceCountOutsideTheDeclaredRangeIsRefused(t *testing.T) {
	config := recipeConfig(t)
	for _, count := range []int{1, 5} {
		requested := count
		briefs := make([]string, count)
		for index := range briefs {
			briefs[index] = "hypothesis"
		}
		_, err := ExpandRecipeToMembers(config, ExpandRecipeInput{
			RecipeID:       "competing-hypotheses",
			SelectedAgents: []string{"debugging-engineer"},
			TaskText:       "weigh competing hypotheses",
			InstanceCount:  &requested,
			InstanceBriefs: briefs,
		})
		if err == nil {
			t.Errorf("instance_count %d is outside [2, 4] and must be refused", count)
		}
	}
}

func TestADynamicRecipeNeedsItsKeywordToFire(t *testing.T) {
	// Same rule as the selector's: a dynamic recipe is triggered by the task
	// text, so expanding one whose keyword never appeared would dispatch a
	// team the plan would not have contained.
	config := recipeConfig(t)
	_, err := ExpandRecipeToMembers(config, ExpandRecipeInput{
		RecipeID:       "competing-hypotheses",
		SelectedAgents: []string{"debugging-engineer"},
		TaskText:       "an unrelated task",
		InstanceBriefs: []string{"a", "b"},
	})
	if err == nil {
		t.Fatal("a dynamic recipe whose keyword did not fire must be refused")
	}
	if !strings.Contains(err.Error(), "task_text") {
		t.Errorf("the refusal must name task_text as a consulted signal: %v", err)
	}
}
