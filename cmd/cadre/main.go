// Command cadre is the Go entry point for the Cadre CLI dispatcher,
// ported from bin/cadre.py per ADR-001-CLI-GO-REFACTOR.md and
// CADRE_CLI_GO_ARCHITECTURE.md. Kept intentionally thin: all routing,
// version detection, and SDLC delegation logic lives in internal/cli, so it
// stays independently testable without spawning a real process.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/deagy/cadre/cli/internal/cli"
	"github.com/deagy/cadre/cli/internal/platform"
)

func main() {
	os.Exit(run())
}

func run() int {
	repoRoot, err := platform.RepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: could not resolve repository root: %s\n", err)
		return 1
	}

	deps := cli.Deps{
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		RepoRoot: repoRoot,
		SDLCDeps: cli.DefaultSDLCDeps(repoRoot),
	}

	return cli.Run(context.Background(), os.Args[1:], deps)
}
