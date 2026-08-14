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

	// Load routing configuration
	routingPath := filepath.Join(repoRoot, "roster", "orchestration", "routing.json")
	routing, err := orchestration.LoadRouting(routingPath)
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
