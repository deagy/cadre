package knowledge

import (
	"fmt"
	"sync"
	"time"
)

// StreamingBatchWriter provides streaming API for batch operations.
type StreamingBatchWriter struct {
	mu                sync.Mutex
	index             *HSNWIndex
	buffer            chan StreamingOperation
	bufferSize        int
	timeout           time.Duration
	errors            []StreamingError
	operationCount    int64
	successCount      int64
	failureCount      int64
	lastFlushTime     time.Time
	autoFlushInterval time.Duration
	flushTimer        *time.Timer
}

// StreamingOperation represents a single operation in a batch.
type StreamingOperation struct {
	Type      string            // "delete", "undelete", "update"
	MessageID string
	Embedding []float32         // For update operations
	Timestamp time.Time
}

// StreamingError tracks operation failures.
type StreamingError struct {
	Operation StreamingOperation
	Error     string
	Timestamp time.Time
}

// StreamingStats provides metrics for streaming operations.
type StreamingStats struct {
	TotalOperations  int64
	SuccessCount     int64
	FailureCount     int64
	SuccessRate      float64
	AvgLatencyMs     float64
	ThroughputPerSec float64
	BufferUtilization float64
}

// NewStreamingBatchWriter creates a streaming batch writer.
func NewStreamingBatchWriter(index *HSNWIndex, bufferSize int) *StreamingBatchWriter {
	return &StreamingBatchWriter{
		index:             index,
		buffer:            make(chan StreamingOperation, bufferSize),
		bufferSize:        bufferSize,
		timeout:           30 * time.Second,
		errors:            make([]StreamingError, 0),
		lastFlushTime:     time.Now(),
		autoFlushInterval: 5 * time.Second,
	}
}

// Delete queues a delete operation.
func (sbw *StreamingBatchWriter) Delete(messageID string) error {
	sbw.mu.Lock()
	defer sbw.mu.Unlock()

	if sbw.buffer == nil {
		return fmt.Errorf("writer is closed")
	}

	op := StreamingOperation{
		Type:      "delete",
		MessageID: messageID,
		Timestamp: time.Now(),
	}

	select {
	case sbw.buffer <- op:
		sbw.operationCount++
		return nil
	case <-time.After(sbw.timeout):
		return fmt.Errorf("buffer full, timeout waiting")
	}
}

// Undelete queues an undelete operation.
func (sbw *StreamingBatchWriter) Undelete(messageID string) error {
	sbw.mu.Lock()
	defer sbw.mu.Unlock()

	if sbw.buffer == nil {
		return fmt.Errorf("writer is closed")
	}

	op := StreamingOperation{
		Type:      "undelete",
		MessageID: messageID,
		Timestamp: time.Now(),
	}

	select {
	case sbw.buffer <- op:
		sbw.operationCount++
		return nil
	case <-time.After(sbw.timeout):
		return fmt.Errorf("buffer full, timeout waiting")
	}
}

// Update queues an update operation.
func (sbw *StreamingBatchWriter) Update(messageID string, embedding []float32) error {
	sbw.mu.Lock()
	defer sbw.mu.Unlock()

	if sbw.buffer == nil {
		return fmt.Errorf("writer is closed")
	}

	if len(embedding) == 0 {
		return fmt.Errorf("empty embedding")
	}

	op := StreamingOperation{
		Type:      "update",
		MessageID: messageID,
		Embedding: embedding,
		Timestamp: time.Now(),
	}

	select {
	case sbw.buffer <- op:
		sbw.operationCount++
		return nil
	case <-time.After(sbw.timeout):
		return fmt.Errorf("buffer full, timeout waiting")
	}
}

// Flush processes all buffered operations.
func (sbw *StreamingBatchWriter) Flush() error {
	sbw.mu.Lock()

	if sbw.buffer == nil {
		sbw.mu.Unlock()
		return fmt.Errorf("writer is closed")
	}

	// Drain buffer
	ops := make([]StreamingOperation, 0)
	timeout := time.After(1 * time.Second)

	draining := true
	for draining {
		select {
		case op := <-sbw.buffer:
			ops = append(ops, op)
		case <-timeout:
			draining = false
		default:
			draining = false
		}
	}

	sbw.mu.Unlock()

	// Process operations
	for _, op := range ops {
		err := sbw.processOperation(op)
		if err != nil {
			sbw.mu.Lock()
			sbw.errors = append(sbw.errors, StreamingError{
				Operation: op,
				Error:     err.Error(),
				Timestamp: time.Now(),
			})
			sbw.failureCount++
			sbw.mu.Unlock()
		} else {
			sbw.mu.Lock()
			sbw.successCount++
			sbw.mu.Unlock()
		}
	}

	sbw.mu.Lock()
	sbw.lastFlushTime = time.Now()
	sbw.mu.Unlock()

	return nil
}

// processOperation executes a single operation.
func (sbw *StreamingBatchWriter) processOperation(op StreamingOperation) error {
	switch op.Type {
	case "delete":
		return sbw.index.Delete(op.MessageID)
	case "undelete":
		return sbw.index.Undelete(op.MessageID)
	case "update":
		return sbw.index.Update(op.MessageID, op.Embedding)
	default:
		return fmt.Errorf("unknown operation type: %s", op.Type)
	}
}

// Close flushes and closes the writer.
func (sbw *StreamingBatchWriter) Close() error {
	sbw.mu.Lock()
	defer sbw.mu.Unlock()

	if sbw.buffer == nil {
		return nil // Already closed
	}

	close(sbw.buffer)
	sbw.buffer = nil

	return nil
}

// GetStats returns streaming statistics.
func (sbw *StreamingBatchWriter) GetStats() *StreamingStats {
	sbw.mu.Lock()
	defer sbw.mu.Unlock()

	stats := &StreamingStats{
		TotalOperations:    sbw.operationCount,
		SuccessCount:       sbw.successCount,
		FailureCount:       sbw.failureCount,
		BufferUtilization:  float64(len(sbw.buffer)) / float64(sbw.bufferSize) * 100,
	}

	if sbw.operationCount > 0 {
		stats.SuccessRate = float64(sbw.successCount) / float64(sbw.operationCount) * 100
	}

	return stats
}

// GetErrors returns accumulated errors.
func (sbw *StreamingBatchWriter) GetErrors() []StreamingError {
	sbw.mu.Lock()
	defer sbw.mu.Unlock()

	errors := make([]StreamingError, len(sbw.errors))
	copy(errors, sbw.errors)
	return errors
}

// ClearErrors resets error list.
func (sbw *StreamingBatchWriter) ClearErrors() {
	sbw.mu.Lock()
	defer sbw.mu.Unlock()

	sbw.errors = make([]StreamingError, 0)
}

// StreamingIterator provides streaming read access to operations.
type StreamingIterator struct {
	mu        sync.RWMutex
	operations chan StreamingOperation
	closed    bool
	count     int64
}

// NewStreamingIterator creates a streaming iterator.
func NewStreamingIterator() *StreamingIterator {
	return &StreamingIterator{
		operations: make(chan StreamingOperation, 1000),
		closed:     false,
	}
}

// Emit sends an operation to the iterator.
func (si *StreamingIterator) Emit(op StreamingOperation) error {
	si.mu.RLock()
	if si.closed {
		si.mu.RUnlock()
		return fmt.Errorf("iterator is closed")
	}
	si.mu.RUnlock()

	select {
	case si.operations <- op:
		si.mu.Lock()
		si.count++
		si.mu.Unlock()
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("emit timeout")
	}
}

// Next gets the next operation (blocking).
func (si *StreamingIterator) Next() (StreamingOperation, error) {
	si.mu.RLock()
	if si.closed {
		si.mu.RUnlock()
		return StreamingOperation{}, fmt.Errorf("iterator is closed")
	}
	si.mu.RUnlock()

	op, ok := <-si.operations
	if !ok {
		return StreamingOperation{}, fmt.Errorf("iterator closed")
	}

	return op, nil
}

// Close closes the iterator.
func (si *StreamingIterator) Close() error {
	si.mu.Lock()
	defer si.mu.Unlock()

	if si.closed {
		return nil
	}

	si.closed = true
	close(si.operations)

	return nil
}

// Count returns total operations processed.
func (si *StreamingIterator) Count() int64 {
	si.mu.RLock()
	defer si.mu.RUnlock()

	return si.count
}
