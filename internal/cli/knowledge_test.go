package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/deagy/cadre/cli/internal/knowledge"
)

// Database tests are skipped in CGO_ENABLED=0 builds (standard SQLite limitation).
// These tests focus on CLI interface without database I/O.

// writeStoreConfig writes a knowledge-store config.json pointing at dbPath
// and returns its path. Tests go through the real config loader rather than
// hand-building a Config, so a change to resolution or validation shows up
// here rather than being bypassed.
func writeStoreConfig(t *testing.T, dbPath string, extra map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	raw := map[string]any{"database": dbPath}
	for k, v := range extra {
		raw[k] = v
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshalling config: %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

// testEnv resolves a knowledgeEnv for a store at dbPath.
func testEnv(t *testing.T, dbPath string, extra ...map[string]any) knowledgeEnv {
	t.Helper()
	var supplied map[string]any
	if len(extra) > 0 {
		supplied = extra[0]
	}
	cfg, tier, err := knowledge.LoadConfig(writeStoreConfig(t, dbPath, supplied))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return knowledgeEnv{cfg: cfg, tier: tier}
}

func TestKnowledgeInitFlagError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Test with invalid flag
	code := knowledgeInit(dbPath, []string{"--invalid-flag"})
	if code != 2 {
		t.Errorf("Expected exit code 2 for flag error, got %d", code)
	}
}

func TestKnowledgeInitUnexpectedArg(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Test with unexpected argument
	code := knowledgeInit(dbPath, []string{"unexpected-arg"})
	if code != 2 {
		t.Errorf("Expected exit code 2 for unexpected arg, got %d", code)
	}
}

func TestKnowledgeStatsVerifyError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent.db")

	// Test stats on non-existent store should fail
	code := knowledgeStats(dbPath, []string{})
	if code == 0 {
		t.Error("Expected non-zero exit code for non-existent store")
	}
}

func TestKnowledgeCmdHelp(t *testing.T) {
	code := KnowledgeCmd([]string{"help"})
	if code != 0 {
		t.Errorf("Expected exit code 0 for help, got %d", code)
	}
}

func TestKnowledgeCmdUnknown(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// A resolvable config, so this exercises the unknown-subcommand branch
	// rather than failing earlier on config resolution.
	cfgPath := writeStoreConfig(t, dbPath, nil)
	code := KnowledgeCmd([]string{"--config", cfgPath, "unknown-command"})
	if code == 0 {
		t.Error("Expected non-zero exit code for unknown command")
	}
}

func TestKnowledgeIngestMissingSource(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// ingest without --source should fail
	code := knowledgeIngest(testEnv(t, dbPath), []string{})
	if code != 2 {
		t.Errorf("Expected exit code 2 for missing source, got %d", code)
	}
}

func TestKnowledgeSearchMissingQuery(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// search without query should fail
	code := knowledgeSearch(testEnv(t, dbPath), []string{})
	if code != 2 {
		t.Errorf("Expected exit code 2 for missing query, got %d", code)
	}
}

func TestKnowledgeSearchMissingClassification(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// search without --classification should fail
	code := knowledgeSearch(testEnv(t, dbPath), []string{"test-query"})
	if code != 2 {
		t.Errorf("Expected exit code 2 for missing classification, got %d", code)
	}
}

func TestKnowledgeDeleteNoMode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// delete without deletion mode should fail
	code := knowledgeDelete(dbPath, []string{})
	if code != 2 {
		t.Errorf("Expected exit code 2 for no deletion mode, got %d", code)
	}
}

func TestKnowledgeDeleteMultipleModes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// delete with multiple modes should fail
	code := knowledgeDelete(dbPath, []string{"--expired", "--source", "test"})
	if code != 2 {
		t.Errorf("Expected exit code 2 for multiple deletion modes, got %d", code)
	}
}

func TestKnowledgeNoSubcommand(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Test with no subcommand
	code := KnowledgeCmd([]string{"--config", dbPath})
	if code != 2 {
		t.Errorf("Expected exit code 2 for no subcommand, got %d", code)
	}
}
