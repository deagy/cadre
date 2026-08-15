package kernel

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The kernel ownership boundary, in Go.
//
// kernel/ owns lifecycle gate schemas, run-record validation and
// gate-authority semantics -- permanently. roster/ supplies a role catalog
// and a provider profile *into* projects that adopt the kernel; it never
// becomes authoritative for another project's gate approvals. roster/ asks,
// the kernel answers.
//
// Two Python packages could not import each other without someone noticing.
// One Go module removes that: `internal/selector` importing `internal/kernel`
// compiles, passes review as a small cleanup ("why shell out to ourselves?"),
// and dissolves the boundary in a single line. Nothing about the type system
// objects.
//
// So this is the replacement for what the process split used to give for
// free, and it is why the kernel ships as its own binary. Ported from
// roster/orchestration/test/test_kernel_boundary.py, which permits exactly
// two couplings: shelling out to the kernel CLI, and reading
// kernel/contracts/*.json as data.

const (
	kernelPkg   = "github.com/deagy/cadre/cli/internal/kernel"
	modulePath  = "github.com/deagy/cadre/cli"
	packagesDir = ".."
)

// rosterSidePackages are the packages that must ask rather than link. Named
// explicitly rather than "everything except the kernel": the failure mode of
// a guard like this is an incomplete list, and a list that has to be edited
// when a package is added is one someone will notice.
var rosterSidePackages = []string{
	"selector", "orchestration", "contextstore", "knowledge", "generators", "cli",
}

// packageImportsIn returns import path -> files that import it, for one
// package directory.
//
// One ParseFile per entry rather than ParseDir: ParseDir is deprecated as of
// Go 1.25 because it ignores build tags when grouping files into packages.
// That would matter here -- a build-tagged file importing the kernel is still
// an import of the kernel, and a grouping that quietly dropped it would leave
// this guard passing over exactly the file that broke the boundary.
//
// Mirrors internal/contextstore/boundary_test.go, which parses the same way
// for the same reason.
func packageImportsIn(t *testing.T, directory string) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("reading %s: %v", directory, err)
	}
	fileSet := token.NewFileSet()
	imports := map[string][]string{}
	inspected := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, filepath.Join(directory, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		inspected++
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			imports[path] = append(imports[path], name)
		}
	}
	if inspected == 0 {
		t.Fatalf("%s contains no Go files; this guard would inspect nothing", directory)
	}
	return imports
}

func TestNoRosterSidePackageImportsTheKernel(t *testing.T) {
	checked := 0
	for _, name := range rosterSidePackages {
		directory := filepath.Join(packagesDir, name)
		if _, err := os.Stat(directory); err != nil {
			t.Errorf("%s is named in this guard but does not exist; the list is stale "+
				"and may be silently covering nothing", name)
			continue
		}
		checked++
		for path, files := range packageImportsIn(t, directory) {
			if path == kernelPkg || strings.HasPrefix(path, kernelPkg+"/") {
				t.Errorf("internal/%s imports %s in %v.\n"+
					"The kernel is shelled out to, never linked: roster/ asks and the kernel "+
					"answers. An in-process call makes this repository authoritative for "+
					"another project's gate approvals, which is the one thing the boundary "+
					"exists to prevent.", name, path, files)
			}
		}
	}
	// Self-vacuity: a guard that checked nothing would pass loudest.
	if checked == 0 {
		t.Fatal("no roster-side packages were checked; this guard asserted nothing")
	}
}

func TestTheKernelDoesNotImportRosterSideCode(t *testing.T) {
	// The other direction, and the more tempting one: the kernel reaching for
	// the selector's routing or the context store would make gate evaluation
	// depend on a particular roster, which is exactly the coupling the
	// permanent ownership split forbids.
	for path, files := range packageImportsIn(t, ".") {
		if !strings.HasPrefix(path, modulePath+"/internal/") {
			continue
		}
		if path == kernelPkg || strings.HasPrefix(path, kernelPkg+"/") {
			continue
		}
		t.Errorf("the kernel imports %s in %v.\n"+
			"Gate evaluation must not depend on any particular roster's code.", path, files)
	}
}

func TestTheKernelShipsAsItsOwnBinary(t *testing.T) {
	// The structural half. Even with both import directions clean, folding
	// the kernel into `cadre` would put gate authority and role selection in
	// one process, one flag away from each other.
	main := filepath.Join("..", "..", "cmd", "agentic-sdlc", "main.go")
	source, err := os.ReadFile(main)
	if err != nil {
		t.Fatalf("the kernel has no binary of its own at cmd/agentic-sdlc: %v", err)
	}
	if !strings.Contains(string(source), kernelPkg) {
		t.Error("cmd/agentic-sdlc does not use internal/kernel")
	}

	// And the cadre binary must not reach for it.
	cadreImports := packageImportsIn(t, filepath.Join("..", "..", "cmd", "cadre"))
	if files, present := cadreImports[kernelPkg]; present {
		t.Errorf("cmd/cadre imports the kernel in %v; they are separate binaries "+
			"so that the boundary survives a refactor", files)
	}
}
