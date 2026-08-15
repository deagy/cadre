package kernel

import (
	"bytes"
	"encoding/json"
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

func TestAMissingLedgerIsAnEmptyLedgerNotAnError(t *testing.T) {
	// Stated directly rather than by comparison, because it is the one
	// behaviour a caller depends on and the one most likely to be "fixed"
	// into an error by somebody reading the code later.
	root, _ := plannedProject(t)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"list-gate-issues", "--root", root, "--task-id", fixtureTask},
		&stdout, &stderr); code != 0 {
		t.Fatalf("a task with no ledger exited %d: %s", code, stderr.String())
	}
	var ledger map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &ledger); err != nil {
		t.Fatalf("the output was not JSON: %v", err)
	}
	if ledger["task_id"] != fixtureTask {
		t.Errorf("the empty ledger does not name its task: %v", ledger)
	}
	entries, ok := ledger["entries"].(map[string]any)
	if !ok || len(entries) != 0 {
		t.Errorf("entries should be an empty object, got %#v", ledger["entries"])
	}
}

func TestATaskIDCannotEscapeTheProject(t *testing.T) {
	// A task id is a directory name. Without this, `--task-id ../../..` reads
	// a file outside the project entirely -- and these readers print whatever
	// they read.
	root, _ := plannedProject(t)
	for _, hostile := range []string{
		"../../etc", "..", ".", "a/b", "with space", "-leading-dash", "",
		"trailing/", "nul\x00byte",
	} {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"list-gate-issues", "--root", root, "--task-id", hostile},
			&stdout, &stderr)
		if code == 0 {
			t.Errorf("--task-id %q was accepted, printing: %s", hostile, stdout.String())
		}
	}

	// And an ordinary id still works, or the check above would pass by
	// refusing everything.
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"list-gate-issues", "--root", root, "--task-id", fixtureTask},
		&stdout, &stderr); code != 0 {
		t.Errorf("an ordinary task id was refused: %s", stderr.String())
	}
}

func TestRenderIndentedMatchesPythonsJSONDumps(t *testing.T) {
	// The renderer on its own, over the shapes that differ between the two
	// languages' encoders. Each expectation below was taken from
	// `json.dumps(value, indent=2)`.
	for _, probe := range []struct {
		name     string
		document string
		expected string
	}{
		{"key order is the document's", `{"zebra": 1, "alpha": 2}`,
			"{\n  \"zebra\": 1,\n  \"alpha\": 2\n}\n"},
		{"empty containers collapse", `{"a": {}, "b": []}`,
			"{\n  \"a\": {},\n  \"b\": []\n}\n"},
		{"non-ASCII is escaped", `{"s": "café"}`,
			"{\n  \"s\": \"caf\\u00e9\"\n}\n"},
		{"astral runes become surrogate pairs", `{"s": "🚀"}`,
			"{\n  \"s\": \"\\ud83d\\ude80\"\n}\n"},
		{"HTML punctuation is not escaped", `{"s": "a<b>c&d"}`,
			"{\n  \"s\": \"a<b>c&d\"\n}\n"},
		{"integers stay integral", `{"n": 1, "big": 10000000000000000000}`,
			"{\n  \"n\": 1,\n  \"big\": 10000000000000000000\n}\n"},
		{"nesting indents by two", `{"a": [{"b": 1}]}`,
			"{\n  \"a\": [\n    {\n      \"b\": 1\n    }\n  ]\n}\n"},
		{"null and booleans", `{"a": null, "b": true, "c": false}`,
			"{\n  \"a\": null,\n  \"b\": true,\n  \"c\": false\n}\n"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			value, err := DecodeOrdered([]byte(probe.document))
			if err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if rendered := RenderIndented(value); rendered != probe.expected {
				t.Errorf("rendered %q, want %q", rendered, probe.expected)
			}
		})
	}
}
