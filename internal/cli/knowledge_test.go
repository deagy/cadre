//go:build cgo
// +build cgo

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

func TestKnowledgeIngestMissingSource(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// ingest without --source should fail
	code := knowledgeIngest(dbPath, []string{})
	if code != 2 {
		t.Errorf("Expected exit code 2 for missing source, got %d", code)
	}
}

func TestKnowledgeSearchMissingQuery(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// search without query should fail
	code := knowledgeSearch(dbPath, []string{})
	if code != 2 {
		t.Errorf("Expected exit code 2 for missing query, got %d", code)
	}
}

func TestKnowledgeSearchMissingClassification(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// search without --classification should fail
	code := knowledgeSearch(dbPath, []string{"test-query"})
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
