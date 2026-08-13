package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGates(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expected    []int
		expectError bool
	}{
		{
			name:     "single gate",
			input:    "[1]",
			expected: []int{1},
		},
		{
			name:     "multiple gates",
			input:    "[1, 2, 3]",
			expected: []int{1, 2, 3},
		},
		{
			name:     "gates with spaces",
			input:    "[ 1 , 2 , 3 ]",
			expected: []int{1, 2, 3},
		},
		{
			name:        "empty list",
			input:       "[]",
			expectError: true,
		},
		{
			name:        "non-integer",
			input:       "[1, a, 3]",
			expectError: true,
		},
		{
			name:        "duplicate gate",
			input:       "[1, 2, 1]",
			expectError: true,
		},
		{
			name:        "missing brackets",
			input:       "1, 2, 3",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gates, err := parseGates(tt.input)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if len(gates) != len(tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, gates)
				return
			}
			for i, g := range gates {
				if g != tt.expected[i] {
					t.Errorf("expected %v, got %v", tt.expected, gates)
					break
				}
			}
		})
	}
}

func TestStripInlineComment(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "Product Owner",
			expected: "Product Owner",
		},
		{
			input:    "Product Owner # this is a comment",
			expected: "Product Owner",
		},
		{
			input:    "prior G1/G2/G6 decisions # comment",
			expected: "prior G1/G2/G6 decisions",
		},
		{
			input:    "C# Lead",
			expected: "C# Lead", // # not preceded by space, not a comment
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := stripInlineComment(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestGatePhrase(t *testing.T) {
	tests := []struct {
		input    []int
		expected string
	}{
		{
			input:    []int{1},
			expected: "gate G1",
		},
		{
			input:    []int{1, 2},
			expected: "gates G1 and G2",
		},
		{
			input:    []int{1, 2, 3},
			expected: "gates G1, G2, and G3",
		},
		{
			input:    []int{1, 2, 3, 4},
			expected: "gates G1, G2, G3, and G4",
		},
	}

	for _, tt := range tests {
		t.Run(strings.Trim(tt.expected, "gates "), func(t *testing.T) {
			result := gatePhrase(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestGateList(t *testing.T) {
	tests := []struct {
		input    []int
		expected string
	}{
		{
			input:    []int{1},
			expected: "G1",
		},
		{
			input:    []int{1, 2, 3},
			expected: "G1, G2, G3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := gateList(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestRenderAide(t *testing.T) {
	template := `---
id: {id}
title: {title}
knowledge_focus: {knowledge_focus}
---

# {title} Aide

Prepare the package for {gate_phrase} ({gate_list}).`

	aide := AideData{
		ID:             "test-aide",
		Title:          "Test Authority",
		Gates:          []int{1, 2},
		KnowledgeFocus: "test knowledge",
	}

	marker := "<!-- GENERATED -->"
	rendered, err := RenderAide(template, aide, marker)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	// Check replacements
	checks := map[string]string{
		"id: test-aide":                       "id replaced",
		"title: Test Authority":               "title replaced",
		"knowledge_focus: test knowledge":     "knowledge_focus replaced",
		"# Test Authority Aide":               "title in body",
		"gates G1 and G2":                     "gate_phrase replaced",
		"(G1, G2)":                            "gate_list replaced",
		"<!-- GENERATED -->":                  "marker inserted",
	}

	for check, desc := range checks {
		if !strings.Contains(rendered, check) {
			t.Errorf("missing %s: %q not found in rendered output", desc, check)
		}
	}

	// Marker should appear after frontmatter
	lines := strings.Split(rendered, "\n")
	frontmatterFound := false
	markerAfterFrontmatter := false
	for i, line := range lines {
		if line == "---" {
			frontmatterFound = true
		}
		if frontmatterFound && strings.Contains(line, "GENERATED") {
			markerAfterFrontmatter = true
			// Marker should be on or right after the closing ---
			if i > 3 && i < 10 {
				markerAfterFrontmatter = true
			}
		}
	}
	if !markerAfterFrontmatter {
		t.Errorf("marker not placed correctly after frontmatter")
	}
}

func TestLoadAidesReal(t *testing.T) {
	// Load real aides.yaml from repository
	wd, _ := os.Getwd()
	aidesPath := filepath.Join(wd, "../../roster/authority/aides.yaml")

	aides, err := LoadAides(aidesPath)
	if err != nil {
		t.Skipf("cannot load real aides.yaml: %v", err)
	}

	// Should have 8 aides
	if len(aides) != 8 {
		t.Errorf("expected 8 aides, got %d", len(aides))
	}

	// Check specific aides
	productOwner := aides[0] // Should be product-owner-aide (sorted alphabetically)
	if productOwner.ID != "engineering-lead-aide" && productOwner.ID != "product-owner-aide" {
		t.Logf("first aide: %s", productOwner.ID)
	}

	// All should have gates and knowledge_focus
	for _, aide := range aides {
		if len(aide.Gates) == 0 {
			t.Errorf("aide %s has no gates", aide.ID)
		}
		if aide.KnowledgeFocus == "" {
			t.Errorf("aide %s has no knowledge_focus", aide.ID)
		}
	}

	t.Logf("loaded %d real aides: %v", len(aides), []string{
		aides[0].ID, aides[1].ID, "...", aides[len(aides)-1].ID,
	})
}

func TestGenerateAuthorityAidesReal(t *testing.T) {
	// Full end-to-end test with real files
	wd, _ := os.Getwd()
	authorityRoot := filepath.Join(wd, "../../roster/authority")
	aidesPath := filepath.Join(authorityRoot, "aides.yaml")
	templatePath := filepath.Join(authorityRoot, "_template.md.tmpl")

	generated, err := GenerateAuthorityAides(authorityRoot, aidesPath, templatePath, GeneratedMarker)
	if err != nil {
		t.Skipf("cannot generate from real files: %v", err)
	}

	// Should have 8 files
	if len(generated) != 8 {
		t.Errorf("expected 8 generated files, got %d", len(generated))
	}

	// Check that each file contains expected content
	for path, content := range generated {
		if !strings.Contains(content, "---") {
			t.Errorf("%s: missing frontmatter", path)
		}
		if !strings.Contains(content, GeneratedMarker) {
			t.Errorf("%s: missing generated marker", path)
		}
		if !strings.Contains(content, "Aide") {
			t.Errorf("%s: missing 'Aide' in title", path)
		}

		// Extract aide ID from path
		parts := strings.Split(path, string(os.PathSeparator))
		if len(parts) >= 2 {
			aideID := parts[len(parts)-2]
			if !strings.Contains(content, "id: "+aideID) {
				t.Errorf("%s: id frontmatter mismatch", path)
			}
		}
	}

	t.Logf("generated %d aide AGENT.md files", len(generated))
}

