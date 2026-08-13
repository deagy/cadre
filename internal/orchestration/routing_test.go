package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRouting(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		expectError bool
	}{
		{
			name:        "load real routing config",
			path:        "../../roster/orchestration/routing.json",
			expectError: false,
		},
		{
			name:        "nonexistent file",
			path:        "/nonexistent/routing.json",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wd, _ := os.Getwd()
			path := filepath.Join(wd, tt.path)

			config, err := LoadRouting(path)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if config == nil {
				t.Fatalf("config is nil")
			}
			if len(config.Routes) == 0 {
				t.Fatalf("routes is empty")
			}
		})
	}
}

func TestLoadCatalog(t *testing.T) {
	// Catalog is YAML format, skipping JSON-based test for now.
	// Will be tested when YAML parser is integrated.
	t.Skip("catalog.yaml is YAML format, test deferred until YAML parser added")
}

func TestValidateRouting(t *testing.T) {
	tests := []struct {
		name        string
		config      *RoutingConfig
		expectError bool
	}{
		{
			name:        "nil config",
			config:      nil,
			expectError: true,
		},
		{
			name: "valid config",
			config: &RoutingConfig{
				Routes: []Route{
					{ID: "route1", WorkflowShape: "unclassified"},
					{ID: "route2", WorkflowShape: "unclassified"},
				},
				RiskRules: []Risk{
					{ID: "risk1", Level: "high"},
				},
			},
			expectError: false,
		},
		{
			name: "duplicate route IDs",
			config: &RoutingConfig{
				Routes: []Route{
					{ID: "route1", WorkflowShape: "unclassified"},
					{ID: "route1", WorkflowShape: "unclassified"},
				},
			},
			expectError: true,
		},
		{
			name: "duplicate risk IDs",
			config: &RoutingConfig{
				Routes: []Route{
					{ID: "route1", WorkflowShape: "unclassified"},
				},
				RiskRules: []Risk{
					{ID: "risk1", Level: "high"},
					{ID: "risk1", Level: "high"},
				},
			},
			expectError: true,
		},
		{
			name: "empty route ID",
			config: &RoutingConfig{
				Routes: []Route{
					{ID: "", WorkflowShape: "unclassified"},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRouting(tt.config)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadRealRoutingStructure(t *testing.T) {
	// Load and inspect real routing config to understand its structure
	wd, _ := os.Getwd()
	path := filepath.Join(wd, "../../roster/orchestration/routing.json")

	config, err := LoadRouting(path)
	if err != nil {
		t.Skipf("cannot load routing config: %v", err)
	}

	// Verify structure
	if len(config.Routes) == 0 {
		t.Errorf("routes array is empty")
	}

	// Spot-check first route
	if len(config.Routes) > 0 {
		route := config.Routes[0]
		if route.ID == "" {
			t.Errorf("first route has empty ID")
		}
		if route.WorkflowShape == "" {
			t.Errorf("first route has empty workflow_shape")
		}
	}

	t.Logf("loaded %d routes, %d risks", len(config.Routes), len(config.RiskRules))
}

func TestRoundTripRouting(t *testing.T) {
	// Create, serialize, deserialize, and verify a routing config
	original := &RoutingConfig{
		Routes: []Route{
			{
				ID:            "test-route",
				WorkflowShape: "unclassified",
				Keywords:      []string{"keyword1", "keyword2"},
				Primary:       []string{"agent1"},
				Reviewers:     []string{"agent2"},
			},
		},
		RiskRules: []Risk{
			{
				ID:       "test-risk",
				Level:    "high",
				Keywords: []string{"sensitive"},
			},
		},
	}

	// Serialize
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("cannot marshal: %v", err)
	}

	// Deserialize
	var restored RoutingConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("cannot unmarshal: %v", err)
	}

	// Verify
	if len(restored.Routes) != 1 {
		t.Errorf("expected 1 route, got %d", len(restored.Routes))
	}
	if restored.Routes[0].ID != "test-route" {
		t.Errorf("route ID mismatch: %q", restored.Routes[0].ID)
	}
	if len(restored.RiskRules) != 1 {
		t.Errorf("expected 1 risk, got %d", len(restored.RiskRules))
	}
}
