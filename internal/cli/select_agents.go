package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deagy/cadre/cli/internal/orchestration"
	"github.com/deagy/cadre/cli/internal/platform"
)

// SelectAgents is the `cadre select` command.
// Determines which agents should handle a task based on routing rules.
func SelectAgents(args []string) int {
	fs := flag.NewFlagSet("cadre select", flag.ContinueOnError)
	taskFlag := fs.String("task", "", "Task description (required)")
	filesFlag := fs.String("files", "", "Comma-separated list of changed files")
	classificationFlag := fs.String("classification", "internal", "Task classification (internal, medium, high, critical)")
	taskIDFlag := fs.String("task-id", "", "Task identifier (required)")
	outputFlag := fs.String("output", "text", "Output format: text or json")
	checkFlag := fs.Bool("check", false, "Validate without executing")
	recordTelemetryFlag := fs.Bool("record-telemetry", false, "Append an opt-in, local-only outcome record to the selection telemetry log")
	recordTelemetryIncludeTaskFlag := fs.Bool("record-telemetry-include-task", false, "Additionally record the raw task text and changed files (off even when --record-telemetry is on)")
	telemetryPathFlag := fs.String("telemetry-path", "", "Override the selection telemetry file path (default: .agents/orchestration/selection-telemetry.jsonl)")
	overlayFlag := fs.String("overlay", "", "Explicit routing overlay file path, bypassing walk-up discovery of .agents/orchestration/routing-overlay.json")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 2
	}

	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "cadre select: unexpected argument: %s\n", fs.Arg(0))
		return 2
	}

	// Validate required flags
	if *taskFlag == "" {
		fmt.Fprintf(os.Stderr, "cadre select: --task is required\n")
		return 2
	}

	if *taskIDFlag == "" {
		fmt.Fprintf(os.Stderr, "cadre select: --task-id is required\n")
		return 2
	}

	// Parse changed files
	var files []string
	if *filesFlag != "" {
		files = strings.Split(*filesFlag, ",")
		for i := range files {
			files[i] = strings.TrimSpace(files[i])
		}
	}

	// Find repository root
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot get working directory: %v\n", err)
		return 1
	}
	repoRoot, err := platform.FindProjectRoot(wd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot find repository root: %v\n", err)
		return 1
	}

	// Load routing configuration, applying a project-local overlay
	// (.agents/orchestration/routing-overlay.json, discovered by walking up
	// from cwd, or an explicit --overlay path) if one is present. With no
	// overlay, this resolves to the base routing.json unchanged.
	routingPath := filepath.Join(repoRoot, "roster", "orchestration", "routing.json")
	effectiveRoutingMap, resolvedOverlayPath, overlayApplied, err := orchestration.ResolveEffectiveRouting(routingPath, wd, *overlayFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot resolve routing overlay: %v\n", err)
		return 1
	}
	routing, err := orchestration.EffectiveRoutingConfig(effectiveRoutingMap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot load routing: %v\n", err)
		return 1
	}

	// Match task to routes
	matches, err := orchestration.MatchTaskToRoutes(*taskFlag, files, *classificationFlag, routing)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: no routes matched for task: %v\n", err)
		return 1
	}

	// Build dispatch plan
	plan, err := orchestration.BuildDispatchPlan(
		*taskIDFlag,
		*taskFlag,
		files,
		*classificationFlag,
		matches,
		routing,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot build dispatch plan: %v\n", err)
		return 1
	}

	// Validate dispatch result
	if plan.DispatchDisposition.Status == "no-agents-selected" {
		fmt.Fprintf(os.Stderr, "cadre: no agents selected for this task\n")
		return 1
	}

	// Bind the plan to the exact catalog.yaml/routing.json content (and,
	// best-effort, git commit) that produced it. Best-effort by design here
	// (unlike the Python original, where catalog.yaml is already a
	// mandatory read): this Go select pipeline does not otherwise require
	// catalog.yaml to exist, so a missing catalog degrades to an absent
	// Provenance field rather than failing the whole command.
	catalogPath := filepath.Join(repoRoot, "roster", "catalog.yaml")
	provOpts := orchestration.BuildProvenanceOptions{}
	if overlayApplied {
		provOpts.OverlayPath = resolvedOverlayPath
	}
	if prov, err := orchestration.BuildProvenance(catalogPath, routingPath, provOpts); err == nil {
		plan.Provenance = prov
	}

	// Record opt-in, local-only selection telemetry as a side effect. Never
	// runs unless explicitly enabled via --record-telemetry or
	// CADRE_SELECTION_TELEMETRY=1 -- see telemetry.go's package doc. This
	// never changes stdout/--output JSON above; it is a side effect only.
	if orchestration.IsEnabled(*recordTelemetryFlag) {
		includeTask := orchestration.IncludeTaskEnabled(*recordTelemetryIncludeTaskFlag)
		if _, err := orchestration.RecordSelection(plan, repoRoot, *telemetryPathFlag, includeTask); err != nil {
			fmt.Fprintf(os.Stderr, "cadre select: failed to record telemetry: %v\n", err)
			return 1
		}
	}

	// Output result
	if *checkFlag {
		// In check mode, just validate that we can produce a plan
		fmt.Printf("✓ Plan is valid for task %q (classification: %s)\n", *taskFlag, *classificationFlag)
		return 0
	}

	// Format and output plan
	switch *outputFlag {
	case "json":
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre: cannot marshal plan: %v\n", err)
			return 1
		}
		fmt.Println(string(data))

	case "text":
		fmt.Print(plan.PlanText())

	default:
		fmt.Fprintf(os.Stderr, "cadre select: unknown output format: %s\n", *outputFlag)
		return 2
	}

	return 0
}
