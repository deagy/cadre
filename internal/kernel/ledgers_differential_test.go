package kernel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The four `list-*` sidecar readers, compared with the Python kernel byte for
// byte -- not as parsed values.
//
// Bytes are the standard here because these subcommands print a document
// somebody else's tooling reads. Both kernels re-serialize the file rather
// than copying it, so the output carries three things a value comparison
// would not check: the key order the file was written in, Python's \uXXXX
// escaping of non-ASCII, and the exact indentation. A Go-native encoder gets
// all three wrong at once -- sorted keys, raw UTF-8 -- and a caller diffing
// the two would see every line change.

// ledgerReaders name each subcommand, the file it reads, and a ledger to
// write for the populated case.
var ledgerReaders = []struct {
	subcommand string
	filename   string
	populated  string
}{
	{
		"list-gate-issues", "gate-issues-gitlab.json",
		`{"schema_version": 1, "task_id": "TASK-2", "project_path": "acme/app",
          "bot_username": "sdlc-bot", "mocked": false,
          "entries": {"G1": {"iid": 7, "title": "G1 Intent — café ☕",
                             "url": "https://gitlab.example/acme/app/-/issues/7"}}}`,
	},
	{
		"list-github-gate-issues", "gate-issues-github.json",
		`{"schema_version": 1, "task_id": "TASK-2", "repo": "acme/app",
          "bot_login": "sdlc-bot", "mocked": true,
          "entries": {"G5": {"number": 12, "title": "G5 Security <review> & sign-off"}}}`,
	},
	{
		"list-gate-status", "gate-status-github.json",
		`{"schema_version": 1, "task_id": "TASK-2", "forge": "github",
          "target": {"repo": "acme/app", "pr": 3}, "bot_username": "sdlc-bot",
          "mocked": false, "marker": "abc123",
          "entries": [{"comment_id": 99, "rendered_at": "2026-08-15T09:00:00+00:00"}]}`,
	},
	{
		"list-reviewer-nudge", "reviewer-nudge-github.json",
		`{"schema_version": 1, "task_id": "TASK-2", "forge": "github",
          "target": {"repo": "acme/app", "pr": 3}, "bot_username": "sdlc-bot",
          "mocked": false, "marker": "def456", "entries": []}`,
	},
}

func TestTheLedgerReadersPrintIdenticalBytes(t *testing.T) {
	for _, reader := range ledgerReaders {
		t.Run(reader.subcommand, func(t *testing.T) {
			// A task with no ledger yet. The answer is an empty ledger, not an
			// error, and getting that skeleton wrong is invisible until
			// somebody's tooling reads a field that is not there.
			root, _ := plannedProject(t)
			compareLedgerOutput(t, root, reader.subcommand)

			// And the same task once a ledger exists. The fixture is written
			// with keys deliberately out of alphabetical order and with
			// non-ASCII in a title, because those are the two things a
			// Go-native encoder silently changes.
			path := filepath.Join(root, Overlay, "runs", fixtureTask, reader.filename)
			if err := os.WriteFile(path, []byte(reader.populated), 0o644); err != nil {
				t.Fatal(err)
			}
			compareLedgerOutput(t, root, reader.subcommand)
		})
	}
}

func compareLedgerOutput(t *testing.T, root, subcommand string) {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	pythonCode, pythonOutput := runPythonKernel(repoRoot,
		subcommand, "--root", root, "--task-id", fixtureTask)

	var stdout, stderr bytes.Buffer
	goCode := Run([]string{subcommand, "--root", root, "--task-id", fixtureTask}, &stdout, &stderr)

	if pythonCode != goCode {
		t.Errorf("exit codes differ -- python %d, go %d (go stderr: %s)",
			pythonCode, goCode, stderr.String())
	}
	if stdout.String() != pythonOutput {
		t.Errorf("output differs.\npython:\n%s\ngo:\n%s", pythonOutput, stdout.String())
	}
	// Self-vacuity: two implementations that both printed nothing would agree.
	if strings.TrimSpace(pythonOutput) == "" {
		t.Error("the Python kernel printed nothing; this comparison would prove nothing")
	}
}
