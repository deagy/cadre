package orchestration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This repository ships a provider bundle; the kernel loads it and refuses it
// outside the compatibility window the bundle declares.
//
// The kernel used to hold this guard, globbing the bundles its own repository
// shipped. After the extraction it ships none, so the check moved here — to
// the only side that knows which kernel version its bundle is written against.
// The kernel keeps a demoted version that only proves its loader still runs.
//
// It asks the kernel binary rather than reading a version out of its source,
// because shelling out is one of exactly two couplings the boundary permits
// and because the number that matters is the one a consumer would actually
// install.
func kernelBinary(t *testing.T) (string, bool) {
	t.Helper()
	if explicit := os.Getenv("AGENTIC_SDLC_BIN"); explicit != "" {
		return explicit, true
	}
	if found, err := exec.LookPath("agentic-sdlc"); err == nil {
		return found, true
	}
	return "", false
}

// A prebuilt binary only, deliberately.
//
// This first also fell back to a sibling checkout's bin/agentic-sdlc, which
// builds the kernel on demand. It passed alone and failed in the full package
// run: something else in this package narrows the environment for its own
// purposes, the wrapper could no longer find a Go toolchain, and the failure
// surfaced here as "exit status 1" from a command that has nothing to do with
// the test that broke it.
//
// A guard whose result depends on which other tests ran is worse than one that
// skips, because it fails for reasons that are not about the thing it guards.
// So this asks for a binary that already exists -- which is also the honest
// subject, since the question is whether a consumer'''s installed kernel would
// accept this bundle.

func semver(t *testing.T, text string) [3]int {
	t.Helper()
	parts := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(text, "v")), ".", 3)
	if len(parts) != 3 {
		t.Fatalf("not a semantic version: %q", text)
	}
	var out [3]int
	for i, part := range parts {
		value, err := strconv.Atoi(strings.SplitN(part, "-", 2)[0])
		if err != nil {
			t.Fatalf("not a semantic version: %q", text)
		}
		out[i] = value
	}
	return out
}

func lessThan(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func TestOurProviderBundleAcceptsTheKernelWeDependOn(t *testing.T) {
	binary, ok := kernelBinary(t)
	if !ok {
		if os.Getenv("CI") != "" {
			t.Fatal("no kernel binary is reachable and this is CI, where this guard " +
				"must not be skipped. Set AGENTIC_SDLC_BIN.")
		}
		t.Skip("no kernel binary reachable; set AGENTIC_SDLC_BIN to check")
	}
	output, err := exec.Command(binary, "--version").Output()
	if err != nil {
		t.Fatalf("asking %s for its version: %v", binary, err)
	}
	kernelVersion := semver(t, string(output))

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(filepath.Dir(root), "provider", "provider.json")
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
		t.Fatalf("%s declares no kernel_compatibility window; this guard is checking nothing", manifestPath)
	}

	minimum := semver(t, manifest.KernelCompatibility.Minimum)
	maximum := semver(t, manifest.KernelCompatibility.MaximumExclusive)
	// Say which binary answered, and at what version.
	//
	// A verifier found this test passing while checking the wrong artifact: with
	// AGENTIC_SDLC_BIN unset it resolved a pipx-installed legacy CLI on PATH
	// reporting 0.13.2, not a build of the kernel this repository pins at 0.14.2.
	// It passed only because 0.13.2 sits exactly on the window's inclusive
	// minimum, so a green run said nothing about the kernel the repository
	// actually depends on.
	//
	// The check is still the right one -- "would a consumer's installed kernel
	// accept this bundle" is a question about what is installed. What was wrong
	// is that a passing run was illegible. Logging the path and version makes a
	// pass state what it checked, so a stale binary shadowing the name on PATH
	// is visible rather than silent. The pin itself is covered separately and
	// without a binary, by TestTheKernelVersionPinSatisfiesOurOwnProvider.
	t.Logf("asked %s, which reports %s", binary, strings.TrimSpace(string(output)))

	if lessThan(kernelVersion, minimum) || !lessThan(kernelVersion, maximum) {
		t.Errorf("provider/provider.json declares [%s, %s) and %s reports %s — "+
			"the kernel would refuse this repository's own bundle.",
			manifest.KernelCompatibility.Minimum, manifest.KernelCompatibility.MaximumExclusive,
			binary, strings.TrimSpace(string(output)))
	}
}
