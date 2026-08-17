package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// bin/cadre, the shim every invocation goes through.
//
// It is a POSIX shell script that builds cmd/cadre if needed and execs it.
// Everything else in this repository tests the binary; nothing tested the
// script, and it carries three behaviours that are not the binary's and that
// each already broke once:
//
//   - it exports CADRE_REPO_ROOT, without which the binary can only find its
//     own roster/ by walking up from the caller's working directory -- wrong
//     twice over, since the binary lives under .cadre-build-cache/ and cadre
//     is routinely run from another project;
//   - it buffers `go build` output, because a cold module cache writes
//     "go: downloading ..." to stderr and that is indistinguishable from a CLI
//     diagnostic to anything reading this wrapper's stderr;
//   - it resolves its own path through symlinks by hand, since `readlink -f`
//     is GNU-only and the documented install puts a symlink on PATH.
//
// Ported from test_repository_health.py's bin_agents wrapper tests.

// shimPath locates bin/cadre and skips when the shim cannot run here.
func shimPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bin/cadre is a POSIX shell script; bin/cadre.ps1 is the Windows entry point")
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

// runShim invokes the shim, sharing this checkout's build cache so a warm run
// costs nothing. The environment is passed through rather than cleared: the
// shim needs PATH to find go, and HOME for the module cache.
func runShim(t *testing.T, program string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	command := exec.Command(program, args...)
	command.Dir = filepath.Dir(filepath.Dir(mustGetwd(t)))
	var outBuilder, errBuilder strings.Builder
	command.Stdout = &outBuilder
	command.Stderr = &errBuilder
	err := command.Run()
	code = 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running %s: %v", program, err)
		}
		code = exitErr.ExitCode()
	}
	return outBuilder.String(), errBuilder.String(), code
}

func TestTheShimIsExecutable(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	for _, name := range []string{"cadre", "cadre.ps1"} {
		path := filepath.Join(root, "bin", name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s is missing: %v", name, err)
			continue
		}
		// The PowerShell wrapper is invoked through an interpreter, so its
		// executable bit matters less -- but a repository that ships one
		// without the bit set on the POSIX one is broken for everybody.
		if name == "cadre" && info.Mode().Perm()&0o111 == 0 {
			t.Errorf("bin/%s is not executable (mode %v); a git checkout would "+
				"hand every user a shim they cannot run", name, info.Mode().Perm())
		}
	}
}

func TestTheShimWritesNothingToStderrOnSuccess(t *testing.T) {
	// The contract a cold module cache broke: `go build` writes
	// "go: downloading ..." to stderr, and callers that treat any stderr as a
	// diagnostic -- the Cline vitest suites did -- fail the run. The shim
	// buffers the build and replays it only on failure.
	shim := shimPath(t)
	stdout, stderr, code := runShim(t, shim, "--version")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("`cadre --version` wrote to stderr:\n%s", stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("`cadre --version` wrote nothing to stdout; a silent success " +
			"would satisfy the stderr check above while doing nothing")
	}
}

func TestTheShimRejectsAnUnknownSubcommand(t *testing.T) {
	shim := shimPath(t)
	stdout, stderr, code := runShim(t, shim, "definitely-not-a-subcommand")
	if code == 0 {
		t.Fatalf("an unknown subcommand exited 0\nstdout: %s", stdout)
	}
	if !strings.Contains(stderr+stdout, "definitely-not-a-subcommand") &&
		!strings.Contains(strings.ToLower(stderr+stdout), "unknown") {
		t.Errorf("the refusal names neither the subcommand nor the problem:\n"+
			"stdout: %s\nstderr: %s", stdout, stderr)
	}
}

func TestTheShimResolvesItselfThroughASymlink(t *testing.T) {
	// The documented system-wide install puts a symlink on PATH. The shim
	// resolves its own location by hand because `readlink -f` is GNU-only, so
	// this is the check that the hand-rolled loop works -- and a broken one
	// does not fail loudly, it computes the wrong REPO_ROOT and builds or
	// dispatches against the wrong checkout.
	shim := shimPath(t)
	link := filepath.Join(t.TempDir(), "cadre")
	if err := os.Symlink(shim, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	stdout, stderr, code := runShim(t, link, "--version")
	if code != 0 {
		t.Fatalf("invoked through a symlink, exit %d\nstderr: %s", code, stderr)
	}
	direct, _, _ := runShim(t, shim, "--version")
	if stdout != direct {
		t.Errorf("invoking through a symlink produced different output:\n"+
			"  direct:  %q\n  symlink: %q", direct, stdout)
	}
}

func TestTheShimTellsTheBinaryWhichCheckoutProducedIt(t *testing.T) {
	// CADRE_REPO_ROOT is how the binary finds its own roster/. Dropping the
	// export is not obviously broken, because FindInstallationRoot has two
	// fallbacks after it: the executable's own directory, then cwd.
	//
	// Both have to be denied for this to test anything. The first version of
	// this test ran from outside the checkout but left the binary in
	// .cadre-build-cache/ *inside* it, so the executable-directory fallback
	// found the roster and the test passed with the export deleted -- a
	// mutation caught it.
	//
	// So: build the binary somewhere else, and run from somewhere else again.
	// That leaves CADRE_REPO_ROOT as the only thing that can find the roster,
	// which is exactly the arrangement of a PATH install.
	shim := shimPath(t)
	cache := t.TempDir()
	elsewhere := t.TempDir()

	command := exec.Command(shim, "select", "--task", "add a REST endpoint",
		"--files", "api/main.go", "--task-id", "SHIM-1", "--classification", "internal")
	command.Dir = elsewhere
	command.Env = append(os.Environ(), "CADRE_BUILD_CACHE="+cache)
	var out, errOut strings.Builder
	command.Stdout = &out
	command.Stderr = &errOut
	err := command.Run()
	if err != nil {
		t.Fatalf("`cadre select` with the binary and the cwd both outside the "+
			"checkout failed: %v\nstderr: %s", err, shimHead(errOut.String(), 400))
	}
	if !strings.Contains(out.String(), "\"agents\"") {
		t.Errorf("the plan carries no agents, so the roster was not found:\n%s",
			shimHead(out.String(), 300))
	}
}

func TestTheShimHasNoPythonInIt(t *testing.T) {
	// The shim dispatched every subcommand through a Python interpreter until
	// the port landed. It resolves none now, and this is what keeps it that
	// way: re-adding one is a line in this file, which is a small edit that
	// reads as reasonable on its own.
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	for _, name := range []string{"cadre", "cadre.ps1"} {
		content, err := os.ReadFile(filepath.Join(root, "bin", name))
		if err != nil {
			continue
		}
		for number, line := range strings.Split(string(content), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			for _, interpreter := range []string{"python3 ", "python ", "py -3"} {
				if strings.Contains(line, interpreter) {
					t.Errorf("bin/%s:%d resolves a Python interpreter:\n  %s",
						name, number+1, trimmed)
				}
			}
		}
	}
}

func shimHead(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
