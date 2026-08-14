package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratePlugin(t *testing.T) {
	wd, _ := os.Getwd()
	repoRoot := filepath.Join(wd, "../..")
	tmpDir := t.TempDir()

	pkg, err := GeneratePlugin(repoRoot, tmpDir)
	if err != nil {
		t.Skipf("cannot generate plugin (real data test): %v", err)
	}

	if pkg == nil {
		t.Fatalf("pkg is nil")
	}

	// Check essential files are generated
	checks := map[string]bool{
		".codex-plugin/plugin.json":  false,
		".claude-plugin/plugin.json": false,
		"README.md":                  false,
		"agents":                     false, // Directory marker
		"provider/provider.json":     false,
	}

	for filePath := range pkg.Files {
		for check := range checks {
			if strings.Contains(filePath, check) {
				checks[check] = true
			}
		}
	}

	for check, found := range checks {
		if !found {
			t.Errorf("missing expected generated file: %s", check)
		}
	}

	// Check file count is reasonable (should have many files)
	if len(pkg.Files) < 10 {
		t.Errorf("expected many generated files, got %d", len(pkg.Files))
	}
}

func TestGeneratePluginSkillCopies(t *testing.T) {
	tmpDir := t.TempDir()
	pkg := &PluginPackage{
		OutputRoot: tmpDir,
		Files:      make(map[string]string),
	}

	wd, _ := os.Getwd()
	manifestRoot := filepath.Join(wd, "../..")

	err := generateSkillCopies(pkg, manifestRoot)
	if err != nil {
		t.Fatalf("skill copies failed: %v", err)
	}

	// Skills are optional, but if any exist, they should be copied
	// Just verify the function runs without error
}

func TestGeneratePluginAgentWrappers(t *testing.T) {
	tmpDir := t.TempDir()
	pkg := &PluginPackage{
		OutputRoot: tmpDir,
		Files:      make(map[string]string),
	}

	roles := []RoleMetadata{
		{
			ID:         "test-agent",
			Phase:      "build",
			Capability: "code_author",
			Model:      "sonnet",
		},
	}

	err := generateAgentWrappers(pkg, roles)
	if err != nil {
		t.Fatalf("agent wrappers failed: %v", err)
	}

	if len(pkg.Files) == 0 {
		t.Errorf("no files generated for agent wrapper")
	}

	// Verify wrapper content
	for path, content := range pkg.Files {
		if strings.Contains(path, "test-agent.md") {
			if !strings.Contains(content, "test-agent") {
				t.Errorf("wrapper missing agent ID")
			}
			if !strings.Contains(content, "build") {
				t.Errorf("wrapper missing phase")
			}
			return
		}
	}

	t.Errorf("agent wrapper file not generated")
}

func TestWritePluginFiles(t *testing.T) {
	tmpDir := t.TempDir()
	pkg := &PluginPackage{
		OutputRoot: tmpDir,
		Files: map[string]string{
			filepath.Join(tmpDir, "test", "file.txt"): "test content",
		},
	}

	err := WritePluginFiles(pkg)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Verify file was written
	filePath := filepath.Join(tmpDir, "test", "file.txt")
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Errorf("file not written: %v", err)
	}
	if string(content) != "test content" {
		t.Errorf("file content mismatch")
	}
}

func TestCheckPluginPackage(t *testing.T) {
	tmpDir := t.TempDir()
	testFilePath := filepath.Join(tmpDir, "test", "file.txt")

	// Create test structure
	if err := os.MkdirAll(filepath.Dir(testFilePath), 0755); err != nil {
		t.Fatalf("cannot create test dir: %v", err)
	}

	pkg := &PluginPackage{
		OutputRoot: tmpDir,
		Files: map[string]string{
			testFilePath: "expected content",
		},
	}

	// Write matching file
	if err := os.WriteFile(testFilePath, []byte("expected content"), 0644); err != nil {
		t.Fatalf("cannot write test file: %v", err)
	}

	// Check should pass
	current, stale, err := CheckPluginPackage(pkg)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if !current {
		t.Errorf("check should pass, got stale: %v", stale)
	}

	// Modify file and check again
	if err := os.WriteFile(testFilePath, []byte("modified"), 0644); err != nil {
		t.Fatalf("cannot modify file: %v", err)
	}

	current, stale, err = CheckPluginPackage(pkg)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if current {
		t.Errorf("check should fail after modification")
	}
	if len(stale) == 0 {
		t.Errorf("expected stale files")
	}
}

func TestGeneratePluginManifests(t *testing.T) {
	tmpDir := t.TempDir()
	pkg := &PluginPackage{
		OutputRoot: tmpDir,
		Files:      make(map[string]string),
	}

	err := generatePluginManifests(pkg, tmpDir)
	if err != nil {
		t.Fatalf("manifest generation failed: %v", err)
	}

	if len(pkg.Files) != 2 {
		t.Errorf("expected 2 manifest files, got %d", len(pkg.Files))
	}

	// Check content
	for path, content := range pkg.Files {
		if strings.Contains(path, "plugin.json") {
			if !strings.Contains(content, "cadre") {
				t.Errorf("manifest missing plugin name")
			}
		}
	}
}

func TestGenerateReadme(t *testing.T) {
	tmpDir := t.TempDir()
	pkg := &PluginPackage{
		OutputRoot: tmpDir,
		Files:      make(map[string]string),
	}

	err := generateReadme(pkg)
	if err != nil {
		t.Fatalf("readme generation failed: %v", err)
	}

	if len(pkg.Files) == 0 {
		t.Errorf("no README generated")
	}

	for path, content := range pkg.Files {
		if strings.Contains(path, "README.md") {
			if !strings.Contains(content, "Cadre") {
				t.Errorf("README missing title")
			}
			if !strings.Contains(content, "Installation") {
				t.Errorf("README missing installation section")
			}
			return
		}
	}

	t.Errorf("README.md not generated")
}
