package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// makeCheckout builds the marker set orchestration.FindCheckoutRoot looks
// for: a .git entry plus roster/catalog.yaml and bin/cadre. Those three
// together, rather than .git alone, are what distinguish a Cadre checkout
// from any other git repository the caller happens to be standing in.
func makeCheckout(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "roster"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	for _, relative := range []string{"roster/catalog.yaml", "bin/cadre"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRequireCheckoutAllowsAGitCheckout(t *testing.T) {
	if !requireCheckout("generate-role-metadata", makeCheckout(t, t.TempDir())) {
		t.Error("a Cadre checkout must be allowed to regenerate")
	}
}

func TestRequireCheckoutRefusesAnInstalledTree(t *testing.T) {
	// The concrete bug this exists for: `cadre generate-authority-aides` from
	// a pip/pipx install rewrote eight AGENT.md files under
	// <prefix>/share/cadre and reported success, leaving the installation
	// differing from the release it claims to be. The rewritten files stay
	// valid, so nothing downstream ever notices.
	prefix := t.TempDir()
	installed := filepath.Join(prefix, "share", "cadre")
	if err := os.MkdirAll(filepath.Join(installed, "roster", "authority"), 0o755); err != nil {
		t.Fatal(err)
	}
	if requireCheckout("generate-authority-aides", installed) {
		t.Error("an installed tree with no .git must be refused")
	}
}

func TestRequireCheckoutRefusesAnUnrelatedGitRepository(t *testing.T) {
	// A user's own project is a git repository too. Accepting one would let a
	// regeneration command write a roster/ into somebody's application repo.
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if requireCheckout("generate-role-metadata", other) {
		t.Error("a git repository that is not a Cadre checkout must be refused")
	}
}

func TestRequireCheckoutFindsACheckoutFromASubdirectory(t *testing.T) {
	// The installation root handed to this guard is not always the checkout
	// root, so it has to ascend rather than test only what it was given.
	root := makeCheckout(t, t.TempDir())
	nested := filepath.Join(root, "nested", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if !requireCheckout("generate-plugin", nested) {
		t.Error("a subdirectory of a checkout must still be allowed")
	}
}
