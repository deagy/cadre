package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Telemetry is local-file-only, and its summary is a report on a file.
//
// Two properties, neither of which was asserted on the Go side.
//
// The first is the one that matters: `cadre select` writes a record of what it
// selected -- task text included, when asked twice -- and that record never
// leaves the machine. selection_telemetry.py had two tests for this, a source
// grep and a check on the module's namespace, because "opt-in local telemetry"
// and "telemetry" differ by exactly this property and nothing in the name says
// which one it is.
//
// The second is ordinary: summarize reads a JSON-lines file. A missing file,
// an empty one, and a malformed line each have a defined answer, and running
// the CLI is how you find out what it is.

// telemetrySourceFiles are the files that implement writing and summarizing a
// telemetry record -- the code paths a record actually passes through.
func telemetrySourceFiles(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	sources := map[string]string{}
	// All three: the writer, the summarizer, and the command that drives it.
	// The first version of this listed only two and missed
	// internal/orchestration/telemetry.go -- which is where Summarize actually
	// lives, and therefore the file that reads every record back.
	for _, relative := range []string{
		filepath.Join("internal", "selector", "telemetry.go"),
		filepath.Join("internal", "orchestration", "telemetry.go"),
		filepath.Join("internal", "cli", "selection_telemetry.go"),
	} {
		content, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatalf("reading %s: %v", relative, err)
		}
		sources[relative] = string(content)
	}
	return sources
}

func TestTelemetryCodeCannotReachTheNetwork(t *testing.T) {
	// A source scan, matching what the Python did. It is not airtight -- a
	// transitive import could carry a socket in without naming one here -- but
	// it catches the way this would actually change: somebody adds an upload,
	// and the import appears in the file that writes the record.
	//
	// Package-qualified so a role named "network-management-automation-
	// implementer" appearing in a fixture is not a finding.
	forbidden := regexp.MustCompile(
		`"net"|"net/http"|"net/url"|"os/exec"|http\.(Get|Post|Client|NewRequest)|net\.Dial`)

	sources := telemetrySourceFiles(t)
	if len(sources) == 0 {
		t.Fatal("no telemetry sources were read; this test would prove nothing")
	}
	for path, content := range sources {
		for number, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if forbidden.MatchString(line) {
				t.Errorf("%s:%d reaches off the machine:\n  %s\n"+
					"Selection telemetry is a local JSON-lines file. A record of what "+
					"a task selected -- with the task text in it, when --telemetry-task "+
					"is passed -- must not be sent anywhere.", path, number+1, trimmed)
			}
		}
	}
}

func TestTheNetworkScanWouldNoticeAnUpload(t *testing.T) {
	// Guards the guard. The scan above passes over a tree that contains no
	// match, which is also what it does if the pattern is wrong.
	forbidden := regexp.MustCompile(
		`"net"|"net/http"|"net/url"|"os/exec"|http\.(Get|Post|Client|NewRequest)|net\.Dial`)

	for _, upload := range []string{
		`	"net/http"`,
		`	resp, err := http.Post(endpoint, "application/json", body)`,
		`	conn, err := net.Dial("tcp", host)`,
		`	client := &http.Client{Timeout: time.Second}`,
		`	"os/exec"`,
	} {
		if !forbidden.MatchString(upload) {
			t.Errorf("the scan would not notice: %s", upload)
		}
	}
	for _, permitted := range []string{
		`		"network-management-automation-implementer",`,
		`	// the record never goes over the network`,
		`	if strings.Contains(text, "http") {`,
		`	record["route_frequency"] = counts`,
	} {
		if forbidden.MatchString(permitted) {
			t.Errorf("the scan would wrongly reject: %s", permitted)
		}
	}
}

// runSummarize invokes the command the way a user does and captures stdout,
// because the report's shape is the deliverable and only the CLI produces it.
func runSummarize(t *testing.T, path string) (string, int) {
	t.Helper()
	realStdout := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	code := SelectionTelemetryCmd([]string{"--summarize", path})
	write.Close()
	os.Stdout = realStdout

	var captured strings.Builder
	buffer := make([]byte, 4096)
	for {
		n, err := read.Read(buffer)
		captured.Write(buffer[:n])
		if err != nil {
			break
		}
	}
	return captured.String(), code
}

func TestSummarizingAnEmptyFileReportsZeroRatherThanFailing(t *testing.T) {
	// An empty file is what a fresh opt-in looks like before the first
	// selection. Erroring there would read as a broken install.
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	output, code := runSummarize(t, path)
	if code != 0 {
		t.Fatalf("exit %d on an empty file:\n%s", code, output)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, output)
	}
	if total, _ := report["total_records"].(float64); total != 0 {
		t.Errorf("total_records = %v, want 0", report["total_records"])
	}
}

func TestSummarizingAMissingFileFailsAndNamesIt(t *testing.T) {
	// Distinct from the empty case on purpose. "No telemetry yet" and "the
	// path you gave me is wrong" are different problems, and a zero report for
	// the second one hides a typo behind a plausible answer.
	path := filepath.Join(t.TempDir(), "not-here.jsonl")
	output, code := runSummarize(t, path)
	if code == 0 {
		t.Fatalf("a missing file summarized successfully:\n%s", output)
	}
}

func TestAMalformedLineIsRefusedRatherThanSkipped(t *testing.T) {
	// Skipping it would under-report every count in the file while still
	// printing a confident-looking report.
	path := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(path, []byte(
		`{"status":"ok"}`+"\nnot json\n"+`{"status":"ok"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, code := runSummarize(t, path)
	if code == 0 {
		t.Fatalf("a malformed line was tolerated:\n%s", output)
	}
}

func TestTheSummaryAggregatesAcrossRecords(t *testing.T) {
	// The baseline the three refusals above are refusals *from*. Without it,
	// a summarize that failed on everything would satisfy all of them.
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join([]string{
		`{"status":"ok","workflow":"feature-development","matched_routes":["backend","frontend"]}`,
		`{"status":"needs-triage","workflow":"triage","matched_routes":[]}`,
		`{"status":"ok","workflow":"feature-development","matched_routes":["backend"]}`,
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	output, code := runSummarize(t, path)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, output)
	}
	var report struct {
		TotalRecords    int            `json:"total_records"`
		StatusCounts    map[string]int `json:"status_counts"`
		WorkflowCounts  map[string]int `json:"workflow_counts"`
		RouteFrequency  map[string]int `json:"route_frequency"`
		NeedsTriageRate float64        `json:"needs_triage_rate"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, output)
	}
	if report.TotalRecords != 3 {
		t.Errorf("total_records = %d, want 3", report.TotalRecords)
	}
	if report.StatusCounts["ok"] != 2 || report.StatusCounts["needs-triage"] != 1 {
		t.Errorf("status_counts = %v", report.StatusCounts)
	}
	if report.WorkflowCounts["feature-development"] != 2 {
		t.Errorf("workflow_counts = %v", report.WorkflowCounts)
	}
	// Routes are counted across records, not per record -- "backend" appears
	// in two of them.
	if report.RouteFrequency["backend"] != 2 || report.RouteFrequency["frontend"] != 1 {
		t.Errorf("route_frequency = %v", report.RouteFrequency)
	}
	if report.NeedsTriageRate < 0.33 || report.NeedsTriageRate > 0.34 {
		t.Errorf("needs_triage_rate = %v, want ~1/3", report.NeedsTriageRate)
	}
}

func TestAnAbsentWorkflowIsCountedUnderTheEmptyKey(t *testing.T) {
	// Pinning a difference from the Python rather than leaving it to be
	// discovered. selection_telemetry.py counted a null workflow under a
	// distinct None key, which json.dumps rendered as "null"; this counts it
	// under "". A record with `"workflow": ""` therefore lands in the same
	// bucket as one with `"workflow": null`.
	//
	// Written down because it is a real if small fidelity loss, and because a
	// future change in either direction should be a decision rather than a
	// surprise.
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(
		`{"status":"ok","workflow":null}`+"\n"+`{"status":"ok","workflow":""}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, code := runSummarize(t, path)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, output)
	}
	var report struct {
		WorkflowCounts map[string]int `json:"workflow_counts"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	if report.WorkflowCounts[""] != 2 {
		t.Errorf("workflow_counts = %v; a null and an empty workflow are both "+
			"counted under \"\" -- if that changed, this is the test to update",
			report.WorkflowCounts)
	}
}
