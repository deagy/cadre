package knowledge

import (
	"testing"
)

// Federation tests

func TestFederatedStoreBasic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping federation test in short mode")
	}

	// Setup stores
	store1 := setupTestDB(t)
	defer store1.Close()

	store2 := setupTestDB(t)
	defer store2.Close()

	// Setup registry and federation
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store1)
	registry.AddStore("shard-1", store2)

	federated := NewFederatedStore(registry)

	// Ingest test data to both shards
	msg := &Message{
		Source:       "app-1",
		ConversationID: "conv-1",
		SourceMessageID: "msg-1",
		Role:         "user",
		Content:      "test content",
		Classification: "general",
	}

	err := federated.FederatedIngest("app-1", "general", "conv-1", msg)
	if err != nil {
		t.Fatalf("Failed to ingest message: %v", err)
	}
}

func TestFederatedSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping federation test in short mode")
	}

	// Setup stores with test data
	store1 := setupTestDB(t)
	defer store1.Close()

	store2 := setupTestDB(t)
	defer store2.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Add messages to store1
	msgID1, _ := store1.SaveMessage(
		"app-a", nil, "conv-1", nil, "msg-1",
		"user", "machine learning algorithms", nil, "technical", false,
		`[]`, `{}`, nil,
	)
	embs, _ := embedder.Embed([]string{"machine learning"})
	store1.SaveChunk(msgID1, 0, "machine learning", embedder.Name(), embedder.Model(), embs[0])

	// Add messages to store2
	msgID2, _ := store2.SaveMessage(
		"app-b", nil, "conv-2", nil, "msg-2",
		"user", "deep learning networks", nil, "technical", false,
		`[]`, `{}`, nil,
	)
	embs, _ = embedder.Embed([]string{"deep learning"})
	store2.SaveChunk(msgID2, 0, "deep learning", embedder.Name(), embedder.Model(), embs[0])

	// Setup federation
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store1)
	registry.AddStore("shard-1", store2)

	federated := NewFederatedStore(registry)

	// Perform federated search
	opts := FederatedSearchOptions{
		SearchOptions: SearchOptions{
			Query:             "learning",
			Classification:    "technical",
			EmbeddingProvider: embedder,
			Top:               10,
		},
	}

	result, err := federated.FederatedSearch(opts)
	if err != nil {
		t.Fatalf("Federated search failed: %v", err)
	}

	if result == nil {
		t.Fatal("Result is nil")
	}

	// Should get results from both shards
	if len(result.Results) == 0 {
		t.Error("Expected search results")
	}

	if result.TotalQueried != 2 {
		t.Errorf("Expected 2 shards queried, got %d", result.TotalQueried)
	}
}

func TestFederatedSearchResultAggregation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping federation test in short mode")
	}

	store1 := setupTestDB(t)
	defer store1.Close()

	store2 := setupTestDB(t)
	defer store2.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Add multiple messages to each store
	for i := 0; i < 5; i++ {
		msgID, _ := store1.SaveMessage(
			"app-a", nil, "conv", nil, string(rune(48+i)),
			"user", "test message content", nil, "general", false,
			`[]`, `{}`, nil,
		)
		embs, _ := embedder.Embed([]string{"content"})
		store1.SaveChunk(msgID, 0, "content", embedder.Name(), embedder.Model(), embs[0])
	}

	for i := 0; i < 3; i++ {
		msgID, _ := store2.SaveMessage(
			"app-b", nil, "conv", nil, string(rune(53+i)),
			"user", "other test message", nil, "general", false,
			`[]`, `{}`, nil,
		)
		embs, _ := embedder.Embed([]string{"message"})
		store2.SaveChunk(msgID, 0, "message", embedder.Name(), embedder.Model(), embs[0])
	}

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store1)
	registry.AddStore("shard-1", store2)

	federated := NewFederatedStore(registry)

	opts := FederatedSearchOptions{
		SearchOptions: SearchOptions{
			Query:             "test",
			Classification:    "general",
			EmbeddingProvider: embedder,
			Top:               5,
		},
	}

	result, err := federated.FederatedSearch(opts)
	if err != nil {
		t.Fatalf("Federated search failed: %v", err)
	}

	// Results should be limited to Top (5)
	if len(result.Results) > 5 {
		t.Errorf("Expected at most 5 results, got %d", len(result.Results))
	}

	// Results should be sorted by similarity (descending)
	for i := 0; i < len(result.Results)-1; i++ {
		if result.Results[i].CosineSimilarity < result.Results[i+1].CosineSimilarity {
			t.Error("Results not sorted by similarity (descending)")
			break
		}
	}
}

func TestFederatedDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping federation test in short mode")
	}

	store1 := setupTestDB(t)
	defer store1.Close()

	store2 := setupTestDB(t)
	defer store2.Close()

	// Add messages to both stores
	store1.SaveMessage(
		"app-a", nil, "conv", nil, "msg-1",
		"user", "content", nil, "secret", false,
		`[]`, `{}`, nil,
	)

	store2.SaveMessage(
		"app-b", nil, "conv", nil, "msg-2",
		"user", "content", nil, "secret", false,
		`[]`, `{}`, nil,
	)

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store1)
	registry.AddStore("shard-1", store2)

	federated := NewFederatedStore(registry)

	// Delete by classification across all shards
	classification := "secret"
	delResult, err := federated.FederatedDelete(FederatedDeleteOptions{
		Mode:           "classification",
		Classification: &classification,
		AuthorizedBy:   "test-user",
	})

	if err != nil {
		t.Fatalf("Federated delete failed: %v", err)
	}

	if delResult.TotalDeleted != 2 {
		t.Errorf("Expected 2 deleted, got %d", delResult.TotalDeleted)
	}

	if delResult.TotalQueried != 2 {
		t.Errorf("Expected 2 shards queried, got %d", delResult.TotalQueried)
	}
}

func TestFederatedStats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping federation test in short mode")
	}

	store1 := setupTestDB(t)
	defer store1.Close()

	store2 := setupTestDB(t)
	defer store2.Close()

	// Add messages to each store
	store1.SaveMessage(
		"app-1", nil, "conv-1", nil, "msg-1",
		"user", "content", nil, "general", false,
		`[]`, `{}`, nil,
	)

	store2.SaveMessage(
		"app-2", nil, "conv-2", nil, "msg-2",
		"user", "content", nil, "general", false,
		`[]`, `{}`, nil,
	)

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store1)
	registry.AddStore("shard-1", store2)

	federated := NewFederatedStore(registry)

	// Get federated stats
	stats, err := federated.FederatedStats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	if stats.TotalMessages != 2 {
		t.Errorf("Expected 2 total messages, got %d", stats.TotalMessages)
	}

	if len(stats.ShardStats) != 2 {
		t.Errorf("Expected 2 shard stats, got %d", len(stats.ShardStats))
	}
}

func TestFederatedShardingStats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping federation test in short mode")
	}

	store1 := setupTestDB(t)
	defer store1.Close()

	store2 := setupTestDB(t)
	defer store2.Close()

	// Add different message counts
	for i := 0; i < 5; i++ {
		store1.SaveMessage(
			"app-1", nil, "conv", nil, string(rune(48+i)),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)
	}

	for i := 0; i < 3; i++ {
		store2.SaveMessage(
			"app-2", nil, "conv", nil, string(rune(53+i)),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)
	}

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store1)
	registry.AddStore("shard-1", store2)

	federated := NewFederatedStore(registry)

	shardStats, err := federated.ShardingStats()
	if err != nil {
		t.Fatalf("Failed to get sharding stats: %v", err)
	}

	if shardStats.TotalShards != 2 {
		t.Errorf("Expected 2 shards, got %d", shardStats.TotalShards)
	}

	if shardStats.ActiveShards != 2 {
		t.Errorf("Expected 2 active shards, got %d", shardStats.ActiveShards)
	}

	if shardStats.ShardStrategy != "source" {
		t.Errorf("Expected strategy 'source', got %s", shardStats.ShardStrategy)
	}

	if shardStats.Distribution["shard-0"] != 5 {
		t.Errorf("Expected 5 messages in shard-0, got %d", shardStats.Distribution["shard-0"])
	}

	if shardStats.Distribution["shard-1"] != 3 {
		t.Errorf("Expected 3 messages in shard-1, got %d", shardStats.Distribution["shard-1"])
	}
}

func TestFederatedDeleteWithErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping federation test in short mode")
	}

	store1 := setupTestDB(t)
	defer store1.Close()

	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)
	registry.AddStore("shard-0", store1)
	// shard-1 intentionally not registered

	federated := NewFederatedStore(registry)

	// Try to delete with invalid mode
	delResult, err := federated.FederatedDelete(FederatedDeleteOptions{
		Mode:         "invalid",
		AuthorizedBy: "test-user",
	})

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should have errors for unregistered shard
	if delResult.TotalFailed == 0 {
		t.Error("Expected some shard failures")
	}
}

func TestFederatedSearchNoStores(t *testing.T) {
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)

	federated := NewFederatedStore(registry)

	opts := FederatedSearchOptions{
		SearchOptions: SearchOptions{
			Query:          "test",
			Classification: "general",
		},
	}

	_, err := federated.FederatedSearch(opts)
	if err == nil {
		t.Error("Expected error when no stores registered")
	}
}

func TestFederatedDeleteNoStores(t *testing.T) {
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)

	federated := NewFederatedStore(registry)

	_, err := federated.FederatedDelete(FederatedDeleteOptions{
		Mode:         "expired",
		AuthorizedBy: "test-user",
	})

	if err == nil {
		t.Error("Expected error when no stores registered")
	}
}

func TestFederatedStatsNoStores(t *testing.T) {
	strategy := &SourceShardingStrategy{}
	registry := NewStoreRegistry(strategy)

	federated := NewFederatedStore(registry)

	_, err := federated.FederatedStats()
	if err == nil {
		t.Error("Expected error when no stores registered")
	}
}
