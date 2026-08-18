package cli

import (
	"testing"
)

func TestGenerateRoleMetadataHelp(t *testing.T) {
	// An explicit --help is a satisfied request, so it exits 0. This
	// asserted 2, which is what the command actually did: flag.ErrHelp was
	// folded in with genuine parse failures. Exit 2 belongs to the bad
	// invocations the sibling tests cover.
	code := GenerateRoleMetadata([]string{"--help"})
	if code != 0 {
		t.Errorf("expected exit code 0 for --help, got %d", code)
	}
}

func TestGenerateRoleMetadataUnexpectedArg(t *testing.T) {
	// Test with unexpected argument
	code := GenerateRoleMetadata([]string{"unexpected-arg"})
	if code != 2 {
		t.Errorf("expected exit code 2 for unexpected argument, got %d", code)
	}
}
