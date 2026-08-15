package kernel

import (
	"bytes"
	"encoding/json"
	"os"
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

const (
	decideTask     = "TASK-1"
	decideActor    = "github.com/product-owner"
	decideEvidence = "github-review:acme/app:pull/1:review/1:reviewer/product-owner"
	decideWhen     = "2026-08-15T09:00:00+00:00"
)

func decideArgs(overrides ...string) []string {
	args := []string{
		"--task-id", decideTask, "--gate", "G1", "--role", "product_owner",
		"--actor-id", decideActor, "--evidence-uri", decideEvidence,
		"--decided-at", decideWhen, "--decision", "approved",
	}
	return append(args, overrides...)
}

// makeGateApprovable fills in everything a gate needs before an approval can
// take effect: bound artifacts, evidence, a preparer, and an independent
// verifier who declared they did not prepare it.
func makeGateApprovable(t *testing.T, root string) {
	t.Helper()
	mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
		func(document map[string]any) {
			gates, _ := document["lifecycle_gates"].([]any)
			gate, _ := gates[0].(map[string]any)
			gate["evidence_refs"] = []any{map[string]any{
				"evidence_id": "g1-intent", "uri": "github-issue:acme/app:issues/7",
				"hash_algorithm": "sha256", "hash": strings.Repeat("a", 64),
				"classification": "internal",
			}}
			gate["artifact_bindings"] = []any{map[string]any{
				"artifact_id": "intent", "revision": "r1", "digest": "sha256:abc",
			}}
			gate["preparers"] = []any{map[string]any{
				"id": "agent://product-intent-agent", "kind": "agent",
				"role": "product-intent-agent",
			}}
			gate["independent_verifier"] = map[string]any{
				"id": "agent://code-reviewer", "kind": "agent", "role": "code-reviewer",
			}
			gate["independence_declaration"] = map[string]any{
				"verifier_confirmed_not_preparer":   true,
				"verifier_made_material_correction": false,
			}
		})
}

// approveG1For records an approval through the kernel itself, so a case can
// start from a gate that already has one.
func approveG1For(t *testing.T, root, manifest string) {
	t.Helper()
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Decide(DecideRequest{
		Root: root, TaskID: decideTask, GateID: "G1", AuthorityRole: "product_owner",
		Decision: "approved", ActorID: decideActor, EvidenceURI: decideEvidence,
		DecidedAt: "2026-08-14T09:00:00+00:00",
	}); err != nil {
		t.Fatalf("seeding an approval: %v", err)
	}
}

func setGateIdentity(field, id string) func(*testing.T, string) {
	return func(t *testing.T, root string) {
		t.Helper()
		mutateJSON(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"),
			func(document map[string]any) {
				gates, _ := document["lifecycle_gates"].([]any)
				gate, _ := gates[0].(map[string]any)
				identity := map[string]any{"id": id, "kind": "human", "role": "Product Owner"}
				if field == "preparers" {
					gate["preparers"] = []any{identity}
					return
				}
				gate["independent_verifier"] = identity
			})
	}
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

// providerManifest is the provider this repository ships, which the fixtures
// are built against.
func providerManifest(t *testing.T) string {
	t.Helper()
	return filepath.Join(repositoryRoot(t), "provider", "provider.json")
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// decidableProject is a project with assigned authorities and one planned
// task, which is the least a decision needs to exist at all.
func decidableProject(t *testing.T) (root, manifest string) {
	t.Helper()
	template, manifest := plannedProjectTemplate(t)
	root = t.TempDir()
	if err := copyTree(template, root); err != nil {
		t.Fatal(err)
	}
	// The template's own planned task is TASK-2, already carrying a G1
	// decision. These cases want an untouched one.
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Plan(PlanRequest{
		Root: root, TaskID: decideTask, Task: "add an endpoint to the billing service",
	}); err != nil {
		t.Fatalf("planning the fixture task: %v", err)
	}
	return root, manifest
}

// The invariants, stated without reference to the Python kernel so they
// survive its removal.

func TestDecideRefusesSelfApprovalEvenWhenEverythingElseIsInOrder(t *testing.T) {
	// The case that matters most: a gate that is otherwise completely ready to
	// approve, where the only thing wrong is who is approving it. Every other
	// check passes, so nothing but this one stands between the record and a
	// forged approval.
	for _, role := range []string{"preparers", "independent_verifier"} {
		t.Run(role, func(t *testing.T) {
			root, manifest := decidableProject(t)
			makeGateApprovable(t, root)
			setGateIdentity(role, decideActor)(t, root)

			registry := NewRegistry()
			if err := registry.LoadProvider(manifest); err != nil {
				t.Fatal(err)
			}
			before := readFile(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"))

			_, err := registry.Decide(DecideRequest{
				Root: root, TaskID: decideTask, GateID: "G1", AuthorityRole: "product_owner",
				Decision: "approved", ActorID: decideActor, EvidenceURI: decideEvidence,
				DecidedAt: decideWhen,
			})
			if err == nil {
				t.Fatal("an identity approved work it was involved in producing")
			}
			after := readFile(t, filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"))
			if before != after {
				t.Error("the refused decision still wrote to the run record")
			}
		})
	}
}

func TestDecideCannotApproveAGateOnItsOwnSayS0(t *testing.T) {
	// One authority's "approved" is an input, not a conclusion. Without the
	// artifacts, evidence and verifier declaration a gate requires, the
	// approval is recorded and the gate stays where it was.
	root, manifest := decidableProject(t)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Decide(DecideRequest{
		Root: root, TaskID: decideTask, GateID: "G1", AuthorityRole: "product_owner",
		Decision: "approved", ActorID: decideActor, EvidenceURI: decideEvidence,
		DecidedAt: decideWhen,
	}); err != nil {
		t.Fatalf("the decision itself was refused: %v", err)
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(readFile(t,
		filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"))), &record); err != nil {
		t.Fatal(err)
	}
	gates, _ := record["lifecycle_gates"].([]any)
	gate, _ := gates[0].(map[string]any)
	if gate["status"] == "approved" {
		t.Error("a gate with no bound artifacts, evidence or verifier was marked approved")
	}
	approvals, _ := gate["human_approvals"].([]any)
	if len(approvals) != 1 {
		t.Errorf("the decision itself should still be recorded, got %d approvals", len(approvals))
	}
}

func TestARejectionWithdrawsAnEarlierApproval(t *testing.T) {
	// A gate that reached approved and is then rejected must not stay
	// approved. The earlier approval stays in the record as history; the gate
	// does not keep the status it was given.
	root, manifest := decidableProject(t)
	makeGateApprovable(t, root)
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	decision := DecideRequest{
		Root: root, TaskID: decideTask, GateID: "G1", AuthorityRole: "product_owner",
		Decision: "approved", ActorID: decideActor, EvidenceURI: decideEvidence,
		DecidedAt: decideWhen,
	}
	if _, err := registry.Decide(decision); err != nil {
		t.Fatal(err)
	}
	if status := gateStatus(t, root); status != "approved" {
		t.Fatalf("the fixture did not reach approved (%s); this test would prove nothing", status)
	}

	decision.Decision = "rejected"
	if _, err := registry.Decide(decision); err != nil {
		t.Fatal(err)
	}
	if status := gateStatus(t, root); status == "approved" {
		t.Error("the gate stayed approved after its approval was withdrawn")
	}
}

func gateStatus(t *testing.T, root string) string {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(readFile(t,
		filepath.Join(root, Overlay, "runs", decideTask, "run-record.json"))), &record); err != nil {
		t.Fatal(err)
	}
	gates, _ := record["lifecycle_gates"].([]any)
	gate, _ := gates[0].(map[string]any)
	status, _ := gate["status"].(string)
	return status
}
