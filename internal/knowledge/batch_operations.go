package knowledge

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// BatchMessage represents a message to be bulk imported.
type BatchMessage struct {
	MessageID      string                 `json:"message_id"`
	Content        string                 `json:"content"`
	Classification string                 `json:"classification"`
	Source         string                 `json:"source"`
	Embedding      []float32              `json:"embedding,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// BatchImportResult tracks import progress and results.
type BatchImportResult struct {
	TotalRead    int64
	SuccessCount int64
	FailureCount int64
	SkippedCount int64
	StartTime    time.Time
	EndTime      time.Time
	Errors       []string
	BatchSize    int
	DryRun       bool
}

// BulkDeleteResult tracks deletion progress and results.
type BulkDeleteResult struct {
	TotalMatched int64
	DeletedCount int64
	FailureCount int64
	StartTime    time.Time
	EndTime      time.Time
	Errors       []string
	DryRun       bool
	FilterUsed   string
}

// BulkUpdateResult tracks update progress and results.
type BulkUpdateResult struct {
	TotalMatched int64
	UpdatedCount int64
	FailureCount int64
	StartTime    time.Time
	EndTime      time.Time
	Errors       []string
	DryRun       bool
	ChangeCount  int // Number of fields changed per message
}

// BatchOperations provides bulk operation capabilities. It holds no state
// today -- every method operates purely on its arguments -- so it needs no
// lock; add one alongside the first shared field.
type BatchOperations struct{}

// NewBatchOperations creates a batch operations manager.
func NewBatchOperations() *BatchOperations {
	return &BatchOperations{}
}

// ImportFromFile imports messages from a file in batch.
func (bo *BatchOperations) ImportFromFile(filepath string, format string, batchSize int, skipErrors bool, dryRun bool) (*BatchImportResult, error) {
	result := &BatchImportResult{
		StartTime: time.Now(),
		BatchSize: batchSize,
		DryRun:    dryRun,
		Errors:    []string{},
	}

	// Read file based on format
	var messages []BatchMessage
	var err error

	switch strings.ToLower(format) {
	case "json":
		messages, err = bo.readJSONFile(filepath)
	case "jsonl":
		messages, err = bo.readJSONLFile(filepath)
	case "csv":
		messages, err = bo.readCSVFile(filepath)
	default:
		return result, fmt.Errorf("unsupported format: %s", format)
	}

	if err != nil {
		return result, err
	}

	result.TotalRead = int64(len(messages))

	// Process in batches
	for i := 0; i < len(messages); i += batchSize {
		end := i + batchSize
		if end > len(messages) {
			end = len(messages)
		}

		batch := messages[i:end]
		for _, msg := range batch {
			if err := bo.validateMessage(&msg); err != nil {
				if skipErrors {
					result.SkippedCount++
					result.Errors = append(result.Errors, fmt.Sprintf("msg %s: %v", msg.MessageID, err))
					continue
				}
				result.FailureCount++
				result.Errors = append(result.Errors, fmt.Sprintf("msg %s: %v", msg.MessageID, err))
				continue
			}

			// A dry run and a real run both count the message as a success
			// today: the database insert this branch is meant to guard is not
			// implemented yet, so there is nothing for dryRun to skip.
			result.SuccessCount++
		}
	}

	result.EndTime = time.Now()
	return result, nil
}

// DeleteByFilter deletes messages matching filter criteria in batch.
func (bo *BatchOperations) DeleteByFilter(classification string, source string, olderThanDays int, batchSize int, dryRun bool) (*BulkDeleteResult, error) {
	result := &BulkDeleteResult{
		StartTime: time.Now(),
		DryRun:    dryRun,
		Errors:    []string{},
	}

	// Build filter description
	filters := []string{}
	if classification != "" {
		filters = append(filters, fmt.Sprintf("classification=%s", classification))
	}
	if source != "" {
		filters = append(filters, fmt.Sprintf("source=%s", source))
	}
	if olderThanDays > 0 {
		filters = append(filters, fmt.Sprintf("older_than=%d_days", olderThanDays))
	}
	result.FilterUsed = strings.Join(filters, " AND ")

	if result.FilterUsed == "" {
		return result, fmt.Errorf("at least one filter required (classification, source, or older_than)")
	}

	// Simulate finding and deleting messages
	// In real implementation, would query database with filters
	result.TotalMatched = 100 // Placeholder

	if !dryRun {
		result.DeletedCount = result.TotalMatched
	}

	result.EndTime = time.Now()
	return result, nil
}

// UpdateByFilter updates messages matching filter criteria in batch.
func (bo *BatchOperations) UpdateByFilter(filter string, changes map[string]interface{}, batchSize int, dryRun bool) (*BulkUpdateResult, error) {
	result := &BulkUpdateResult{
		StartTime:   time.Now(),
		DryRun:      dryRun,
		ChangeCount: len(changes),
		Errors:      []string{},
	}

	if filter == "" {
		return result, fmt.Errorf("filter required for bulk update")
	}

	if len(changes) == 0 {
		return result, fmt.Errorf("no changes specified")
	}

	// Simulate finding and updating messages
	// In real implementation, would query database with filter
	result.TotalMatched = 50 // Placeholder

	if !dryRun {
		result.UpdatedCount = result.TotalMatched
	}

	result.EndTime = time.Now()
	return result, nil
}

// Helper methods

func (bo *BatchOperations) readJSONFile(filepath string) ([]BatchMessage, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}

	var messages []BatchMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}

	return messages, nil
}

func (bo *BatchOperations) readJSONLFile(filepath string) ([]BatchMessage, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var messages []BatchMessage
	decoder := json.NewDecoder(file)

	for decoder.More() {
		var msg BatchMessage
		if err := decoder.Decode(&msg); err != nil {
			return messages, fmt.Errorf("error decoding line: %w", err)
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

func (bo *BatchOperations) readCSVFile(filepath string) ([]BatchMessage, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("CSV file must have header and at least one data row")
	}

	headers := records[0]
	headerMap := make(map[string]int)
	for i, h := range headers {
		headerMap[h] = i
	}

	var messages []BatchMessage
	for _, record := range records[1:] {
		msg := BatchMessage{
			Metadata: make(map[string]interface{}),
		}

		if idx, ok := headerMap["message_id"]; ok && idx < len(record) {
			msg.MessageID = record[idx]
		}
		if idx, ok := headerMap["content"]; ok && idx < len(record) {
			msg.Content = record[idx]
		}
		if idx, ok := headerMap["classification"]; ok && idx < len(record) {
			msg.Classification = record[idx]
		}
		if idx, ok := headerMap["source"]; ok && idx < len(record) {
			msg.Source = record[idx]
		}

		messages = append(messages, msg)
	}

	return messages, nil
}

func (bo *BatchOperations) validateMessage(msg *BatchMessage) error {
	if msg.MessageID == "" {
		return fmt.Errorf("message_id required")
	}
	if msg.Content == "" {
		return fmt.Errorf("content required")
	}
	if msg.Classification == "" {
		return fmt.Errorf("classification required")
	}
	return nil
}

// GetDuration returns operation duration.
func (result *BatchImportResult) GetDuration() time.Duration {
	return result.EndTime.Sub(result.StartTime)
}

// GetDuration returns operation duration.
func (result *BulkDeleteResult) GetDuration() time.Duration {
	return result.EndTime.Sub(result.StartTime)
}

// GetDuration returns operation duration.
func (result *BulkUpdateResult) GetDuration() time.Duration {
	return result.EndTime.Sub(result.StartTime)
}

// GetSuccessRate returns percentage of successful operations.
func (result *BatchImportResult) GetSuccessRate() float64 {
	if result.TotalRead == 0 {
		return 0
	}
	return float64(result.SuccessCount) / float64(result.TotalRead) * 100
}

// GetSuccessRate returns percentage of successful operations.
func (result *BulkDeleteResult) GetSuccessRate() float64 {
	if result.TotalMatched == 0 {
		return 0
	}
	return float64(result.DeletedCount) / float64(result.TotalMatched) * 100
}

// GetSuccessRate returns percentage of successful operations.
func (result *BulkUpdateResult) GetSuccessRate() float64 {
	if result.TotalMatched == 0 {
		return 0
	}
	return float64(result.UpdatedCount) / float64(result.TotalMatched) * 100
}

// GetThroughput returns operations per second.
func (result *BatchImportResult) GetThroughput() float64 {
	duration := result.GetDuration().Seconds()
	if duration == 0 {
		return 0
	}
	return float64(result.SuccessCount) / duration
}

// GetThroughput returns operations per second.
func (result *BulkDeleteResult) GetThroughput() float64 {
	duration := result.GetDuration().Seconds()
	if duration == 0 {
		return 0
	}
	return float64(result.DeletedCount) / duration
}

// GetThroughput returns operations per second.
func (result *BulkUpdateResult) GetThroughput() float64 {
	duration := result.GetDuration().Seconds()
	if duration == 0 {
		return 0
	}
	return float64(result.UpdatedCount) / duration
}
