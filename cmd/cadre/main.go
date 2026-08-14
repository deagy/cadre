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
		// platform.RepoRoot() answers "which project is the caller working
		// on", and legitimately has no answer when `cadre` is invoked from
		// a directory that is not a checkout at all -- `cadre select --root
		// <elsewhere>` run from a scratch directory, which
		// test_repository_health.py's symlink case does deliberately.
		// $CADRE_REPO_ROOT, exported by bin/cadre and bin/cadre.ps1, names
		// the checkout that produced this binary and is the right fallback:
		// it is where this CLI's own resources live, and it is never
		// consulted while the working-directory walk succeeds, so it cannot
		// redirect a caller who is inside a project.
		repoRoot = os.Getenv("CADRE_REPO_ROOT")
		if repoRoot == "" {
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
