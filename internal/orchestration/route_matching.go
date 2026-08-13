package orchestration

import (
	"fmt"
	"regexp"
	"strings"
)

// RouteMatch represents a single route that matched a task.
type RouteMatch struct {
	RouteID   string            // Route identifier from routing.json
	Rank      int               // Ranking priority (lower = higher priority)
	Agents    []string          // Selected agent IDs
	Primary   []string          // Primary agents (can author/execute)
	Reviewers []string          // Reviewer agents (independent review)
	Support   []string          // Support agents (advisory, no execution)
	Reasons   map[string]string // Why this route was selected
}

// MatchTaskToRoutes finds all routes matching a task description.
// Implements path-based, keyword-based, and risk-based routing from routing.json.
func MatchTaskToRoutes(task string, files []string, classification string, routing *RoutingConfig) ([]RouteMatch, error) {
	if routing == nil || len(routing.Routes) == 0 {
		return nil, fmt.Errorf("no routing rules configured")
	}

	var matches []RouteMatch

	// Route matching uses three strategies:
	// 1. Path-based: does the task description or changed files match path patterns?
	// 2. Keyword-based: does the task description contain keywords?
	// 3. Risk-based: what classification/risk rules apply?

	for _, route := range routing.Routes {
		if matched := matchRoute(task, files, classification, route); matched != nil {
			matches = append(matches, *matched)
		}
	}

	// Sort by rank (lower rank = higher priority)
	// TODO: Implement stable sort when adding more sorting criteria
	if len(matches) == 0 {
		return nil, fmt.Errorf("no routes matched task: %q", task)
	}

	return matches, nil
}

// matchRoute checks if a single route matches the task.
func matchRoute(task string, files []string, classification string, route Route) *RouteMatch {
	if route.ID == "" {
		return nil // Skip routes without ID
	}

	match := &RouteMatch{
		RouteID: route.ID,
		Reasons: make(map[string]string),
	}

	// Check path patterns
	if len(route.Paths) > 0 {
		if matchesPathList(files, route.Paths) {
			match.Reasons["path_match"] = "file paths match route patterns"
		}
	}

	// Check keywords
	if len(route.Keywords) > 0 {
		if matchesKeywordList(task, route.Keywords) {
			match.Reasons["keyword_match"] = "task description matches route keywords"
		}
	}

	// If no matching criteria satisfied, skip this route
	if len(match.Reasons) == 0 {
		return nil
	}

	// Extract agents from route
	match.Primary = append(match.Primary, route.Primary...)
	match.Reviewers = append(match.Reviewers, route.Reviewers...)
	match.Support = append(match.Support, route.Support...)

	// Collect all agents
	match.Agents = append(match.Agents, route.Primary...)
	match.Agents = append(match.Agents, route.Reviewers...)
	match.Agents = append(match.Agents, route.Support...)

	return match
}

// matchesPathList checks if any file matches the route's path patterns.
func matchesPathList(files []string, patterns []string) bool {
	for _, pattern := range patterns {
		re := globToRegex(pattern)
		for _, file := range files {
			if re.MatchString(file) {
				return true
			}
		}
	}
	return false
}

// matchesKeywordList checks if the task description contains any route keywords.
func matchesKeywordList(task string, keywords []string) bool {
	taskLower := strings.ToLower(task)
	for _, kw := range keywords {
		if strings.Contains(taskLower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// matchesRiskRules checks if the classification matches risk-based rules.
func matchesRiskRulesForClassification(classification string, risks []Risk) bool {
	if classification == "" {
		return false
	}

	classLower := strings.ToLower(classification)
	for _, risk := range risks {
		if strings.ToLower(risk.Level) == classLower {
			return true
		}
	}
	return false
}

// globToRegex converts a shell glob pattern to a regex.
// Supports *, ?, and [...] patterns.
func globToRegex(glob string) *regexp.Regexp {
	var pattern strings.Builder
	pattern.WriteString("^")

	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				// ** matches anything including / (with optional /)
				// src/**/*.go should match src/main.go and src/cmd/main.go
				pattern.WriteString("(?:.*/)?")
				i++ // Skip the next *
				// Skip any following / character
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
				}
			} else {
				// * matches anything except /
				pattern.WriteString("[^/]*")
			}
		case '?':
			// ? matches any single character except /
			pattern.WriteString("[^/]")
		case '[':
			// Character class [...] - find the closing ]
			j := strings.IndexByte(glob[i:], ']')
			if j == -1 {
				pattern.WriteByte('[')
			} else {
				pattern.WriteString(glob[i : i+j+1])
				i += j
			}
		case '.', '+', '^', '$', '(', ')', '|', '{', '}':
			// Escape regex special characters
			pattern.WriteByte('\\')
			pattern.WriteByte(c)
		default:
			pattern.WriteByte(c)
		}
	}

	pattern.WriteString("$")

	re, err := regexp.Compile(pattern.String())
	if err != nil {
		// Fallback to exact match if regex compilation fails
		re, _ = regexp.Compile("^" + regexp.QuoteMeta(glob) + "$")
	}
	return re
}

// SelectAgents groups matched routes' agents by role.
// Implements the selection logic from build_dispatch_plan.py.
func SelectAgents(matches []RouteMatch) (primary []string, reviewers []string, support []string) {
	seen := make(map[string]bool)

	for _, match := range matches {
		for _, agent := range match.Primary {
			if !seen[agent] {
				primary = append(primary, agent)
				seen[agent] = true
			}
		}
	}

	for _, match := range matches {
		for _, agent := range match.Reviewers {
			if !seen[agent] {
				reviewers = append(reviewers, agent)
				seen[agent] = true
			}
		}
	}

	for _, match := range matches {
		for _, agent := range match.Support {
			if !seen[agent] {
				support = append(support, agent)
				seen[agent] = true
			}
		}
	}

	return
}
