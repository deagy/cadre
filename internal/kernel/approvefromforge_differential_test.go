package kernel

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The forge approval adapters and the source-link commands, compared with the
// Python kernel.
//
// These write approvals into a run record, so every case compares four things
// -- exit code, stdout, stderr, and the resulting record byte for byte. A
// refusal that reports itself correctly and still writes the approval would
// pass any weaker check, and that is the failure worth catching: an approval
// that exists in the record and should not.
//
// The four automatic commands read a forge first, so they are exercised
// through mock files. The four manual ones take the identifiers from the
// operator and touch no network at all.

const approveTask = decideTask

type forgeApprovalCase struct {
	name string
	// fixture builds the project. Defaults to githubBoundProject.
	fixture func(t *testing.T) (string, string)
	// mocks are the forge responses this case answers from.
	mocks map[string]any
	// prepare edits the project after the fixture is built.
	prepare func(t *testing.T, root string)
	args    []string
	// touchesRecord says whether a run record should exist to compare. Every
	// case here has one; the flag exists for the ones that fail before the
	// project is even read.
	expectExit     int
	expectContains string
	// usageError marks a case the argument parser rejects, where argparse's
	// usage block is exempt from the comparison. See the exemption below.
	usageError bool
}

const (
	approveWhen   = "2026-08-15T09:00:00+00:00"
	reviewerLogin = "product-owner"
)

var forgeApprovalCases = []forgeApprovalCase{
	// approve-from-github: the operator supplies the identifiers.
	{
		name: "a GitHub review recorded by hand",
		args: []string{"approve-from-github", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app",
			"--pr", "1", "--review-id", "5001", "--reviewer-login", reviewerLogin,
			"--commit-sha", "abc123", "--decided-at", approveWhen},
		expectContains: `"gate_status": "approved"`,
	},
	{
		// The binding. Somebody else's approval on the same pull request must
		// not be recordable as this authority's.
		name: "a review by somebody who is not the assigned authority",
		args: []string{"approve-from-github", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app",
			"--pr", "1", "--review-id", "5001", "--reviewer-login", "somebody-else",
			"--commit-sha", "abc123", "--decided-at", approveWhen},
		expectExit: 1, expectContains: "does not match assigned authority login",
	},
	{
		// A repository name the URI grammar cannot express. A URI nobody can
		// decompose is evidence nobody can check.
		name: "a repository the review URI cannot express",
		args: []string{"approve-from-github", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app/extra",
			"--pr", "1", "--review-id", "5001", "--reviewer-login", reviewerLogin,
			"--commit-sha", "abc123", "--decided-at", approveWhen},
		expectExit: 1, expectContains: "invalid GitHub review URI components",
	},
	{
		name: "an approval time that is not a date",
		args: []string{"approve-from-github", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app",
			"--pr", "1", "--review-id", "5001", "--reviewer-login", reviewerLogin,
			"--commit-sha", "abc123", "--decided-at", "yesterday"},
		expectExit: 1, expectContains: "--decided-at must be a valid RFC 3339 date-time",
	},
	{
		name: "an approval with no --decided-at at all",
		args: []string{"approve-from-github", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app",
			"--pr", "1", "--review-id", "5001", "--reviewer-login", reviewerLogin,
			"--commit-sha", "abc123"},
		expectContains: `"gate_status": "approved"`,
	},
	{
		name: "an authority the gate does not require",
		args: []string{"approve-from-github", "--task-id", approveTask,
			"--gate", "G1", "--role", "security_lead", "--repo", "acme/app",
			"--pr", "1", "--review-id", "5001", "--reviewer-login", reviewerLogin,
			"--commit-sha", "abc123", "--decided-at", approveWhen},
		expectExit: 1, expectContains: "does not require authority role",
	},
	{
		name: "an authority nobody is assigned to",
		prepare: func(t *testing.T, root string) {
			mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
				func(document map[string]any) {
					owner, _ := document["product_owner"].(map[string]any)
					owner["status"] = "unassigned"
					owner["assignee"] = ""
				})
		},
		args: []string{"approve-from-github", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app",
			"--pr", "1", "--review-id", "5001", "--reviewer-login", reviewerLogin,
			"--commit-sha", "abc123", "--decided-at", approveWhen},
		expectExit: 1, expectContains: "is not assigned",
	},
	{
		// G2 needs two authorities. One approval must leave it unapproved --
		// this is the case where a kernel that marked a gate approved on the
		// first approval it saw would look completely fine.
		name: "one of the two authorities a gate needs",
		args: []string{"approve-from-github", "--task-id", approveTask,
			"--gate", "G2", "--role", "product_owner", "--repo", "acme/app",
			"--pr", "1", "--review-id", "5001", "--reviewer-login", reviewerLogin,
			"--commit-sha", "abc123", "--decided-at", approveWhen},
		expectContains: `"gate_status": "pending"`,
	},
	{
		name: "a gate id that does not exist",
		args: []string{"approve-from-github", "--task-id", approveTask,
			"--gate", "G99", "--role", "product_owner", "--repo", "acme/app",
			"--pr", "1", "--review-id", "5001", "--reviewer-login", reviewerLogin,
			"--commit-sha", "abc123", "--decided-at", approveWhen},
		expectExit: 2, expectContains: "invalid choice", usageError: true,
	},
	{
		name: "a pull request number that is not a number",
		args: []string{"approve-from-github", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app",
			"--pr", "one", "--review-id", "5001", "--reviewer-login", reviewerLogin,
			"--commit-sha", "abc123", "--decided-at", approveWhen},
		expectExit: 2, expectContains: "invalid int value", usageError: true,
	},

	// approve-from-github-pr: the kernel reads the reviews itself.
	{
		name:  "the latest approving review on a pull request",
		mocks: map[string]any{GitHubReviewsMockEnv: githubReviews()},
		args: []string{"approve-from-github-pr", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app", "--pr", "1"},
		expectContains: `"selected_review_id": 5002`,
	},
	{
		// The newest decision stands. Taking the latest *approval* instead
		// would let an approval survive the reviewer's own later request for
		// changes.
		name: "a reviewer who approved and then asked for changes",
		mocks: map[string]any{GitHubReviewsMockEnv: []any{
			review(5001, reviewerLogin, "APPROVED", "2026-08-14T09:00:00Z", "abc123"),
			review(5002, reviewerLogin, "CHANGES_REQUESTED", "2026-08-15T09:00:00Z", "abc123"),
		}},
		args: []string{"approve-from-github-pr", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app", "--pr", "1"},
		expectExit: 1, expectContains: "is not an effective approval",
	},
	{
		// A dismissed approval is not one. It stays in the API's list.
		name: "a review that was dismissed",
		mocks: map[string]any{GitHubReviewsMockEnv: []any{
			dismissed(review(5001, reviewerLogin, "APPROVED", "2026-08-15T09:00:00Z", "abc123")),
		}},
		args: []string{"approve-from-github-pr", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app", "--pr", "1"},
		expectExit: 1, expectContains: "is not an effective approval",
	},
	{
		name: "a pull request nobody assigned reviewed",
		mocks: map[string]any{GitHubReviewsMockEnv: []any{
			review(5001, "somebody-else", "APPROVED", "2026-08-15T09:00:00Z", "abc123"),
		}},
		args: []string{"approve-from-github-pr", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app", "--pr", "1"},
		expectExit: 1, expectContains: "no GitHub review found for reviewer",
	},
	{
		// The commit filter, which is what stops an approval of an old
		// revision being recorded against the current one.
		name:  "a commit nobody approved",
		mocks: map[string]any{GitHubReviewsMockEnv: githubReviews()},
		args: []string{"approve-from-github-pr", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app", "--pr", "1",
			"--commit-sha", "deadbeef"},
		expectExit: 1, expectContains: "at commit deadbeef",
	},
	{
		name:  "a commit that was approved, named explicitly",
		mocks: map[string]any{GitHubReviewsMockEnv: githubReviews()},
		args: []string{"approve-from-github-pr", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app", "--pr", "1",
			"--commit-sha", "ABC123"},
		expectContains: `"selected_commit_sha": "abc123"`,
	},
	{
		name: "an authority with no GitHub binding and no --reviewer-login",
		// An assignee that is an employee id rather than a forge URL: a
		// perfectly ordinary way to record a person, and one this command
		// cannot turn into a login.
		prepare: func(t *testing.T, root string) {
			mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
				func(document map[string]any) {
					owner, _ := document["product_owner"].(map[string]any)
					owner["assignee"] = "emp-4471"
					delete(owner, "github_login")
				})
		},
		mocks: map[string]any{GitHubReviewsMockEnv: githubReviews()},
		args: []string{"approve-from-github-pr", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--repo", "acme/app", "--pr", "1"},
		expectExit: 1, expectContains: "has no GitHub login binding",
	},

	// approve-from-gitlab and approve-from-gitlab-mr.
	{
		name:    "a GitLab approval recorded by hand",
		fixture: gitlabBoundProject,
		args: []string{"approve-from-gitlab", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--project-path", "acme/app",
			"--mr-iid", "7", "--approval-id", "42", "--approver-username", reviewerLogin,
			"--commit-sha", "abc123", "--decided-at", approveWhen},
		expectContains: `"gate_status": "approved"`,
	},
	{
		name:    "a GitLab approval by somebody who is not the assigned authority",
		fixture: gitlabBoundProject,
		args: []string{"approve-from-gitlab", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--project-path", "acme/app",
			"--mr-iid", "7", "--approval-id", "42", "--approver-username", "somebody-else",
			"--commit-sha", "abc123", "--decided-at", approveWhen},
		expectExit: 1, expectContains: "does not match assigned authority username",
	},
	{
		name:    "the merge request's approvals, read by the kernel",
		fixture: gitlabBoundProject,
		mocks:   map[string]any{GitLabApprovalsMockEnv: gitlabApprovals()},
		args: []string{"approve-from-gitlab-mr", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--project-path", "acme/app",
			"--mr-iid", "7"},
		expectContains: `"selected_approval_id": "77"`,
	},
	{
		name:    "a merge request nobody assigned approved",
		fixture: gitlabBoundProject,
		mocks: map[string]any{GitLabApprovalsMockEnv: map[string]any{
			"sha": "abc123", "updated_at": "2026-08-15T09:00:00Z",
			"approved_by": []any{map[string]any{
				"user": map[string]any{"id": 99, "username": "somebody-else"}}},
		}},
		args: []string{"approve-from-gitlab-mr", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--project-path", "acme/app",
			"--mr-iid", "7"},
		expectExit: 1, expectContains: "no GitLab approval found for approver",
	},
	{
		// The MR-level updated_at is what stands in for a per-approver time.
		// A response with none has no usable decision time at all.
		name:    "a merge request with no update time",
		fixture: gitlabBoundProject,
		mocks: map[string]any{GitLabApprovalsMockEnv: map[string]any{
			"sha": "abc123",
			"approved_by": []any{map[string]any{
				"user": map[string]any{"id": 77, "username": reviewerLogin}}},
		}},
		args: []string{"approve-from-gitlab-mr", "--task-id", approveTask,
			"--gate", "G1", "--role", "product_owner", "--project-path", "acme/app",
			"--mr-iid", "7"},
		expectExit: 1, expectContains: "no GitLab approval found for approver",
	},

	// The source-link commands.
	{
		name:  "a GitLab issue linked as G1's intent record",
		mocks: map[string]any{GitLabIssueFetchMock: gitlabIssue("opened")},
		args: []string{"link-intent-from-gitlab-issue", "--task-id", approveTask,
			"--role", "product_owner", "--project-path", "acme/app", "--issue-iid", "7"},
		expectContains: `"record_field": "intent_record_id"`,
	},
	{
		name:  "a GitLab issue linked as G2's requirements baseline",
		mocks: map[string]any{GitLabIssueFetchMock: gitlabIssue("closed")},
		args: []string{"link-requirements-from-gitlab-issue", "--task-id", approveTask,
			"--role", "product_owner", "--project-path", "acme/app", "--issue-iid", "8"},
		expectContains: `"record_field": "requirements_baseline_id"`,
	},
	{
		name:  "a GitHub issue linked as G1's intent record",
		mocks: map[string]any{GitHubIssueFetchMock: githubIssue("open")},
		args: []string{"link-intent-from-github-issue", "--task-id", approveTask,
			"--role", "product_owner", "--repo", "acme/app", "--issue-number", "7"},
		expectContains: `"issue_uri": "github-issue:acme/app:issues/7"`,
	},
	{
		// GitHub says "open" where GitLab says "opened", and either forge's
		// word on the other's endpoint is a response this cannot read.
		name:  "a GitHub issue whose state is GitLab's",
		mocks: map[string]any{GitHubIssueFetchMock: githubIssue("opened")},
		args: []string{"link-intent-from-github-issue", "--task-id", approveTask,
			"--role", "product_owner", "--repo", "acme/app", "--issue-number", "7"},
		expectExit: 1, expectContains: "unrecognized state",
	},
	{
		name: "an issue with no title",
		mocks: map[string]any{GitLabIssueFetchMock: map[string]any{
			"state": "opened", "web_url": "https://gitlab.com/acme/app/-/issues/7"}},
		args: []string{"link-intent-from-gitlab-issue", "--task-id", approveTask,
			"--role", "product_owner", "--project-path", "acme/app", "--issue-iid", "7"},
		expectExit: 1, expectContains: "is missing a title",
	},
	{
		name:  "a link attached by an authority the gate does not require",
		mocks: map[string]any{GitLabIssueFetchMock: gitlabIssue("opened")},
		args: []string{"link-intent-from-gitlab-issue", "--task-id", approveTask,
			"--role", "security_lead", "--project-path", "acme/app", "--issue-iid", "7"},
		expectExit: 1, expectContains: "does not require authority role",
	},
	{
		name:  "a link attached by an unassigned authority",
		mocks: map[string]any{GitLabIssueFetchMock: gitlabIssue("opened")},
		prepare: func(t *testing.T, root string) {
			mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
				func(document map[string]any) {
					owner, _ := document["product_owner"].(map[string]any)
					owner["status"] = "unassigned"
					owner["assignee"] = ""
				})
		},
		args: []string{"link-intent-from-gitlab-issue", "--task-id", approveTask,
			"--role", "product_owner", "--project-path", "acme/app", "--issue-iid", "7"},
		expectExit: 1, expectContains: "is not assigned",
	},
	{
		name:  "a role that is not an authority at all",
		mocks: map[string]any{GitLabIssueFetchMock: gitlabIssue("opened")},
		args: []string{"link-intent-from-gitlab-issue", "--task-id", approveTask,
			"--role", "chief-approver", "--project-path", "acme/app", "--issue-iid", "7"},
		expectExit: 2, expectContains: "invalid choice", usageError: true,
	},
}

func dismissed(entry map[string]any) map[string]any {
	entry["dismissed_state"] = "DISMISSED"
	return entry
}

func githubReviews() []any {
	return []any{
		review(5001, reviewerLogin, "APPROVED", "2026-08-14T09:00:00Z", "abc123"),
		review(5002, reviewerLogin, "APPROVED", "2026-08-15T09:00:00Z", "abc123"),
		review(5003, "somebody-else", "APPROVED", "2026-08-16T09:00:00Z", "abc123"),
	}
}

func gitlabApprovals() map[string]any {
	return map[string]any{
		"sha": "abc123", "updated_at": "2026-08-15T09:00:00Z",
		"approved_by": []any{
			map[string]any{"user": map[string]any{
				"id": 77, "username": reviewerLogin,
				// Present in a real response and deliberately never read.
				"name": "A Person", "avatar_url": "https://example.invalid/a.png",
			}},
		},
	}
}

func gitlabIssue(state string) map[string]any {
	return map[string]any{
		"title": "Ship the billing endpoint", "state": state,
		"web_url":    "https://gitlab.com/acme/app/-/issues/7",
		"updated_at": "2026-08-15T09:00:00Z",
	}
}

func githubIssue(state string) map[string]any {
	return map[string]any{
		"title": "Ship the billing endpoint", "state": state,
		"html_url":   "https://github.com/acme/app/issues/7",
		"updated_at": "2026-08-15T09:00:00Z",
	}
}

func TestTheForgeApprovalAdaptersMatchThePythonKernel(t *testing.T) {
	for _, probe := range forgeApprovalCases {
		t.Run(probe.name, func(t *testing.T) {
			freezeClock(t)
			fixture := probe.fixture
			if fixture == nil {
				fixture = githubBoundProject
			}
			root, manifest := fixture(t)
			if probe.prepare != nil {
				probe.prepare(t, root)
			}
			for variable, payload := range probe.mocks {
				writeForgeMock(t, variable, payload)
			}

			pythonRoot := filepath.Join(t.TempDir(), "python")
			goRoot := filepath.Join(t.TempDir(), "go")
			for _, target := range []string{pythonRoot, goRoot} {
				if err := copyTree(root, target); err != nil {
					t.Fatal(err)
				}
			}

			args := append([]string{"--provider", manifest, probe.args[0],
				"--root", root}, probe.args[1:]...)
			pythonCode, pythonOutput := runPythonGateStatus(t, replaceRoot(args, root, pythonRoot))
			var stdout, stderr bytes.Buffer
			goCode := Run(replaceRoot(args, root, goRoot), &stdout, &stderr)
			goOutput := stdout.String() + stderr.String()

			if pythonCode != probe.expectExit || goCode != probe.expectExit {
				t.Fatalf("expected exit %d -- python %d, go %d\npython: %s\ngo: %s",
					probe.expectExit, pythonCode, goCode, pythonOutput, goOutput)
			}
			// Two exemptions, and only for a usage error, both of them
			// argparse's rendering rather than anything the kernel words.
			//
			// The usage block: argparse prints a wrapped summary above its
			// message and the Go CLI prints none. Reproducing argparse's line
			// wrapping for every subcommand would be a facsimile of a thing
			// that disappears with the Python kernel; the missing usage block
			// is recorded as a follow-up instead.
			//
			// The choice list: argparse quoted every choice up to Python 3.13
			// and stopped in 3.14, so the same kernel prints two different
			// messages depending on the interpreter running it. CI found this
			// -- it runs an older python3 in the Go job than this machine has
			// -- and it is the reason the quoting inside `(choose from ...)`
			// is normalised away. What is still compared exactly: the command,
			// the flag, the offending value and *which* choices are listed.
			if probe.usageError {
				python := normalizeChoiceList(lastLine(pythonOutput))
				golang := normalizeChoiceList(lastLine(goOutput))
				if python != golang {
					t.Errorf("the usage error differs.\npython: %s\ngo:     %s", python, golang)
				}
			} else if pythonOutput != goOutput {
				t.Errorf("output differs.\npython:\n%s\ngo:\n%s", pythonOutput, goOutput)
			}
			if probe.expectContains != "" && !strings.Contains(goOutput, probe.expectContains) {
				t.Errorf("the output does not contain %q; this case checks something else:\n%s",
					probe.expectContains, goOutput)
			}

			// The record, which is where an approval that should not exist
			// would be.
			name := filepath.Join(Overlay, "runs", approveTask, "run-record.json")
			pythonRecord := readFile(t, filepath.Join(pythonRoot, name))
			goRecord := readFile(t, filepath.Join(goRoot, name))
			if pythonRecord != goRecord {
				t.Errorf("the run records differ.\npython:\n%s\ngo:\n%s",
					pythonRecord, goRecord)
			}
		})
	}
}

// choiceList matches argparse's "(choose from ...)" tail.
var choiceList = regexp.MustCompile(`\(choose from [^)]*\)`)

// normalizeChoiceList drops argparse's per-choice quoting.
//
// Only inside the parenthesised list: the offending value's own quotes are
// outside it and stay compared, because those the kernel controls.
func normalizeChoiceList(line string) string {
	return choiceList.ReplaceAllStringFunc(line, func(segment string) string {
		return strings.ReplaceAll(segment, "'", "")
	})
}

// lastLine is the final non-empty line of a stream.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	return lines[len(lines)-1]
}

// The invariants, stated without reference to the Python kernel.

func linkIssue(
	t *testing.T, root, manifest, gateID, role string, issue *SourceIssue,
) (*orderedObject, error) {
	t.Helper()
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	return registry.RecordSourceIssueLink(SourceLinkRequest{
		Root: root, TaskID: approveTask, GateID: gateID, AuthorityRole: role,
		ProjectPath: "acme/app", IssueNumber: issue.Number,
	}, issue)
}

func TestASourceLinkNeverApprovesAGate(t *testing.T) {
	// The whole distinction between these commands and the adapters above.
	// Linking an issue records where a gate's contribution came from; it is
	// not a human saying the contribution is acceptable, and no amount of
	// linking should ever produce an approved gate.
	freezeClock(t)
	root, manifest := githubBoundProject(t)

	before := readFile(t, filepath.Join(root, Overlay, "runs", approveTask, "run-record.json"))
	for _, number := range []int{7, 8, 9} {
		if _, err := linkIssue(t, root, manifest, "G1", "product_owner",
			&SourceIssue{Number: number, Title: "Intent", State: "opened"}); err != nil {
			t.Fatalf("linking issue %d: %v", number, err)
		}
	}
	after := readFile(t, filepath.Join(root, Overlay, "runs", approveTask, "run-record.json"))
	if before == after {
		t.Fatal("linking changed nothing at all; this test would prove nothing")
	}

	record, err := loadJSONObject(filepath.Join(root, Overlay, "runs", approveTask,
		"run-record.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range listOf(record["lifecycle_gates"]) {
		gate, _ := raw.(map[string]any)
		if gate["status"] == "approved" {
			t.Errorf("%v is approved after three source links", gate["gate_id"])
		}
		if len(listOf(gate["human_approvals"])) > 0 {
			t.Errorf("%v gained a human approval from a source link: %v",
				gate["gate_id"], gate["human_approvals"])
		}
	}
}

func TestRelinkingReplacesRatherThanAccumulates(t *testing.T) {
	// The record field holds one URI at a time. If the evidence refs
	// accumulated, the record would name one issue while carrying evidence
	// for three, and an auditor reading the refs would find two that are no
	// longer the source of anything.
	freezeClock(t)
	root, manifest := githubBoundProject(t)

	for _, number := range []int{7, 8, 9} {
		if _, err := linkIssue(t, root, manifest, "G1", "product_owner",
			&SourceIssue{Number: number, Title: "Intent", State: "opened"}); err != nil {
			t.Fatal(err)
		}
	}

	record, err := loadJSONObject(filepath.Join(root, Overlay, "runs", approveTask,
		"run-record.json"))
	if err != nil {
		t.Fatal(err)
	}
	if record["intent_record_id"] != "gitlab-issue:acme/app:issues/9" {
		t.Errorf("the record names %v, not the issue linked last", record["intent_record_id"])
	}
	gate, _ := listOf(record["lifecycle_gates"])[0].(map[string]any)
	var sourceRefs []string
	for _, raw := range listOf(gate["evidence_refs"]) {
		ref, _ := raw.(map[string]any)
		id := toStringOrEmpty(ref["evidence_id"])
		if strings.HasPrefix(id, "g1-source-gitlab-issue-") {
			sourceRefs = append(sourceRefs, id)
		}
	}
	if len(sourceRefs) != 1 {
		t.Errorf("three links left %d source evidence refs: %v", len(sourceRefs), sourceRefs)
	}
	if len(sourceRefs) == 1 && sourceRefs[0] != "g1-source-gitlab-issue-9" {
		t.Errorf("the surviving ref is %s, not the issue linked last", sourceRefs[0])
	}
	// The unrelated evidence the gate already carried must still be there --
	// dropping by prefix must not drop everything.
	if len(listOf(gate["evidence_refs"])) < 2 {
		t.Errorf("linking removed the gate's other evidence: %v", gate["evidence_refs"])
	}
}

func TestOnlyG1AndG2AcceptASourceLink(t *testing.T) {
	// Unreachable from the CLI, where each subcommand fixes its own gate. It
	// stays enforced in the recorder so a future caller cannot attach an
	// intent record to a gate that has no field to hold it.
	freezeClock(t)
	root, manifest := githubBoundProject(t)
	for _, gateID := range []string{"G3", "G5", "G10"} {
		_, err := linkIssue(t, root, manifest, gateID, "product_owner",
			&SourceIssue{Number: 7, Title: "Intent", State: "opened"})
		if err == nil {
			t.Errorf("%s accepted a source link", gateID)
			continue
		}
		if !strings.Contains(err.Error(), "does not accept a GitLab issue source link") {
			t.Errorf("%s was refused for the wrong reason: %v", gateID, err)
		}
	}
}

func TestAGitLabApprovalCarriesNoPersonalData(t *testing.T) {
	// The data-minimization rule, checked at the boundary where it could be
	// broken: the normalizer is the only thing that reads GitLab's response,
	// so a field it does not extract cannot reach an evidence record.
	records, err := GitLabApprovalRecordsFromAPIResponse(map[string]any{
		"sha": "abc123", "updated_at": "2026-08-15T09:00:00Z",
		"approved_by": []any{map[string]any{"user": map[string]any{
			"id": 77, "username": "product-owner",
			"name":       "A Real Person",
			"email":      "person@example.invalid",
			"avatar_url": "https://example.invalid/a.png",
			"web_url":    "https://gitlab.com/product-owner",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one record, got %d", len(records))
	}
	record, _ := records[0].(map[string]any)
	for _, forbidden := range []string{"name", "email", "avatar_url", "web_url"} {
		if _, present := record[forbidden]; present {
			t.Errorf("the approval record carries %s: %v", forbidden, record)
		}
	}
	// And it does carry the five fields evidence needs, or the check above
	// passes by extracting nothing.
	for _, required := range []string{
		"approval_id", "username", "state", "decided_at", "commit_sha"} {
		if _, present := record[required]; !present {
			t.Errorf("the approval record is missing %s: %v", required, record)
		}
	}
	if record["username"] != "product-owner" {
		t.Errorf("the pseudonymous username was not kept: %v", record)
	}
}

func TestTheLatestReviewDecidesEvenWhenAnEarlierOneApproved(t *testing.T) {
	// Stated directly, because it is the one selection rule that could
	// plausibly have gone the other way -- and the other way would let a
	// withdrawn approval be recorded as evidence.
	reviews := []any{
		review(1, "alice", "APPROVED", "2026-08-14T09:00:00Z", "abc"),
		review(2, "alice", "CHANGES_REQUESTED", "2026-08-15T09:00:00Z", "abc"),
	}
	if _, err := SelectGitHubReview(reviews, "alice", ""); err == nil {
		t.Error("an approval that was superseded by a request for changes was selected")
	}

	// And the reverse: changes requested, then approved, is an approval.
	reversed := []any{
		review(1, "alice", "CHANGES_REQUESTED", "2026-08-14T09:00:00Z", "abc"),
		review(2, "alice", "APPROVED", "2026-08-15T09:00:00Z", "abc"),
	}
	selected, err := SelectGitHubReview(reversed, "alice", "")
	if err != nil {
		t.Fatalf("an approval that superseded a request for changes was refused: %v", err)
	}
	if id, _ := jsonInteger(selected["id"]); id != 2 {
		t.Errorf("selected review %v, not the later one", selected["id"])
	}
}

func TestAReviewWithNoUsableTimestampIsNeverSelected(t *testing.T) {
	// A review with no valid submitted_at cannot be ordered against any other,
	// so selecting it would make "the latest" meaningless.
	reviews := []any{
		review(1, "alice", "APPROVED", "not a date", "abc"),
	}
	if _, err := SelectGitHubReview(reviews, "alice", ""); err == nil {
		t.Error("a review with an unparseable timestamp was selected")
	}
	approvals := []any{
		map[string]any{"approval_id": "1", "username": "alice",
			"state": "approved", "decided_at": nil, "commit_sha": "abc"},
	}
	if _, err := SelectGitLabApproval(approvals, "alice", ""); err == nil {
		t.Error("an approval with no decision time was selected")
	}
}

func TestReviewerMatchingIgnoresCaseOnBothSides(t *testing.T) {
	// GitHub logins and GitLab usernames are case-insensitive, so a project
	// that wrote "Product-Owner" into its authority map must still match a
	// review by "product-owner". Refusing would block a legitimate approval.
	reviews := []any{review(1, "Product-Owner", "APPROVED", "2026-08-15T09:00:00Z", "abc")}
	if _, err := SelectGitHubReview(reviews, "product-owner", ""); err != nil {
		t.Errorf("a differently-cased login did not match: %v", err)
	}
	// And a genuinely different login still does not match.
	if _, err := SelectGitHubReview(reviews, "somebody-else", ""); err == nil {
		t.Error("a different login matched")
	}
}
