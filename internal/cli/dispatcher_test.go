package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var errInteropUnavailable = errors.New("interop: unavailable in test")

func writeSubcommandsTSV(t *testing.T, dir string, rows [][3]string) string {
	t.Helper()
	var b strings.Builder
	for _, row := range rows {
		b.WriteString(row[0] + "\t" + row[1] + "\t" + row[2] + "\n")
	}
	path := filepath.Join(dir, "subcommands.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSubcommands(t *testing.T) {
	dir := t.TempDir()
	path := writeSubcommandsTSV(t, dir, [][3]string{
		{"select", "roster/orchestration/src/select_agents.py", "Deterministic agent/gate selection"},
		{"config", "roster/shared/src/settings.py", "Show resolved operator settings"},
	})

	rows, err := LoadSubcommands(path)
	if err != nil {
		t.Fatalf("LoadSubcommands() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("LoadSubcommands() len = %d, want 2", len(rows))
	}
	if rows[0].Name != "select" || rows[0].Script != "roster/orchestration/src/select_agents.py" {
		t.Errorf("rows[0] = %+v, unexpected", rows[0])
	}
}

func TestLoadSubcommands_MalformedRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subcommands.tsv")
	if err := os.WriteFile(path, []byte("only-two\tfields\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSubcommands(path); err == nil {
		t.Error("LoadSubcommands() error = nil, want error for malformed row")
	}
}

func TestRun_Help(t *testing.T) {
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][3]string{
		{"select", "roster/orchestration/src/select_agents.py", "Deterministic agent/gate selection"},
	})

	var stdout, stderr bytes.Buffer
	deps := Deps{
		Stdout:          &stdout,
		Stderr:          &stderr,
		RepoRoot:        dir,
		SubcommandsPath: subPath,
	}

	for _, argv := range [][]string{{}, {"help"}, {"-h"}, {"--help"}} {
		stdout.Reset()
		stderr.Reset()
		code := Run(context.Background(), argv, deps)
		if code != 0 {
			t.Errorf("Run(%v) code = %d, want 0", argv, code)
		}
		if !strings.Contains(stdout.String(), "Usage: cadre <subcommand>") {
			t.Errorf("Run(%v) stdout missing usage text: %s", argv, stdout.String())
		}
		if !strings.Contains(stdout.String(), "select") {
			t.Errorf("Run(%v) stdout missing subcommand listing: %s", argv, stdout.String())
		}
	}
}

func TestRun_UnknownSubcommand(t *testing.T) {
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, nil)

	var stdout, stderr bytes.Buffer
	deps := Deps{
		Stdout:          &stdout,
		Stderr:          &stderr,
		RepoRoot:        dir,
		SubcommandsPath: subPath,
	}

	code := Run(context.Background(), []string{"does-not-exist"}, deps)
	if code != 1 {
		t.Errorf("Run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand 'does-not-exist'") {
		t.Errorf("stderr = %q, missing unknown-subcommand message", stderr.String())
	}
}

func TestRun_RoutesToSubcommandScript(t *testing.T) {
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][3]string{
		{"config", "roster/shared/src/settings.py", "Show resolved operator settings"},
	})

	var gotScript string
	var gotArgs []string
	var gotEnv []string

	deps := Deps{
		Stdout:          io.Discard,
		Stderr:          io.Discard,
		RepoRoot:        dir,
		SubcommandsPath: subPath,
		PythonExecutable: func(ctx context.Context, script string, args []string, env []string, stdout, stderr io.Writer, stdin io.Reader) (int, error) {
			gotScript = script
			gotArgs = append([]string(nil), args...)
			gotEnv = env
			return 0, nil
		},
	}

	code := Run(context.Background(), []string{"config", "--task", "foo"}, deps)
	if code != 0 {
		t.Fatalf("Run() code = %d, want 0", code)
	}
	wantScript := filepath.Join(dir, "roster/shared/src/settings.py")
	if gotScript != wantScript {
		t.Errorf("script = %q, want %q", gotScript, wantScript)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "--task" || gotArgs[1] != "foo" {
		t.Errorf("args = %v, want [--task foo]", gotArgs)
	}
	if gotEnv != nil {
		t.Errorf("env = %v, want nil for non-interactive dispatch", gotEnv)
	}
}

func TestRun_InteractiveFlagSetsChildEnv(t *testing.T) {
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][3]string{
		{"config", "roster/shared/src/settings.py", "Show resolved operator settings"},
	})

	var gotEnv []string
	var gotArgs []string
	deps := Deps{
		Stdout:          io.Discard,
		Stderr:          io.Discard,
		RepoRoot:        dir,
		SubcommandsPath: subPath,
		PythonExecutable: func(ctx context.Context, script string, args []string, env []string, stdout, stderr io.Writer, stdin io.Reader) (int, error) {
			gotEnv = env
			gotArgs = args
			return 0, nil
		},
	}

	code := Run(context.Background(), []string{InteractiveFlag, "config", "show"}, deps)
	if code != 0 {
		t.Fatalf("Run() code = %d, want 0", code)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "show" {
		t.Errorf("args = %v, want [show] (the --interactive flag must be consumed, not forwarded)", gotArgs)
	}
	found := false
	for _, kv := range gotEnv {
		if kv == "CADRE_INTERACTIVE=1" {
			found = true
		}
	}
	if !found {
		t.Errorf("env = %v, want CADRE_INTERACTIVE=1 present", gotEnv)
	}
}

func TestRun_SubcommandExitCodePropagates(t *testing.T) {
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][3]string{
		{"config", "roster/shared/src/settings.py", "desc"},
	})

	deps := Deps{
		Stdout:          io.Discard,
		Stderr:          io.Discard,
		RepoRoot:        dir,
		SubcommandsPath: subPath,
		PythonExecutable: func(ctx context.Context, script string, args []string, env []string, stdout, stderr io.Writer, stdin io.Reader) (int, error) {
			return 7, nil
		},
	}

	code := Run(context.Background(), []string{"config"}, deps)
	if code != 7 {
		t.Errorf("Run() code = %d, want 7", code)
	}
}

func TestRun_SubcommandExecutionErrorReturnsOne(t *testing.T) {
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][3]string{
		{"config", "roster/shared/src/settings.py", "desc"},
	})

	deps := Deps{
		Stdout:          io.Discard,
		Stderr:          io.Discard,
		RepoRoot:        dir,
		SubcommandsPath: subPath,
		PythonExecutable: func(ctx context.Context, script string, args []string, env []string, stdout, stderr io.Writer, stdin io.Reader) (int, error) {
			return 1, errInteropUnavailable
		},
	}

	code := Run(context.Background(), []string{"config"}, deps)
	if code != 1 {
		t.Errorf("Run() code = %d, want 1", code)
	}
}

func TestRun_MissingSubcommandsFile(t *testing.T) {
	dir := t.TempDir()
	deps := Deps{
		Stdout:          io.Discard,
		Stderr:          io.Discard,
		RepoRoot:        dir,
		SubcommandsPath: filepath.Join(dir, "does-not-exist.tsv"),
	}
	code := Run(context.Background(), []string{"select"}, deps)
	if code != 1 {
		t.Errorf("Run() code = %d, want 1", code)
	}
}

func TestUsage_ListsAllSubcommandsAndSDLC(t *testing.T) {
	subs := []Subcommand{
		{Name: "select", Script: "x.py", Description: "Deterministic selection"},
	}
	out := Usage(subs)
	if !strings.Contains(out, "select") {
		t.Error("Usage() missing subcommand name")
	}
	if !strings.Contains(out, "sdlc") {
		t.Error("Usage() missing sdlc row")
	}
	if !strings.Contains(out, "Deterministic selection") {
		t.Error("Usage() missing description")
	}
}

func TestRun_GenerateRoleMetadataRouting(t *testing.T) {
	// Test that generate-role-metadata is properly routed to the Go CLI
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][3]string{})

	var stderr bytes.Buffer
	deps := Deps{
		Stdout:          io.Discard,
		Stderr:          &stderr,
		RepoRoot:        dir,
		SubcommandsPath: subPath,
	}

	// generate-role-metadata should be routed to the Go CLI
	// It will fail because the repo root structure doesn't exist, but it should
	// reach the Go CLI code path (not Python dispatch)
	code := Run(context.Background(), []string{"generate-role-metadata", "--help"}, deps)

	// The command should exit with code 2 (flag parsing error for --help)
	if code != 2 {
		t.Errorf("Run() for generate-role-metadata code = %d, want 2", code)
	}
}

func TestRun_GeneratePluginRouting(t *testing.T) {
	// Test that generate-plugin is properly routed to the Go CLI
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][3]string{})

	var stderr bytes.Buffer
	deps := Deps{
		Stdout:          io.Discard,
		Stderr:          &stderr,
		RepoRoot:        dir,
		SubcommandsPath: subPath,
	}

	// generate-plugin without --output should exit with 2
	code := Run(context.Background(), []string{"generate-plugin"}, deps)

	// Should fail with code 2 (missing required --output)
	if code != 2 {
		t.Errorf("Run() for generate-plugin code = %d, want 2", code)
	}
}

func TestRun_SelectRouting(t *testing.T) {
	// Test that select is properly routed to the Go CLI
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][3]string{})

	var stderr bytes.Buffer
	deps := Deps{
		Stdout:          io.Discard,
		Stderr:          &stderr,
		RepoRoot:        dir,
		SubcommandsPath: subPath,
	}

	// select without required flags should exit with 2
	code := Run(context.Background(), []string{"select"}, deps)

	// Should fail with code 2 (missing required --task and --task-id)
	if code != 2 {
		t.Errorf("Run() for select code = %d, want 2", code)
	}
}
