package kernel

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `create-github-gate-issues`, compared with the Python kernel.
//
// Built the same way as the GitLab command's comparison -- two worlds, one
// where nothing exists and one where everything does, with the report, the
// exit code and the ledger all compared byte-for-byte -- and extended to the
// six things GitHub does that GitLab does not:
//
//   - a pull request answering an issue-label search
//   - a search page that comes back exactly full
//   - a repository with issues turned off, or a public one
//   - a login that exists but cannot be assigned
//   - a write that reports success and does not take
//   - labels that collide case-insensitively
//
// Neither implementation sleeps here. Both space their mutative calls out by a
// second in production, and both expose that as a seam its own test suite
// already zeroes; the delay is timing, not behaviour, and thirteen seconds per
// case would make this suite something nobody runs.

func runPythonGithubIssues(t *testing.T, args []string) (int, string) {
	t.Helper()
	script := `
import sys
import agentic_sdlc
from agentic_sdlc import github_issue_write
agentic_sdlc.now = lambda: sys.argv[1]
github_issue_write.WRITE_DELAY_SECONDS = 0
sys.exit(agentic_sdlc.main(sys.argv[2:]))
`
	invocation := append([]string{"-c", script, frozenMoment}, args...)
	command := exec.Command("python3", invocation...)
	command.Dir = filepath.Join(repositoryRoot(t), "kernel")
	output, _ := command.CombinedOutput()
	return command.ProcessState.ExitCode(), string(output)
}

type githubIssuesCase struct {
	name           string
	alreadyCreated bool
	damage         func(t *testing.T, mocks *githubMockPair)
	extraArgs      []string
	wrongDigest    bool
	noDigest       bool
	expectExit     int
	expectContains string
}

var githubIssuesCases = []githubIssuesCase{
	{name: "creating every issue", expectContains: `"status": "created"`},
	{
		name: "reusing every issue", alreadyCreated: true,
		expectContains: `"status": "reused"`,
	},

	// The GitHub-only failure modes.

	{
		// GitHub's issue-list endpoint returns pull requests too. One of these
		// marker labels cannot legitimately be on a PR, so a match is
		// tampering rather than something to filter past.
		name: "a pull request wearing a marker label", alreadyCreated: true,
		damage: func(t *testing.T, mocks *githubMockPair) {
			search, _ := mocks.issue["search"].(map[string]any)
			key := lowestKey(search, nil)
			matches, _ := search[key].([]any)
			entry, _ := matches[0].(map[string]any)
			entry["pull_request"] = map[string]any{
				"url": "https://api.github.com/repos/acme/app/pulls/1"}
		},
		expectExit: 2, expectContains: "label-on-pull-request",
	},
	{
		// A full page means there may be more. This never paginates, so an
		// ambiguity this size is a human's to resolve.
		name: "a search page that comes back full", alreadyCreated: true,
		damage: func(t *testing.T, mocks *githubMockPair) {
			search, _ := mocks.issue["search"].(map[string]any)
			key := lowestKey(search, nil)
			matches, _ := search[key].([]any)
			first, _ := matches[0].(map[string]any)
			for len(matches) < issueSearchPageSize {
				matches = append(matches, map[string]any{
					"number": 900 + len(matches), "labels": first["labels"]})
			}
			search[key] = matches
		},
		expectExit: 2, expectContains: "result-cap-exceeded",
	},
	{
		// The pre-flight. GitHub has no per-issue confidential flag, so a
		// public repository is the whole of the exposure decision.
		name: "a public repository",
		damage: func(t *testing.T, mocks *githubMockPair) {
			repo, _ := mocks.issue["repo"].(map[string]any)
			repo["private"] = false
		},
		expectExit: 1, expectContains: "--allow-public-repo was not passed",
	},
	{
		name: "a public repository the operator allowed",
		damage: func(t *testing.T, mocks *githubMockPair) {
			repo, _ := mocks.issue["repo"].(map[string]any)
			repo["private"] = false
		},
		extraArgs:      []string{"--allow-public-repo"},
		expectContains: `"status": "created"`,
	},
	{
		name: "a repository with issues turned off",
		damage: func(t *testing.T, mocks *githubMockPair) {
			repo, _ := mocks.issue["repo"].(map[string]any)
			repo["has_issues"] = false
		},
		expectExit: 1, expectContains: "issues are disabled on repository",
	},
	{
		// A login nobody answers to: one authority's refusal, and the rest of
		// the task is still tracked.
		name: "an authority whose login is not a GitHub user",
		damage: func(t *testing.T, mocks *githubMockPair) {
			users, _ := mocks.read["users"].(map[string]any)
			users[lowestKey(users, nil)] = false
		},
		expectExit: 2, expectContains: "github-user-unresolved",
	},
	{
		// A real user who cannot be assigned. GitHub would accept the
		// assignment and not make it, so this is refused before creating.
		name: "an authority who is not a collaborator",
		damage: func(t *testing.T, mocks *githubMockPair) {
			collaborators, _ := mocks.read["collaborators"].(map[string]any)
			collaborators[lowestKey(collaborators, nil)] = false
		},
		expectExit: 2, expectContains: "not-a-collaborator",
	},
	{
		// The TOCTOU backstop: the collaborator check passed and the create
		// still came back with nobody assigned.
		name: "a create whose assignee was silently dropped",
		damage: func(t *testing.T, mocks *githubMockPair) {
			verify, _ := mocks.issue["verify"].(map[string]any)
			issue, _ := verify[lowestKey(verify, hasGithubAssignees)].(map[string]any)
			issue["assignees"] = []any{}
		},
		expectExit: 2, expectContains: "post-creation verification failed (assignees)",
	},
	{
		// The same drop, on the reconciling PATCH. Reporting success for an
		// assignment that did not happen is worse than refusing to report.
		name: "a reconciling PATCH that does not take", alreadyCreated: true,
		damage: func(t *testing.T, mocks *githubMockPair) {
			verify, _ := mocks.issue["verify"].(map[string]any)
			issue, _ := verify[lowestKey(verify, hasGithubAssignees)].(map[string]any)
			issue["assignees"] = []any{map[string]any{"login": "somebody-else"}}
		},
		extraArgs:  []string{"--reconcile-assignees"},
		expectExit: 2, expectContains: "silently dropped the assignee",
	},
	{
		// GitHub folds label case, so an issue labelled AGENTIC-SDLC is the
		// same issue. Reporting it as missing its anchor would refuse to reuse
		// something this kernel created.
		name: "labels that differ only in case", alreadyCreated: true,
		damage: func(t *testing.T, mocks *githubMockPair) {
			search, _ := mocks.issue["search"].(map[string]any)
			for key := range search {
				matches, _ := search[key].([]any)
				entry, _ := matches[0].(map[string]any)
				var upper []any
				for _, raw := range listOf(entry["labels"]) {
					label, _ := raw.(map[string]any)
					upper = append(upper, map[string]any{
						"name": strings.ToUpper(toStringOrEmpty(label["name"]))})
				}
				entry["labels"] = upper
			}
		},
		expectContains: `"status": "reused"`,
	},

	// The behaviours shared with the GitLab command, checked here too because
	// this is a separate implementation of each.

	{
		name: "two issues matching one label", alreadyCreated: true,
		damage: func(t *testing.T, mocks *githubMockPair) {
			search, _ := mocks.issue["search"].(map[string]any)
			key := lowestKey(search, nil)
			matches, _ := search[key].([]any)
			first, _ := matches[0].(map[string]any)
			search[key] = append(matches, map[string]any{
				"number": 999, "labels": first["labels"]})
		},
		expectExit: 2, expectContains: "ambiguous identity",
	},
	{
		name: "a matched issue somebody else wrote", alreadyCreated: true,
		damage: func(t *testing.T, mocks *githubMockPair) {
			verify, _ := mocks.issue["verify"].(map[string]any)
			issue, _ := verify[lowestKey(verify, nil)].(map[string]any)
			issue["user"] = map[string]any{"login": "somebody-else"}
		},
		expectExit: 2, expectContains: "author does not match the verified bot identity",
	},
	{
		name: "a matched issue carrying a foreign label", alreadyCreated: true,
		damage: func(t *testing.T, mocks *githubMockPair) {
			search, _ := mocks.issue["search"].(map[string]any)
			key := lowestKey(search, func(key string, _ any) bool {
				return strings.HasPrefix(key, githubGateLabelPrefix)
			})
			matches, _ := search[key].([]any)
			entry, _ := matches[0].(map[string]any)
			entry["labels"] = append(listOf(entry["labels"]),
				map[string]any{"name": githubGateLabelPrefix + "0000000000000000"})
		},
		expectExit: 2, expectContains: "foreign label",
	},
	{
		// A create that landed in another repository, read back from the API's
		// own URL rather than the one that was asked for.
		name: "an issue that came back from another repository",
		damage: func(t *testing.T, mocks *githubMockPair) {
			verify, _ := mocks.issue["verify"].(map[string]any)
			issue, _ := verify[lowestKey(verify, nil)].(map[string]any)
			issue["repository_url"] = "https://api.github.com/repos/acme/other"
		},
		expectExit: 2, expectContains: "post-creation verification failed (repo_from_url)",
	},
	{
		name: "an assignee that moved", alreadyCreated: true,
		damage: func(t *testing.T, mocks *githubMockPair) {
			verify, _ := mocks.issue["verify"].(map[string]any)
			issue, _ := verify[lowestKey(verify, hasGithubAssignees)].(map[string]any)
			issue["assignees"] = []any{map[string]any{"login": "somebody-else"}}
		},
		expectExit: 2, expectContains: `"drift": "assignee_changed"`,
	},
	{
		name:       "a named gate outside the configured set",
		extraArgs:  []string{"--gates", "G9"},
		expectExit: 2, expectContains: "not part of the task's configured",
	},
	{
		name:       "a named gate that does not exist",
		extraArgs:  []string{"--gates", "G99"},
		expectExit: 1, expectContains: "unknown gate id",
	},
	{
		name: "an apply with no plan digest", noDigest: true,
		expectExit: 1, expectContains: "--apply requires --plan-digest",
	},
	{
		name: "an apply whose plan digest no longer matches", wrongDigest: true,
		expectExit: 2, expectContains: "--plan-digest mismatch",
	},
}

func hasGithubAssignees(_ string, value any) bool {
	issue, _ := value.(map[string]any)
	return len(listOf(issue["assignees"])) > 0
}

func TestCreateGithubGateIssuesMatchesThePythonKernel(t *testing.T) {
	for _, probe := range githubIssuesCases {
		t.Run(probe.name, func(t *testing.T) {
			freezeClock(t)
			stubMutationDelay(t)
			root, manifest := githubIssuesFixture(t)
			report := githubDryRun(t, root, manifest)
			digest, _ := report["plan_digest"].(string)

			mocks := githubMocks(t, root, report, probe.alreadyCreated)
			if probe.damage != nil {
				probe.damage(t, mocks)
			}
			writeForgeMock(t, GitHubReadMockEnv, mocks.read)
			writeForgeMock(t, GitHubIssueMockEnv, mocks.issue)

			pythonRoot := filepath.Join(t.TempDir(), "python")
			goRoot := filepath.Join(t.TempDir(), "go")
			for _, target := range []string{pythonRoot, goRoot} {
				if err := copyTree(root, target); err != nil {
					t.Fatal(err)
				}
			}

			args := []string{"--provider", manifest, "create-github-gate-issues",
				"--root", root, "--task-id", githubIssuesTask, "--repo", "acme/app",
				"--as-bot", "sdlc-bot", "--allow-classification", "internal",
				"--apply", "--i-know-this-is-mocked"}
			switch {
			case probe.noDigest:
			case probe.wrongDigest:
				args = append(args, "--plan-digest", "sha256:0000000000000000")
			default:
				args = append(args, "--plan-digest", digest)
			}
			args = append(args, probe.extraArgs...)

			pythonCode, pythonOutput := runPythonGithubIssues(t, replaceRoot(args, root, pythonRoot))
			var stdout, stderr bytes.Buffer
			goCode := Run(replaceRoot(args, root, goRoot), &stdout, &stderr)
			goOutput := stdout.String() + stderr.String()

			if pythonCode != probe.expectExit || goCode != probe.expectExit {
				t.Fatalf("expected exit %d -- python %d, go %d\npython: %s\ngo: %s",
					probe.expectExit, pythonCode, goCode, pythonOutput, goOutput)
			}
			if pythonOutput != goOutput {
				t.Errorf("report differs.\npython:\n%s\ngo:\n%s", pythonOutput, goOutput)
			}
			if probe.expectContains != "" && !strings.Contains(goOutput, probe.expectContains) {
				t.Errorf("the report does not contain %q; this case checks something else:\n%s",
					probe.expectContains, goOutput)
			}

			name := filepath.Join(Overlay, "runs", githubIssuesTask, githubLedgerFile)
			pythonLedger, pythonExists := readIfPresent(filepath.Join(pythonRoot, name))
			goLedger, goExists := readIfPresent(filepath.Join(goRoot, name))
			if pythonExists != goExists {
				t.Fatalf("%s: python wrote it=%v, go wrote it=%v", name, pythonExists, goExists)
			}
			if pythonExists && blankLedgerTimes(pythonLedger) != blankLedgerTimes(goLedger) {
				t.Errorf("%s differs.\npython:\n%s\ngo:\n%s", name, pythonLedger, goLedger)
			}
		})
	}
}

// The invariants, stated without reference to the Python kernel.
