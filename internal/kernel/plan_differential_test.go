package kernel

import (
	"bytes"
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
