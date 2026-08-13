// Package cli implements the Cadre CLI dispatcher: argument routing,
// version detection, and SDLC delegation. It is an exact behavioral replica
// of bin/cadre.py, ported per ADR-001-CLI-GO-REFACTOR.md and
// CADRE_CLI_GO_ARCHITECTURE.md.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/deagy/cadre/cli/internal/interop"
)

// InteractiveFlag mirrors bin/cadre.py's INTERACTIVE_FLAG.
const InteractiveFlag = "--interactive"

// Subcommand is one row of bin/subcommands.tsv: a name, the Python script it
// dispatches to (relative to the repository root), and a one-line
// description used in usage text.
type Subcommand struct {
	Name        string
	Script      string
	Description string
}

// LoadSubcommands parses bin/subcommands.tsv, mirroring bin/cadre.py's
// load_subcommands(). Each non-empty line is `name\tscript\tdescription`.
func LoadSubcommands(path string) ([]Subcommand, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var rows []Subcommand
	for _, line := range strings.Split(string(contents), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("subcommands.tsv: malformed row (want 3 tab-separated fields, got %d): %q", len(fields), line)
		}
		rows = append(rows, Subcommand{Name: fields[0], Script: fields[1], Description: fields[2]})
	}
	return rows, nil
}

// Usage renders the same usage text as bin/cadre.py's usage().
func Usage(subcommands []Subcommand) string {
	var b strings.Builder
	b.WriteString("Usage: cadre <subcommand> [args...]\n\n")
	b.WriteString("Subcommands:\n")
	for _, row := range subcommands {
		fmt.Fprintf(&b, "  %-16s %s\n", row.Name, row.Description)
	}
	fmt.Fprintf(&b, "  %-16s %s\n", "sdlc", sdlcDescription)
	fmt.Fprintf(&b, "  %-16s %s\n", "help", "Show this message")
	b.WriteString("\n")
	b.WriteString("Each subcommand's own --help documents its arguments, e.g. `cadre sdlc plan --help`.\n\n")
	fmt.Fprintf(&b, "`%s`, given as the leading argument before the subcommand name (e.g. "+
		"`cadre %s select ...`), opts the dispatched subcommand into "+
		"roster/shared/src/settings.py's interactive configuration prompt (CADRE_INTERACTIVE=1, "+
		"passed via an explicit subprocess env= rather than mutating this process's own "+
		"environment) -- only honored when stdin/stdout are both a real terminal; a value entered "+
		"is offered a write to the project-local or user-global cadre config file.\n", InteractiveFlag, InteractiveFlag)
	b.WriteString("For `init`, this is distinct from `cadre init --interactive`, which starts the " +
		"shared-policy overlay questionnaire; use both flags when both prompt flows are needed.")
	return b.String()
}

// Deps collects the dispatcher's runtime dependencies: I/O streams, the
// repository root, and how to run a Python subcommand -- injected so tests
// never touch the real process's stdio or spawn a real interpreter.
type Deps struct {
	Stdout io.Writer
	Stderr io.Writer

	RepoRoot         string
	SubcommandsPath  string
	PythonExecutable func(ctx context.Context, script string, args []string, env []string, stdout, stderr io.Writer, stdin io.Reader) (int, error)
	SDLCDeps         SDLCDeps
}

// Run is an exact behavioral replica of bin/cadre.py's main(): parse
// --version, --interactive, the subcommand name, and dispatch to sdlc
// delegation, a Python subcommand script, help text, or an unknown-command
// error. Returns the process exit code (0 success, 1 error, 2 for a
// dispatcher-level argument problem -- though as in the Python original,
// most argument parsing is delegated to the subcommand itself and errors
// there surface as whatever exit code that subcommand chooses).
func Run(ctx context.Context, argv []string, deps Deps) int {
	if len(argv) == 1 && argv[0] == "--version" {
		version, err := CLIVersion(deps.RepoRoot)
		if err != nil {
			writef(deps.Stderr, "cadre: %s\n", err)
			return 1
		}
		writef(deps.Stdout, "cadre %s\n", version)
		return 0
	}

	interactive := false
	if len(argv) > 0 && argv[0] == InteractiveFlag {
		interactive = true
		argv = argv[1:]
	}

	subcommandsPath := deps.SubcommandsPath
	if subcommandsPath == "" {
		subcommandsPath = filepath.Join(deps.RepoRoot, "bin", "subcommands.tsv")
	}
	subcommands, err := LoadSubcommands(subcommandsPath)
	if err != nil {
		writef(deps.Stderr, "cadre: %s\n", err)
		return 1
	}

	command := "help"
	var rest []string
	if len(argv) > 0 {
		command = argv[0]
		rest = argv[1:]
	}

	if command == "help" || command == "-h" || command == "--help" {
		writeln(deps.Stdout, Usage(subcommands))
		return 0
	}

	if command == "sdlc" {
		return DispatchSDLC(ctx, deps.RepoRoot, rest, interactive, deps.SDLCDeps)
	}

	// Route Go-implemented generators
	if command == "generate-authority-aides" {
		return GenerateAuthorityAides(rest)
	}
	if command == "generate-role-metadata" {
		return GenerateRoleMetadata(rest)
	}

	var match *Subcommand
	for i := range subcommands {
		if subcommands[i].Name == command {
			match = &subcommands[i]
			break
		}
	}
	if match == nil {
		writef(deps.Stderr, "cadre: unknown subcommand '%s'\n", command)
		writeln(deps.Stderr, Usage(subcommands))
		return 1
	}

	runPython := deps.PythonExecutable
	if runPython == nil {
		runPython = defaultPythonExecutable
	}
	code, err := runPython(ctx, filepath.Join(deps.RepoRoot, match.Script), rest, childEnv(interactive), deps.Stdout, deps.Stderr, os.Stdin)
	if err != nil {
		writef(deps.Stderr, "cadre: %s\n", err)
		return 1
	}
	return code
}

// defaultPythonExecutable is the production PythonExecutable: it delegates
// to internal/interop's PythonSubcommand, which locates a Python 3.10+
// interpreter and runs the given script as a subprocess, mirroring
// bin/cadre.py's subprocess.run([sys.executable, script, *rest]).
func defaultPythonExecutable(ctx context.Context, script string, args []string, env []string, stdout, stderr io.Writer, stdin io.Reader) (int, error) {
	return interop.PythonSubcommand(ctx, script, args, interop.Options{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Env:    env,
	})
}

// writef and writeln write CLI output and deliberately discard the write
// error. deps.Stdout/deps.Stderr are ordinary process stdio in production
// (or an in-memory buffer in tests); a write failure there (a closed pipe,
// a full disk on the other end of a redirect) is not something this CLI can
// meaningfully react to differently than just exiting with the exit code
// it already computed -- there is no secondary error-reporting channel to
// escalate a write failure to. This mirrors bin/cadre.py's own behavior:
// Python's print() only raises on a write failure if the caller explicitly
// checks, which bin/cadre.py does not.
func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func writeln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}
