package kernel

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

const frozenMoment = "2026-08-15T09:00:00.000000Z"

// freezeClock pins the Go kernel's clock for one test.
func freezeClock(t *testing.T) {
	t.Helper()
	moment, err := time.Parse("2006-01-02T15:04:05.000000Z", frozenMoment)
	if err != nil {
		t.Fatal(err)
	}
	previous := timeNow
	timeNow = func() time.Time { return moment }
	t.Cleanup(func() { timeNow = previous })
}

// runPythonGateStatus runs the Python kernel with its clock pinned to the same
// moment, so both sides render an identical body.
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

// statusProject is a project with one planned task to report on.
func statusProject(t *testing.T) (root, manifest string) {
	t.Helper()
	return decidableProject(t)
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

// TestOnlyGitLabNotesCarryASystemFlag records an asymmetry that is easy to get
// wrong in either direction.
//
// GitLab's notes endpoint returns both real comments and the forge's own
// activity notes, and one of those can quote a body containing our marker --
// matching it would make this try to edit a note it cannot edit. GitHub's
// issue-comment endpoint returns only real comments, so there is no flag to
// read and none is invented.
func TestOnlyGitLabNotesCarryASystemFlag(t *testing.T) {
	note := normaliseGitLabNote(map[string]any{
		"id": 1, "body": "quoted", "system": true,
		"author": map[string]any{"username": "sdlc-bot"},
	})
	if !note.IsSystem {
		t.Error("a GitLab system note was not marked as one")
	}
	ordinary := normaliseGitLabNote(map[string]any{
		"id": 2, "body": "real", "author": map[string]any{"username": "sdlc-bot"},
	})
	if ordinary.IsSystem {
		t.Error("an ordinary GitLab note was marked as a system note")
	}

	// GitHub has no such field, and a payload carrying one must not acquire
	// the meaning by accident.
	comment := normaliseGitHubComment(map[string]any{
		"id": 3, "body": "real", "system": true,
		"user": map[string]any{"login": "sdlc-bot"},
	})
	if comment.IsSystem {
		t.Error("a GitHub comment picked up a system flag GitHub does not send")
	}
	if comment.Author != "sdlc-bot" || comment.Body != "real" {
		t.Errorf("the comment was not normalised: %+v", comment)
	}
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

func readIfPresent(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// The invariants, stated without reference to the Python kernel.

func TestTheStatusCommentSaysItIsNotApproval(t *testing.T) {
	// The advisory paragraph exists to be read by somebody about to make a
	// mistake, so its presence is a property of the artifact rather than a
	// stylistic choice. A render that dropped it would look fine.
	freezeClock(t)
	root, manifest := statusProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	projection, err := registry.GateStatusProjection(root, decideTask)
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	body := RenderGateStatusBody(decideTask, projection, contracts, frozenMoment)

	for _, required := range []string{
		"not an approval and is never read back",
		"does not approve any lifecycle gate",
		"Not approval evidence",
		"Reacting or replying to this comment does not approve anything",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("the rendered comment does not say %q:\n%s", required, body)
		}
	}
}

func TestTheStatusCommentRendersNoFreeText(t *testing.T) {
	// The property that makes the absent sanitizer correct rather than an
	// omission. Nothing project-supplied reaches this body -- so a re-entry's
	// actor and reason, which are a real identity and free text, must not
	// appear even though they sit right beside the fields that do.
	freezeClock(t)
	root, manifest := statusProject(t)
	mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
		func(document map[string]any) {
			document["re_entry_history"] = []any{map[string]any{
				"invalidated_at": "2026-08-14T09:00:00+00:00",
				"actor":          "github.com/a-real-person",
				"reason":         "a reason nobody outside this project should read",
				"earliest_gate":  "G4",
			}}
		})
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	projection, err := registry.GateStatusProjection(root, decideTask)
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	body := RenderGateStatusBody(decideTask, projection, contracts, frozenMoment)

	for _, leaked := range []string{"a-real-person", "nobody outside this project"} {
		if strings.Contains(body, leaked) {
			t.Errorf("the comment leaked %q:\n%s", leaked, body)
		}
	}
	// The count and the earliest gate do appear -- otherwise this test would
	// pass on a render that omitted the section entirely.
	if !strings.Contains(body, "Re-entries recorded: 1") {
		t.Errorf("the re-entry count is missing:\n%s", body)
	}
	if !strings.Contains(body, "earliest re-entered gate: G4") {
		t.Errorf("the earliest re-entered gate is missing:\n%s", body)
	}
}

func TestOnlyTheTimestampIsIgnoredWhenComparingBodies(t *testing.T) {
	// The timestamp changes on every invocation by design, so comparing it
	// would make every run rewrite a comment that already says the right
	// thing. Everything else must still count as a change.
	body := "line one\nCurrent phase: intent · rendered 2026-08-15T09:00:00Z\nline three\n"
	later := "line one\nCurrent phase: intent · rendered 2027-01-01T00:00:00Z\nline three\n"
	if canonicaliseForComparison(body) != canonicaliseForComparison(later) {
		t.Error("two bodies differing only in timestamp compared as different")
	}

	changed := "line one\nCurrent phase: build · rendered 2026-08-15T09:00:00Z\nline three\n"
	if canonicaliseForComparison(body) == canonicaliseForComparison(changed) {
		t.Error("a changed phase compared as unchanged")
	}
	elsewhere := "line one\nCurrent phase: intent · rendered 2026-08-15T09:00:00Z\nline four\n"
	if canonicaliseForComparison(body) == canonicaliseForComparison(elsewhere) {
		t.Error("a change after the timestamp line compared as unchanged")
	}
}

func TestTheStatusMarkerIsDomainSeparated(t *testing.T) {
	// The marker identifies this comment among everything else this kernel
	// puts on a forge. Sharing a formula with the issue markers would let one
	// task's gate issue and its status comment collide.
	marker := ComputeStatusMarker("TASK-1")
	if marker == TaskHash("TASK-1") {
		t.Error("the matching marker and the displayed hash are the same value")
	}
	if len(marker) != 16 {
		t.Errorf("the marker is %d characters", len(marker))
	}
	if marker != hexSHA256([]byte("gate-status\x00TASK-1"))[:16] {
		t.Error("the marker formula changed; every existing comment stops matching")
	}

	// Any template version matches, so a future v2 finds and updates a v1
	// comment rather than posting a second one beside it.
	pattern := markerPattern(marker)
	for _, version := range []string{"v1", "v2", "v37"} {
		if !pattern.MatchString("<!-- agentic-sdlc:gate-status:" + version + ":" + marker + " -->") {
			t.Errorf("a %s comment did not match", version)
		}
	}
	if pattern.MatchString("<!-- agentic-sdlc:gate-status:v1:" + TaskHash("TASK-2") + " -->") {
		t.Error("another task's marker matched")
	}
}
