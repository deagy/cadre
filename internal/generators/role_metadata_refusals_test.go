package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What the role-metadata loader refuses, and why each refusal is not
// pedantry.
//
// Every field below ends up in `catalog.yaml`, which the selector reads to
// decide who is dispatched to a task and with what model, tools and sandbox.
// A role missing one does not fail loudly at dispatch time -- it produces a
// catalog entry with a blank where a decision should be, and the first sign is
// a specialist running with the wrong tier or not being routed to at all.
//
// So the loader refuses at generation time. These exercise those refusals,
// which nothing did: Go had tests for the happy path and for the real roster,
// and the error branches were reachable only by breaking the real one.
//
// Ported from roster/orchestration/test/test_role_metadata.py, the last of the
// four guards that kept the Python generators alive after the Go CLI replaced
// them. That file tested the Python generator directly, so it goes with it;
// what it *checked* is here.

// roleFrontmatter writes an AGENT.md with the given frontmatter fields.
func roleFrontmatter(t *testing.T, directory string, fields map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	var builder strings.Builder
	builder.WriteString("---\n")
	// Written in a fixed order so a failure message is stable between runs.
	for _, key := range []string{
		"id", "phase", "capability", "model", "codex_model",
		"reasoning_effort", "knowledge_focus",
	} {
		if value, present := fields[key]; present {
			builder.WriteString(key + ": " + value + "\n")
		}
	}
	builder.WriteString("---\n\nA role.\n")
	path := filepath.Join(directory, "AGENT.md")
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func completeRole() map[string]string {
	return map[string]string{
		"id": "sample-role", "phase": "engineering", "capability": "code_author",
		"model": "sonnet", "codex_model": "gpt-5.6-terra",
		"reasoning_effort": "medium", "knowledge_focus": "sample",
	}
}

func TestARoleMissingAnyRequiredFieldIsRefused(t *testing.T) {
	// One case per field, because the whole point is that *each* is required.
	// A loop over a complete role with one key removed is the only shape that
	// cannot pass by checking the same field twice.
	directory := t.TempDir()
	path := roleFrontmatter(t, filepath.Join(directory, "sample-role"), completeRole())
	if _, err := LoadRoleMetadata(path, "engineering/sample-role/AGENT.md"); err != nil {
		t.Fatalf("a complete role was refused, so every case below would pass "+
			"for the wrong reason: %v", err)
	}

	for field := range completeRole() {
		t.Run("without "+field, func(t *testing.T) {
			fields := completeRole()
			delete(fields, field)
			path := roleFrontmatter(t, filepath.Join(t.TempDir(), "sample-role"), fields)
			_, err := LoadRoleMetadata(path, "engineering/sample-role/AGENT.md")
			if err == nil {
				t.Fatalf("a role with no %s was accepted", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("the refusal does not name %s: %v", field, err)
			}
		})
	}
}

func TestFrontmatterThatIsNotFrontmatterIsRefused(t *testing.T) {
	// A file whose delimiters are missing or unclosed parses as *something* if
	// the reader is lenient -- usually as a role with no fields at all, which
	// then fails somewhere less obvious.
	for _, probe := range []struct {
		name, content, wants string
	}{
		{"no opening delimiter", "id: sample\n---\n\nbody\n", "opening delimiter"},
		{"no closing delimiter", "---\nid: sample\n\nbody\n", "closing delimiter"},
		{"nothing at all", "", "delimiter"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "sample-role")
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "AGENT.md")
			if err := os.WriteFile(path, []byte(probe.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadRoleMetadata(path, "engineering/sample-role/AGENT.md")
			if err == nil {
				t.Fatalf("accepted a file with no usable frontmatter:\n%q", probe.content)
			}
			if !strings.Contains(err.Error(), probe.wants) {
				t.Errorf("refused for a different reason than this case is about.\n"+
					"wanted something naming %q, got: %v", probe.wants, err)
			}
		})
	}
}

func TestACatalogOrderNamingARoleWithNoFileIsRefused(t *testing.T) {
	// catalog-order.txt is hand-maintained and the roles are directories, so
	// the two drift when a role is renamed. Carrying on would drop the role
	// from the catalog silently -- the selector would simply never route to
	// it again.
	rosterRoot := t.TempDir()
	roleFrontmatter(t, filepath.Join(rosterRoot, "engineering", "present-role"),
		map[string]string{
			"id": "present-role", "phase": "engineering", "capability": "code_author",
			"model": "sonnet", "codex_model": "gpt-5.6-terra",
			"reasoning_effort": "medium", "knowledge_focus": "sample",
		})

	discovered, err := DiscoverRoles(rosterRoot)
	if err != nil {
		t.Fatalf("discovering roles: %v", err)
	}
	tiers := map[string]ModelTierInfo{
		"sonnet": {CodexModel: "gpt-5.6-terra", ReasoningEffort: "medium"},
	}

	// The role that exists loads.
	if _, err := LoadAllRoles(rosterRoot, []string{"present-role"}, discovered, tiers); err != nil {
		t.Fatalf("a role that exists was refused: %v", err)
	}
	// The one that does not is named, rather than skipped.
	_, err = LoadAllRoles(rosterRoot, []string{"present-role", "renamed-away"}, discovered, tiers)
	if err == nil {
		t.Fatal("an ordered role with no AGENT.md was accepted")
	}
	if !strings.Contains(err.Error(), "renamed-away") {
		t.Errorf("the refusal does not name the missing role: %v", err)
	}
}

func TestARoleWhoseModelTierTheManifestDoesNotDefineIsRefused(t *testing.T) {
	// The tier decides the model, the reasoning effort and -- through the
	// capability profile -- the tools and sandbox. A role naming one nothing
	// defines is a role nobody can render a wrapper for.
	tiers := map[string]ModelTierInfo{
		"sonnet": {CodexModel: "gpt-5.6-terra", ReasoningEffort: "medium"},
	}
	if err := ValidateModelTier("sonnet", "gpt-5.6-terra", "medium", tiers); err != nil {
		t.Fatalf("a declared tier was refused: %v", err)
	}
	for _, probe := range []struct {
		name, model, codexModel, effort string
	}{
		{"a model tier nothing defines", "experimental", "gpt-5.6-terra", "medium"},
		{"a codex model the tier does not map to", "sonnet", "gpt-somethingelse", "medium"},
		{"a reasoning effort the tier does not carry", "sonnet", "gpt-5.6-terra", "extreme"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			if err := ValidateModelTier(probe.model, probe.codexModel, probe.effort, tiers); err == nil {
				t.Errorf("accepted model=%q codex=%q effort=%q",
					probe.model, probe.codexModel, probe.effort)
			}
		})
	}
}
