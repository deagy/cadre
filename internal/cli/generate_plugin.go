package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deagy/cadre/cli/internal/generators"
	"github.com/deagy/cadre/cli/internal/platform"
)

// GeneratePlugin is the `cadre generate-plugin` command.
func GeneratePlugin(args []string) int {
	fs := flag.NewFlagSet("cadre generate-plugin", flag.ContinueOnError)
	checkMode := fs.Bool("check", false, "Validate without writing (exit 1 if stale)")
	outputFlag := fs.String("output", "", "Output directory for plugin package (required)")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 2
	}

	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "cadre generate-plugin: unexpected argument: %s\n", fs.Arg(0))
		return 2
	}

	// --output is required
	if *outputFlag == "" {
		fmt.Fprintf(os.Stderr, "cadre generate-plugin: --output is required (e.g., --output plugin)\n")
		return 2
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

	// Generate plugin package
	pkg, err := generators.GeneratePlugin(repoRoot, *outputFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 1
	}

	if *checkMode {
		current, staleFiles, err := generators.CheckPluginPackage(pkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
			return 1
		}

		if !current {
			fmt.Fprintf(os.Stderr, "Generated plugin is stale; run cadre generate-plugin:\n")
			for _, file := range staleFiles {
				fmt.Fprintf(os.Stderr, "  %s\n", file)
			}
			return 1
		}

		fmt.Printf("Generated plugin is current: %d files under %s\n", len(pkg.Files), pkg.OutputRoot)
		return 0
	}

	// Write files
	if err := generators.WritePluginFiles(pkg); err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 1
	}

	// Report generated files
	fmt.Printf("Generated plugin: %d files under %s\n", len(pkg.Files), pkg.OutputRoot)
	fmt.Printf("  Skills: %s\n", filepath.Join(pkg.OutputRoot, "skills"))
	fmt.Printf("  Agents: %s\n", filepath.Join(pkg.OutputRoot, "agents"))
	fmt.Printf("  Suite: %s\n", filepath.Join(pkg.OutputRoot, "suite"))
	fmt.Printf("  Provider: %s\n", filepath.Join(pkg.OutputRoot, "provider"))
	fmt.Printf("  Manifests: .codex-plugin/ and .claude-plugin/\n")

	return 0
}
