package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config file's server block must reach the resolved Config.
//
// This test class is the one whose absence let a whole feature ship
// unreachable. ServerConfig existed, ResolveActorObserver read it, and three
// unit tests exercised the server path by building `&Config{Server: …}` in Go
// — proving the function worked, and proving nothing about whether anything
// could call it with a real value. LoadConfig never parsed "server", so from
// a released binary the authenticated branch could not be reached at all.
//
// A test that constructs the struct it is testing skips the question that
// matters. This one writes a config.json and reads it back.

func writeKnowledgeConfig(t *testing.T, dir string, extra map[string]any) string {
	t.Helper()
	cfg := map[string]any{
		"database": filepath.Join(dir, "store.db"),
		"embedding": map[string]any{
			"provider": "local-hashing", "dimensions": 384, "batch_size": 8,
		},
		"chunking":  map[string]any{"max_characters": 800, "overlap_characters": 80},
		"ingestion": map[string]any{"redact_secrets": true, "default_classification": "internal"},
		"retention": map[string]any{
			"default_days_by_classification":   map[string]any{},
			"refuse_restricted_without_window": true,
		},
	}
	for key, value := range extra {
		cfg[key] = value
	}
	path := filepath.Join(dir, "config.json")
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshalling config: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func TestLoadConfigReadsTheServerBlock(t *testing.T) {
	dir := isolatedProject(t)
	path := writeKnowledgeConfig(t, dir, map[string]any{
		"server": map[string]any{
			"url":         "https://recall.internal.example",
			"api_key_env": "RECALL_API_KEY",
		},
	})

	cfg, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("loading a config with a server block: %v", err)
	}
	if cfg.Server.URL != "https://recall.internal.example" {
		t.Fatalf("server.url did not survive the load: %q", cfg.Server.URL)
	}
	if cfg.Server.APIKeyEnv != "RECALL_API_KEY" {
		t.Fatalf("server.api_key_env did not survive the load: %q", cfg.Server.APIKeyEnv)
	}
}

// No server block is the default and must load cleanly.
func TestLoadConfigWithoutAServerBlockLoads(t *testing.T) {
	dir := isolatedProject(t)
	path := writeKnowledgeConfig(t, dir, nil)

	cfg, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("a config with no server block must load: %v", err)
	}
	if cfg.Server.URL != "" {
		t.Fatalf("no server block produced a URL: %q", cfg.Server.URL)
	}
}

// A malformed server block must refuse rather than be ignored.
//
// Ignoring it leaves a caller believing their records carry an authenticated
// subject when they carry the local machine's OS user, which is the exact
// misplaced confidence this phase exists to remove.
func TestLoadConfigRefusesAMalformedServerBlock(t *testing.T) {
	dir := isolatedProject(t)
	path := writeKnowledgeConfig(t, dir, map[string]any{"server": "https://not-an-object"})

	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("a server block that is not an object was accepted")
	}
}

// A credential with nowhere to send it authenticates nothing.
func TestLoadConfigRefusesACredentialWithNoServerURL(t *testing.T) {
	dir := isolatedProject(t)
	path := writeKnowledgeConfig(t, dir, map[string]any{
		"server": map[string]any{"api_key_env": "RECALL_API_KEY"},
	})

	_, _, err := LoadConfig(path)
	if err == nil {
		t.Fatal("api_key_env with no url was accepted")
	}
	if !strings.Contains(err.Error(), "url") {
		t.Fatalf("the refusal should name the missing url: %v", err)
	}
}
