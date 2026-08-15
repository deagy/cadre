package kernel

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `plan`, compared with the Python kernel on two copies of the same project.
//
// Byte comparison, of three things: the plan printed to stdout, the
// dispatch-plan.json written to disk, and the run-record.json written beside
// it. Only the two wall-clock timestamps are blanked, and they are the only
// two fields either kernel is allowed to vary -- which is exactly why they are
// the fields excluded from the dispatch fingerprint. Everything else,
// including the fingerprint itself, must match to the byte.
//
// The task texts below are chosen to reach each branch of the workflow
// decision and each shape of the output rather than to look realistic: an
// incident, a production deployment, a task nothing matches, one that trips
// four human-only mutation gates at once, and one carrying non-ASCII and HTML
// punctuation, because those are what the two languages' JSON encoders
// disagree about.

var planProbes = []struct {
	name     string
	task     string
	expected string // a substring the plan must contain, so the case cannot pass vacuously
	// prepare optionally changes the project before planning. Applied to both
	// copies, so the two kernels still see identical input -- it exists because
	// a freshly initialised project leaves several of plan's branches
	// unreachable: it configures no change intake and ignores no gates.
	prepare func(t *testing.T, root string)
}{
	{"an ordinary change", "add an endpoint to the billing service", `"workflow": "new-service"`, nil},
	{"a production deployment", "deploy to production the payments gateway",
		`"workflow": "production-release"`, nil},
	{"an incident", "major incident: service outage in eu-west",
		`"workflow": "support-escalation"`, nil},
	{"nothing matches", "wibble frobnicate quux", `"status": "needs-triage"`, nil},
	{"four mutation gates at once",
		"delete data and destroy infrastructure during a production migration with root access to accept risk",
		`"id": "risk-acceptance"`, nil},
	// The expectation is the *escaped* form on purpose: Python writes
	// \u00e9 and leaves <, > and & alone, and Go's encoder does the exact
	// opposite of both. This is the case that pins the encoder.
	{"non-ASCII and HTML punctuation", `a <script> & "quoted" café ☕ task`,
		`a <script> & \"quoted\" caf\u00e9 \u2615 task`, nil},

	// One identity cannot author and review the same work. This project's
	// routing names backend-engineer as the backend route's author and as a
	// reviewer on the database route, so a task matching both is the case
	// where the plan has to drop it from one side.
	{"an agent matched as both author and reviewer",
		"backend work on postgresql ha", `"reviewers"`, nil},

	// Change intake, which a fresh project configures empty. The keyword is
	// matched as a whole word: "release" must not fire on "released", and the
	// task text below says "released" precisely so a substring match would
	// show up as a divergence.
	{"change intake matches whole words only", "the api was released last week",
		`"support"`, func(t *testing.T, root string) {
			mutateJSON(t, filepath.Join(root, Overlay, "routing.json"),
				func(document map[string]any) {
					document["change_intake"] = map[string]any{
						"keywords":      []any{"release", "change"},
						"agents":        []any{"release-engineer"},
						"quality_gates": []any{"G8"},
					}
				})
		}},
	{"change intake fires on the whole word", "prepare the release for the api",
		`release-engineer`, func(t *testing.T, root string) {
			mutateJSON(t, filepath.Join(root, Overlay, "routing.json"),
				func(document map[string]any) {
					document["change_intake"] = map[string]any{
						"keywords":      []any{"release", "change"},
						"agents":        []any{"release-engineer"},
						"quality_gates": []any{"G8"},
					}
				})
		}},

	// An ignored gate, which a fresh project also has none of. Ignoring a gate
	// mid-sequence is the interesting shape: it stays in the dispatch sequence
	// marked "ignored" rather than disappearing from it.
	{"a gate the project ignores", "backend api work",
		`"status": "ignored"`, func(t *testing.T, root string) {
			mutateJSON(t, filepath.Join(root, Overlay, "routing.json"),
				func(document map[string]any) {
					document["ignored_gates"] = []any{"G4"}
				})
		}},
}

func TestPlanProducesIdenticalDocuments(t *testing.T) {
	for _, probe := range planProbes {
		t.Run(probe.name, func(t *testing.T) {
			pythonRoot, manifest := initialisedProject(t)
			if probe.prepare != nil {
				probe.prepare(t, pythonRoot)
			}
			goRoot := t.TempDir()
			if err := copyTree(pythonRoot, goRoot); err != nil {
				t.Fatal(err)
			}

			repoRoot := repositoryRoot(t)
			pythonCode, pythonPlan := runPythonKernel(repoRoot, "--provider", manifest,
				"plan", "--root", pythonRoot, "--task-id", "TASK-1", "--task", probe.task)

			var stdout, stderr bytes.Buffer
			goCode := Run([]string{"--provider", manifest, "plan", "--root", goRoot,
				"--task-id", "TASK-1", "--task", probe.task}, &stdout, &stderr)

			if pythonCode != goCode {
				t.Fatalf("exit codes differ -- python %d, go %d\npython: %s\ngo: %s",
					pythonCode, goCode, truncate(pythonPlan), stderr.String())
			}
			if !strings.Contains(pythonPlan, probe.expected) {
				t.Fatalf("the Python kernel's plan does not contain %q, so this case checks "+
					"something other than what it names:\n%s", probe.expected, truncate(pythonPlan))
			}

			comparePlanBytes(t, "printed plan", pythonPlan, stdout.String())
			for _, document := range []string{"dispatch-plan.json", "run-record.json"} {
				comparePlanBytes(t, document,
					readTaskDocument(t, pythonRoot, document),
					readTaskDocument(t, goRoot, document))
			}
		})
	}
}

// timestampField blanks the two wall-clock fields, and nothing else.
var timestampField = regexp.MustCompile(
	`"(generated_at|recorded_at)": "[^"]*"`)

func comparePlanBytes(t *testing.T, label, python, golang string) {
	t.Helper()
	if timestampField.ReplaceAllString(python, `"$1": "<when>"`) !=
		timestampField.ReplaceAllString(golang, `"$1": "<when>"`) {
		t.Errorf("%s differs.\npython:\n%s\ngo:\n%s", label, python, golang)
	}
	// Self-vacuity: two empty documents would compare equal.
	if !strings.Contains(python, `"task_id"`) {
		t.Errorf("%s does not look like a plan document; the comparison proves nothing", label)
	}
}

func readTaskDocument(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, Overlay, "runs", "TASK-1", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(data)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// initialisedProject builds a bare project overlay -- no planned task -- and
// returns a fresh copy of it.
func initialisedProject(t *testing.T) (root, manifest string) {
	t.Helper()
	template, manifest := plannedProjectTemplate(t)
	root = t.TempDir()
	if err := copyTree(template, root); err != nil {
		t.Fatal(err)
	}
	// The shared template already has a planned TASK-2; these cases plan
	// TASK-1 beside it, which also means every case runs against a project
	// that already contains a run rather than an empty one.
	return root, manifest
}

func TestPlanRefusesToRedefineAnExistingTask(t *testing.T) {
	// The refusal that protects a record already accumulating approvals. A
	// re-plan with different text is not an update -- it is a different task
	// wearing an ID somebody has already approved gates against.
	root, manifest := initialisedProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}

	first := PlanRequest{Root: root, TaskID: "TASK-9", Task: "add an endpoint to the billing service"}
	if _, err := registry.Plan(first); err != nil {
		t.Fatalf("the first plan failed: %v", err)
	}
	// The same plan again is a no-op, not a conflict.
	if _, err := registry.Plan(first); err != nil {
		t.Errorf("re-planning an identical task was refused: %v", err)
	}

	changed := first
	changed.Task = "something else entirely"
	err := registry.Plan2Error(changed)
	if err == nil {
		t.Fatal("re-planning the same ID with different text was accepted")
	}
	if !strings.Contains(err.Error(), "use a new task ID") {
		t.Errorf("the refusal does not say what to do instead: %v", err)
	}

	// And the existing record is untouched: a refused plan must not have
	// half-written anything.
	record := readTaskDocumentNamed(t, root, "TASK-9", "run-record.json")
	if !strings.Contains(record, "add an endpoint to the billing service") {
		t.Error("the refused re-plan modified the existing run record")
	}
}

// Plan2Error is a readability shim for the tests: Plan returns a result the
// error cases never use.
func (r *Registry) Plan2Error(request PlanRequest) error {
	_, err := r.Plan(request)
	return err
}

func readTaskDocumentNamed(t *testing.T, root, taskID, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, Overlay, "runs", taskID, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(data)
}

func TestPlanNeverMarksAGateApproved(t *testing.T) {
	// The invariant `plan` exists under. It may move one gate to `ready` --
	// "the prerequisites are met, somebody may now look at this" -- and that
	// is the furthest it is allowed to go. A planner that could approve would
	// make every gate downstream of it decorative.
	root, manifest := initialisedProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Plan(PlanRequest{
		Root: root, TaskID: "TASK-8", Task: "add an endpoint to the billing service",
	}); err != nil {
		t.Fatal(err)
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(readTaskDocumentNamed(t, root, "TASK-8", "run-record.json")),
		&record); err != nil {
		t.Fatal(err)
	}
	gates, _ := record["lifecycle_gates"].([]any)
	if len(gates) != len(GateIDs) {
		t.Fatalf("expected %d gates, got %d", len(GateIDs), len(gates))
	}
	ready := 0
	for _, raw := range gates {
		gate, _ := raw.(map[string]any)
		switch gate["status"] {
		case "approved":
			t.Errorf("%v was approved by planning", gate["gate_id"])
		case "ready":
			ready++
		}
	}
	// At most one, and for this project exactly one -- G1 has no
	// prerequisites and its authority is assigned in the fixture.
	if ready != 1 {
		t.Errorf("expected exactly one gate to be ready after planning, got %d", ready)
	}
}

func TestPlanRefusesATaskIDThatEscapesTheProject(t *testing.T) {
	root, manifest := initialisedProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	for _, hostile := range []string{"../escape", "..", ".", "a/b", "", "with space"} {
		if err := registry.Plan2Error(PlanRequest{
			Root: root, TaskID: hostile, Task: "add an endpoint",
		}); err == nil {
			t.Errorf("task ID %q was accepted", hostile)
		}
	}
}
