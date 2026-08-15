package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/deagy/cadre/cli/internal/platform"
)

// The Go selector refuses to run when a routing overlay is present, because
// an overlay changes the effective ruleset and a plan built from the base
// rules alone would answer a question the project did not ask.
//
// That refusal is only worth anything if it actually finds the overlay. These
// pin the two properties of discovery that a plausible-looking guard gets
// wrong -- and getting either wrong fails in the dangerous direction, by
// proceeding rather than by refusing.
func TestRoutingOverlayDiscoveryMatchesThePythonConvention(t *testing.T) {
	overlayRelative := filepath.Join(".agents", "orchestration", "routing-overlay.json")

	writeOverlay := func(t *testing.T, dir string) {
		t.Helper()
		path := filepath.Join(dir, overlayRelative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"version": 1}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("found in an ancestor below the .git boundary", func(t *testing.T) {
		// routing_overlay.py walks up to the nearest .git, so an overlay
		// declared at the project root governs a selection run against a
		// subdirectory. A guard that stats only the target root misses this.
		project := t.TempDir()
		if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeOverlay(t, project)
		nested := filepath.Join(project, "services", "api")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}

		if _, found := platform.FindFileAtProjectRoot(overlayRelative, nested); !found {
			t.Error("an overlay at the project root must govern a run from a subdirectory")
		}
	})

	t.Run("the filename is routing-overlay.json", func(t *testing.T) {
		// The base file is routing.json; the overlay is routing-overlay.json.
		// Watching for the base filename finds nothing and lets every real
		// overlay through.
		project := t.TempDir()
		writeOverlay(t, project)

		if _, found := platform.FindFileAtProjectRoot(
			filepath.Join(".agents", "orchestration", "routing.json"), project); found {
			t.Error("routing.json is the base config, not the overlay -- watching for it detects nothing")
		}
		if _, found := platform.FindFileAtProjectRoot(overlayRelative, project); !found {
			t.Error("routing-overlay.json is the name the overlay is discovered under")
		}
	})

	t.Run("not found above the .git boundary", func(t *testing.T) {
		// The walk stops at the project boundary, so an unrelated overlay in
		// a parent checkout must not be picked up and refused on.
		outer := t.TempDir()
		writeOverlay(t, outer)
		project := filepath.Join(outer, "vendored")
		if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}

		if _, found := platform.FindFileAtProjectRoot(overlayRelative, project); found {
			t.Error("discovery must stop at .git rather than climbing into an unrelated checkout")
		}
	})
}
