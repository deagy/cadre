package interop

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindPython310_LocatesAnInterpreter(t *testing.T) {
	// This test only asserts against whatever Python 3.10+ interpreter the
	// test environment provides; if none is available, skip rather than
	// fail, since interpreter availability is an environment property, not
	// something this package controls.
	path, err := FindPython310(context.Background())
	if err != nil {
		t.Skipf("no Python 3.10+ interpreter available in test environment: %v", err)
	}
	if path == "" {
		t.Error("FindPython310() returned empty path with nil error")
	}
}

func TestPythonSubcommand_RunsScriptAndCapturesOutput(t *testing.T) {
	if _, err := FindPython310(context.Background()); err != nil {
		t.Skipf("no Python 3.10+ interpreter available: %v", err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "script.py")
	contents := "import sys\nprint('hello ' + sys.argv[1])\nsys.exit(0)\n"
	if err := os.WriteFile(script, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code, err := PythonSubcommand(context.Background(), script, []string{"world"}, Options{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("PythonSubcommand() error = %v", err)
	}
	if code != 0 {
		t.Errorf("PythonSubcommand() code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hello world") {
		t.Errorf("stdout = %q, want to contain %q", stdout.String(), "hello world")
	}
}

func TestPythonSubcommand_PropagatesNonzeroExit(t *testing.T) {
	if _, err := FindPython310(context.Background()); err != nil {
		t.Skipf("no Python 3.10+ interpreter available: %v", err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "script.py")
	if err := os.WriteFile(script, []byte("import sys\nsys.exit(5)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, err := PythonSubcommand(context.Background(), script, nil, Options{
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("PythonSubcommand() error = %v", err)
	}
	if code != 5 {
		t.Errorf("PythonSubcommand() code = %d, want 5", code)
	}
}

func TestPythonSubcommand_EnvOverride(t *testing.T) {
	if _, err := FindPython310(context.Background()); err != nil {
		t.Skipf("no Python 3.10+ interpreter available: %v", err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "script.py")
	contents := "import os\nprint(os.environ.get('CADRE_INTERACTIVE', 'unset'))\n"
	if err := os.WriteFile(script, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	_, err := PythonSubcommand(context.Background(), script, nil, Options{
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
		Env:    append(os.Environ(), "CADRE_INTERACTIVE=1"),
	})
	if err != nil {
		t.Fatalf("PythonSubcommand() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "1") {
		t.Errorf("stdout = %q, want CADRE_INTERACTIVE=1 to be visible to child", stdout.String())
	}
}
