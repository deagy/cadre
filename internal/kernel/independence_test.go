package kernel

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The kernel stays separately versioned, and owns its contracts alone.
//
// Merging four repositories into one merged their trees, not their version
// lines. The kernel is no longer a separately *publishable* distribution --
// the Python package went when the Go port replaced it -- but it is still
// separately *versioned*, and that is the half carrying the contract:
// provider.json's kernel_compatibility window is only meaningful if the kernel
// can move independently of the role catalog.
//
// The other half is ownership. kernel/contracts/*.json are the gate schemas;
// a second copy under roster/ is a copy that drifts, and the drift is silent
// because both files parse and both look authoritative.
//
// Ported from test_kernel_boundary.py's TestKernelStaysIndependentlyReleasable,
// the last two properties in that file without a Go counterpart.

func TestTheKernelCarriesItsOwnVersionConstant(t *testing.T) {
	// Asserted on the source rather than on the constant, because the constant
	// is what a caller reads and the *declaration* is what a merge would
	// quietly replace with a shared one.
	root := repositoryRootForKernel(t)
	source, err := os.ReadFile(filepath.Join(root, "internal", "kernel", "provider.go"))
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	declaration := regexp.MustCompile(`(?m)^const Version = "\d+\.\d+\.\d+"`)
	if !declaration.Match(source) {
		t.Error("internal/kernel/provider.go no longer declares its own semantic " +
			"version constant.\n" +
			"provider.json's kernel_compatibility window is a range over this " +
			"number. If the kernel stops being versioned independently of the " +
			"role catalog, that window stops meaning anything.")
	}
	// And the constant agrees with the declaration, so this cannot pass on a
	// commented-out line.
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(Version) {
		t.Errorf("the exported Version is %q, not a semantic version", Version)
	}
}

func TestTheKernelOwnsTheLifecycleContractsAlone(t *testing.T) {
	root := repositoryRootForKernel(t)
	for _, contract := range []string{
		"lifecycle-gates.json", "mutation-gates.json", "run-record.schema.json",
	} {
		path := filepath.Join(root, "kernel", "contracts", contract)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the kernel does not ship %s: %v", contract, err)
		}
	}

	// ...and roster/ carries no copy to drift against. A second file parses
	// fine and looks authoritative; nothing about the drift announces itself.
	rosterRoot := filepath.Join(root, "roster")
	if _, err := os.Stat(rosterRoot); err != nil {
		t.Skipf("no roster tree here: %v", err)
	}
	var strays []string
	err := filepath.WalkDir(rosterRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		switch entry.Name() {
		case "lifecycle-gates.json", "mutation-gates.json", "run-record.schema.json":
			relative, _ := filepath.Rel(root, path)
			strays = append(strays, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking roster/: %v", err)
	}
	sort.Strings(strays)
	if len(strays) > 0 {
		t.Errorf("roster/ carries %d copy of a kernel contract:\n  %s\n\n"+
			"The kernel owns gate schemas. A copy here drifts, and both files "+
			"parse, so nothing says which one is being read.",
			len(strays), strings.Join(strays, "\n  "))
	}
}

// repositoryRootForKernel walks up from the package directory to the checkout.
func repositoryRootForKernel(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(working))
}
