package orchestration

import (
	"os"
	"testing"
)

func TestNewPythonCLIBridge(t *testing.T) {
	bridge := NewPythonCLIBridge("python3", "/scripts")

	if bridge == nil {
		t.Fatalf("bridge should not be nil")
	}

	if bridge.pythonPath != "python3" {
		t.Errorf("expected python3, got %s", bridge.pythonPath)
	}
}

func TestPythonCLIBridgeDefaultPython(t *testing.T) {
	bridge := NewPythonCLIBridge("", "/scripts")

	if bridge.pythonPath != "python3" {
		t.Errorf("should default to python3")
	}
}

func TestPythonRequestMarshaling(t *testing.T) {
	request := PythonRequest{
		Script: "test.py",
		Action: "test_action",
		Args: map[string]interface{}{
			"key": "value",
		},
	}

	if request.Script != "test.py" {
		t.Errorf("script mismatch")
	}

	if request.Action != "test_action" {
		t.Errorf("action mismatch")
	}
}

func TestPythonResponseMarshaling(t *testing.T) {
	response := PythonResponse{
		Success: true,
		Data:    "test data",
		Status:  "completed",
	}

	if !response.Success {
		t.Errorf("should be successful")
	}

	if response.Status != "completed" {
		t.Errorf("status mismatch")
	}
}

func TestNewKnowledgeStoreClient(t *testing.T) {
	bridge := NewPythonCLIBridge("python3", "/scripts")
	client := NewKnowledgeStoreClient(bridge)

	if client == nil {
		t.Errorf("client should not be nil")
	}
}

func TestNewPythonCompatibilityLayer(t *testing.T) {
	bridge := NewPythonCLIBridge("python3", "/scripts")
	layer := NewPythonCompatibilityLayer(bridge)

	if layer == nil {
		t.Errorf("layer should not be nil")
	}
}

func TestInvokePythonScriptNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	bridge := NewPythonCLIBridge("python3", tmpDir)

	response, err := bridge.InvokePython("nonexistent.py", "test", nil)

	if err == nil {
		t.Errorf("should error when script not found")
	}

	if response.Success {
		t.Errorf("response should not be successful")
	}
}

func TestPythonRequestArgs(t *testing.T) {
	request := PythonRequest{
		Script: "test.py",
		Action: "process",
		Args: map[string]interface{}{
			"filename": "data.txt",
			"format":   "json",
			"count":    42,
		},
	}

	if request.Args == nil {
		t.Errorf("args should not be nil")
	}

	args := request.Args.(map[string]interface{})
	if args["filename"] != "data.txt" {
		t.Errorf("filename mismatch")
	}

	if args["count"] != 42 {
		t.Errorf("count mismatch")
	}
}

func TestPythonResponseError(t *testing.T) {
	response := PythonResponse{
		Success: false,
		Error:   "test error",
	}

	if response.Success {
		t.Errorf("should not be successful")
	}

	if response.Error != "test error" {
		t.Errorf("error message mismatch")
	}
}

func TestPythonBridgeEnvironment(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a dummy Python script for testing
	scriptPath := tmpDir + "/test.py"
	scriptContent := `#!/usr/bin/env python3
import json
import sys

print(json.dumps({"success": true, "data": "test"}))
`

	err := os.WriteFile(scriptPath, []byte(scriptContent), 0755)
	if err != nil {
		t.Fatalf("failed to create test script: %v", err)
	}

	bridge := NewPythonCLIBridge("python3", tmpDir)

	// Note: This test checks the bridge setup, not actual Python execution
	if bridge.pythonPath != "python3" {
		t.Errorf("python path mismatch")
	}
}
