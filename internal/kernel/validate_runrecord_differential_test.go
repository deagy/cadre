package kernel

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// The run-record half of `validate`, compared against the Python kernel on a
// project this kernel built, into which a single deliberate lie is told.
//
// Happy-path parity would prove very little here. A freshly planned task has
// nothing approved, so both implementations agree by saying nothing -- and the
// checks that matter are the ones that only run once somebody has said yes.
// So each case below takes a real run record and breaks exactly one thing:
// the verifier is also the preparer; the approver is also the preparer; the
// review evidence names somebody other than the assigned authority; an
// authority is relabeled. If the Go port missed a check, the case that breaks
// it diverges and names itself.
//
// One honest exemption, applied by normalizeSchemaWording below. Both
// implementations validate these documents against the same JSON Schema, and
// both report a violation at the same location, but the sentence describing it
// is the validating library's own -- "'owner' is a required property" against
// "missing properties: 'owner'". The comparison holds the file and the
// location exactly and drops the trailing sentence. Nothing else is exempted:
// every message this kernel writes itself is compared in full, brackets and
// quoting included.

// schemaMessagePrefix captures "<path>: schema <location>" and drops whatever
// the validating library wrote after it.
var schemaMessagePrefix = regexp.MustCompile(`^(.*: schema [^:]*):`)

func normalizeSchemaWording(messages []string) []string {
	normalized := make([]string, 0, len(messages))
	for _, message := range messages {
		if match := schemaMessagePrefix.FindStringSubmatch(message); match != nil {
			normalized = append(normalized, match[1])
			continue
		}
		normalized = append(normalized, message)
	}
	return normalized
}

var (
	fixtureOnce     sync.Once
	fixtureTemplate string
	fixtureManifest string
	fixtureSkip     string
)

// plannedProjectTemplate builds one project with a planned, part-approved task
// and returns it for copying. Built once: it costs four kernel invocations,
// and every case wants the same starting point.
func plannedProjectTemplate(t *testing.T) (template, manifest string) {
	t.Helper()
	fixtureOnce.Do(func() {
		root, manifestPath, reason := buildPlannedProject()
		fixtureTemplate, fixtureManifest, fixtureSkip = root, manifestPath, reason
	})
	if fixtureSkip != "" {
		t.Skip(fixtureSkip)
	}
	return fixtureTemplate, fixtureManifest
}

func buildPlannedProject() (root, manifest, skip string) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return "", "", err.Error()
	}
	manifest = filepath.Join(repoRoot, "provider", "provider.json")
	if _, err := os.Stat(manifest); err != nil {
		return "", "", "no provider manifest in this checkout"
	}
	// Not t.TempDir: this outlives the test that happened to build it.
	root, err = os.MkdirTemp("", "kernel-run-fixture-")
	if err != nil {
		return "", "", err.Error()
	}

	// Built by *this* kernel, not the Python one.
	//
	// It was Python's until the port finished, and that had a failure mode
	// worth naming: when Python could not run, this returned a skip reason and
	// every differential downstream of the fixture reported PASS having
	// executed nothing. A green suite that checks nothing is worse than a red
	// one. Building it in-process removes the skip entirely -- the fixture is
	// now as available as the code under test.
	//
	// It is a starting state, not an assertion. Both kernels then act on
	// identical copies of it, so which one produced it does not privilege
	// either; what it does mean is that the differentials now start from a
	// state the shipping kernel actually produces.
	run := func(args ...string) (int, string) {
		var output bytes.Buffer
		code := Run(append([]string{"--provider", manifest}, args...), &output, &output)
		return code, output.String()
	}
	if code, output := run("init", "--root", root, "--profile", "secure-cloud",
		"--project-id", "probe"); code != 0 {
		return "", "", "kernel init failed: " + truncate(output)
	}

	// Assign every authority. A freshly initialised project has none, and an
	// unassigned authority cannot approve -- so without this the fixture could
	// never reach the state the interesting checks guard.
	authoritiesPath := filepath.Join(root, Overlay, "authorities.json")
	if err := rewriteJSON(authoritiesPath, func(document map[string]any) {
		for role, raw := range document {
			authority, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, isAuthority := authority["status"]; !isAuthority {
				continue
			}
			authority["status"] = "assigned"
			authority["assignee"] = "github.com/" + strings.ReplaceAll(role, "_", "-")
			if authority["applicability"] == "unknown" {
				authority["applicability"] = "applicable"
			}
		}
	}); err != nil {
		return "", "", err.Error()
	}

	// Planned after assignment, so the run record's authority requirements are
	// applicable rather than unresolved.
	if code, output := run("plan", "--root", root, "--task-id", fixtureTask,
		"--task", "add an endpoint"); code != 0 {
		return "", "", "kernel plan failed: " + truncate(output)
	}
	if code, output := run("decide", "--root", root, "--task-id", fixtureTask,
		"--gate", "G1", "--role", "product_owner", "--decision", "approved",
		"--actor-id", "github.com/product-owner",
		"--evidence-uri", "github-review:acme/app:pull/1:review/1:reviewer/product-owner",
	); code != 0 {
		return "", "", "kernel decide failed: " + truncate(output)
	}
	return root, manifest, ""
}

const fixtureTask = "TASK-2"

// plannedProject copies the template into a fresh directory for one case.
func plannedProject(t *testing.T) (root, manifest string) {
	t.Helper()
	template, manifest := plannedProjectTemplate(t)
	root = t.TempDir()
	if err := copyTree(template, root); err != nil {
		t.Fatalf("copying the fixture: %v", err)
	}
	return root, manifest
}

// TestThePythonKernelIsAvailableToCompareAgainst fails, rather than skips,
// when the other half of the differentials cannot run.
//
// Every test in this package that compares against the Python kernel skips
// when it is unimportable -- which is right for each of them individually and
// wrong for the suite: a run with no Python reports green having compared
// nothing. That is the state this test exists to make visible, and it is not
// hypothetical. Before the shared fixture was built in-process, a missing
// Python turned 43 subtests into silent passes.
//
// This states an existing requirement rather than adding one: the repository's
// own roster/ and plugin/ suites are Python, so a checkout without it cannot
// run its tests anyway. When the Python kernel is deleted, this test and the
// differentials go together -- it is not a reason to keep either.
func TestThePythonKernelIsAvailableToCompareAgainst(t *testing.T) {
	code, output := runPythonKernel(repositoryRoot(t), "--version")
	if code != 0 {
		t.Fatalf("the Python kernel could not run, so every differential in this "+
			"package compared nothing and reported success:\n%s", output)
	}
	if strings.TrimSpace(output) != Version {
		t.Errorf("the Python kernel reports %q and this one reports %q; the "+
			"differentials are comparing two different kernels",
			strings.TrimSpace(output), Version)
	}
}

func runPythonKernel(repoRoot string, args ...string) (int, string) {
	return pythonKernelIn(filepath.Join(repoRoot, "kernel"), args...)
}

func copyTree(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil // the fixture holds no links; anything else is not ours to copy
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func rewriteJSON(path string, mutate func(map[string]any)) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return err
	}
	mutate(document)
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

func recordPathIn(root string) string {
	return filepath.Join(root, Overlay, "runs", fixtureTask, "run-record.json")
}

func dispatchPathIn(root string) string {
	return filepath.Join(root, Overlay, "runs", fixtureTask, "dispatch-plan.json")
}

// approveG1 promotes G1 to an approval that passes every check, so that a
// case breaking one thing is breaking exactly one thing.
func approveG1(document map[string]any) {
	gates, _ := document["lifecycle_gates"].([]any)
	if len(gates) == 0 {
		return
	}
	gate, _ := gates[0].(map[string]any)
	gate["status"] = "approved"
	gate["applicability"] = "applicable"
	gate["decided_at"] = "2026-08-15T09:00:00+00:00"
	gate["evidence_refs"] = []any{map[string]any{
		"evidence_id": "g1-intent", "uri": "github-issue:acme/app:issues/7",
		"hash_algorithm": "sha256", "hash": strings.Repeat("a", 64),
		"classification": "internal",
	}}
	gate["artifact_bindings"] = []any{map[string]any{
		"artifact_id": "intent", "revision": "r1", "digest": "sha256:abc",
	}}
	gate["preparers"] = []any{map[string]any{
		"id": "agent://product-intent-agent", "kind": "agent", "role": "product-intent-agent",
	}}
	gate["independent_verifier"] = map[string]any{
		"id": "agent://code-reviewer", "kind": "agent", "role": "code-reviewer",
	}
	gate["independence_declaration"] = map[string]any{
		"verifier_confirmed_not_preparer": true, "verifier_made_material_correction": false,
	}
}

func firstGate(document map[string]any) map[string]any {
	gates, _ := document["lifecycle_gates"].([]any)
	if len(gates) == 0 {
		return map[string]any{}
	}
	gate, _ := gates[0].(map[string]any)
	return gate
}

func gateApprovals(gate map[string]any) []map[string]any {
	var approvals []map[string]any
	for _, raw := range listOf(gate["human_approvals"]) {
		if approval, ok := raw.(map[string]any); ok {
			approvals = append(approvals, approval)
		}
	}
	return approvals
}

// runRecordProbes each break one thing about a real, planned run record.
//
// `expect` names a substring the resulting report must contain. Without it a
// case could pass by both implementations reporting nothing, which is
// agreement about having checked nothing.
var runRecordProbes = []struct {
	name   string
	expect string
	mutate func(t *testing.T, root string)
}{
	{"an-approved-gate-is-accepted", "", func(t *testing.T, root string) {
		mutateJSON(t, recordPathIn(root), approveG1)
	}},

	{"verifier-is-also-the-preparer", "verifier is also a preparer",
		func(t *testing.T, root string) {
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				approveG1(document)
				gate := firstGate(document)
				preparers := listOf(gate["preparers"])
				gate["independent_verifier"] = preparers[0]
			})
		}},

	{"verifier-made-a-material-correction",
		"verifier made a material correction and lost approval authority",
		func(t *testing.T, root string) {
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				approveG1(document)
				declaration, _ := firstGate(document)["independence_declaration"].(map[string]any)
				declaration["verifier_made_material_correction"] = true
			})
		}},

	{"approver-is-also-a-preparer", "approver is not independent",
		func(t *testing.T, root string) {
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				approveG1(document)
				gate := firstGate(document)
				preparers := listOf(gate["preparers"])
				for _, approval := range gateApprovals(gate) {
					preparers = append(preparers, approval["approver"])
				}
				gate["preparers"] = preparers
			})
		}},

	{"approver-is-not-human", "approval is not human", func(t *testing.T, root string) {
		mutateJSON(t, recordPathIn(root), func(document map[string]any) {
			approveG1(document)
			for _, approval := range gateApprovals(firstGate(document)) {
				approver, _ := approval["approver"].(map[string]any)
				approver["kind"] = "agent"
			}
		})
	}},

	{"review-evidence-names-somebody-else",
		"approval GitHub reviewer does not match assigned authority",
		func(t *testing.T, root string) {
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				approveG1(document)
				for _, approval := range gateApprovals(firstGate(document)) {
					approval["evidence_refs"] = []any{map[string]any{
						"evidence_id": "g1-review", "hash_algorithm": "sha256",
						"hash": strings.Repeat("b", 64), "classification": "internal",
						"uri": "github-review:acme/app:pull/1:review/1:reviewer/somebody-else",
					}}
				}
			})
		}},

	{"authority-is-relabeled", "is relabeled", func(t *testing.T, root string) {
		mutateJSON(t, recordPathIn(root), func(document map[string]any) {
			approveG1(document)
			for _, raw := range listOf(firstGate(document)["authority_requirements"]) {
				requirement, _ := raw.(map[string]any)
				requirement["role"] = "Chief Approver"
			}
		})
	}},

	{"authority-applicability-is-unresolved",
		"approved with unresolved authority applicability", func(t *testing.T, root string) {
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				approveG1(document)
				for _, raw := range listOf(firstGate(document)["authority_requirements"]) {
					requirement, _ := raw.(map[string]any)
					requirement["applicability"] = "unknown"
				}
			})
		}},

	{"a-critical-finding-is-still-open", "unresolved critical/high findings",
		func(t *testing.T, root string) {
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				approveG1(document)
				firstGate(document)["findings"] = []any{map[string]any{
					"finding_id": "F-1", "severity": "critical", "status": "open",
					"owner": "github.com/security-lead",
				}}
			})
		}},

	{"an-accepted-finding-has-no-exception", "accepted finding lacks a valid exception",
		func(t *testing.T, root string) {
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				approveG1(document)
				firstGate(document)["findings"] = []any{map[string]any{
					"finding_id": "F-2", "severity": "high", "status": "accepted-exception",
					"owner": "github.com/security-lead",
				}}
			})
		}},

	{"an-exception-is-self-approved", "accepted finding lacks a valid exception",
		func(t *testing.T, root string) {
			// Owner and approver are the same person. The exception is
			// complete in every other respect, which is the point: this is
			// self-approval reached through the escape hatch rather than the
			// front door, and it must be refused just as firmly.
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				approveG1(document)
				gate := firstGate(document)
				gate["findings"] = []any{map[string]any{
					"finding_id": "F-3", "severity": "high", "status": "accepted-exception",
					"owner": "github.com/security-lead",
				}}
				identity := map[string]any{
					"id": "github.com/security-lead", "kind": "human", "role": "Security Lead",
				}
				gate["exceptions"] = []any{map[string]any{
					"exception_id": "X-1", "finding_id": "F-3", "justification": "ship it",
					"compensating_controls": []any{"monitoring"},
					"owner":                 identity, "approver": identity,
					"expires_at": "2027-01-01T00:00:00+00:00", "remediation_plan": "fix next sprint",
				}}
			})
		}},

	{"an-execution-record-is-missing", "is missing its required execution record",
		func(t *testing.T, root string) {
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				summary, _ := document["execution_summary"].(map[string]any)
				gates, _ := summary["gates"].(map[string]any)
				delete(gates, "G3")
			})
		}},

	{"an-execution-record-omits-its-required-agents",
		"required agent set does not match lifecycle contract", func(t *testing.T, root string) {
			// Absent is not the same as empty. This is the case the port got
			// wrong first: comparing lengths made a record that said nothing
			// look like one that said "nobody".
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				summary, _ := document["execution_summary"].(map[string]any)
				gates, _ := summary["gates"].(map[string]any)
				for _, raw := range gates {
					gate, _ := raw.(map[string]any)
					gate["configured"] = true
					delete(gate, "required_agents")
				}
			})
		}},

	{"an-invalidated-gate-has-no-re-entry-gate",
		"invalidation is missing required re-entry gate", func(t *testing.T, root string) {
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				for _, raw := range listOf(document["lifecycle_gates"]) {
					gate, _ := raw.(map[string]any)
					gate["status"] = "invalidated"
					delete(gate, "required_reentry_gate")
				}
			})
		}},

	{"a-downstream-gate-survives-an-invalidation", "must be invalidated",
		func(t *testing.T, root string) {
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				gate := firstGate(document)
				gate["status"] = "invalidated"
				gate["required_reentry_gate"] = "G1"
			})
		}},

	{"the-dispatch-plan-was-edited-after-the-fact",
		"stored dispatch fingerprint does not match current dispatch content",
		func(t *testing.T, root string) {
			mutateJSON(t, dispatchPathIn(root), func(document map[string]any) {
				agents, _ := document["agents"].(map[string]any)
				agents["primary"] = []any{"backend-engineer"}
			})
		}},

	{"the-plan-makes-one-agent-author-and-reviewer", "dispatch author/reviewer overlap",
		func(t *testing.T, root string) {
			mutateJSON(t, dispatchPathIn(root), func(document map[string]any) {
				agents, _ := document["agents"].(map[string]any)
				primary := stringsIn(agents["primary"])
				if len(primary) == 0 {
					primary = []string{"backend-engineer"}
					agents["primary"] = []any{"backend-engineer"}
				}
				agents["reviewers"] = []any{primary[0]}
			})
		}},

	{"the-task-ids-disagree", "task IDs do not match", func(t *testing.T, root string) {
		mutateJSON(t, dispatchPathIn(root), func(document map[string]any) {
			document["task_id"] = "TASK-99"
		})
	}},

	{"the-gates-are-out-of-order", "lifecycle gates must be exactly G1-G10 in order",
		func(t *testing.T, root string) {
			mutateJSON(t, recordPathIn(root), func(document map[string]any) {
				gates := listOf(document["lifecycle_gates"])
				reversed := make([]any, 0, len(gates))
				for index := len(gates) - 1; index >= 0; index-- {
					reversed = append(reversed, gates[index])
				}
				document["lifecycle_gates"] = reversed
			})
		}},

	{"the-dispatch-plan-is-gone", "must both exist", func(t *testing.T, root string) {
		if err := os.Remove(dispatchPathIn(root)); err != nil {
			t.Fatal(err)
		}
	}},

	{"a-timestamp-has-no-offset", "is not a 'date-time'", func(t *testing.T, root string) {
		mutateJSON(t, recordPathIn(root), func(document map[string]any) {
			document["recorded_at"] = "2026-08-15 09:00:00"
		})
	}},

	{"a-required-field-is-missing", "missing required fields", func(t *testing.T, root string) {
		mutateJSON(t, recordPathIn(root), func(document map[string]any) {
			delete(document, "baseline_revision")
		})
	}},
}

func TestBrokenRunRecordsAreDescribedIdentically(t *testing.T) {
	for _, probe := range runRecordProbes {
		t.Run(probe.name, func(t *testing.T) {
			root, manifest := plannedProject(t)
			probe.mutate(t, root)

			python := pythonValidate(t, root, manifest)
			golang := goValidateProject(t, root, manifest)

			pythonErrors := normalizeSchemaWording(python.Errors)
			goErrors := normalizeSchemaWording(golang.Errors)
			if difference := setDifference(pythonErrors, goErrors); len(difference) > 0 {
				t.Errorf("only the Python kernel reported: %v", difference)
			}
			if difference := setDifference(goErrors, pythonErrors); len(difference) > 0 {
				t.Errorf("only the Go kernel reported: %v", difference)
			}
			if python.Valid != golang.Valid {
				t.Errorf("verdicts differ -- python valid=%v, go valid=%v",
					python.Valid, golang.Valid)
			}

			// The case has to have done something. Agreement about an
			// unbroken record proves nothing about the check it was aimed at.
			if probe.expect == "" {
				if !golang.Valid {
					t.Errorf("a correctly approved gate was rejected: %v", golang.Errors)
				}
				return
			}
			found := false
			for _, message := range golang.Errors {
				if strings.Contains(message, probe.expect) {
					found = true
				}
			}
			if !found {
				t.Errorf("no error mentioned %q; the mutation missed its check: %v",
					probe.expect, golang.Errors)
			}
		})
	}
}

// TestAnApprovedGateIsInvalidWhenItsApproverPreparedIt states the invariant
// directly rather than by comparison, so that it survives the Python kernel's
// removal -- which is the point of the whole port.
func TestAnApprovedGateIsInvalidWhenItsApproverPreparedIt(t *testing.T) {
	root, manifest := plannedProject(t)
	mutateJSON(t, recordPathIn(root), func(document map[string]any) {
		approveG1(document)
		gate := firstGate(document)
		preparers := listOf(gate["preparers"])
		for _, approval := range gateApprovals(gate) {
			preparers = append(preparers, approval["approver"])
		}
		gate["preparers"] = preparers
	})

	report := goValidateProject(t, root, manifest)
	if report.Valid {
		t.Fatal("a gate approved by the identity that prepared it was called valid")
	}
	if report.Ready {
		t.Error("an invalid project was also reported ready")
	}
}

func goValidateProject(t *testing.T, root, manifest string) ValidationReport {
	t.Helper()
	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatalf("LoadProvider: %v", err)
	}
	overlay, err := LoadOverlay(root)
	if err != nil {
		t.Fatalf("LoadOverlay: %v", err)
	}
	return registry.ValidateProject(root, overlay)
}
