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

// No marker is no longer an error: the binary reports the version compiled
// into it.
//
// This asserted an error, which was the behaviour that broke the released
// artifact. A release archive ships the executable alone, so `cadre --version`
// on a fresh download hit exactly this path and failed. Reporting the
// embedded version is both correct and the only answer available.
func TestCLIVersion_MissingMarkerFallsBackToTheEmbeddedVersion(t *testing.T) {
	repoRoot := t.TempDir()
	got, err := CLIVersion(repoRoot)
	if err != nil {
		t.Fatalf("CLIVersion() error = %v; a binary must be able to report its own version", err)
	}
	if got == "" {
		t.Error("CLIVersion() returned an empty version")
	}
}

func TestCLIVersion_UnparseableMarkerFallsBackToTheEmbeddedVersion(t *testing.T) {
	repoRoot := t.TempDir()
	markerDir := filepath.Join(repoRoot, "cadre_cli")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "_version.py"), []byte("# no version here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// An unparseable marker falls back too, rather than failing. The marker
	// is preferred when it yields a version; when it does not, the embedded
	// one is still a better answer than refusing to report any version at
	// all -- and for a wheel or plugin install the two agree anyway, since
	// the same distribution supplied both.
	got, err := CLIVersion(repoRoot)
	if err != nil {
		t.Fatalf("CLIVersion() error = %v; an unreadable marker should not silence the version", err)
	}
	if got == "" {
		t.Error("CLIVersion() returned an empty version")
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

func TestCLIVersionReadsThePlainVersionFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := CLIVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.3" {
		t.Errorf("CLIVersion = %q, want 1.2.3", got)
	}
}

func TestCLIVersionWorksFromAWheelInstallLayout(t *testing.T) {
	// The bug this replaced: the binary wheel does not vendor cadre_cli, so
	// `cadre --version` from an installed distribution failed outright with
	// "could not read Cadre version marker". A smoke test running `cadre help`
	// and `cadre select` never noticed, because neither reads the version.
	//
	// The marker now sits beside the installation root, which is the one
	// place every channel agrees on.
	prefix := t.TempDir()
	installed := filepath.Join(prefix, "share", "cadre")
	if err := os.MkdirAll(filepath.Join(installed, "roster"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "VERSION"), []byte("0.9.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := CLIVersion(installed)
	if err != nil {
		t.Fatalf("a wheel-installed layout must report its version: %v", err)
	}
	if got != "0.9.1" {
		t.Errorf("CLIVersion = %q, want 0.9.1", got)
	}
}

func TestCLIVersionStillReadsAnOlderPythonMarker(t *testing.T) {
	// A plugin or wheel built before the marker changed still carries
	// cadre_cli/_version.py. Reporting no version at all when reading one of
	// those would be a worse outcome than accepting both shapes.
	root := t.TempDir()
	markerDir := filepath.Join(root, "cadre_cli")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "\"\"\"docstring\"\"\"\n\nVERSION = \"0.4.2\"\n"
	if err := os.WriteFile(filepath.Join(markerDir, "_version.py"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := CLIVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "0.4.2" {
		t.Errorf("CLIVersion = %q, want 0.4.2 from the legacy marker", got)
	}
}

func TestCLIVersionPrefersTheCurrentMarkerOverTheLegacyOne(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("2.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	markerDir := filepath.Join(root, "cadre_cli")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "_version.py"), []byte("VERSION = \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := CLIVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.0.0" {
		t.Errorf("CLIVersion = %q, want the current marker to win", got)
	}
}
