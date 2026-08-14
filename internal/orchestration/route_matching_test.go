package orchestration

import (
	"testing"
)

func TestGlobToRegex(t *testing.T) {
	tests := []struct {
		glob    string
		matches []string
		fails   []string
	}{
		{
			glob: "*.go",
			matches: []string{
				"main.go",
				"test.go",
				"dispatcher.go",
			},
			fails: []string{
				"main.py",
				"dir/test.go",
				"go",
			},
		},
		{
			glob: "src/**/*.go",
			matches: []string{
				"src/main.go",
				"src/cmd/main.go",
				"src/internal/cli/dispatcher.go",
			},
			fails: []string{
				"main.go",
				"tests/main.go",
			},
		},
		{
			glob: "cmd/cadre/*",
			matches: []string{
				"cmd/cadre/main.go",
				"cmd/cadre/version.go",
			},
			fails: []string{
				"cmd/cadre",
				"cmd/other/main.go",
				"cmd/cadre/sub/file.go",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.glob, func(t *testing.T) {
			re := globToRegex(tt.glob)

			for _, match := range tt.matches {
				if !re.MatchString(match) {
					t.Errorf("glob %q should match %q", tt.glob, match)
				}
			}

			for _, fail := range tt.fails {
				if re.MatchString(fail) {
					t.Errorf("glob %q should not match %q", tt.glob, fail)
				}
			}
		})
	}
}

func TestMatchesKeywords(t *testing.T) {
	tests := []struct {
		task     string
		keywords []string
		expected bool
	}{
		{
			task: "Add a new API endpoint for user authentication",
			keywords: []string{
				"API",
				"endpoint",
			},
			expected: true,
		},
		{
			task: "Fix bug in the authentication module",
			keywords: []string{
				"database",
				"schema",
			},
			expected: false,
		},
		{
			task: "Refactor kubernetes manifests",
			keywords: []string{
				"kubernetes",
				"helm",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.task, func(t *testing.T) {
			result := matchesKeywordList(tt.task, tt.keywords)
			if result != tt.expected {
				t.Errorf("matchesKeywordList(%q, ...) = %v, want %v", tt.task, result, tt.expected)
			}
		})
	}
}

func TestMatchesRiskRules(t *testing.T) {
	tests := []struct {
		classification string
		risks          []Risk
		expected       bool
	}{
		{
			classification: "critical",
			risks: []Risk{
				{
					ID:    "risk-1",
					Level: "critical",
				},
			},
			expected: true,
		},
		{
			classification: "internal",
			risks: []Risk{
				{
					ID:    "risk-2",
					Level: "high",
				},
			},
			expected: false,
		},
		{
			classification: "high",
			risks: []Risk{
				{
					ID:    "risk-3",
					Level: "high",
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.classification, func(t *testing.T) {
			result := matchesRiskRulesForClassification(tt.classification, tt.risks)
			if result != tt.expected {
				t.Errorf("matchesRiskRulesForClassification(%q, ...) = %v, want %v", tt.classification, result, tt.expected)
			}
		})
	}
}

func TestMatchesPaths(t *testing.T) {
	tests := []struct {
		files    []string
		patterns []string
		expected bool
	}{
		{
			files: []string{
				"main.go",
				"test.go",
			},
			patterns: []string{
				"*.go",
			},
			expected: true,
		},
		{
			files: []string{
				"src/main.py",
				"src/test.py",
			},
			patterns: []string{
				"*.go",
				"*.js",
			},
			expected: false,
		},
		{
			files: []string{
				"cmd/cadre/main.go",
				"internal/cli/dispatcher.go",
			},
			patterns: []string{
				"cmd/**/*.go",
			},
			expected: true,
		},
	}

	for i, tt := range tests {
		result := matchesPathList(tt.files, tt.patterns)
		if result != tt.expected {
			t.Errorf("test %d: matchesPathList(...) = %v, want %v", i, result, tt.expected)
		}
	}
}

func TestSelectAgents(t *testing.T) {
	matches := []RouteMatch{
		{
			RouteID:   "route-1",
			Primary:   []string{"agent-1", "agent-2"},
			Reviewers: []string{"agent-3"},
			Support:   []string{"agent-4"},
		},
		{
			RouteID:   "route-2",
			Primary:   []string{"agent-2"}, // Duplicate - should be deduplicated
			Reviewers: []string{"agent-5"},
			Support:   []string{},
		},
	}

	primary, reviewers, support := SelectAgents(matches)

	if len(primary) != 2 {
		t.Errorf("expected 2 primary agents, got %d", len(primary))
	}

	if len(reviewers) != 2 {
		t.Errorf("expected 2 reviewer agents, got %d", len(reviewers))
	}

	if len(support) != 1 {
		t.Errorf("expected 1 support agent, got %d", len(support))
	}

	// Verify no duplicates
	seenPrimary := make(map[string]bool)
	for _, a := range primary {
		if seenPrimary[a] {
			t.Errorf("duplicate primary agent: %s", a)
		}
		seenPrimary[a] = true
	}
}

func TestMatchRoute(t *testing.T) {
	route := Route{
		ID: "test-route",
		Keywords: []string{
			"API",
			"endpoint",
		},
		Primary: []string{
			"backend-engineer",
			"api-contract-engineer",
		},
		Reviewers: []string{
			"code-reviewer",
		},
	}

	match := matchRoute("Design an API endpoint for user authentication", []string{}, "", route)
	if match == nil {
		t.Fatalf("expected route to match, got nil")
	}

	if match.RouteID != "test-route" {
		t.Errorf("route ID mismatch: got %q", match.RouteID)
	}

	if len(match.Primary) != 2 {
		t.Errorf("expected 2 primary agents, got %d", len(match.Primary))
	}

	if len(match.Reviewers) != 1 {
		t.Errorf("expected 1 reviewer agent, got %d", len(match.Reviewers))
	}

	if _, ok := match.Reasons["keyword_match"]; !ok {
		t.Errorf("expected keyword_match reason to be present")
	}
}

func TestMatchTaskToRoutes(t *testing.T) {
	routing := &RoutingConfig{
		Routes: []Route{
			{
				ID: "backend-route",
				Keywords: []string{
					"API",
					"backend",
				},
				Primary: []string{
					"backend-engineer",
				},
			},
			{
				ID: "frontend-route",
				Keywords: []string{
					"React",
					"frontend",
				},
				Primary: []string{
					"frontend-engineer",
				},
			},
		},
	}

	matches, err := MatchTaskToRoutes(
		"Implement a REST API endpoint for user authentication",
		[]string{},
		"internal",
		routing,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(matches))
	}

	if matches[0].RouteID != "backend-route" {
		t.Errorf("expected backend-route, got %q", matches[0].RouteID)
	}
}

func TestMatchTaskToRoutesNoMatch(t *testing.T) {
	routing := &RoutingConfig{
		Routes: []Route{
			{
				ID: "api-route",
				Keywords: []string{
					"REST",
					"endpoint",
				},
				Primary: []string{
					"backend-engineer",
				},
			},
		},
	}

	_, err := MatchTaskToRoutes(
		"Write documentation about deployment procedures",
		[]string{},
		"internal",
		routing,
	)

	if err == nil {
		t.Fatalf("expected error for no matching routes")
	}
}
