package generators

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The packaged selector analyses the caller's repository, not its own.
//
// An installed plugin lives somewhere on the user's machine and is run from
// inside their project. If it resolved the repository root from its own
// location -- or from wherever the binary happens to sit -- every plan it
// produced would describe the wrong repository: the plugin's files, the
// plugin's git status, and a set of agents chosen for none of the caller's
// work.
//
// That failure is not loud. A plan comes back, it is well-formed, and it names
// real roles. Only the inputs are wrong.
//
// Ported from test_repository_health.py's
// test_packaged_selector_targets_callers_git_repository.

// consumerRepository builds a git repository that is not this checkout, with
// one commit, and returns its path and that commit's sha.
func consumerRepository(t *testing.T) (root, baseCommit string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "unrelated")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = root
		// A deterministic identity, so this does not depend on the developer's
		// global git config or fail on a machine that has none.
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "base")
	baseCommit = run("rev-parse", "HEAD")

	resolved, err := filepath.EvalSymlinks(root)
	if err == nil {
		root = resolved
	}
	return root, baseCommit
}

// runPackagedSelect runs the packaged wrapper from inside dir and returns the
// decoded plan.
func runPackagedSelect(t *testing.T, wrapper, binary, dir string, args ...string) map[string]any {
	t.Helper()
	command := exec.Command(wrapper, append([]string{"select"}, args...)...)
	command.Dir = dir
	command.Stdin = strings.NewReader("")
	command.Env = append(os.Environ(), "CADRE_BINARY="+binary)
	var out, errOut strings.Builder
	command.Stdout = &out
	command.Stderr = &errOut
	if err := command.Run(); err != nil {
		t.Fatalf("the packaged selector failed in %s: %v\nstderr:\n%s",
			dir, err, errOut.String())
	}
	var plan map[string]any
	if err := json.Unmarshal([]byte(out.String()), &plan); err != nil {
		t.Fatalf("the plan is not JSON: %v\n%s", err, out.String())
	}
	return plan
}

// packagedWrapper returns the committed distribution's wrapper, and a binary
// for it to run.
//
// The committed plugin/ rather than a freshly generated one: the wrapper reads
// its version from .claude-plugin/plugin.json, which is hand-authored and so
// absent from a generated tree. The committed copy is also what a
// GitHub-sourced marketplace actually serves, which makes it the right subject.
//
// The wrapper is a downloader -- it fetches a released binary for the platform
// -- so it is pointed at one built here through CADRE_BINARY, the override it
// offers for exactly this. What is under test is the wrapper's resolution of
// *which repository to analyse*, not its download path.
func packagedWrapper(t *testing.T) (wrapper, binary string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the packaged wrapper is a POSIX sh script")
	}
	for _, tool := range []string{"git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("needs %s: %v", tool, err)
		}
	}
	root := repositoryRoot(t)
	wrapper = filepath.Join(root, "plugin", "bin", "cadre")
	if _, err := os.Stat(wrapper); err != nil {
		t.Skipf("no committed distribution wrapper: %v", err)
	}

	binary = filepath.Join(t.TempDir(), "cadre")
	build := exec.Command("go", "build", "-o", binary, "./cmd/cadre")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := build.CombinedOutput(); err != nil {
		// Fatal, not a skip. A missing toolchain is a legitimate reason to sit
		// this out; this repository failing to compile is not, and skipping
		// there makes the test disappear exactly when the CLI is broken. A
		// mutation caught this: it did not compile, the build failed, and the
		// run reported ok.
		t.Fatalf("cannot build the CLI for the wrapper to run: %v\n%s", err, out)
	}
	return wrapper, binary
}

func stringsFrom(value any) []string {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range list {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func TestThePackagedSelectorReportsTheCallersRepository(t *testing.T) {
	wrapper, binary := packagedWrapper(t)
	consumer, _ := consumerRepository(t)

	// An uncommitted file, which is what `git status` sees.
	if err := os.MkdirAll(filepath.Join(consumer, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "frontend", "App.tsx"),
		[]byte("export default 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := runPackagedSelect(t, wrapper, binary, consumer, "--task", "Update React")
	inputs, ok := plan["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("the plan carries no inputs: %v", plan)
	}

	reported, _ := inputs["repository_root"].(string)
	if reported != consumer {
		t.Errorf("repository_root = %q, want the caller's repository %q\n"+
			"An installed plugin that resolves its own location instead would "+
			"produce a well-formed plan about the wrong repository.",
			reported, consumer)
	}
	changed := stringsFrom(inputs["changed_files"])
	if len(changed) != 1 || changed[0] != "frontend/App.tsx" {
		t.Errorf("changed_files = %v, want [frontend/App.tsx] from the caller's "+
			"working tree", changed)
	}
}

func TestThePackagedSelectorDiffsAgainstTheCallersBaseCommit(t *testing.T) {
	// The other input path. `--base <sha>` resolves against the caller's
	// history, and a selector reading its own repository would either find no
	// such commit or diff something unrelated.
	wrapper, binary := packagedWrapper(t)
	consumer, base := consumerRepository(t)

	if err := os.MkdirAll(filepath.Join(consumer, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "frontend", "App.tsx"),
		[]byte("export default 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "frontend"}} {
		command := exec.Command("git", args...)
		command.Dir = consumer
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	plan := runPackagedSelect(t, wrapper, binary, consumer, "--task", "Update React", "--base", base)
	inputs, _ := plan["inputs"].(map[string]any)
	changed := stringsFrom(inputs["changed_files"])
	if len(changed) != 1 || changed[0] != "frontend/App.tsx" {
		t.Errorf("changed_files = %v, want [frontend/App.tsx] from the caller's "+
			"diff against %s", changed, base[:8])
	}
}
