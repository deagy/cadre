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
	case "detect":
		return detectCmd(args[1:], stdout, stderr)
	case "-h", "--help":
		_, _ = fmt.Fprintln(stdout, "usage: agentic-sdlc <subcommand> [args...]")
		_, _ = fmt.Fprintln(stdout, "\nSubcommands ported to Go so far:")
		_, _ = fmt.Fprintln(stdout, "  show-contract <name>   Print a bundled lifecycle contract as JSON")
		_, _ = fmt.Fprintln(stdout, "  detect [--root ROOT]   Report what a repository looks like, changing nothing")
		return 0
	}

	// Deliberately not "unknown subcommand": during the port, a subcommand
	// this binary does not implement is one the Python kernel still does,
	// and saying so points at the actual fix.
	_, _ = fmt.Fprintf(stderr,
		"agentic-sdlc: %q is not ported to the Go kernel yet; use the Python kernel for it\n", args[0])
	return 2
}

func detectCmd(args []string, stdout, stderr io.Writer) int {
	root := "."
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "--root" && index+1 < len(args):
			index++
			root = args[index]
		case strings.HasPrefix(args[index], "--root="):
			root = strings.TrimPrefix(args[index], "--root=")
		default:
			_, _ = fmt.Fprintf(stderr, "usage: agentic-sdlc detect [--root ROOT]\n")
			return 2
		}
	}

	rendered, err := RenderDetection(DetectRepository(root))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "agentic-sdlc detect: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprint(stdout, rendered)
	return 0
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
