package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// Integration test for resolve-shared command to verify os.Executable() resolution
// works for packaged plugin installations without CADRE_REPO_ROOT.

var (
	builtResolveCliOnce sync.Once
	builtResolveCliPath string
	builtResolveCliErr  error
)

// resolveSharedBinary builds the CLI binary for subprocess tests.
func resolveSharedBinary(t *testing.T) string {
	t.Helper()
	builtResolveCliOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cadre-resolve-test")
		if err != nil {
			builtResolveCliErr = err
			return
		}
		binary := filepath.Join(dir, "cadre")
		build := exec.Command("go", "build", "-o", binary, "github.com/deagy/cadre/cli/cmd/cadre")
		if output, err := build.CombinedOutput(); err != nil {
			builtResolveCliErr = fmt.Errorf("building the CLI under test: %w\n%s", err, output)
			return
		}
		builtResolveCliPath = binary
	})
	if builtResolveCliErr != nil {
		t.Fatalf("cannot build the CLI under test: %v", builtResolveCliErr)
	}
	return builtResolveCliPath
}

func TestResolveShared_PackagedPluginLayout_NoCADRERepoRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Build the CLI binary once for this test
	binary := resolveSharedBinary(t)

	// Create a synthetic packaged plugin layout:
	// <plugin>/
	//   bin/cadre
	//   suite/
	//     roster/
	//       shared/
	//         operating-principles.md
	pluginRoot := t.TempDir()
	binDir := filepath.Join(pluginRoot, "bin")
	suiteDir := filepath.Join(pluginRoot, "suite")
	rosterDir := filepath.Join(suiteDir, "roster", "shared")

	if err := os.MkdirAll(rosterDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a test file with unique content so we can verify it was found
	testContent := "# Test Operating Principles - Plugin Version\nThis is from suite/roster/shared/"
	testFile := filepath.Join(rosterDir, "operating-principles.md")
	if err := os.WriteFile(testFile, []byte(testContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate the binary being at <plugin>/bin by copying it there
	// (We can't control os.Executable() directly, but we can verify the behavior
	//  by running from an unrelated directory)
	copyBinary := filepath.Join(binDir, "cadre")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binaryData, err := os.ReadFile(binary)
	if err != nil {
		t.Fatalf("cannot read built binary: %v", err)
	}
	if err := os.WriteFile(copyBinary, binaryData, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create an unrelated working directory where the user might be
	userDir := t.TempDir()

	// Run the binary from the user's directory with CADRE_REPO_ROOT unset
	cmd := exec.Command(copyBinary, "resolve-shared", "operating-principles.md")
	cmd.Dir = userDir
	cmd.Env = []string{}                                 // Empty env to ensure CADRE_REPO_ROOT is not set
	cmd.Env = append(cmd.Env, "PATH="+os.Getenv("PATH")) // Keep PATH for basic shell operations

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve-shared failed: %v\nOutput: %s", err, output)
	}

	// Verify we got the content from the plugin layout
	if !bytes.Contains(output, []byte(testContent)) {
		t.Errorf("Expected plugin content in output, got: %s", output)
	}
}

func TestResolveShared_CheckoutBeatsPlugin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Build the CLI binary
	binary := resolveSharedBinary(t)

	// Create both checkout layout and plugin layout, verify checkout wins
	checkoutRoot := t.TempDir()
	checkoutContent := "# Canonical Operating Principles - Checkout Version"
	checkoutFile := filepath.Join(checkoutRoot, "roster", "shared", "operating-principles.md")
	if err := os.MkdirAll(filepath.Dir(checkoutFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkoutFile, []byte(checkoutContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a .git so the checkout is detectable
	if err := os.Mkdir(filepath.Join(checkoutRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Add a plugin directory with different content
	pluginDir := filepath.Join(checkoutRoot, "plugin", "suite", "roster", "shared")
	pluginContent := "# Plugin Operating Principles"
	pluginFile := filepath.Join(pluginDir, "operating-principles.md")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pluginFile, []byte(pluginContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run from inside the checkout
	toolsDir := filepath.Join(checkoutRoot, "plugin", "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binary, "resolve-shared", "operating-principles.md")
	cmd.Dir = toolsDir

	// Build env to ensure CADRE_REPO_ROOT is not set
	cmd.Env = nil
	for _, env := range os.Environ() {
		if !bytes.HasPrefix([]byte(env), []byte("CADRE_REPO_ROOT=")) {
			cmd.Env = append(cmd.Env, env)
		}
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve-shared failed: %v\nOutput: %s", err, output)
	}

	// Verify we got the canonical checkout content, not the plugin content
	if !bytes.Contains(output, []byte(checkoutContent)) {
		t.Errorf("Expected canonical checkout content, got: %s", output)
	}
	if bytes.Contains(output, []byte("Plugin Version")) {
		t.Errorf("Should not have gotten plugin content, got: %s", output)
	}
}
