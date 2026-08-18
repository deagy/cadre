package enginecli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/engine/agents"
	"github.com/deagy/cadre/cli/internal/engine/executor"
	"github.com/deagy/cadre/cli/internal/engine/runtime"
)

type harness struct {
	deps   Deps
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	root   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	kernelRoot := filepath.Dir(filepath.Dir(filepath.Dir(working)))

	store := executor.NewMemoryCheckpointer()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	return &harness{
		deps: Deps{
			Stdout: stdout, Stderr: stderr, KernelRoot: kernelRoot,
			Prepare: func(request runtime.PlanRequest) (runtime.PlanRequest, error) {
				request.Client = agents.FakeModelClient{}
				request.Checkpointer = store
				return request, nil
			},
		},
		stdout: stdout, stderr: stderr, root: t.TempDir(),
	}
}

func (h *harness) run(argv ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	return Run(argv, h.deps)
}

func (h *harness) json(t *testing.T) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, h.stdout.String())
	}
	return payload
}

func TestPlanStatusResume(t *testing.T) {
	h := newHarness(t)

	if code := h.run("plan", "--root", h.root, "--task-id", "task-1",
		"--task", "refactor the architecture of the billing service"); code != 0 {
		t.Fatalf("plan = %d: %s", code, h.stderr.String())
	}
	if h.json(t)["status"] != "interrupted" {
		t.Errorf("plan status = %v, want interrupted", h.json(t)["status"])
	}

	if code := h.run("status", "--root", h.root, "--task-id", "task-1"); code != 0 {
		t.Fatalf("status = %d: %s", code, h.stderr.String())
	}
	if h.json(t)["status"] != "interrupted" {
		t.Errorf("status = %v", h.json(t)["status"])
	}

	decision := filepath.Join(t.TempDir(), "decision.json")
	if err := os.WriteFile(decision, []byte(`{
	  "status": "approved",
	  "approver": {"id": "product_owner", "role": "Product Owner", "kind": "human"},
	  "evidence_refs": [{"evidence_id":"e1","uri":"https://x/1","hash_algorithm":"sha256","hash":"abc","classification":"internal"}]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("resume", "--root", h.root, "--task-id", "task-1", "--decision", decision); code != 0 {
		t.Fatalf("resume = %d: %s", code, h.stderr.String())
	}
}

// A decision can arrive on stdin, which is how a pipeline supplies one.
func TestResumeReadsADecisionFromStdin(t *testing.T) {
	h := newHarness(t)
	if code := h.run("plan", "--root", h.root, "--task-id", "task-1", "--task", "refactor the architecture"); code != 0 {
		t.Fatalf("plan = %d: %s", code, h.stderr.String())
	}

	h.deps.Stdin = strings.NewReader(`{"status": "pending"}`)
	if code := h.run("resume", "--root", h.root, "--task-id", "task-1", "--decision", "-"); code != 0 {
		t.Fatalf("resume from stdin = %d: %s", code, h.stderr.String())
	}
}

// Export renders a schema-shaped record, and validate agrees with it.
func TestExportThenValidate(t *testing.T) {
	h := newHarness(t)
	if code := h.run("plan", "--root", h.root, "--task-id", "task-1", "--task", "refactor the architecture"); code != 0 {
		t.Fatalf("plan = %d: %s", code, h.stderr.String())
	}

	if code := h.run("export", "--root", h.root, "--task-id", "task-1"); code != 0 {
		t.Fatalf("export = %d: %s", code, h.stderr.String())
	}
	record := h.json(t)
	gates, _ := record["lifecycle_gates"].([]any)
	if len(gates) != 10 {
		t.Fatalf("exported %d gates, want all ten schema slots", len(gates))
	}
	// Ten slots alone proves little: a record with every gate marked
	// not-applicable also has ten. The sequence has to be reflected, or
	// "in scope but not reached" and "out of scope" become the same claim.
	// Ten slots alone proves little, and so does counting applicable gates:
	// a gate the run already reached exports as-is whatever the sequence
	// says. What the sequence governs is the gates *not* yet reached, which
	// must be applicable-and-pending rather than not-applicable -- "in scope,
	// not done" against "out of scope" are opposite claims.
	unreachedInSequence := 0
	for _, entry := range gates {
		gate, _ := entry.(map[string]any)
		if gate["status"] == "pending" && gate["applicability"] == "applicable" {
			unreachedInSequence++
		}
	}
	if unreachedInSequence == 0 {
		t.Error("no gate is applicable-and-pending; the derived sequence is not reflected in the record")
	}

	// Writing to a file must produce the same document.
	output := filepath.Join(t.TempDir(), "record.json")
	if code := h.run("export", "--root", h.root, "--task-id", "task-1", "--output", output); code != 0 {
		t.Fatalf("export --output = %d: %s", code, h.stderr.String())
	}
	written, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("reading the written record: %v", err)
	}
	var fromFile map[string]any
	if err := json.Unmarshal(written, &fromFile); err != nil {
		t.Fatalf("the written record is not JSON: %v", err)
	}

	// A freshly planned run is structurally valid: nothing is approved yet,
	// so no approval invariant can be broken. Asserting the exact code
	// matters -- accepting "0, 1 or 2" would pass whatever validate did,
	// including ignoring its own result.
	if code := h.run("validate", "--root", h.root, "--task-id", "task-1"); code != 0 {
		t.Errorf("validate exited %d for a freshly planned run, want 0: %s", code, h.stderr.String())
	}
}

// Invalidate and reenter refuse to record a change nobody is accountable for.
func TestInvalidateAndReenterRequireReasonAndActor(t *testing.T) {
	h := newHarness(t)
	if code := h.run("plan", "--root", h.root, "--task-id", "task-1", "--task", "refactor the architecture"); code != 0 {
		t.Fatalf("plan = %d: %s", code, h.stderr.String())
	}

	for _, command := range []string{"invalidate", "reenter"} {
		if code := h.run(command, "--root", h.root, "--task-id", "task-1", "--gate", "G1"); code == 0 {
			t.Errorf("%s without --reason or --actor succeeded", command)
		}
		if !strings.Contains(h.stderr.String(), "--reason") {
			t.Errorf("%s did not say what was missing: %s", command, h.stderr.String())
		}
	}

	if code := h.run("invalidate", "--root", h.root, "--task-id", "task-1",
		"--gate", "G1", "--reason", "upstream change", "--actor", "alice"); code != 0 {
		t.Fatalf("invalidate = %d: %s", code, h.stderr.String())
	}
	record := h.json(t)
	if record["actor"] != "alice" || record["reason"] != "upstream change" {
		t.Errorf("record = %v, want the actor and reason recorded", record)
	}
}

func TestMissingFlagsAndUnknownCommands(t *testing.T) {
	h := newHarness(t)

	if code := h.run("plan", "--task-id", "t"); code == 0 {
		t.Error("plan without --root succeeded")
	}
	if code := h.run("status", "--root", h.root); code == 0 {
		t.Error("status without --task-id succeeded")
	}
	if code := h.run("nonsense"); code != 2 {
		t.Errorf("an unknown command exited %d, want 2", code)
	}
	if code := h.run(); code != 2 {
		t.Errorf("no command exited %d, want 2", code)
	}
	if code := h.run("--help"); code != 0 {
		t.Errorf("--help exited %d, want 0", code)
	}
}

// validate exits with its own code, so a caller can tell a defect (1) from a
// decision nobody has made (2).
//
// The reachable non-zero case: authorities start unassigned, so approving a
// gate produces an approved gate whose authority applicability is still
// "unknown" -- structurally valid, blocked on a decision. A test that only
// ever validates a clean record cannot tell whether the exit code is being
// reported or discarded.
func TestValidateReportsABlockerRatherThanSuccess(t *testing.T) {
	h := newHarness(t)
	if code := h.run("plan", "--root", h.root, "--task-id", "task-1", "--task", "refactor the architecture"); code != 0 {
		t.Fatalf("plan = %d: %s", code, h.stderr.String())
	}

	decision := filepath.Join(t.TempDir(), "decision.json")
	if err := os.WriteFile(decision, []byte(`{
	  "status": "approved",
	  "approver": {"id": "product_owner", "role": "Product Owner", "kind": "human"},
	  "evidence_refs": [{"evidence_id":"e1","uri":"https://x/1","hash_algorithm":"sha256","hash":"abc","classification":"internal"}]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("resume", "--root", h.root, "--task-id", "task-1", "--decision", decision); code != 0 {
		t.Fatalf("resume = %d: %s", code, h.stderr.String())
	}

	code := h.run("validate", "--root", h.root, "--task-id", "task-1")
	if code == 0 {
		t.Fatal("an approved gate with an unresolved authority validated clean; " +
			"the exit code is not being reported")
	}
	if code != 2 {
		t.Errorf("validate exited %d, want 2 -- an unresolved authority is a blocker, not a defect: %s",
			code, h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "unresolved authority applicability") {
		t.Errorf("validate did not name the blocker: %s", h.stderr.String())
	}
}

// A dry run is the default: this is the one command that writes somewhere
// other than the project's own .agentic-sdlc directory.
func TestCreateRequirementIssuesDefaultsToADryRun(t *testing.T) {
	h := newHarness(t)
	if code := h.run("plan", "--root", h.root, "--task-id", "task-1", "--task", "refactor the architecture"); code != 0 {
		t.Fatalf("plan = %d: %s", code, h.stderr.String())
	}

	items := filepath.Join(t.TempDir(), "items.json")
	if err := os.WriteFile(items, []byte(`{"schema_version":1,"gate_id":"G2","items":[
	  {"key":"req-1","title":"Add rate limiting","description":"Limit requests per client."}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	code := h.run("create-requirement-issues", "--root", h.root, "--task-id", "task-1",
		"--project", "group/project", "--items", items)
	if code != 0 {
		t.Fatalf("dry run = %d: %s", code, h.stderr.String())
	}
	payload := h.json(t)
	if payload["mode"] != "dry-run" {
		t.Errorf("mode = %v, want dry-run without --apply", payload["mode"])
	}
	if payload["plan_digest"] == nil || payload["plan_digest"] == "" {
		t.Error("a dry run produced no plan digest to apply with")
	}
}

// Applying without naming the expected bot would publish under whoever happens
// to be authenticated.
func TestApplyingRequiresTheBotIdentity(t *testing.T) {
	h := newHarness(t)
	if code := h.run("plan", "--root", h.root, "--task-id", "task-1", "--task", "refactor the architecture"); code != 0 {
		t.Fatalf("plan = %d: %s", code, h.stderr.String())
	}

	items := filepath.Join(t.TempDir(), "items.json")
	if err := os.WriteFile(items, []byte(`{"schema_version":1,"gate_id":"G2","items":[
	  {"key":"req-1","title":"Add rate limiting","description":"Limit requests."}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	code := h.run("create-requirement-issues", "--root", h.root, "--task-id", "task-1",
		"--project", "group/project", "--items", items, "--apply", "--plan-digest", "sha256:x")
	if code == 0 {
		t.Error("an apply without --as-bot succeeded")
	}
	if !strings.Contains(h.stderr.String(), "--as-bot") {
		t.Errorf("the refusal did not name --as-bot: %s", h.stderr.String())
	}
}

// Sanitisation refusals reach the operator rather than being swallowed.
func TestAQuickActionInAnItemIsRefused(t *testing.T) {
	h := newHarness(t)
	if code := h.run("plan", "--root", h.root, "--task-id", "task-1", "--task", "refactor the architecture"); code != 0 {
		t.Fatalf("plan = %d: %s", code, h.stderr.String())
	}

	items := filepath.Join(t.TempDir(), "items.json")
	if err := os.WriteFile(items, []byte(`{"schema_version":1,"gate_id":"G2","items":[
	  {"key":"req-1","title":"Looks fine","description":"/assign @someone"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := h.run("create-requirement-issues", "--root", h.root, "--task-id", "task-1",
		"--project", "group/project", "--items", items); code == 0 {
		t.Error("an item containing a GitLab quick action was planned")
	}
	if !strings.Contains(h.stderr.String(), "quick-action") {
		t.Errorf("the refusal did not explain itself: %s", h.stderr.String())
	}
}

// Listing a task that has published nothing is an empty ledger, not an error.
func TestListingAnUnpublishedTask(t *testing.T) {
	h := newHarness(t)
	if code := h.run("list-requirement-issues", "--root", h.root, "--task-id", "task-1"); code != 0 {
		t.Fatalf("list = %d: %s", code, h.stderr.String())
	}
	payload := h.json(t)
	if payload["count"] != float64(0) {
		t.Errorf("count = %v, want 0", payload["count"])
	}
}

// A refused mutation gate halts the run, and a halted run publishes nothing.
//
// Eligibility is read from the run's own checkpoint rather than taken from a
// flag: whether a run was refused authorisation is a fact about the run, and
// letting a caller assert otherwise would publish requirements from a task
// somebody declined to authorise.
func TestAHaltedRunCannotPublishRequirementIssues(t *testing.T) {
	h := newHarness(t)

	// "deploy to production" is a human-only phrase in the shipped contract,
	// so planning stops for authorisation.
	if code := h.run("plan", "--root", h.root, "--task-id", "task-1",
		"--task", "refactor the architecture then deploy to production"); code != 0 {
		t.Fatalf("plan = %d: %s", code, h.stderr.String())
	}
	if h.json(t)["status"] != "interrupted" {
		t.Fatalf("the mutation gate did not stop the run: %v", h.json(t))
	}

	refusal := filepath.Join(t.TempDir(), "refusal.json")
	if err := os.WriteFile(refusal, []byte(`{"authorized": false, "reason": "not now"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("resume", "--root", h.root, "--task-id", "task-1", "--decision", refusal); code != 0 {
		t.Fatalf("resume = %d: %s", code, h.stderr.String())
	}

	items := filepath.Join(t.TempDir(), "items.json")
	if err := os.WriteFile(items, []byte(`{"schema_version":1,"gate_id":"G2","items":[
	  {"key":"req-1","title":"Add rate limiting","description":"Limit requests."}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	code := h.run("create-requirement-issues", "--root", h.root, "--task-id", "task-1",
		"--project", "group/project", "--items", items)
	if code == 0 {
		t.Fatal("a halted run planned requirement issues")
	}
	// Blocked, not a defect: exit 2, the same distinction validate makes.
	if code != 2 {
		t.Errorf("exit = %d, want 2 for a blocked run: %s", code, h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "halted") {
		t.Errorf("the refusal did not name the halt: %s", h.stderr.String())
	}
}
