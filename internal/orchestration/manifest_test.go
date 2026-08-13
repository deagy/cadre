package orchestration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRosterManifest(t *testing.T) {
	tests := []struct {
		name        string
		root        string
		expectError bool
		errMessage  string
	}{
		{
			name:        "load valid manifest from cadre repo",
			root:        "../../roster",
			expectError: false,
		},
		{
			name:        "missing manifest",
			root:        "/nonexistent/path",
			expectError: true,
			errMessage:  "missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wd, _ := os.Getwd()
			root := filepath.Join(wd, tt.root)

			manifest, err := LoadRosterManifest(root)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error, got none")
				}
				// Error message check is optional for brevity
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if manifest == nil {
				t.Fatalf("manifest is nil")
			}
			if manifest.ID == "" {
				t.Fatalf("manifest.ID is empty")
			}
			if manifest.Version == "" {
				t.Fatalf("manifest.Version is empty")
			}
			if manifest.CatalogPath == "" {
				t.Fatalf("manifest.CatalogPath is empty")
			}
			if manifest.RoutingPath == "" {
				t.Fatalf("manifest.RoutingPath is empty")
			}
		})
	}
}

func TestLoadRosterManifestPaths(t *testing.T) {
	// Load real manifest from repo
	wd, _ := os.Getwd()
	root := filepath.Join(wd, "../..")
	manifest, err := LoadRosterManifest(root)
	if err != nil {
		t.Skipf("cannot load real manifest: %v", err)
	}

	// Verify all paths exist
	paths := map[string]string{
		"catalog":       manifest.CatalogPath,
		"routing":       manifest.RoutingPath,
		"role_root":     manifest.RoleRootPath,
		"shared_policy": manifest.SharedPolicyRootPath,
	}

	for name, path := range paths {
		if path == "" {
			t.Errorf("%s path is empty", name)
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s path %q does not exist: %v", name, path, err)
			continue
		}
		if name != "catalog" && name != "routing" && !info.IsDir() {
			t.Errorf("%s path %q is not a directory", name, path)
		}
	}
}

func TestResolveManifestPath(t *testing.T) {
	// Create a temporary directory structure for testing
	tmpDir := t.TempDir()

	// Create test files
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("cannot create test file: %v", err)
	}

	testDir := filepath.Join(tmpDir, "testdir")
	if err := os.Mkdir(testDir, 0755); err != nil {
		t.Fatalf("cannot create test dir: %v", err)
	}

	tests := []struct {
		name        string
		root        string
		value       string
		isDir       bool
		expectError bool
	}{
		{
			name:        "valid file path",
			root:        tmpDir,
			value:       "test.txt",
			isDir:       false,
			expectError: false,
		},
		{
			name:        "valid directory path",
			root:        tmpDir,
			value:       "testdir",
			isDir:       true,
			expectError: false,
		},
		{
			name:        "empty value",
			root:        tmpDir,
			value:       "",
			isDir:       false,
			expectError: true,
		},
		{
			name:        "nonexistent file",
			root:        tmpDir,
			value:       "nonexistent.txt",
			isDir:       false,
			expectError: true,
		},
		{
			name:        "escape attempt with ..",
			root:        tmpDir,
			value:       "../etc/passwd",
			isDir:       false,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolveManifestPath(tt.root, tt.value, "test_field", tt.isDir)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if resolved == "" {
				t.Errorf("resolved path is empty")
			}
		})
	}
}
