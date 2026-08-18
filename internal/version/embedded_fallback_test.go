package version

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cadre "github.com/deagy/cadre/cli"
)

// A binary with no installation beside it still knows its own version.
//
// The released archive contains one file: the executable. Resolve looks for a
// VERSION marker beside an installation root, finds none, and used to return
// an error -- so `cadre --version`, the first command anyone runs against a
// download, failed on the very artifact the release publishes. The smoke test
// missed it because it runs from the repository checkout, where a marker
// exists.
func TestResolveFallsBackToTheEmbeddedVersion(t *testing.T) {
	// A directory with no marker in it or above it that Resolve would accept.
	empty := t.TempDir()

	resolved, err := Resolve(empty)
	if err != nil {
		t.Fatalf("Resolve(%q) = error %v; a binary must be able to report its own version", empty, err)
	}
	if resolved == "" {
		t.Fatal("Resolve returned an empty version")
	}
	if resolved != cadre.Version() {
		t.Errorf("Resolve fell back to %q, want the embedded %q", resolved, cadre.Version())
	}
}

// The embedded value is the VERSION file, not a copy of it.
//
// //go:embed reads the same path the release workflow and the wheel build
// read, so there is no second copy to drift. This asserts the shape rather
// than the value: a marker that arrived with quotes or a trailing newline
// would be embedded verbatim and reported verbatim.
func TestTheEmbeddedVersionIsCleanlyFormed(t *testing.T) {
	embedded := cadre.Version()
	if embedded == "" {
		t.Fatal("no version embedded; every build path must carry one")
	}
	if strings.TrimSpace(embedded) != embedded {
		t.Errorf("embedded version %q carries surrounding whitespace", embedded)
	}
	for _, unwanted := range []string{`"`, "'", "\n", "VERSION", "="} {
		if strings.Contains(embedded, unwanted) {
			t.Errorf("embedded version %q contains %q; it should be the bare version", embedded, unwanted)
		}
	}
}

// Markers still win. A packaged plugin or an installed wheel must report its
// own version, which can differ from that of the binary reading it across an
// upgrade -- so the fallback must not shadow a marker that is present.
func TestAMarkerBeatsTheEmbeddedVersion(t *testing.T) {
	root := t.TempDir()
	writeMarker(t, root, "9.9.9")

	resolved, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != "9.9.9" {
		t.Errorf("Resolve = %q, want the marker's 9.9.9 rather than the embedded %q",
			resolved, cadre.Version())
	}
}

func writeMarker(t *testing.T, root, version string) {
	t.Helper()
	path := filepath.Join(root, "VERSION")
	if err := os.WriteFile(path, []byte(version+"\n"), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
