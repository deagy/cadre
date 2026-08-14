package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/deagy/cadre/cli/internal/knowledge"
)

// Helper to create imbalanced test shards
func setupImbalancedShards(t *testing.T) (string, func()) {
	tmpDir := t.TempDir()
	shardDir := filepath.Join(tmpDir, ".agents", "knowledge-store")
	os.MkdirAll(shardDir, 0755)

	// Create shard-0.db with many messages
	shard0Path := filepath.Join(shardDir, "shard-0.db")
	store0, err := knowledge.Open(shard0Path)
	if err != nil {
		t.Fatalf("Cannot create shard-0: %v", err)
	}
	defer store0.Close()

	for i := 0; i < 80; i++ {
		store0.SaveMessage(
			"app-a", nil, "conv", nil, string(rune(48+(i%10))),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)
	}

	// Create shard-1.db with few messages
	shard1Path := filepath.Join(shardDir, "shard-1.db")
	store1, err := knowledge.Open(shard1Path)
	if err != nil {
		t.Fatalf("Cannot create shard-1: %v", err)
	}
	defer store1.Close()

	for i := 0; i < 20; i++ {
		store1.SaveMessage(
			"app-b", nil, "conv", nil, string(rune(48+i)),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return filepath.Join(shardDir, "store.db"), cleanup
}

func TestKnowledgeRebalanceAnalyze(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	// Test rebalance analyze
	exitCode := knowledgeRebalance(dbPath, []string{})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeRebalanceDryRun(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	// Test dry-run flag
	exitCode := knowledgeRebalance(dbPath, []string{"--dry-run"})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeRebalanceJSON(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	// Test JSON output
	exitCode := knowledgeRebalance(dbPath, []string{"--json"})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeRebalanceWithStrategy(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	strategies := []string{"classification", "source", "conversation", "composite"}
	for _, strategy := range strategies {
		exitCode := knowledgeRebalance(dbPath, []string{"--strategy", strategy})
		if exitCode != 0 {
			t.Errorf("Expected exit code 0 for strategy %s, got %d", strategy, exitCode)
		}
	}
}

func TestKnowledgeRebalanceInvalidStrategy(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	exitCode := knowledgeRebalance(dbPath, []string{"--strategy", "invalid"})
	if exitCode != 2 {
		t.Errorf("Expected exit code 2, got %d", exitCode)
	}
}

func TestKnowledgeRebalanceNoShards(t *testing.T) {
	tmpDir := t.TempDir()
	shardDir := filepath.Join(tmpDir, ".agents", "knowledge-store")
	os.MkdirAll(shardDir, 0755)

	// Create only store.db (single-store mode)
	dbPath := filepath.Join(shardDir, "store.db")
	store, err := knowledge.Open(dbPath)
	if err != nil {
		t.Fatalf("Cannot create store: %v", err)
	}
	store.Close()

	// Should fail in single-store mode
	exitCode := knowledgeRebalance(dbPath, []string{})
	if exitCode != 1 {
		t.Errorf("Expected exit code 1 for single-store mode, got %d", exitCode)
	}
}

func TestKnowledgeRebalanceUnexpectedArg(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	exitCode := knowledgeRebalance(dbPath, []string{"unexpected-arg"})
	if exitCode != 2 {
		t.Errorf("Expected exit code 2, got %d", exitCode)
	}
}

func TestKnowledgeRebalanceStatus(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	// Test status command
	exitCode := knowledgeRebalanceStatus(dbPath, []string{})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeRebalanceStatusJSON(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	// Test status with JSON output
	exitCode := knowledgeRebalanceStatus(dbPath, []string{"--json"})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeRebalanceStatusWithStrategy(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	strategies := []string{"classification", "source", "conversation"}
	for _, strategy := range strategies {
		exitCode := knowledgeRebalanceStatus(dbPath, []string{"--strategy", strategy})
		if exitCode != 0 {
			t.Errorf("Expected exit code 0 for strategy %s, got %d", strategy, exitCode)
		}
	}
}

func TestKnowledgeRebalanceStatusInvalidStrategy(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	exitCode := knowledgeRebalanceStatus(dbPath, []string{"--strategy", "invalid"})
	if exitCode != 2 {
		t.Errorf("Expected exit code 2, got %d", exitCode)
	}
}

func TestKnowledgeRebalanceStatusNoShards(t *testing.T) {
	tmpDir := t.TempDir()
	shardDir := filepath.Join(tmpDir, ".agents", "knowledge-store")
	os.MkdirAll(shardDir, 0755)

	// Create only store.db (single-store mode)
	dbPath := filepath.Join(shardDir, "store.db")
	store, err := knowledge.Open(dbPath)
	if err != nil {
		t.Fatalf("Cannot create store: %v", err)
	}
	store.Close()

	// Should fail in single-store mode
	exitCode := knowledgeRebalanceStatus(dbPath, []string{})
	if exitCode != 1 {
		t.Errorf("Expected exit code 1 for single-store mode, got %d", exitCode)
	}
}

func TestKnowledgeRebalanceStatusUnexpectedArg(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	exitCode := knowledgeRebalanceStatus(dbPath, []string{"unexpected-arg"})
	if exitCode != 2 {
		t.Errorf("Expected exit code 2, got %d", exitCode)
	}
}

func TestKnowledgeRebalanceCombined(t *testing.T) {
	dbPath, cleanup := setupImbalancedShards(t)
	defer cleanup()

	// First analyze
	exitCode := knowledgeRebalance(dbPath, []string{"--dry-run", "--json"})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0 for analyze, got %d", exitCode)
	}

	// Then check status
	exitCode = knowledgeRebalanceStatus(dbPath, []string{"--json"})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0 for status, got %d", exitCode)
	}
}
