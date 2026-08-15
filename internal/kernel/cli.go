package kernel

import (
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
	case "plan":
		return planCmd(registry, args[1:], stdout, stderr)
	case "decide":
		return decideCmd(registry, args[1:], stdout, stderr)
	case "status":
		return statusCmd(registry, args[1:], stdout, stderr)
	case "invalidate":
		return recordSurgeryCmd(registry, args[0], args[1:], stdout, stderr)
	case "reenter":
		return recordSurgeryCmd(registry, args[0], args[1:], stdout, stderr)
	case "upgrade":
		return upgradeCmd(registry, args[1:], stdout, stderr)
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
		_, _ = fmt.Fprintln(stdout, "  plan --task-id ID --task TEXT         Create a dispatch plan and pending run record")
		_, _ = fmt.Fprintln(stdout, "  decide --task-id ID --gate G --role R --decision D --actor-id A --evidence-uri U")
		_, _ = fmt.Fprintln(stdout, "  status --task-id ID                   Show a task's gate state (and advance it)")
		_, _ = fmt.Fprintln(stdout, "  invalidate --task-id ID --earliest-gate G --reason R --actor A")
		_, _ = fmt.Fprintln(stdout, "  reenter --task-id ID --earliest-gate G --reason R --actor A")
		_, _ = fmt.Fprintln(stdout, "  upgrade (--check | --apply)            Check or apply a kernel lock upgrade")
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
//
// Rendered rather than marshalled: encoding/json escapes <, > and & inside
// strings, and several of these messages tell an operator to "rerun with
// --provider <manifest>". Marshalling turned that into \u003cmanifest\u003e,
// which is still valid JSON and still unreadable in a terminal -- caught by
// comparing stderr with the Python kernel's.
func jsonError(err error) string {
	rendered := RenderIndented(ordered("error", err.Error()))
	return strings.TrimSuffix(rendered, "\n")
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
		return printJSON(registry.Providers, stdout)
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
			return printJSON(provider, stdout)
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
	return printJSON(sorted, stdout)
}

// printJSON writes a value as the Python kernel's print(json.dumps(value,
// indent=2)) does. RenderIndented rather than encoding/json: Go escapes <, >
// and & and leaves non-ASCII raw, and Python does the exact opposite of both.
func printJSON(value any, stdout io.Writer) int {
	_, _ = fmt.Fprint(stdout, RenderIndented(value))
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
// a trailing newline, <, > and & unescaped, and non-ASCII escaped. A finding
// naming a path is the common case for all three.
func printReport(stdout io.Writer, report ValidationReport) {
	_, _ = fmt.Fprint(stdout, RenderIndented(report))
}

// planCmd answers `plan`: build a task's dispatch plan and run record.
//
// The plan is printed and both documents are written. Printing the plan is
// what a caller pipes into `cadre select`-adjacent tooling; writing it is what
// makes the task exist as far as every other subcommand is concerned.
func planCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := PlanRequest{Root: "."}
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "--root" && index+1 < len(args):
			index++
			request.Root = args[index]
		case strings.HasPrefix(args[index], "--root="):
			request.Root = strings.TrimPrefix(args[index], "--root=")
		case args[index] == "--task-id" && index+1 < len(args):
			index++
			request.TaskID = args[index]
		case strings.HasPrefix(args[index], "--task-id="):
			request.TaskID = strings.TrimPrefix(args[index], "--task-id=")
		case args[index] == "--task" && index+1 < len(args):
			index++
			request.Task = args[index]
		case strings.HasPrefix(args[index], "--task="):
			request.Task = strings.TrimPrefix(args[index], "--task=")
		default:
			_, _ = fmt.Fprintln(stderr,
				"usage: agentic-sdlc plan [--root ROOT] --task-id TASK_ID --task TASK")
			return 2
		}
	}
	var missing []string
	if request.TaskID == "" {
		missing = append(missing, "--task-id")
	}
	if request.Task == "" {
		missing = append(missing, "--task")
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc plan: error: the following arguments are required: %s\n",
			strings.Join(missing, ", "))
		return 2
	}

	result, err := registry.Plan(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result.Dispatch))
	return 0
}

// decideCmd answers `decide`: record one human decision on one gate.
//
// The flag names and the choice sets mirror the Python parser's, including
// which values --gate and --role accept: an invalid choice is a usage error
// (exit 2), not a decision the kernel tries and then refuses.
func decideCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := DecideRequest{Root: "."}
	fields := map[string]*string{
		"--root": &request.Root, "--task-id": &request.TaskID, "--gate": &request.GateID,
		"--role": &request.AuthorityRole, "--decision": &request.Decision,
		"--actor-id": &request.ActorID, "--evidence-uri": &request.EvidenceURI,
		"--note": &request.Note, "--decided-at": &request.DecidedAt,
	}
	if code := parseFlags("decide", args, fields, stderr); code != 0 {
		return code
	}

	var missing []string
	for _, required := range []struct {
		name  string
		value string
	}{
		{"--task-id", request.TaskID}, {"--gate", request.GateID},
		{"--role", request.AuthorityRole}, {"--decision", request.Decision},
		{"--actor-id", request.ActorID}, {"--evidence-uri", request.EvidenceURI},
	} {
		if required.value == "" {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc decide: error: the following arguments are required: %s\n",
			strings.Join(missing, ", "))
		return 2
	}
	if code := rejectInvalidChoice(stderr, "decide", "--gate", request.GateID, GateIDs); code != 0 {
		return code
	}
	if code := rejectInvalidChoice(stderr, "decide", "--role", request.AuthorityRole,
		sortedAuthorityRoles()); code != 0 {
		return code
	}
	if code := rejectInvalidChoice(stderr, "decide", "--decision", request.Decision,
		[]string{"approved", "rejected", "request-changes"}); code != 0 {
		return code
	}

	result, err := registry.Decide(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	return 0
}

// sortedAuthorityRoles is the --role choice set, in the order argparse prints
// it: the Python parser builds it from sorted(AUTHORITY_ROLES).
func sortedAuthorityRoles() []string {
	roles := make([]string, 0, len(AuthorityRoles))
	for role := range AuthorityRoles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

// recordSurgeryCmd answers `invalidate` and `reenter`, which take the same
// four arguments and differ only in what they do with them.
func recordSurgeryCmd(
	registry *Registry, name string, args []string, stdout, stderr io.Writer,
) int {
	request := RecordSurgeryRequest{Root: "."}
	fields := map[string]*string{
		"--root": &request.Root, "--task-id": &request.TaskID,
		"--earliest-gate": &request.EarliestGate, "--reason": &request.Reason,
		"--actor": &request.Actor,
	}
	if code := parseFlags(name, args, fields, stderr); code != 0 {
		return code
	}

	var missing []string
	for _, required := range []struct{ name, value string }{
		{"--task-id", request.TaskID}, {"--earliest-gate", request.EarliestGate},
		{"--reason", request.Reason}, {"--actor", request.Actor},
	} {
		if required.value == "" {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc %s: error: the following arguments are required: %s\n",
			name, strings.Join(missing, ", "))
		return 2
	}
	if code := rejectInvalidChoice(stderr, name, "--earliest-gate",
		request.EarliestGate, GateIDs); code != 0 {
		return code
	}

	var result *orderedObject
	var err error
	if name == "invalidate" {
		result, err = registry.Invalidate(request)
	} else {
		result, err = registry.Reenter(request)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	return 0
}

// upgradeCmd answers `upgrade`. The two modes are mutually exclusive and one
// is required, matching the Python parser: an `upgrade` with neither is
// ambiguous about whether it was meant to write.
func upgradeCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	root, check, apply := ".", false, false
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "--root" && index+1 < len(args):
			index++
			root = args[index]
		case strings.HasPrefix(args[index], "--root="):
			root = strings.TrimPrefix(args[index], "--root=")
		case args[index] == "--check":
			check = true
		case args[index] == "--apply":
			apply = true
		default:
			_, _ = fmt.Fprintln(stderr,
				"usage: agentic-sdlc upgrade [--root ROOT] (--check | --apply)")
			return 2
		}
	}
	if check == apply {
		_, _ = fmt.Fprintln(stderr,
			"agentic-sdlc upgrade: error: exactly one of --check or --apply is required")
		return 2
	}

	result, err := registry.Upgrade(root, apply)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	return 0
}

// parseFlags reads `--name value` and `--name=value` into the named targets.
func parseFlags(command string, args []string, fields map[string]*string, stderr io.Writer) int {
	for index := 0; index < len(args); index++ {
		name, value, inline := strings.Cut(args[index], "=")
		target, known := fields[name]
		if !known {
			_, _ = fmt.Fprintf(stderr, "agentic-sdlc %s: unknown argument %q\n", command, args[index])
			return 2
		}
		if inline {
			*target = value
			continue
		}
		if index+1 >= len(args) {
			_, _ = fmt.Fprintf(stderr, "agentic-sdlc %s: %s needs a value\n", command, name)
			return 2
		}
		index++
		*target = args[index]
	}
	return 0
}

func rejectInvalidChoice(stderr io.Writer, command, flag, value string, allowed []string) int {
	for _, candidate := range allowed {
		if candidate == value {
			return 0
		}
	}
	_, _ = fmt.Fprintf(stderr,
		"agentic-sdlc %s: error: argument %s: invalid choice: %q (choose from %s)\n",
		command, flag, value, strings.Join(allowed, ", "))
	return 2
}

// statusCmd answers `status`, which despite its name writes -- see status.go.
func statusCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	root, taskID := ".", ""
	fields := map[string]*string{"--root": &root, "--task-id": &taskID}
	if code := parseFlags("status", args, fields, stderr); code != 0 {
		return code
	}
	if taskID == "" {
		_, _ = fmt.Fprintln(stderr,
			"agentic-sdlc status: error: the following arguments are required: --task-id")
		return 2
	}

	result, err := registry.Status(root, taskID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	return 0
}
