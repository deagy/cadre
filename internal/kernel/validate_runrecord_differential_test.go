package kernel

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

func dispatchPathIn(root string) string {
	return filepath.Join(root, Overlay, "runs", fixtureTask, "dispatch-plan.json")
}

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
