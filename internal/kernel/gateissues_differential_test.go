package kernel

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// `create-gate-issues`, compared with the Python kernel.
//
// The property this command exists for is idempotency: run it twice and the
// second run reuses what the first created rather than creating a second copy.
// So the cases below are built in pairs -- a mock where nothing exists yet, and
// the same mock where everything does -- and the comparison covers the report,
// the exit code, and the sidecar ledger, because the ledger is what an operator
// reads afterwards to find out what happened.
//
// Every mock is derived from a real dry run rather than hand-written. The
// labels, markers and titles are all computed from the task and the contracts,
// and a hand-written fixture would encode whatever this port produced rather
// than what the plan actually calls for.

func lowestKey(entries map[string]any, keep func(string, any) bool) string {
	var candidates []string
	for key, value := range entries {
		if keep == nil || keep(key, value) {
			candidates = append(candidates, key)
		}
	}
	if len(candidates) == 0 {
		panic("no mock entry matched: the case would damage nothing")
	}
	sort.Strings(candidates)
	return candidates[0]
}

func hasAssignees(_ string, value any) bool {
	issue, _ := value.(map[string]any)
	return len(listOf(issue["assignees"])) > 0
}

func withoutAssignees(key string, value any) bool { return !hasAssignees(key, value) }

// duplicateMatch makes one issue of the given family findable twice.
func duplicateMatch(prefix string) func(*testing.T, map[string]any) {
	return func(t *testing.T, mock map[string]any) {
		search, _ := mock["search"].(map[string]any)
		key := lowestKey(search, func(key string, _ any) bool {
			return strings.Contains(key, prefix)
		})
		matches, _ := search[key].([]any)
		first, _ := matches[0].(map[string]any)
		search[key] = append(matches, map[string]any{
			"iid": 999, "labels": listOf(first["labels"])})
	}
}

// foreignAuthor puts somebody else's name on one issue. Gate issues have no
// assignee and approval issues have exactly one, which is how a case picks
// which family it is damaging.
func foreignAuthor(family func(string, any) bool) func(*testing.T, map[string]any) {
	return func(t *testing.T, mock map[string]any) {
		verify, _ := mock["verify"].(map[string]any)
		issue, _ := verify[lowestKey(verify, family)].(map[string]any)
		issue["author"] = map[string]any{"username": "somebody-else"}
	}
}

func madeConfidential(family func(string, any) bool) func(*testing.T, map[string]any) {
	return func(t *testing.T, mock map[string]any) {
		verify, _ := mock["verify"].(map[string]any)
		issue, _ := verify[lowestKey(verify, family)].(map[string]any)
		issue["confidential"] = true
	}
}

// gateIssuesCase is one comparison.
type gateIssuesCase struct {
	name string
	// alreadyCreated builds the second-run world instead of the first.
	alreadyCreated bool
	// damage edits the mock after it is built, to make one thing go wrong.
	damage func(t *testing.T, mock map[string]any)
	// extraArgs are appended to the apply invocation.
	extraArgs []string
	// wrongDigest supplies a digest that no longer matches.
	wrongDigest bool
	// noDigest omits --plan-digest entirely.
	noDigest       bool
	expectExit     int
	expectContains string
}

var gateIssuesCases = []gateIssuesCase{
	{
		name: "creating every issue", expectContains: `"status": "created"`,
	},
	{
		// The whole point: a second run finds what the first created and
		// reuses it, rather than creating a duplicate beside it.
		name: "reusing every issue", alreadyCreated: true,
		expectContains: `"status": "reused"`,
	},
	{
		// Two issues carrying the same label pair: this cannot tell which is
		// the one it created. Gate and approval issues are separate cases
		// because the two paths check this separately -- one covering both
		// would pass on either check alone.
		name: "two gate issues match one label", alreadyCreated: true,
		damage:     duplicateMatch(gateLabelPrefix),
		expectExit: 2, expectContains: "ambiguous identity",
	},
	{
		name: "two approval issues match one label", alreadyCreated: true,
		damage:     duplicateMatch(approvalLabelPrefix),
		expectExit: 2, expectContains: "ambiguous identity",
	},
	{
		// An issue somebody else authored, carrying our label. Reusing it
		// would attach this task's tracking to a stranger's issue. Checked
		// separately on each path, so covered separately here.
		name: "a matched gate issue somebody else wrote", alreadyCreated: true,
		damage:     foreignAuthor(withoutAssignees),
		expectExit: 2, expectContains: "author does not match the verified bot identity",
	},
	{
		name: "a matched approval issue somebody else wrote", alreadyCreated: true,
		damage:     foreignAuthor(hasAssignees),
		expectExit: 2, expectContains: "author does not match the verified bot identity",
	},
	{
		// A label the issue should not carry. Two identities on one issue
		// means it would be reused for two different things.
		name: "a matched issue carrying a foreign label", alreadyCreated: true,
		damage: func(t *testing.T, mock map[string]any) {
			search, _ := mock["search"].(map[string]any)
			key := lowestKey(search, func(key string, _ any) bool {
				return strings.Contains(key, gateLabelPrefix)
			})
			matches, _ := search[key].([]any)
			issue, _ := matches[0].(map[string]any)
			issue["labels"] = append(listOf(issue["labels"]),
				gateLabelPrefix+"0000000000000000")
		},
		expectExit: 2, expectContains: "foreign label",
	},
	{
		// The forge stored something other than what was sent -- here, an
		// issue made confidential, which the authorities it is for could not
		// read. Both paths verify separately, so both are covered.
		name:       "a gate issue that does not verify after creation",
		damage:     madeConfidential(withoutAssignees),
		expectExit: 2, expectContains: "post-creation verification failed",
	},
	{
		name:       "an approval issue that does not verify after creation",
		damage:     madeConfidential(hasAssignees),
		expectExit: 2, expectContains: "post-creation verification failed",
	},
	{
		// Somebody reassigned an approval issue on the forge. Reported, never
		// silently corrected.
		name: "an approval issue whose assignee moved", alreadyCreated: true,
		damage: func(t *testing.T, mock map[string]any) {
			verify, _ := mock["verify"].(map[string]any)
			issue, _ := verify[lowestKey(verify, hasAssignees)].(map[string]any)
			issue["assignees"] = []any{map[string]any{"username": "somebody-else"}}
		},
		expectExit: 2, expectContains: `"drift": "assignee_changed"`,
	},
	{
		// The same drift, with the operator asking for it to be corrected.
		name: "assignee drift, reconciled on request", alreadyCreated: true,
		damage: func(t *testing.T, mock map[string]any) {
			verify, _ := mock["verify"].(map[string]any)
			issue, _ := verify[lowestKey(verify, hasAssignees)].(map[string]any)
			issue["assignees"] = []any{map[string]any{"username": "somebody-else"}}
		},
		extraArgs:      []string{"--reconcile-assignees"},
		expectContains: "assignee_changed (reconciled)",
	},
	{
		// A username nobody on the instance answers to. Refused for that one
		// candidate, and the rest of the run continues.
		name: "an authority whose username resolves to nobody",
		damage: func(t *testing.T, mock map[string]any) {
			users, _ := mock["users"].(map[string]any)
			users[lowestKey(users, nil)] = []any{}
		},
		expectExit: 2, expectContains: "gitlab-user-unresolved",
	},
	{
		// Two accounts answering to one username. Assigning an approval to
		// either would be picking somebody nobody named.
		name: "an authority whose username is ambiguous",
		damage: func(t *testing.T, mock map[string]any) {
			users, _ := mock["users"].(map[string]any)
			username := lowestKey(users, nil)
			matches, _ := users[username].([]any)
			users[username] = append(matches, map[string]any{
				"id": 9999, "username": username, "state": "active"})
		},
		expectExit: 2, expectContains: "gitlab-user-ambiguous",
	},
	{
		// A gate the operator named that the task is not configured for. Named
		// gates are refused rather than skipped -- an instruction that cannot
		// be carried out is more useful said than silently ignored.
		name:       "a named gate outside the configured set",
		extraArgs:  []string{"--gates", "G9"},
		expectExit: 2, expectContains: "not part of the task's configured",
	},
	{
		// Not a gate at all: a structural mistake in the invocation, not
		// something a human has to go and resolve on the forge.
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
	{
		// An instance without the Issue Links API. The operator asked for a
		// link; a run that silently did not make one is worse than one that
		// says it could not.
		name: "an instance with no Issue Links API",
		damage: func(t *testing.T, mock map[string]any) {
			links := map[string]any{}
			// Every possible source iid, since which one is reached first
			// depends on the plan.
			for index := 100; index < 130; index++ {
				links[fmt.Sprint(index)] = map[string]any{
					"error_status": 403, "error": "disabled"}
			}
			mock["link"] = links
		},
		extraArgs:  []string{"--link-type", "relates_to"},
		expectExit: 2, expectContains: "Issue Links API unavailable",
	},
}

func TestCreateGateIssuesMatchesThePythonKernel(t *testing.T) {
	for _, probe := range gateIssuesCases {
		t.Run(probe.name, func(t *testing.T) {
			freezeClock(t)
			root, manifest := issuesFixture(t)
			report := dryRunPlan(t, root, manifest)
			digest, _ := report["plan_digest"].(string)

			mock := issueMock(t, root, report, probe.alreadyCreated)
			if probe.damage != nil {
				probe.damage(t, mock)
			}
			writeForgeMock(t, GitLabIssueMockEnv, mock)

			pythonRoot := filepath.Join(t.TempDir(), "python")
			goRoot := filepath.Join(t.TempDir(), "go")
			for _, target := range []string{pythonRoot, goRoot} {
				if err := copyTree(root, target); err != nil {
					t.Fatal(err)
				}
			}

			args := []string{"--provider", manifest, "create-gate-issues",
				"--root", root, "--task-id", issuesTask, "--project-path", "acme/app",
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

			pythonCode, pythonOutput := runPythonGateStatus(t, replaceRoot(args, root, pythonRoot))
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

			// The ledger, with its timestamps blanked -- everything else in it
			// is what an operator reads to find out what happened.
			name := filepath.Join(Overlay, "runs", issuesTask, "gate-issues-gitlab.json")
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

// ledgerTimestampField matches the two wall-clock fields a ledger entry
// carries, and nothing else in it.
var ledgerTimestampField = regexp.MustCompile(`"(attempted_at|recorded_at)": "[^"]*"`)

func blankLedgerTimes(ledger string) string {
	return ledgerTimestampField.ReplaceAllString(ledger, `"$1": "<when>"`)
}

// The invariants, stated without reference to the Python kernel.
