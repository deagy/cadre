package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Integration tests for Python interoperability and cross-language workflows.
// These tests verify that the Go CLI can be invoked via subprocess (as Python would do)
// and produce correct results.

func TestPythonInteropIngestAndSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-interop.db")

	// Simulate Python creating JSON messages and piping to Go CLI
	messages := `{"message_id": "py-msg-1", "conversation_id": "python-conv", "role": "user", "content": "Python script message"}
{"message_id": "py-msg-2", "conversation_id": "python-conv", "role": "assistant", "content": "Python helper response"}
`

	// Test ingest via subprocess (like Python would call it)
	cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
		"--source", "python-script",
		"--classification", "general")

	cmd.Stdin = bytes.NewBufferString(messages)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ingest failed: %v\nOutput: %s", err, output)
	}

	if !bytes.Contains(output, []byte("Ingested 2 messages")) {
		t.Errorf("Expected ingest confirmation, got: %s", output)
	}

	// Test search via subprocess and parse JSON output
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "search",
		"--classification", "general",
		"--json",
		"Python")

	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("search failed: %v\nOutput: %s", err, output)
	}

	var searchResult map[string]interface{}
	if err := json.Unmarshal(output, &searchResult); err != nil {
		t.Fatalf("cannot parse search JSON: %v\nOutput: %s", err, output)
	}

	count, ok := searchResult["count"].(float64)
	if !ok || int(count) == 0 {
		t.Errorf("Expected search results, got: %v", searchResult)
	}
}

func TestPythonInteropJSONIngestion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-json-ingest.db")

	// Create test messages in various formats that Python might generate
	type PyMessage struct {
		MessageID      string `json:"message_id"`
		ConversationID string `json:"conversation_id"`
		ConversationTitle *string `json:"conversation_title,omitempty"`
		Role           string `json:"role"`
		Content        string `json:"content"`
	}

	messages := []PyMessage{
		{
			MessageID:      "py-1",
			ConversationID: "py-conv-1",
			Role:           "user",
			Content:        "First Python-generated message",
		},
		{
			MessageID:      "py-2",
			ConversationID: "py-conv-1",
			Role:           "assistant",
			Content:        "Response from Python-managed conversation",
		},
		{
			MessageID:      "py-3",
			ConversationID: "py-conv-2",
			ConversationTitle: strPtr("Python Conversation 2"),
			Role:           "user",
			Content:        "Another message from Python script",
		},
	}

	// Build JSON lines
	var msgLines bytes.Buffer
	for _, msg := range messages {
		msgJSON, _ := json.Marshal(msg)
		msgLines.Write(msgJSON)
		msgLines.WriteString("\n")
	}

	// Ingest via Go CLI
	cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
		"--source", "python-app",
		"--classification", "technical")

	cmd.Stdin = &msgLines
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ingest failed: %v\nOutput: %s", err, output)
	}

	if !bytes.Contains(output, []byte("Ingested 3 messages")) {
		t.Errorf("Expected 3 ingested messages, got: %s", output)
	}

	// Verify via stats
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "stats", "--json")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}

	var stats map[string]interface{}
	if err := json.Unmarshal(output, &stats); err != nil {
		t.Fatalf("cannot parse stats JSON: %v", err)
	}

	totalMessages, _ := stats["total_messages"].(float64)
	if int(totalMessages) != 3 {
		t.Errorf("Expected 3 messages, got %v", totalMessages)
	}
}

func TestPythonInteropSearchJSONParsing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-search-json.db")

	// Ingest test data
	messages := `{"message_id": "m1", "conversation_id": "conv1", "role": "user", "content": "machine learning models"}
{"message_id": "m2", "conversation_id": "conv1", "role": "assistant", "content": "learning algorithms are powerful"}
{"message_id": "m3", "conversation_id": "conv2", "role": "user", "content": "data science techniques"}
`

	cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
		"--source", "ml-data",
		"--classification", "ml-research")

	cmd.Stdin = bytes.NewBufferString(messages)
	cmd.CombinedOutput()

	// Search and verify JSON structure
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "search",
		"--classification", "ml-research",
		"--json",
		"machine learning")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("cannot parse search JSON: %v", err)
	}

	// Verify structure (Python would check these fields)
	if query, ok := result["query"].(string); !ok || query != "machine learning" {
		t.Errorf("Expected query field, got: %v", result["query"])
	}

	if mode, ok := result["mode"].(string); !ok || mode != "vector" {
		t.Errorf("Expected mode=vector, got: %v", result["mode"])
	}

	if results, ok := result["results"].([]interface{}); !ok || len(results) == 0 {
		t.Errorf("Expected results array, got: %v", result["results"])
	}
}

func TestPythonInteropContentSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-content-search.db")

	// Ingest diverse content
	messages := `{"message_id": "doc1", "conversation_id": "docs", "role": "document", "content": "Kubernetes is a container orchestration platform"}
{"message_id": "doc2", "conversation_id": "docs", "role": "document", "content": "Docker containers run applications"}
{"message_id": "doc3", "conversation_id": "docs", "role": "document", "content": "Terraform manages infrastructure as code"}
`

	cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
		"--source", "docs",
		"--classification", "infrastructure")

	cmd.Stdin = bytes.NewBufferString(messages)
	cmd.CombinedOutput()

	// Test content search (Python parsing results)
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "search",
		"--classification", "infrastructure",
		"--mode", "content",
		"--json",
		"container")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("cannot parse search JSON: %v", err)
	}

	if mode, ok := result["mode"].(string); !ok || mode != "content" {
		t.Errorf("Expected mode=content, got: %v", result["mode"])
	}

	count, _ := result["count"].(float64)
	if int(count) != 2 {
		t.Errorf("Expected 2 results (Kubernetes + Docker), got: %v", count)
	}
}

func TestPythonInteropDeletionTracking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-deletion.db")

	// Ingest test data
	messages := `{"message_id": "del1", "conversation_id": "del-conv", "role": "user", "content": "test message 1"}
{"message_id": "del2", "conversation_id": "del-conv", "role": "user", "content": "test message 2"}
{"message_id": "del3", "conversation_id": "del-conv", "role": "user", "content": "test message 3"}
`

	cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
		"--source", "to-delete",
		"--classification", "temporary")

	cmd.Stdin = bytes.NewBufferString(messages)
	cmd.CombinedOutput()

	// Delete and verify JSON tracking
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "delete",
		"--source", "to-delete",
		"--json")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	var delResult map[string]interface{}
	if err := json.Unmarshal(output, &delResult); err != nil {
		t.Fatalf("cannot parse delete JSON: %v", err)
	}

	// Python would check these fields
	deleted, _ := delResult["deleted"].(float64)
	if int(deleted) != 3 {
		t.Errorf("Expected 3 deleted, got: %v", deleted)
	}

	authBy, ok := delResult["authorized_by"].(string)
	if !ok || authBy != "cli-user" {
		t.Errorf("Expected authorized_by field, got: %v", delResult["authorized_by"])
	}
}

func TestPythonInteropMultipleClassifications(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-multi-class.db")

	// Ingest public
	cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
		"--source", "data",
		"--classification", "public")
	cmd.Stdin = bytes.NewBufferString(`{"message_id": "pub1", "conversation_id": "c1", "role": "user", "content": "public info"}`)
	cmd.CombinedOutput()

	// Ingest secret
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
		"--source", "data",
		"--classification", "secret")
	cmd.Stdin = bytes.NewBufferString(`{"message_id": "sec1", "conversation_id": "c2", "role": "user", "content": "secret info"}`)
	cmd.CombinedOutput()

	// Search in public only
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "search",
		"--classification", "public",
		"--json",
		"info")

	output, _ := cmd.CombinedOutput()
	var result map[string]interface{}
	json.Unmarshal(output, &result)

	count, _ := result["count"].(float64)
	if int(count) != 1 {
		t.Errorf("Expected 1 public result, got: %v", count)
	}

	// Delete secret
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "delete",
		"--classification", "secret",
		"--json",
		"--authorized-by", "python-script")

	output, _ = cmd.CombinedOutput()
	var delResult map[string]interface{}
	json.Unmarshal(output, &delResult)

	deleted, _ := delResult["deleted"].(float64)
	if int(deleted) != 1 {
		t.Errorf("Expected 1 deletion, got: %v", deleted)
	}
}

func TestPythonInteropSourceFiltering(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-source-filter.db")

	// Ingest from multiple sources
	msg1 := `{"message_id": "src1-1", "conversation_id": "conv", "role": "user", "content": "message from source 1"}`
	msg2 := `{"message_id": "src2-1", "conversation_id": "conv", "role": "user", "content": "message from source 2"}`

	cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
		"--source", "python-source-1",
		"--classification", "general")
	cmd.Stdin = bytes.NewBufferString(msg1)
	cmd.CombinedOutput()

	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
		"--source", "python-source-2",
		"--classification", "general")
	cmd.Stdin = bytes.NewBufferString(msg2)
	cmd.CombinedOutput()

	// Search filtering by source (single)
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "search",
		"--classification", "general",
		"--sources", "python-source-1",
		"--json",
		"message")

	output, _ := cmd.CombinedOutput()
	var result map[string]interface{}
	json.Unmarshal(output, &result)

	count, _ := result["count"].(float64)
	if int(count) != 1 {
		t.Errorf("Expected 1 result from source-1, got: %v", count)
	}

	// Search with multiple sources
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "search",
		"--classification", "general",
		"--sources", "python-source-1,python-source-2",
		"--json",
		"message")

	output, _ = cmd.CombinedOutput()
	json.Unmarshal(output, &result)

	count, _ = result["count"].(float64)
	if int(count) != 2 {
		t.Errorf("Expected 2 results from both sources, got: %v", count)
	}
}

func TestPythonInteropErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-errors.db")

	// Test ingest without source
	cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest")
	cmd.Stdin = bytes.NewBufferString(`{"message_id": "m", "role": "u", "content": "test"}`)
	output, err := cmd.CombinedOutput()

	if err == nil {
		t.Error("Expected error for missing source")
	}

	if !bytes.Contains(output, []byte("--source is required")) {
		t.Errorf("Expected source error message, got: %s", output)
	}

	// Test search without classification
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "search", "query")
	output, err = cmd.CombinedOutput()

	if err == nil {
		t.Error("Expected error for missing classification")
	}

	if !bytes.Contains(output, []byte("--classification is required")) {
		t.Errorf("Expected classification error, got: %s", output)
	}
}

func TestPythonInteropLargeScale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-large-scale.db")

	// Generate large dataset (simulating Python doing bulk ingest)
	var msgLines bytes.Buffer
	for i := 1; i <= 100; i++ {
		msg := fmt.Sprintf(
			`{"message_id": "msg-%d", "conversation_id": "large-conv", "role": "user", "content": "bulk message %d with content"}`,
			i, i)
		msgLines.WriteString(msg)
		msgLines.WriteString("\n")
	}

	// Ingest large dataset
	cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
		"--source", "bulk-python-source",
		"--classification", "test-data")

	cmd.Stdin = &msgLines
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("large ingest failed: %v", err)
	}

	if !bytes.Contains(output, []byte("Ingested 100 messages")) {
		t.Errorf("Expected 100 messages ingested, got: %s", output)
	}

	// Verify stats
	cmd = exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "stats", "--json")
	output, _ = cmd.CombinedOutput()

	var stats map[string]interface{}
	json.Unmarshal(output, &stats)

	totalMessages, _ := stats["total_messages"].(float64)
	if int(totalMessages) != 100 {
		t.Errorf("Expected 100 messages in stats, got: %v", totalMessages)
	}
}

func TestPythonInteropConcurrentOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-concurrent.db")

	// Simulate concurrent Python scripts writing to same store
	// (tests that store handles multiple simultaneous operations)

	// Script 1: Ingest
	go func() {
		msg := `{"message_id": "c1", "conversation_id": "concurrent", "role": "user", "content": "from script 1"}`
		cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
			"--source", "script-1",
			"--classification", "concurrent")
		cmd.Stdin = bytes.NewBufferString(msg)
		cmd.CombinedOutput()
	}()

	// Script 2: Ingest
	go func() {
		msg := `{"message_id": "c2", "conversation_id": "concurrent", "role": "user", "content": "from script 2"}`
		cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "ingest",
			"--source", "script-2",
			"--classification", "concurrent")
		cmd.Stdin = bytes.NewBufferString(msg)
		cmd.CombinedOutput()
	}()

	// Wait for goroutines
	time.Sleep(500 * time.Millisecond)

	// Verify both messages ingested
	cmd := exec.Command(cliBinaryPath(), "knowledge", "--config", dbPath, "stats", "--json")
	output, _ := cmd.CombinedOutput()

	var stats map[string]interface{}
	json.Unmarshal(output, &stats)

	totalMessages, _ := stats["total_messages"].(float64)
	if int(totalMessages) != 2 {
		t.Errorf("Expected 2 concurrent ingestions, got: %v", totalMessages)
	}
}

// Helper functions

func cliBinaryPath() string {
	// Check environment variable first (set by test runner)
	if bin := os.Getenv("CADRE_BIN"); bin != "" {
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
	}

	// Try to find it in typical locations
	paths := []string{
		"/tmp/cadre-test",
		"./cadre",
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Fallback: assume it's in PATH
	return "cadre"
}

func strPtr(s string) *string {
	return &s
}
