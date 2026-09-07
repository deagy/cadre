package contextstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaultsWhenNoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// Explicit path must exist -- fail closed.
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("expected an error for a nonexistent explicit config path")
	}
}

func TestLoadConfigExplicitPathAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, tier, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tier != TierExplicitConfig {
		t.Errorf("tier = %q, want %q", tier, TierExplicitConfig)
	}
	if cfg.Embedding.Provider != "hashing" {
		t.Errorf("embedding.provider = %q, want hashing", cfg.Embedding.Provider)
	}
	if cfg.Expiry.MaximumTTLDays != 90 {
		t.Errorf("expiry.maximum_ttl_days = %d, want 90", cfg.Expiry.MaximumTTLDays)
	}
	if cfg.Expiry.DefaultTTLDaysByScope["agent"] != 1 {
		t.Errorf("expiry.default_ttl_days_by_scope.agent = %d, want 1", cfg.Expiry.DefaultTTLDaysByScope["agent"])
	}
}

func TestLoadConfigRejectsNonHashingProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"embedding": {"provider": "openai-compatible"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("expected rejection of a non-hashing embedding provider (OD-5)")
	}
}

func TestLoadConfigRejectsTTLExceedingMaximum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"expiry": {"default_ttl_days_by_scope": {"agent": 1, "dispatch": 7, "project": 200}, "maximum_ttl_days": 90}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("expected rejection when a scope's default TTL exceeds the maximum")
	}
}

func TestLoadConfigRejectsUnknownScope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"expiry": {"default_ttl_days_by_scope": {"agent": 1, "dispatch": 7, "project": 30, "bogus": 5}, "maximum_ttl_days": 90}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("expected rejection of an unknown scope key")
	}
}

func TestLoadConfigRejectsChunkOverlapNotSmallerThanMax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"chunking": {"max_characters": 100, "overlap_characters": 100}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("expected rejection when overlap_characters >= max_characters")
	}
}

func TestLoadConfigResolvesRelativeDatabaseAgainstConfigDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"database": "./data/context.db"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, "data", "context.db")
	if cfg.Database != want {
		t.Errorf("database = %q, want %q", cfg.Database, want)
	}
}

func TestFindProjectLocalConfigWalksToGitBoundary(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(dir, ".agents", "context-store")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	found, ok := FindProjectLocalConfig(nested)
	if !ok {
		t.Fatal("expected to find the project-local config")
	}
	if found != configPath {
		t.Errorf("found = %q, want %q", found, configPath)
	}
}

func TestLoadConfigRejectsProjectLocalDatabaseEscapingConfigDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(dir, ".agents", "context-store")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	// Escapes the config directory via a relative "../../.." path.
	if err := os.WriteFile(configPath, []byte(`{"database": "../../../../etc/escaped.db"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if _, _, err := LoadConfig(""); err == nil {
		t.Fatal("expected rejection of a project-local database path escaping its config directory")
	}
}

// --- Symlink escape guard tests ---

func TestLoadConfigRejectsSymlinkedProjectLocalFile(t *testing.T) {
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "evil.json")
	os.WriteFile(outsideFile, []byte("{}"), 0o644)

	project := t.TempDir()
	gitDir := filepath.Join(project, ".git")
	os.Mkdir(gitDir, 0o755)
	agentsDir := filepath.Join(project, ".agents", "context-store")
	os.MkdirAll(agentsDir, 0o755)
	symlinkPath := filepath.Join(agentsDir, "config.json")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	t.Chdir(project)
	_, _, err := LoadConfig("")
	if err == nil {
		t.Fatal("expected rejection of a symlinked project-local config pointing outside the project root")
	}
}

func TestLoadConfigRejectsSymlinkedProjectLocalDirectory(t *testing.T) {
	outside := t.TempDir()
	outsideDir := filepath.Join(outside, "evil-dir")
	os.MkdirAll(outsideDir, 0o755)
	os.WriteFile(filepath.Join(outsideDir, "config.json"), []byte("{}"), 0o644)

	project := t.TempDir()
	gitDir := filepath.Join(project, ".git")
	os.Mkdir(gitDir, 0o755)
	agentsDir := filepath.Join(project, ".agents")
	os.MkdirAll(agentsDir, 0o755)
	symlinkPath := filepath.Join(agentsDir, "context-store")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Skipf("cannot create symlink in this environment: %v", err)
	}

	t.Chdir(project)
	_, _, err := LoadConfig("")
	if err == nil {
		t.Fatal("expected rejection of a project-local config directory symlink pointing outside the project root")
	}
}

func TestLoadConfigAllowsLegitimateProjectLocalConfig(t *testing.T) {
	// Regression test: ensure the symlink guard doesn't break normal config resolution
	project := t.TempDir()
	gitDir := filepath.Join(project, ".git")
	os.Mkdir(gitDir, 0o755)
	agentsDir := filepath.Join(project, ".agents", "context-store")
	os.MkdirAll(agentsDir, 0o755)
	configFile := filepath.Join(agentsDir, "config.json")
	os.WriteFile(configFile, []byte("{}"), 0o644)

	t.Chdir(project)
	cfg, tier, err := LoadConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tier != TierProjectLocal {
		t.Errorf("tier = %q, want project-local", tier)
	}
	if cfg.Embedding.Provider != "hashing" {
		t.Errorf("provider = %q, want hashing", cfg.Embedding.Provider)
	}
}
