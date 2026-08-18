package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deagy/cadre/cli/internal/generators"
	"github.com/deagy/cadre/cli/internal/platform"
)

const usagePortClineAgents = "[--root <dir>] [--source <dir>]"

// PortClineAgentsCmd is `cadre port-cline-agents`: render the packaged
// plugin's agents and skills into the Cline preset/skill formats.
//
// Replaces `python3 plugin/tools/port_cline_agents.py`, the last step of the
// documented regeneration sequence that needed an interpreter.
func PortClineAgentsCmd(args []string) int {
	fs := flag.NewFlagSet("cadre port-cline-agents", flag.ContinueOnError)
	setUsage(fs, "port-cline-agents", usagePortClineAgents)
	root := fs.String("root", "cline-plugins", "directory containing cline-agents/ (the port target)")
	source := fs.String("source", "", "directory containing the generated agents/ and skills/ (defaults to --root)")
	if err := fs.Parse(args); err != nil {
		return parseExitCode(err)
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: cadre port-cline-agents "+usagePortClineAgents)
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
	absSource := ""
	if *source != "" {
		if absSource, err = filepath.Abs(*source); err != nil {
			fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
			return 1
		}
	}

	agents, skills, err := generators.PortClineAgents(repoRoot, absRoot, absSource)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %v\n", err)
		return 1
	}
	fmt.Printf("Ported %d agent(s) and %d skill(s) into cline-agents/.\n", len(agents), len(skills))
	return 0
}
