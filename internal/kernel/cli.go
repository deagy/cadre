package kernel

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// globalPrefix is the arguments before the subcommand.
//
// Only these are the top-level parser's: `detect --version` is an unknown
// argument to the `detect` subparser in Python, not a version request, and it
// has to stay one here.
func globalPrefix(args []string) []string {
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "--provider":
			index++
		case strings.HasPrefix(args[index], "--provider="), strings.HasPrefix(args[index], "-"):
		default:
			return args[:index]
		}
	}
	return args
}

// Run dispatches one kernel CLI invocation.
//
// Every subcommand the Python parser declares is answered here. The
// not-implemented branch at the bottom is kept for the reverse case -- a
// subcommand added to the Python kernel and not here -- because a kernel that
// exits 0 for something it does not implement would report gate approvals
// that never happened.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: agentic-sdlc <subcommand> [args...]")
		return 2
	}

	// --version before any provider is loaded, because argparse answers it
	// during parsing: `--provider nonsense.json --version` prints the version
	// and exits 0 rather than failing on the manifest.
	for _, arg := range globalPrefix(args) {
		if arg == "--version" {
			_, _ = fmt.Fprintln(stdout, Version)
			return 0
		}
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
	case "create-gate-issues":
		return createGateIssuesCmd(registry, args[1:], stdout, stderr)
	case "create-github-gate-issues":
		return createGithubGateIssuesCmd(registry, args[1:], stdout, stderr)
	case "approve-from-github":
		return approveFromGithubCmd(registry, args[1:], stdout, stderr)
	case "approve-from-github-pr":
		return approveFromGithubPRCmd(registry, args[1:], stdout, stderr)
	case "approve-from-gitlab":
		return approveFromGitlabCmd(registry, args[1:], stdout, stderr)
	case "approve-from-gitlab-mr":
		return approveFromGitlabMRCmd(registry, args[1:], stdout, stderr)
	case "link-intent-from-gitlab-issue":
		return linkSourceIssueCmd(registry, args[0], "G1", ForgeGitLab, args[1:], stdout, stderr)
	case "link-requirements-from-gitlab-issue":
		return linkSourceIssueCmd(registry, args[0], "G2", ForgeGitLab, args[1:], stdout, stderr)
	case "link-intent-from-github-issue":
		return linkSourceIssueCmd(registry, args[0], "G1", ForgeGitHub, args[1:], stdout, stderr)
	case "link-requirements-from-github-issue":
		return linkSourceIssueCmd(registry, args[0], "G2", ForgeGitHub, args[1:], stdout, stderr)
	case "publish-reviewer-nudge":
		return publishReviewerNudgeCmd(registry, args[1:], stdout, stderr)
	case "publish-gate-status":
		return publishGateStatusCmd(registry, args[1:], stdout, stderr)
	case "request-gate-reviewers-gitlab":
		return requestGateReviewersGitLabCmd(registry, args[1:], stdout, stderr)
	case "request-gate-reviewers":
		return requestGateReviewersCmd(registry, args[1:], stdout, stderr)
	case "repair":
		return repairCmd(registry, args[1:], stdout, stderr)
	case "init":
		return initCmd(registry, args[1:], stdout, stderr)
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
		_, _ = fmt.Fprintln(stdout, "  create-gate-issues --task-id ID --project-path P --as-bot B [--apply]")
		_, _ = fmt.Fprintln(stdout, "  create-github-gate-issues --task-id ID --repo R --as-bot B [--apply]")
		_, _ = fmt.Fprintln(stdout, "  approve-from-github --task-id ID --gate G --role R --repo R --pr N --review-id N --reviewer-login L --commit-sha S")
		_, _ = fmt.Fprintln(stdout, "  approve-from-github-pr --task-id ID --gate G --role R --repo R --pr N")
		_, _ = fmt.Fprintln(stdout, "  approve-from-gitlab --task-id ID --gate G --role R --project-path P --mr-iid N --approval-id A --approver-username U --commit-sha S")
		_, _ = fmt.Fprintln(stdout, "  approve-from-gitlab-mr --task-id ID --gate G --role R --project-path P --mr-iid N")
		_, _ = fmt.Fprintln(stdout, "  link-intent-from-gitlab-issue / link-requirements-from-gitlab-issue --task-id ID --role R --project-path P --issue-iid N")
		_, _ = fmt.Fprintln(stdout, "  link-intent-from-github-issue / link-requirements-from-github-issue --task-id ID --role R --repo R --issue-number N")
		_, _ = fmt.Fprintln(stdout, "  publish-gate-status --task-id ID --forge F --as-bot B [--apply]")
		_, _ = fmt.Fprintln(stdout, "  publish-reviewer-nudge --task-id ID --repo R --pr N --as-bot B [--apply]")
		_, _ = fmt.Fprintln(stdout, "  request-gate-reviewers --task-id ID --repo R --pr N --as-bot B")
		_, _ = fmt.Fprintln(stdout, "  request-gate-reviewers-gitlab --task-id ID --project-path P --mr-iid N --as-bot B")
		_, _ = fmt.Fprintln(stdout, "  repair [--runner R] [--apply]         Inspect or safely repair an initialization")
		_, _ = fmt.Fprintln(stdout, "  init [--profile P] [--project-id ID] [--dry-run]  Initialize a project overlay")
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
			got = fmt.Sprintf(" %s", pythonRepr(positional[0]))
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
			if apply {
				return rejectExclusive(stderr, "upgrade", "--check", "--apply")
			}
			check = true
		case args[index] == "--apply":
			if check {
				return rejectExclusive(stderr, "upgrade", "--apply", "--check")
			}
			apply = true
		default:
			// Named, not just refused. Every other command in this CLI says
			// which argument it did not understand, and an operator reading
			// only a usage line has to diff it against what they typed.
			_, _ = fmt.Fprintf(stderr, "agentic-sdlc upgrade: unknown argument %q\n", args[index])
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

// exclusiveFlags refuses two flags from one mutually exclusive group.
//
// argparse names the flag seen *second* as the offender and the one seen first
// as what it conflicts with, so the order the operator typed them in is part
// of the message. Repeating a single flag is fine, in both.
type exclusiveFlags struct{ seen string }

// mark records a flag, returning the conflicting one if there is a clash.
func (e *exclusiveFlags) mark(flag string) (string, bool) {
	if e.seen != "" && e.seen != flag {
		return e.seen, false
	}
	e.seen = flag
	return "", true
}

func rejectExclusive(stderr io.Writer, command, flag, conflict string) int {
	_, _ = fmt.Fprintf(stderr,
		"agentic-sdlc %s: error: argument %s: not allowed with argument %s\n",
		command, flag, conflict)
	return 2
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
		"agentic-sdlc %s: error: argument %s: invalid choice: %s (choose from %s)\n",
		command, flag, pythonRepr(value), strings.Join(allowed, ", "))
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

// initCmd answers `init`.
//
// --repair and --apply are accepted so the argument shape matches the Python
// parser's, and refused with the same message: repair is its own subcommand
// here, and --apply outside it is a request nothing can honour.
func initCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := InitRequest{Root: ".", Classification: "internal", Runner: "both"}
	var extensions []string
	repair, apply, force := false, false, false

	for index := 0; index < len(args); index++ {
		name, value, inline := strings.Cut(args[index], "=")
		takeValue := func() (string, bool) {
			if inline {
				return value, true
			}
			if index+1 >= len(args) {
				_, _ = fmt.Fprintf(stderr, "agentic-sdlc init: %s needs a value\n", name)
				return "", false
			}
			index++
			return args[index], true
		}
		switch name {
		case "--root", "--profile", "--project-id", "--classification", "--runner", "--extension":
			got, ok := takeValue()
			if !ok {
				return 2
			}
			switch name {
			case "--root":
				request.Root = got
			case "--profile":
				request.Profile = got
			case "--project-id":
				request.ProjectID = got
			case "--classification":
				request.Classification = got
			case "--runner":
				request.Runner = got
			case "--extension":
				extensions = append(extensions, got)
			}
		case "--dry-run":
			if force {
				return rejectExclusive(stderr, "init", "--dry-run", "--force")
			}
			request.DryRun = true
		case "--force":
			// Declared by the Python parser and documented there as reserved:
			// init never overwrites, with it or without it. Accepted rather
			// than rejected because a script passing it should not start
			// failing on the day the Go kernel takes over.
			if request.DryRun {
				return rejectExclusive(stderr, "init", "--force", "--dry-run")
			}
			force = true
		case "--repair":
			repair = true
		case "--apply":
			apply = true
		default:
			_, _ = fmt.Fprintf(stderr, "agentic-sdlc init: unknown argument %q\n", args[index])
			return 2
		}
	}
	request.Extensions = extensions
	_ = force // reserved by the Python parser; see --force above

	if repair {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(fmt.Errorf(
			"init --repair is not ported to the Go kernel yet; use `agentic-sdlc repair`")))
		return 1
	}
	if apply {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(fmt.Errorf(
			"--apply is only valid with init --repair")))
		return 1
	}
	if code := rejectInvalidChoice(stderr, "init", "--runner", request.Runner,
		[]string{"codex", "claude", "both"}); code != 0 {
		return code
	}

	result, err := registry.Initialize(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	return 0
}

// repairCmd answers `repair`. Without --apply it plans and writes nothing,
// which is the mode worth being able to run anywhere.
func repairCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := RepairRequest{Root: ".", Runner: "both"}
	for index := 0; index < len(args); index++ {
		name, value, inline := strings.Cut(args[index], "=")
		switch name {
		case "--root", "--runner":
			got := value
			if !inline {
				if index+1 >= len(args) {
					_, _ = fmt.Fprintf(stderr, "agentic-sdlc repair: %s needs a value\n", name)
					return 2
				}
				index++
				got = args[index]
			}
			if name == "--root" {
				request.Root = got
			} else {
				request.Runner = got
			}
		case "--apply":
			request.Apply = true
		default:
			_, _ = fmt.Fprintf(stderr, "agentic-sdlc repair: unknown argument %q\n", args[index])
			return 2
		}
	}
	if code := rejectInvalidChoice(stderr, "repair", "--runner", request.Runner,
		[]string{"codex", "claude", "both"}); code != 0 {
		return code
	}

	result, code, err := registry.Repair(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	return code
}

// requestGateReviewersCmd answers `request-gate-reviewers`.
//
// Reporting only. There is no --apply, and the absence is the feature: see
// gatereviewers.go for why the write capability is not here.
func requestGateReviewersCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := ReviewerRequest{Root: "."}
	var pullRequest, gates string
	fields := map[string]*string{
		"--root": &request.Root, "--task-id": &request.TaskID, "--repo": &request.Repo,
		"--pr": &pullRequest, "--as-bot": &request.AsBot, "--gates": &gates,
		"--allow-classification": &request.AllowClassification,
	}
	if code := parseFlags("request-gate-reviewers", args, fields, stderr); code != 0 {
		return code
	}

	var missing []string
	for _, required := range []struct{ name, value string }{
		{"--task-id", request.TaskID}, {"--repo", request.Repo},
		{"--pr", pullRequest}, {"--as-bot", request.AsBot},
	} {
		if required.value == "" {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc request-gate-reviewers: error: the following arguments are required: %s\n",
			strings.Join(missing, ", "))
		return 2
	}
	number, err := strconv.Atoi(pullRequest)
	if err != nil {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc request-gate-reviewers: error: argument --pr: invalid int value: %s\n",
			pythonRepr(pullRequest))
		return 2
	}
	request.PullRequest = number
	for _, gate := range strings.Split(gates, ",") {
		if trimmed := strings.TrimSpace(gate); trimmed != "" {
			request.Gates = append(request.Gates, trimmed)
		}
	}

	report, err := registry.RequestGateReviewers(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(report))

	// Exit 2 when anything needs a human: a refusal, or a login that cannot be
	// asked. Distinct from 1 -- the report was built and is correct, and what
	// it says is that somebody has to act before these reviewers can be
	// requested. A caller treating non-zero as failure still stops.
	for _, entry := range report.Reviewers {
		if problemClassifications[entry.Classification] {
			return 2
		}
	}
	if len(report.Refusals) > 0 {
		return 2
	}
	return 0
}

// requestGateReviewersGitLabCmd answers `request-gate-reviewers-gitlab`.
func requestGateReviewersGitLabCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := GitLabReviewerRequest{Root: "."}
	var mergeRequest, gates string
	fields := map[string]*string{
		"--root": &request.Root, "--task-id": &request.TaskID,
		"--project-path": &request.ProjectPath, "--mr-iid": &mergeRequest,
		"--as-bot": &request.AsBot, "--gates": &gates,
		"--allow-classification": &request.AllowClassification,
	}
	if code := parseFlags("request-gate-reviewers-gitlab", args, fields, stderr); code != 0 {
		return code
	}

	var missing []string
	for _, required := range []struct{ name, value string }{
		{"--task-id", request.TaskID}, {"--project-path", request.ProjectPath},
		{"--mr-iid", mergeRequest}, {"--as-bot", request.AsBot},
	} {
		if required.value == "" {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc request-gate-reviewers-gitlab: error: the following arguments are required: %s\n",
			strings.Join(missing, ", "))
		return 2
	}
	iid, err := strconv.Atoi(mergeRequest)
	if err != nil {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc request-gate-reviewers-gitlab: error: argument --mr-iid: invalid int value: %s\n",
			pythonRepr(mergeRequest))
		return 2
	}
	request.MergeRequestIID = iid
	for _, gate := range strings.Split(gates, ",") {
		if trimmed := strings.TrimSpace(gate); trimmed != "" {
			request.Gates = append(request.Gates, trimmed)
		}
	}

	report, err := registry.RequestGateReviewersGitLab(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(report))

	// Same mapping as the GitHub report, over this forge's own problem set --
	// the two share "withheld-conflict" and differ in the rest.
	for _, entry := range report.Reviewers {
		if gitlabProblemClassifications[entry.Classification] {
			return 2
		}
	}
	if len(report.Refusals) > 0 {
		return 2
	}
	return 0
}

// publishGateStatusCmd answers `publish-gate-status`.
//
// Three exit codes, and the middle one carries the distinction: 0 done or
// nothing to do, 2 blocked and needing a human, 1 the command could not run.
func publishGateStatusCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := GateStatusRequest{Root: "."}
	var pullRequest, mergeRequest string
	fields := map[string]*string{
		"--root": &request.Root, "--task-id": &request.TaskID, "--forge": &request.Forge,
		"--as-bot": &request.AsBot, "--allow-classification": &request.AllowClassification,
		"--repo": &request.Repo, "--pr": &pullRequest,
		"--project-path": &request.ProjectPath, "--mr-iid": &mergeRequest,
	}
	flags := map[string]*bool{
		// --dry-run is the explicit spelling of the default, so it has
		// somewhere to land and nothing to change. --apply is the flag that
		// makes a run do anything, and the group below is what stops an
		// operator asking for both.
		"--dry-run": new(bool),
		"--apply":   &request.Apply, "--break-lock": &request.BreakLock,
		"--i-know-this-is-mocked": &request.KnowinglyMocked,
	}
	if code := parseFlagsWithGroups("publish-gate-status", args, fields, flags,
		[][]string{{"--dry-run", "--apply"}}, stderr); code != 0 {
		return code
	}

	var missing []string
	for _, required := range []struct{ name, value string }{
		{"--task-id", request.TaskID}, {"--forge", request.Forge}, {"--as-bot", request.AsBot},
	} {
		if required.value == "" {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc publish-gate-status: error: the following arguments are required: %s\n",
			strings.Join(missing, ", "))
		return 2
	}
	if code := rejectInvalidChoice(stderr, "publish-gate-status", "--forge", request.Forge,
		[]string{ForgeGitHub, ForgeGitLab}); code != 0 {
		return code
	}
	for _, numeric := range []struct {
		name  string
		text  string
		value *int
	}{
		{"--pr", pullRequest, &request.PullRequest},
		{"--mr-iid", mergeRequest, &request.MergeRequestIID},
	} {
		if numeric.text == "" {
			continue
		}
		parsed, err := strconv.Atoi(numeric.text)
		if err != nil {
			_, _ = fmt.Fprintf(stderr,
				"agentic-sdlc publish-gate-status: error: argument %s: invalid int value: %s\n",
				numeric.name, pythonRepr(numeric.text))
			return 2
		}
		*numeric.value = parsed
	}

	summary, err := registry.PublishGateStatus(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		var blocked *GateStatusBlocked
		if errors.As(err, &blocked) {
			return 2
		}
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(summary))
	return 0
}

// parseFlagsWithGroups reads `--name value`, `--name=value` and bare switches,
// refusing two flags from the same mutually exclusive group.
//
// Groups may be nil for a command that declares none.
func parseFlagsWithGroups(
	command string, args []string, fields map[string]*string, switches map[string]*bool,
	groups [][]string, stderr io.Writer,
) int {
	// One tracker per group, indexed by every flag in it, so a flag's group
	// is found by the name the operator typed.
	trackers := map[string]*exclusiveFlags{}
	for _, group := range groups {
		tracker := &exclusiveFlags{}
		for _, flag := range group {
			trackers[flag] = tracker
		}
	}

	for index := 0; index < len(args); index++ {
		name, value, inline := strings.Cut(args[index], "=")
		if tracker, grouped := trackers[name]; grouped {
			if conflict, ok := tracker.mark(name); !ok {
				return rejectExclusive(stderr, command, name, conflict)
			}
		}
		if flag, isSwitch := switches[name]; isSwitch {
			*flag = true
			continue
		}
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

// publishReviewerNudgeCmd answers `publish-reviewer-nudge`.
func publishReviewerNudgeCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := ReviewerNudgeRequest{Root: "."}
	var pullRequest, gates string
	fields := map[string]*string{
		"--root": &request.Root, "--task-id": &request.TaskID, "--repo": &request.Repo,
		"--pr": &pullRequest, "--as-bot": &request.AsBot, "--gates": &gates,
		"--allow-classification": &request.AllowClassification,
	}
	flags := map[string]*bool{
		// --dry-run is the explicit spelling of the default, so it has
		// somewhere to land and nothing to change. --apply is the flag that
		// makes a run do anything, and the group below is what stops an
		// operator asking for both.
		"--dry-run": new(bool),
		"--apply":   &request.Apply, "--break-lock": &request.BreakLock,
		"--i-know-this-is-mocked": &request.KnowinglyMocked,
	}
	if code := parseFlagsWithGroups("publish-reviewer-nudge", args, fields, flags,
		[][]string{{"--dry-run", "--apply"}}, stderr); code != 0 {
		return code
	}

	var missing []string
	for _, required := range []struct{ name, value string }{
		{"--task-id", request.TaskID}, {"--repo", request.Repo},
		{"--pr", pullRequest}, {"--as-bot", request.AsBot},
	} {
		if required.value == "" {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc publish-reviewer-nudge: error: the following arguments are required: %s\n",
			strings.Join(missing, ", "))
		return 2
	}
	number, err := strconv.Atoi(pullRequest)
	if err != nil {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc publish-reviewer-nudge: error: argument --pr: invalid int value: %s\n",
			pullRequest)
		return 2
	}
	request.PullRequest = number
	for _, gate := range strings.Split(gates, ",") {
		if trimmed := strings.TrimSpace(gate); trimmed != "" {
			request.Gates = append(request.Gates, trimmed)
		}
	}

	summary, err := registry.PublishReviewerNudge(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		var blocked *ReviewerNudgeBlocked
		if errors.As(err, &blocked) {
			return 2
		}
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(summary))
	return 0
}

// createGithubGateIssuesCmd answers `create-github-gate-issues`.
//
// Same exit-code shape as its GitLab counterpart: 2 for a blocked run and for
// a completed one that found drift or refused a candidate. No --link-type
// here, and one flag that command does not have -- --allow-public-repo, which
// is what stands in for GitLab's per-issue confidential flag.
func createGithubGateIssuesCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := GithubGateIssuesRequest{Root: "."}
	var gates string
	fields := map[string]*string{
		"--root": &request.Root, "--task-id": &request.TaskID,
		"--repo": &request.Repo, "--as-bot": &request.AsBot,
		"--gates": &gates, "--plan-digest": &request.PlanDigest,
		"--allow-classification": &request.AllowClassification,
	}
	flags := map[string]*bool{
		// --dry-run is the explicit spelling of the default, so it has
		// somewhere to land and nothing to change. --apply is the flag that
		// makes a run do anything, and the group below is what stops an
		// operator asking for both.
		"--dry-run": new(bool),
		"--apply":   &request.Apply, "--include-scope": &request.IncludeScope,
		"--reconcile-assignees":   &request.ReconcileAssignees,
		"--allow-public-repo":     &request.AllowPublicRepo,
		"--break-lock":            &request.BreakLock,
		"--i-know-this-is-mocked": &request.KnowinglyMocked,
	}
	if code := parseFlagsWithGroups("create-github-gate-issues", args, fields, flags,
		[][]string{{"--dry-run", "--apply"}}, stderr); code != 0 {
		return code
	}

	var missing []string
	for _, required := range []struct{ name, value string }{
		{"--task-id", request.TaskID}, {"--repo", request.Repo}, {"--as-bot", request.AsBot},
	} {
		if required.value == "" {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc create-github-gate-issues: error: the following arguments are required: %s\n",
			strings.Join(missing, ", "))
		return 2
	}
	for _, gate := range strings.Split(gates, ",") {
		if trimmed := strings.TrimSpace(gate); trimmed != "" {
			request.Gates = append(request.Gates, trimmed)
		}
	}

	result, err := registry.CreateGithubGateIssues(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		var blocked *GateIssuesGithubBlocked
		if errors.As(err, &blocked) {
			return 2
		}
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	if len(listOf(result.values["refusals"])) > 0 || result.values["drift_detected"] == true {
		return 2
	}
	return 0
}

// createGateIssuesCmd answers `create-gate-issues`.
//
// Exit 2 covers both a blocked run and a completed one that found assignee
// drift or refused a candidate: each means somebody has to look before the
// task's tracking is complete.
func createGateIssuesCmd(registry *Registry, args []string, stdout, stderr io.Writer) int {
	request := GateIssuesRequest{Root: "."}
	var gates string
	fields := map[string]*string{
		"--root": &request.Root, "--task-id": &request.TaskID,
		"--project-path": &request.ProjectPath, "--as-bot": &request.AsBot,
		"--gates": &gates, "--plan-digest": &request.PlanDigest,
		"--allow-classification": &request.AllowClassification,
		"--link-type":            &request.LinkType,
	}
	flags := map[string]*bool{
		// --dry-run is the explicit spelling of the default, so it has
		// somewhere to land and nothing to change. --apply is the flag that
		// makes a run do anything, and the group below is what stops an
		// operator asking for both.
		"--dry-run": new(bool),
		"--apply":   &request.Apply, "--include-scope": &request.IncludeScope,
		"--reconcile-assignees":   &request.ReconcileAssignees,
		"--break-lock":            &request.BreakLock,
		"--i-know-this-is-mocked": &request.KnowinglyMocked,
	}
	if code := parseFlagsWithGroups("create-gate-issues", args, fields, flags,
		[][]string{{"--dry-run", "--apply"}}, stderr); code != 0 {
		return code
	}

	var missing []string
	for _, required := range []struct{ name, value string }{
		{"--task-id", request.TaskID}, {"--project-path", request.ProjectPath},
		{"--as-bot", request.AsBot},
	} {
		if required.value == "" {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc create-gate-issues: error: the following arguments are required: %s\n",
			strings.Join(missing, ", "))
		return 2
	}
	// One link type, and it is validated here rather than at the forge: an
	// unrecognised type reaches GitLab as a create-link call that fails
	// mid-run, after some issues already exist.
	if request.LinkType != "" {
		if code := rejectInvalidChoice(stderr, "create-gate-issues", "--link-type",
			request.LinkType, []string{"relates_to"}); code != 0 {
			return code
		}
	}
	for _, gate := range strings.Split(gates, ",") {
		if trimmed := strings.TrimSpace(gate); trimmed != "" {
			request.Gates = append(request.Gates, trimmed)
		}
	}

	result, err := registry.CreateGateIssues(request)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		var blocked *GateIssuesBlocked
		if errors.As(err, &blocked) {
			return 2
		}
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(result))
	if len(listOf(result.values["refusals"])) > 0 || result.values["drift_detected"] == true {
		return 2
	}
	return 0
}
