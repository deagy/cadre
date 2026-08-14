package selector

import (
	"regexp"
	"sort"
	"strings"
)

// Team is one entry of the plan's `teams` array.
//
// Fixed and dynamic recipes emit different fields, and the omitempty tags
// reproduce that: a fixed team has members and a routes trigger, a dynamic one
// has a role, instances and a keywords trigger. Emitting both shapes' keys
// would change the plan.
type Team struct {
	ID                string      `json:"id"`
	Type              string      `json:"type"`
	Members           []string    `json:"members,omitempty"`
	Role              string      `json:"role,omitempty"`
	Instances         int         `json:"instances,omitempty"`
	TriggerReason     TeamTrigger `json:"trigger_reason"`
	CommunicationMode string      `json:"communication_mode"`
	Fallback          string      `json:"fallback"`
	Description       string      `json:"description"`
}

// TeamTrigger records why a team formed: matched routes for a fixed recipe,
// matched keywords for a dynamic one.
type TeamTrigger struct {
	Routes   []string `json:"routes,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
}

// BuildTeams ports _build_teams: named teams formed deterministically from
// the same signals routing already matched.
//
// A fixed recipe only ever surfaces agents routing or risk rules already
// selected -- a team never pulls in an agent that would not otherwise be
// dispatched. That is what makes the teams array safe to act on without
// re-checking authority.
func BuildTeams(config map[string]any, matchedRoutes []Match, selectedAgents []string, taskText string) []Team {
	selected := setOf(selectedAgents)
	matchedRouteIDs := map[string]bool{}
	for _, route := range matchedRoutes {
		matchedRouteIDs[route.ID] = true
	}

	recipes, _ := config["team_recipes"].([]any)
	teams := []Team{}
	for _, raw := range recipes {
		recipe, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch recipeType, _ := recipe["type"].(string); recipeType {
		case "fixed":
			team, ok := buildFixedTeam(recipe, matchedRouteIDs, selected)
			if ok {
				teams = append(teams, team)
			}
		case "dynamic":
			team, ok := buildDynamicTeam(recipe, matchedRouteIDs, selected, taskText)
			if ok {
				teams = append(teams, team)
			}
		}
	}
	return teams
}

func buildFixedTeam(recipe map[string]any, matchedRouteIDs, selected map[string]bool) (Team, bool) {
	var triggering []string
	for _, routeID := range stringSlice(recipe["route_ids"]) {
		if matchedRouteIDs[routeID] {
			triggering = append(triggering, routeID)
		}
	}
	sort.Strings(triggering)

	minimumMatches, _ := recipe["minimum_matches"].(float64)
	if float64(len(triggering)) < minimumMatches {
		return Team{}, false
	}

	var members []string
	for _, agent := range stringSlice(recipe["members"]) {
		if selected[agent] {
			members = append(members, agent)
		}
	}
	// Default 2, matching Python's .get(..., 2): a "team" of one is a single
	// dispatch, not a team.
	minimumMembers := 2.0
	if declared, ok := recipe["minimum_members_selected"].(float64); ok {
		minimumMembers = declared
	}
	if float64(len(members)) < minimumMembers {
		return Team{}, false
	}

	id, _ := recipe["id"].(string)
	mode, _ := recipe["communication_mode"].(string)
	fallback, _ := recipe["fallback"].(string)
	description, _ := recipe["description"].(string)
	return Team{
		ID:                id,
		Type:              "fixed",
		Members:           members,
		TriggerReason:     TeamTrigger{Routes: triggering},
		CommunicationMode: mode,
		Fallback:          fallback,
		Description:       description,
	}, true
}

func buildDynamicTeam(recipe map[string]any, matchedRouteIDs, selected map[string]bool, taskText string) (Team, bool) {
	role, _ := recipe["role"].(string)
	if !selected[role] {
		return Team{}, false
	}
	if required, ok := recipe["requires_route"].(string); ok && required != "" && !matchedRouteIDs[required] {
		return Team{}, false
	}
	var matchedKeywords []string
	for _, keyword := range stringSlice(recipe["keywords"]) {
		if KeywordMatches(strings.ToLower(taskText), keyword) {
			matchedKeywords = append(matchedKeywords, keyword)
		}
	}
	if len(matchedKeywords) == 0 {
		return Team{}, false
	}

	id, _ := recipe["id"].(string)
	instances, _ := recipe["instances"].(float64)
	mode, _ := recipe["communication_mode"].(string)
	fallback, _ := recipe["fallback"].(string)
	description, _ := recipe["description"].(string)
	return Team{
		ID:                id,
		Type:              "dynamic",
		Role:              role,
		Instances:         int(instances),
		TriggerReason:     TeamTrigger{Keywords: matchedKeywords},
		CommunicationMode: mode,
		Fallback:          fallback,
		Description:       description,
	}, true
}

var changeIntakeCache = map[string]*regexp.Regexp{}

// MatchesChangeIntake ports _matches_change_intake: implementation work that
// must start with intent and requirements.
//
// Note this uses a *different* boundary class from KeywordMatches --
// `[^a-z0-9]` rather than `[a-z0-9-]`. A hyphen is a boundary here and a word
// character there, so the same keyword can match under one and not the other.
// That difference is deliberate in Python and is preserved rather than
// unified; unifying them would silently change which tasks are treated as
// change intake.
func MatchesChangeIntake(config map[string]any, task string) bool {
	intake, _ := config["change_intake"].(map[string]any)
	if intake == nil {
		return false
	}
	normalized := strings.ToLower(task)
	for _, keyword := range stringSlice(intake["keywords"]) {
		pattern, cached := changeIntakeCache[keyword]
		if !cached {
			pattern = regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(strings.ToLower(keyword)) + `([^a-z0-9]|$)`)
			changeIntakeCache[keyword] = pattern
		}
		if pattern.MatchString(normalized) {
			return true
		}
	}
	return false
}

// ValidateAgents ports _validate_agents: routing may not select an agent the
// catalog does not declare.
func ValidateAgents(groups AgentGroups, catalog []string) error {
	known := setOf(catalog)
	for _, group := range [][]string{groups.Primary, groups.Reviewers, groups.Support} {
		for _, agent := range group {
			if !known[agent] {
				return &UnknownAgentError{Agent: agent}
			}
		}
	}
	return nil
}

// UnknownAgentError reports a selected agent absent from the catalog.
type UnknownAgentError struct{ Agent string }

func (e *UnknownAgentError) Error() string {
	return "Routing selected an unknown agent: " + e.Agent //nolint:staticcheck // ST1005: ported verbatim from build_dispatch_plan.py.
}
