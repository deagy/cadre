package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deagy/cadre/cli/internal/generators"
	"github.com/deagy/cadre/cli/internal/platform"
)

// PortHermesSkillsCmd is `cadre port-hermes-skills`: render the roster into
// the skill tree the Hermes agent loads, under <root>/skills/cadre/.
//
// Reads roster/ in the checkout, not the packaged plugin, so it has no
// --source and no place in the generate-plugin ordering; it is checkout-only
// for the same reason generate-plugin is.
func PortHermesSkillsCmd(args []string) int {
	fs := flag.NewFlagSet("cadre port-hermes-skills", flag.ContinueOnError)
	setUsage(fs, "port-hermes-skills", usagePortHermesSkills)
	root := fs.String("root", "hermes-plugins", "directory that receives skills/cadre/ (the port target)")
	if err := fs.Parse(args); err != nil {
		return parseExitCode(err)
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: cadre port-hermes-skills "+usagePortHermesSkills)
		return 2
	}

	repoRoot, err := platform.FindInstallationRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot find the installation root: %v\n", err)
		return 1
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 1
	}

	roles, workflows, err := generators.PortHermesSkills(repoRoot, absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 1
	}
	fmt.Printf("Ported %d role(s), %d workflow(s) and the orchestrator into skills/cadre/.\n", len(roles), len(workflows))
	return 0
}
