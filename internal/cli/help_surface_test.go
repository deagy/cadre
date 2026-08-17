package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// `--help` is a question, not a command.
//
// Two properties, each recording a defect that happened:
//
//   - No help output names a .py file. A command that prints its own
//     implementation filename is naming the thing that runs rather than the
//     thing the user typed -- and after the port, every such name is also
//     wrong.
//   - Asking for help changes nothing on disk. test_cli_surface.py calls this
//     "the shape of the generate-authority-aides defect, which regenerated
//     eight files when asked for help": an argv scan that treats an
//     unrecognised flag as the write path.
//
// Ported from test_cli_surface.py's SubcommandNamingTest.

// helpShimPath locates bin/cadre, skipping where it cannot run.
//
// Deliberately local rather than shared with wrapper_shim_test.go: that file
// is a separate change, and a test helper is not worth coupling two
// independent branches over.
func helpShimPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bin/cadre is a POSIX shell script")
	}
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	path := filepath.Join(root, "bin", "cadre")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("the shim builds the CLI and needs a Go toolchain: %v", err)
	}
	return path
}

// subcommandNames reads the dispatch table, which is the single source of
// truth for what the CLI offers.
func subcommandNames(t *testing.T) []string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	raw, err := os.ReadFile(filepath.Join(root, "bin", "subcommands.tsv"))
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	var names []string
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.SplitN(trimmed, "\t", 2)
		names = append(names, strings.TrimSpace(fields[0]))
	}
	if len(names) < 10 {
		t.Fatalf("read %d subcommands; the table was not parsed", len(names))
	}
	return names
}

// runHelp asks one subcommand for help, bounded and with stdin closed so a
// server subcommand cannot sit waiting for a request.
func runHelp(t *testing.T, shim, name string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, shim, name, "--help")
	command.Dir = filepath.Dir(filepath.Dir(mustGetwd(t)))
	command.Stdin = strings.NewReader("")
	output, _ := command.CombinedOutput() // a non-zero exit is fine; the text is the subject
	return string(output)
}

func TestNoSubcommandHelpNamesAnImplementationFile(t *testing.T) {
	shim := helpShimPath(t)
	checked := 0
	for _, name := range subcommandNames(t) {
		output := runHelp(t, shim, name)
		if strings.TrimSpace(output) == "" {
			t.Errorf("`cadre %s --help` printed nothing", name)
			continue
		}
		checked++
		for _, token := range strings.Fields(output) {
			cleaned := strings.Trim(token, "`'\"(),.:;")
			if strings.HasSuffix(cleaned, ".py") {
				t.Errorf("`cadre %s --help` names an implementation file: %s\n"+
					"Name the command the user typed instead.", name, cleaned)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no help output was captured; this test would prove nothing")
	}
	t.Logf("checked help output for %d subcommands", checked)
}

func TestAskingEverySubcommandForHelpChangesNothingOnDisk(t *testing.T) {
	// Compares `git status --porcelain` around the whole surface. An argv scan
	// that treats an unrecognised flag as its default action shows up here and
	// essentially nowhere else -- the command exits 0, prints something
	// plausible, and has rewritten generated files underneath.
	shim := helpShimPath(t)
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))

	treeState := func() string {
		command := exec.Command("git", "status", "--porcelain")
		command.Dir = root
		out, err := command.Output()
		if err != nil {
			t.Skipf("cannot read the working tree state: %v", err)
		}
		return string(out)
	}

	before := treeState()
	for _, name := range subcommandNames(t) {
		runHelp(t, shim, name)
	}
	after := treeState()

	if before != after {
		t.Errorf("`--help` changed the working tree.\n  before:\n%s\n  after:\n%s\n"+
			"A subcommand is treating an unrecognised flag as its default action "+
			"instead of rejecting it.", before, after)
	}
}
