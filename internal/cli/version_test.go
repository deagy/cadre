package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIVersion_ChecksOutLayout(t *testing.T) {
	repoRoot := t.TempDir()
	markerDir := filepath.Join(repoRoot, "cadre_cli")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := "\"\"\"docstring\"\"\"\n\nVERSION = \"0.5.0\"\n"
	if err := os.WriteFile(filepath.Join(markerDir, "_version.py"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := CLIVersion(repoRoot)
	if err != nil {
		t.Fatalf("CLIVersion() error = %v", err)
	}
	if got != "0.5.0" {
		t.Errorf("CLIVersion() = %q, want %q", got, "0.5.0")
	}
}

func TestCLIVersion_VendoredWheelLayout(t *testing.T) {
	// Mirrors the vendored-wheel fallback: REPO_ROOT.parent / "_version.py"
	// when <repoRoot>/cadre_cli/_version.py does not exist.
	installRoot := t.TempDir()
	repoRoot := filepath.Join(installRoot, "checkout")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := "VERSION = '1.2.3'\n"
	if err := os.WriteFile(filepath.Join(installRoot, "_version.py"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := CLIVersion(repoRoot)
	if err != nil {
		t.Fatalf("CLIVersion() error = %v", err)
	}
	if got != "1.2.3" {
		t.Errorf("CLIVersion() = %q, want %q", got, "1.2.3")
	}
}

func TestCLIVersion_MissingMarker(t *testing.T) {
	repoRoot := t.TempDir()
	if _, err := CLIVersion(repoRoot); err == nil {
		t.Error("CLIVersion() error = nil, want error for missing marker file")
	}
}

func TestCLIVersion_MarkerWithoutVersionAssignment(t *testing.T) {
	repoRoot := t.TempDir()
	markerDir := filepath.Join(repoRoot, "cadre_cli")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "_version.py"), []byte("# no version here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := CLIVersion(repoRoot); err == nil {
		t.Error("CLIVersion() error = nil, want error for marker missing VERSION")
	}
}

func TestCLIVersion_EmptyStringLiteral(t *testing.T) {
	repoRoot := t.TempDir()
	markerDir := filepath.Join(repoRoot, "cadre_cli")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "_version.py"), []byte("VERSION = \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := CLIVersion(repoRoot)
	if err != nil {
		t.Fatalf("CLIVersion() error = %v", err)
	}
	if got != "" {
		t.Errorf("CLIVersion() = %q, want empty string", got)
	}
}
