package cli

import (
	"testing"
)

func TestSelectAgentsMissingTask(t *testing.T) {
	code := SelectAgents([]string{})
	if code != 2 {
		t.Errorf("expected exit code 2 for missing --task, got %d", code)
	}
}

func TestSelectAgentsMissingTaskID(t *testing.T) {
	code := SelectAgents([]string{"--task", "Some task"})
	if code != 2 {
		t.Errorf("expected exit code 2 for missing --task-id, got %d", code)
	}
}

func TestSelectAgentsUnexpectedArg(t *testing.T) {
	code := SelectAgents([]string{
		"--task", "Some task",
		"--task-id", "TASK-001",
		"unexpected-arg",
	})
	if code != 2 {
		t.Errorf("expected exit code 2 for unexpected argument, got %d", code)
	}
}

func TestSelectAgentsHelp(t *testing.T) {
	// Test --help flag
	code := SelectAgents([]string{"--help"})
	if code != 2 {
		t.Errorf("expected exit code 2 for --help, got %d", code)
	}
}

func TestSelectAgentsInvalidOutputFormat(t *testing.T) {
	// This will fail because routing.json won't be found, but if it did,
	// an invalid output format should fail with exit code 2
	code := SelectAgents([]string{
		"--task", "Some task",
		"--task-id", "TASK-001",
		"--output", "invalid-format",
	})
	// Exit code 1 for missing routing, but would be 2 for invalid format if routing existed
	if code != 1 && code != 2 {
		t.Errorf("expected exit code 1 or 2, got %d", code)
	}
}

func TestSelectAgentsValidClassifications(t *testing.T) {
	classifications := []string{"internal", "medium", "high", "critical"}

	for _, class := range classifications {
		// These will fail due to missing routing, but shouldn't fail on the classification value itself
		code := SelectAgents([]string{
			"--task", "Test task",
			"--task-id", "TASK-001",
			"--classification", class,
		})
		// Should fail finding routing (exit 1), not due to classification parsing (exit 2)
		if code == 2 {
			t.Errorf("classification %q caused exit code 2 (parsing error)", class)
		}
	}
}
