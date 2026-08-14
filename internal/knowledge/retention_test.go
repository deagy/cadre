package knowledge

import (
	"testing"
	"time"
)

// Retention and deletion tests. Require CGO_ENABLED=1 due to SQLite.

func TestGetExpiredMessages(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save messages with different retention dates
	now := time.Now().UTC()
	pastTime := now.AddDate(0, 0, -1).Format("2006-01-02T15:04:05.000Z")
	futureTime := now.AddDate(0, 0, 1).Format("2006-01-02T15:04:05.000Z")

	store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "expired message", nil, "general", false,
		`[]`, `{}`, &pastTime,
	)

	store.SaveMessage(
		"source", nil, "conv-2", nil, "msg-2",
		"user", "not expired", nil, "general", false,
		`[]`, `{}`, &futureTime,
	)

	store.SaveMessage(
		"source", nil, "conv-3", nil, "msg-3",
		"user", "no retention", nil, "general", false,
		`[]`, `{}`, nil,
	)

	// Get expired messages
	expired, err := store.GetExpiredMessages()
	if err != nil {
		t.Fatalf("GetExpiredMessages failed: %v", err)
	}

	if len(expired) != 1 {
		t.Errorf("Expected 1 expired message, got %d", len(expired))
	}

	if expired[0].Classification != "general" {
		t.Errorf("Expected classification 'general', got %s", expired[0].Classification)
	}
}

func TestDeleteExpired(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save expired messages
	now := time.Now().UTC()
	pastTime := now.AddDate(0, 0, -1).Format("2006-01-02T15:04:05.000Z")

	store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "expired 1", nil, "general", false,
		`[]`, `{}`, &pastTime,
	)

	store.SaveMessage(
		"source", nil, "conv-2", nil, "msg-2",
		"user", "expired 2", nil, "general", false,
		`[]`, `{}`, &pastTime,
	)

	// Delete expired
	deleted, err := store.DeleteExpired("test-user")
	if err != nil {
		t.Fatalf("DeleteExpired failed: %v", err)
	}

	if deleted != 2 {
		t.Errorf("Expected 2 deleted, got %d", deleted)
	}

	// Verify deletion
	remaining, err := store.GetExpiredMessages()
	if err != nil {
		t.Fatalf("GetExpiredMessages failed: %v", err)
	}

	if len(remaining) != 0 {
		t.Errorf("Expected no expired messages, got %d", len(remaining))
	}
}

func TestDeleteExpiredWithChunks(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Save expired message with chunks
	now := time.Now().UTC()
	pastTime := now.AddDate(0, 0, -1).Format("2006-01-02T15:04:05.000Z")

	msgID, _ := store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "expired with chunks", nil, "general", false,
		`[]`, `{}`, &pastTime,
	)

	embeddings, _ := embedder.Embed([]string{"test"})
	store.SaveChunk(msgID, 0, "chunk 1", embedder.Name(), embedder.Model(), embeddings[0])
	store.SaveChunk(msgID, 1, "chunk 2", embedder.Name(), embedder.Model(), embeddings[0])

	// Verify chunks exist
	chunks, err := store.GetChunks(msgID)
	if err != nil {
		t.Fatalf("GetChunks failed: %v", err)
	}

	if len(chunks) != 2 {
		t.Errorf("Expected 2 chunks, got %d", len(chunks))
	}

	// Delete expired (cascade delete should remove chunks)
	deleted, _ := store.DeleteExpired("test-user")
	if deleted != 1 {
		t.Errorf("Expected 1 deleted, got %d", deleted)
	}

	// Verify chunks are also deleted
	chunkCount, err := store.ChunkCount()
	if err != nil {
		t.Fatalf("ChunkCount failed: %v", err)
	}

	if chunkCount != 0 {
		t.Errorf("Expected 0 chunks after cascade delete, got %d", chunkCount)
	}
}

func TestDeleteByClassification(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save messages with different classifications
	store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "secret 1", nil, "secret", false,
		`[]`, `{}`, nil,
	)

	store.SaveMessage(
		"source", nil, "conv-2", nil, "msg-2",
		"user", "secret 2", nil, "secret", false,
		`[]`, `{}`, nil,
	)

	store.SaveMessage(
		"source", nil, "conv-3", nil, "msg-3",
		"user", "general", nil, "general", false,
		`[]`, `{}`, nil,
	)

	// Delete by classification
	deleted, err := store.DeleteByClassification("secret", "testing purge", "test-user")
	if err != nil {
		t.Fatalf("DeleteByClassification failed: %v", err)
	}

	if deleted != 2 {
		t.Errorf("Expected 2 deleted, got %d", deleted)
	}

	// Verify general classification remains
	msgCount, err := store.MessageCount()
	if err != nil {
		t.Fatalf("MessageCount failed: %v", err)
	}

	if msgCount != 1 {
		t.Errorf("Expected 1 message remaining, got %d", msgCount)
	}
}

func TestDeleteByClassificationEmpty(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Delete non-existent classification
	deleted, err := store.DeleteByClassification("nonexistent", "test", "user")
	if err != nil {
		t.Fatalf("DeleteByClassification failed: %v", err)
	}

	if deleted != 0 {
		t.Errorf("Expected 0 deleted for non-existent classification, got %d", deleted)
	}
}

func TestDeleteBySource(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save messages from different sources
	store.SaveMessage(
		"source-a", nil, "conv-1", nil, "msg-1",
		"user", "from a", nil, "general", false,
		`[]`, `{}`, nil,
	)

	store.SaveMessage(
		"source-a", nil, "conv-2", nil, "msg-2",
		"user", "from a", nil, "general", false,
		`[]`, `{}`, nil,
	)

	store.SaveMessage(
		"source-b", nil, "conv-3", nil, "msg-3",
		"user", "from b", nil, "general", false,
		`[]`, `{}`, nil,
	)

	// Delete by source
	deleted, err := store.DeleteBySource("source-a", "cleanup", "test-user")
	if err != nil {
		t.Fatalf("DeleteBySource failed: %v", err)
	}

	if deleted != 2 {
		t.Errorf("Expected 2 deleted, got %d", deleted)
	}

	// Verify source-b remains
	msgCount, err := store.MessageCount()
	if err != nil {
		t.Fatalf("MessageCount failed: %v", err)
	}

	if msgCount != 1 {
		t.Errorf("Expected 1 message remaining, got %d", msgCount)
	}
}

func TestDeleteByAge(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	now := time.Now().UTC()

	// Save messages with different ingestion times
	// These have computed ingested_at times, so we need to be careful
	store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "message 1", nil, "general", false,
		`[]`, `{}`, nil,
	)

	store.SaveMessage(
		"source", nil, "conv-2", nil, "msg-2",
		"user", "message 2", nil, "general", false,
		`[]`, `{}`, nil,
	)

	// Set ingested_at times manually for testing
	oldTime := now.AddDate(0, 0, -10).Format("2006-01-02T15:04:05.000Z")
	_, _ = store.db.Exec(`
		UPDATE messages SET ingested_at = ? WHERE conversation_id = ?
	`, oldTime, "conv-1")

	// Delete messages older than 5 days
	deleted, err := store.DeleteByAge(5, nil, "age cleanup", "test-user")
	if err != nil {
		t.Fatalf("DeleteByAge failed: %v", err)
	}

	if deleted != 1 {
		t.Errorf("Expected 1 deleted (older than 5 days), got %d", deleted)
	}
}

func TestDeleteByAgeWithClassification(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	now := time.Now().UTC()

	// Save messages with different classifications
	store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "secret old", nil, "secret", false,
		`[]`, `{}`, nil,
	)

	store.SaveMessage(
		"source", nil, "conv-2", nil, "msg-2",
		"user", "general old", nil, "general", false,
		`[]`, `{}`, nil,
	)

	// Set old ingestion times
	oldTime := now.AddDate(0, 0, -10).Format("2006-01-02T15:04:05.000Z")
	_, _ = store.db.Exec(`
		UPDATE messages SET ingested_at = ?
	`, oldTime)

	// Delete only secret classification older than 5 days
	classification := "secret"
	deleted, err := store.DeleteByAge(5, &classification, "age cleanup", "test-user")
	if err != nil {
		t.Fatalf("DeleteByAge failed: %v", err)
	}

	if deleted != 1 {
		t.Errorf("Expected 1 deleted, got %d", deleted)
	}

	// Verify general remains
	msgCount, err := store.MessageCount()
	if err != nil {
		t.Fatalf("MessageCount failed: %v", err)
	}

	if msgCount != 1 {
		t.Errorf("Expected 1 message remaining, got %d", msgCount)
	}
}

func TestGetDeletionHistory(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Create some deletion runs by performing deletions
	now := time.Now().UTC()
	pastTime := now.AddDate(0, 0, -1).Format("2006-01-02T15:04:05.000Z")

	store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "expired", nil, "general", false,
		`[]`, `{}`, &pastTime,
	)

	store.DeleteExpired("user1")

	// Get deletion history
	history, err := store.GetDeletionHistory(nil)
	if err != nil {
		t.Fatalf("GetDeletionHistory failed: %v", err)
	}

	if len(history) != 1 {
		t.Errorf("Expected 1 deletion run, got %d", len(history))
	}

	if history[0].Status != "complete" {
		t.Errorf("Expected status 'complete', got %s", history[0].Status)
	}

	if history[0].AuthorizedBy == nil || *history[0].AuthorizedBy != "user1" {
		var authVal string
		if history[0].AuthorizedBy != nil {
			authVal = *history[0].AuthorizedBy
		}
		t.Errorf("Expected authorized_by 'user1', got %s", authVal)
	}
}

func TestGetDeletionHistoryFilterByStatus(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	now := time.Now().UTC()
	pastTime := now.AddDate(0, 0, -1).Format("2006-01-02T15:04:05.000Z")

	store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "expired", nil, "general", false,
		`[]`, `{}`, &pastTime,
	)

	store.DeleteExpired("user1")

	// Get only complete deletions
	completeStatus := "complete"
	complete, err := store.GetDeletionHistory(&completeStatus)
	if err != nil {
		t.Fatalf("GetDeletionHistory failed: %v", err)
	}

	if len(complete) != 1 {
		t.Errorf("Expected 1 complete deletion, got %d", len(complete))
	}

	// Get only running deletions (should be empty)
	runningStatus := "running"
	running, err := store.GetDeletionHistory(&runningStatus)
	if err != nil {
		t.Fatalf("GetDeletionHistory failed: %v", err)
	}

	if len(running) != 0 {
		t.Errorf("Expected 0 running deletions, got %d", len(running))
	}
}

func TestGetDeletionStats(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	now := time.Now().UTC()
	pastTime := now.AddDate(0, 0, -1).Format("2006-01-02T15:04:05.000Z")

	// Create expired messages
	store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "expired 1", nil, "general", false,
		`[]`, `{}`, &pastTime,
	)

	store.SaveMessage(
		"source", nil, "conv-2", nil, "msg-2",
		"user", "expired 2", nil, "general", false,
		`[]`, `{}`, &pastTime,
	)

	// Delete expired
	store.DeleteExpired("user1")

	// Get stats
	stats, err := store.GetDeletionStats()
	if err != nil {
		t.Fatalf("GetDeletionStats failed: %v", err)
	}

	if stats["expiration_deleted"] != 2 {
		t.Errorf("Expected 2 expiration deletions, got %d", stats["expiration_deleted"])
	}

	if stats["expiration_runs"] != 1 {
		t.Errorf("Expected 1 expiration run, got %d", stats["expiration_runs"])
	}
}

func TestDeletionRunTracking(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save messages for deletion
	store.SaveMessage(
		"source", nil, "conv-1", nil, "msg-1",
		"user", "secret 1", nil, "secret", false,
		`[]`, `{}`, nil,
	)

	store.SaveMessage(
		"source", nil, "conv-2", nil, "msg-2",
		"user", "secret 2", nil, "secret", false,
		`[]`, `{}`, nil,
	)

	// Delete by classification
	deleted, _ := store.DeleteByClassification("secret", "security purge", "admin")

	// Verify deletion run was recorded
	history, err := store.GetDeletionHistory(nil)
	if err != nil {
		t.Fatalf("GetDeletionHistory failed: %v", err)
	}

	if len(history) != 1 {
		t.Errorf("Expected 1 deletion run, got %d", len(history))
	}

	run := history[0]
	if run.PolicyType != "classification" {
		t.Errorf("Expected policy_type 'classification', got %s", run.PolicyType)
	}

	if run.Reason != "security purge" {
		t.Errorf("Expected reason 'security purge', got %s", run.Reason)
	}

	if run.TargetCount != 2 {
		t.Errorf("Expected target_count 2, got %d", run.TargetCount)
	}

	if run.DeletedCount != deleted {
		t.Errorf("Expected deleted_count %d, got %d", deleted, run.DeletedCount)
	}

	if *run.Classification != "secret" {
		t.Errorf("Expected classification 'secret', got %s", *run.Classification)
	}

	if *run.AuthorizedBy != "admin" {
		t.Errorf("Expected authorized_by 'admin', got %s", *run.AuthorizedBy)
	}
}

func TestDeleteBySourceError(t *testing.T) {
	requireSQLite(t)
	store := setupTestDB(t)
	defer store.Close()

	// Empty source should return error
	_, err := store.DeleteBySource("", "test", "user")
	if err == nil {
		t.Error("Expected error for empty source")
	}
}

func TestDeleteByAgeNegative(t *testing.T) {
	requireSQLite(t)
	store := setupTestDB(t)
	defer store.Close()

	// Negative age should return error
	_, err := store.DeleteByAge(-1, nil, "test", "user")
	if err == nil {
		t.Error("Expected error for negative age")
	}
}

func TestDeleteMultipleSources(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	// Save from multiple sources
	for i := 1; i <= 3; i++ {
		source := "source-" + string(rune(96+i))
		for j := 1; j <= 2; j++ {
			store.SaveMessage(
				source, nil, "conv", nil, "msg"+string(rune(96+j)),
				"user", "content", nil, "general", false,
				`[]`, `{}`, nil,
			)
		}
	}

	// Delete one source
	deleted, _ := store.DeleteBySource("source-a", "cleanup", "user")

	if deleted != 2 {
		t.Errorf("Expected 2 deleted from source-a, got %d", deleted)
	}

	// Verify 4 remain
	msgCount, err := store.MessageCount()
	if err != nil {
		t.Fatalf("MessageCount failed: %v", err)
	}

	if msgCount != 4 {
		t.Errorf("Expected 4 messages remaining, got %d", msgCount)
	}
}

func TestCascadeDeleteOnDeletion(t *testing.T) {
	requireSQLite(t)
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}

	store := setupTestDB(t)
	defer store.Close()

	embedder := NewLocalHashingEmbedder(128)

	// Save messages with multiple chunks each
	for i := 1; i <= 2; i++ {
		msgID, _ := store.SaveMessage(
			"source", nil, "conv", nil, "msg"+string(rune(96+i)),
			"user", "content", nil, "general", false,
			`[]`, `{}`, nil,
		)

		embeddings, _ := embedder.Embed([]string{"chunk1", "chunk2"})
		store.SaveChunk(msgID, 0, "chunk1", embedder.Name(), embedder.Model(), embeddings[0])
		store.SaveChunk(msgID, 1, "chunk2", embedder.Name(), embedder.Model(), embeddings[1])
	}

	// Verify chunks exist
	chunkCount, _ := store.ChunkCount()
	if chunkCount != 4 {
		t.Errorf("Expected 4 chunks before deletion, got %d", chunkCount)
	}

	// Delete one source
	store.DeleteBySource("source", "test", "user")

	// Verify all chunks are deleted
	chunkCount, err := store.ChunkCount()
	if err != nil {
		t.Fatalf("ChunkCount failed: %v", err)
	}

	if chunkCount != 0 {
		t.Errorf("Expected 0 chunks after cascade delete, got %d", chunkCount)
	}
}
