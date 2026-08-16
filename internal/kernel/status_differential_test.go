package kernel

import (
	"bytes"
	"path/filepath"
	"testing"
)

// `status`, compared with the Python kernel -- including the part of its
// behaviour that its name hides.
//
// It writes. Running it advances the next eligible gate to `ready` and
// persists the record, so the cases below compare the run record as well as
// the printed output: a port that only printed the advance would agree on
// stdout and silently stop moving gates for every project that relies on it.

func TestStatusAgreesWithThePythonKernel(t *testing.T) {
	for _, probe := range []struct {
		name    string
		prepare func(t *testing.T, root string)
	}{
		{"a task with an approved gate", nil},
		{"a task where nothing has been decided", func(t *testing.T, root string) {
			// Every gate back to pending, so the advance has somewhere to go.
			// Against the approved fixture alone, a port that never advanced
			// anything would still agree.
			mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
				func(document map[string]any) {
					for _, raw := range listOf(document["lifecycle_gates"]) {
						gate, _ := raw.(map[string]any)
						gate["status"] = "pending"
					}
				})
		}},
		{"a task whose gates are all invalidated", func(t *testing.T, root string) {
			mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
				func(document map[string]any) {
					for _, raw := range listOf(document["lifecycle_gates"]) {
						gate, _ := raw.(map[string]any)
						gate["status"] = "invalidated"
						gate["required_reentry_gate"] = "G1"
					}
				})
		}},
	} {
		t.Run(probe.name, func(t *testing.T) {
			pythonRoot, manifest := approvedProject(t)
			if probe.prepare != nil {
				probe.prepare(t, pythonRoot)
			}
			goRoot := t.TempDir()
			if err := copyTree(pythonRoot, goRoot); err != nil {
				t.Fatal(err)
			}

			pythonCode, pythonOutput := runPythonKernel(repositoryRoot(t), "--provider", manifest,
				"status", "--root", pythonRoot, "--task-id", decideTask)
			var stdout, stderr bytes.Buffer
			goCode := Run([]string{"--provider", manifest, "status", "--root", goRoot,
				"--task-id", decideTask}, &stdout, &stderr)

			if pythonCode != 0 || goCode != 0 {
				t.Fatalf("expected success -- python %d, go %d\npython: %s\ngo: %s",
					pythonCode, goCode, pythonOutput, stderr.String())
			}
			if pythonOutput != stdout.String() {
				t.Errorf("output differs.\npython:\n%s\ngo:\n%s", pythonOutput, stdout.String())
			}

			recordPath := filepath.Join(Overlay, "runs", decideTask, "run-record.json")
			if readFile(t, filepath.Join(pythonRoot, recordPath)) !=
				readFile(t, filepath.Join(goRoot, recordPath)) {
				t.Error("the run records differ after status")
			}
		})
	}
}
