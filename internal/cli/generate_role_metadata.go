package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deagy/cadre/cli/internal/generators"
	"github.com/deagy/cadre/cli/internal/platform"
)

// GenerateRoleMetadata is the `cadre generate-role-metadata` command.
func GenerateRoleMetadata(args []string) int {
	fs := flag.NewFlagSet("cadre generate-role-metadata", flag.ContinueOnError)
	checkMode := fs.Bool("check", false, "Report whether files are current without writing anything (exit 1 if stale)")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 2
	}

	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "cadre generate-role-metadata: unexpected argument: %s\n", fs.Arg(0))
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

	// Generate role metadata files
	generated, err := generators.GenerateRoleMetadata(repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 1
	}

	if *checkMode {
		current, staleFiles, err := generators.CheckRoleMetadata(repoRoot, generated)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
			return 1
		}

		if !current {
			fmt.Fprintf(os.Stderr, "Role metadata files are stale; run cadre generate-role-metadata:\n")
			for _, file := range staleFiles {
				fmt.Fprintf(os.Stderr, "  %s\n", file)
			}
			return 1
		}

		// Count generated files for report
		fileCount := 2 + len(generated.CodexWrappers) + 1 // catalog.yaml + agent-catalog.json + wrappers + routing.json
		fmt.Printf("%d role metadata files are current\n", fileCount)
		return 0
	}

	// Write files
	if err := generators.WriteRoleMetadataFiles(repoRoot, generated); err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 1
	}

	// Report generated files
	wrapperCount := len(generated.CodexWrappers)
	fmt.Printf("Generated role metadata:\n")
	fmt.Printf("  catalog.yaml: %s\n", filepath.Join(repoRoot, "roster", "catalog.yaml"))
	fmt.Printf("  agent-catalog.json: %s\n", filepath.Join(repoRoot, "provider", "agent-catalog.json"))
	fmt.Printf("  Codex wrappers: %d role .toml files under %s\n", wrapperCount, filepath.Join(repoRoot, "provider", "wrappers"))
	fmt.Printf("  routing.json: updated with knowledge_focus\n")
	return 0
}
