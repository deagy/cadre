package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWrapper(t *testing.T, dir, name, model string) string {
	t.Helper()
	content := ProvenanceMarker + "\n[agent]\nname = \"x\"\nmodel = \"" + model + "\"\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSyncWrappersInstallsIntoEmptyTarget(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "agents")
	writeWrapper(t, source, "agents-role-a.toml", "sonnet")
	writeWrapper(t, source, "agents-role-b.toml", "opus")

	result, err := SyncWrappers(source, target)
	if err != nil {
		t.Fatalf("SyncWrappers: %v", err)
	}
	if len(result.Installed) != 2 {
		t.Errorf("installed = %v, want 2 entries", result.Installed)
	}
	if len(result.Unchanged) != 0 {
		t.Errorf("unchanged = %v, want none", result.Unchanged)
	}
	if result.IndexStatus != "installed" {
		t.Errorf("index_status = %q, want installed", result.IndexStatus)
	}
	data, err := os.ReadFile(filepath.Join(target, "agents-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index AgentsIndex
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Roles) != 2 {
		t.Errorf("index has %d roles, want 2", len(index.Roles))
	}
	roleA, ok := index.Roles["role-a"]
	if !ok {
		t.Fatal("expected role-a in the index")
	}
	if roleA.Model == nil || *roleA.Model != "sonnet" {
		t.Errorf("role-a model = %v, want sonnet", roleA.Model)
	}
}

func TestSyncWrappersRerunIsUnchanged(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "agents")
	writeWrapper(t, source, "agents-role-a.toml", "sonnet")

	if _, err := SyncWrappers(source, target); err != nil {
		t.Fatal(err)
	}
	result, err := SyncWrappers(source, target)
	if err != nil {
		t.Fatalf("second SyncWrappers: %v", err)
	}
	if len(result.Installed) != 0 {
		t.Errorf("expected an idempotent re-run to report nothing installed, got %v", result.Installed)
	}
	if len(result.Unchanged) != 1 {
		t.Errorf("unchanged = %v, want 1 entry", result.Unchanged)
	}
	if result.IndexStatus != "unchanged" {
		t.Errorf("index_status = %q, want unchanged", result.IndexStatus)
	}
}

func TestSyncWrappersRefusesToOverwriteUnownedFile(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "agents")
	writeWrapper(t, source, "agents-role-a.toml", "sonnet")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	// A hand-authored file with the same name, no provenance marker.
	if err := os.WriteFile(filepath.Join(target, "agents-role-a.toml"), []byte("hand written, not ours"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := SyncWrappers(source, target)
	if err == nil {
		t.Fatal("expected refusal to overwrite an unowned destination file")
	}
	if !strings.Contains(err.Error(), "unowned") {
		t.Errorf("error = %v, want mention of 'unowned'", err)
	}
	// The unowned file must be untouched.
	data, _ := os.ReadFile(filepath.Join(target, "agents-role-a.toml"))
	if string(data) != "hand written, not ours" {
		t.Error("expected the unowned file's content to survive the refused sync")
	}
}

func TestSyncWrappersOverwritesOwnedFileWithChangedContent(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "agents")
	writeWrapper(t, source, "agents-role-a.toml", "sonnet")
	if _, err := SyncWrappers(source, target); err != nil {
		t.Fatal(err)
	}

	// Change the source and re-sync.
	writeWrapper(t, source, "agents-role-a.toml", "opus")
	result, err := SyncWrappers(source, target)
	if err != nil {
		t.Fatalf("SyncWrappers: %v", err)
	}
	found := false
	for _, p := range result.Installed {
		if strings.HasSuffix(p, "agents-role-a.toml") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected agents-role-a.toml to be reported installed after a content change, got %v", result.Installed)
	}
	data, err := os.ReadFile(filepath.Join(target, "agents-role-a.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "opus") {
		t.Error("expected the destination to be updated to the new content")
	}
}

func TestSyncWrappersRefusesSymlinkedSource(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("symlink creation may be restricted in some CI sandboxes")
	}
	source := t.TempDir()
	outside := t.TempDir()
	realFile := filepath.Join(outside, "real.toml")
	if err := os.WriteFile(realFile, []byte(ProvenanceMarker), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile, filepath.Join(source, "agents-role-a.toml")); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}
	target := filepath.Join(t.TempDir(), "agents")
	_, err := SyncWrappers(source, target)
	if err == nil {
		t.Fatal("expected refusal of a symlinked source wrapper")
	}
}

func TestSyncWrappersRefusesSymlinkedDestination(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("symlink creation may be restricted in some CI sandboxes")
	}
	source := t.TempDir()
	writeWrapper(t, source, "agents-role-a.toml", "sonnet")
	target := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.toml")
	if err := os.WriteFile(outside, []byte("elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "agents-role-a.toml")); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}
	_, err := SyncWrappers(source, target)
	if err == nil {
		t.Fatal("expected refusal of a symlinked destination wrapper")
	}
}

func TestSyncWrappersErrorsOnEmptySource(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "agents")
	_, err := SyncWrappers(source, target)
	if err == nil {
		t.Fatal("expected an error when the source has no agents-*.toml wrappers")
	}
}

func TestRoleIDFromWrapperName(t *testing.T) {
	if got := roleIDFromWrapperName("agents-backend-engineer.toml"); got != "backend-engineer" {
		t.Errorf("got %q, want backend-engineer", got)
	}
}

func TestExtractModel(t *testing.T) {
	content := []byte("name = \"x\"\nmodel = \"opus\"\nother = 1\n")
	model, ok := extractModel(content)
	if !ok || model != "opus" {
		t.Errorf("model=%q ok=%v, want opus/true", model, ok)
	}
}

func TestExtractModelReturnsFalseWhenAbsent(t *testing.T) {
	if _, ok := extractModel([]byte("name = \"x\"\n")); ok {
		t.Error("expected no model to be found")
	}
}

func TestIndexIsOnlyWrittenAfterAllWrappersSucceed(t *testing.T) {
	// If one wrapper write is refused (unowned collision), the index must
	// not be created/updated at all -- verifying the "only build the index
	// once every wrapper write has succeeded" ordering.
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "agents")
	writeWrapper(t, source, "agents-role-a.toml", "sonnet")
	writeWrapper(t, source, "agents-role-b.toml", "opus")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "agents-role-b.toml"), []byte("unowned"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := SyncWrappers(source, target)
	if err == nil {
		t.Fatal("expected an error from the unowned collision")
	}
	if _, statErr := os.Stat(filepath.Join(target, "agents-index.json")); statErr == nil {
		t.Error("expected agents-index.json to not exist when a wrapper write failed")
	}
}
