package knowledge

import (
	"fmt"
	"testing"
	"time"
)

func TestStreamingBatchWriterCreation(t *testing.T) {
	idx := NewHSNWIndex(16, 200)
	writer := NewStreamingBatchWriter(idx, 1000)

	if writer == nil {
		t.Error("Failed to create streaming writer")
	}

	if writer.bufferSize != 1000 {
		t.Errorf("Expected buffer size 1000, got %d", writer.bufferSize)
	}
}

func TestStreamingDelete(t *testing.T) {
	idx := NewHSNWIndex(16, 200)
	idx.Insert("msg-1", []float32{1.0, 0.0})

	writer := NewStreamingBatchWriter(idx, 100)
	defer writer.Close()

	err := writer.Delete("msg-1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if writer.operationCount != 1 {
		t.Errorf("Expected 1 operation, got %d", writer.operationCount)
	}
}

func TestStreamingUndelete(t *testing.T) {
	idx := NewHSNWIndex(16, 200)
	idx.Insert("msg-1", []float32{1.0, 0.0})
	idx.Delete("msg-1")

	writer := NewStreamingBatchWriter(idx, 100)
	defer writer.Close()

	err := writer.Undelete("msg-1")
	if err != nil {
		t.Fatalf("Undelete failed: %v", err)
	}

	if writer.operationCount != 1 {
		t.Errorf("Expected 1 operation, got %d", writer.operationCount)
	}
}

func TestStreamingUpdate(t *testing.T) {
	idx := NewHSNWIndex(16, 200)
	idx.Insert("msg-1", []float32{1.0, 0.0})

	writer := NewStreamingBatchWriter(idx, 100)
	defer writer.Close()

	err := writer.Update("msg-1", []float32{0.5, 0.5})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if writer.operationCount != 1 {
		t.Errorf("Expected 1 operation, got %d", writer.operationCount)
	}
}

func TestStreamingFlush(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert vectors
	for i := 1; i <= 5; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 5.0, 0.5})
	}

	writer := NewStreamingBatchWriter(idx, 100)
	defer writer.Close()

	// Queue operations
	for i := 1; i <= 3; i++ {
		writer.Delete(fmt.Sprintf("msg-%d", i))
	}

	// Flush
	err := writer.Flush()
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	if writer.successCount != 3 {
		t.Errorf("Expected 3 successes, got %d", writer.successCount)
	}
}

func TestStreamingStats(t *testing.T) {
	idx := NewHSNWIndex(16, 200)
	idx.Insert("msg-1", []float32{1.0, 0.0})

	writer := NewStreamingBatchWriter(idx, 100)
	defer writer.Close()

	writer.Delete("msg-1")
	writer.Flush()

	stats := writer.GetStats()

	if stats.TotalOperations != 1 {
		t.Errorf("Expected 1 operation, got %d", stats.TotalOperations)
	}

	if stats.SuccessCount != 1 {
		t.Errorf("Expected 1 success, got %d", stats.SuccessCount)
	}

	if stats.SuccessRate != 100.0 {
		t.Errorf("Expected 100%% success rate, got %.1f%%", stats.SuccessRate)
	}
}

func TestStreamingClosed(t *testing.T) {
	idx := NewHSNWIndex(16, 200)
	writer := NewStreamingBatchWriter(idx, 100)

	writer.Close()

	// Operations should fail on closed writer
	err := writer.Delete("msg-1")
	if err == nil {
		t.Error("Expected error for closed writer")
	}
}

func TestStreamingErrorTracking(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	writer := NewStreamingBatchWriter(idx, 100)
	defer writer.Close()

	// Try to delete non-existent
	writer.Delete("msg-nonexistent")

	writer.Flush()

	errors := writer.GetErrors()
	if len(errors) != 1 {
		t.Errorf("Expected 1 error, got %d", len(errors))
	}

	if writer.failureCount != 1 {
		t.Errorf("Expected 1 failure, got %d", writer.failureCount)
	}
}

func TestStreamingBulkOperations(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert 100 vectors
	for i := 1; i <= 100; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 100.0, 0.5})
	}

	writer := NewStreamingBatchWriter(idx, 1000)
	defer writer.Close()

	// Stream 50 deletes
	for i := 1; i <= 50; i++ {
		writer.Delete(fmt.Sprintf("msg-%d", i))
	}

	// Stream 30 updates
	for i := 51; i <= 80; i++ {
		writer.Update(fmt.Sprintf("msg-%d", i), []float32{0.1, 0.9})
	}

	// Flush all
	err := writer.Flush()
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	stats := writer.GetStats()
	if stats.TotalOperations != 80 {
		t.Errorf("Expected 80 operations, got %d", stats.TotalOperations)
	}

	if stats.SuccessCount != 80 {
		t.Errorf("Expected 80 successes, got %d", stats.SuccessCount)
	}
}

func TestStreamingIterator(t *testing.T) {
	iter := NewStreamingIterator()
	defer iter.Close()

	// Emit operations
	op1 := StreamingOperation{
		Type:      "delete",
		MessageID: "msg-1",
		Timestamp: time.Now(),
	}

	err := iter.Emit(op1)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}

	// Read operation
	received, err := iter.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if received.MessageID != "msg-1" {
		t.Errorf("Expected msg-1, got %s", received.MessageID)
	}

	if iter.Count() != 1 {
		t.Errorf("Expected count 1, got %d", iter.Count())
	}
}

func TestStreamingIteratorClosed(t *testing.T) {
	iter := NewStreamingIterator()

	iter.Close()

	// Emit should fail on closed iterator
	err := iter.Emit(StreamingOperation{
		Type:      "delete",
		MessageID: "msg-1",
	})

	if err == nil {
		t.Error("Expected error for closed iterator")
	}
}

func TestStreamingHighThroughput(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	// Insert 1000 vectors
	for i := 1; i <= 1000; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 1000.0, 0.5})
	}

	writer := NewStreamingBatchWriter(idx, 5000)
	defer writer.Close()

	// Stream 500 operations
	for i := 1; i <= 500; i++ {
		if i%2 == 0 {
			writer.Delete(fmt.Sprintf("msg-%d", i))
		} else {
			writer.Update(fmt.Sprintf("msg-%d", i), []float32{0.1, 0.9})
		}
	}

	start := time.Now()
	writer.Flush()
	elapsed := time.Since(start)

	stats := writer.GetStats()
	if stats.TotalOperations != 500 {
		t.Errorf("Expected 500 operations, got %d", stats.TotalOperations)
	}

	// Should complete quickly
	if elapsed > 1*time.Second {
		t.Logf("Warning: high throughput test took %v", elapsed)
	}
}

func TestStreamingClearErrors(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	writer := NewStreamingBatchWriter(idx, 100)
	defer writer.Close()

	// Generate error
	writer.Delete("msg-nonexistent")
	writer.Flush()

	errors := writer.GetErrors()
	if len(errors) != 1 {
		t.Error("Expected 1 error before clear")
	}

	writer.ClearErrors()

	errors = writer.GetErrors()
	if len(errors) != 0 {
		t.Error("Expected 0 errors after clear")
	}
}

func TestStreamingMultipleFlushes(t *testing.T) {
	idx := NewHSNWIndex(16, 200)

	for i := 1; i <= 10; i++ {
		idx.Insert(fmt.Sprintf("msg-%d", i), []float32{float32(i) / 10.0, 0.5})
	}

	writer := NewStreamingBatchWriter(idx, 100)
	defer writer.Close()

	// First batch
	for i := 1; i <= 3; i++ {
		writer.Delete(fmt.Sprintf("msg-%d", i))
	}
	writer.Flush()

	if writer.successCount != 3 {
		t.Error("First flush failed")
	}

	// Second batch
	for i := 4; i <= 6; i++ {
		writer.Delete(fmt.Sprintf("msg-%d", i))
	}
	writer.Flush()

	if writer.successCount != 6 {
		t.Errorf("Expected 6 total successes, got %d", writer.successCount)
	}
}
