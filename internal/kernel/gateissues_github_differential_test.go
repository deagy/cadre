package kernel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
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

const githubIssuesTask = "TASK-1"

// stubMutationDelay removes the inter-write pause for the Go side.
func stubMutationDelay(t *testing.T) {
	t.Helper()
	previous := DelayBetweenMutations
	DelayBetweenMutations = func() {}
	t.Cleanup(func() { DelayBetweenMutations = previous })
}

// runPythonGithubIssues runs the Python kernel with its clock pinned and its
// write delay zeroed, matching what stubMutationDelay does on this side.
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

// githubIssuesFixture is a project whose authorities carry GitHub bindings.
func githubIssuesFixture(t *testing.T) (root, manifest string) {
	t.Helper()
	root, manifest = decidableProject(t)
	mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
		func(document map[string]any) {
			for role, raw := range document {
				authority, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if _, isAuthority := authority["status"]; !isAuthority {
					continue
				}
				authority["github_login"] = strings.ReplaceAll(role, "_", "-")
			}
		})
	return root, manifest
}

// githubDryRun runs the Go dry run and returns its report.
func githubDryRun(t *testing.T, root, manifest string) map[string]any {
	t.Helper()
	writeForgeMock(t, GitHubReadMockEnv, map[string]any{
		"identity": map[string]any{"login": "sdlc-bot"},
	})
	writeForgeMock(t, GitHubIssueMockEnv, map[string]any{
		"repo": map[string]any{"has_issues": true, "private": true},
	})
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--provider", manifest, "create-github-gate-issues",
		"--root", root, "--task-id", githubIssuesTask, "--repo", "acme/app",
		"--as-bot", "sdlc-bot", "--allow-classification", "internal"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("the dry run failed: %s%s", stdout.String(), stderr.String())
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return report
}

// githubMocks builds both canned response files for a plan.
//
// created=false is the first-run world; created=true is the world where every
// issue already exists and the run must reuse rather than duplicate.
type githubMockPair struct {
	read  map[string]any
	issue map[string]any
}

func githubMocks(t *testing.T, root string, report map[string]any, created bool) *githubMockPair {
	t.Helper()
	contracts, err := lifecycleGateContracts()
	if err != nil {
		t.Fatal(err)
	}
	record, err := loadJSONObject(filepath.Join(root, Overlay, "runs", githubIssuesTask,
		"run-record.json"))
	if err != nil {
		t.Fatal(err)
	}
	gateByID := map[string]map[string]any{}
	for _, raw := range listOf(record["lifecycle_gates"]) {
		if gate, ok := raw.(map[string]any); ok {
			id, _ := gate["gate_id"].(string)
			gateByID[id] = gate
		}
	}

	search := map[string]any{}
	createResponses := map[string]any{}
	verify := map[string]any{}
	users := map[string]any{}
	collaborators := map[string]any{}
	number := 100

	addIssue := func(label, title, assignee string) {
		createResponses[FixedLabel+","+label] = map[string]any{"number": number}
		assignees := []any{}
		if assignee != "" {
			assignees = append(assignees, map[string]any{"login": assignee})
		}
		verify[fmt.Sprint(number)] = map[string]any{
			"title": title, "state": "open",
			"labels": []any{
				map[string]any{"name": FixedLabel}, map[string]any{"name": label}},
			"assignees":      assignees,
			"user":           map[string]any{"login": "sdlc-bot"},
			"repository_url": "https://api.github.com/repos/acme/app",
			"html_url":       fmt.Sprintf("https://github.com/acme/app/issues/%d", number),
		}
		if created {
			// A search answers on the marker label alone, and the anchor label
			// is checked on the match rather than queried for.
			search[label] = []any{map[string]any{
				"number": number,
				"labels": []any{
					map[string]any{"name": FixedLabel}, map[string]any{"name": label}},
			}}
		}
		number++
	}

	for _, raw := range listOf(report["gate_issues"]) {
		item, _ := raw.(map[string]any)
		gateID, _ := item["gate_id"].(string)
		label, _ := item["label"].(string)
		gateName := gateID
		if name, ok := contracts[gateID]["name"].(string); ok && name != "" {
			gateName = name
		}
		title, err := GateIssueTitle(githubIssuesTask, gateID, gateName)
		if err != nil {
			t.Fatal(err)
		}
		addIssue(label, title, "")
	}
	for _, raw := range listOf(report["approval_issues"]) {
		item, _ := raw.(map[string]any)
		gateID, _ := item["gate_id"].(string)
		authorityID, _ := item["authority_id"].(string)
		label, _ := item["label"].(string)
		gateName := gateID
		if name, ok := contracts[gateID]["name"].(string); ok && name != "" {
			gateName = name
		}
		role := authorityID
		for _, requirementRaw := range listOf(gateByID[gateID]["authority_requirements"]) {
			requirement, _ := requirementRaw.(map[string]any)
			if requirement["authority_id"] == authorityID {
				if declared, ok := requirement["role"].(string); ok {
					role = declared
				}
			}
		}
		title, err := ApprovalIssueTitle(githubIssuesTask, gateID, gateName, role)
		if err != nil {
			t.Fatal(err)
		}
		login := strings.ReplaceAll(authorityID, "_", "-")
		users[login] = true
		collaborators["acme/app:"+login] = true
		addIssue(label, title, login)
	}

	return &githubMockPair{
		read: map[string]any{
			"identity": map[string]any{"login": "sdlc-bot"},
			"users":    users, "collaborators": collaborators,
		},
		issue: map[string]any{
			"repo":   map[string]any{"has_issues": true, "private": true},
			"search": search, "create": createResponses, "verify": verify,
		},
	}
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

func applyGithubIssues(
	t *testing.T, root, manifest, digest string, reconcile bool,
) (*orderedObject, error) {
	t.Helper()
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	return registry.CreateGithubGateIssues(GithubGateIssuesRequest{
		Root: root, TaskID: githubIssuesTask, Repo: "acme/app", AsBot: "sdlc-bot",
		Apply: true, PlanDigest: digest, AllowClassification: "internal",
		ReconcileAssignees: reconcile, KnowinglyMocked: true,
	})
}

func TestASecondGithubRunCreatesNothing(t *testing.T) {
	freezeClock(t)
	stubMutationDelay(t)
	root, manifest := githubIssuesFixture(t)
	report := githubDryRun(t, root, manifest)
	digest, _ := report["plan_digest"].(string)

	first := githubMocks(t, root, report, false)
	writeForgeMock(t, GitHubReadMockEnv, first.read)
	writeForgeMock(t, GitHubIssueMockEnv, first.issue)
	firstResult, err := applyGithubIssues(t, root, manifest, digest, false)
	if err != nil {
		t.Fatalf("the first run failed: %v", err)
	}
	createdCount := countStatuses(firstResult, "created")
	if createdCount == 0 {
		t.Fatal("the first run created nothing; this test would prove nothing")
	}

	second := githubMocks(t, root, report, true)
	writeForgeMock(t, GitHubReadMockEnv, second.read)
	writeForgeMock(t, GitHubIssueMockEnv, second.issue)
	secondResult, err := applyGithubIssues(t, root, manifest, digest, false)
	if err != nil {
		t.Fatalf("the second run failed: %v", err)
	}
	if created := countStatuses(secondResult, "created"); created != 0 {
		t.Errorf("the second run created %d issues", created)
	}
	if reused := countStatuses(secondResult, "reused"); reused != createdCount {
		t.Errorf("the first run created %d issues and the second reused %d",
			createdCount, reused)
	}
}

func TestTheTwoForgesNeverShareAMarker(t *testing.T) {
	// The domain separation that keeps one task's GitLab and GitHub tracking
	// apart. A shared marker would mean a GitHub search matching a GitLab
	// issue's label, and each run reusing the other forge's artifact.
	for _, gateID := range []string{"G1", "G5", "G10"} {
		if ComputeGateMarker(githubIssuesTask, gateID) ==
			ComputeGithubGateMarker(githubIssuesTask, gateID) {
			t.Errorf("%s: the two forges compute the same gate marker", gateID)
		}
		if ComputeApprovalMarker(githubIssuesTask, gateID, "product_owner") ==
			ComputeGithubApprovalMarker(githubIssuesTask, gateID, "product_owner") {
			t.Errorf("%s: the two forges compute the same approval marker", gateID)
		}
	}
	// And the label prefixes are disjoint, so neither prefix check can match
	// the other forge's label as a foreign one of its own family.
	if strings.HasPrefix(githubGateLabelPrefix, gateLabelPrefix) ||
		strings.HasPrefix(gateLabelPrefix, githubGateLabelPrefix) {
		t.Errorf("one gate label prefix is a prefix of the other: %q, %q",
			gateLabelPrefix, githubGateLabelPrefix)
	}
	if strings.HasPrefix(githubApprovalLabelPrefix, approvalLabelPrefix) ||
		strings.HasPrefix(approvalLabelPrefix, githubApprovalLabelPrefix) {
		t.Errorf("one approval label prefix is a prefix of the other: %q, %q",
			approvalLabelPrefix, githubApprovalLabelPrefix)
	}
}

func TestTheTwoForgesNeverShareAPlanDigest(t *testing.T) {
	// A digest is the handshake between a dry run and an apply. If the two
	// commands hashed a task identically, a digest an operator took from one
	// forge's dry run would be accepted by the other's apply.
	freezeClock(t)
	root, manifest := githubIssuesFixture(t)

	writeForgeMock(t, GitLabIssueMockEnv, map[string]any{
		"identity": map[string]any{"username": "sdlc-bot"},
	})
	// Exit 2 is expected and irrelevant here: this fixture's authorities carry
	// GitHub bindings, so every GitLab approval candidate is refused. A plan
	// full of refusals still has a digest, and the digest is the whole point.
	var gitlabOut bytes.Buffer
	if code := Run([]string{"--provider", manifest, "create-gate-issues",
		"--root", root, "--task-id", githubIssuesTask, "--project-path", "acme/app",
		"--as-bot", "sdlc-bot", "--allow-classification", "internal"},
		&gitlabOut, &gitlabOut); code != 0 && code != 2 {
		t.Fatalf("the GitLab dry run failed: %s", gitlabOut.String())
	}
	var gitlabReport map[string]any
	if err := json.Unmarshal(gitlabOut.Bytes(), &gitlabReport); err != nil {
		t.Fatal(err)
	}
	if gitlabReport["plan_digest"] == nil {
		t.Fatal("the GitLab dry run produced no digest to compare")
	}
	githubReport := githubDryRun(t, root, manifest)

	if gitlabReport["plan_digest"] == githubReport["plan_digest"] {
		t.Errorf("both forges hash this task to %v", githubReport["plan_digest"])
	}
}

func TestAGithubApprovalIssuePointsAtTheGithubCommand(t *testing.T) {
	// Somebody reading one of these has to be told what actually records an
	// approval. Naming the other forge's subcommand would send them somewhere
	// that cannot record anything for this issue.
	description, err := RenderGithubApprovalDescription("TASK-1", "G5", "abc123",
		"acme/app", 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"closing this issue is not approval evidence",
		"does not approve G5",
		"must not be a preparer or the independent verifier",
		"agentic-sdlc approve-from-github-pr",
		"> parent acme/app#7",
	} {
		if !strings.Contains(description, required) {
			t.Errorf("the approval issue does not say %q:\n%s", required, description)
		}
	}
	if strings.Contains(description, "gitlab") {
		t.Errorf("a GitHub approval issue mentions GitLab:\n%s", description)
	}
}

func TestAGithubIssueNeverCarriesTheTaskID(t *testing.T) {
	secret := "PROJECT-SECRET-CODENAME"
	description, err := RenderGithubApprovalDescription(secret, "G1",
		ComputeGithubApprovalMarker(secret, "G1", "product_owner"), "acme/app", 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(description, secret) {
		t.Errorf("the approval description carries the raw task id:\n%s", description)
	}
	if !strings.Contains(description, TaskHash(secret)) {
		t.Errorf("the approval description carries neither the id nor its hash:\n%s", description)
	}
}

func TestAFullSearchPageIsNeverTreatedAsAnAnswer(t *testing.T) {
	// Stated directly, because the boundary is the interesting part: nineteen
	// results is an ordinary ambiguity, twenty is a page that may have been
	// truncated, and the two are reported differently.
	one := []any{map[string]any{"number": 1}}
	if err := checkSearchResults(one, "gate G1"); err != nil {
		t.Errorf("a single match was refused: %v", err)
	}

	nineteen := make([]any, 19)
	for index := range nineteen {
		nineteen[index] = map[string]any{"number": index}
	}
	err := checkSearchResults(nineteen, "gate G1")
	if err == nil || !strings.Contains(err.Error(), "ambiguous identity") {
		t.Errorf("nineteen matches were not reported as ambiguous: %v", err)
	}

	twenty := make([]any, 0, issueSearchPageSize)
	twenty = append(twenty, nineteen...)
	twenty = append(twenty, map[string]any{"number": 19})
	err = checkSearchResults(twenty, "gate G1")
	if err == nil || !strings.Contains(err.Error(), "result-cap-exceeded") {
		t.Errorf("a full page was not reported as possibly truncated: %v", err)
	}
}

func TestASearchIsNeverAnswerableByAPullRequest(t *testing.T) {
	// Checked before the count, so a full page containing a pull request
	// reports the more specific problem.
	matches := []any{map[string]any{"number": 1, "pull_request": map[string]any{}}}
	err := checkSearchResults(matches, "gate G1")
	if err == nil || !strings.Contains(err.Error(), "label-on-pull-request") {
		t.Errorf("a pull request answered an issue search: %v", err)
	}

	full := make([]any, issueSearchPageSize)
	for index := range full {
		full[index] = map[string]any{"number": index}
	}
	full[issueSearchPageSize-1] = map[string]any{
		"number": 99, "pull_request": map[string]any{}}
	err = checkSearchResults(full, "gate G1")
	if err == nil || !strings.Contains(err.Error(), "label-on-pull-request") {
		t.Errorf("a full page hid the pull request in it: %v", err)
	}
}

func TestGithubLabelComparisonsFoldCase(t *testing.T) {
	// GitHub treats these as one label. Comparing exactly would refuse to
	// reuse an issue this kernel created.
	label, err := GithubGateLabel(ComputeGithubGateMarker("TASK-1", "G1"))
	if err != nil {
		t.Fatal(err)
	}
	entry := map[string]any{"labels": []any{
		map[string]any{"name": strings.ToUpper(FixedLabel)},
		map[string]any{"name": strings.ToUpper(label)},
	}}
	if err := validateMatchedGithubIssue(entry, label, githubGateLabelPrefix, "gate G1"); err != nil {
		t.Errorf("an uppercased pair of this kernel's own labels was refused: %v", err)
	}
	// And an issue genuinely missing the anchor is still refused, or the check
	// above passes by accepting everything.
	missing := map[string]any{"labels": []any{map[string]any{"name": label}}}
	if err := validateMatchedGithubIssue(missing, label, githubGateLabelPrefix, "gate G1"); err == nil {
		t.Error("an issue with no anchor label was accepted")
	}
}

func TestMutationsAreSpacedButNeverBeforeTheFirst(t *testing.T) {
	// The delay exists to keep a burst under GitHub's secondary rate limit. A
	// run that paused before its first write would add a second to every
	// invocation for nothing.
	previous := DelayBetweenMutations
	delays := 0
	DelayBetweenMutations = func() { delays++ }
	t.Cleanup(func() { DelayBetweenMutations = previous })

	mutations := &githubMutations{}
	mutations.next()
	if delays != 0 {
		t.Errorf("the first mutation was delayed")
	}
	for index := 0; index < 4; index++ {
		mutations.next()
	}
	if delays != 4 {
		t.Errorf("five mutations produced %d delays, not four", delays)
	}
}

func TestASecondaryRateLimitIsRecognisedInWhateverWrapsIt(t *testing.T) {
	// `gh` surfaces neither the status nor a structured error, so this matches
	// on the message text. It has to survive the surrounding prose.
	for _, stderr := range []string{
		"You have exceeded a secondary rate limit. Please wait a few minutes.",
		"gh: HTTP 403: Secondary Rate Limit exceeded (https://api.github.com/...)",
		"error: SECONDARY RATE LIMIT",
	} {
		if !isSecondaryRateLimit(stderr) {
			t.Errorf("not recognised as a secondary rate limit: %q", stderr)
		}
	}
	for _, stderr := range []string{
		"HTTP 403: Resource not accessible by integration",
		"API rate limit exceeded for user ID 1",
	} {
		if isSecondaryRateLimit(stderr) {
			t.Errorf("an ordinary failure was read as a secondary rate limit: %q", stderr)
		}
	}
}

func TestAnAlreadyExistingLabelIsNotAFailure(t *testing.T) {
	// EnsureLabel runs before every creation, so the second gate of a run
	// always finds the anchor label already there.
	for _, stderr := range []string{
		`gh: HTTP 422: Validation Failed (already_exists)`,
		`{"message":"Validation Failed","errors":[{"code":"already_exists"}]} 422`,
	} {
		if !isLabelAlreadyExists(stderr) {
			t.Errorf("not recognised as an existing label: %q", stderr)
		}
	}
	// A different 422 is still a failure -- treating every 422 as success
	// would hide a label this kernel could not create at all.
	if isLabelAlreadyExists(`gh: HTTP 422: Validation Failed (invalid name)`) {
		t.Error("an unrelated 422 was treated as an existing label")
	}
}

// githubLedgerRepoField pins the two field names that tell an operator which
// forge a ledger belongs to.
var githubLedgerRepoField = regexp.MustCompile(`"(repo|bot_login)":`)

func TestTheGithubLedgerNamesItsOwnForge(t *testing.T) {
	freezeClock(t)
	stubMutationDelay(t)
	root, manifest := githubIssuesFixture(t)
	report := githubDryRun(t, root, manifest)
	digest, _ := report["plan_digest"].(string)

	mocks := githubMocks(t, root, report, false)
	writeForgeMock(t, GitHubReadMockEnv, mocks.read)
	writeForgeMock(t, GitHubIssueMockEnv, mocks.issue)
	if _, err := applyGithubIssues(t, root, manifest, digest, false); err != nil {
		t.Fatal(err)
	}

	ledger, exists := readIfPresent(filepath.Join(root, Overlay, "runs", githubIssuesTask,
		githubLedgerFile))
	if !exists {
		t.Fatal("no ledger was written")
	}
	if matches := githubLedgerRepoField.FindAllString(ledger, -1); len(matches) != 2 {
		t.Errorf("the ledger does not carry both repo and bot_login:\n%s", ledger)
	}
	for _, absent := range []string{`"project_path"`, `"bot_username"`, `"issue_iid"`} {
		if strings.Contains(ledger, absent) {
			t.Errorf("the GitHub ledger carries the GitLab command's %s:\n%s", absent, ledger)
		}
	}
	if !strings.Contains(ledger, `"issue_number"`) {
		t.Errorf("the ledger records no issue numbers:\n%s", ledger)
	}
}
