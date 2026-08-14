package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// `cadre select` dispatches to roster/orchestration/src/select_agents.py
// (see SelectAgents' doc comment for why), so the behaviour worth pinning
// here is the dispatch itself: which copy of the selector is chosen, and
// that a missing one fails with a pointer rather than a panic or a silent
// success. Flag parsing, defaults, the plan's shape and its byte-level
// output contract belong to the selector and are covered where that
// contract lives -- roster/orchestration/test/test_selector.py and
// test_repository_health.py's wrapper-parity cases.

func writeSelector(t *testing.T, root string) string {
	t.Helper()
	script := filepath.Join(root, filepath.FromSlash(SelectorScriptRelativePath))
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(script, []byte("# stand-in selector\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return script
}

func TestFindCadreFilePrefersCadreRepoRoot(t *testing.T) {
	root := t.TempDir()
	script := writeSelector(t, root)
	t.Setenv("CADRE_REPO_ROOT", root)

	found, err := FindCadreFile(SelectorScriptRelativePath)
	if err != nil {
		t.Fatalf("FindCadreFile: %v", err)
	}
	if found != script {
		t.Errorf("FindCadreFile() = %q, want %q", found, script)
	}
}

func TestFindCadreFileWalksUpFromWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	script := writeSelector(t, root)
	// An empty value must not be mistaken for "a root was configured".
	t.Setenv("CADRE_REPO_ROOT", "")

	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Chdir(nested)

	found, err := FindCadreFile(SelectorScriptRelativePath)
	if err != nil {
		t.Fatalf("FindCadreFile: %v", err)
	}
	// The temporary directory may itself be reached through a symlink
	// (/tmp -> /private/tmp on macOS), so compare resolved paths.
	if resolvedFound, resolvedScript := resolve(t, found), resolve(t, script); resolvedFound != resolvedScript {
		t.Errorf("FindCadreFile() = %q, want %q", resolvedFound, resolvedScript)
	}
}

func TestFindCadreFileReportsAMissingRoster(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("CADRE_REPO_ROOT", empty)
	t.Chdir(empty)

	if _, err := FindCadreFile("roster/does/not/exist.py"); err == nil {
		t.Fatal("FindCadreFile() succeeded for a path that exists nowhere")
	}
}

func TestSelectAgentsFailsClosedWithoutARoster(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("CADRE_REPO_ROOT", empty)
	t.Chdir(empty)

	if code := SelectAgents(nil); code != 1 {
		t.Errorf("SelectAgents() = %d, want 1 when the selector cannot be located", code)
	}
}

func resolve(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}
