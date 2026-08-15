package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/deagy/cadre/cli/internal/generators"
	"github.com/deagy/cadre/cli/internal/platform"
)

// GeneratePlugin is the `cadre generate-plugin` command.
//
// --output is required rather than defaulting anywhere: a default would
// silently create a stray directory. In this repository it is always
// `--output plugin`.
func GeneratePlugin(args []string) int {
	fs := flag.NewFlagSet("cadre generate-plugin", flag.ContinueOnError)
	checkMode := fs.Bool("check", false, "Validate without writing (exit 1 if stale)")
	outputFlag := fs.String("output", "", "Output directory for plugin package (required)")
	forceReadme := fs.Bool("force-readme", false,
		"Overwrite an existing downstream package's own README.md with the register's template")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 2
	}

	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "cadre generate-plugin: unexpected argument: %s\n", fs.Arg(0))
		return 2
	}

	if *outputFlag == "" {
		fmt.Fprintf(os.Stderr,
			"cadre generate-plugin: --output is required. The packaged plugin lives in "+
				"this repository's plugin/ directory, e.g.\n    cadre generate-plugin --output plugin\n")
		return 2
	}

	repoRoot, err := platform.FindInstallationRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot find installation root: %v\n", err)
		return 1
	}

	// Generating a distribution requires the full source tree, including the
	// files an installed copy does not carry. From an install it produces a
	// package silently missing them rather than failing.
	if !*checkMode && !requireCheckout("generate-plugin", repoRoot) {
		return 2
	}

	result, err := generators.RunGeneratePlugin(repoRoot, *outputFlag, generators.GeneratePluginOptions{
		Check:       *checkMode,
		ForceReadme: *forceReadme,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 1
	}

	if *checkMode {
		if !result.Current {
			fmt.Fprintln(os.Stderr, "Generated plugin is stale or non-deterministic; run cadre generate-plugin")
			for _, difference := range result.Differences {
				fmt.Fprintf(os.Stderr, "  %s\n", difference)
			}
			return 1
		}
		fmt.Printf("Generated plugin is current under %s\n", result.OutputRoot)
		return 0
	}

	fmt.Printf("Generated %d self-contained files under %s\n", len(result.Written), result.OutputRoot)
	if !result.WroteReadme {
		fmt.Fprintf(os.Stderr,
			"README.md left untouched: %s/.codex-plugin/plugin.json already exists, so %s is treated "+
				"as an already-initialized downstream package that owns its own README.md. Pass "+
				"--force-readme to overwrite it with the register's own template instead.\n",
			result.OutputRoot, result.OutputRoot)
	}
	return 0
}
