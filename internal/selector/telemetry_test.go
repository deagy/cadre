package selector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTelemetryRecordOmitsRawContentUnlessAskedTwice(t *testing.T) {
	// The two-flag design exists for this property: recording that a
	// selection happened is a different decision from recording what it was
	// about. A record carries structural facts by default and nothing else.
	plan := map[string]any{
		"task_id": "T-1", "status": "ok", "workflow": "new-service",
		"inputs": map[string]any{
			"task":           "migrate the customer billing database",
			"changed_files":  []any{"db/customers.sql"},
			"classification": "confidential",
		},
		"agents": map[string]any{"primary": []any{"a", "b"}, "reviewers": []any{"c"}},
		// Reduced to bare ids, because reasons.paths[].file entries are
		// changed-file paths and would otherwise leak through a field that
		// looks like it only carries route metadata.
		"matched_routes": []any{map[string]any{
			"id":      "backend",
			"reasons": map[string]any{"paths": []any{map[string]any{"file": "db/customers.sql"}}},
		}},
	}

	base := BuildTelemetryRecord(plan, false, time.Now())
	encoded, err := TelemetryJSON(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"billing", "customers.sql"} {
		if strings.Contains(string(encoded), leaked) {
			t.Errorf("the base record leaked %q:\n%s", leaked, encoded)
		}
	}
	if _, present := base["task"]; present {
		t.Error("the base record must not carry the task")
	}
	routes, _ := base["matched_routes"].([]any)
	if len(routes) != 1 || routes[0] != "backend" {
		t.Errorf("matched_routes = %v, want bare ids", base["matched_routes"])
	}

	withTask := BuildTelemetryRecord(plan, true, time.Now())
	if withTask["task"] != "migrate the customer billing database" {
		t.Errorf("task = %v, want it recorded on the second opt-in", withTask["task"])
	}
}

func TestTelemetryJSONUsesPythonsDefaultSpacingAndRawUnicode(t *testing.T) {
	// json.dumps(sort_keys=True, ensure_ascii=False) with no separators
	// argument: sorted keys, spaced separators, non-ASCII left alone.
	//
	// Both differences from CanonicalJSON change no meaning, which is exactly
	// why they are easy to get wrong -- a telemetry file appended to by two
	// implementations would parse the same and diff on every line.
	got, err := TelemetryJSON(map[string]any{"zebra": 1, "alpha": "café"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"alpha": "café", "zebra": 1}`; string(got) != want {
		t.Errorf("TelemetryJSON = %s, want %s", got, want)
	}

	// The other encoder in this package escapes and is compact, on purpose.
	canonical, err := CanonicalJSON(map[string]any{"zebra": 1, "alpha": "café"})
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) == string(got) {
		t.Error("the fingerprint encoder and the telemetry encoder must differ")
	}
}

func TestTelemetryIsOffUntilExplicitlyEnabled(t *testing.T) {
	t.Setenv(telemetryEnableEnv, "")
	if TelemetryIsEnabled(false) {
		t.Error("telemetry must be off by default")
	}
	if !TelemetryIsEnabled(true) {
		t.Error("the flag must enable it")
	}
	for _, value := range []string{"1", "true", "YES", " on "} {
		t.Setenv(telemetryEnableEnv, value)
		if !TelemetryIsEnabled(false) {
			t.Errorf("env value %q must enable telemetry", value)
		}
	}
	for _, value := range []string{"0", "false", "no", "maybe", ""} {
		t.Setenv(telemetryEnableEnv, value)
		if TelemetryIsEnabled(false) {
			t.Errorf("env value %q must not enable telemetry", value)
		}
	}
}

func TestTelemetryPathPrefersTheExplicitOverride(t *testing.T) {
	t.Setenv(telemetryPathEnv, "/from/env.jsonl")
	if got := ResolveTelemetryPath("/repo", "/explicit.jsonl"); got != "/explicit.jsonl" {
		t.Errorf("path = %q, want the explicit override to win", got)
	}
	if got := ResolveTelemetryPath("/repo", ""); got != "/from/env.jsonl" {
		t.Errorf("path = %q, want the environment value", got)
	}
	t.Setenv(telemetryPathEnv, "")
	want := filepath.Join("/repo", ".agents", "orchestration", "selection-telemetry.jsonl")
	if got := ResolveTelemetryPath("/repo", ""); got != want {
		t.Errorf("path = %q, want the project-local default %q", got, want)
	}
}

func TestRecordSelectionAppendsOneLinePerCall(t *testing.T) {
	root := t.TempDir()
	// A nested path, so the parent directories are created rather than
	// assumed to exist.
	destination := filepath.Join(root, "nested", "deeper", "telemetry.jsonl")
	plan := map[string]any{"task_id": "T-1", "status": "ok"}

	for range 3 {
		if _, err := RecordSelection(plan, root, destination, false); err != nil {
			t.Fatal(err)
		}
	}

	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(contents), "\n"), "\n")
	if len(lines) != 3 {
		t.Errorf("wrote %d lines, want one per call:\n%s", len(lines), contents)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			t.Errorf("line is not one complete JSON object: %q", line)
		}
	}
}

func TestTelemetryRecordedAtIsAZuluTimestamp(t *testing.T) {
	// Python renders millisecond precision with a Z suffix; an offset form
	// would sort differently in a file two implementations both append to.
	record := BuildTelemetryRecord(map[string]any{}, false,
		time.Date(2026, 8, 14, 12, 34, 56, 789_000_000, time.UTC))
	if got := record["recorded_at"]; got != "2026-08-14T12:34:56.789Z" {
		t.Errorf("recorded_at = %v, want a millisecond Zulu timestamp", got)
	}
}
