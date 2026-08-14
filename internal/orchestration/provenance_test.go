package orchestration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "catalog.yaml", "version: 1\n")

	hash, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if hash[:7] != "sha256:" {
		t.Fatalf("hash %q does not start with sha256:", hash)
	}

	// Deterministic: hashing again gives the identical value.
	hash2, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile (second read): %v", err)
	}
	if hash != hash2 {
		t.Fatalf("hash changed across reads of unchanged content: %q != %q", hash, hash2)
	}

	// Changing content changes the hash.
	writeTestFile(t, dir, "catalog.yaml", "version: 2\n")
	hash3, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile (after edit): %v", err)
	}
	if hash3 == hash {
		t.Fatal("expected hash to change after content changed")
	}
}

func TestHashFileMissing(t *testing.T) {
	_, err := HashFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing file, per the fail-hard contract")
	}
}

func TestBuildProvenanceBasic(t *testing.T) {
	dir := t.TempDir()
	catalogPath := writeTestFile(t, dir, "catalog.yaml", "version: 1\n")
	routingPath := writeTestFile(t, dir, "routing.json", `{"version":1,"routes":[]}`)

	prov, err := BuildProvenance(catalogPath, routingPath, BuildProvenanceOptions{})
	if err != nil {
		t.Fatalf("BuildProvenance: %v", err)
	}
	if prov.CatalogContentHash == "" {
		t.Error("expected CatalogContentHash to be set")
	}
	if prov.RoutingContentHash == "" {
		t.Error("expected RoutingContentHash to be set")
	}
	if prov.OverlayApplied {
		t.Error("expected OverlayApplied false when no overlay path given")
	}
	if prov.OverlayPath != "" || prov.OverlayContentHash != "" {
		t.Error("expected overlay fields empty when no overlay path given")
	}
	// Not inside a git repo (temp dir) -- git identity should be absent, not fabricated.
	if prov.GitCommitSHA != "" {
		t.Error("expected no GitCommitSHA outside a git working tree")
	}
}

func TestBuildProvenanceMissingCatalog(t *testing.T) {
	dir := t.TempDir()
	routingPath := writeTestFile(t, dir, "routing.json", `{}`)

	_, err := BuildProvenance(filepath.Join(dir, "missing-catalog.yaml"), routingPath, BuildProvenanceOptions{})
	if err == nil {
		t.Fatal("expected an error when catalog.yaml is missing")
	}
}

func TestBuildProvenanceWithOverlay(t *testing.T) {
	dir := t.TempDir()
	catalogPath := writeTestFile(t, dir, "catalog.yaml", "version: 1\n")
	routingPath := writeTestFile(t, dir, "routing.json", `{"version":1}`)
	overlayPath := writeTestFile(t, dir, "routing-overlay.json", `{"routes":[]}`)

	prov, err := BuildProvenance(catalogPath, routingPath, BuildProvenanceOptions{OverlayPath: overlayPath})
	if err != nil {
		t.Fatalf("BuildProvenance: %v", err)
	}
	if !prov.OverlayApplied {
		t.Error("expected OverlayApplied true")
	}
	if prov.OverlayPath != overlayPath {
		t.Errorf("OverlayPath = %q, want %q", prov.OverlayPath, overlayPath)
	}
	if prov.OverlayContentHash == "" {
		t.Error("expected OverlayContentHash to be set")
	}
	// routing_content_hash must still be the *base* file's hash, not a merged artifact.
	baseHash, _ := HashFile(routingPath)
	if prov.RoutingContentHash != baseHash {
		t.Error("expected RoutingContentHash to remain the base routing.json hash with an overlay applied")
	}
}

func TestBuildProvenanceLifecycleContractVersion(t *testing.T) {
	dir := t.TempDir()
	catalogPath := writeTestFile(t, dir, "catalog.yaml", "version: 1\n")
	routingPath := writeTestFile(t, dir, "routing.json", `{}`)

	version := 7
	prov, err := BuildProvenance(catalogPath, routingPath, BuildProvenanceOptions{AgenticSDLCContractVersion: &version})
	if err != nil {
		t.Fatalf("BuildProvenance: %v", err)
	}
	if prov.AgenticSDLCContractVersion == nil || *prov.AgenticSDLCContractVersion != 7 {
		t.Errorf("AgenticSDLCContractVersion = %v, want 7", prov.AgenticSDLCContractVersion)
	}
}

func TestBuildProvenanceNoLifecycleContractVersion(t *testing.T) {
	dir := t.TempDir()
	catalogPath := writeTestFile(t, dir, "catalog.yaml", "version: 1\n")
	routingPath := writeTestFile(t, dir, "routing.json", `{}`)

	prov, err := BuildProvenance(catalogPath, routingPath, BuildProvenanceOptions{})
	if err != nil {
		t.Fatalf("BuildProvenance: %v", err)
	}
	if prov.AgenticSDLCContractVersion != nil {
		t.Error("expected AgenticSDLCContractVersion to stay nil when not supplied -- never fabricate a value")
	}
}

func TestGitIdentityRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// The Cadre checkout itself is a real git repo -- exercise GitIdentity
	// against its own real catalog.yaml/routing.json.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Join(wd, "..", "..")
	catalogPath := filepath.Join(repoRoot, "roster", "catalog.yaml")
	routingPath := filepath.Join(repoRoot, "roster", "orchestration", "routing.json")
	if _, err := os.Stat(catalogPath); err != nil {
		t.Skip("not running inside the cadre checkout")
	}

	sha, _, ok := GitIdentity(catalogPath, routingPath)
	if !ok {
		t.Fatal("expected GitIdentity to succeed inside a real git checkout")
	}
	if len(sha) != 40 {
		t.Errorf("expected a 40-char git commit SHA, got %q (len %d)", sha, len(sha))
	}
}

func TestGitIdentityOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	catalogPath := writeTestFile(t, dir, "catalog.yaml", "version: 1\n")
	routingPath := writeTestFile(t, dir, "routing.json", `{}`)

	_, _, ok := GitIdentity(catalogPath, routingPath)
	if ok {
		t.Fatal("expected GitIdentity to report not-ok outside any git working tree")
	}
}
