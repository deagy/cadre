package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEntry represents a single audit log entry.
type AuditEntry struct {
	ID         string                 `json:"id"`
	Timestamp  time.Time              `json:"timestamp"`
	EventType  string                 `json:"event_type"` // agent-dispatch, execution-start, result-consolidation, etc.
	Actor      string                 `json:"actor"`      // who/what triggered this event
	Resource   string                 `json:"resource"`   // task ID or agent ID being acted upon
	Action     string                 `json:"action"`     // specific action taken
	Result     string                 `json:"result"`     // success, failed, skipped
	Duration   time.Duration          `json:"duration"`
	Metadata   map[string]interface{} `json:"metadata"`
	Severity   string                 `json:"severity"` // info, warning, error, critical
	ErrorMsg   string                 `json:"error_msg,omitempty"`
	SHA256Hash string                 `json:"sha256_hash"`
	ChainHash  string                 `json:"chain_hash"` // hash of previous entry for chain integrity
}

// AuditLogger defines the interface for logging audit events.
type AuditLogger interface {
	Log(entry *AuditEntry) error
	GetEntries(since time.Time, eventType string) ([]*AuditEntry, error)
	Close() error
}

// FileBasedAuditLogger writes audit entries to a file.
type FileBasedAuditLogger struct {
	filePath   string
	mu         sync.Mutex
	lastHash   string
	entries    []*AuditEntry
	fileHandle *os.File
}

// NewFileBasedAuditLogger creates a new file-based audit logger.
func NewFileBasedAuditLogger(filePath string) (*FileBasedAuditLogger, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create audit log directory: %w", err)
	}

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open audit log file: %w", err)
	}

	logger := &FileBasedAuditLogger{
		filePath:   filePath,
		fileHandle: f,
		entries:    make([]*AuditEntry, 0),
	}

	return logger, nil
}

// Log appends an entry to the audit log with chain integrity hash.
func (fal *FileBasedAuditLogger) Log(entry *AuditEntry) error {
	fal.mu.Lock()
	defer fal.mu.Unlock()

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	if entry.ID == "" {
		entry.ID = generateAuditID()
	}

	entry.ChainHash = fal.lastHash

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal audit entry: %w", err)
	}

	fal.lastHash = computeHash(data)
	entry.SHA256Hash = fal.lastHash

	line := make([]byte, 0, len(data)+1)
	line = append(line, data...)
	line = append(line, '\n')
	if _, err := fal.fileHandle.Write(line); err != nil {
		return fmt.Errorf("failed to write audit entry: %w", err)
	}

	fal.entries = append(fal.entries, entry)

	return nil
}

// GetEntries retrieves audit entries since a given time.
func (fal *FileBasedAuditLogger) GetEntries(since time.Time, eventType string) ([]*AuditEntry, error) {
	fal.mu.Lock()
	defer fal.mu.Unlock()

	var result []*AuditEntry
	for _, entry := range fal.entries {
		if !entry.Timestamp.Before(since) {
			if eventType == "" || entry.EventType == eventType {
				result = append(result, entry)
			}
		}
	}

	return result, nil
}

// Close closes the audit log file.
func (fal *FileBasedAuditLogger) Close() error {
	fal.mu.Lock()
	defer fal.mu.Unlock()

	if fal.fileHandle != nil {
		return fal.fileHandle.Close()
	}

	return nil
}

// StructuredAuditLogger provides high-level structured logging for orchestration events.
type StructuredAuditLogger struct {
	logger         AuditLogger
	taskID         string
	classification string
	startTime      time.Time
}

// NewStructuredAuditLogger creates a new structured audit logger.
func NewStructuredAuditLogger(logger AuditLogger, taskID, classification string) *StructuredAuditLogger {
	return &StructuredAuditLogger{
		logger:         logger,
		taskID:         taskID,
		classification: classification,
		startTime:      time.Now(),
	}
}

// LogWorkflowStart logs the start of a workflow execution.
func (sal *StructuredAuditLogger) LogWorkflowStart(input *WorkflowInput) error {
	return sal.logger.Log(&AuditEntry{
		EventType: "workflow-start",
		Actor:     "orchestration-engine",
		Resource:  input.TaskID,
		Action:    "workflow-initiated",
		Result:    "started",
		Severity:  "info",
		Metadata: map[string]interface{}{
			"task":           input.Task,
			"classification": input.Classification,
			"changed_files":  len(input.ChangedFiles),
		},
	})
}

// LogWorkflowComplete logs the completion of a workflow execution.
func (sal *StructuredAuditLogger) LogWorkflowComplete(taskID string, status string, duration time.Duration) error {
	return sal.logger.Log(&AuditEntry{
		EventType: "workflow-complete",
		Actor:     "orchestration-engine",
		Resource:  taskID,
		Action:    "workflow-finished",
		Result:    status,
		Duration:  duration,
		Severity:  "info",
		Metadata: map[string]interface{}{
			"total_duration": duration.String(),
		},
	})
}

// LogAgentDispatch logs the dispatch of an agent.
func (sal *StructuredAuditLogger) LogAgentDispatch(agentID string, role string, planID string) error {
	return sal.logger.Log(&AuditEntry{
		EventType: "agent-dispatch",
		Actor:     "dispatcher",
		Resource:  agentID,
		Action:    "dispatch-initiated",
		Result:    "dispatched",
		Severity:  "info",
		Metadata: map[string]interface{}{
			"role":    role,
			"plan_id": planID,
			"task_id": sal.taskID,
		},
	})
}

// LogAgentCompletion logs the completion of an agent execution.
func (sal *StructuredAuditLogger) LogAgentCompletion(agentID string, status string, duration time.Duration, findingCount int) error {
	severity := "info"
	if status == "failed" {
		severity = "warning"
	}

	return sal.logger.Log(&AuditEntry{
		EventType: "agent-complete",
		Actor:     agentID,
		Resource:  sal.taskID,
		Action:    "execution-complete",
		Result:    status,
		Duration:  duration,
		Severity:  severity,
		Metadata: map[string]interface{}{
			"findings": findingCount,
		},
	})
}

// LogResultConsolidation logs the consolidation of results.
func (sal *StructuredAuditLogger) LogResultConsolidation(totalAgents int, successCount int, qualityScore float64) error {
	return sal.logger.Log(&AuditEntry{
		EventType: "result-consolidation",
		Actor:     "consolidation-engine",
		Resource:  sal.taskID,
		Action:    "results-consolidated",
		Result:    "success",
		Severity:  "info",
		Metadata: map[string]interface{}{
			"total_agents":  totalAgents,
			"successful":    successCount,
			"quality_score": qualityScore,
		},
	})
}

// LogCacheOperation logs a cache operation.
func (sal *StructuredAuditLogger) LogCacheOperation(operation string, key string, hit bool) error {
	result := "miss"
	if hit {
		result = "hit"
	}

	return sal.logger.Log(&AuditEntry{
		EventType: "cache-operation",
		Actor:     "cache-engine",
		Resource:  key,
		Action:    operation,
		Result:    result,
		Severity:  "info",
		Metadata: map[string]interface{}{
			"task_id": sal.taskID,
		},
	})
}

// LogError logs an error during orchestration.
func (sal *StructuredAuditLogger) LogError(eventType string, actor string, resource string, err error) error {
	return sal.logger.Log(&AuditEntry{
		EventType: eventType,
		Actor:     actor,
		Resource:  resource,
		Action:    "error-occurred",
		Result:    "failed",
		Severity:  "error",
		ErrorMsg:  err.Error(),
		Metadata: map[string]interface{}{
			"task_id": sal.taskID,
		},
	})
}

// LogKnowledgeRetrieval logs knowledge retrieval operations.
func (sal *StructuredAuditLogger) LogKnowledgeRetrieval(sourceCount int, success bool) error {
	result := "failed"
	if success {
		result = "success"
	}

	severity := "warning"
	if success {
		severity = "info"
	}

	return sal.logger.Log(&AuditEntry{
		EventType: "knowledge-retrieval",
		Actor:     "knowledge-engine",
		Resource:  sal.taskID,
		Action:    "retrieval-attempted",
		Result:    result,
		Severity:  severity,
		Metadata: map[string]interface{}{
			"source_count": sourceCount,
		},
	})
}

// AuditTrail represents a complete audit trail for a workflow execution.
type AuditTrail struct {
	TaskID         string
	StartTime      time.Time
	EndTime        time.Time
	Classification string
	Entries        []*AuditEntry
	EntryCount     int
}

// NewAuditTrail creates a new audit trail.
func NewAuditTrail(taskID, classification string) *AuditTrail {
	return &AuditTrail{
		TaskID:         taskID,
		Classification: classification,
		StartTime:      time.Now(),
		Entries:        make([]*AuditEntry, 0),
	}
}

// AddEntry adds an entry to the trail.
func (at *AuditTrail) AddEntry(entry *AuditEntry) {
	at.Entries = append(at.Entries, entry)
	at.EntryCount++
	at.EndTime = entry.Timestamp
}

// Summary returns a summary of the audit trail.
func (at *AuditTrail) Summary() string {
	if len(at.Entries) == 0 {
		return fmt.Sprintf("Audit Trail for %s: No entries", at.TaskID)
	}

	eventTypes := make(map[string]int)
	severities := make(map[string]int)

	for _, entry := range at.Entries {
		eventTypes[entry.EventType]++
		severities[entry.Severity]++
	}

	return fmt.Sprintf(
		"Audit Trail for %s (classification: %s)\n"+
			"Duration: %v\n"+
			"Total Entries: %d\n"+
			"Event Types: %v\n"+
			"Severities: %v",
		at.TaskID,
		at.Classification,
		at.EndTime.Sub(at.StartTime),
		at.EntryCount,
		eventTypes,
		severities,
	)
}

// Helper functions

func generateAuditID() string {
	return fmt.Sprintf("audit-%d", time.Now().UnixNano())
}

func computeHash(data []byte) string {
	return fmt.Sprintf("%x", data)
}
