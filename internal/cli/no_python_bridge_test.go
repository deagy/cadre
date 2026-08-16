package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The CLI runs no Python.
//
// Every subcommand dispatched through a Python script until the ports landed,
// and internal/interop existed to run them. Nothing calls it now, so it is
// gone -- and this is what keeps it gone, because the way it would come back
// is one subcommand at a time, each looking locally reasonable.
//
// The property is about *executing* an interpreter, not mentioning one. Two
// things in this repository legitimately name Python and must keep working:
//
//   - internal/generators writes a shell snippet into the packaged plugin's
//     hook wrapper, which probes for python3 on the installing machine. That
//     is generated text, not this process running anything.
//   - internal/kernel resolves an interpreter *path* with exec.LookPath, to
//     record what would run for a Python project. It describes; it does not
//     invoke.
//
// So the scan looks for the call that actually starts a process.

// goSourceFiles walks the Go tree, skipping tests -- this file names the
// patterns it forbids in order to assert on them.
func goSourceFiles(t *testing.T) []string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	var files []string
	for _, directory := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, directory),
			func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() || !strings.HasSuffix(path, ".go") ||
					strings.HasSuffix(path, "_test.go") {
					return nil
				}
				files = append(files, path)
				return nil
			})
		if err != nil {
			t.Fatalf("walking %s: %v", directory, err)
		}
	}
	return files
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return working
}

func TestNoGoCodeExecutesAPythonInterpreter(t *testing.T) {
	// exec.Command / exec.CommandContext with an interpreter as the program.
	// A path built into a variable first would slip past a literal scan, which
	// is why the companion check below asserts the bridge package is gone:
	// re-adding one is a new file, and that is visible in review in a way one
	// more exec.Command inside an existing file is not.
	starting := regexp.MustCompile(
		`exec\.Command(Context)?\([^)]*"(python|python3|py)"`)

	files := goSourceFiles(t)
	if len(files) < 50 {
		t.Fatalf("scanned only %d Go files; the walk is broken, not the tree", len(files))
	}
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for number, line := range strings.Split(string(content), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if starting.MatchString(line) {
				t.Errorf("%s:%d starts a Python interpreter:\n  %s\n"+
					"The CLI is Go. A subcommand that needs Python is a subcommand "+
					"that has not been ported.", path, number+1, trimmed)
			}
		}
	}
}

func TestThePythonBridgePackageIsGone(t *testing.T) {
	// internal/interop held PythonSubcommand, the single entry point every
	// dispatched subcommand went through. Its absence is the structural fact;
	// the scan above catches an interpreter started some other way, and this
	// catches the tidy version returning.
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	if _, err := os.Stat(filepath.Join(root, "internal", "interop")); err == nil {
		t.Error("internal/interop is back. If a subcommand genuinely needs to run " +
			"Python again, that is a decision worth making explicitly rather than " +
			"by restoring a bridge.")
	}

	// And no Go file imports it, which is what would fail first if it returned
	// with a caller.
	for _, path := range goSourceFiles(t) {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "cli/internal/interop") {
			t.Errorf("%s imports the Python bridge", path)
		}
	}
}

func TestTheScanWouldNoticeAnInterpreterBeingStarted(t *testing.T) {
	// Guards the guard. The scan above passes over a tree that contains no
	// match, which is also what it would do if the pattern were wrong. This
	// runs the same pattern over lines that must match and lines that must
	// not, so a pattern that stopped matching anything fails here rather than
	// passing silently forever.
	starting := regexp.MustCompile(
		`exec\.Command(Context)?\([^)]*"(python|python3|py)"`)

	for _, forbidden := range []string{
		`cmd := exec.Command("python3", script)`,
		`out, err := exec.Command("python", "-c", src).Output()`,
		`exec.CommandContext(ctx, "python3", args...)`,
	} {
		if !starting.MatchString(forbidden) {
			t.Errorf("the scan would not notice: %s", forbidden)
		}
	}
	for _, permitted := range []string{
		`resolved, err := exec.LookPath("python3")`,
		`for _, name := range []string{"python3", "python"} {`,
		`Set("command", ` + "`" + `python3 "${CLAUDE_PLUGIN_ROOT}/hook.py"` + "`" + `)`,
		`{"python", []string{"pyproject.toml", "requirements.txt"}},`,
		`command := exec.Command("git", "ls-files")`,
	} {
		if starting.MatchString(permitted) {
			t.Errorf("the scan would wrongly reject: %s", permitted)
		}
	}
}
