package kernel

import (
	"fmt"
	"io"
	"strings"
)

// Run dispatches one kernel CLI invocation.
//
// Only `show-contract` is ported so far. Everything else returns the
// not-implemented exit code rather than silently succeeding: a kernel that
// exits 0 for a subcommand it does not implement would report gate approvals
// that never happened.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: agentic-sdlc <subcommand> [args...]")
		return 2
	}

	switch args[0] {
	case "show-contract":
		return showContractCmd(args[1:], stdout, stderr)
	case "-h", "--help":
		_, _ = fmt.Fprintln(stdout, "usage: agentic-sdlc <subcommand> [args...]")
		_, _ = fmt.Fprintln(stdout, "\nSubcommands ported to Go so far:")
		_, _ = fmt.Fprintln(stdout, "  show-contract <name>   Print a bundled lifecycle contract as JSON")
		return 0
	}

	// Deliberately not "unknown subcommand": during the port, a subcommand
	// this binary does not implement is one the Python kernel still does,
	// and saying so points at the actual fix.
	_, _ = fmt.Fprintf(stderr,
		"agentic-sdlc: %q is not ported to the Go kernel yet; use the Python kernel for it\n", args[0])
	return 2
}

func showContractCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintf(stderr, "usage: agentic-sdlc show-contract {%s}\n",
			strings.Join(ContractNames, ","))
		return 2
	}
	contract, err := ShowContract(args[0])
	if err != nil {
		// Exit 2 for a bad argument, matching argparse's own convention --
		// the Python kernel reserves other codes for real failures, and a
		// caller distinguishing "I asked wrongly" from "the kernel is broken"
		// depends on that.
		_, _ = fmt.Fprintf(stderr, "agentic-sdlc show-contract: %v\n", err)
		return 2
	}
	_, _ = fmt.Fprint(stdout, contract)
	return 0
}
