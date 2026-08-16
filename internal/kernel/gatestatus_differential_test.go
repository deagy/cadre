package kernel

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `publish-gate-status`, compared with the Python kernel.
//
// The rendered body is the interesting artifact: it is what a human reads on a
// pull request, and every token in it is either fixed template text or a
// closed-enum value from the run record. So the comparison is on the whole
// body, byte for byte, and the timestamp is the only thing normalised -- and
// only where a run is not being checked for post-write verification, which is
// the one behaviour that depends on the timestamp being exact.
//
// Both sides are run against a frozen clock for the apply cases. Without that,
// the body a run posts and the body it verifies against differ by the
// microseconds between them, and the verification refuses every time -- which
// is correct behaviour and useless as a test of the success path.

func runPythonGateStatus(t *testing.T, args []string) (int, string) {
	t.Helper()
	script := `
import sys
import agentic_sdlc
agentic_sdlc.now = lambda: sys.argv[1]
sys.exit(agentic_sdlc.main(sys.argv[2:]))
`
	invocation := append([]string{"-c", script, frozenMoment}, args...)
	command := exec.Command("python3", invocation...)
	command.Dir = filepath.Join(repositoryRoot(t), "kernel")
	output, _ := command.CombinedOutput()
	return command.ProcessState.ExitCode(), string(output)
}

func TestTheGateStatusBodyMatchesThePythonKernel(t *testing.T) {
	for _, probe := range []struct {
		name    string
		prepare func(t *testing.T, root string)
	}{
		{name: "a freshly planned task"},
		{
			name: "a task with an approved gate",
			prepare: func(t *testing.T, root string) {
				makeGateApprovable(t, root)
			},
		},
		{
			// The three status cells that are not simply the gate's status:
			// not-applicable wins over everything, a pending re-entry says so,
			// and a human-only gate is marked so a reader does not wait for
			// automation that will never move it.
			name: "gates in every special state",
			prepare: func(t *testing.T, root string) {
				mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
					func(document map[string]any) {
						gates := listOf(document["lifecycle_gates"])
						first, _ := gates[0].(map[string]any)
						first["applicability"] = "not-applicable"
						second, _ := gates[1].(map[string]any)
						second["required_reentry_gate"] = "G1"
						second["status"] = "invalidated"
					})
			},
		},
		{
			name: "a task with re-entry history",
			prepare: func(t *testing.T, root string) {
				mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
					func(document map[string]any) {
						document["re_entry_history"] = []any{
							map[string]any{
								"invalidated_at": "2026-08-14T09:00:00+00:00",
								"actor":          "github.com/somebody", "reason": "a private reason",
								"earliest_gate": "G4",
							},
							map[string]any{
								"invalidated_at": "2026-08-15T09:00:00+00:00",
								"actor":          "github.com/somebody-else", "reason": "another",
								"earliest_gate": "G2",
							},
						}
					})
			},
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			freezeClock(t)
			root, manifest := statusProject(t)
			if probe.prepare != nil {
				probe.prepare(t, root)
			}
			writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list":     map[string]any{},
				"create":   map[string]any{}, "fetch": map[string]any{},
			})

			args := []string{"--provider", manifest, "publish-gate-status",
				"--root", root, "--task-id", decideTask, "--forge", "github",
				"--repo", "acme/app", "--pr", "3", "--as-bot", "sdlc-bot",
				"--allow-classification", "internal"}

			pythonCode, pythonOutput := runPythonGateStatus(t, args)
			var stdout, stderr bytes.Buffer
			goCode := Run(args, &stdout, &stderr)

			if pythonCode != 0 || goCode != 0 {
				t.Fatalf("expected success -- python %d, go %d\npython: %s\ngo: %s",
					pythonCode, goCode, pythonOutput, stdout.String()+stderr.String())
			}
			if pythonOutput != stdout.String() {
				t.Errorf("summary differs.\npython:\n%s\ngo:\n%s", pythonOutput, stdout.String())
			}
		})
	}
}

// gateStatusApplyCases exercise the classify-then-write path.
var gateStatusApplyCases = []struct {
	name string
	// mock is built from the body a dry run produced, so the fetch echo can
	// match it exactly -- which is what the post-write verification compares.
	mock  func(body string) map[string]any
	apply bool
	// unacknowledged drops --i-know-this-is-mocked, which every other apply
	// case passes.
	unacknowledged bool
	expectExit     int
	// expectAction, when set, must appear in the summary.
	expectAction string
}{
	{
		name: "creating the comment",
		mock: func(body string) map[string]any {
			return map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list":     map[string]any{"acme/app#3": map[string]any{"1": []any{}}},
				"create":   map[string]any{"acme/app#3": map[string]any{"id": 42}},
				"fetch": map[string]any{"42": map[string]any{
					"id": 42, "body": body, "user": map[string]any{"login": "sdlc-bot"}}},
			}
		},
		apply: true, expectAction: `"action": "create"`,
	},
	{
		// The same comment already there, saying the same thing. Nothing is
		// written, and the ledger records that nothing was.
		name: "the comment is already current",
		mock: func(body string) map[string]any {
			return map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list": map[string]any{"acme/app#3": map[string]any{"1": []any{
					map[string]any{"id": 42, "body": body,
						"user": map[string]any{"login": "sdlc-bot"}}}}},
				"create": map[string]any{}, "fetch": map[string]any{},
			}
		},
		apply: true, expectAction: `"action": "unchanged"`,
	},
	{
		name: "the comment is stale",
		mock: func(body string) map[string]any {
			return map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list": map[string]any{"acme/app#3": map[string]any{"1": []any{
					map[string]any{
						"id": 42,
						// Same marker, different content -- so it matches and
						// must be updated rather than duplicated.
						"body": strings.Replace(body, "Current phase:", "Stale phase:", 1),
						"user": map[string]any{"login": "sdlc-bot"}}}}},
				"update": map[string]any{"42": map[string]any{}},
				"fetch": map[string]any{"42": map[string]any{
					"id": 42, "body": body, "user": map[string]any{"login": "sdlc-bot"}}},
			}
		},
		apply: true, expectAction: `"action": "update"`,
	},
	{
		// Somebody else's comment carrying our marker. Editing it would
		// overwrite their words under our banner.
		name: "a comment somebody else wrote",
		mock: func(body string) map[string]any {
			return map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list": map[string]any{"acme/app#3": map[string]any{"1": []any{
					map[string]any{"id": 42, "body": body,
						"user": map[string]any{"login": "somebody-else"}}}}},
				"create": map[string]any{}, "fetch": map[string]any{},
			}
		},
		apply: true, expectExit: 2,
	},
	{
		// Two comments with the marker: this cannot tell which is its own.
		name: "two matching comments",
		mock: func(body string) map[string]any {
			return map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list": map[string]any{"acme/app#3": map[string]any{"1": []any{
					map[string]any{"id": 42, "body": body,
						"user": map[string]any{"login": "sdlc-bot"}},
					map[string]any{"id": 43, "body": body,
						"user": map[string]any{"login": "sdlc-bot"}}}}},
				"create": map[string]any{}, "fetch": map[string]any{},
			}
		},
		apply: true, expectExit: 2,
	},
	{
		// The same ambiguity, reported rather than raised: computing a
		// diagnosis is safe, acting on it is not.
		name: "two matching comments, reported by a dry run",
		mock: func(body string) map[string]any {
			return map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list": map[string]any{"acme/app#3": map[string]any{"1": []any{
					map[string]any{"id": 42, "body": body,
						"user": map[string]any{"login": "sdlc-bot"}},
					map[string]any{"id": 43, "body": body,
						"user": map[string]any{"login": "sdlc-bot"}}}}},
				"create": map[string]any{}, "fetch": map[string]any{},
			}
		},
		expectAction: `"action": "blocked"`,
	},
	{
		// The real unchanged case: the comment was posted earlier, so it
		// carries an older timestamp, and nothing else about the task has
		// moved. Comparing the timestamp would rewrite a comment that already
		// says the right thing, on every single run.
		name: "the same comment posted at a different time",
		mock: func(body string) map[string]any {
			return map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list": map[string]any{"acme/app#3": map[string]any{"1": []any{
					map[string]any{"id": 42,
						"body": strings.Replace(body, frozenMoment,
							"2026-08-14T07:30:00.000000Z", 1),
						"user": map[string]any{"login": "sdlc-bot"}}}}},
				"create": map[string]any{}, "fetch": map[string]any{},
			}
		},
		apply: true, expectAction: `"action": "unchanged"`,
	},
	{
		// An apply against a mocked backend without saying so. Allowed only
		// when the operator acknowledges it, because otherwise they walk away
		// believing a publication happened.
		name: "a mocked apply with no acknowledgement",
		mock: func(body string) map[string]any {
			return map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list":     map[string]any{"acme/app#3": map[string]any{"1": []any{}}},
				"create":   map[string]any{"acme/app#3": map[string]any{"id": 42}},
				"fetch": map[string]any{"42": map[string]any{
					"id": 42, "body": body, "user": map[string]any{"login": "sdlc-bot"}}},
			}
		},
		apply: true, unacknowledged: true, expectExit: 1,
	},
	{
		// The forge echoed back something other than what was posted.
		name: "a write that does not verify",
		mock: func(body string) map[string]any {
			return map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list":     map[string]any{"acme/app#3": map[string]any{"1": []any{}}},
				"create":   map[string]any{"acme/app#3": map[string]any{"id": 42}},
				"fetch": map[string]any{"42": map[string]any{
					"id": 42, "body": "something else entirely",
					"user": map[string]any{"login": "sdlc-bot"}}},
			}
		},
		apply: true, expectExit: 2,
	},
}

func TestTheGateStatusApplyPathMatchesThePythonKernel(t *testing.T) {
	for _, probe := range gateStatusApplyCases {
		t.Run(probe.name, func(t *testing.T) {
			freezeClock(t)
			root, manifest := statusProject(t)

			// One dry run first, to learn the exact body both sides will
			// render -- the mock's echo has to match it for the post-write
			// verification to have anything to agree with.
			writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list":     map[string]any{}, "create": map[string]any{}, "fetch": map[string]any{},
			})
			base := []string{"--provider", manifest, "publish-gate-status",
				"--root", root, "--task-id", decideTask, "--forge", "github",
				"--repo", "acme/app", "--pr", "3", "--as-bot", "sdlc-bot",
				"--allow-classification", "internal"}
			var probeOut, probeErr bytes.Buffer
			if code := Run(base, &probeOut, &probeErr); code != 0 {
				t.Fatalf("the dry run that establishes the body failed: %s", probeErr.String())
			}
			var summary struct {
				Body string `json:"body"`
			}
			if err := json.Unmarshal(probeOut.Bytes(), &summary); err != nil {
				t.Fatal(err)
			}

			writeForgeMock(t, GitHubStatusMockEnv, probe.mock(summary.Body))
			args := base
			if probe.apply {
				args = append(append([]string{}, base...), "--apply")
				if !probe.unacknowledged {
					args = append(args, "--i-know-this-is-mocked")
				}
			}

			// Separate project copies, because an apply writes a ledger and
			// the two runs must not see each other's.
			pythonRoot := filepath.Join(t.TempDir(), "python")
			goRoot := filepath.Join(t.TempDir(), "go")
			for _, target := range []string{pythonRoot, goRoot} {
				if err := copyTree(root, target); err != nil {
					t.Fatal(err)
				}
			}
			pythonArgs := replaceRoot(args, root, pythonRoot)
			goArgs := replaceRoot(args, root, goRoot)

			pythonCode, pythonOutput := runPythonGateStatus(t, pythonArgs)
			var stdout, stderr bytes.Buffer
			goCode := Run(goArgs, &stdout, &stderr)
			goOutput := stdout.String() + stderr.String()

			if pythonCode != probe.expectExit || goCode != probe.expectExit {
				t.Fatalf("expected exit %d -- python %d, go %d\npython: %s\ngo: %s",
					probe.expectExit, pythonCode, goCode, pythonOutput, goOutput)
			}
			if pythonOutput != goOutput {
				t.Errorf("output differs.\npython:\n%s\ngo:\n%s", pythonOutput, goOutput)
			}
			if probe.expectAction != "" && !strings.Contains(goOutput, probe.expectAction) {
				t.Errorf("no %s in the summary; the case checks something else:\n%s",
					probe.expectAction, goOutput)
			}

			// And the ledgers agree, which is the part an operator reads
			// afterwards to find out what happened.
			for _, forge := range []string{"github"} {
				name := filepath.Join(Overlay, "runs", decideTask, "gate-status-"+forge+".json")
				pythonLedger, pythonExists := readIfPresent(filepath.Join(pythonRoot, name))
				goLedger, goExists := readIfPresent(filepath.Join(goRoot, name))
				if pythonExists != goExists {
					t.Errorf("%s: python wrote it=%v, go wrote it=%v", name, pythonExists, goExists)
					continue
				}
				if pythonExists && pythonLedger != goLedger {
					t.Errorf("%s differs.\npython:\n%s\ngo:\n%s", name, pythonLedger, goLedger)
				}
			}
		})
	}
}

func replaceRoot(args []string, from, to string) []string {
	replaced := make([]string, len(args))
	for index, value := range args {
		replaced[index] = strings.ReplaceAll(value, from, to)
	}
	return replaced
}
