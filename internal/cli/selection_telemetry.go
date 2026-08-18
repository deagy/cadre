package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// SelectionTelemetryCmd is the `cadre selection-telemetry` command:
// summarizes an accumulated, opt-in, local-only `cadre select` telemetry
// JSON-lines file. Recording itself happens as a side effect of `cadre
// select` (see select_agents.go), never here.
func SelectionTelemetryCmd(args []string) int {
	fs := flag.NewFlagSet("cadre selection-telemetry", flag.ContinueOnError)
	setUsage(fs, "selection-telemetry", usageSelectionTelemetry)
	summarizePath := fs.String("summarize", "", "Path to a selection-telemetry JSON-lines file (required)")

	if err := fs.Parse(args); err != nil {
		return parseExitCode(err)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "cadre selection-telemetry: unexpected argument: %s\n", fs.Arg(0))
		return 2
	}
	if *summarizePath == "" {
		fmt.Fprintln(os.Stderr, "cadre selection-telemetry: --summarize is required")
		return 2
	}

	summary, err := orchestration.Summarize(*summarizePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre selection-telemetry: %v\n", err)
		return 1
	}
	fmt.Println(string(data))
	return 0
}
