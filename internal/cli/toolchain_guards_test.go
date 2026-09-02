package cli

// Two properties of the test suite itself, both of which it has already
// violated.
//
// A test that shells out to an external tool has to decide what happens when
// the tool is absent. Skipping is almost always right: the absence is a
// property of the machine, not a defect in the code under test, and a suite
// that fails on a missing toolchain fails for people who could not have
// caused it. Failing is defensible only where the tool's presence is itself
// the thing being asserted.
//
// The suite disagreed with itself about this. `packaged_selector_test.go`
// looped over `[]string{"git", "go"}` and skipped on either; twenty lines
// away in the same package, `guard_binaries_test.go` guarded on `git`, then
// built with the Go toolchain and called `t.Fatalf` if that failed. Both
// were written to the same intent and only one held to it.
//
// The second property is what a guard says when it succeeds. Every
// `LookPath` site reported the tool on the failure branch -- "needs go: ..."
// -- and nothing at all on the success branch, so a passing run never
// recorded which binary it had actually checked against. That is not
// pedantry: a pipx-installed `agentic-sdlc` once shadowed the kernel on
// PATH, and the guards that resolved it reported a pass without ever saying
// which one they had found.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// toolchainInvocation matches shelling out to a tool whose absence should
// skip rather than fail.
var toolchainInvocation = regexp.MustCompile(`exec\.Command\(\s*"(go|git|npm|node|python3?)"`)

// lookPathCall matches a resolution of an external tool by name.
var lookPathCall = regexp.MustCompile(`exec\.LookPath\(\s*"([a-zA-Z0-9_.-]+)"`)

// toolLoopLiteral matches `range []string{"git", "go"}` — one guard covering
// several tools, which is the tidiest form of this and must not be missed.
var toolLoopLiteral = regexp.MustCompile(`range\s*\[\]string\{([^}]*)\}`)

// genericToolchains are tools whose *availability* is the question. Which
// `git` you found almost never matters.
var genericToolchains = map[string]bool{
	"go": true, "git": true, "npm": true, "node": true, "python": true,
	"python3": true, "bash": true, "sh": true, "gcc": true, "cc": true, "tar": true,
}

// discardedLookPath matches a resolution whose resolved path is thrown away.
//
// `if _, err := exec.LookPath("go"); err != nil` is the whole defect in one
// line: the guard knows exactly which binary it found and assigns it to the
// blank identifier, so the only run that could have reported something
// useful reports nothing.
var discardedLookPath = regexp.MustCompile(`_,\s*err\s*:?=\s*exec\.LookPath\(`)

// repositoryTestFiles returns every tracked _test.go under the repository.
func repositoryTestFiles(t *testing.T, repo string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(repo, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "vendor", ".git", "plugin", "cline-plugins", "provider", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", repo, err)
	}
	return paths
}

// TestAToolchainInvocationIsGuardedBySomethingThatSkips asserts that a test
// file shelling out to an external toolchain first resolves it and skips.
//
// Scoped per file rather than per function: a guard in one helper serves
// every test in the file that calls it, which is how packaged_selector_test.go
// is written, and demanding a guard adjacent to each invocation would force
// that helper to be inlined everywhere.
func TestAToolchainInvocationIsGuardedBySomethingThatSkips(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range repositoryTestFiles(t, repo) {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		lines := strings.Split(string(content), "\n")
		relative, relErr := filepath.Rel(repo, path)
		if relErr != nil {
			relative = path
		}
		// Per tool, not per file. A file that resolves `git` and then runs
		// the Go toolchain is exactly the defect: guard_binaries_test.go
		// guarded on git, built with go, and failed rather than skipped.
		resolved := map[string]bool{}
		for _, found := range lookPathCall.FindAllStringSubmatch(string(content), -1) {
			resolved[found[1]] = true
		}
		// `for _, tool := range []string{"git", "go"}` resolves both, and is
		// the pattern packaged_selector_test.go uses correctly.
		if loop := toolLoopLiteral.FindStringSubmatch(string(content)); loop != nil {
			for _, quoted := range strings.Split(loop[1], ",") {
				resolved[strings.Trim(strings.TrimSpace(quoted), `"`)] = true
			}
		}

		for index, line := range lines {
			match := toolchainInvocation.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			if resolved[match[1]] {
				// The file resolves this tool before running it; a guard in a
				// helper serves every test in the file that calls it.
				continue
			}
			// Otherwise the error path decides. Running the tool and skipping
			// when it errors is a legitimate guard -- better than LookPath in
			// one respect, since it also covers "git exists but this is not a
			// repository". Reaching t.Fatal instead is the defect.
			window := lines[index:min(index+15, len(lines))]
			joined := strings.Join(window, "\n")
			if strings.Contains(joined, "t.Skip") {
				continue
			}
			if !strings.Contains(joined, "t.Fatal") {
				continue
			}
			t.Errorf(
				"%s:%d runs %q and reaches t.Fatal when it is unavailable, so the suite "+
					"fails rather than skips on a machine without it.\n"+
					"  A missing toolchain is a property of the machine, not a defect in the "+
					"code under test.\n"+
					"  Either resolve it with exec.LookPath and skip, as packaged_selector_test.go "+
					"does, or skip on the command's own error, as help_surface_test.go does.",
				relative, index+1, match[1])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestAResolvedToolIsNamedOnTheSuccessPath asserts that a guard resolving an
// external tool reports which one it found, not only which one it missed.
//
// A pass that does not say what it checked is not evidence. The failure
// branch already names the tool, because an error message has to; the
// success branch is where the information is lost, and the success branch is
// the one that runs when the guard is doing its job.
func TestAResolvedToolIsNamedOnTheSuccessPath(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range repositoryTestFiles(t, repo) {
		// This file quotes the pattern it looks for, so it matches itself.
		if strings.HasSuffix(path, "toolchain_guards_test.go") {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		text := string(content)
		for _, line := range strings.Split(text, "\n") {
			if !discardedLookPath.MatchString(line) {
				continue
			}
			named := lookPathCall.FindStringSubmatch(line)
			if named == nil || genericToolchains[named[1]] {
				// Availability is the question for a toolchain, and the
				// failure branch already names it. Identity is the question
				// only for a binary the project itself ships or depends on.
				continue
			}
			relative, relErr := filepath.Rel(repo, path)
			if relErr != nil {
				relative = path
			}
			t.Errorf(
				"%s resolves %q and assigns the resolved path to the blank identifier.\n"+
					"  For a project binary, which one you found is the whole question. Keep "+
					"it and log it.\n"+
					"  A pipx-installed `agentic-sdlc` once shadowed the kernel on PATH; the "+
					"guards that resolved it\n"+
					"  reported a pass without ever saying which one. See "+
					"provider_compatibility_test.go, which logs the binary it asked.",
				relative, named[1])
		}
	}
}
