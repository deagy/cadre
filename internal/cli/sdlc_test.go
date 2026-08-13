package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeScriptExt returns the executable-script suffix and shebang-equivalent
// mechanism appropriate for the current OS; these tests only run a trivial
// exit-code-returning script, so a plain shell script suffices on
// POSIX and this suite is skipped on Windows where a .sh script is not
// directly executable.
func requirePOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake sdlc binary is a POSIX shell script")
	}
}

func writeFakeSDLCBinary(t *testing.T, dir string, exitCode int) string {
	t.Helper()
	path := filepath.Join(dir, "fake-sdlc")
	script := "#!/bin/sh\necho \"args: $@\"\nexit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

func TestDispatchSDLC_UsesInTreeFallback(t *testing.T) {
	requirePOSIX(t)

	repoRoot := t.TempDir()
	binDir := filepath.Join(repoRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeSDLCBinary(t, binDir, 0)
	if err := os.Rename(filepath.Join(binDir, "fake-sdlc"), filepath.Join(binDir, "agentic-sdlc")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "provider"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "provider", "provider.json"), []byte(`{"kernel_compatibility":{"minimum":"0.1.0","maximum_exclusive":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := DispatchSDLC(context.Background(), repoRoot, []string{"plan"}, false, SDLCDeps{
		Stdin:          bytes.NewReader(nil),
		Stdout:         &stdout,
		Stderr:         &stderr,
		InstallMessage: func() string { return "unused" },
	})

	if code != 0 {
		t.Fatalf("DispatchSDLC() exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if got := stdout.String(); got == "" {
		t.Error("expected fake sdlc binary output on stdout, got none")
	}
}

func TestDispatchSDLC_NoBinaryFound(t *testing.T) {
	repoRoot := t.TempDir() // no bin/agentic-sdlc present

	var stdout, stderr bytes.Buffer
	code := DispatchSDLC(context.Background(), repoRoot, []string{"plan"}, false, SDLCDeps{
		Stdin:          bytes.NewReader(nil),
		Stdout:         &stdout,
		Stderr:         &stderr,
		InstallMessage: func() string { return "install message" },
	})

	if code != 1 {
		t.Errorf("DispatchSDLC() exit code = %d, want 1", code)
	}
	if stderr.String() == "" {
		t.Error("expected install message on stderr")
	}
}

func TestDispatchSDLC_NonzeroExitPropagates(t *testing.T) {
	requirePOSIX(t)

	repoRoot := t.TempDir()
	binDir := filepath.Join(repoRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeSDLCBinary(t, binDir, 3)
	if err := os.Rename(filepath.Join(binDir, "fake-sdlc"), filepath.Join(binDir, "agentic-sdlc")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := DispatchSDLC(context.Background(), repoRoot, []string{"plan"}, false, SDLCDeps{
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if code != 3 {
		t.Errorf("DispatchSDLC() exit code = %d, want 3", code)
	}
}

func TestSDLCInstallMessage_ReadsKernelCompatibility(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, "provider"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"kernel_compatibility":{"minimum":"0.13.3","maximum_exclusive":"1.0.0"}}`
	if err := os.WriteFile(filepath.Join(repoRoot, "provider", "provider.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	got := SDLCInstallMessage(repoRoot)
	want := "cadre: Agentic SDLC v0.13.3 or newer (below v1.0.0) is required; install it from https://github.com/deagy/cadre"
	if got != want {
		t.Errorf("SDLCInstallMessage() = %q, want %q", got, want)
	}
}

func TestSDLCInstallMessage_FallsBackWhenManifestMissing(t *testing.T) {
	repoRoot := t.TempDir()
	got := SDLCInstallMessage(repoRoot)
	want := "cadre: Agentic SDLC a compatible version is required; install it from https://github.com/deagy/cadre"
	if got != want {
		t.Errorf("SDLCInstallMessage() = %q, want %q", got, want)
	}
}
