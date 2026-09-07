package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindProjectOverlay(t *testing.T) {
	dir := makeGitCheckout(t)
	writeFile(t, filepath.Join(dir, ".agents", "shared", "agent-autonomy.yaml"), "x: 1\n")

	found, ok := FindProjectOverlay("agent-autonomy.yaml", dir)
	if !ok {
		t.Fatal("expected to find the overlay")
	}
	if found != filepath.Join(dir, ".agents", "shared", "agent-autonomy.yaml") {
		t.Errorf("found = %q", found)
	}
}

func TestFindProjectOverlayNoneFound(t *testing.T) {
	dir := makeGitCheckout(t)
	_, ok := FindProjectOverlay("agent-autonomy.yaml", dir)
	if ok {
		t.Fatal("expected no overlay found")
	}
}

func TestResolveSharedConfigNoOverlayStructured(t *testing.T) {
	sharedDir := t.TempDir()
	writeFile(t, filepath.Join(sharedDir, "some-policy.yaml"), "a: 1\nb: 2\n")
	dir := makeGitCheckout(t)

	result, err := ResolveSharedConfig(sharedDir, "some-policy.yaml", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsText {
		t.Fatal("expected a structured result")
	}
	if result.Structured["a"] != 1 || result.Structured["b"] != 2 {
		t.Errorf("Structured = %v", result.Structured)
	}
}

func TestResolveSharedConfigMergesOverlay(t *testing.T) {
	sharedDir := t.TempDir()
	writeFile(t, filepath.Join(sharedDir, "some-policy.yaml"), "a: 1\nnested:\n  x: base\n")
	dir := makeGitCheckout(t)
	writeFile(t, filepath.Join(dir, ".agents", "shared", "some-policy.yaml"), "b: 2\nnested:\n  y: overlay\n")

	result, err := ResolveSharedConfig(sharedDir, "some-policy.yaml", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Structured["a"] != 1 || result.Structured["b"] != 2 {
		t.Errorf("Structured = %v", result.Structured)
	}
	nested := result.Structured["nested"].(map[string]any)
	if nested["x"] != "base" || nested["y"] != "overlay" {
		t.Errorf("nested = %v", nested)
	}
}

func TestResolveSharedConfigMarkdownAddendum(t *testing.T) {
	sharedDir := t.TempDir()
	writeFile(t, filepath.Join(sharedDir, "policy.md"), "# Base policy\n")
	dir := makeGitCheckout(t)
	writeFile(t, filepath.Join(dir, ".agents", "shared", "policy.md"), "Extra project rule.\n")

	result, err := ResolveSharedConfig(sharedDir, "policy.md", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsText {
		t.Fatal("expected a text result")
	}
	if !containsSubstr(result.Text, "# Base policy") || !containsSubstr(result.Text, "Extra project rule.") {
		t.Errorf("Text = %q", result.Text)
	}
}

func TestResolveSharedConfigMarkdownNoOverlay(t *testing.T) {
	sharedDir := t.TempDir()
	writeFile(t, filepath.Join(sharedDir, "policy.md"), "# Base policy\n")
	dir := makeGitCheckout(t)

	result, err := ResolveSharedConfig(sharedDir, "policy.md", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "# Base policy\n" {
		t.Errorf("Text = %q, want the base text unchanged", result.Text)
	}
}

func TestResolveSharedConfigMissingDefault(t *testing.T) {
	sharedDir := t.TempDir()
	dir := makeGitCheckout(t)
	_, err := ResolveSharedConfig(sharedDir, "does-not-exist.yaml", dir)
	if err == nil {
		t.Fatal("expected an error for a missing shared default")
	}
}

func TestCheckAutonomyOverlayAllowsNarrowing(t *testing.T) {
	base := map[string]any{"category": map[string]any{"action": "allowed"}}
	overlay := map[string]any{"category": map[string]any{"action": "human_approval"}}
	if err := checkAutonomyOverlay(base, overlay); err != nil {
		t.Errorf("narrowing (allowed -> human_approval) must be permitted: %v", err)
	}
}

func TestCheckAutonomyOverlayRejectsLoosening(t *testing.T) {
	base := map[string]any{"category": map[string]any{"action": "human_approval"}}
	overlay := map[string]any{"category": map[string]any{"action": "allowed"}}
	if err := checkAutonomyOverlay(base, overlay); err == nil {
		t.Fatal("expected rejection of loosening (human_approval -> allowed)")
	}
}

func TestCheckAutonomyOverlayRejectsUndefinedKey(t *testing.T) {
	base := map[string]any{"category": map[string]any{"action": "allowed"}}
	overlay := map[string]any{"category": map[string]any{"unknown_action": "never"}}
	if err := checkAutonomyOverlay(base, overlay); err == nil {
		t.Fatal("expected rejection of an undefined key")
	}
}

func TestCheckAutonomyOverlayRejectsFixedKeys(t *testing.T) {
	base := map[string]any{"policy_version": "1", "category": map[string]any{"action": "allowed"}}
	overlay := map[string]any{"policy_version": "2"}
	if err := checkAutonomyOverlay(base, overlay); err == nil {
		t.Fatal("expected rejection of an overlay touching policy_version")
	}
}

func TestCheckAutonomyOverlayRejectsUnrecognizedValue(t *testing.T) {
	base := map[string]any{"category": map[string]any{"action": "allowed"}}
	overlay := map[string]any{"category": map[string]any{"action": "sort-of-allowed"}}
	if err := checkAutonomyOverlay(base, overlay); err == nil {
		t.Fatal("expected rejection of an unrecognized permission value")
	}
}

func TestCheckAutonomyOverlayAllowsNoOpRestatement(t *testing.T) {
	base := map[string]any{"category": map[string]any{"action": "allowed"}}
	overlay := map[string]any{"category": map[string]any{"action": "allowed"}}
	if err := checkAutonomyOverlay(base, overlay); err != nil {
		t.Errorf("unexpected rejection of a no-op restatement: %v", err)
	}
}

func TestResolveSharedConfigAutonomyRejectsLoosening(t *testing.T) {
	sharedDir := t.TempDir()
	writeFile(t, filepath.Join(sharedDir, autonomyFilename), "category:\n  action: human_approval\n")
	dir := makeGitCheckout(t)
	writeFile(t, filepath.Join(dir, ".agents", "shared", autonomyFilename), "category:\n  action: allowed\n")

	_, err := ResolveSharedConfig(sharedDir, autonomyFilename, dir)
	if err == nil {
		t.Fatal("expected ResolveSharedConfig to enforce the narrowing-only rule for agent-autonomy.yaml")
	}
}

func containsSubstr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// --- Symlink escape guard tests ---

func TestRejectSymlinkEscapeOnReadWithDepthCatchesFileEscape(t *testing.T) {
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "evil.yaml")
	os.WriteFile(outsideFile, []byte("evil_config: true\n"), 0o644)

	project := makeGitCheckout(t)
	agentsDir := filepath.Join(project, ".agents", "shared")
	os.MkdirAll(agentsDir, 0o755)
	symlinkPath := filepath.Join(agentsDir, "agent-autonomy.yaml")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	_, err := rejectSymlinkEscapeOnReadWithDepth(symlinkPath, 3)
	if err == nil {
		t.Fatal("expected rejection of a symlink pointing outside the project root")
	}
}

func TestRejectSymlinkEscapeOnReadWithDepthCatchesDirectoryEscape(t *testing.T) {
	outside := t.TempDir()
	outsideDir := filepath.Join(outside, "evil-dir")
	os.MkdirAll(outsideDir, 0o755)

	project := makeGitCheckout(t)
	agentsDir := filepath.Join(project, ".agents")
	os.MkdirAll(agentsDir, 0o755)
	symlinkPath := filepath.Join(agentsDir, "shared")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	// Try to access a file through the symlinked directory
	evilFile := filepath.Join(symlinkPath, "agent-autonomy.yaml")
	os.WriteFile(filepath.Join(outsideDir, "agent-autonomy.yaml"), []byte("evil: true\n"), 0o644)

	_, err := rejectSymlinkEscapeOnReadWithDepth(evilFile, 3)
	if err == nil {
		t.Fatal("expected rejection of a file accessed through a symlinked intermediate directory")
	}
}

func TestResolveSharedConfigRejectsSymlinkedYAMLOverlay(t *testing.T) {
	sharedDir := t.TempDir()
	writeFile(t, filepath.Join(sharedDir, "some-policy.yaml"), "a: 1\n")

	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "evil.yaml")
	os.WriteFile(outsideFile, []byte("a: evil\n"), 0o644)

	project := makeGitCheckout(t)
	agentsDir := filepath.Join(project, ".agents", "shared")
	os.MkdirAll(agentsDir, 0o755)
	symlinkPath := filepath.Join(agentsDir, "some-policy.yaml")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	_, err := ResolveSharedConfig(sharedDir, "some-policy.yaml", project)
	if err == nil {
		t.Fatal("expected ResolveSharedConfig to reject a symlinked overlay pointing outside the project")
	}
}

func TestResolveSharedConfigRejectsSymlinkedMarkdownOverlay(t *testing.T) {
	sharedDir := t.TempDir()
	writeFile(t, filepath.Join(sharedDir, "policy.md"), "# Base policy\n")

	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "evil.md")
	os.WriteFile(outsideFile, []byte("# Evil content\n"), 0o644)

	project := makeGitCheckout(t)
	agentsDir := filepath.Join(project, ".agents", "shared")
	os.MkdirAll(agentsDir, 0o755)
	symlinkPath := filepath.Join(agentsDir, "policy.md")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	_, err := ResolveSharedConfig(sharedDir, "policy.md", project)
	if err == nil {
		t.Fatal("expected ResolveSharedConfig to reject a symlinked Markdown overlay pointing outside the project")
	}
}

func TestResolveSharedConfigAllowsLegitimateOverlay(t *testing.T) {
	// Regression test: ensure the symlink guard doesn't break normal overlay resolution
	sharedDir := t.TempDir()
	writeFile(t, filepath.Join(sharedDir, "some-policy.yaml"), "a: 1\n")
	dir := makeGitCheckout(t)
	writeFile(t, filepath.Join(dir, ".agents", "shared", "some-policy.yaml"), "b: 2\n")

	result, err := ResolveSharedConfig(sharedDir, "some-policy.yaml", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsText {
		if result.Structured["a"] != 1 || result.Structured["b"] != 2 {
			t.Errorf("Structured = %v, expected a=1, b=2", result.Structured)
		}
	}
}
