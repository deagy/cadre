//go:build cgo
// +build cgo

package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNewBatchOperations(t *testing.T) {
	bo := NewBatchOperations()
	if bo == nil {
		t.Error("NewBatchOperations should not return nil")
	}
}

func TestValidateMessage(t *testing.T) {
	bo := NewBatchOperations()

	tests := []struct {
		name    string
		msg     BatchMessage
		wantErr bool
	}{
		{
			name: "valid_message",
			msg: BatchMessage{
				MessageID:      "msg-1",
				Content:        "test content",
				Classification: "internal",
			},
			wantErr: false,
		},
		{
			name: "missing_message_id",
			msg: BatchMessage{
				Content:        "test content",
				Classification: "internal",
			},
			wantErr: true,
		},
		{
			name: "missing_content",
			msg: BatchMessage{
				MessageID:      "msg-1",
				Classification: "internal",
			},
			wantErr: true,
		},
		{
			name: "missing_classification",
			msg: BatchMessage{
				MessageID: "msg-1",
				Content:   "test content",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bo.validateMessage(&tt.msg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMessage() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReadJSONFile(t *testing.T) {
	bo := NewBatchOperations()

	// Create test file
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "test.json")

	testData := `[
		{"message_id": "msg-1", "content": "test 1", "classification": "internal"},
		{"message_id": "msg-2", "content": "test 2", "classification": "public"}
	]`

	if err := os.WriteFile(jsonFile, []byte(testData), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	messages, err := bo.readJSONFile(jsonFile)
	if err != nil {
		t.Fatalf("readJSONFile() error = %v", err)
	}

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}

	if messages[0].MessageID != "msg-1" {
		t.Errorf("Expected msg-1, got %s", messages[0].MessageID)
	}
}

func TestReadJSONLFile(t *testing.T) {
	bo := NewBatchOperations()

	// Create test file
	tmpDir := t.TempDir()
	jsonlFile := filepath.Join(tmpDir, "test.jsonl")

	testData := `{"message_id": "msg-1", "content": "test 1", "classification": "internal"}
{"message_id": "msg-2", "content": "test 2", "classification": "public"}`

	if err := os.WriteFile(jsonlFile, []byte(testData), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	messages, err := bo.readJSONLFile(jsonlFile)
	if err != nil {
		t.Fatalf("readJSONLFile() error = %v", err)
	}

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}
}

func TestImportFromFile(t *testing.T) {
	bo := NewBatchOperations()

	// Create test file
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "test.json")

	testData := `[
		{"message_id": "msg-1", "content": "test 1", "classification": "internal"},
		{"message_id": "msg-2", "content": "test 2", "classification": "public"}
	]`

	if err := os.WriteFile(jsonFile, []byte(testData), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	result, err := bo.ImportFromFile(jsonFile, "json", 100, false, true)
	if err != nil {
		t.Fatalf("ImportFromFile() error = %v", err)
	}

	if result.TotalRead != 2 {
		t.Errorf("Expected 2 total read, got %d", result.TotalRead)
	}

	if result.SuccessCount != 2 {
		t.Errorf("Expected 2 success, got %d", result.SuccessCount)
	}

	rate := result.GetSuccessRate()
	if rate != 100 {
		t.Errorf("Expected 100%% success rate, got %f%%", rate)
	}
}

func TestDeleteByFilter(t *testing.T) {
	bo := NewBatchOperations()

	result, err := bo.DeleteByFilter("internal", "", 0, 100, true)
	if err != nil {
		t.Fatalf("DeleteByFilter() error = %v", err)
	}

	if result.DryRun != true {
		t.Error("Expected dry run to be true")
	}

	if result.FilterUsed == "" {
		t.Error("Expected filter to be set")
	}
}

func TestDeleteByFilterNoFilter(t *testing.T) {
	bo := NewBatchOperations()

	_, err := bo.DeleteByFilter("", "", 0, 100, true)
	if err == nil {
		t.Error("Expected error when no filter provided")
	}
}

func TestUpdateByFilter(t *testing.T) {
	bo := NewBatchOperations()

	changes := map[string]interface{}{
		"classification": "new_class",
	}

	result, err := bo.UpdateByFilter("source=test", changes, 100, true)
	if err != nil {
		t.Fatalf("UpdateByFilter() error = %v", err)
	}

	if result.ChangeCount != 1 {
		t.Errorf("Expected 1 change, got %d", result.ChangeCount)
	}

	if result.DryRun != true {
		t.Error("Expected dry run to be true")
	}
}

func TestUpdateByFilterNoFilter(t *testing.T) {
	bo := NewBatchOperations()

	changes := map[string]interface{}{"test": "value"}
	_, err := bo.UpdateByFilter("", changes, 100, true)
	if err == nil {
		t.Error("Expected error when no filter provided")
	}
}

func TestUpdateByFilterNoChanges(t *testing.T) {
	bo := NewBatchOperations()

	_, err := bo.UpdateByFilter("filter=value", map[string]interface{}{}, 100, true)
	if err == nil {
		t.Error("Expected error when no changes provided")
	}
}

func TestImportResultThroughput(t *testing.T) {
	bo := NewBatchOperations()

	// Create test file with many messages
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "test.json")

	testData := `[`
	for i := 1; i <= 100; i++ {
		testData += `{"message_id": "msg-` + fmt.Sprintf("%d", i) + `", "content": "test", "classification": "internal"}`
		if i < 100 {
			testData += `,`
		}
	}
	testData += `]`

	if err := os.WriteFile(jsonFile, []byte(testData), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	result, err := bo.ImportFromFile(jsonFile, "json", 10, false, true)
	if err != nil {
		t.Fatalf("ImportFromFile() error = %v", err)
	}

	throughput := result.GetThroughput()
	if throughput <= 0 {
		t.Errorf("Expected positive throughput, got %f", throughput)
	}
}

func TestBatchImportWithErrors(t *testing.T) {
	bo := NewBatchOperations()

	// Create test file with invalid messages
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "test.json")

	testData := `[
		{"message_id": "msg-1", "content": "test 1", "classification": "internal"},
		{"message_id": "", "content": "test 2", "classification": "public"},
		{"message_id": "msg-3", "content": "", "classification": "internal"}
	]`

	if err := os.WriteFile(jsonFile, []byte(testData), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	result, err := bo.ImportFromFile(jsonFile, "json", 100, true, true)
	if err != nil {
		t.Fatalf("ImportFromFile() error = %v", err)
	}

	if result.SkippedCount != 2 {
		t.Errorf("Expected 2 skipped, got %d", result.SkippedCount)
	}

	if result.SuccessCount != 1 {
		t.Errorf("Expected 1 success, got %d", result.SuccessCount)
	}
}
