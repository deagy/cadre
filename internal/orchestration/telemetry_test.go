package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func samplePlan() *DispatchPlan {
	return &DispatchPlan{
		TaskID:         "TASK-1",
		Task:           "sensitive task description",
		ChangedFiles:   []string{"secret/path.go"},
		Classification: "internal",
		Workflow:       "standard",
		MatchedRoutes:  []string{"backend", "debugging"},
		Agents: AgentGroups{
			Primary:   []string{"backend-engineer"},
			Reviewers: []string{"code-reviewer", "test-engineer"},
		},
		Teams: []Team{{Name: "core-team"}},
		QualityGates: []Gate{
			{ID: "code-review", Required: true},
			{ID: "optional-gate", Required: false},
		},
		HumanGates: []Gate{{ID: "human-signoff"}},
		DispatchDisposition: Disposition{
			Status: "staffed",
		},
	}
}

func TestBuildSelectionTelemetryRecordExcludesTaskByDefault(t *testing.T) {
	record := BuildSelectionTelemetryRecord(samplePlan(), false)

	if record.Task != "" || record.ChangedFiles != nil {
		t.Fatal("expected Task/ChangedFiles to be omitted without includeTask")
	}
	if record.TaskID != "TASK-1" {
		t.Errorf("TaskID = %q, want TASK-1", record.TaskID)
	}
	if record.Status != "staffed" {
		t.Errorf("Status = %q, want staffed", record.Status)
	}
	if record.AgentCounts.Primary != 1 || record.AgentCounts.Reviewers != 2 {
		t.Errorf("AgentCounts = %+v", record.AgentCounts)
	}
	if record.RequiredQualityGateCount != 1 {
		t.Errorf("RequiredQualityGateCount = %d, want 1", record.RequiredQualityGateCount)
	}
	if record.HumanGateCount != 1 {
		t.Errorf("HumanGateCount = %d, want 1", record.HumanGateCount)
	}
	if len(record.Teams) != 1 || record.Teams[0] != "core-team" {
		t.Errorf("Teams = %v", record.Teams)
	}
}

func TestBuildSelectionTelemetryRecordIncludeTask(t *testing.T) {
	record := BuildSelectionTelemetryRecord(samplePlan(), true)
	if record.Task != "sensitive task description" {
		t.Error("expected Task set when includeTask is true")
	}
	if len(record.ChangedFiles) != 1 || record.ChangedFiles[0] != "secret/path.go" {
		t.Error("expected ChangedFiles set when includeTask is true")
	}
}

func TestRecordSelectionAndSummarize(t *testing.T) {
	root := t.TempDir()

	path1, err := RecordSelection(samplePlan(), root, "", false)
	if err != nil {
		t.Fatalf("RecordSelection: %v", err)
	}

	plan2 := samplePlan()
	plan2.DispatchDisposition.Status = "needs-triage"
	plan2.MatchedRoutes = []string{"backend"}
	path2, err := RecordSelection(plan2, root, "", false)
	if err != nil {
		t.Fatalf("RecordSelection (2nd): %v", err)
	}
	if path1 != path2 {
		t.Fatalf("expected both records to go to the same default path, got %q and %q", path1, path2)
	}

	// Never leaks raw task text into the file without includeTask.
	data, err := os.ReadFile(path1)
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(data), "sensitive task description") {
		t.Fatal("telemetry file leaked raw task text without --record-telemetry-include-task")
	}

	summary, err := Summarize(path1)
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

func TestRecordSelectionWithIncludeTask(t *testing.T) {
	root := t.TempDir()
	path, err := RecordSelection(samplePlan(), root, "", true)
	if err != nil {
		t.Fatalf("RecordSelection: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(data), "sensitive task description") {
		t.Fatal("expected raw task text present when includeTask is true")
	}
}

func TestRecordSelectionExplicitPath(t *testing.T) {
	root := t.TempDir()
	explicit := filepath.Join(root, "custom", "telemetry.jsonl")

	path, err := RecordSelection(samplePlan(), root, explicit, false)
	if err != nil {
		t.Fatalf("RecordSelection: %v", err)
	}
	if path != explicit {
		t.Errorf("path = %q, want %q", path, explicit)
	}
	if _, err := os.Stat(explicit); err != nil {
		t.Fatalf("expected file to exist at %q: %v", explicit, err)
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

func TestRecordIsValidJSONLine(t *testing.T) {
	root := t.TempDir()
	path, err := RecordSelection(samplePlan(), root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &record); err != nil { // Trim trailing newline.
		t.Fatalf("record is not valid JSON: %v", err)
	}
	if record["schema_version"].(float64) != float64(SelectionTelemetrySchemaVersion) {
		t.Errorf("schema_version = %v", record["schema_version"])
	}
}
