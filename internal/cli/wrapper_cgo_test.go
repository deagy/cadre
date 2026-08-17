package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bin/cadre must build a knowledge-capable binary, and must not become
// unbuildable when it cannot.
//
// The wrapper used to run a bare `go build`, which inherits `go env
// CGO_ENABLED` -- 0 on plenty of machines. mattn/go-sqlite3 ships a cgo-less
// stub, so that binary links cleanly and then fails *every* `cadre knowledge`
// call at runtime with "Binary was compiled with 'CGO_ENABLED=0'". Nothing
// warned at build time, so a checkout could sit in that state indefinitely.
//
// Forcing CGO_ENABLED=1 unconditionally trades one failure for a worse one:
// with no C toolchain the build fails outright and the whole CLI stops
// working, not just `knowledge`. Hence prefer-then-fall-back, pinned here from
// both directions.
//
// Each case uses its own CADRE_BUILD_CACHE, so it neither reads nor clobbers
// the developer's real .cadre-build-cache/ -- and so the second case cannot
// pass by finding a cgo binary the first case built.
//
// Ported from test_cli_surface.py's WrapperCgoBuildTest.

// runShimIsolated invokes bin/cadre with its own build cache and a cleared
// CGO_ENABLED, so the wrapper's own decision is what gets exercised rather
// than whatever the developer's environment happens to say.
func runShimIsolated(t *testing.T, cache string, extraEnv []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	shim := cgoShimPath(t)
	command := exec.Command(shim, args...)
	command.Dir = filepath.Dir(filepath.Dir(mustGetwd(t)))
	command.Stdin = strings.NewReader("")

	environment := make([]string, 0, len(os.Environ())+len(extraEnv)+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "CGO_ENABLED=") || strings.HasPrefix(entry, "CADRE_BUILD_CACHE=") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "CADRE_BUILD_CACHE="+cache)
	environment = append(environment, extraEnv...)
	command.Env = environment

	var out, errOut strings.Builder
	command.Stdout = &out
	command.Stderr = &errOut
	if err := command.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running the wrapper: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errOut.String(), code
}

func cgoShimPath(t *testing.T) string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	path := filepath.Join(root, "bin", "cadre")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("the wrapper builds the CLI and needs a Go toolchain: %v", err)
	}
	return path
}

func TestTheWrapperBuildsACgoBinaryWhereACCompilerExists(t *testing.T) {
	// The original defect. `cadre doctor` reports whether the knowledge store
	// is usable, which is exactly the thing a CGO_ENABLED=0 build breaks.
	if _, err := exec.LookPath("cc"); err != nil {
		if _, err := exec.LookPath("gcc"); err != nil {
			t.Skip("needs a C toolchain to build with cgo")
		}
	}
	// doctor exits non-zero on a cwd/binary mismatch, which is expected here
	// because the binary lives in a temp cache. The knowledge-store line is
	// what this is about, not the exit code.
	stdout, stderr, _ := runShimIsolated(t, t.TempDir(), nil, "doctor")
	if !strings.Contains(stdout, "knowledge store:    available") {
		t.Errorf("bin/cadre did not build with cgo where a C compiler exists, so "+
			"`cadre knowledge` is dead on arrival.\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	}
}

func TestTheWrapperFallsBackAndStaysUsableWithoutACCompiler(t *testing.T) {
	// The other direction. Removing the fallback would make a machine with no
	// C compiler unable to run `cadre` at all -- a much larger failure than
	// the one it was meant to fix.
	//
	// CC is pointed at nothing rather than PATH being emptied: the wrapper
	// needs `go` on PATH to build at all, so hiding the toolchain wholesale
	// would test a different refusal.
	stdout, stderr, code := runShimIsolated(t, t.TempDir(),
		[]string{"CC=/nonexistent/cc"}, "--version")
	if code != 0 {
		t.Fatalf("the wrapper must fall back to a cgo-less build rather than "+
			"failing.\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !regexp.MustCompile(`^cadre \S+\n$`).MatchString(stdout) {
		t.Errorf("unexpected --version output: %q", stdout)
	}
	// The buffering contract still holds on the fallback path. A cold-cache
	// build writes "go: downloading ..." to stderr, and anything that leaks
	// breaks --version for every stderr-sensitive caller.
	if stderr != "" {
		t.Errorf("the fallback build leaked to stderr:\n%s", stderr)
	}
}
