package kernel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
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

	// --provider is a global flag, consumed before the subcommand, exactly as
	// the Python kernel's top-level parser does. Providers load in the order
	// given: dependency checks and duplicate detection both compare against
	// what came before.
	registry := NewRegistry()
	rest := args
	for len(rest) > 0 && (rest[0] == "--provider" || strings.HasPrefix(rest[0], "--provider=")) {
		var manifest string
		if rest[0] == "--provider" {
			if len(rest) < 2 {
				_, _ = fmt.Fprintln(stderr, "agentic-sdlc: --provider needs a manifest path")
				return 2
			}
			manifest, rest = rest[1], rest[2:]
		} else {
			manifest, rest = strings.TrimPrefix(rest[0], "--provider="), rest[1:]
		}
		if err := registry.LoadProvider(manifest); err != nil {
			// Exit 1, not 2: the request was well formed and the provider was
			// refused on its merits, which is a different thing from a usage
			// error and is what a caller needs to tell apart.
			_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
			return 1
		}
	}
	if len(rest) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: agentic-sdlc <subcommand> [args...]")
		return 2
	}
	args = rest

	switch args[0] {
	case "provider", "profile", "extension":
		return introspectionCmd(registry, args, stdout, stderr)
	case "show-contract":
		return showContractCmd(args[1:], stdout, stderr)
	case "detect":
		return detectCmd(args[1:], stdout, stderr)
	case "validate":
		return validateCmd(registry, args[1:], stdout, stderr)
	case "list-gate-issues":
		return listLedgerCmd(args[0], args[1:], stdout, stderr, ReadGateIssuesLedger)
	case "list-github-gate-issues":
		return listLedgerCmd(args[0], args[1:], stdout, stderr, ReadGitHubGateIssuesLedger)
	case "list-gate-status":
		return listLedgerCmd(args[0], args[1:], stdout, stderr, ReadGateStatusLedgers)
	case "list-reviewer-nudge":
		return listLedgerCmd(args[0], args[1:], stdout, stderr, ReadReviewerNudgeLedger)
	case "-h", "--help":
		_, _ = fmt.Fprintln(stdout, "usage: agentic-sdlc <subcommand> [args...]")
		_, _ = fmt.Fprintln(stdout, "\nSubcommands ported to Go so far:")
		_, _ = fmt.Fprintln(stdout, "  show-contract <name>   Print a bundled lifecycle contract as JSON")
		_, _ = fmt.Fprintln(stdout, "  detect [--root ROOT]   Report what a repository looks like, changing nothing")
		_, _ = fmt.Fprintln(stdout, "  validate [--root ROOT] Check a project's configuration and run records")
		_, _ = fmt.Fprintln(stdout, "  list-gate-issues --task-id ID         Print the GitLab gate-issues ledger")
		_, _ = fmt.Fprintln(stdout, "  list-github-gate-issues --task-id ID  Print the GitHub gate-issues ledger")
		_, _ = fmt.Fprintln(stdout, "  list-gate-status --task-id ID         Print both forges' gate-status ledgers")
		_, _ = fmt.Fprintln(stdout, "  list-reviewer-nudge --task-id ID      Print the reviewer-nudge ledger")
		return 0
	}

	// Deliberately not "unknown subcommand": during the port, a subcommand
	// this binary does not implement is one the Python kernel still does,
	// and saying so points at the actual fix.
	_, _ = fmt.Fprintf(stderr,
		"agentic-sdlc: %q is not ported to the Go kernel yet; use the Python kernel for it\n", args[0])
	return 2
}

// jsonError matches the Python kernel's top-level error document, which is
// what a caller parsing stderr expects to find there.
func jsonError(err error) string {
	encoded, marshalErr := json.MarshalIndent(map[string]string{"error": err.Error()}, "", "  ")
	if marshalErr != nil {
		return `{"error": "` + err.Error() + `"}`
	}
	return string(encoded)
}

// introspectionCmd answers `provider`, `profile` and `extension`: what this
// kernel invocation was told about, and nothing more.
//
// The shape is the Python parser's: every kind takes an `action`, which is
// {list,inspect} for provider and {list} for the other two, and provider
// alone takes an optional provider_id. Modelled wrong on the first attempt --
// `provider <id>` without an action -- and the differential caught it, which
// is what it is for.
func introspectionCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	kind, rest := args[0], args[1:]

	// --root is accepted and ignored, as the Python kernel does (argparse
	// SUPPRESS): introspection reports loaded state, which no directory
	// changes. Accepting it keeps a caller's invocation working; acting on it
	// would imply a project scope these subcommands do not have.
	var positional []string
	for index := 0; index < len(rest); index++ {
		switch {
		case rest[index] == "--root" && index+1 < len(rest):
			index++
		case strings.HasPrefix(rest[index], "--root="):
		case strings.HasPrefix(rest[index], "-"):
			_, _ = fmt.Fprintf(stderr, "agentic-sdlc %s: unknown argument %q\n", kind, rest[index])
			return 2
		default:
			positional = append(positional, rest[index])
		}
	}

	allowed := map[string]bool{"list": true}
	if kind == "provider" {
		allowed["inspect"] = true
	}
	if len(positional) == 0 || !allowed[positional[0]] {
		choices := "list"
		if kind == "provider" {
			choices = "list, inspect"
		}
		got := ""
		if len(positional) > 0 {
			got = fmt.Sprintf(" %q", positional[0])
		}
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc %s: error: argument action: invalid choice%s (choose from %s)\n",
			kind, got, choices)
		return 2
	}
	action := positional[0]

	switch kind {
	case "profile":
		return printSortedIDs(registry.ProfileIDs(), stdout, stderr)
	case "extension":
		return printSortedIDs(registry.ExtensionIDs(), stdout, stderr)
	}

	if action == "list" {
		return printJSON(registry.Providers, stdout, stderr)
	}
	if len(positional) < 2 {
		// argparse makes provider_id optional, so `provider inspect` with no
		// id reaches the handler and looks for a provider named None.
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(fmt.Errorf("unknown loaded provider: None")))
		return 1
	}
	wanted := positional[1]
	for _, provider := range registry.Providers {
		if provider.ID == wanted {
			return printJSON(provider, stdout, stderr)
		}
	}
	_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(fmt.Errorf("unknown loaded provider: %s", wanted)))
	return 1
}

func printSortedIDs(ids map[string]bool, stdout, stderr io.Writer) int {
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	return printJSON(sorted, stdout, stderr)
}

func printJSON(value any, stdout, stderr io.Writer) int {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_, _ = fmt.Fprintf(stderr, "agentic-sdlc: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprint(stdout, buffer.String())
	return 0
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

// validateCmd answers `validate`: is this project coherent, and is it ready?
//
// Three exit codes, and the middle one is the point: 0 valid and ready, 2
// valid but blocked on a decision no automation may make, 1 invalid. A caller
// that treats non-zero as failure still stops on a blocker, and one that reads
// the code can tell "somebody needs to decide something" apart from "this
// project contradicts itself".
func validateCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	root := "."
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "--root" && index+1 < len(args):
			index++
			root = args[index]
		case strings.HasPrefix(args[index], "--root="):
			root = strings.TrimPrefix(args[index], "--root=")
		default:
			_, _ = fmt.Fprintf(stderr, "usage: agentic-sdlc validate [--root ROOT]\n")
			return 2
		}
	}

	resolved, err := resolveExisting(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "agentic-sdlc validate: %v\n", err)
		return 1
	}

	// An unreadable overlay is reported in the same document shape as any
	// other finding rather than as a crash: a caller parsing this output
	// should not need a second code path for "the project could not be read".
	overlay, err := LoadOverlay(resolved)
	if err != nil {
		printReport(stdout, ValidationReport{
			Valid: false, Ready: false, Errors: []string{err.Error()}, Blockers: []string{},
		})
		return 1
	}

	report := registry.ValidateProject(resolved, overlay)
	printReport(stdout, report)
	switch {
	case len(report.Errors) > 0:
		return 1
	case len(report.Blockers) > 0:
		return 2
	default:
		return 0
	}
}

// printReport writes the report as the Python kernel does: two-space indent,
// a trailing newline, and no HTML escaping. Go escapes <, > and & inside
// strings by default, which would mangle any finding quoting a path or a
// comparison operator.
func printReport(stdout io.Writer, report ValidationReport) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		_, _ = fmt.Fprintf(stdout, `{"valid": false, "ready": false, "errors": ["%v"], "blockers": []}`, err)
		return
	}
	_, _ = stdout.Write(buffer.Bytes())
}
