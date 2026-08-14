package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderCatalog(t *testing.T) {
	header := "# Generated catalog\nagents:\n"
	roles := []RoleMetadata{
		{
			ID:              "product-intent-agent",
			Definition:      "roster/planning/product-intent-agent/AGENT.md",
			Phase:           "planning",
			Capability:      "document_author",
			Model:           "sonnet",
			CodexModel:      "gpt-5.6-terra",
			ReasoningEffort: "medium",
			KnowledgeFocus:  "product objectives",
		},
		{
			ID:              "backend-engineer",
			Definition:      "roster/build/backend-engineer/AGENT.md",
			Phase:           "build",
			Capability:      "code_author",
			Model:           "sonnet",
			CodexModel:      "gpt-5.6-terra",
			ReasoningEffort: "medium",
			KnowledgeFocus:  "backend implementation",
		},
	}

	catalog, err := RenderCatalog(roles, header)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	// Check structure
	checks := map[string]bool{
		"agents:":                     false,
		"product-intent-agent:":       false,
		"definition: roster/planning": false,
		"phase: planning":             false,
		"model: sonnet":               false,
		"backend-engineer:":           false,
	}

	for check := range checks {
		if strings.Contains(catalog, check) {
			checks[check] = true
		}
	}

	for check, found := range checks {
		if !found {
			t.Errorf("missing expected content: %q", check)
		}
	}

	// Check ordering
	pos1 := strings.Index(catalog, "product-intent-agent:")
	pos2 := strings.Index(catalog, "backend-engineer:")
	if pos1 >= pos2 {
		t.Errorf("entries not in correct order")
	}
}

func TestLoadCatalogHeader(t *testing.T) {
	wd, _ := os.Getwd()
	headerPath := filepath.Join(wd, "../../roster/_catalog_header.yaml.tmpl")

	header, err := LoadCatalogHeader(headerPath)
	if err != nil {
		t.Skipf("cannot load header: %v", err)
	}

	if header == "" {
		t.Errorf("header is empty")
	}

	// Should contain YAML comment structure
	if !strings.Contains(header, "#") {
		t.Errorf("header doesn't contain expected comments")
	}
}

func TestRenderCatalogCarriesTheAuthorityBlockComment(t *testing.T) {
	roles := []RoleMetadata{{
		ID:              "product-owner-aide",
		Definition:      "authority/product-owner-aide/AGENT.md",
		Phase:           "authority",
		Capability:      "read_only",
		Model:           "opus",
		CodexModel:      "gpt-5.6-sol",
		ReasoningEffort: "high",
	}}
	catalog, err := RenderCatalog(roles, "agents:\n")
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.Contains(catalog, "# `phase: authority` roles below prepare the decision package") {
		t.Errorf("the hand-authored authority-block comment was dropped:\n%s", catalog)
	}
	if strings.Index(catalog, "# `phase: authority`") > strings.Index(catalog, "  product-owner-aide:") {
		t.Errorf("the authority-block comment must precede its role block")
	}
}

func TestSpliceKnowledgeFocusIsAByteExactNoOpOnAnUnchangedRoleSet(t *testing.T) {
	original := strings.Join([]string{
		"{",
		`  "routes": [`,
		`    {"id": "keep-me"}`,
		"  ],",
		`  "knowledge_focus": {`,
		`    "beta": "second role, listed first on purpose",`,
		`    "alpha": "first role"`,
		"  },",
		`  "teams": {}`,
		"}",
		"",
	}, "\n")

	// Deliberately supplied in a different order than routing.json lists them:
	// the splice must preserve each existing row's position rather than
	// reordering the file to match catalog-order.txt.
	roles := []RoleMetadata{
		{ID: "alpha", KnowledgeFocus: "first role"},
		{ID: "beta", KnowledgeFocus: "second role, listed first on purpose"},
	}

	spliced, err := SpliceKnowledgeFocus(original, roles)
	if err != nil {
		t.Fatalf("splice failed: %v", err)
	}
	if spliced != original {
		t.Errorf("splice was not byte-exact:\ngot:\n%s\nwant:\n%s", spliced, original)
	}
}

func TestSpliceKnowledgeFocusAppendsNewRolesAndLeavesSiblingsAlone(t *testing.T) {
	original := strings.Join([]string{
		"{",
		`  "routes": [`,
		`    {"id": "keep-me"}`,
		"  ],",
		`  "knowledge_focus": {`,
		`    "alpha": "first role"`,
		"  }",
		"}",
		"",
	}, "\n")

	roles := []RoleMetadata{
		{ID: "alpha", KnowledgeFocus: "first role"},
		{ID: "gamma", KnowledgeFocus: "a newly added role"},
	}

	spliced, err := SpliceKnowledgeFocus(original, roles)
	if err != nil {
		t.Fatalf("splice failed: %v", err)
	}
	if !strings.Contains(spliced, `"gamma": "a newly added role"`) {
		t.Errorf("new role was not appended:\n%s", spliced)
	}
	if !strings.Contains(spliced, `    {"id": "keep-me"}`) {
		t.Errorf("splice disturbed an unrelated region:\n%s", spliced)
	}
	if strings.Index(spliced, `"alpha"`) > strings.Index(spliced, `"gamma"`) {
		t.Errorf("an existing row must keep its position ahead of an appended one")
	}
}

func TestSpliceKnowledgeFocusRejectsAMissingAnchor(t *testing.T) {
	if _, err := SpliceKnowledgeFocus(`{"routes": []}`, nil); err == nil {
		t.Fatal("expected a failure when routing.json has no knowledge_focus anchor")
	}
}

func TestPyJSONStringUnicodeLeavesNonASCIIAlone(t *testing.T) {
	if got := pyJSONStringUnicode("an em — dash"); got != `"an em — dash"` {
		t.Errorf("pyJSONStringUnicode = %s", got)
	}
	if got := pyJSONStringUnicode("quote \" and \\ slash"); got != `"quote \" and \\ slash"` {
		t.Errorf("pyJSONStringUnicode = %s", got)
	}
}
