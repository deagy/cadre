package kernel

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// `publish-reviewer-nudge`, compared with the Python kernel.
//
// The rendered comment is the artifact, and what it does *not* contain matters
// as much as what it does: no `@`-mention, no name for anybody withheld by an
// independence conflict, and none of the run record's free text. Each of those
// is a data-exposure or notification property rather than a formatting one, so
// each has its own test rather than being covered incidentally by the byte
// comparison.

func TestTheReviewerNudgeBodyMatchesThePythonKernel(t *testing.T) {
	for _, probe := range []struct {
		name    string
		prepare func(t *testing.T, root string, mock map[string]any)
		apply   bool
		// unacknowledged drops --i-know-this-is-mocked.
		unacknowledged bool
		expectExit     int
		expectContains string
	}{
		{
			name:           "reviewers worth suggesting",
			expectContains: "Suggested reviewers:",
		},
		{
			// The PR author holds authority, so a login is withheld -- counted
			// in the comment, never named.
			name: "a withheld reviewer is counted, not named",
			prepare: func(t *testing.T, root string, mock map[string]any) {
				pullRequest, _ := mock["pr"].(map[string]any)
				pullRequest["user"] = map[string]any{"login": "engineering-lead"}
			},
			expectContains: "not shown due to a gate-independence conflict",
		},
		{
			// Everybody has already reviewed the current head, so there is
			// nothing to nudge about and the comment says so plainly.
			name: "nobody to nudge",
			prepare: func(t *testing.T, root string, mock map[string]any) {
				mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
					func(document map[string]any) {
						// Narrow the run to a single gate, so one review
						// covers everything.
						gates := listOf(document["lifecycle_gates"])
						for index, raw := range gates {
							gate, _ := raw.(map[string]any)
							if index > 0 {
								gate["applicability"] = "not-applicable"
							}
						}
					})
			},
			expectContains: "",
		},
		{
			name:  "posting the comment",
			apply: true, expectContains: `"action": "create"`,
		},
		{
			name:  "a mocked apply with no acknowledgement",
			apply: true, unacknowledged: true, expectExit: 1,
		},
	} {
		t.Run(probe.name, func(t *testing.T) {
			freezeClock(t)
			root, manifest := reviewerFixture(t)
			mock := gitHubReviewerMock()
			if probe.prepare != nil {
				probe.prepare(t, root, mock)
			}
			writeForgeMock(t, GitHubReadMockEnv, mock)
			writeForgeMock(t, "AGENTIC_SDLC_TEST_GITHUB_REVIEWS_FILE", gitHubReviewsMock())
			writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
				"identity": map[string]any{"login": "sdlc-bot"},
				"list":     map[string]any{}, "create": map[string]any{}, "fetch": map[string]any{},
			})

			base := []string{"--provider", manifest, "publish-reviewer-nudge",
				"--root", root, "--task-id", decideTask, "--repo", "acme/app",
				"--pr", "3", "--as-bot", "sdlc-bot", "--allow-classification", "internal"}

			// A dry run first, to learn the body the apply will verify
			// against -- the fetch echo has to match it exactly.
			if probe.apply {
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
				writeForgeMock(t, GitHubStatusMockEnv, map[string]any{
					"identity": map[string]any{"login": "sdlc-bot"},
					"list":     map[string]any{"acme/app#3": map[string]any{"1": []any{}}},
					"create":   map[string]any{"acme/app#3": map[string]any{"id": 77}},
					"fetch": map[string]any{"77": map[string]any{
						"id": 77, "body": summary.Body,
						"user": map[string]any{"login": "sdlc-bot"}}},
				})
			}

			args := base
			if probe.apply {
				args = append(append([]string{}, base...), "--apply")
				if !probe.unacknowledged {
					args = append(args, "--i-know-this-is-mocked")
				}
			}

			pythonRoot := filepath.Join(t.TempDir(), "python")
			goRoot := filepath.Join(t.TempDir(), "go")
			for _, target := range []string{pythonRoot, goRoot} {
				if err := copyTree(root, target); err != nil {
					t.Fatal(err)
				}
			}

			pythonCode, pythonOutput := runPythonGateStatus(t, replaceRoot(args, root, pythonRoot))
			var stdout, stderr bytes.Buffer
			goCode := Run(replaceRoot(args, root, goRoot), &stdout, &stderr)
			goOutput := stdout.String() + stderr.String()

			if pythonCode != probe.expectExit || goCode != probe.expectExit {
				t.Fatalf("expected exit %d -- python %d, go %d\npython: %s\ngo: %s",
					probe.expectExit, pythonCode, goCode, pythonOutput, goOutput)
			}
			if pythonOutput != goOutput {
				t.Errorf("output differs.\npython:\n%s\ngo:\n%s", pythonOutput, goOutput)
			}
			if probe.expectContains != "" && !strings.Contains(goOutput, probe.expectContains) {
				t.Errorf("the comment does not contain %q:\n%s", probe.expectContains, goOutput)
			}

			// And the ledger, which is what an operator reads afterwards.
			name := filepath.Join(Overlay, "runs", decideTask, "reviewer-nudge-github.json")
			pythonLedger, pythonExists := readIfPresent(filepath.Join(pythonRoot, name))
			goLedger, goExists := readIfPresent(filepath.Join(goRoot, name))
			if pythonExists != goExists {
				t.Errorf("%s: python wrote it=%v, go wrote it=%v", name, pythonExists, goExists)
			} else if pythonExists && pythonLedger != goLedger {
				t.Errorf("%s differs.\npython:\n%s\ngo:\n%s", name, pythonLedger, goLedger)
			}
		})
	}
}

// The invariants, stated without reference to the Python kernel.
