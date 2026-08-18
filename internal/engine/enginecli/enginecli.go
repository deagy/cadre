// Package enginecli is the engine's command-line surface.
//
// Ported from engine/agentic_sdlc_langgraph/cli.py. Thin by design: every
// command routes through internal/engine/runtime, the same entry points the
// HTTP service uses, so the two surfaces describe a run identically rather
// than each formatting its own answer.
package enginecli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/deagy/cadre/cli/internal/engine/contracts"
	"github.com/deagy/cadre/cli/internal/engine/executor"
	"github.com/deagy/cadre/cli/internal/engine/export"
	"github.com/deagy/cadre/cli/internal/engine/runtime"
	"github.com/deagy/cadre/cli/internal/engine/validate"
)

// Deps are the process facilities a command uses, injectable for tests.
type Deps struct {
	Stdout     io.Writer
	Stderr     io.Writer
	Stdin      io.Reader
	KernelRoot string

	// Prepare lets a test substitute the model client and checkpoint store.
	Prepare func(runtime.PlanRequest) (runtime.PlanRequest, error)
}

func (d Deps) prepare(request runtime.PlanRequest) (runtime.PlanRequest, error) {
	request.KernelRoot = d.KernelRoot
	if d.Prepare == nil {
		return request, nil
	}
	return d.Prepare(request)
}

func (d Deps) printJSON(payload any) error {
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(d.Stdout, string(encoded))
	return err
}

func (d Deps) fail(format string, args ...any) int {
	fmt.Fprintf(d.Stderr, format+"\n", args...)
	return 1
}

// Run dispatches one engine subcommand.
func Run(argv []string, deps Deps) int {
	if len(argv) == 0 {
		fmt.Fprintln(deps.Stderr, usage)
		return 2
	}
	switch argv[0] {
	case "plan":
		return cmdPlan(argv[1:], deps)
	case "resume":
		return cmdResume(argv[1:], deps)
	case "status":
		return cmdStatus(argv[1:], deps)
	case "invalidate":
		return cmdInvalidate(argv[1:], deps)
	case "reenter":
		return cmdReenter(argv[1:], deps)
	case "export":
		return cmdExport(argv[1:], deps)
	case "validate":
		return cmdValidate(argv[1:], deps)
	case "create-requirement-issues":
		return cmdCreateRequirementIssues(argv[1:], deps)
	case "list-requirement-issues":
		return cmdListRequirementIssues(argv[1:], deps)
	case "-h", "--help", "help":
		fmt.Fprintln(deps.Stdout, usage)
		return 0
	default:
		fmt.Fprintf(deps.Stderr, "unknown command %q\n\n%s\n", argv[0], usage)
		return 2
	}
}

const usage = `Usage: agentic-sdlc-engine <command> [options]

Commands:
  plan        Plan a task: derive its gate sequence and run to the first stop
  resume      Resume an interrupted task with a decision
  status      Print a task's current gate and interrupt status
  invalidate  Invalidate a gate and every gate after it
  reenter     Reset a gate and every gate after it so they run again
  export      Print the run record for a task
  validate    Validate a task's run record against the kernel schema

  create-requirement-issues  Plan or publish a gate's requirements as GitLab issues
  list-requirement-issues    Print what a task has already published`

// commonFlags are the two every command needs.
func commonFlags(fs *flag.FlagSet) (root, taskID *string) {
	return fs.String("root", "", "Project root holding .agentic-sdlc/"),
		fs.String("task-id", "", "Task identifier")
}

func requireCommon(deps Deps, command, root, taskID string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(taskID) == "" {
		deps.fail("cadre %s: --root and --task-id are required", command)
		return false
	}
	return true
}

func parse(fs *flag.FlagSet, argv []string, deps Deps) bool {
	fs.SetOutput(deps.Stderr)
	return fs.Parse(argv) == nil
}

func cmdPlan(argv []string, deps Deps) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	root, taskID := commonFlags(fs)
	task := fs.String("task", "", "The task text (required for a new task)")
	profile := fs.String("profile", "generic", "Profile id")
	ignored := fs.String("ignored-gates", "", "Comma-separated gate ids, e.g. G4,G5")
	provider := fs.String("provider", "", "Path to a provider manifest")
	classification := fs.String("classification", "internal", "Task classification")
	intentIssue := fs.String("intent-gitlab-issue", "", "GitLab issue reference for G1, <project>#<iid>")
	requirementsIssue := fs.String("requirements-gitlab-issue", "", "GitLab issue reference for G2")
	if !parse(fs, argv, deps) {
		return 2
	}
	if !requireCommon(deps, "plan", *root, *taskID) {
		return 1
	}

	request, err := deps.prepare(runtime.PlanRequest{
		Root: *root, TaskID: *taskID, TaskText: *task, ProfileID: *profile,
		ProviderManifest: *provider, IgnoredGateIDs: splitGates(*ignored),
	})
	if err != nil {
		return deps.fail("cadre plan: %v", err)
	}

	payload, err := runtime.CreateOrReconnectTask(runtime.TaskRequest{
		PlanRequest: request, Classification: *classification,
		IntentRecordID: *intentIssue, RequirementsBaselineID: *requirementsIssue,
	})
	if err != nil {
		return deps.fail("cadre plan: %v", err)
	}
	if err := deps.printJSON(payload); err != nil {
		return deps.fail("cadre plan: %v", err)
	}
	return 0
}

func cmdResume(argv []string, deps Deps) int {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	root, taskID := commonFlags(fs)
	decisionPath := fs.String("decision", "", "Path to a JSON decision file, or - for stdin")
	if !parse(fs, argv, deps) {
		return 2
	}
	if !requireCommon(deps, "resume", *root, *taskID) {
		return 1
	}
	if strings.TrimSpace(*decisionPath) == "" {
		return deps.fail("cadre resume: --decision is required")
	}

	decision, err := loadDecision(*decisionPath, deps.Stdin)
	if err != nil {
		return deps.fail("cadre resume: %v", err)
	}

	request, err := deps.prepare(runtime.PlanRequest{Root: *root, TaskID: *taskID})
	if err != nil {
		return deps.fail("cadre resume: %v", err)
	}
	payload, err := runtime.ResumeTask(request, decision)
	if err != nil {
		return deps.fail("cadre resume: %v", err)
	}
	if err := deps.printJSON(payload); err != nil {
		return deps.fail("cadre resume: %v", err)
	}
	return 0
}

func cmdStatus(argv []string, deps Deps) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	root, taskID := commonFlags(fs)
	if !parse(fs, argv, deps) {
		return 2
	}
	if !requireCommon(deps, "status", *root, *taskID) {
		return 1
	}

	request, err := deps.prepare(runtime.PlanRequest{Root: *root, TaskID: *taskID})
	if err != nil {
		return deps.fail("cadre status: %v", err)
	}
	payload, err := runtime.TaskStatus(request)
	if err != nil {
		return deps.fail("cadre status: %v", err)
	}
	if err := deps.printJSON(payload); err != nil {
		return deps.fail("cadre status: %v", err)
	}
	return 0
}

func cmdInvalidate(argv []string, deps Deps) int {
	return reentryCommand("invalidate", argv, deps,
		func(engine *executor.Executor, taskID, gate, reason, actor string) (any, error) {
			return engine.Invalidate(taskID, gate, reason, actor)
		})
}

func cmdReenter(argv []string, deps Deps) int {
	return reentryCommand("reenter", argv, deps,
		func(engine *executor.Executor, taskID, gate, reason, actor string) (any, error) {
			return engine.Reenter(taskID, gate, reason, actor)
		})
}

func reentryCommand(name string, argv []string, deps Deps,
	apply func(*executor.Executor, string, string, string, string) (any, error)) int {

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	root, taskID := commonFlags(fs)
	gate := fs.String("gate", "", "Earliest gate id affected, e.g. G2")
	reason := fs.String("reason", "", "Why")
	actor := fs.String("actor", "", "Who")
	if !parse(fs, argv, deps) {
		return 2
	}
	if !requireCommon(deps, name, *root, *taskID) {
		return 1
	}
	// Reason and actor are required, not defaulted. Both land in the run
	// record's history, and a record saying a gate was invalidated by nobody
	// for no reason is worse than one that was never written.
	if strings.TrimSpace(*gate) == "" || strings.TrimSpace(*reason) == "" || strings.TrimSpace(*actor) == "" {
		return deps.fail("cadre %s: --gate, --reason and --actor are required", name)
	}

	request, err := deps.prepare(runtime.PlanRequest{Root: *root, TaskID: *taskID})
	if err != nil {
		return deps.fail("cadre %s: %v", name, err)
	}
	engine, _, err := runtime.ExecutorForTask(request)
	if err != nil {
		return deps.fail("cadre %s: %v", name, err)
	}
	record, err := apply(engine, *taskID, *gate, *reason, *actor)
	if err != nil {
		return deps.fail("cadre %s: %v", name, err)
	}
	if err := deps.printJSON(record); err != nil {
		return deps.fail("cadre %s: %v", name, err)
	}
	return 0
}

// runRecordFor rebuilds a task and renders its run record.
func runRecordFor(deps Deps, root, taskID string) (map[string]any, error) {
	request, err := deps.prepare(runtime.PlanRequest{Root: root, TaskID: taskID})
	if err != nil {
		return nil, err
	}
	engine, metadata, err := runtime.ExecutorForTask(request)
	if err != nil {
		return nil, err
	}
	checkpoint, _, err := engine.Checkpointer.Load(taskID)
	if err != nil {
		return nil, err
	}

	gates := map[string]any{}
	for gateID, gate := range checkpoint.State.LifecycleGates {
		encoded, err := json.Marshal(gate)
		if err != nil {
			return nil, err
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return nil, err
		}
		gates[gateID] = decoded
	}

	stateForExport := map[string]any{
		"task_id":         checkpoint.State.TaskID,
		"classification":  checkpoint.State.Classification,
		"scope":           checkpoint.State.Scope,
		"lifecycle_gates": gates,
	}
	if checkpoint.State.IntentRecordID != nil {
		stateForExport["intent_record_id"] = *checkpoint.State.IntentRecordID
	}
	if checkpoint.State.RequirementsBaselineID != nil {
		stateForExport["requirements_baseline_id"] = *checkpoint.State.RequirementsBaselineID
	}

	record := export.RunRecord(stateForExport, export.Options{
		SequenceGateIDs: metadata.GateSequenceIDs,
		IgnoredGateIDs:  metadata.IgnoredGateIDs,
	})

	// Round-trip through JSON before returning. The record is built with Go
	// types -- []string among them -- and the schema validator walks decoded
	// JSON, so it rejects a []string with "invalid jsonType" before checking
	// anything the schema actually says. That made `validate` fail on every
	// record, with an error about the encoding rather than the content.
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	var plain map[string]any
	if err := json.Unmarshal(encoded, &plain); err != nil {
		return nil, err
	}
	return plain, nil
}

func cmdExport(argv []string, deps Deps) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	root, taskID := commonFlags(fs)
	output := fs.String("output", "", "Write to this path instead of stdout")
	if !parse(fs, argv, deps) {
		return 2
	}
	if !requireCommon(deps, "export", *root, *taskID) {
		return 1
	}

	record, err := runRecordFor(deps, *root, *taskID)
	if err != nil {
		return deps.fail("cadre export: %v", err)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return deps.fail("cadre export: %v", err)
	}
	if *output != "" {
		if err := os.WriteFile(*output, append(encoded, '\n'), 0o644); err != nil {
			return deps.fail("cadre export: %v", err)
		}
		return 0
	}
	fmt.Fprintln(deps.Stdout, string(encoded))
	return 0
}

// cmdValidate exits with the validator's own code, so a caller can tell a
// defect (1) from a decision nobody has made (2).
func cmdValidate(argv []string, deps Deps) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	root, taskID := commonFlags(fs)
	if !parse(fs, argv, deps) {
		return 2
	}
	if !requireCommon(deps, "validate", *root, *taskID) {
		return 1
	}

	record, err := runRecordFor(deps, *root, *taskID)
	if err != nil {
		return deps.fail("cadre validate: %v", err)
	}

	contractsDir := filepath.Join(deps.KernelRoot, "kernel", "contracts")
	schema, err := jsonschema.Compile(filepath.Join(contractsDir, "run-record.schema.json"))
	if err != nil {
		return deps.fail("cadre validate: %v", err)
	}
	allGates, err := contracts.LoadLifecycleGates(filepath.Join(contractsDir, "lifecycle-gates.json"))
	if err != nil {
		return deps.fail("cadre validate: %v", err)
	}
	gateContracts := map[string]contracts.Gate{}
	for _, gate := range allGates {
		gateContracts[gate.ID] = gate
	}

	code, messages := validate.RunRecord(record, schema, gateContracts)
	for _, message := range messages {
		fmt.Fprintln(deps.Stderr, message)
	}
	if code == 0 {
		fmt.Fprintln(deps.Stdout, "run record is valid")
	}
	return code
}

func splitGates(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var gates []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			gates = append(gates, trimmed)
		}
	}
	return gates
}

// loadDecision reads a decision from a file, or from stdin for "-".
func loadDecision(path string, stdin io.Reader) (map[string]any, error) {
	var raw []byte
	var err error
	if path == "-" {
		if stdin == nil {
			return nil, errors.New("no stdin to read the decision from")
		}
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var decision map[string]any
	if err := json.Unmarshal(raw, &decision); err != nil {
		return nil, fmt.Errorf("decision is not a JSON object: %w", err)
	}
	return decision, nil
}
