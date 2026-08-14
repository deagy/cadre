package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
)

// RoutingConfig holds the complete routing configuration for agent selection.
// Additional fields beyond Routes/RiskRules are kept as interface{} for forward compatibility.
type RoutingConfig struct {
	Version                 int           `json:"version"`
	Routes                  []Route       `json:"routes"`
	RiskRules               []Risk        `json:"risk_rules"`
	TeamRecipes             []interface{} `json:"team_recipes,omitempty"`
	DefaultGateReviewAgents []string      `json:"default_gate_review_agents,omitempty"`
	IgnoredGates            []string      `json:"ignored_gates,omitempty"`
	KnowledgeFocus          interface{}   `json:"knowledge_focus,omitempty"`
	ChangeIntake            interface{}   `json:"change_intake,omitempty"`
	ContextPacks            interface{}   `json:"context_packs,omitempty"`
	CrossStack              interface{}   `json:"cross_stack,omitempty"`
}

// Route is a single routing rule for agent selection.
type Route struct {
	ID             string   `json:"id"`
	WorkflowShape  string   `json:"workflow_shape"`
	Paths          []string `json:"paths"`
	ExcludePaths   []string `json:"exclude_paths,omitempty"`
	Keywords       []string `json:"keywords"`
	Primary        []string `json:"primary"`
	Reviewers      []string `json:"reviewers"`
	Support        []string `json:"support"`
	QualityGates   []string `json:"quality_gates"`
	RequiredGates  []string `json:"required_gates,omitempty"`
	HumanGates     []string `json:"human_gates,omitempty"`
	LifecycleGates []string `json:"lifecycle_gates,omitempty"`
}

// Risk is a risk classification rule.
type Risk struct {
	ID       string   `json:"id"`
	Paths    []string `json:"paths"`
	Keywords []string `json:"keywords"`
	Level    string   `json:"level"` // e.g. "high", "medium", "low"
}

// Catalog holds the roles and metadata (loaded from catalog.yaml via JSON).
type Catalog struct {
	Roles map[string]interface{} `json:"roles,omitempty"`
}

// LoadRouting loads and parses a routing.json file.
func LoadRouting(path string) (*RoutingConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("routing config not found: %s", path)
		}
		return nil, fmt.Errorf("cannot read routing config %s: %w", path, err)
	}

	var config RoutingConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %w", path, err)
	}

	return &config, nil
}

// LoadCatalog loads a catalog file (assumes JSON, which is the case for both
// catalog.yaml converted to JSON and native JSON catalogs).
func LoadCatalog(path string) (*Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("catalog not found: %s", path)
		}
		return nil, fmt.Errorf("cannot read catalog %s: %w", path, err)
	}

	var catalog Catalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %w", path, err)
	}

	return &catalog, nil
}

// ValidateRouting performs structural validation on a routing config.
func ValidateRouting(config *RoutingConfig) error {
	if config == nil {
		return fmt.Errorf("routing config is nil")
	}

	// Check for duplicate route IDs
	seenIDs := make(map[string]bool)
	for _, route := range config.Routes {
		if route.ID == "" {
			return fmt.Errorf("route has empty id")
		}
		if seenIDs[route.ID] {
			return fmt.Errorf("duplicate route id: %q", route.ID)
		}
		seenIDs[route.ID] = true
	}

	// Check for duplicate risk IDs
	seenRiskIDs := make(map[string]bool)
	for _, risk := range config.RiskRules {
		if risk.ID == "" {
			return fmt.Errorf("risk has empty id")
		}
		if seenRiskIDs[risk.ID] {
			return fmt.Errorf("duplicate risk id: %q", risk.ID)
		}
		seenRiskIDs[risk.ID] = true
	}

	return nil
}
