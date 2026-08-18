package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSubcommandsTSV(t *testing.T, dir string, rows [][2]string) string {
	t.Helper()
	var b strings.Builder
	for _, row := range rows {
		b.WriteString(row[0] + "\t" + row[1] + "\n")
	}
	path := filepath.Join(dir, "subcommands.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSubcommands(t *testing.T) {
	dir := t.TempDir()
	path := writeSubcommandsTSV(t, dir, [][2]string{
		{"select", "Deterministic agent/gate selection"},
		{"config", "Show resolved operator settings"},
	})

	rows, err := LoadSubcommands(path)
	if err != nil {
		t.Fatalf("LoadSubcommands() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("LoadSubcommands() len = %d, want 2", len(rows))
	}
	if rows[0].Name != "select" {
		t.Errorf("rows[0] = %+v, unexpected", rows[0])
	}
}

func TestLoadSubcommands_MalformedRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subcommands.tsv")
	if err := os.WriteFile(path, []byte("only\tone\ttoo-many\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSubcommands(path); err == nil {
		t.Error("LoadSubcommands() error = nil, want error for malformed row")
	}
}

func TestRun_Help(t *testing.T) {
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][2]string{
		{"select", "Deterministic agent/gate selection"},
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

func TestRun_RefusesATableEntryWithNoGoRoute(t *testing.T) {
	// This replaces TestRun_RoutesToSubcommandScript, which asserted that a
	// subcommands.tsv row dispatched to the Python script it named.
	//
	// That fallback is gone. Every subcommand this dispatcher serves is
	// routed in Go before the table is consulted, so the fallback had become
	// unreachable, and the script column that fed it was removed along with
	// the packaged plugin's Python fallback -- the two shipped as one
	// mechanism.
	//
	// A name the table describes but nothing implements is therefore an
	// unknown subcommand, not a silent exec of whatever the row pointed at.
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][2]string{
		{"widget", "A described subcommand with no implementation"},
	})

	var stderr strings.Builder
	code := Run(context.Background(), []string{"widget", "--task", "foo"}, Deps{
		Stdout:          io.Discard,
		Stderr:          &stderr,
		RepoRoot:        dir,
		SubcommandsPath: subPath,
	})

	if code != 1 {
		t.Errorf("Run() code = %d, want 1 for a name with no implementation", code)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand 'widget'") {
		t.Errorf("stderr = %q, want an unknown-subcommand message", stderr.String())
	}
}

func TestChildEnvCarriesTheInteractiveOptIn(t *testing.T) {
	// Was TestRun_InteractiveFlagSetsChildEnv, which observed the env through
	// the Python-script fallback. That fallback is gone, so the env-building
	// itself is what there is to test -- and it is a pure function, which is
	// a better place to assert it than a dispatch path that happened to
	// expose it.
	env := childEnv(true)
	found := false
	for _, kv := range env {
		if kv == "CADRE_INTERACTIVE=1" {
			found = true
		}
	}
	if !found {
		t.Error("childEnv(true) must set CADRE_INTERACTIVE=1")
	}
	for _, kv := range childEnv(false) {
		if strings.HasPrefix(kv, "CADRE_INTERACTIVE=") {
			t.Errorf("childEnv(false) must not set CADRE_INTERACTIVE, got %q", kv)
		}
	}
}

func TestRun_InteractiveFlagOnlyHonoredWhenLeading(t *testing.T) {
	// A leading --interactive is consumed; anywhere else it is the
	// subcommand name and must not be silently swallowed.
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][2]string{{"widget", "described only"}})

	var leading strings.Builder
	Run(context.Background(), []string{InteractiveFlag, "does-not-exist"}, Deps{
		Stdout: io.Discard, Stderr: &leading, RepoRoot: dir, SubcommandsPath: subPath,
	})
	if !strings.Contains(leading.String(), "unknown subcommand 'does-not-exist'") {
		t.Errorf("a leading %s must be consumed; stderr = %q", InteractiveFlag, leading.String())
	}

	var trailing strings.Builder
	Run(context.Background(), []string{"does-not-exist", InteractiveFlag}, Deps{
		Stdout: io.Discard, Stderr: &trailing, RepoRoot: dir, SubcommandsPath: subPath,
	})
	if !strings.Contains(trailing.String(), "unknown subcommand 'does-not-exist'") {
		t.Errorf("a non-leading %s must not be consumed; stderr = %q", InteractiveFlag, trailing.String())
	}
}

func TestRun_SubcommandExitCodePropagates(t *testing.T) {
	// Was measured through the Python fallback's return value. Now measured
	// through a Go-routed subcommand, which is the only kind there is: a
	// usage error must surface as a non-zero exit rather than being
	// swallowed by the dispatcher.
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][2]string{{"widget", "described only"}})

	code := Run(context.Background(), []string{"config", "--no-such-flag"}, Deps{
		Stdout: io.Discard, Stderr: io.Discard, RepoRoot: dir, SubcommandsPath: subPath,
	})
	if code == 0 {
		t.Error("a subcommand's non-zero exit must propagate through Run()")
	}
}

// TestRun_SubcommandExecutionErrorReturnsOne is gone with the mechanism it
// covered: an error from the Python-script fallback. There is no such
// fallback and no PythonExecutable hook to inject a failure through --
// TestRun_RefusesATableEntryWithNoGoRoute covers what reaching the end of
// dispatch means now.

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
		{Name: "select", Description: "Deterministic selection"},
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
	subPath := writeSubcommandsTSV(t, dir, [][2]string{})

	var stderr bytes.Buffer
	deps := Deps{
		Stdout:          io.Discard,
		Stderr:          &stderr,
		RepoRoot:        dir,
		SubcommandsPath: subPath,
	}

	// generate-role-metadata should be routed to the Go CLI, reaching its own
	// flag parsing rather than any other dispatch path.
	//
	// This asserted exit 2, described as "flag parsing error for --help".
	// That was the defect, not the contract: an explicit --help is a
	// satisfied request and exits 0. Routing is what this test is for, and
	// it still shows it -- an unrouted command would not print this
	// command's usage at all.
	code := Run(context.Background(), []string{"generate-role-metadata", "--help"}, deps)

	if code != 0 {
		t.Errorf("Run() for generate-role-metadata --help code = %d, want 0", code)
	}
}

func TestRun_GeneratePluginRouting(t *testing.T) {
	// Test that generate-plugin is properly routed to the Go CLI
	dir := t.TempDir()
	subPath := writeSubcommandsTSV(t, dir, [][2]string{})

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
	subPath := writeSubcommandsTSV(t, dir, [][2]string{})

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
