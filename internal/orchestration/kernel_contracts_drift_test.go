package orchestration

import (
	"os"
	"path/filepath"
	"testing"
)

// kernel-contracts/ holds copies of contracts the lifecycle kernel owns.
//
// Several tests here read the contracts as data, which is one of exactly two
// couplings the kernel boundary permits. Shelling out to the kernel instead
// would make the suite need a kernel installed; fetching them would make it
// need a network. So they are vendored, and this holds the copies to their
// source.
//
// The source is the kernel repository. While it is a sibling checkout that is
// where this looks; once the kernel publishes releases this should compare
// against a pinned release artifact instead, which is a stricter check because
// a sibling checkout can itself be mid-edit.
//
// It skips when no kernel source is reachable and fails under CI, where it
// must not be skipped. A drift guard that silently checks nothing is worse
// than no guard: it reports green while the copies diverge, which is the exact
// failure the vendoring introduces.
var vendoredContracts = []string{"lifecycle-gates.json", "mutation-gates.json", "run-record.schema.json"}

// kernelContractsSource resolves the kernel's own copy. KERNEL_CONTRACTS_DIR
// overrides; otherwise a sibling cadre-lifecycle checkout is tried, then this
// repository's own kernel/ while it still has one.
func kernelContractsSource(t *testing.T) (string, bool) {
	t.Helper()
	if explicit := os.Getenv("KERNEL_CONTRACTS_DIR"); explicit != "" {
		return explicit, true
	}
	root, err := filepath.Abs("..")
	if err != nil {
		return "", false
	}
	repo := filepath.Dir(root)
	for _, candidate := range []string{
		filepath.Join(filepath.Dir(repo), "cadre-lifecycle", "kernel", "contracts"),
		filepath.Join(repo, "kernel", "contracts"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func TestVendoredKernelContractsMatchTheKernel(t *testing.T) {
	source, ok := kernelContractsSource(t)
	if !ok {
		if os.Getenv("CI") != "" {
			t.Fatal("no kernel contract source is reachable and this is CI, " +
				"where this guard must not be skipped. Set KERNEL_CONTRACTS_DIR to a " +
				"kernel checkout or an unpacked kernel release.")
		}
		t.Skip("no kernel contract source reachable; set KERNEL_CONTRACTS_DIR to check")
	}

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	vendored := filepath.Join(filepath.Dir(root), "kernel-contracts")

	for _, name := range vendoredContracts {
		want, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Errorf("reading the kernel's %s: %v", name, err)
			continue
		}
		got, err := os.ReadFile(filepath.Join(vendored, name))
		if err != nil {
			t.Errorf("reading the vendored %s: %v", name, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("kernel-contracts/%s differs from the kernel's copy at %s.\n"+
				"These files are not authoritative. Change the contract in the kernel "+
				"and re-vendor; editing the copy is what this check exists to catch.",
				name, source)
		}
	}
}
