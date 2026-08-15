// Command agentic-sdlc is the Go lifecycle kernel CLI.
//
// A separate binary from `cadre`, deliberately. The kernel owns lifecycle
// gate schemas, run-record validation and gate-authority semantics; roster/
// asks and the kernel answers. Shipping them as one binary would make an
// in-process shortcut across that boundary a one-line change.
package main

import (
	"os"

	"github.com/deagy/cadre/cli/internal/kernel"
)

func main() {
	os.Exit(kernel.Run(os.Args[1:], os.Stdout, os.Stderr))
}
