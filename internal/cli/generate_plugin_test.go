package cli

import (
	"testing"
)

func TestGeneratePluginMissingOutput(t *testing.T) {
	// Test that --output is required
	code := GeneratePlugin([]string{})
	if code != 2 {
		t.Errorf("expected exit code 2 (missing --output), got %d", code)
	}
}

func TestGeneratePluginHelp(t *testing.T) {
	// Test --help flag
	code := GeneratePlugin([]string{"--help"})
	if code != 2 {
		t.Errorf("expected exit code 2 for --help, got %d", code)
	}
}

func TestGeneratePluginUnexpectedArg(t *testing.T) {
	// Test with unexpected argument
	code := GeneratePlugin([]string{"--output", ".", "unexpected-arg"})
	if code != 2 {
		t.Errorf("expected exit code 2 for unexpected argument, got %d", code)
	}
}
