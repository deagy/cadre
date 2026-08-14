// telemetry.go ports roster/orchestration/src/selection_telemetry.py:
// opt-in, local-only telemetry for `cadre select` outcomes.
//
// This has two independent jobs:
//
//  1. Recording (RecordSelection) -- called from the `select` CLI entry
//     point, and only when the caller has explicitly opted in. It appends
//     one JSON-lines record per `cadre select` invocation describing the
//     *outcome* of selection (matched routes, status, workflow, team names,
//     agent counts) to a local file. This is a side effect only; it never
//     changes `cadre select`'s stdout/--output JSON.
//  2. Summarizing (Summarize) -- reads an accumulated JSON-lines file back
//     and reports aggregate stats (route-firing frequency, needs-triage
//     rate, workflow/team frequency).
//
// Hard design constraints, carried over unchanged from the Python original:
//
//   - OFF by default. Recording only happens when the caller passes
//     --record-telemetry to `cadre select` or sets CADRE_SELECTION_TELEMETRY=1
//     in the environment. With neither present, IsEnabled returns false,
//     RecordSelection is never called by the CLI layer, and zero bytes are
//     written anywhere.
//   - Local file only, never a network call. This file must never import
//     net/http or any other networking package.
//   - Records deliberately exclude the raw task text and raw changed-file
//     paths by default. What gets recorded is *structural* facts about the
//     outcome only. A maintainer who wants raw task capture for local
//     debugging can opt in *additionally* via --record-telemetry-include-task
//     (or CADRE_SELECTION_TELEMETRY_INCLUDE_TASK=1) -- off even when
//     ordinary telemetry recording is on.
//
// Deviation from the Python original: the Go DispatchPlan does not (yet)
// track matched risk-rule ids, a source_filter input, or a distinct
// lifecycle_tracking status the way the Python plan schema does, so those
// three telemetry fields are omitted here rather than fabricated. Every
// other field is a direct port.
package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SelectionTelemetrySchemaVersion is bumped whenever the record shape
// changes. Records are local, append-only, and never validated on read, so
// nothing rejects an old-version row -- the version exists so a log holding
// multiple encodings stays self-describing to whoever reads it later.
const SelectionTelemetrySchemaVersion = 2

// Environment variables controlling telemetry opt-in, mirroring the Python
// module's ENV_* constants exactly (so a project's existing env-based
// opt-in keeps working after this port).
const (
	EnvSelectionTelemetryEnable      = "CADRE_SELECTION_TELEMETRY"
	EnvSelectionTelemetryIncludeTask = "CADRE_SELECTION_TELEMETRY_INCLUDE_TASK"
	EnvSelectionTelemetryPath        = "CADRE_SELECTION_TELEMETRY_PATH"
)

// DefaultSelectionTelemetryRelativePath is where telemetry records are
// appended under a repository root, absent an override.
var DefaultSelectionTelemetryRelativePath = filepath.Join(".agents", "orchestration", "selection-telemetry.jsonl")

var trueEnvValues = map[string]bool{"1": true, "true": true, "yes": true, "on": true}

func envFlag(name string) bool {
	return trueEnvValues[strings.ToLower(strings.TrimSpace(os.Getenv(name)))]
}

// IsEnabled reports whether telemetry recording is enabled: the CLI flag or
// the environment variable, explicit opt-in only.
func IsEnabled(cliFlag bool) bool {
	return cliFlag || envFlag(EnvSelectionTelemetryEnable)
}

// IncludeTaskEnabled reports whether raw task-text capture is enabled -- a
// second, separate opt-in on top of IsEnabled.
func IncludeTaskEnabled(cliFlag bool) bool {
	return cliFlag || envFlag(EnvSelectionTelemetryIncludeTask)
}

// ResolveTelemetryPath resolves where telemetry records are appended.
// Priority: an explicit override argument, else
// CADRE_SELECTION_TELEMETRY_PATH, else the project-local default under
// repositoryRoot.
func ResolveTelemetryPath(repositoryRoot, override string) string {
	if override != "" {
		return expandHome(override)
	}
	if envPath := os.Getenv(EnvSelectionTelemetryPath); envPath != "" {
		return expandHome(envPath)
	}
	return filepath.Join(repositoryRoot, DefaultSelectionTelemetryRelativePath)
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

// SelectionTelemetryRecord is one JSON-lines entry describing a completed
// `cadre select` outcome.
type SelectionTelemetryRecord struct {
	SchemaVersion            int         `json:"schema_version"`
	RecordedAt               string      `json:"recorded_at"`
	TaskID                   string      `json:"task_id"`
	Status                   string      `json:"status"`
	Workflow                 string      `json:"workflow"`
	MatchedRoutes            []string    `json:"matched_routes"`
	Classification           string      `json:"classification"`
	AgentCounts              AgentCounts `json:"agent_counts"`
	Teams                    []string    `json:"teams"`
	RequiredQualityGateCount int         `json:"required_quality_gate_count"`
	HumanGateCount           int         `json:"human_gate_count"`
	Task                     string      `json:"task,omitempty"`
	ChangedFiles             []string    `json:"changed_files,omitempty"`
}

// AgentCounts is the per-role agent count in a telemetry record.
type AgentCounts struct {
	Primary   int `json:"primary"`
	Reviewers int `json:"reviewers"`
	Support   int `json:"support"`
}

// BuildSelectionTelemetryRecord derives a telemetry record from a completed
// dispatch plan. Deliberately omits Task/ChangedFiles unless includeTask is
// explicitly set.
func BuildSelectionTelemetryRecord(plan *DispatchPlan, includeTask bool) SelectionTelemetryRecord {
	requiredQualityGates := 0
	for _, gate := range plan.QualityGates {
		if gate.Required {
			requiredQualityGates++
		}
	}

	teamNames := make([]string, 0, len(plan.Teams))
	for _, team := range plan.Teams {
		if team.Name != "" {
			teamNames = append(teamNames, team.Name)
		}
	}

	record := SelectionTelemetryRecord{
		SchemaVersion:  SelectionTelemetrySchemaVersion,
		RecordedAt:     time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		TaskID:         plan.TaskID,
		Status:         plan.DispatchDisposition.Status,
		Workflow:       plan.Workflow,
		MatchedRoutes:  append([]string{}, plan.MatchedRoutes...),
		Classification: plan.Classification,
		AgentCounts: AgentCounts{
			Primary:   len(plan.Agents.Primary),
			Reviewers: len(plan.Agents.Reviewers),
			Support:   len(plan.Agents.Support),
		},
		Teams:                    teamNames,
		RequiredQualityGateCount: requiredQualityGates,
		HumanGateCount:           len(plan.HumanGates),
	}
	if includeTask {
		record.Task = plan.Task
		record.ChangedFiles = append([]string{}, plan.ChangedFiles...)
	}
	return record
}

// RecordSelection appends exactly one JSON-lines record for plan to the
// resolved telemetry file. Callers must gate this behind IsEnabled
// themselves -- this function always writes when called, by design, so that
// "off by default" is enforced at the one CLI call site rather than
// duplicated here. Returns the path written to.
func RecordSelection(plan *DispatchPlan, repositoryRoot, telemetryPathOverride string, includeTask bool) (string, error) {
	path := ResolveTelemetryPath(repositoryRoot, telemetryPathOverride)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}

	record := BuildSelectionTelemetryRecord(plan, includeTask)
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	data = append(data, '\n')

	// A single Write call, not two: under concurrent invocations (e.g. a
	// busy CI environment), two separate writes have no atomicity guarantee
	// against each other even though a single write under O_APPEND does
	// (POSIX, for sizes under PIPE_BUF).
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return path, nil
}

// SelectionTelemetrySummary is the aggregate report produced by Summarize.
type SelectionTelemetrySummary struct {
	TotalRecords    int            `json:"total_records"`
	StatusCounts    map[string]int `json:"status_counts"`
	NeedsTriageRate *float64       `json:"needs_triage_rate"`
	WorkflowCounts  map[string]int `json:"workflow_counts"`
	RouteFrequency  map[string]int `json:"route_frequency"`
	TeamFrequency   map[string]int `json:"team_frequency"`
}

// Summarize aggregates a JSON-lines telemetry file into maintainer-facing
// stats.
func Summarize(path string) (*SelectionTelemetrySummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("telemetry file does not exist: %s", path)
		}
		return nil, err
	}

	statusCounts := map[string]int{}
	workflowCounts := map[string]int{}
	routeCounts := map[string]int{}
	teamCounts := map[string]int{}
	total := 0

	for lineNumber, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("line %d: malformed JSON (%w)", lineNumber+1, err)
		}
		total++

		statusCounts[stringField(record, "status")]++
		workflowCounts[stringField(record, "workflow")]++
		for _, routeID := range stringSliceField(record, "matched_routes") {
			routeCounts[routeID]++
		}
		for _, teamID := range stringSliceField(record, "teams") {
			teamCounts[teamID]++
		}
	}

	summary := &SelectionTelemetrySummary{
		TotalRecords:   total,
		StatusCounts:   statusCounts,
		WorkflowCounts: workflowCounts,
		RouteFrequency: routeCounts,
		TeamFrequency:  teamCounts,
	}
	if total > 0 {
		rate := float64(statusCounts["needs-triage"]) / float64(total)
		summary.NeedsTriageRate = &rate
	}
	return summary, nil
}

func stringField(record map[string]any, key string) string {
	if v, ok := record[key].(string); ok {
		return v
	}
	return ""
}

func stringSliceField(record map[string]any, key string) []string {
	raw, ok := record[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
