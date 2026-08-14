package orchestration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsEnabled(t *testing.T) {
	if IsEnabled(false) {
		t.Fatal("expected telemetry disabled by default (no flag, no env)")
	}
	if !IsEnabled(true) {
		t.Fatal("expected telemetry enabled when the CLI flag is set")
	}

	t.Setenv(EnvSelectionTelemetryEnable, "1")
	if !IsEnabled(false) {
		t.Fatal("expected telemetry enabled via env var")
	}
	t.Setenv(EnvSelectionTelemetryEnable, "0")
	if IsEnabled(false) {
		t.Fatal("expected telemetry disabled for a falsy env value")
	}
}

func TestIncludeTaskEnabled(t *testing.T) {
	if IncludeTaskEnabled(false) {
		t.Fatal("expected include-task disabled by default")
	}
	if !IncludeTaskEnabled(true) {
		t.Fatal("expected include-task enabled when the CLI flag is set")
	}
	t.Setenv(EnvSelectionTelemetryIncludeTask, "yes")
	if !IncludeTaskEnabled(false) {
		t.Fatal("expected include-task enabled via env var")
	}
}

func TestResolveTelemetryPath(t *testing.T) {
	root := "/repo"

	if got := ResolveTelemetryPath(root, ""); got != filepath.Join(root, ".agents", "orchestration", "selection-telemetry.jsonl") {
		t.Errorf("default path = %q", got)
	}

	if got := ResolveTelemetryPath(root, "/explicit/path.jsonl"); got != "/explicit/path.jsonl" {
		t.Errorf("override path = %q, want /explicit/path.jsonl", got)
	}

	t.Setenv(EnvSelectionTelemetryPath, "/env/path.jsonl")
	if got := ResolveTelemetryPath(root, ""); got != "/env/path.jsonl" {
		t.Errorf("env path = %q, want /env/path.jsonl", got)
	}
	// Explicit override still wins over env.
	if got := ResolveTelemetryPath(root, "/explicit/path.jsonl"); got != "/explicit/path.jsonl" {
		t.Errorf("override should beat env var, got %q", got)
	}
}

func TestSummarizeMissingFile(t *testing.T) {
	_, err := Summarize(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err == nil {
		t.Fatal("expected an error for a missing telemetry file")
	}
}

func TestSummarizeMalformedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.jsonl")
	if err := os.WriteFile(path, []byte("{not valid json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Summarize(path)
	if err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

// Summarize's happy path, written directly as JSONL rather than through the
// removed RecordSelection(). `cadre select` is the real producer of this file
// (it is still Python and writes the records itself), so reading a file this
// package did not write is the more faithful shape for this test anyway.
func TestSummarizeAggregatesRecords(t *testing.T) {
	root := t.TempDir()
	path := ResolveTelemetryPath(root, "")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"status":"staffed","matched_routes":["backend","debugging"]}`,
		`{"status":"needs-triage","matched_routes":["backend"]}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := Summarize(path)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary.TotalRecords != 2 {
		t.Errorf("TotalRecords = %d, want 2", summary.TotalRecords)
	}
	if summary.StatusCounts["staffed"] != 1 || summary.StatusCounts["needs-triage"] != 1 {
		t.Errorf("StatusCounts = %v", summary.StatusCounts)
	}
	if summary.NeedsTriageRate == nil || *summary.NeedsTriageRate != 0.5 {
		t.Errorf("NeedsTriageRate = %v, want 0.5", summary.NeedsTriageRate)
	}
	if summary.RouteFrequency["backend"] != 2 {
		t.Errorf("RouteFrequency[backend] = %d, want 2", summary.RouteFrequency["backend"])
	}
	if summary.RouteFrequency["debugging"] != 1 {
		t.Errorf("RouteFrequency[debugging] = %d, want 1", summary.RouteFrequency["debugging"])
	}
}
