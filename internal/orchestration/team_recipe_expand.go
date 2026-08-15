package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deagy/cadre/cli/internal/platform"
	"github.com/deagy/cadre/cli/internal/selector"
)

// Expanding a routing.json team_recipes[] entry into concrete members, for
// the dispatch_team_recipe MCP tool.
//
// A port of team_recipe_dryrun.py's expand_recipe_to_members. It exists so a
// session that already has a real `cadre select` plan can dispatch one of the
// plan's teams[] entries without hand-building the member list -- and, more
// importantly, without being able to hand-build one the plan never proposed.
//
// Firing is decided by selector.BuildTeams, the same code that produced the
// teams[] array in the plan. Reimplementing the match here would let a recipe
// expand for a signal set the selector would not have fired on, which is the
// exact hole this tool must not open.

// TeamMember is one expanded member: a role and the brief it runs with.
type TeamMember struct {
	RoleID string `json:"role_id"`
	Brief  string `json:"brief"`
}

// ExpandRecipeInput is what the caller supplies from its plan.
type ExpandRecipeInput struct {
	RecipeID        string
	MatchedRouteIDs []string
	SelectedAgents  []string
	TaskText        string
	SharedBrief     string
	MemberBriefs    map[string]string
	InstanceBriefs  []string
	InstanceCount   *int
}

// ExpandRecipeToMembers turns a recipe id plus a plan's signals into members.
//
// Refuses rather than guessing in every ambiguous case: an unknown recipe, a
// recipe that would not have fired for these signals, a member with no brief,
// or an instance count outside the recipe's declared range.
func ExpandRecipeToMembers(config map[string]any, input ExpandRecipeInput) ([]TeamMember, error) {
	recipe, err := findRecipe(config, input.RecipeID)
	if err != nil {
		return nil, err
	}

	// The signals are reduced to the shape BuildTeams consumes. Only ids are
	// needed: the caller passes bare id strings from its plan, and the reasons
	// blocks the plan carries alongside them play no part in firing.
	routes := make([]selector.Match, 0, len(input.MatchedRouteIDs))
	for _, id := range input.MatchedRouteIDs {
		routes = append(routes, selector.Match{ID: id})
	}

	var fired *selector.Team
	for _, team := range selector.BuildTeams(config, routes, input.SelectedAgents, input.TaskText) {
		if team.ID == input.RecipeID {
			candidate := team
			fired = &candidate
			break
		}
	}
	if fired == nil {
		return nil, fmt.Errorf(
			"team recipe %q did not fire for this matched_route_ids/selected_agents%s signal "+
				"set; refusing to expand a recipe that would not actually have triggered",
			input.RecipeID, taskTextClause(recipe))
	}

	switch recipeType(recipe) {
	case "fixed":
		return expandFixed(input, fired)
	case "dynamic":
		return expandDynamic(input, recipe, fired)
	}
	return nil, fmt.Errorf("%q: unknown team recipe type %q", input.RecipeID, recipeType(recipe))
}

func findRecipe(config map[string]any, recipeID string) (map[string]any, error) {
	recipes, _ := config["team_recipes"].([]any)
	var known []string
	for _, raw := range recipes {
		recipe, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := recipe["id"].(string)
		known = append(known, id)
		if id == recipeID {
			return recipe, nil
		}
	}
	sort.Strings(known)
	return nil, fmt.Errorf("Unknown team recipe id %q; known ids: %s", //nolint:staticcheck // ported message
		recipeID, strings.Join(known, ", "))
}

func recipeType(recipe map[string]any) string {
	value, _ := recipe["type"].(string)
	return value
}

// taskTextClause keeps the refusal naming only the signals that recipe type
// actually consults, so a fixed recipe's message does not blame task text it
// never read.
func taskTextClause(recipe map[string]any) string {
	if recipeType(recipe) == "dynamic" {
		return "/task_text"
	}
	return ""
}

func expandFixed(input ExpandRecipeInput, fired *selector.Team) ([]TeamMember, error) {
	members := make([]TeamMember, 0, len(fired.Members))
	for _, roleID := range fired.Members {
		brief, ok := input.MemberBriefs[roleID]
		if !ok {
			brief = input.SharedBrief
		}
		if brief == "" {
			return nil, fmt.Errorf(
				"team recipe %q member %q has no brief: supply it in member_briefs "+
					"or provide shared_brief", input.RecipeID, roleID)
		}
		members = append(members, TeamMember{RoleID: roleID, Brief: brief})
	}
	return members, nil
}

func expandDynamic(input ExpandRecipeInput, recipe map[string]any, fired *selector.Team) ([]TeamMember, error) {
	instances, _ := recipe["instances"].(map[string]any)
	minimum, hasMin := jsonInt(instances["min"])
	maximum, hasMax := jsonInt(instances["max"])
	if !hasMin || !hasMax {
		return nil, fmt.Errorf("team recipe %q declares no instance range", input.RecipeID)
	}

	count := minimum
	if input.InstanceCount != nil {
		count = *input.InstanceCount
	}
	if count < minimum || count > maximum {
		return nil, fmt.Errorf(
			"team recipe %q instance_count %d is outside its declared [%d, %d] range",
			input.RecipeID, count, minimum, maximum)
	}

	// One brief per instance, never a shared one repeated. A dynamic recipe
	// like competing-hypotheses-debugging exists to run *distinct* hypotheses
	// in parallel; handing every instance the same brief spends the same
	// dispatch budget to get the same answer several times.
	if len(input.InstanceBriefs) != count {
		return nil, fmt.Errorf(
			"team recipe %q requires exactly %d instance_briefs entries (one per instance); got %d",
			input.RecipeID, count, len(input.InstanceBriefs))
	}

	roleID := fired.Role
	if roleID == "" {
		roleID, _ = recipe["role"].(string)
	}
	members := make([]TeamMember, 0, count)
	for _, brief := range input.InstanceBriefs {
		members = append(members, TeamMember{RoleID: roleID, Brief: brief})
	}
	return members, nil
}

// jsonInt reads an integer that arrived through JSON as a float64.
func jsonInt(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	}
	return 0, false
}

// DispatchTeamRecipeRequest is the dispatch_team_recipe tool's arguments.
type DispatchTeamRecipeRequest struct {
	RecipeID          string            `json:"recipe_id"`
	MatchedRouteIDs   []string          `json:"matched_route_ids"`
	SelectedAgentIDs  []string          `json:"selected_agent_ids"`
	Mode              string            `json:"mode"`
	Classification    string            `json:"classification"`
	ConfirmationToken string            `json:"confirmation_token"`
	TaskText          string            `json:"task_text"`
	SharedBrief       string            `json:"shared_brief"`
	MemberBriefs      map[string]string `json:"member_briefs"`
	InstanceBriefs    []string          `json:"instance_briefs"`
	InstanceCount     *int              `json:"instance_count"`
	Runner            string            `json:"runner"`
	Wait              *bool             `json:"wait"`
}

// teamRecipeToolDefinition describes dispatch_team_recipe.
func teamRecipeToolDefinition() MCPToolDefinition {
	return MCPToolDefinition{
		Name: "dispatch_team_recipe",
		Description: "Expand a routing.json team_recipes[] entry into concrete members and " +
			"dispatch them as a team, for a session that already has a real `cadre select` plan.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"recipe_id": map[string]any{"type": "string",
					"description": "A team_recipes[].id value, e.g. \"parallel-review\"."},
				"matched_route_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "The id of each matched_routes entry from the plan -- bare ids, not the objects."},
				"selected_agent_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "The union of agents.primary, agents.reviewers and agents.support."},
				"task_text":     map[string]any{"type": "string", "description": "The task text, for dynamic recipes."},
				"shared_brief":  map[string]any{"type": "string", "description": "One brief for every fixed member."},
				"member_briefs": map[string]any{"type": "object", "description": "Per-role briefs, overriding shared_brief."},
				"instance_briefs": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "One brief per instance for a dynamic recipe. A shared brief is refused: distinct instances exist to explore distinct hypotheses."},
				"instance_count":     map[string]any{"type": "integer"},
				"mode":               map[string]any{"type": "string"},
				"classification":     map[string]any{"type": "string"},
				"confirmation_token": map[string]any{"type": "string"},
				"runner":             map[string]any{"type": "string"},
				"wait":               map[string]any{"type": "boolean"},
			},
			"required": []string{"recipe_id", "matched_route_ids", "selected_agent_ids"},
		},
	}
}

// DispatchTeamRecipe expands the recipe, then dispatches the members exactly
// as dispatch_team does.
//
// The expansion is the whole value: it will not produce a member list the
// selector would not have proposed, so this tool cannot be used to assemble
// a team the plan never contained.
func (server *DispatchMCPServer) DispatchTeamRecipe(request DispatchTeamRecipeRequest) *MCPToolResponse {
	config, err := loadRoutingForRecipes(server.projectRoot)
	if err != nil {
		return &MCPToolResponse{Status: "error", IsError: true, Error: err.Error()}
	}

	members, err := ExpandRecipeToMembers(config, ExpandRecipeInput{
		RecipeID:        request.RecipeID,
		MatchedRouteIDs: request.MatchedRouteIDs,
		SelectedAgents:  request.SelectedAgentIDs,
		TaskText:        request.TaskText,
		SharedBrief:     request.SharedBrief,
		MemberBriefs:    request.MemberBriefs,
		InstanceBriefs:  request.InstanceBriefs,
		InstanceCount:   request.InstanceCount,
	})
	if err != nil {
		// A refusal to expand is reported as "denied", matching the Python
		// tool: the caller asked for something the plan does not support,
		// which is different from the dispatch itself failing.
		return &MCPToolResponse{Status: "denied", IsError: true, Error: err.Error()}
	}

	// Expanded members are handed to the same dispatch path dispatch_team
	// uses. Nothing about being recipe-derived relaxes what happens next:
	// mode, classification and the confirmation token are validated there.
	dispatchMembers := make([]map[string]string, 0, len(members))
	for _, member := range members {
		dispatchMembers = append(dispatchMembers,
			map[string]string{"role_id": member.RoleID, "brief": member.Brief})
	}
	wait := true
	if request.Wait != nil {
		wait = *request.Wait
	}
	return server.HandleDispatchTeam(&DispatchTeamRequest{
		Members:           dispatchMembers,
		Mode:              request.Mode,
		Classification:    request.Classification,
		ConfirmationToken: request.ConfirmationToken,
		Runner:            request.Runner,
		Wait:              wait,
	})
}

// loadRoutingForRecipes reads the routing config the recipes live in.
//
// The installation's own routing.json, not the target project's: a recipe is
// part of the roster's dispatch vocabulary, and letting a project supply its
// own would let it define teams the roster never sanctioned.
func loadRoutingForRecipes(projectRoot string) (map[string]any, error) {
	root, err := platform.FindInstallationRoot()
	if err != nil {
		return nil, fmt.Errorf("cannot locate the roster: %w", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "roster", "orchestration", "routing.json"))
	if err != nil {
		return nil, fmt.Errorf("cannot read routing.json: %w", err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("routing.json is not valid JSON: %w", err)
	}
	return config, nil
}
