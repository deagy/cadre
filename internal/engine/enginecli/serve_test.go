package enginecli

import (
	"os"
	"testing"
)

// TestResolveWorkspaceRootDefault verifies that resolveWorkspaceRoot defaults
// to the current working directory when no flags are provided.
func TestResolveWorkspaceRootDefault(t *testing.T) {
	expectedCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	result, err := resolveWorkspaceRoot("", false)
	if err != nil {
		t.Fatalf("resolveWorkspaceRoot: %v", err)
	}
	if result != expectedCwd {
		t.Errorf("resolveWorkspaceRoot(\"\", false) = %q, want %q", result, expectedCwd)
	}
}

// TestResolveWorkspaceRootExplicitPath verifies that an explicit --workspace-root
// path is used when provided (and --allow-unconfined-root is not set).
func TestResolveWorkspaceRootExplicitPath(t *testing.T) {
	customPath := "/custom/workspace/root"

	result, err := resolveWorkspaceRoot(customPath, false)
	if err != nil {
		t.Fatalf("resolveWorkspaceRoot: %v", err)
	}
	if result != customPath {
		t.Errorf("resolveWorkspaceRoot(%q, false) = %q, want %q", customPath, result, customPath)
	}
}

// TestResolveWorkspaceRootAllowUnconfinedRootPriority verifies that
// --allow-unconfined-root takes priority over --workspace-root.
func TestResolveWorkspaceRootAllowUnconfinedRootPriority(t *testing.T) {
	customPath := "/custom/workspace/root"

	result, err := resolveWorkspaceRoot(customPath, true)
	if err != nil {
		t.Fatalf("resolveWorkspaceRoot: %v", err)
	}
	if result != "" {
		t.Errorf("resolveWorkspaceRoot(%q, true) = %q, want empty string (unconfined)", customPath, result)
	}
}

// TestResolveWorkspaceRootUnconfinedWithoutPath verifies that
// --allow-unconfined-root alone results in an unconfined state (empty string).
func TestResolveWorkspaceRootUnconfinedWithoutPath(t *testing.T) {
	result, err := resolveWorkspaceRoot("", true)
	if err != nil {
		t.Fatalf("resolveWorkspaceRoot: %v", err)
	}
	if result != "" {
		t.Errorf("resolveWorkspaceRoot(\"\", true) = %q, want empty string (unconfined)", result)
	}
}

// TestResolveWorkspaceRootExplicitPathPrefersExplicit verifies that an explicit
// --workspace-root is used when both flags are not set (confirming the priority
// order: allowUnconfinedRoot > workspaceRoot > default).
func TestResolveWorkspaceRootExplicitPathPrefersExplicit(t *testing.T) {
	customPath := "/custom/workspace/root"

	result, err := resolveWorkspaceRoot(customPath, false)
	if err != nil {
		t.Fatalf("resolveWorkspaceRoot: %v", err)
	}
	if result != customPath {
		t.Errorf("resolveWorkspaceRoot(%q, false) = %q, want %q", customPath, result, customPath)
	}
}
