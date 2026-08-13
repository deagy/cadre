package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// mockSubprocessRunner is a placeholder for subprocess-based agent execution.
// In production, this would spawn actual agent processes (Python scripts, Go binaries, etc.).
type mockSubprocessRunner struct{}

func (m *mockSubprocessRunner) RunAgent(ctx context.Context, agentID string, task string, plan *orchestration.DispatchPlan) (*orchestration.AgentResult, error) {
	// Placeholder: agents run, but produce no results (skipped status)
	return &orchestration.AgentResult{
		AgentID:     agentID,
		Status:      "skipped",
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
	}, nil
}

// ExecuteCmd handles the `cadre execute` command.
// Execute runs a complete orchestration workflow: route selection → dispatch planning →
// knowledge retrieval → agent execution → result reporting.
func ExecuteCmd(ctx context.Context, argv []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cadre execute", flag.ContinueOnError)
	fs.SetOutput(stderr)

	// Required flags
	var taskID string
	var task string
	var classification string

	// Optional flags
	var files flagStringList
	var outputFormat string
	var outputPath string
	var routingPath string
	var checkMode bool

	fs.StringVar(&taskID, "task-id", "", "Task identifier (required)")
	fs.StringVar(&task, "task", "", "Task description (required)")
	fs.StringVar(&classification, "classification", "internal", "Task classification (internal, medium, high, critical)")
	fs.Var(&files, "files", "Changed files (comma-separated, can be repeated)")
	fs.StringVar(&outputFormat, "output", "text", "Output format (json, markdown, text, summary)")
	fs.StringVar(&outputPath, "output-path", "", "Write output to file instead of stdout")
	fs.StringVar(&routingPath, "routing", "", "Path to routing.json (default: repo_root/roster/orchestration/routing.json)")
	fs.BoolVar(&checkMode, "check", false, "Check mode: validate plan without executing agents")

	if err := fs.Parse(argv); err != nil {
		_, _ = fmt.Fprintf(stderr, "cadre execute: %v\n", err)
		return 2
	}

	// Validate required flags
	if taskID == "" {
		_, _ = fmt.Fprintf(stderr, "cadre execute: --task-id is required\n")
		return 2
	}

	if task == "" {
		_, _ = fmt.Fprintf(stderr, "cadre execute: --task is required\n")
		return 2
	}

	// Validate classification
	validClassifications := map[string]bool{
		"internal": true,
		"medium":   true,
		"high":     true,
		"critical": true,
	}
	if !validClassifications[classification] {
		_, _ = fmt.Fprintf(stderr, "cadre execute: invalid classification %q (must be internal, medium, high, or critical)\n", classification)
		return 2
	}

	// Validate output format
	validFormats := map[string]bool{
		"json":     true,
		"markdown": true,
		"text":     true,
		"summary":  true,
	}
	if !validFormats[outputFormat] {
		_, _ = fmt.Fprintf(stderr, "cadre execute: invalid output format %q (must be json, markdown, text, or summary)\n", outputFormat)
		return 2
	}

	// Load routing configuration
	if routingPath == "" {
		// Try to locate routing.json in the repository
		repoRoot, err := findRepositoryRoot()
		if err == nil {
			routingPath = filepath.Join(repoRoot, "roster", "orchestration", "routing.json")
		}
	}

	routing, err := orchestration.LoadRouting(routingPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cadre execute: failed to load routing: %v\n", err)
		return 1
	}

	// Create executor with mock agent runner (placeholder for full agent spawning)
	mockRunner := &mockSubprocessRunner{}
	executor := orchestration.NewExecutor(mockRunner, 4, 0)
	if executor == nil {
		_, _ = fmt.Fprintf(stderr, "cadre execute: failed to create executor\n")
		return 1
	}

	// Create workflow
	workflow := orchestration.NewOrchestrationWorkflow(routing, executor, nil)

	// Build workflow input
	input := &orchestration.WorkflowInput{
		TaskID:         taskID,
		Task:           task,
		ChangedFiles:   files,
		Classification: classification,
	}

	// Execute workflow
	output, err := workflow.Execute(ctx, input)
	if err != nil {
		// Some errors are expected (needs-triage, no-agents-selected)
		// These are still valid outputs that should be reported
		if output != nil {
			_, _ = fmt.Fprintf(stderr, "cadre execute: %s\n", output.Error)
		} else {
			_, _ = fmt.Fprintf(stderr, "cadre execute: %v\n", err)
			return 1
		}
	}

	// In check mode, just output the dispatch plan without executing
	if checkMode {
		if output.DispatchPlan == nil {
			_, _ = fmt.Fprintf(stderr, "cadre execute: no dispatch plan available\n")
			return 1
		}

		planJSON, err := json.MarshalIndent(output.DispatchPlan, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "cadre execute: failed to serialize plan: %v\n", err)
			return 1
		}

		return writeOutput(stdout, outputPath, string(planJSON), stderr)
	}

	// Format output
	formattedOutput, err := workflow.FormatOutput(outputFormat)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cadre execute: failed to format output: %v\n", err)
		return 1
	}

	// Write output
	return writeOutput(stdout, outputPath, formattedOutput, stderr)
}

// writeOutput writes output to stdout or a file, returning the exit code.
func writeOutput(stdout io.Writer, outputPath string, content string, stderr io.Writer) int {
	w := stdout

	if outputPath != "" {
		f, err := os.Create(outputPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "cadre execute: failed to create output file: %v\n", err)
			return 1
		}
		defer f.Close() // nolint:errcheck
		w = f
	}

	if _, err := io.WriteString(w, content); err != nil {
		_, _ = fmt.Fprintf(stderr, "cadre execute: failed to write output: %v\n", err)
		return 1
	}

	return 0
}

// findRepositoryRoot locates the repository root by looking for .git directory.
func findRepositoryRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	current := cwd
	for {
		gitPath := filepath.Join(current, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return current, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Reached filesystem root
			return "", fmt.Errorf("repository root not found")
		}
		current = parent
	}
}

// flagStringList is a flag.Value for repeated string flags.
type flagStringList []string

func (f *flagStringList) String() string {
	return strings.Join(*f, ",")
}

func (f *flagStringList) Set(value string) error {
	*f = append(*f, value)
	return nil
}
