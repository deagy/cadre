package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deagy/cadre/cli/internal/generators"
	"github.com/deagy/cadre/cli/internal/platform"
)

// GenerateAuthorityAides is the `cadre generate-authority-aides` command.
func GenerateAuthorityAides(args []string) int {
	fs := flag.NewFlagSet("cadre generate-authority-aides", flag.ContinueOnError)
	setUsage(fs, "generate-authority-aides", usageGenerateAuthorityAide)
	checkMode := fs.Bool("check", false, "Report whether files are current without writing anything (exit 1 if stale)")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 2
	}

	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "cadre generate-authority-aides: unexpected argument: %s\n", fs.Arg(0))
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

	authorityRoot := filepath.Join(repoRoot, "roster", "authority")
	aidesPath := filepath.Join(authorityRoot, "aides.yaml")
	templatePath := filepath.Join(authorityRoot, "_template.md.tmpl")

	// Generate all aides
	generated, err := generators.GenerateAuthorityAides(authorityRoot, aidesPath, templatePath, generators.GeneratedMarker)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 1
	}

	if *checkMode {
		current, staleFiles, err := generators.CheckAides(authorityRoot, generated)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
			return 1
		}

		if !current {
			fmt.Fprintf(os.Stderr, "Authority-aide AGENT.md files are stale; run cadre generate-authority-aides: %v\n",
				staleFiles)
			return 1
		}

		fmt.Printf("%d authority-aide AGENT.md files are current\n", len(generated))
		return 0
	}

	// Write files
	if err := generators.WriteAideFiles(generated); err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 1
	}

	fmt.Printf("Generated %d authority-aide AGENT.md files under %s\n", len(generated), authorityRoot)
	return 0
}
