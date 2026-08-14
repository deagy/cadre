package orchestration

import (
	"context"
	"strings"
	"testing"
)

func TestNoOpRetriever(t *testing.T) {
	retriever := &NoOpRetriever{}

	config := KnowledgeContext{
		Enabled: true,
	}

	result, err := retriever.Retrieve(context.Background(), config, "test task", "internal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatalf("result is nil")
	}

	if result.Status != "disabled" {
		t.Errorf("expected status disabled, got %q", result.Status)
	}

	if len(result.Passages) != 0 {
		t.Errorf("expected 0 passages, got %d", len(result.Passages))
	}
}

func TestFormatKnowledgeForAgent(t *testing.T) {
	knowledge := &RetrievedKnowledge{
		Query:          "test",
		Classification: "high",
		Status:         "success",
		Passages: []KnowledgePassage{
			{
				ID:      "p1",
				Source:  "docs",
				Content: "Some documentation",
			},
		},
		TotalCount: 1,
	}

	injection := FormatKnowledgeForAgent(knowledge, "agent-1", "primary")

	if injection == nil {
		t.Fatalf("injection is nil")
	}

	if injection.AgentID != "agent-1" {
		t.Errorf("agent ID mismatch: got %q", injection.AgentID)
	}

	if injection.Role != "primary" {
		t.Errorf("role mismatch: got %q", injection.Role)
	}

	if injection.Knowledge != knowledge {
		t.Errorf("knowledge mismatch")
	}

	if injection.Instructions == "" {
		t.Errorf("instructions are empty")
	}

	if !strings.Contains(injection.Instructions, "Knowledge Context") {
		t.Errorf("instructions missing Knowledge Context header")
	}
}

func TestFormatKnowledgeForAgentNil(t *testing.T) {
	injection := FormatKnowledgeForAgent(nil, "agent-1", "primary")

	if injection == nil {
		t.Fatalf("injection is nil")
	}

	if injection.Knowledge.Status != "disabled" {
		t.Errorf("expected status disabled, got %q", injection.Knowledge.Status)
	}
}

func TestValidateKnowledgeConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    KnowledgeContext
		expectErr bool
	}{
		{
			name:      "disabled",
			config:    KnowledgeContext{Enabled: false},
			expectErr: false,
		},
		{
			name:      "valid enabled",
			config:    KnowledgeContext{Enabled: true, Sources: []string{"docs"}, Classifications: []string{"high"}, TopK: 5},
			expectErr: false,
		},
		{
			name:      "enabled but no sources",
			config:    KnowledgeContext{Enabled: true, Classifications: []string{"high"}, TopK: 5},
			expectErr: true,
		},
		{
			name:      "enabled but no classifications",
			config:    KnowledgeContext{Enabled: true, Sources: []string{"docs"}, TopK: 5},
			expectErr: true,
		},
		{
			name:      "topk too low",
			config:    KnowledgeContext{Enabled: true, Sources: []string{"docs"}, Classifications: []string{"high"}, TopK: 0},
			expectErr: true,
		},
		{
			name:      "topk too high",
			config:    KnowledgeContext{Enabled: true, Sources: []string{"docs"}, Classifications: []string{"high"}, TopK: 21},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKnowledgeConfig(tt.config)
			if (err != nil) != tt.expectErr {
				t.Errorf("ValidateKnowledgeConfig expected error=%v, got %v", tt.expectErr, err)
			}
		})
	}
}

func TestFormatSources(t *testing.T) {
	passages := []KnowledgePassage{
		{Source: "docs"},
		{Source: "chat"},
		{Source: "docs"}, // duplicate
		{Source: ""},     // empty
	}

	sources := formatSources(passages)

	if !strings.Contains(sources, "docs") {
		t.Errorf("missing source docs")
	}

	if !strings.Contains(sources, "chat") {
		t.Errorf("missing source chat")
	}

	// Should not have duplicates or empty
	docCount := strings.Count(sources, "docs")
	if docCount != 1 {
		t.Errorf("docs appears %d times, expected 1", docCount)
	}
}

func TestBuildKnowledgeInstructions(t *testing.T) {
	tests := []struct {
		name           string
		knowledge      *RetrievedKnowledge
		shouldHaveText bool
		expectedText   string
	}{
		{
			name:           "nil knowledge",
			knowledge:      nil,
			shouldHaveText: false,
		},
		{
			name:           "disabled knowledge",
			knowledge:      &RetrievedKnowledge{Status: "disabled"},
			shouldHaveText: false,
		},
		{
			name: "error knowledge",
			knowledge: &RetrievedKnowledge{
				Status: "error",
				Error:  "retrieval failed",
			},
			shouldHaveText: false,
		},
		{
			name: "success with passages",
			knowledge: &RetrievedKnowledge{
				Status:         "success",
				Classification: "high",
				TotalCount:     2,
				Passages: []KnowledgePassage{
					{Source: "docs"},
					{Source: "chat"},
				},
			},
			shouldHaveText: true,
			expectedText:   "Knowledge Context",
		},
		{
			name: "success but no passages",
			knowledge: &RetrievedKnowledge{
				Status:     "success",
				TotalCount: 0,
				Passages:   []KnowledgePassage{},
			},
			shouldHaveText: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instructions := buildKnowledgeInstructions(tt.knowledge)
			hasText := instructions != ""
			if hasText != tt.shouldHaveText {
				t.Errorf("expected hasText=%v, got %v", tt.shouldHaveText, hasText)
			}

			if tt.shouldHaveText && tt.expectedText != "" {
				if !strings.Contains(instructions, tt.expectedText) {
					t.Errorf("instructions missing %q", tt.expectedText)
				}
			}
		})
	}
}
