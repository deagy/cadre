package cli

import (
	"testing"
)

func TestGenerateAuthorityAidesHelp(t *testing.T) {
	// An explicit --help is a satisfied request, so it exits 0. This
	// asserted 2, which is what the command actually did: flag.ErrHelp was
	// folded in with genuine parse failures. Exit 0 belongs to the satisfied
	// help requests; exit 2 belongs to bad invocations.
	code := GenerateAuthorityAides([]string{"--help"})
	if code != 0 {
		t.Errorf("expected exit code 0 for --help, got %d", code)
	}
}

func TestGenerateAuthorityAidesUnexpectedArg(t *testing.T) {
	// Test with unexpected argument
	code := GenerateAuthorityAides([]string{"unexpected-arg"})
	if code != 2 {
		t.Errorf("expected exit code 2 for unexpected argument, got %d", code)
	}
}
