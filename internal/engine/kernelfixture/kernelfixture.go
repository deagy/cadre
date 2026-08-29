// Package kernelfixture lays out an installed lifecycle kernel's directory
// shape for tests, around this repository's vendored contracts.
//
// The engine resolves its contracts from a kernel *installation* root, at
// <root>/kernel/contracts/. That used to be this repository, because the
// kernel lived here. It does not any more: the kernel releases from its own
// repository, and the copies this repository keeps live under
// kernel-contracts/ where their name says they are not authoritative.
//
// So tests build the installed shape from the vendored files. The alternatives
// were worse: duplicating them back under kernel/contracts/ would recreate a
// directory that looks exactly like the deleted authoritative one, and
// requiring a real kernel installed would make the suite depend on what the
// machine happens to have.
package kernelfixture

import (
	"os"
	"path/filepath"
	"testing"
)

// Root returns a temporary directory shaped like an installed kernel: the
// vendored contracts under kernel/contracts/, and this repository's own
// default provider bundle linked in beside them.
func Root(t *testing.T, repoRoot string) string {
	t.Helper()
	root := t.TempDir()
	contracts := filepath.Join(root, "kernel", "contracts")
	if err := os.MkdirAll(contracts, 0o755); err != nil {
		t.Fatalf("laying out the fixture kernel root: %v", err)
	}

	vendored := filepath.Join(repoRoot, "kernel-contracts")
	entries, err := os.ReadDir(vendored)
	if err != nil {
		t.Fatalf("reading %s: %v", vendored, err)
	}
	copied := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(vendored, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(contracts, entry.Name()), data, 0o644); err != nil {
			t.Fatalf("writing %s: %v", entry.Name(), err)
		}
		copied++
	}
	if copied == 0 {
		t.Fatalf("no contracts found under %s; the fixture kernel root would be empty "+
			"and every test using it would fail for the wrong reason", vendored)
	}

	// An installed kernel root also carries the default provider bundle the
	// engine resolves agents from. That data is this repository's and did not
	// move, so it is linked rather than copied.
	defaults := filepath.Join(repoRoot, "providers")
	if _, err := os.Stat(defaults); err != nil {
		t.Fatalf("the default provider bundle is missing at %s: %v", defaults, err)
	}
	if err := os.Symlink(defaults, filepath.Join(root, "providers")); err != nil {
		t.Fatalf("linking the default provider bundle: %v", err)
	}
	return root
}
