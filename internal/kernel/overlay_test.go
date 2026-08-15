package kernel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The overlay foundation `validate` will be built on. Everything here decides
// what the kernel reads and where from, so the tests are mostly about what it
// refuses.

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAConfinedPathRefusesToLeaveItsRoot(t *testing.T) {
	// The kernel builds paths from task ids and directory listings. Without
	// this, a task id of "../../elsewhere" reads and writes outside the
	// project entirely.
	root := t.TempDir()

	inside, err := ConfinedPath(root, Overlay, "runs", "TASK-1", "run-record.json")
	if err != nil {
		t.Errorf("an ordinary project path was refused: %v", err)
	}
	if !strings.HasPrefix(inside, root) {
		t.Errorf("resolved to %q, which is outside %q", inside, root)
	}

	for _, escape := range [][]string{
		{".."},
		{Overlay, "..", "..", "elsewhere"},
		{"runs", "../../..", "etc", "passwd"},
	} {
		if _, err := ConfinedPath(root, escape...); err == nil {
			t.Errorf("%v was accepted", escape)
		}
	}
}

func TestAConfinedPathResolvesSymlinksBeforeCheckingContainment(t *testing.T) {
	// The check that matters. A path that *looks* contained can resolve
	// somewhere else entirely, and checking the unresolved form proves
	// nothing about where a read or write lands.
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	root := t.TempDir()
	elsewhere := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, Overlay), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, Overlay, "runs")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	if _, err := ConfinedPath(root, Overlay, "runs", "TASK-1"); err == nil {
		t.Error("a path through a symlink pointing outside the root was accepted")
	}
}

func TestAPathThatDoesNotExistYetIsStillResolved(t *testing.T) {
	// The kernel builds paths for files it is about to create. Refusing to
	// reason about them would make the check unusable exactly where writes
	// happen -- and the directories those files land in do exist, which is
	// where a swapped symlink would redirect the write.
	root := t.TempDir()
	path, err := ConfinedPath(root, Overlay, "runs", "TASK-NEW", "run-record.json")
	if err != nil {
		t.Fatalf("a not-yet-created path was refused: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected the path not to exist yet: %v", err)
	}
}

func TestLoadOverlayReadsAllFourDocuments(t *testing.T) {
	root := t.TempDir()
	overlay := filepath.Join(root, Overlay)
	writeJSON(t, filepath.Join(overlay, "project.json"), map[string]any{"profile": "quick"})
	writeJSON(t, filepath.Join(overlay, "authorities.json"), map[string]any{"product_owner": map[string]any{}})
	writeJSON(t, filepath.Join(overlay, "impact-profile.json"), map[string]any{"impact_categories": []any{}})
	writeJSON(t, filepath.Join(overlay, "routing.json"), map[string]any{"routes": []any{}})

	loaded, err := LoadOverlay(root)
	if err != nil {
		t.Fatalf("LoadOverlay: %v", err)
	}
	if loaded.Project["profile"] != "quick" {
		t.Errorf("project.json was not read: %v", loaded.Project)
	}
	if loaded.Authorities == nil || loaded.Impact == nil || loaded.Routing == nil {
		t.Error("not every overlay document was read")
	}

	// A missing document is an error, not an empty object. Treating an absent
	// authorities.json as "no authorities" would make an uninitialised
	// project look like one with nothing to approve.
	if err := os.Remove(filepath.Join(overlay, "authorities.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOverlay(root); err == nil {
		t.Error("a missing overlay document was treated as empty")
	}
}

func TestAnInvalidApprovalSourceIsAnErrorNotADefault(t *testing.T) {
	// The failure this refuses to make quiet: a project that meant to require
	// GitHub review and misspelled it would otherwise fall back to manual and
	// accept approvals it was configured to reject.
	for _, invalid := range []any{
		map[string]any{"human_gate_default": "github_review"}, // underscore, not hyphen
		map[string]any{"human_gate_default": "GitHub-Review"},
		map[string]any{"human_gate_default": ""},
		map[string]any{"human_gate_default": 1},
		map[string]any{"allow_manual_fallback": "yes"},
		"not an object",
	} {
		if _, err := ApprovalSourcePolicy(map[string]any{"approval_sources": invalid}); err == nil {
			t.Errorf("%v was accepted", invalid)
		}
	}

	// Absent means manual with a fallback -- the permissive reading, but an
	// explicit one rather than an accident.
	policy, err := ApprovalSourcePolicy(map[string]any{})
	if err != nil {
		t.Fatalf("an absent policy was refused: %v", err)
	}
	if policy.HumanGateDefault != "manual" || !policy.AllowManualFallback {
		t.Errorf("default policy = %+v", policy)
	}

	// And the strict combination survives: GitHub review with no fallback.
	strict, err := ApprovalSourcePolicy(map[string]any{
		"approval_sources": map[string]any{
			"human_gate_default": "github-review", "allow_manual_fallback": false,
		},
	})
	if err != nil {
		t.Fatalf("a valid strict policy was refused: %v", err)
	}
	if strict.HumanGateDefault != "github-review" || strict.AllowManualFallback {
		t.Errorf("strict policy = %+v", strict)
	}
}

func TestAnAgentDefinitionCannotEscapeItsProviderRoot(t *testing.T) {
	// A catalog entry naming ../../ is a provider pointing the kernel at a
	// file outside anything it declared.
	providerRoot := t.TempDir()
	catalogPath := filepath.Join(providerRoot, "agent-catalog.json")

	writeJSON(t, catalogPath, map[string]any{
		"schema_version": 1,
		"agents": map[string]any{
			"escaping-agent": map[string]any{
				"kind": "author", "capabilities": []any{"author"},
				"definition": "../../../etc/passwd",
			},
		},
	})
	registry := &Registry{CatalogRoots: []string{catalogPath}, kernelVersionOf: Version}
	if _, err := registry.LoadAgentCatalog(); err == nil {
		t.Error("an agent definition escaping the provider root was accepted")
	}

	// A relative definition inside the root is resolved to an absolute path,
	// so a later reader does not re-resolve it against a different directory.
	definition := filepath.Join(providerRoot, "roles", "agent.md")
	if err := os.MkdirAll(filepath.Dir(definition), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(definition, []byte("role"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, catalogPath, map[string]any{
		"schema_version": 1,
		"agents": map[string]any{
			"ordinary-agent": map[string]any{
				"kind": "author", "capabilities": []any{"author"},
				"definition": "roles/agent.md",
			},
		},
	})
	merged, err := registry.LoadAgentCatalog()
	if err != nil {
		t.Fatalf("an ordinary catalog was refused: %v", err)
	}
	agent := merged["ordinary-agent"].(map[string]any)
	resolved := agent["definition"].(string)
	if !filepath.IsAbs(resolved) {
		t.Errorf("definition %q was not resolved to an absolute path", resolved)
	}
}

func TestATimestampWithoutAnOffsetIsNotValid(t *testing.T) {
	// The offset is the requirement, not the format. A run record's
	// timestamps are evidence of when a gate was decided; one without an
	// offset means whatever the reader's local zone happens to be, which
	// makes an audit trail that says different things to different people.
	//
	// The accepted set matches the Python kernel's, confirmed by running both
	// against the same fourteen inputs -- including the space separator its
	// datetime.fromisoformat allows and RFC3339 does not.
	for _, valid := range []string{
		"2026-08-15T12:00:00Z",
		"2026-08-15T12:00:00+01:00",
		"2026-08-15T12:00:00-05:00",
		"2026-08-15T12:00:00.123456Z",
		"2026-08-15 12:00:00+00:00",
	} {
		if !IsValidDatetime(valid) {
			t.Errorf("%q was rejected", valid)
		}
	}
	for _, invalid := range []any{
		"2026-08-15T12:00:00", // no offset
		"2026-08-15 12:00:00", // no offset, space form
		"2026-08-15",
		"not a date",
		"",
		"2026-13-45T99:99:99Z",
		42, nil, true,
	} {
		if IsValidDatetime(invalid) {
			t.Errorf("%v was accepted", invalid)
		}
	}
}
