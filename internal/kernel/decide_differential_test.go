package kernel

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// `decide`, compared with the Python kernel -- and the one command in this
// port where a defect would not merely miss a problem but manufacture one.
//
// Everything else that has been ported reads. This writes an approval into a
// run record, so the cases below are weighted towards what it must refuse: an
// actor who is not the assigned authority, an actor who prepared the work, an
// actor who verified it, and an approval that cites no forge review under a
// project that requires one.
//
// Each case compares four things -- exit code, stdout, stderr, and the
// resulting run record byte for byte -- because a refusal that reports itself
// correctly while still writing the approval would pass any weaker check.
//
// One exemption, and it is narrower than it looks: when the failure comes from
// the operating system rather than the kernel, the two languages word it
// differently ("[Errno 2] No such file or directory: '<path>'" against "open
// <path>: no such file or directory"). Exit code, stream and the path named
// all match; only the sentence differs. Nothing the kernel words itself is
// exempted.

type decideCase struct {
	name    string
	prepare func(t *testing.T, root string)
	args    []string
	// expectExit is asserted against both kernels, so a case cannot pass by
	// both of them failing for an unrelated reason.
	expectExit int
	// expectStderr, when set, must appear in the refusal -- naming which
	// refusal fired rather than merely that one did.
	expectStderr string
}

func decideArgs(overrides ...string) []string {
	args := []string{
		"--task-id", decideTask, "--gate", "G1", "--role", "product_owner",
		"--actor-id", decideActor, "--evidence-uri", decideEvidence,
		"--decided-at", decideWhen, "--decision", "approved",
	}
	return append(args, overrides...)
}

func requireGitHubReview(t *testing.T, root string) {
	t.Helper()
	mutateJSON(t, filepath.Join(root, Overlay, "project.json"), func(document map[string]any) {
		document["approval_sources"] = map[string]any{
			"human_gate_default": "github-review", "allow_manual_fallback": false,
		}
	})
}

var decideCases = []decideCase{
	{name: "an approval is recorded", args: decideArgs()},
	{name: "a rejection is recorded", args: decideArgs("--decision", "rejected")},
	{name: "changes are requested", args: decideArgs("--decision", "request-changes")},
	{name: "a note is carried", args: decideArgs("--note", "looks fine to me — café")},

	{
		name: "the gate actually reaches approved", prepare: makeGateApprovable,
		args: decideArgs(),
	},

	{
		name:       "somebody who is not the assigned authority",
		args:       decideArgs("--actor-id", "github.com/somebody-else"),
		expectExit: 1, expectStderr: "does not match assigned authority",
	},
	{
		name:       "the actor prepared the work",
		prepare:    setGateIdentity("preparers", decideActor),
		args:       decideArgs(),
		expectExit: 1, expectStderr: "cannot decide on own work",
	},
	{
		name:       "the actor verified the work",
		prepare:    setGateIdentity("independent_verifier", decideActor),
		args:       decideArgs(),
		expectExit: 1, expectStderr: "cannot also decide",
	},
	{
		name:       "an approval with no forge review under a policy that requires one",
		prepare:    requireGitHubReview,
		args:       decideArgs("--evidence-uri", "note:approved-in-person"),
		expectExit: 1, expectStderr: "must be backed by a GitHub review",
	},
	{
		name:    "an approval with a forge review under that same policy",
		prepare: requireGitHubReview, args: decideArgs(),
	},
	{
		name:       "a gate that does not require this authority",
		args:       decideArgs("--role", "security_lead", "--actor-id", "github.com/security-lead"),
		expectExit: 1, expectStderr: "does not require authority role",
	},
	{
		name:       "a timestamp that is not one",
		args:       decideArgs("--decided-at", "yesterday"),
		expectExit: 1, expectStderr: "RFC 3339",
	},

	// Deciding twice supersedes rather than accumulates. Two approvals from
	// one identity on one gate would read as two people having signed.
	{
		name: "the same authority decides twice",
		prepare: func(t *testing.T, root string) {
			makeGateApprovable(t, root)
			approveG1For(t, root, providerManifest(t))
		},
		args: decideArgs(),
	},

	// A second authority who is no longer assigned. The requirement was
	// recorded as applicable when the task was planned, and the person has
	// since left -- so the gate cannot be approved even though the authority
	// deciding here is in order.
	{
		name: "another required authority is no longer assigned",
		prepare: func(t *testing.T, root string) {
			makeGateApprovable(t, root)
			addSecondAuthorityRequirement(t, root)
			mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
				func(document map[string]any) {
					authority, _ := document["engineering_lead"].(map[string]any)
					authority["status"] = "unassigned"
					authority["assignee"] = nil
				})
		},
		args: decideArgs(),
	},
}

// addSecondAuthorityRequirement gives G1 a second applicable human authority,
// so a case can make one of two signatures missing rather than the only one.
func addSecondAuthorityRequirement(t *testing.T, root string) {
	t.Helper()
	mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
		func(document map[string]any) {
			gates, _ := document["lifecycle_gates"].([]any)
			gate, _ := gates[0].(map[string]any)
			requirements, _ := gate["authority_requirements"].([]any)
			gate["authority_requirements"] = append(requirements, map[string]any{
				"authority_id": "engineering_lead", "authority_type": "human-approver",
				"role": "Engineering Lead", "applicability": "applicable",
				"rationale": "Assigned in project authority map",
			})
		})
}

func TestDecideAgreesWithThePythonKernel(t *testing.T) {
	for _, probe := range decideCases {
		t.Run(probe.name, func(t *testing.T) {
			pythonRoot, manifest := decidableProject(t)
			goRoot := t.TempDir()
			if probe.prepare != nil {
				probe.prepare(t, pythonRoot)
			}
			if err := copyTree(pythonRoot, goRoot); err != nil {
				t.Fatal(err)
			}

			pythonCode, pythonOutput := runPythonKernel(repositoryRoot(t),
				append([]string{"--provider", manifest, "decide", "--root", pythonRoot},
					probe.args...)...)

			var stdout, stderr bytes.Buffer
			goCode := Run(append([]string{"--provider", manifest, "decide", "--root", goRoot},
				probe.args...), &stdout, &stderr)

			if pythonCode != probe.expectExit || goCode != probe.expectExit {
				t.Fatalf("expected exit %d -- python %d, go %d\npython: %s\ngo: %s",
					probe.expectExit, pythonCode, goCode, pythonOutput, stderr.String())
			}
			// runPythonKernel combines the streams, so the comparison is
			// against whichever one the Go side used.
			goOutput := stdout.String() + stderr.String()
			if pythonOutput != goOutput {
				t.Errorf("output differs.\npython:\n%s\ngo:\n%s", pythonOutput, goOutput)
			}
			if probe.expectStderr != "" && !strings.Contains(goOutput, probe.expectStderr) {
				t.Errorf("the refusal does not mention %q: %s", probe.expectStderr, goOutput)
			}

			recordPath := filepath.Join(Overlay, "runs", decideTask, "run-record.json")
			pythonRecord := readFile(t, filepath.Join(pythonRoot, recordPath))
			goRecord := readFile(t, filepath.Join(goRoot, recordPath))
			if pythonRecord != goRecord {
				t.Errorf("the run records differ.\npython:\n%s\ngo:\n%s",
					pythonRecord, goRecord)
			}
		})
	}
}
