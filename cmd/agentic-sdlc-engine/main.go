// Command agentic-sdlc-engine drives a task through the G1-G10 lifecycle.
//
// The Go replacement for engine/agentic_sdlc_langgraph's CLI. Thin: every
// subcommand routes through internal/engine/runtime, the same entry points the
// HTTP service uses.
package main

import (
	"fmt"
	"os"

	"github.com/deagy/cadre/cli/internal/engine/enginecli"
	"github.com/deagy/cadre/cli/internal/platform"
)

func main() {
	os.Exit(run())
}

func run() int {
	// The kernel contracts live in the installation, not in whatever project
	// the caller is standing in -- a task's gates are defined by the kernel it
	// runs under, not by the repository it is about.
	kernelRoot, err := platform.FindInstallationRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"agentic-sdlc-engine: cannot locate the kernel contracts: %s\n", err)
		return 1
	}

	return enginecli.Run(os.Args[1:], enginecli.Deps{
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Stdin:      os.Stdin,
		KernelRoot: kernelRoot,
	})
}
