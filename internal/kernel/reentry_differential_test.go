package kernel

import (
	"bytes"
	"path/filepath"
	"regexp"
	"testing"
)

// `invalidate`, `reenter` and `upgrade` -- the three commands that reach into
// a record, or a lock, after the fact.
//
// The fixture matters more here than usual: a project with nothing approved
// has nothing to withdraw, so every case below runs against a task whose G1 is
// genuinely approved, with bound artifacts, evidence, and a source link
// populating a top-level record field. Invalidating a run where nothing had
// been decided would agree trivially.
//
// Comparison is byte-for-byte on the run record and the version lock, with
// only `invalidated_at` blanked -- the one wall-clock field these commands
// write.

// invalidatedAtField blanks the only timestamp these commands produce.
var invalidatedAtField = regexp.MustCompile(`"invalidated_at": "[^"]*"`)

func blankInvalidationTime(text string) string {
	return invalidatedAtField.ReplaceAllString(text, `"invalidated_at": "<when>"`)
}

var reentryCases = []struct {
	name string
	args []string
}{
	// From G1, where the approval and the bound artifacts actually are: this
	// is the case that populates affected_artifact_bindings.
	{"invalidate everything from G1", []string{
		"invalidate", "--task-id", decideTask, "--earliest-gate", "G1",
		"--reason", "requirements changed", "--actor", decideActor}},
	{"invalidate from the middle", []string{
		"invalidate", "--task-id", decideTask, "--earliest-gate", "G4",
		"--reason", "governance rework", "--actor", "github.com/governance-lead"}},
	{"reenter from G1", []string{
		"reenter", "--task-id", decideTask, "--earliest-gate", "G1",
		"--reason", "restarting", "--actor", decideActor}},
	{"reenter from the middle", []string{
		"reenter", "--task-id", decideTask, "--earliest-gate", "G3",
		"--reason", "restarting", "--actor", "github.com/system-architect"}},
	{"upgrade reports without writing", []string{"upgrade", "--check"}},
	{"upgrade applies", []string{"upgrade", "--apply"}},
}

func TestRecordSurgeryAgreesWithThePythonKernel(t *testing.T) {
	for _, probe := range reentryCases {
		t.Run(probe.name, func(t *testing.T) {
			pythonRoot, manifest := approvedProject(t)
			goRoot := t.TempDir()
			if err := copyTree(pythonRoot, goRoot); err != nil {
				t.Fatal(err)
			}

			pythonCode, pythonOutput := runPythonKernel(repositoryRoot(t),
				append([]string{"--provider", manifest}, append(probe.args, "--root", pythonRoot)...)...)
			var stdout, stderr bytes.Buffer
			goCode := Run(append([]string{"--provider", manifest},
				append(probe.args, "--root", goRoot)...), &stdout, &stderr)

			if pythonCode != 0 || goCode != 0 {
				t.Fatalf("expected success -- python %d, go %d\npython: %s\ngo: %s",
					pythonCode, goCode, pythonOutput, stderr.String())
			}
			if blankInvalidationTime(pythonOutput) != blankInvalidationTime(stdout.String()) {
				t.Errorf("output differs.\npython:\n%s\ngo:\n%s", pythonOutput, stdout.String())
			}

			for _, document := range []string{
				filepath.Join("runs", decideTask, "run-record.json"),
				"version.lock",
			} {
				python := blankInvalidationTime(readFile(t, filepath.Join(pythonRoot, Overlay, document)))
				golang := blankInvalidationTime(readFile(t, filepath.Join(goRoot, Overlay, document)))
				if python != golang {
					t.Errorf("%s differs.\npython:\n%s\ngo:\n%s", document, python, golang)
				}
			}
		})
	}
}

// The invariants, stated without reference to the Python kernel.
