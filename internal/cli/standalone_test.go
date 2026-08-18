package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// buildStandaloneCadre compiles the CLI once and returns its path.
//
// Compiled rather than called in-process, because the bug being guarded lives
// in main() and in what the process can discover about itself: resolution
// walks up from the executable and the working directory. A unit test running
// under `go test` sits inside this checkout and finds a roster/ either way, so
// it cannot express "no installation anywhere" -- the first version of this
// test passed for that reason while the released binary was broken.
var (
	standaloneOnce sync.Once
	standalonePath string
	standaloneErr  error
)

func buildStandaloneCadre(t *testing.T) string {
	t.Helper()
	standaloneOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cadre-standalone-")
		if err != nil {
			standaloneErr = err
			return
		}
		binary := filepath.Join(dir, "cadre")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		// Built into a temp directory, not the checkout: resolution walks up
		// from the executable, so a binary left in ./dist would find the very
		// roster/ this test needs to be absent.
		build := exec.Command("go", "build", "-o", binary, "./cmd/cadre")
		build.Dir = filepath.Dir(filepath.Dir(mustGetwd(t)))
		if output, err := build.CombinedOutput(); err != nil {
			standaloneErr = err
			t.Logf("go build: %s", output)
			return
		}
		standalonePath = binary
	})
	if standaloneErr != nil {
		t.Fatalf("building the CLI: %v", standaloneErr)
	}
	return standalonePath
}

// runStandalone runs the built CLI from a directory that is not a checkout.
func runStandalone(t *testing.T, args ...string) (int, string) {
	t.Helper()
	binary := buildStandaloneCadre(t)

	cmd := exec.CommandContext(context.Background(), binary, args...)
	// A temp cwd with no roster/ above it, and no CADRE_REPO_ROOT to point
	// resolution back at one.
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "CADRE_REPO_ROOT=")

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()

	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running %v: %v", args, err)
		}
		code = exit.ExitCode()
	}
	return code, combined.String()
}

// What a fresh download must answer, with no installation anywhere.
//
// A released archive contains the executable and nothing else. main() used to
// resolve the repository root before dispatching and exit 1 if it could not,
// so every one of these failed on the published artifact -- including
// --version, which is what a person runs to check a download, and doctor,
// whose entire job is to explain a situation like this one.
//
// The release smoke test could not catch it: it runs from the checkout, where
// a root always resolves.
func TestAStandaloneBinaryAnswersTheBasics(t *testing.T) {
	for _, args := range [][]string{
		{"--version"},
		{"--help"},
		{"-h"},
		{"help"},
		{"doctor"},
	} {
		code, output := runStandalone(t, args...)
		if code != 0 {
			t.Errorf("cadre %s standalone: exit %d, want 0\n  %s",
				strings.Join(args, " "), code, strings.TrimSpace(output))
			continue
		}
		if strings.TrimSpace(output) == "" {
			t.Errorf("cadre %s standalone: exit 0 but printed nothing", strings.Join(args, " "))
		}
	}
}

// --version must name a build, not print a placeholder.
func TestAStandaloneBinaryReportsItsOwnVersion(t *testing.T) {
	code, output := runStandalone(t, "--version")
	if code != 0 {
		t.Fatalf("cadre --version standalone: exit %d\n  %s", code, output)
	}
	printed := strings.TrimSpace(output)
	version := strings.TrimPrefix(printed, "cadre ")
	if version == printed || version == "" || version == "unknown" || !strings.ContainsRune(version, '.') {
		t.Errorf("cadre --version standalone printed %q, which names no build", printed)
	}
}

// A command that genuinely needs an installation must still refuse, and say
// what was missing.
//
// Removing the blanket failure must not have produced commands that proceed on
// nothing: `cadre select` with no roster has no rules to route by, and
// answering anything at all would be worse than failing.
func TestStandaloneCommandsNeedingAnInstallationSayWhatIsMissing(t *testing.T) {
	for _, args := range [][]string{
		{"select", "--task", "anything"},
		{"generate-plugin", "--output", t.TempDir()},
		{"resolve-shared", "team-profile.yaml"},
	} {
		code, output := runStandalone(t, args...)
		if code == 0 {
			t.Errorf("cadre %s standalone: exit 0, want a failure\n  %s",
				strings.Join(args, " "), strings.TrimSpace(output))
			continue
		}
		if !strings.Contains(output, "CADRE_REPO_ROOT") && !strings.Contains(output, "checkout") {
			t.Errorf("cadre %s standalone failed without naming what it needed:\n  %s",
				strings.Join(args, " "), strings.TrimSpace(output))
		}
	}
}
