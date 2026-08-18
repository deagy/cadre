package kernel

import (
	"go/parser"
	"go/token"
	"io/fs"
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
	"selector", "orchestration", "contextstore", "knowledge", "generators", "cli", "engine",
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
		packageDirs := goPackageDirs(t, directory)
		if len(packageDirs) == 0 {
			t.Errorf("internal/%s contains no Go source; this guard would report it as covered "+
				"while examining nothing", name)
			continue
		}
		checked += len(packageDirs)
		for _, packageDir := range packageDirs {
			for path, files := range packageImportsIn(t, packageDir) {
				if path == kernelPkg || strings.HasPrefix(path, kernelPkg+"/") {
					t.Errorf("internal/%s imports %s in %v.\n"+
						"The kernel is shelled out to, never linked: roster/ asks and the kernel "+
						"answers. An in-process call makes this repository authoritative for "+
						"another project's gate approvals, which is the one thing the boundary "+
						"exists to prevent.", name, path, files)
				}
			}
		}
	}
	// Self-vacuity: a guard that checked nothing would pass loudest.
	if checked == 0 {
		t.Fatal("no roster-side packages were checked; this guard asserted nothing")
	}
}

// neutralPackages are internal packages the kernel may import.
//
// The bar is deliberately high, and one package clears it. internal/canonicaljson
// is a byte-exact JSON encoder with no knowledge of routes, roles, gates or
// projects -- the shared half of a hash the selector computes and the kernel
// re-checks. It is shared precisely so the two cannot drift: they disagreed
// once, and the kernel then rejected every plan the selector produced.
//
// Importing the *selector* to get that encoder would have been the coupling
// this guard forbids, which is why the encoder was extracted instead. Adding
// anything else here needs the same argument: no lifecycle knowledge, and a
// concrete failure that duplication has already caused.
var neutralPackages = map[string]bool{
	modulePath + "/internal/canonicaljson": true,
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
		if neutralPackages[path] {
			continue
		}
		t.Errorf("the kernel imports %s in %v.\n"+
			"Gate evaluation must not depend on any particular roster's code.", path, files)
	}
}

func TestANeutralPackageStaysNeutral(t *testing.T) {
	// Without this, the exemption above is a laundering channel: the kernel
	// could reach any roster-side package by way of one that is allowed to be
	// imported. A neutral package earns the name by importing nothing of ours.
	checked := 0
	for path := range neutralPackages {
		directory := filepath.Join("..", strings.TrimPrefix(path, modulePath+"/internal/"))
		if _, err := os.Stat(directory); err != nil {
			t.Errorf("%s is exempted but does not exist; the exemption covers nothing", path)
			continue
		}
		checked++
		// Compiled code only. The agreement test that holds the two
		// fingerprint implementations together lives in this package's
		// external test package and imports both by necessity -- it is a
		// comparison, not a dependency, and nothing it imports ships.
		for imported, files := range packageImportsIn(t, directory) {
			if allExternalTestFiles(t, directory, files) {
				continue
			}
			if strings.HasPrefix(imported, modulePath+"/") {
				t.Errorf("%s imports %s in %v.\n"+
					"A package the kernel may import must carry no repository code of its own, "+
					"or the boundary is crossed one hop later.", path, imported, files)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no exempted package was checked; this guard asserted nothing")
	}
}

// allExternalTestFiles reports whether every file naming an import belongs to
// an external test package (`package foo_test`), which is compiled only for
// `go test` and linked into nothing.
func allExternalTestFiles(t *testing.T, directory string, files []string) bool {
	t.Helper()
	fileSet := token.NewFileSet()
	for _, name := range files {
		file, err := parser.ParseFile(fileSet, filepath.Join(directory, name), nil, parser.PackageClauseOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		if !strings.HasSuffix(file.Name.Name, "_test") {
			return false
		}
	}
	return len(files) > 0
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

// TestThisRepositoryRunsNoLifecycleOverlayOfItsOwn guards a stated invariant
// that is easy to break by accident and hard to notice afterwards.
//
// This repository supplies the kernel; it does not adopt it. It has no
// `.agentic-sdlc/` overlay and no lifecycle records, which CLAUDE.md states
// outright.
//
// What makes it worth a test rather than a rule: an overlay is what
// `agentic-sdlc init` *creates*, and init defaults `--root` to the working
// directory. Any test, sweep or manual invocation that forgets a `--root`
// writes a complete overlay wherever it happened to be standing -- seven
// JSON files with plausible names, which look like fixtures in a diff. Two
// arrived that way and were committed before this existed.
func TestThisRepositoryRunsNoLifecycleOverlayOfItsOwn(t *testing.T) {
	root := repositoryRoot(t)
	var found []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // an unreadable directory is not evidence of an overlay
		}
		if !info.IsDir() {
			return nil
		}
		switch info.Name() {
		case ".git", "node_modules", ".cadre-build-cache", "plugin-dist":
			return filepath.SkipDir
		case Overlay:
			relative, _ := filepath.Rel(root, path)
			found = append(found, relative)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Errorf("this repository has lifecycle overlays it should not:\n  %s\n"+
			"An `agentic-sdlc init` ran here without --root. Delete these, and give "+
			"whatever produced them an explicit root.", strings.Join(found, "\n  "))
	}

	// The managed AGENTS.md block init writes alongside the overlay. Checked
	// separately because it is *not* under `.agentic-sdlc/`, so the walk above
	// misses it -- which is how one survived the cleanup that removed its own
	// overlay and a sibling copy in the same commit.
	var managed []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() {
				switch info.Name() {
				case ".git", "node_modules", ".cadre-build-cache", "plugin-dist":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if info.Name() != "AGENTS.md" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if strings.Contains(string(data), "<!-- agentic-sdlc:start -->") {
			relative, _ := filepath.Rel(root, path)
			managed = append(managed, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) > 0 {
		t.Errorf("this repository has init-managed AGENTS.md blocks it should not:\n  %s\n"+
			"These are written by `agentic-sdlc init` into a project that adopts the "+
			"kernel. This repository supplies it.", strings.Join(managed, "\n  "))
	}
}

// goPackageDirs returns every directory at or under root that holds Go source.
//
// The walk exists for internal/engine, which is a tree of packages rather than
// one directory. Naming just "engine" in rosterSidePackages would have pointed
// the guard at a directory containing no .go files at all: no imports found,
// no findings reported, and `checked` incremented all the same -- a package
// listed as covered while nothing about it was examined. That is precisely the
// silent-coverage-of-nothing the list's own comment warns about.
//
// A no-op for the other six, none of which has a Go subpackage today. testdata
// is skipped because the toolchain ignores it, so a fixture in there is not
// something this repository builds.
func goPackageDirs(t *testing.T, root string) []string {
	t.Helper()
	var dirs []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if entry.Name() == "testdata" {
			return filepath.SkipDir
		}
		matches, err := filepath.Glob(filepath.Join(path, "*.go"))
		if err != nil {
			return err
		}
		if len(matches) > 0 {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return dirs
}
