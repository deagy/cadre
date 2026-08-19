package enginecli

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

// The engine binary must answer the basics with no installation anywhere.
//
// cadre shipped exactly this defect in cli-v0.5.0 -- main resolved an
// installation root before dispatching and exited 1 when it found none, so a
// downloaded binary could not run --version. It was fixed in 0.5.1, and then
// reproduced here in the engine's own main, written from the same instinct
// and shipped in cli-v0.6.0.
//
// The guard that caught it for cadre was scoped to cmd/cadre. A guard that
// covers one binary out of two is why the second one shipped broken, so this
// covers the engine and the two must be added together in future.
var (
	engineOnce sync.Once
	enginePath string
	engineErr  error
)

func buildStandaloneEngine(t *testing.T) string {
	t.Helper()
	engineOnce.Do(func() {
		dir, err := os.MkdirTemp("", "engine-standalone-")
		if err != nil {
			engineErr = err
			return
		}
		binary := filepath.Join(dir, "agentic-sdlc-engine")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		// Built into a temp directory: resolution walks up from the
		// executable, so a binary inside this checkout would find the very
		// contracts this test needs to be absent.
		build := exec.Command("go", "build", "-o", binary, "./cmd/agentic-sdlc-engine")
		working, err := os.Getwd()
		if err != nil {
			engineErr = err
			return
		}
		build.Dir = filepath.Dir(filepath.Dir(filepath.Dir(working)))
		if output, err := build.CombinedOutput(); err != nil {
			engineErr = err
			t.Logf("go build: %s", output)
			return
		}
		enginePath = binary
	})
	if engineErr != nil {
		t.Fatalf("building the engine: %v", engineErr)
	}
	return enginePath
}

func runStandaloneEngine(t *testing.T, args ...string) (int, string) {
	t.Helper()
	command := exec.CommandContext(context.Background(), buildStandaloneEngine(t), args...)
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "CADRE_REPO_ROOT=")

	var combined bytes.Buffer
	command.Stdout, command.Stderr = &combined, &combined
	err := command.Run()

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

func TestTheEngineBinaryAnswersTheBasicsStandalone(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
		code, output := runStandaloneEngine(t, args...)
		if code != 0 {
			t.Errorf("agentic-sdlc-engine %s: exit %d, want 0\n  %s",
				strings.Join(args, " "), code, strings.TrimSpace(output))
			continue
		}
		if !strings.Contains(output, "Commands:") {
			t.Errorf("agentic-sdlc-engine %s printed no command list", strings.Join(args, " "))
		}
	}

	// An unknown command is a usage error, not an installation error.
	if code, _ := runStandaloneEngine(t, "nonsense"); code != 2 {
		t.Errorf("an unknown command exited %d, want 2", code)
	}
}

// A command that needs the contracts says what is missing and how to fix it.
//
// Not merely that it fails: "open kernel/contracts/lifecycle-gates.json: no
// such file or directory" names a path the reader cannot act on.
func TestTheEngineNamesTheMissingContracts(t *testing.T) {
	code, output := runStandaloneEngine(t, "plan", "--root", ".", "--task-id", "x", "--task", "y")
	if code == 0 {
		t.Fatal("plan succeeded with no contracts available")
	}
	if !strings.Contains(output, "CADRE_REPO_ROOT") {
		t.Errorf("the failure does not say how to resolve it:\n  %s", strings.TrimSpace(output))
	}
}
