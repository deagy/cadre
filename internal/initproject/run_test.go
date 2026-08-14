package initproject

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func newRunOpts(t *testing.T, targetRoot, sharedDir string) RunInitOptions {
	t.Helper()
	return RunInitOptions{
		TargetPath:        targetRoot,
		SharedDefaultsDir: sharedDir,
		AuditLogPath:      filepath.Join(t.TempDir(), "audit.jsonl"),
	}
}

func TestRunInitDefaultsModeNoOpWritesNothing(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	dir := makeGitProject(t)
	opts := newRunOpts(t, dir, sharedDir)
	opts.Force = true

	var stdout, stderr bytes.Buffer
	code := RunInit(opts, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".agents", "shared"))
	if len(entries) != 0 {
		t.Errorf("expected no overlay files written in defaults mode, got %v", entries)
	}
}

func TestRunInitSetWithForceWritesFile(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	dir := makeGitProject(t)
	opts := newRunOpts(t, dir, sharedDir)
	opts.Force = true
	opts.SetValues = []string{"platform.hosting_model=cloud"}

	var stdout, stderr bytes.Buffer
	code := RunInit(opts, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	written := filepath.Join(dir, ".agents", "shared", TeamProfileFilename)
	data, err := os.ReadFile(written)
	if err != nil {
		t.Fatalf("expected %s to be written: %v", written, err)
	}
	if !containsSub(string(data), "hosting_model: cloud") {
		t.Errorf("written content = %q", data)
	}
}

func TestRunInitDryRunDoesNotWrite(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	dir := makeGitProject(t)
	opts := newRunOpts(t, dir, sharedDir)
	opts.SetValues = []string{"platform.hosting_model=cloud"}
	// Force is false -- this is a dry run.

	var stdout, stderr bytes.Buffer
	code := RunInit(opts, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".agents", "shared", TeamProfileFilename)); err == nil {
		t.Error("expected no file written on a dry run")
	}
	if !containsSub(stdout.String(), "would write") && !containsSub(stdout.String(), "dry-run") {
		t.Errorf("expected dry-run preview output, got %q", stdout.String())
	}
}

func TestRunInitFailClosedAbortsAllWritesOnOneBadField(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	dir := makeGitProject(t)
	opts := newRunOpts(t, dir, sharedDir)
	opts.Force = true
	// One good --set (stack) and one autonomy loosening that must be
	// rejected -- A-005 requires NEITHER file gets written.
	opts.SetValues = []string{
		"platform.hosting_model=cloud",
		"autonomy:repository.commit=allowed",
	}

	var stdout, stderr bytes.Buffer
	code := RunInit(opts, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected a non-zero exit code on fail-closed abort, stdout=%s", stdout.String())
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".agents", "shared"))
	if len(entries) != 0 {
		t.Errorf("expected NO files written when any planned write fails validation (A-005), got %v", entries)
	}
}

func TestRunInitPrintAnswersRedactsAutonomyValue(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	dir := makeGitProject(t)
	opts := newRunOpts(t, dir, sharedDir)
	opts.PrintAnswers = true
	opts.SetValues = []string{"autonomy:repository.push=never"}

	var stdout, stderr bytes.Buffer
	code := RunInit(opts, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if containsSub(stdout.String(), "new_value: never") {
		t.Errorf("expected the autonomy value to be redacted from --print-answers output, got %q", stdout.String())
	}
}

func TestRunInitRepairModeReportsWithoutWriting(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	dir := makeGitProject(t)
	opts := newRunOpts(t, dir, sharedDir)
	opts.Repair = true

	var stdout, stderr bytes.Buffer
	code := RunInit(opts, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".agents", "shared"))
	if len(entries) != 0 {
		t.Errorf("expected --repair to never write files, got %v", entries)
	}
}

func TestRunInitRepairRejectsChangePlanningFlags(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	dir := makeGitProject(t)
	opts := newRunOpts(t, dir, sharedDir)
	opts.Repair = true
	opts.Force = true

	var stdout, stderr bytes.Buffer
	code := RunInit(opts, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage error) for --repair --force", code)
	}
}

func TestRunInitRefusesSelfCheckoutTarget(t *testing.T) {
	root, ok := suiteCheckoutRoot()
	if !ok {
		t.Skip("not running inside a git checkout")
	}
	sharedDir := realSharedDefaultsDirForTest(t)
	opts := newRunOpts(t, root, sharedDir)

	var stdout, stderr bytes.Buffer
	code := RunInit(opts, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected RunInit to refuse writing into this suite's own checkout")
	}
}

func TestRunInitInteractiveFailsClosed(t *testing.T) {
	sharedDir := realSharedDefaultsDirForTest(t)
	dir := makeGitProject(t)
	opts := newRunOpts(t, dir, sharedDir)
	opts.Interactive = true

	var stdout, stderr bytes.Buffer
	code := RunInit(opts, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !containsSub(stderr.String(), "--answers") {
		t.Errorf("expected the fail-closed message to point at --answers/--set, got %q", stderr.String())
	}
}
