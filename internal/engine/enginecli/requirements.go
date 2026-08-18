package enginecli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/deagy/cadre/cli/internal/engine/requirementissues"
	"github.com/deagy/cadre/cli/internal/engine/runtime"
)

// cmdCreateRequirementIssues plans or publishes a gate's requirement issues.
//
// Dry run is the default and apply is opt-in, because this is the one command
// in the engine that writes to somewhere other than the project's own
// .agentic-sdlc directory.
func cmdCreateRequirementIssues(argv []string, deps Deps) int {
	fs := flag.NewFlagSet("create-requirement-issues", flag.ContinueOnError)
	root, taskID := commonFlags(fs)
	project := fs.String("project", "", "GitLab project path, e.g. group/project")
	items := fs.String("items", "", "Path to the items JSON file, or - for stdin")
	apply := fs.Bool("apply", false, "Create issues; without this the plan is printed and nothing is written")
	planDigest := fs.String("plan-digest", "", "Required with --apply, from a prior dry run")
	asBot := fs.String("as-bot", "", "GitLab username the credential must authenticate as")
	maxItems := fs.Int("max-items", requirementissues.DefaultMaxItems, "Refuse an items file larger than this")
	breakLock := fs.Bool("break-lock", false, "Explicitly override a held publish lock")
	acknowledgeMock := fs.Bool("i-know-this-is-mocked", false,
		"Permit --apply while the GitLab backend is mocked")
	if !parse(fs, argv, deps) {
		return 2
	}
	if !requireCommon(deps, "create-requirement-issues", *root, *taskID) {
		return 1
	}
	if strings.TrimSpace(*project) == "" || strings.TrimSpace(*items) == "" {
		return deps.fail("cadre create-requirement-issues: --project and --items are required")
	}
	if *apply && strings.TrimSpace(*asBot) == "" {
		// The identity is verified against the live credential before
		// anything is created, so applying without naming the expected bot
		// would publish under whoever happens to be authenticated.
		return deps.fail("cadre create-requirement-issues: --apply requires --as-bot")
	}

	raw, err := requirementissues.ReadItemsSource(*items, deps.Stdin)
	if err != nil {
		return deps.fail("cadre create-requirement-issues: %v", err)
	}

	// The run's own state decides eligibility; it is read here rather than
	// taken from a flag, so an operator cannot assert a run is publishable.
	eligibility, err := eligibilityFor(deps, *root, *taskID)
	if err != nil {
		return deps.fail("cadre create-requirement-issues: %v", err)
	}

	payload, err := requirementissues.Publish(requirementissues.PublishRequest{
		Root: *root, TaskID: *taskID, GateID: "G2", Project: *project,
		ItemsRaw: raw, MaxItems: *maxItems, Apply: *apply, PlanDigest: *planDigest,
		AsBot: *asBot, BreakLock: *breakLock, AcknowledgeMock: *acknowledgeMock,
		Eligibility: eligibility,
	})
	if err != nil {
		// A blocked publish is not a defect: it is a run state the operator
		// has to resolve, and exit 2 says so the way validate does.
		if _, blocked := err.(requirementissues.Blocked); blocked {
			fmt.Fprintf(deps.Stderr, "cadre create-requirement-issues: %v\n", err)
			return 2
		}
		return deps.fail("cadre create-requirement-issues: %v", err)
	}
	if err := deps.printJSON(payload); err != nil {
		return deps.fail("cadre create-requirement-issues: %v", err)
	}
	return 0
}

// cmdListRequirementIssues prints what a task has already published.
func cmdListRequirementIssues(argv []string, deps Deps) int {
	fs := flag.NewFlagSet("list-requirement-issues", flag.ContinueOnError)
	root, taskID := commonFlags(fs)
	if !parse(fs, argv, deps) {
		return 2
	}
	if !requireCommon(deps, "list-requirement-issues", *root, *taskID) {
		return 1
	}

	ledger, err := requirementissues.ReadLedger(*root, *taskID)
	if err != nil {
		return deps.fail("cadre list-requirement-issues: %v", err)
	}
	if err := deps.printJSON(requirementissues.DescribeLedger(ledger)); err != nil {
		return deps.fail("cadre list-requirement-issues: %v", err)
	}
	return 0
}

// eligibilityFor reads a task's own run state.
//
// Taken from the checkpoint rather than from flags: whether a run is halted,
// awaiting re-entry, or sitting on a blocked gate is a fact about the run, and
// letting a caller assert it would let an operator publish requirements from a
// run that was refused.
func eligibilityFor(deps Deps, root, taskID string) (requirementissues.Eligibility, error) {
	var eligibility requirementissues.Eligibility

	request, err := deps.prepare(runtime.PlanRequest{Root: root, TaskID: taskID})
	if err != nil {
		return eligibility, err
	}
	engine, _, err := runtime.ExecutorForTask(request)
	if err != nil {
		return eligibility, err
	}
	checkpoint, found, err := engine.Checkpointer.Load(taskID)
	if err != nil {
		return eligibility, err
	}
	if !found {
		// A planned but unstarted run has no gate state; nothing is halted and
		// nothing is blocked.
		return eligibility, nil
	}

	eligibility.RunHalted = checkpoint.State.RunHalted
	eligibility.ReEntryCount = len(checkpoint.State.ReEntryHistory)
	if gate, present := checkpoint.State.LifecycleGates["G2"]; present {
		eligibility.GateStatus = gate.Status
		if gate.RequiredReentryGate != nil {
			eligibility.RequiredReentryGate = *gate.RequiredReentryGate
		}
	}
	return eligibility, nil
}
