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
	// Try resolving the repository root in order:
	// 1. The caller's project (project-relative, from cwd)
	// 2. The CLI installation (for packaged plugins and checkouts)
	//    FindInstallationRoot already includes CADRE_REPO_ROOT as its first step
	repoRoot, err := platform.RepoRoot()
	if err != nil {
		// platform.RepoRoot() answers "which project is the caller working
		// on", and legitimately has no answer when `cadre` is invoked from
		// a directory that is not a checkout at all -- `cadre select --root
		// <elsewhere>` run from a scratch directory, which
		// test_repository_health.py's symlink case does deliberately.
		// Try self-discovery for packaged plugin installations: find the
		// installation's own roster/ by walking up from the executable or cwd.
		// This also checks CADRE_REPO_ROOT as a first step.
		repoRoot, err = platform.FindInstallationRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre: could not resolve repository root: %s\n", err)
			return 1
		}
	}

	deps := cli.Deps{
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		RepoRoot: repoRoot,
		SDLCDeps: cli.DefaultSDLCDeps(repoRoot),
	}

	return cli.Run(context.Background(), os.Args[1:], deps)
}
