// Package interop is the fallback mechanism for invoking a Cadre subcommand
// that has not yet been ported to Go: it locates a Python 3.10+ interpreter
// and shells out to the subcommand's script under roster/*/src/, mirroring
// bin/cadre.py's own subprocess.run([sys.executable, script, *rest]) call.
//
// This exists because CADRE_CLI_GO_ARCHITECTURE.md's porting plan is
// incremental: several subcommands (knowledge, context, init,
// bootstrap-codex, mcp-dispatch-server, gitlab-evidence, role-fidelity) are
// deliberately staying Python for the foreseeable future, and even the
// subcommands slated for later phases are Python until their own port
// lands. This package is what keeps every subcommand runnable throughout
// that transition, not just the ones Phase 1 covers.
package interop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// ErrNoPython310 is returned when no interpreter meeting the minimum
// version could be located.
var ErrNoPython310 = errors.New("interop: no Python 3.10+ interpreter found on PATH")

// pythonCandidates mirrors the interpreter probing bin/cadre (the POSIX sh
// shim) performs before handing off to bin/cadre.py: try python3 first,
// then python, in that order. Each candidate is version-checked below --
// being found on PATH under one of these names is not sufficient, since
// `python` may resolve to a Python 2 interpreter on some systems.
var pythonCandidates = []string{"python3", "python"}

// Options carries the pieces of a Python subcommand invocation that differ
// between production use and tests, mirroring the stdio and environment
// arguments SDLCDeps carries for the sibling sdlc.go dispatch path.
type Options struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Env is the exact environment to run the child with. A nil value
	// means "inherit this process's environment unmodified" (exec.Cmd's
	// own nil-Env convention), matching bin/cadre.py's _child_env(False)
	// returning None. Callers wanting CADRE_INTERACTIVE=1 set should build
	// this themselves (e.g. append("CADRE_INTERACTIVE=1") to os.Environ())
	// rather than this package guessing at interactivity from a bool, so
	// the same interactive-env construction logic used for SDLC delegation
	// stays in one place (internal/cli's childEnv).
	Env []string
}

// defaultOptions fills unset stdio fields with the process's own standard
// streams, mirroring subprocess.run's default of inheriting the parent's
// file descriptors when none are redirected.
func (o Options) defaultOptions() Options {
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	return o
}

// PythonSubcommand invokes a Cadre subcommand implemented as a Python
// script, mirroring bin/cadre.py's dispatch for any row of
// bin/subcommands.tsv that has not been ported to Go: it locates a
// Python 3.10+ interpreter, runs `<python> <script> <args...>`, and
// returns the child's exit code.
//
// script must already be an absolute (or otherwise directly runnable) path
// -- resolving it relative to the repository root is the caller's
// responsibility (see platform.RepoRoot()), not this package's, so this
// package stays agnostic of where the repository root search itself lives.
//
// Uses exec.CommandContext (not exec.Cmd.Start+Wait split, and never
// os.StartProcess) so cancellation of ctx terminates the child, and so
// argument quoting is handled correctly cross-platform -- the same
// reasoning bin/cadre.py's own comment gives for using subprocess.run
// rather than os.execv: joining argv into a command-line string without
// list2cmdline-equivalent quoting on Windows silently re-splits any
// argument containing a space.
func PythonSubcommand(ctx context.Context, script string, args []string, opts Options) (int, error) {
	python, err := FindPython310(ctx)
	if err != nil {
		return 1, err
	}

	opts = opts.defaultOptions()

	cmdArgs := make([]string, 0, len(args)+1)
	cmdArgs = append(cmdArgs, script)
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.CommandContext(ctx, python, cmdArgs...)
	cmd.Env = opts.Env
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr

	runErr := cmd.Run()
	if runErr == nil {
		return 0, nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	// The interpreter itself could not be started (removed after the probe
	// above, permission error, etc.).
	return 1, fmt.Errorf("interop: failed to run %s: %w", python, runErr)
}

// FindPython310 locates a Python interpreter satisfying the CLI's minimum
// supported version (3.10), probing candidates in the same order as
// bin/cadre's POSIX sh shim (python3, then python), version-checking each
// with `<candidate> -c "import sys; exit(0 if sys.version_info >= (3, 10)
// else 1)"` rather than trusting the name alone.
func FindPython310(ctx context.Context) (string, error) {
	for _, candidate := range pythonCandidates {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		checkCmd := exec.CommandContext(ctx, path, "-c",
			"import sys; sys.exit(0 if sys.version_info >= (3, 10) else 1)")
		if checkCmd.Run() == nil {
			return path, nil
		}
	}
	return "", ErrNoPython310
}
