package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteCmdMissingTaskID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := ExecuteCmd(context.Background(), []string{"--task", "Test"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("expected exit code 2 for missing --task-id, got %d", code)
	}
	if !strings.Contains(stderr.String(), "task-id") {
		t.Errorf("stderr should mention --task-id")
	}
}

func TestExecuteCmdMissingTask(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := ExecuteCmd(context.Background(), []string{"--task-id", "TASK-001"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("expected exit code 2 for missing --task, got %d", code)
	}
	if !strings.Contains(stderr.String(), "task") {
		t.Errorf("stderr should mention --task")
	}
}

func TestExecuteCmdInvalidClassification(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := ExecuteCmd(context.Background(), []string{
		"--task-id", "TASK-001",
		"--task", "Test task",
		"--classification", "invalid",
	}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("expected exit code 2 for invalid classification, got %d", code)
	}
	if !strings.Contains(stderr.String(), "classification") {
		t.Errorf("stderr should mention invalid classification")
	}
}

func TestExecuteCmdInvalidOutputFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := ExecuteCmd(context.Background(), []string{
		"--task-id", "TASK-001",
		"--task", "Test task",
		"--output", "invalid",
		"--routing", "/nonexistent/routing.json",
	}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("expected exit code 2 for invalid output format, got %d", code)
	}
	if !strings.Contains(stderr.String(), "output format") {
		t.Errorf("stderr should mention output format")
	}
}

func TestExecuteCmdMissingRouting(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := ExecuteCmd(context.Background(), []string{
		"--task-id", "TASK-001",
		"--task", "Test task",
		"--routing", "/nonexistent/routing.json",
	}, &stdout, &stderr)

	if code != 1 {
		t.Errorf("expected exit code 1 for missing routing file, got %d", code)
	}
	if !strings.Contains(stderr.String(), "routing") {
		t.Errorf("stderr should mention routing issue")
	}
}

func TestFlagStringList(t *testing.T) {
	var fl flagStringList

	if err := fl.Set("file1.go"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if err := fl.Set("file2.go"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if len(fl) != 2 {
		t.Errorf("expected 2 files, got %d", len(fl))
	}

	if fl[0] != "file1.go" || fl[1] != "file2.go" {
		t.Errorf("unexpected file list: %v", fl)
	}

	str := fl.String()
	if !strings.Contains(str, "file1.go") || !strings.Contains(str, "file2.go") {
		t.Errorf("String() should include both files")
	}
}

func TestWriteOutputToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := writeOutput(&stdout, "", "test output", &stderr)

	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}

	if stdout.String() != "test output" {
		t.Errorf("stdout should contain output")
	}
}

func TestWriteOutputToFile(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "output.txt")

	var stdout, stderr bytes.Buffer
	code := writeOutput(&stdout, tmpFile, "test output", &stderr)

	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}

	// Verify file was created
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	if string(data) != "test output" {
		t.Errorf("file content should match output")
	}
}

func TestWriteOutputInvalidPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := writeOutput(&stdout, "/invalid/nonexistent/path/output.txt", "test", &stderr)

	if code != 1 {
		t.Errorf("expected exit code 1 for invalid path, got %d", code)
	}
}

func TestFindRepositoryRoot(t *testing.T) {
	// This test assumes we're running from within a git repository
	root, err := findRepositoryRoot()
	if err != nil {
		t.Skipf("not in a git repository: %v", err)
	}

	if root == "" {
		t.Errorf("repository root is empty")
	}

	// Verify .git exists
	gitPath := filepath.Join(root, ".git")
	if _, err := os.Stat(gitPath); err != nil {
		t.Errorf(".git not found at repository root")
	}
}

func TestFindRepositoryRootNotFound(t *testing.T) {
	// Create a temporary directory outside of any git repo
	tmpDir := t.TempDir()

	// Change to temp directory
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer os.Chdir(oldCwd)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	root, err := findRepositoryRoot()
	if err == nil {
		t.Errorf("expected error when .git not found, got root=%q", root)
	}
}

func TestValidClassifications(t *testing.T) {
	validClassifications := []string{"internal", "medium", "high", "critical"}

	for _, class := range validClassifications {
		var stdout, stderr bytes.Buffer
		// Use a nonexistent routing path to test classification validation
		// The classification validation happens before routing loading
		ExecuteCmd(context.Background(), []string{
			"--task-id", "TASK-001",
			"--task", "Test",
			"--classification", class,
			"--routing", "/nonexistent/routing.json",
		}, &stdout, &stderr)

		// Should fail on routing, not classification
		errMsg := stderr.String()
		if strings.Contains(errMsg, "classification") {
			t.Errorf("classification %q should be valid, got error: %s", class, errMsg)
		}
	}
}

func TestExecuteCmdInvalidStrategy(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := ExecuteCmd(context.Background(), []string{
		"--task-id", "TASK-001",
		"--task", "Test task",
		"--strategy", "invalid",
		"--routing", "/nonexistent/routing.json",
	}, &stdout, &stderr)

	if code != 2 {
		t.Errorf("expected exit code 2 for invalid strategy, got %d", code)
	}
	if !strings.Contains(stderr.String(), "strategy") {
		t.Errorf("stderr should mention invalid strategy")
	}
}

func TestValidStrategies(t *testing.T) {
	validStrategies := []string{"mock", "dry", "subprocess"}

	for _, strategy := range validStrategies {
		var stdout, stderr bytes.Buffer
		// Use a nonexistent routing path to test strategy validation
		// The strategy validation happens before routing loading
		ExecuteCmd(context.Background(), []string{
			"--task-id", "TASK-001",
			"--task", "Test",
			"--strategy", strategy,
			"--routing", "/nonexistent/routing.json",
		}, &stdout, &stderr)

		// Should fail on routing, not strategy
		errMsg := stderr.String()
		if strings.Contains(errMsg, "strategy") {
			t.Errorf("strategy %q should be valid, got error: %s", strategy, errMsg)
		}
	}
}
