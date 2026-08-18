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
	// An explicit --help is a satisfied request, so it exits 0. This
	// asserted 2, which is what the command actually did: flag.ErrHelp was
	// folded in with genuine parse failures. Exit 2 belongs to the bad
	// invocations the sibling tests cover.
	code := GeneratePlugin([]string{"--help"})
	if code != 0 {
		t.Errorf("expected exit code 0 for --help, got %d", code)
	}
}

func TestGeneratePluginUnexpectedArg(t *testing.T) {
	// Test with unexpected argument
	code := GeneratePlugin([]string{"--output", ".", "unexpected-arg"})
	if code != 2 {
		t.Errorf("expected exit code 2 for unexpected argument, got %d", code)
	}
}
