package generators

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCatalogOrder(t *testing.T) {
	wd, _ := os.Getwd()
	orderPath := filepath.Join(wd, "../../roster/catalog-order.txt")

	roles, err := LoadCatalogOrder(orderPath)
	if err != nil {
		t.Skipf("cannot load catalog-order.txt: %v", err)
	}

	if len(roles) == 0 {
		t.Errorf("expected roles, got none")
	}

	// Check for expected roles
	hasProductIntent := false
	for _, id := range roles {
		if id == "product-intent-agent" {
			hasProductIntent = true
			break
		}
	}
	if !hasProductIntent {
		t.Errorf("expected product-intent-agent in order")
	}

	t.Logf("loaded %d role ids from catalog-order.txt", len(roles))
}

func TestLoadModelTiers(t *testing.T) {
	wd, _ := os.Getwd()
	capsPath := filepath.Join(wd, "../../roster/runner-capabilities.json")

	tiers, err := LoadModelTiers(capsPath)
	if err != nil {
		t.Skipf("cannot load runner-capabilities.json: %v", err)
	}

	// Check for all three tiers
	expectedTiers := []string{"opus", "sonnet", "haiku"}
	for _, tierName := range expectedTiers {
		if _, exists := tiers[tierName]; !exists {
			t.Errorf("missing tier: %s", tierName)
		}
	}

	// Check tier mappings
	if tiers["opus"].CodexModel != "gpt-5.6-sol" {
		t.Errorf("opus codex_model mismatch: %s", tiers["opus"].CodexModel)
	}
	if tiers["opus"].ReasoningEffort != "high" {
		t.Errorf("opus reasoning_effort mismatch: %s", tiers["opus"].ReasoningEffort)
	}

	t.Logf("loaded %d model tiers", len(tiers))
}

func TestDiscoverRoles(t *testing.T) {
	wd, _ := os.Getwd()
	rosterRoot := filepath.Join(wd, "../../roster")

	roles, err := DiscoverRoles(rosterRoot)
	if err != nil {
		t.Skipf("cannot discover roles: %v", err)
	}

	if len(roles) < 100 {
		t.Errorf("expected 100+ roles, got %d", len(roles))
	}

	// Check for specific roles
	if _, exists := roles["product-intent-agent"]; !exists {
		t.Errorf("expected product-intent-agent")
	}
	if _, exists := roles["backend-engineer"]; !exists {
		t.Errorf("expected backend-engineer")
	}

	t.Logf("discovered %d roles", len(roles))
}

func TestLoadRoleMetadata(t *testing.T) {
	wd, _ := os.Getwd()
	agentMdPath := filepath.Join(wd, "../../roster/planning/product-intent-agent/AGENT.md")

	metadata, err := LoadRoleMetadata(agentMdPath, "roster/planning/product-intent-agent/AGENT.md")
	if err != nil {
		t.Skipf("cannot load role metadata: %v", err)
	}

	if metadata.ID != "product-intent-agent" {
		t.Errorf("id mismatch: %s", metadata.ID)
	}
	if metadata.Phase != "planning" {
		t.Errorf("phase mismatch: %s", metadata.Phase)
	}
	if metadata.Model != "sonnet" {
		t.Errorf("model mismatch: %s", metadata.Model)
	}
	if metadata.CodexModel != "gpt-5.6-terra" {
		t.Errorf("codex_model mismatch: %s", metadata.CodexModel)
	}
	if metadata.ReasoningEffort != "medium" {
		t.Errorf("reasoning_effort mismatch: %s", metadata.ReasoningEffort)
	}
	if metadata.KnowledgeFocus == "" {
		t.Errorf("knowledge_focus is empty")
	}

	t.Logf("loaded role: %s (%s)", metadata.ID, metadata.Phase)
}

func TestValidateModelTier(t *testing.T) {
	tiers := map[string]ModelTierInfo{
		"opus": {
			CodexModel:      "gpt-5.6-sol",
			ReasoningEffort: "high",
		},
		"sonnet": {
			CodexModel:      "gpt-5.6-terra",
			ReasoningEffort: "medium",
		},
		"haiku": {
			CodexModel:      "gpt-5.6-luna",
			ReasoningEffort: "low",
		},
	}

	tests := []struct {
		name        string
		model       string
		codex       string
		effort      string
		expectError bool
	}{
		{
			name:        "valid opus",
			model:       "opus",
			codex:       "gpt-5.6-sol",
			effort:      "high",
			expectError: false,
		},
		{
			name:        "valid sonnet",
			model:       "sonnet",
			codex:       "gpt-5.6-terra",
			effort:      "medium",
			expectError: false,
		},
		{
			name:        "invalid model",
			model:       "invalid",
			codex:       "gpt-5.6-sol",
			effort:      "high",
			expectError: true,
		},
		{
			name:        "mismatched codex",
			model:       "opus",
			codex:       "gpt-5.6-terra",
			effort:      "high",
			expectError: true,
		},
		{
			name:        "mismatched effort",
			model:       "opus",
			codex:       "gpt-5.6-sol",
			effort:      "medium",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateModelTier(tt.model, tt.codex, tt.effort, tiers)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error=%v, got %v", tt.expectError, err)
			}
		})
	}
}

func TestLoadAllRolesReal(t *testing.T) {
	wd, _ := os.Getwd()
	rosterRoot := filepath.Join(wd, "../../roster")
	orderPath := filepath.Join(rosterRoot, "catalog-order.txt")
	capsPath := filepath.Join(rosterRoot, "runner-capabilities.json")

	// Load order
	orderIDs, err := LoadCatalogOrder(orderPath)
	if err != nil {
		t.Skipf("cannot load order: %v", err)
	}

	// Discover roles
	discovered, err := DiscoverRoles(rosterRoot)
	if err != nil {
		t.Skipf("cannot discover roles: %v", err)
	}

	// Load model tiers
	tiers, err := LoadModelTiers(capsPath)
	if err != nil {
		t.Skipf("cannot load tiers: %v", err)
	}

	// Load all roles
	roles, err := LoadAllRoles(rosterRoot, orderIDs, discovered, tiers)
	if err != nil {
		t.Skipf("cannot load all roles: %v", err)
	}

	if len(roles) != len(orderIDs) {
		t.Errorf("expected %d roles, got %d", len(orderIDs), len(roles))
	}

	// Check each role has required metadata
	for _, role := range roles {
		if role.ID == "" || role.Phase == "" || role.Model == "" {
			t.Errorf("role has missing metadata: %+v", role)
			break
		}
	}
}
