package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(working)))
}

// KernelVersion must satisfy this repository's own provider bundle.
//
// It is a hand-kept constant because the engine may not link the kernel:
// roster-side packages ask, they do not import. That arrangement drifted twice
// in the Python. The second time was live -- the engine believed the kernel
// was 0.13.0 while it was 0.14.1, and provider/provider.json requires 0.13.2,
// so loading this repository's own secure-cloud provider failed with
// "incompatible with kernel 0.13.0". The provider was fine; the mirror was
// stale.
//
// That test read the kernel's source, which worked while the kernel lived in
// this repository. It does not any more, and reading whatever kernel happens
// to be installed would be worse: an older binary on one machine is an
// environment fact, not a defect in this repository.
//
// So the constant is now a pin rather than a mirror -- this repository
// declares which kernel it targets -- and the check is the one that actually
// bit: a pin outside our own provider's compatibility window means the engine
// refuses the bundle this repository ships. That needs no kernel present to
// catch, which makes it stricter than what it replaces, not weaker.
//
// Whether the pinned version is a kernel that was ever released is a different
// question, and belongs to whatever consumes the kernel's release feed.
func TestTheKernelVersionPinSatisfiesOurOwnProvider(t *testing.T) {
	manifestPath := filepath.Join(repoRoot(t), "provider", "provider.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("reading %s: %v", manifestPath, err)
	}
	var manifest struct {
		KernelCompatibility struct {
			Minimum          string `json:"minimum"`
			MaximumExclusive string `json:"maximum_exclusive"`
		} `json:"kernel_compatibility"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parsing %s: %v", manifestPath, err)
	}
	if manifest.KernelCompatibility.Minimum == "" {
		t.Fatalf("%s declares no kernel_compatibility window; this guard read nothing", manifestPath)
	}

	satisfied, err := VersionSatisfies(KernelVersion,
		manifest.KernelCompatibility.Minimum, manifest.KernelCompatibility.MaximumExclusive)
	if err != nil {
		t.Fatalf("comparing the pin against %s: %v", manifestPath, err)
	}
	if !satisfied {
		t.Errorf("KernelVersion is %q and %s declares [%s, %s) -- the engine would refuse "+
			"the provider bundle this repository ships.",
			KernelVersion, manifestPath,
			manifest.KernelCompatibility.Minimum, manifest.KernelCompatibility.MaximumExclusive)
	}
}

// The providers this repository ships must load.
//
// This is the check the Python could not pass. Both manifests are real, and
// one of them was being refused.
func TestTheShippedProvidersLoad(t *testing.T) {
	root := repoRoot(t)
	for _, manifest := range []string{
		filepath.Join(root, "provider", "provider.json"),
		filepath.Join(root, "providers", "agentic-sdlc-defaults", "provider.json"),
	} {
		loaded, err := LoadProvider(manifest, nil)
		if err != nil {
			t.Errorf("%s: %v", manifest, err)
			continue
		}
		if loaded.ID == "" {
			t.Errorf("%s loaded with no id", manifest)
		}
		if len(loaded.ProfileRoots) == 0 {
			t.Errorf("%s loaded with no profile roots", manifest)
		}
		if !strings.HasPrefix(loaded.ManifestSHA256, "sha256:") || !strings.HasPrefix(loaded.CatalogSHA256, "sha256:") {
			t.Errorf("%s produced digests %q / %q", manifest, loaded.ManifestSHA256, loaded.CatalogSHA256)
		}
	}
}

// Loading the same manifest twice with nothing already loaded succeeds twice:
// duplicate detection is against what the caller passes, not ambient state.
func TestLoadingIsPureWithRespectToAlreadyLoaded(t *testing.T) {
	manifest := filepath.Join(repoRoot(t), "providers", "agentic-sdlc-defaults", "provider.json")

	first, err := LoadProvider(manifest, nil)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if _, err := LoadProvider(manifest, nil); err != nil {
		t.Fatalf("second load with none already loaded: %v", err)
	}
	if _, err := LoadProvider(manifest, []LoadedProvider{first}); err == nil {
		t.Error("loading against itself should be a duplicate provider id")
	}
}

func TestVersionSatisfies(t *testing.T) {
	cases := []struct {
		version, minimum, maximum string
		want                      bool
	}{
		{"0.14.1", "0.13.2", "1.0.0", true},
		{"0.13.0", "0.13.2", "1.0.0", false}, // the live failure, pinned
		{"1.0.0", "0.13.2", "1.0.0", false},  // maximum is exclusive
		{"0.13.2", "0.13.2", "1.0.0", true},  // minimum is inclusive
		{"0.3.0", "0.3.0", "0.4.0", true},
	}
	for _, testCase := range cases {
		got, err := VersionSatisfies(testCase.version, testCase.minimum, testCase.maximum)
		if err != nil {
			t.Fatalf("VersionSatisfies(%q,%q,%q): %v", testCase.version, testCase.minimum, testCase.maximum, err)
		}
		if got != testCase.want {
			t.Errorf("VersionSatisfies(%q, %q, %q) = %v, want %v",
				testCase.version, testCase.minimum, testCase.maximum, got, testCase.want)
		}
	}
	if _, err := VersionSatisfies("1.2", "1.0.0", "2.0.0"); err == nil {
		t.Error("a two-part version was accepted as semver")
	}
}

// A manifest is data. A path in it must not reach outside the provider.
func TestProviderResourceConfinesPaths(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "catalog.json")
	if err := os.WriteFile(inside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "escaped.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	if _, err := ProviderResource(root, "catalog.json", "agent_catalog", false); err != nil {
		t.Errorf("a path inside the provider was rejected: %v", err)
	}
	if _, err := ProviderResource(root, "../"+filepath.Base(outside), "agent_catalog", false); err == nil {
		t.Error("a path escaping the manifest directory was accepted")
	}
	if _, err := ProviderResource(root, "", "agent_catalog", false); err == nil {
		t.Error("an empty path was accepted")
	}
	if _, err := ProviderResource(root, "catalog.json", "profile_roots", true); err == nil {
		t.Error("a file was accepted where a directory is required")
	}
}

// A reviewer that can also author is the one thing the model exists to stop.
func TestAReviewerMayNotAuthor(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, "agents.json"), `{
	  "schema_version": 1,
	  "agents": {"a-reviewer": {"kind": "reviewer", "capabilities": ["reviewer", "author"]}}
	}`)
	if err := os.MkdirAll(filepath.Join(root, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(root, "provider.json"), `{
	  "schema_version": 1, "id": "test", "version": "1.0.0",
	  "kernel_compatibility": {"minimum": "0.0.1", "maximum_exclusive": "99.0.0"},
	  "agent_catalog": "agents.json", "profile_roots": ["profiles"]
	}`)

	_, err := LoadProvider(filepath.Join(root, "provider.json"), nil)
	if err == nil {
		t.Fatal("a reviewer carrying the author capability was accepted")
	}
	if !strings.Contains(err.Error(), "must remain read-only") {
		t.Errorf("error was %q, want it to name the read-only requirement", err)
	}
}

func TestUnknownManifestKeysAreRejected(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, "provider.json"), `{
	  "schema_version": 1, "id": "test", "version": "1.0.0", "surprise": true,
	  "kernel_compatibility": {"minimum": "0.0.1", "maximum_exclusive": "99.0.0"},
	  "agent_catalog": "agents.json", "profile_roots": ["profiles"]
	}`)
	_, err := LoadProvider(filepath.Join(root, "provider.json"), nil)
	if err == nil || !strings.Contains(err.Error(), "unknown fields") {
		t.Errorf("error was %v, want it to name the unknown field", err)
	}
}

func writeJSON(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
