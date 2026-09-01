package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	code := knowledgeInit(testEnv(t, dbPath), []string{"--invalid-flag"})
	if code != 2 {
		t.Errorf("Expected exit code 2 for flag error, got %d", code)
	}
}

// TestKnowledgeInitRefusesToCreateAStore pins the change of ownership: init
// used to create a store when the configured path did not exist, which is how
// a mistyped path became an empty store that searched clean. cadre does not
// own store creation any more, so a missing store is an error naming the tool
// that does.
func TestKnowledgeInitRefusesToCreateAStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "absent.db")

	code := knowledgeInit(testEnv(t, dbPath), nil)
	if code == 0 {
		t.Fatal("init reported success for a store that does not exist")
	}
	if _, err := os.Stat(dbPath); err == nil {
		t.Fatalf("init created %s; cadre no longer creates stores", dbPath)
	}
}

// TestRetiredVerbsNameTheirReplacement checks every retired verb answers by
// name rather than falling through to "unknown subcommand". An operator who
// had a working command yesterday gets told where it went.
func TestRetiredVerbsNameTheirReplacement(t *testing.T) {
	for _, verb := range knowledgeRetiredList() {
		stderr := captureStderr(t, func() {
			if code := KnowledgeCmd([]string{verb}); code != 2 {
				t.Errorf("%s: exit %d, want 2", verb, code)
			}
		})
		if !strings.Contains(stderr, verb) {
			t.Errorf("%s: message does not name the verb: %s", verb, stderr)
		}
		if !strings.Contains(stderr, "retired") {
			t.Errorf("%s: message does not say it is retired: %s", verb, stderr)
		}
	}
}

// TestRetiredVerbsAnswerBeforeConfigResolution: a retired verb is answered
// without resolving a config, so an operator on a machine with no knowledge
// config still learns where the command went instead of being told their
// config is missing.
func TestRetiredVerbsAnswerBeforeConfigResolution(t *testing.T) {
	stderr := captureStderr(t, func() {
		if code := KnowledgeCmd([]string{"--config", "/nonexistent/config.json", "hybrid-search"}); code != 2 {
			t.Errorf("exit %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "recall hybrid-search") {
		t.Errorf("expected the replacement verb, got: %s", stderr)
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
