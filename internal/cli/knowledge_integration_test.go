package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
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
	cfgPath := writeIntegrationConfig(t, tmpDir, dbPath)

	// Simulate Python creating JSON messages and piping to Go CLI
	messages := `{"message_id": "py-msg-1", "conversation_id": "python-conv", "role": "user", "content": "Python script message"}
{"message_id": "py-msg-2", "conversation_id": "python-conv", "role": "assistant", "content": "Python helper response"}
`

	// Test ingest via subprocess (like Python would call it)
	cmd := exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest",
		"--source", "python-script",
		"--classification", "internal")

	cmd.Stdin = bytes.NewBufferString(messages)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ingest failed: %v\nOutput: %s", err, output)
	}

	if !bytes.Contains(output, []byte("Ingested 2 messages")) {
		t.Errorf("Expected ingest confirmation, got: %s", output)
	}

	// Test search via subprocess and parse JSON output
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "search",
		"--all-sources",
		"--classification", "internal",
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
	cfgPath := writeIntegrationConfig(t, tmpDir, dbPath)

	// Create test messages in various formats that Python might generate
	type PyMessage struct {
		MessageID         string  `json:"message_id"`
		ConversationID    string  `json:"conversation_id"`
		ConversationTitle *string `json:"conversation_title,omitempty"`
		Role              string  `json:"role"`
		Content           string  `json:"content"`
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
			MessageID:         "py-3",
			ConversationID:    "py-conv-2",
			ConversationTitle: strPtr("Python Conversation 2"),
			Role:              "user",
			Content:           "Another message from Python script",
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
	cmd := exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest",
		"--source", "python-app",
		"--classification", "internal")

	cmd.Stdin = &msgLines
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ingest failed: %v\nOutput: %s", err, output)
	}

	if !bytes.Contains(output, []byte("Ingested 3 messages")) {
		t.Errorf("Expected 3 ingested messages, got: %s", output)
	}

	// Verify via stats
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "stats", "--json")
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
	cfgPath := writeIntegrationConfig(t, tmpDir, dbPath)

	// Ingest test data
	messages := `{"message_id": "m1", "conversation_id": "conv1", "role": "user", "content": "machine learning models"}
{"message_id": "m2", "conversation_id": "conv1", "role": "assistant", "content": "learning algorithms are powerful"}
{"message_id": "m3", "conversation_id": "conv2", "role": "user", "content": "data science techniques"}
`

	cmd := exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest",
		"--source", "ml-data",
		"--classification", "internal")

	cmd.Stdin = bytes.NewBufferString(messages)
	cmd.CombinedOutput()

	// Search and verify JSON structure
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "search",
		"--all-sources",
		"--classification", "internal",
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

	// The bundle carries a stable query_id, never the query text: the store
	// deliberately never writes a raw query anywhere, and a JSON envelope
	// that echoed it would put it back into whatever captures this output.
	if _, present := result["query"]; present {
		t.Errorf("the retrieval bundle echoed the raw query back: %v", result["query"])
	}
	if queryID, ok := result["query_id"].(string); !ok || queryID == "" {
		t.Errorf("Expected query_id field, got: %v", result["query_id"])
	}

	if mode, ok := result["mode"].(string); !ok || mode != "vector" {
		t.Errorf("Expected mode=vector, got: %v", result["mode"])
	}

	if trust, ok := result["trust"].(string); !ok || trust != "untrusted_reference" {
		t.Errorf("Expected the untrusted-data trust label, got: %v", result["trust"])
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
	cfgPath := writeIntegrationConfig(t, tmpDir, dbPath)

	// Ingest diverse content
	messages := `{"message_id": "doc1", "conversation_id": "docs", "role": "document", "content": "Kubernetes is a container orchestration platform"}
{"message_id": "doc2", "conversation_id": "docs", "role": "document", "content": "Docker containers run applications"}
{"message_id": "doc3", "conversation_id": "docs", "role": "document", "content": "Terraform manages infrastructure as code"}
`

	cmd := exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest",
		"--source", "docs",
		"--classification", "internal")

	cmd.Stdin = bytes.NewBufferString(messages)
	cmd.CombinedOutput()

	// Test content search (Python parsing results)
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "search",
		"--all-sources",
		"--classification", "internal",
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
	cfgPath := writeIntegrationConfig(t, tmpDir, dbPath)

	// Ingest test data
	messages := `{"message_id": "del1", "conversation_id": "del-conv", "role": "user", "content": "test message 1"}
{"message_id": "del2", "conversation_id": "del-conv", "role": "user", "content": "test message 2"}
{"message_id": "del3", "conversation_id": "del-conv", "role": "user", "content": "test message 3"}
`

	cmd := exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest",
		"--source", "to-delete",
		"--classification", "internal")

	cmd.Stdin = bytes.NewBufferString(messages)
	cmd.CombinedOutput()

	// Delete and verify JSON tracking
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "delete",
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
	cfgPath := writeIntegrationConfig(t, tmpDir, dbPath)

	// Ingest public
	cmd := exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest",
		"--source", "data",
		"--classification", "public")
	cmd.Stdin = bytes.NewBufferString(`{"message_id": "pub1", "conversation_id": "c1", "role": "user", "content": "public info"}`)
	cmd.CombinedOutput()

	// Ingest secret
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest",
		"--source", "data",
		"--classification", "confidential")
	cmd.Stdin = bytes.NewBufferString(`{"message_id": "sec1", "conversation_id": "c2", "role": "user", "content": "secret info"}`)
	cmd.CombinedOutput()

	// Search in public only
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "search",
		"--all-sources",
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
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "delete",
		"--classification", "confidential",
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
	cfgPath := writeIntegrationConfig(t, tmpDir, dbPath)

	// Ingest from multiple sources
	msg1 := `{"message_id": "src1-1", "conversation_id": "conv", "role": "user", "content": "message from source 1"}`
	msg2 := `{"message_id": "src2-1", "conversation_id": "conv", "role": "user", "content": "message from source 2"}`

	cmd := exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest",
		"--source", "python-source-1",
		"--classification", "internal")
	cmd.Stdin = bytes.NewBufferString(msg1)
	cmd.CombinedOutput()

	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest",
		"--source", "python-source-2",
		"--classification", "internal")
	cmd.Stdin = bytes.NewBufferString(msg2)
	cmd.CombinedOutput()

	// Search filtering by source (single)
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "search",
		"--classification", "internal",
		"--source", "python-source-1",
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
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "search",
		"--classification", "internal",
		"--source", "python-source-1",
		"--source", "python-source-2",
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
	cfgPath := writeIntegrationConfig(t, tmpDir, dbPath)

	// Test ingest without source
	cmd := exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest")
	cmd.Stdin = bytes.NewBufferString(`{"message_id": "m", "role": "u", "content": "test"}`)
	output, err := cmd.CombinedOutput()

	if err == nil {
		t.Error("Expected error for missing source")
	}

	if !bytes.Contains(output, []byte("--source is required")) {
		t.Errorf("Expected source error message, got: %s", output)
	}

	// Test search without classification
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "search", "query")
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
	cfgPath := writeIntegrationConfig(t, tmpDir, dbPath)

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
	cmd := exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest",
		"--source", "bulk-python-source",
		"--classification", "internal")

	cmd.Stdin = &msgLines
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("large ingest failed: %v", err)
	}

	if !bytes.Contains(output, []byte("Ingested 100 messages")) {
		t.Errorf("Expected 100 messages ingested, got: %s", output)
	}

	// Verify stats
	cmd = exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "stats", "--json")
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
	cfgPath := writeIntegrationConfig(t, tmpDir, dbPath)

	// Two writers against one store, to prove the store serialises them
	// rather than losing one.
	//
	// This used to launch both in bare goroutines, sleep 500ms, and hope --
	// while discarding both subprocess errors and the stats decode. It flaked
	// twice in CI, and when it did the only symptom was "expected 2, got 1"
	// with no way to tell a lost write from a slow one. A WaitGroup removes
	// the timing guess; checking the errors makes a real failure say what
	// happened.
	messages := []struct{ source, payload string }{
		{"script-1", `{"message_id": "c1", "conversation_id": "concurrent", "role": "user", "content": "from script 1"}`},
		{"script-2", `{"message_id": "c2", "conversation_id": "concurrent", "role": "user", "content": "from script 2"}`},
	}

	var waitGroup sync.WaitGroup
	failures := make([]string, len(messages))

	for index, message := range messages {
		waitGroup.Add(1)
		go func(index int, source, payload string) {
			defer waitGroup.Done()
			command := exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "ingest",
				"--source", source, "--classification", "internal")
			command.Stdin = bytes.NewBufferString(payload)
			if output, err := command.CombinedOutput(); err != nil {
				failures[index] = fmt.Sprintf("%s: %v\n%s", source, err, output)
			}
		}(index, message.source, message.payload)
	}
	waitGroup.Wait()

	for _, failure := range failures {
		if failure != "" {
			t.Fatalf("a concurrent ingest failed:\n%s", failure)
		}
	}

	output, err := exec.Command(cliBinaryPath(t), "knowledge", "--config", cfgPath, "stats", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("stats failed: %v\n%s", err, output)
	}
	var stats map[string]any
	if err := json.Unmarshal(output, &stats); err != nil {
		t.Fatalf("stats returned output that is not JSON: %v\n%s", err, output)
	}

	totalMessages, _ := stats["total_messages"].(float64)
	if int(totalMessages) != len(messages) {
		t.Errorf("total_messages = %v, want %d -- a concurrent write was lost",
			stats["total_messages"], len(messages))
	}
}

// Helper functions

// writeIntegrationConfig writes a knowledge-store config.json pointing at
// dbPath and returns its path.
//
// `--config` names a config *file*, not a database. It used to be read as a
// database path, which is why every test in this file passed one: a missing
// path silently created a new empty store rather than failing.
func writeIntegrationConfig(t *testing.T, dir, dbPath string) string {
	t.Helper()
	path := filepath.Join(dir, "knowledge-config.json")
	data, err := json.Marshal(map[string]any{"database": dbPath})
	if err != nil {
		t.Fatalf("marshalling config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

var (
	builtCLIOnce sync.Once
	builtCLIPath string
	builtCLIErr  error
)

// cliBinaryPath returns the CLI binary these subprocess tests exercise.
//
// It builds the binary from this working tree unless CADRE_BIN names one
// explicitly. It previously fell back to /tmp/cadre-test, ./cadre, and
// finally bare "cadre" on PATH -- so on any machine with a `cadre` installed
// (or a stale build left in /tmp) these tests silently validated some other
// binary's behaviour and passed no matter what this package did. A test that
// cannot fail on a regression is worse than no test, because it reports
// coverage it does not have.
func cliBinaryPath(t *testing.T) string {
	t.Helper()
	// Every test in this file drives a SQLite-backed store through the
	// binary. The binary is built with this process's own CGO setting, so
	// the test process's driver availability is a faithful proxy for the
	// subprocess's -- see sqlite_guard_test.go.
	requireSQLite(t)
	if bin := os.Getenv("CADRE_BIN"); bin != "" {
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
		t.Fatalf("CADRE_BIN is set to %q but that path does not exist", bin)
	}

	builtCLIOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cadre-cli-under-test")
		if err != nil {
			builtCLIErr = err
			return
		}
		binary := filepath.Join(dir, "cadre")
		build := exec.Command("go", "build", "-o", binary, "github.com/deagy/cadre/cli/cmd/cadre")
		if output, err := build.CombinedOutput(); err != nil {
			builtCLIErr = fmt.Errorf("building the CLI under test: %w\n%s", err, output)
			return
		}
		builtCLIPath = binary
	})
	if builtCLIErr != nil {
		t.Skipf("cannot build the CLI under test: %v", builtCLIErr)
	}
	return builtCLIPath
}

func strPtr(s string) *string {
	return &s
}
