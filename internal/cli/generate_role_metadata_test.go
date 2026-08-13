package cli

import (
	"testing"
)

func TestGenerateRoleMetadataHelp(t *testing.T) {
	// Test --help flag
	code := GenerateRoleMetadata([]string{"--help"})
	if code != 2 {
		t.Errorf("expected exit code 2 for --help, got %d", code)
	}
}

func TestGenerateRoleMetadataUnexpectedArg(t *testing.T) {
	// Test with unexpected argument
	code := GenerateRoleMetadata([]string{"unexpected-arg"})
	if code != 2 {
		t.Errorf("expected exit code 2 for unexpected argument, got %d", code)
	}
}
