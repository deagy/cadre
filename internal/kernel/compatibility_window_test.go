package kernel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The kernel-compatibility window is half-open: [minimum, maximum_exclusive).
//
// A provider declares the kernel range whose gate semantics it was written
// against. Outside that range its profiles describe a lifecycle this kernel
// does not implement, so LoadProvider refuses rather than loading hopefully.
//
// The boundary is where the mistake lives -- `<=` instead of `<` on the
// maximum admits a provider written for a kernel that has not shipped, and
// `<` instead of `<=` on the minimum refuses one written for exactly this
// kernel. Neither shows up on the versions that happen to be in the tree.
//
// contracts_test.go checks the shipped manifests against this kernel, but it
// does so by *restating* LoadProvider's comparison:
//
//	// The same comparison LoadProvider makes, so this fails exactly when a
//	// real load would.
//	if semverLessThan(current, minimum) || !semverLessThan(current, maximum) {
//
// A test that re-implements the expression it is checking agrees with the
// implementation by construction. Change LoadProvider to `<=` on the maximum
// and that check keeps using the old expression and keeps passing. So these
// drive LoadProvider itself.
//
// Ported from test_repository_health.py's
// test_kernel_version_in_range_enforces_half_open_bounds.

// providerManifestAt writes a minimal valid provider manifest declaring the
// given window, and returns its path.
func providerManifestAt(t *testing.T, minimum, maximumExclusive string) string {
	t.Helper()
	root := t.TempDir()
	// agent_catalog is required and must resolve to a real file inside the
	// manifest's own directory. Omitting it made every accept case fail with
	// "agent_catalog must be a non-empty relative path" -- a refusal that has
	// nothing to do with the window, and would have read as the window being
	// wrong.
	if err := os.WriteFile(filepath.Join(root, "agent-catalog.json"),
		[]byte(`{"schema_version": 1, "agents": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"schema_version": 1,
		"id":             "window-fixture",
		"version":        "1.0.0",
		"agent_catalog":  "agent-catalog.json",
		"profile_roots":  []any{"profiles"},
		"kernel_compatibility": map[string]any{
			"minimum":           minimum,
			"maximum_exclusive": maximumExclusive,
		},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "provider.json")
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// registryForKernel builds a registry pinned to a kernel version, so the
// window can be exercised at its edges without waiting for the real kernel to
// reach them.
func registryForKernel(version string) *Registry {
	return &Registry{kernelVersionOf: version}
}

func TestTheCompatibilityWindowIsHalfOpen(t *testing.T) {
	const minimum, maximumExclusive = "0.3.0", "0.4.0"
	for _, testCase := range []struct {
		kernel   string
		accepted bool
		why      string
	}{
		{"0.2.9", false, "below the minimum"},
		{"0.3.0", true, "exactly the minimum, which is inclusive"},
		{"0.3.9", true, "inside the window"},
		{"0.4.0", false, "exactly the maximum, which is exclusive"},
		{"0.4.1", false, "above the maximum"},
	} {
		t.Run(testCase.kernel+" is "+testCase.why, func(t *testing.T) {
			path := providerManifestAt(t, minimum, maximumExclusive)
			err := registryForKernel(testCase.kernel).LoadProvider(path)
			switch {
			case testCase.accepted && err != nil:
				t.Errorf("kernel %s was refused by [%s, %s): %v",
					testCase.kernel, minimum, maximumExclusive, err)
			case !testCase.accepted && err == nil:
				t.Errorf("kernel %s was accepted by [%s, %s); the window is "+
					"half-open, so %s", testCase.kernel, minimum, maximumExclusive,
					testCase.why)
			case !testCase.accepted && err != nil &&
				!strings.Contains(err.Error(), "kernel_compatibility"):
				t.Errorf("kernel %s was refused for some other reason: %v",
					testCase.kernel, err)
			}
		})
	}
}

func TestTheWindowComparesVersionsNumericallyNotLexically(t *testing.T) {
	// Lexically "0.10.0" sorts below "0.9.0", so a string comparison accepts a
	// kernel ten minor versions past the window and refuses one inside it.
	// The versions in the tree today do not distinguish the two.
	for _, testCase := range []struct {
		kernel                    string
		minimum, maximumExclusive string
		accepted                  bool
	}{
		{"0.10.0", "0.9.0", "0.11.0", true},
		{"0.9.0", "0.10.0", "0.11.0", false},
		{"1.2.10", "1.2.9", "1.3.0", true},
		{"2.0.0", "0.9.0", "0.11.0", false},
	} {
		name := testCase.kernel + " in [" + testCase.minimum + "," + testCase.maximumExclusive + ")"
		t.Run(name, func(t *testing.T) {
			path := providerManifestAt(t, testCase.minimum, testCase.maximumExclusive)
			err := registryForKernel(testCase.kernel).LoadProvider(path)
			if testCase.accepted && err != nil {
				t.Errorf("refused: %v", err)
			}
			if !testCase.accepted && err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestARefusalNamesTheWindowAndTheKernel(t *testing.T) {
	// The message is the whole remedy. Someone hitting this has a provider and
	// a kernel that disagree, and cannot act without knowing which versions
	// are involved.
	path := providerManifestAt(t, "0.3.0", "0.4.0")
	err := registryForKernel("0.5.0").LoadProvider(path)
	if err == nil {
		t.Fatal("an out-of-window provider loaded")
	}
	for _, want := range []string{"window-fixture", "0.3.0", "0.4.0", "0.5.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

func TestAMalformedWindowIsRefusedRatherThanTreatedAsUnbounded(t *testing.T) {
	// A window that does not parse must not read as "no constraint". Failing
	// open here would load a provider written for any kernel at all, which is
	// the state the window exists to prevent.
	for _, testCase := range []struct{ name, minimum, maximum string }{
		{"a non-numeric minimum", "not-a-version", "0.4.0"},
		{"a non-numeric maximum", "0.3.0", "latest"},
		{"an empty minimum", "", "0.4.0"},
		{"a two-part version", "0.3", "0.4.0"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := providerManifestAt(t, testCase.minimum, testCase.maximum)
			if err := registryForKernel("0.3.5").LoadProvider(path); err == nil {
				t.Error("a malformed window loaded successfully; an unparseable " +
					"range must not read as an unbounded one")
			}
		})
	}
}
