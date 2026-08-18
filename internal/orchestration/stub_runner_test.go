package orchestration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// A dispatch test must not spawn an agent CLI that happens to be installed on
// the machine running it.
//
// DefaultRunner is codex, and the full-dispatch tests ran with whatever
// `codex` resolved to on PATH. On a machine without one the spawn fails
// immediately and the test takes milliseconds; on a machine with one it
// launches a real agent process and waits up to DefaultTimeoutSeconds, which
// is 600. That is where the intermittent ten-minute TestDispatchSyncWait came
// from -- it was never a hang, it was the child timeout elapsing.
//
// Measured on a machine with codex installed: 0.007s for the package with no
// runner on PATH, 3.4s with one, and a 300s timeout when the dispatch tests
// ran together.
//
// The flake is the visible half. The other half is that running `go test`
// launched real agent sessions as a side effect, with whatever credentials and
// network access the developer had.
//
// Applied per test rather than in a TestMain, deliberately. Setting the runner
// binary for the whole package also changes what ExecuteDispatchChild resolves,
// and TestExecuteDispatchChildCodexRunner asserts the *default* resolution --
// a package-wide stub silently rewrites what that test is about. So this is
// for the tests that go through the full dispatch path, which are the ones
// that spawn.

// stubRunner points the runner settings at a script that echoes stdin, the
// same shape dispatch_reaches_child_test.go uses, so a test that inspects
// child output sees consistent behaviour.
func stubRunner(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub runner is a POSIX shell script")
	}
	script := filepath.Join(t.TempDir(), "stub-runner")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
		t.Fatalf("writing the stub runner: %v", err)
	}
	t.Setenv("SECURE_CLOUD_AGENTS_CODEX_BIN", script)
	t.Setenv("SECURE_CLOUD_AGENTS_CLAUDE_BIN", script)
}

func TestTheStubRunnerIsNotTheInstalledAgentCli(t *testing.T) {
	// Guards the guard. If stubRunner ever stopped taking effect, the only
	// symptom would be the suite getting slower on some machines and not
	// others -- which is exactly how the original went unnoticed.
	stubRunner(t)
	for name, runner := range map[string]string{
		"SECURE_CLOUD_AGENTS_CODEX_BIN":  "codex",
		"SECURE_CLOUD_AGENTS_CLAUDE_BIN": "claude",
	} {
		configured := os.Getenv(name)
		if configured == "" {
			t.Errorf("%s is unset after stubRunner, so dispatch would resolve %s "+
				"from PATH", name, runner)
			continue
		}
		if _, err := os.Stat(configured); err != nil {
			t.Errorf("%s names %s, which does not exist: %v", name, configured, err)
			continue
		}
		installed, err := exec.LookPath(runner)
		if err != nil {
			continue // nothing installed here; the stub is belt and braces
		}
		if configured == installed {
			t.Errorf("%s points at the installed %s (%s), so this test would launch "+
				"a real agent session", name, runner, installed)
		}
	}
}

// noRunnerBinary points the runner settings at a path that does not exist, so
// "no binary resolves" is a fact of the test rather than a fact about the
// machine.
//
// Three tests asserted a status of "unavailable" while arranging nothing --
// they were reading the CI runner's PATH, which has no agent CLIs on it. On a
// developer machine that has one, the same tests spawned it and got "failed".
func noRunnerBinary(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "no-such-runner")
	t.Setenv("SECURE_CLOUD_AGENTS_CODEX_BIN", missing)
	t.Setenv("SECURE_CLOUD_AGENTS_CLAUDE_BIN", missing)
}

// The slot an async dispatch takes must go back to the limiter it came from.
//
// dispatchAsync resolved currentDispatchLimiter() twice -- once to acquire,
// once to release -- and the pointer behind it is swappable, which
// dispatch_limiter_isolation_test.go does. Because the release sat inside a
// goroutine, its defer ran after the swap and released into a limiter that had
// never issued it a slot, taking a token that limiter did own. Every later
// dispatch then ran one slot short, and the one that finally found the channel
// empty blocked in Release forever.
//
// dispatchSync was not broken: a deferred call's receiver is evaluated at the
// defer, so it resolved before any swap. The companion test below covers it
// anyway, because that correctness is easy to undo by accident.
//
// It surfaced as TestDispatchSyncWait taking ten minutes, which read as a slow
// child rather than a deadlock, because DefaultTimeoutSeconds is 600 and the
// test had no deadline of its own. This makes the same defect fail in seconds
// with a name.
func TestAnAsyncDispatchReturnsItsSlotToTheLimiterItTookItFrom(t *testing.T) {
	stubRunner(t)

	// One slot, so a stolen token is immediately observable.
	own := NewConcurrencyLimiter(1)
	previous := dispatchLimiter.Swap(own)
	t.Cleanup(func() { dispatchLimiter.Store(previous) })

	result := DispatchSecureCloudRole(
		testRoots(t, "code-reviewer"),
		"code-reviewer", "test brief", ModePlanningOnly, "public",
		"", "limiter-task", "limiter-session", "public", DefaultRunner, false,
	)
	if status, _ := result["status"].(string); status == "denied" {
		t.Fatalf("the dispatch was denied a slot before it started: %v", result)
	}

	// Restore while the child is still in flight. This is the window the bug
	// lived in: the goroutine's deferred release runs after this point.
	dispatchLimiter.Store(previous)

	deadline := time.Now().Add(20 * time.Second)
	for {
		if own.TryAcquire() {
			own.Release()
			return // the slot came back where it belonged
		}
		if time.Now().After(deadline) {
			t.Fatal("the async dispatch never returned its slot to the limiter that " +
				"issued it. The release resolved the limiter separately from the " +
				"acquire, so it took a token from whichever limiter was current " +
				"when the child finished.")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The same property on the synchronous path.
//
// dispatchSync holds its slot only for the duration of the call, so the swap
// has to land while the child is running. A stub that sleeps makes that window
// reliable instead of a race the test would lose most of the time.
func TestASyncDispatchReturnsItsSlotToTheLimiterItTookItFrom(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub runner is a POSIX shell script")
	}
	script := filepath.Join(t.TempDir(), "slow-runner")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 2\nexec cat\n"), 0o755); err != nil {
		t.Fatalf("writing the slow stub runner: %v", err)
	}
	t.Setenv("SECURE_CLOUD_AGENTS_CODEX_BIN", script)
	t.Setenv("SECURE_CLOUD_AGENTS_CLAUDE_BIN", script)

	own := NewConcurrencyLimiter(1)
	previous := dispatchLimiter.Swap(own)
	t.Cleanup(func() { dispatchLimiter.Store(previous) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		DispatchSecureCloudRole(
			testRoots(t, "code-reviewer"),
			"code-reviewer", "test brief", ModePlanningOnly, "public",
			"", "sync-limiter-task", "sync-limiter-session", "public",
			DefaultRunner, true,
		)
	}()

	// Swap while the child is still sleeping, so the deferred release resolves
	// after the pointer moved.
	time.Sleep(300 * time.Millisecond)
	dispatchLimiter.Store(previous)

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the synchronous dispatch never returned. Its deferred release " +
			"resolved the limiter separately from its acquire, so it waited on a " +
			"channel that had never been given its slot.")
	}

	if !own.TryAcquire() {
		t.Error("the synchronous dispatch did not return its slot to the limiter " +
			"that issued it")
		return
	}
	own.Release()
}
