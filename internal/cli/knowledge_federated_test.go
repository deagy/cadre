package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/deagy/cadre/cli/internal/knowledge"
)

// Helper to create a test shard directory with multiple stores
func setupTestShards(t *testing.T) (string, func()) {
	tmpDir := t.TempDir()
	shardDir := filepath.Join(tmpDir, ".agents", "knowledge-store")
	os.MkdirAll(shardDir, 0755)

	// Create shard-0.db
	shard0Path := filepath.Join(shardDir, "shard-0.db")
	store0, err := knowledge.Open(shard0Path)
	if err != nil {
		t.Fatalf("Cannot create shard-0: %v", err)
	}
	defer store0.Close()

	// Add a message to shard-0
	msgID, err := store0.SaveMessage(
		"app-a", nil, "conv-1", nil, "msg-1",
		"user", "test message for shard 0", nil, "internal", false,
		`[]`, `{}`, nil,
	)
	if err != nil {
		t.Fatalf("Cannot save message to shard-0: %v", err)
	}

	embedder := knowledge.NewLocalHashingEmbedder(128)
	embs, _ := embedder.Embed([]string{"test"})
	store0.SaveChunk(msgID, 0, "test", embedder.Name(), embedder.Model(), embs[0])

	// Create shard-1.db
	shard1Path := filepath.Join(shardDir, "shard-1.db")
	store1, err := knowledge.Open(shard1Path)
	if err != nil {
		t.Fatalf("Cannot create shard-1: %v", err)
	}
	defer store1.Close()

	// Add a message to shard-1
	msgID2, err := store1.SaveMessage(
		"app-b", nil, "conv-2", nil, "msg-2",
		"user", "another message for shard 1", nil, "internal", false,
		`[]`, `{}`, nil,
	)
	if err != nil {
		t.Fatalf("Cannot save message to shard-1: %v", err)
	}
	embs, _ = embedder.Embed([]string{"another"})
	store1.SaveChunk(msgID2, 0, "another", embedder.Name(), embedder.Model(), embs[0])

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return filepath.Join(shardDir, "store.db"), cleanup
}

func TestKnowledgeShardsNoShards(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "store.db")

	// Try shards command with no stores (should fail)
	exitCode := knowledgeShards(dbPath, []string{})
	if exitCode != 1 {
		t.Errorf("Expected exit code 1 for no shards, got %d", exitCode)
	}
}

func TestKnowledgeShardsBasic(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	// Test shards command with JSON output
	exitCode := knowledgeShards(dbPath, []string{"--json"})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeSharedsWithStrategy(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	strategies := []string{"classification", "source", "conversation", "composite"}
	for _, strategy := range strategies {
		exitCode := knowledgeShards(dbPath, []string{"--strategy", strategy})
		if exitCode != 0 {
			t.Errorf("Expected exit code 0 for strategy %s, got %d", strategy, exitCode)
		}
	}
}

func TestKnowledgeSharedsInvalidStrategy(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	exitCode := knowledgeShards(dbPath, []string{"--strategy", "invalid"})
	if exitCode != 2 {
		t.Errorf("Expected exit code 2 for invalid strategy, got %d", exitCode)
	}
}

func TestKnowledgeFederatedSearchSingleStore(t *testing.T) {
	requireSQLite(t)
	// Single-store mode (store.db exists but no shard-*.db files)
	// Federated commands should fail gracefully since they require multi-shard setup
	tmpDir := t.TempDir()
	shardDir := filepath.Join(tmpDir, ".agents", "knowledge-store")
	os.MkdirAll(shardDir, 0755)

	// Create only store.db (single-store mode, no shards)
	dbPath := filepath.Join(shardDir, "store.db")
	store, err := knowledge.Open(dbPath)
	if err != nil {
		t.Fatalf("Cannot create single store: %v", err)
	}
	store.Close()

	// Try federated search in single-store mode (should fail)
	exitCode := knowledgeFederatedSearch(testEnv(t, dbPath), []string{
		"--classification", "internal",
		"--all-sources",
		"test",
	})
	if exitCode != 1 {
		t.Errorf("Expected exit code 1 for single-store mode, got %d", exitCode)
	}
}

func TestKnowledgeFederatedSearchMissingClassification(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	exitCode := knowledgeFederatedSearch(testEnv(t, dbPath), []string{"test"})
	if exitCode != 2 {
		t.Errorf("Expected exit code 2 for missing classification, got %d", exitCode)
	}
}

func TestKnowledgeFederatedSearchMissingQuery(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	exitCode := knowledgeFederatedSearch(testEnv(t, dbPath), []string{"--classification", "internal"})
	if exitCode != 2 {
		t.Errorf("Expected exit code 2 for missing query, got %d", exitCode)
	}
}

func TestKnowledgeFederatedSearchBasic(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	exitCode := knowledgeFederatedSearch(testEnv(t, dbPath), []string{
		"--classification", "internal",
		"--all-sources",
		"--json",
		"test",
	})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeFederatedSearchWithParallelism(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	exitCode := knowledgeFederatedSearch(testEnv(t, dbPath), []string{
		"--classification", "internal",
		"--all-sources",
		"--parallel", "2",
		"test",
	})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeFederatedSearchWithStrategy(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	strategies := []string{"classification", "source", "conversation"}
	for _, strategy := range strategies {
		exitCode := knowledgeFederatedSearch(testEnv(t, dbPath), []string{
			"--classification", "internal",
			"--all-sources",
			"--strategy", strategy,
			"test",
		})
		if exitCode != 0 {
			t.Errorf("Expected exit code 0 for strategy %s, got %d", strategy, exitCode)
		}
	}
}

func TestKnowledgeFederatedSearchInvalidStrategy(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	exitCode := knowledgeFederatedSearch(testEnv(t, dbPath), []string{
		"--classification", "internal",
		"--all-sources",
		"--strategy", "invalid",
		"test",
	})
	if exitCode != 2 {
		t.Errorf("Expected exit code 2 for invalid strategy, got %d", exitCode)
	}
}

func TestKnowledgeFederatedDeleteSingleStore(t *testing.T) {
	requireSQLite(t)
	// Single-store mode (store.db exists but no shard-*.db files)
	// Federated commands should fail gracefully since they require multi-shard setup
	tmpDir := t.TempDir()
	shardDir := filepath.Join(tmpDir, ".agents", "knowledge-store")
	os.MkdirAll(shardDir, 0755)

	// Create only store.db (single-store mode, no shards)
	dbPath := filepath.Join(shardDir, "store.db")
	store, err := knowledge.Open(dbPath)
	if err != nil {
		t.Fatalf("Cannot create single store: %v", err)
	}
	store.Close()

	// Try federated delete in single-store mode (should fail)
	exitCode := knowledgeFederatedDelete(dbPath, []string{
		"--expired",
	})
	if exitCode != 1 {
		t.Errorf("Expected exit code 1 for single-store mode, got %d", exitCode)
	}
}

func TestKnowledgeFederatedDeleteNoMode(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	// No deletion mode specified
	exitCode := knowledgeFederatedDelete(dbPath, []string{})
	if exitCode != 2 {
		t.Errorf("Expected exit code 2 for no deletion mode, got %d", exitCode)
	}
}

func TestKnowledgeFederatedDeleteMultipleModes(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	// Multiple deletion modes specified
	exitCode := knowledgeFederatedDelete(dbPath, []string{
		"--expired",
		"--classification", "internal",
	})
	if exitCode != 2 {
		t.Errorf("Expected exit code 2 for multiple modes, got %d", exitCode)
	}
}

func TestKnowledgeFederatedDeleteByClassification(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	exitCode := knowledgeFederatedDelete(dbPath, []string{
		"--classification", "internal",
		"--json",
	})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeFederatedDeleteBySource(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	exitCode := knowledgeFederatedDelete(dbPath, []string{
		"--source", "app-a",
		"--json",
	})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeFederatedDeleteByAge(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	exitCode := knowledgeFederatedDelete(dbPath, []string{
		"--age", "1",
		"--json",
	})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeFederatedDeleteByExpired(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	exitCode := knowledgeFederatedDelete(dbPath, []string{
		"--expired",
		"--json",
	})
	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", exitCode)
	}
}

func TestKnowledgeFederatedDeleteWithStrategy(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	strategies := []string{"classification", "source"}
	for _, strategy := range strategies {
		exitCode := knowledgeFederatedDelete(dbPath, []string{
			"--strategy", strategy,
			"--classification", "internal",
		})
		if exitCode != 0 {
			t.Errorf("Expected exit code 0 for strategy %s, got %d", strategy, exitCode)
		}
	}
}

func TestKnowledgeFederatedDeleteInvalidStrategy(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	exitCode := knowledgeFederatedDelete(dbPath, []string{
		"--strategy", "invalid",
		"--classification", "internal",
	})
	if exitCode != 2 {
		t.Errorf("Expected exit code 2 for invalid strategy, got %d", exitCode)
	}
}

func TestDiscoverShardsMultipleShards(t *testing.T) {
	requireSQLite(t)
	dbPath, cleanup := setupTestShards(t)
	defer cleanup()

	shards, err := discoverShards(dbPath)
	if err != nil {
		t.Fatalf("Cannot discover shards: %v", err)
	}

	// Should find both shard-0 and shard-1
	if len(shards) < 2 {
		t.Errorf("Expected at least 2 shards, got %d", len(shards))
	}

	// Close shards
	for _, shard := range shards {
		shard.Close()
	}
}

func TestDiscoverShardsSingleStore(t *testing.T) {
	requireSQLite(t)
	tmpDir := t.TempDir()
	shardDir := filepath.Join(tmpDir, ".agents", "knowledge-store")
	os.MkdirAll(shardDir, 0755)

	dbPath := filepath.Join(shardDir, "store.db")
	store, err := knowledge.Open(dbPath)
	if err != nil {
		t.Fatalf("Cannot create single store: %v", err)
	}
	defer store.Close()

	// Discover should reject single-store mode (no shard-*.db files)
	shards, err := discoverShards(dbPath)
	if err == nil {
		t.Error("Expected error for single-store mode")
	}
	if len(shards) != 0 {
		t.Errorf("Expected 0 shards for single-store mode, got %d", len(shards))
		for _, shard := range shards {
			shard.Close()
		}
	}
}

func TestDiscoverShardsNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nonexistent", "store.db")

	shards, err := discoverShards(dbPath)
	if err == nil {
		t.Error("Expected error for nonexistent directory")
	}
	if len(shards) > 0 {
		for _, s := range shards {
			s.Close()
		}
	}
}
