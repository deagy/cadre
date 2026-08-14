package cli

import (
	"path/filepath"
	"testing"
)

// Database tests are skipped in CGO_ENABLED=0 builds (standard SQLite limitation).
// These tests focus on CLI interface without database I/O.

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

	code := KnowledgeCmd([]string{"--config", dbPath, "unknown-command"})
	if code == 0 {
		t.Error("Expected non-zero exit code for unknown command")
	}
}

func TestKnowledgeSearchStubbed(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// search subcommand should return 1 (not implemented)
	code := KnowledgeCmd([]string{"--config", dbPath, "search"})
	if code == 0 {
		t.Error("Expected non-zero exit code for search (not yet implemented)")
	}
}

func TestKnowledgeContextStubbed(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// context subcommand should return 1 (not implemented)
	code := KnowledgeCmd([]string{"--config", dbPath, "context"})
	if code == 0 {
		t.Error("Expected non-zero exit code for context (not yet implemented)")
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
