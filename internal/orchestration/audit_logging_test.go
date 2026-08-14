package orchestration

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewFileBasedAuditLogger(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if logger == nil {
		t.Errorf("logger is nil")
	}

	logger.Close()
}

func TestAuditLogEntry(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	entry := &AuditEntry{
		EventType: "test-event",
		Actor:     "test-actor",
		Resource:  "test-resource",
		Action:    "test-action",
		Result:    "success",
		Severity:  "info",
		Metadata: map[string]interface{}{
			"key": "value",
		},
	}

	err = logger.Log(entry)
	if err != nil {
		t.Fatalf("failed to log entry: %v", err)
	}

	if entry.ID == "" {
		t.Errorf("entry ID should not be empty")
	}

	if entry.Timestamp.IsZero() {
		t.Errorf("entry timestamp should not be zero")
	}

	if entry.SHA256Hash == "" {
		t.Errorf("entry hash should not be empty")
	}
}

func TestAuditLogChainIntegrity(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	entry1 := &AuditEntry{
		EventType: "event1",
		Actor:     "actor1",
		Resource:  "resource1",
		Action:    "action1",
		Result:    "success",
		Severity:  "info",
	}

	entry2 := &AuditEntry{
		EventType: "event2",
		Actor:     "actor2",
		Resource:  "resource2",
		Action:    "action2",
		Result:    "success",
		Severity:  "info",
	}

	logger.Log(entry1)
	logger.Log(entry2)

	if entry1.ChainHash != "" {
		t.Errorf("first entry should have empty chain hash")
	}

	if entry2.ChainHash == "" {
		t.Errorf("second entry should have non-empty chain hash")
	}

	if entry2.ChainHash != entry1.SHA256Hash {
		t.Errorf("second entry chain hash should match first entry hash")
	}
}

func TestGetEntriesSinceTime(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	now := time.Now()

	entry1 := &AuditEntry{
		Timestamp: now.Add(-10 * time.Minute),
		EventType: "old-event",
		Actor:     "actor1",
		Resource:  "resource1",
		Action:    "action1",
		Result:    "success",
		Severity:  "info",
	}

	entry2 := &AuditEntry{
		Timestamp: now,
		EventType: "new-event",
		Actor:     "actor2",
		Resource:  "resource2",
		Action:    "action2",
		Result:    "success",
		Severity:  "info",
	}

	logger.Log(entry1)
	logger.Log(entry2)

	entries, err := logger.GetEntries(now.Add(-5*time.Minute), "")
	if err != nil {
		t.Fatalf("failed to get entries: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].EventType != "new-event" {
		t.Errorf("expected new-event, got %s", entries[0].EventType)
	}
}

func TestGetEntriesByEventType(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	logger.Log(&AuditEntry{
		EventType: "agent-dispatch",
		Actor:     "dispatcher",
		Resource:  "agent1",
		Action:    "dispatch",
		Result:    "success",
		Severity:  "info",
	})

	logger.Log(&AuditEntry{
		EventType: "agent-complete",
		Actor:     "agent1",
		Resource:  "task1",
		Action:    "complete",
		Result:    "success",
		Severity:  "info",
	})

	entries, err := logger.GetEntries(time.Now().Add(-1*time.Hour), "agent-dispatch")
	if err != nil {
		t.Fatalf("failed to get entries: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 agent-dispatch entry, got %d", len(entries))
	}

	if entries[0].EventType != "agent-dispatch" {
		t.Errorf("expected agent-dispatch, got %s", entries[0].EventType)
	}
}

func TestStructuredAuditLogger(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	sal := NewStructuredAuditLogger(logger, "TASK-001", "internal")

	input := &WorkflowInput{
		TaskID:         "TASK-001",
		Task:           "test task",
		Classification: "internal",
		ChangedFiles:   []string{"file1.go", "file2.go"},
	}

	err = sal.LogWorkflowStart(input)
	if err != nil {
		t.Fatalf("failed to log workflow start: %v", err)
	}

	entries, err := logger.GetEntries(time.Now().Add(-1*time.Minute), "workflow-start")
	if err != nil {
		t.Fatalf("failed to get entries: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 workflow-start entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Resource != "TASK-001" {
		t.Errorf("expected TASK-001, got %s", entry.Resource)
	}

	if entry.Metadata["changed_files"] != 2 {
		t.Errorf("expected 2 changed files in metadata")
	}
}

func TestAuditLogAgentDispatch(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	sal := NewStructuredAuditLogger(logger, "TASK-001", "internal")

	err = sal.LogAgentDispatch("agent-1", "code-reviewer", "plan-123")
	if err != nil {
		t.Fatalf("failed to log agent dispatch: %v", err)
	}

	entries, err := logger.GetEntries(time.Now().Add(-1*time.Minute), "agent-dispatch")
	if err != nil {
		t.Fatalf("failed to get entries: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 agent-dispatch entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Resource != "agent-1" {
		t.Errorf("expected agent-1, got %s", entry.Resource)
	}

	if entry.Metadata["role"] != "code-reviewer" {
		t.Errorf("expected code-reviewer role")
	}
}

func TestAuditLogAgentCompletion(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	sal := NewStructuredAuditLogger(logger, "TASK-001", "internal")

	err = sal.LogAgentCompletion("agent-1", "success", 5*time.Second, 3)
	if err != nil {
		t.Fatalf("failed to log agent completion: %v", err)
	}

	entries, err := logger.GetEntries(time.Now().Add(-1*time.Minute), "agent-complete")
	if err != nil {
		t.Fatalf("failed to get entries: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 agent-complete entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Duration != 5*time.Second {
		t.Errorf("expected 5s duration, got %v", entry.Duration)
	}

	if entry.Metadata["findings"] != 3 {
		t.Errorf("expected 3 findings in metadata")
	}
}

func TestAuditLogResultConsolidation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	sal := NewStructuredAuditLogger(logger, "TASK-001", "internal")

	err = sal.LogResultConsolidation(5, 4, 0.85)
	if err != nil {
		t.Fatalf("failed to log result consolidation: %v", err)
	}

	entries, err := logger.GetEntries(time.Now().Add(-1*time.Minute), "result-consolidation")
	if err != nil {
		t.Fatalf("failed to get entries: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 result-consolidation entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Metadata["total_agents"] != 5 {
		t.Errorf("expected 5 agents")
	}

	if entry.Metadata["quality_score"] != 0.85 {
		t.Errorf("expected 0.85 quality score")
	}
}

func TestAuditLogCacheOperation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	sal := NewStructuredAuditLogger(logger, "TASK-001", "internal")

	err = sal.LogCacheOperation("get", "cache:abc123", true)
	if err != nil {
		t.Fatalf("failed to log cache operation: %v", err)
	}

	entries, err := logger.GetEntries(time.Now().Add(-1*time.Minute), "cache-operation")
	if err != nil {
		t.Fatalf("failed to get entries: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 cache-operation entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Result != "hit" {
		t.Errorf("expected cache hit, got %s", entry.Result)
	}
}

func TestAuditLogError(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	sal := NewStructuredAuditLogger(logger, "TASK-001", "internal")

	testErr := os.ErrInvalid
	err = sal.LogError("agent-dispatch", "dispatcher", "agent-1", testErr)
	if err != nil {
		t.Fatalf("failed to log error: %v", err)
	}

	entries, err := logger.GetEntries(time.Now().Add(-1*time.Minute), "agent-dispatch")
	if err != nil {
		t.Fatalf("failed to get entries: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Result != "failed" {
		t.Errorf("expected failed result")
	}

	if entry.Severity != "error" {
		t.Errorf("expected error severity")
	}

	if entry.ErrorMsg == "" {
		t.Errorf("expected error message")
	}
}

func TestAuditTrail(t *testing.T) {
	trail := NewAuditTrail("TASK-001", "internal")

	entry1 := &AuditEntry{
		Timestamp: time.Now(),
		EventType: "event1",
		Actor:     "actor1",
		Resource:  "resource1",
		Action:    "action1",
		Result:    "success",
		Severity:  "info",
	}

	entry2 := &AuditEntry{
		Timestamp: time.Now().Add(1 * time.Second),
		EventType: "event2",
		Actor:     "actor2",
		Resource:  "resource2",
		Action:    "action2",
		Result:    "success",
		Severity:  "error",
	}

	trail.AddEntry(entry1)
	trail.AddEntry(entry2)

	if trail.EntryCount != 2 {
		t.Errorf("expected 2 entries, got %d", trail.EntryCount)
	}

	summary := trail.Summary()
	if summary == "" {
		t.Errorf("summary should not be empty")
	}

	if !containsString(summary, "TASK-001") {
		t.Errorf("summary should contain task ID")
	}
}

func TestAuditFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	logger.Close()

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}

	if (info.Mode() & 0077) != 0 {
		t.Errorf("audit log should only be readable by owner (0600)")
	}
}

func TestAuditLogKnowledgeRetrieval(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "audit.log")

	logger, err := NewFileBasedAuditLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	sal := NewStructuredAuditLogger(logger, "TASK-001", "internal")

	err = sal.LogKnowledgeRetrieval(3, true)
	if err != nil {
		t.Fatalf("failed to log knowledge retrieval: %v", err)
	}

	entries, err := logger.GetEntries(time.Now().Add(-1*time.Minute), "knowledge-retrieval")
	if err != nil {
		t.Fatalf("failed to get entries: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 knowledge-retrieval entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Result != "success" {
		t.Errorf("expected success result")
	}

	if entry.Metadata["source_count"] != 3 {
		t.Errorf("expected 3 sources")
	}
}

func containsString(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
