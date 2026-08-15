// Package cli implements the Cadre CLI dispatcher: argument routing,
// version detection, and SDLC delegation. It is an exact behavioral replica
// of bin/cadre.py, ported per ADR-001-CLI-GO-REFACTOR.md and
// CADRE_CLI_GO_ARCHITECTURE.md.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// InteractiveFlag mirrors bin/cadre.py's INTERACTIVE_FLAG.
const InteractiveFlag = "--interactive"

// SubcommandsTableRelativePath locates bin/subcommands.tsv relative to a
// Cadre checkout. It is also what `cadre generate-plugin` reads to build
// the packaged plugin's own `bin/cadre` wrapper, so a row added here
// reaches both dispatchers.
const SubcommandsTableRelativePath = "bin/subcommands.tsv"

// Subcommand is one row of bin/subcommands.tsv: a name and the description
// shown in `cadre help`.
//
// There is no script column any more. It named the Python implementation the
// packaged plugin's wrapper used to exec when it could not resolve the Go
// binary; that fallback is gone (see internal/generators' wrapper), the suite
// no longer ships those scripts, and every subcommand this dispatcher serves
// is built in. A column naming files the distribution does not contain is
// exactly the stale configuration this migration keeps finding.
// Historical shape:
// dispatches to (relative to the repository root), and a one-line
// description used in usage text.
type Subcommand struct {
	Name        string
	Description string
}

// LoadSubcommands parses bin/subcommands.tsv, mirroring bin/cadre.py's
// load_subcommands(). Each non-empty line is `name\tdescription`.
func LoadSubcommands(path string) ([]Subcommand, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var rows []Subcommand
	for _, line := range strings.Split(string(contents), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			return nil, fmt.Errorf("subcommands.tsv: malformed row (want 2 tab-separated fields, got %d): %q", len(fields), line)
		}
		rows = append(rows, Subcommand{Name: fields[0], Description: fields[1]})
	}
	return rows, nil
}

// Usage renders the same usage text as bin/cadre.py's usage().
func Usage(subcommands []Subcommand) string {
	var b strings.Builder
	b.WriteString("Usage: cadre <subcommand> [args...]\n\n")
	b.WriteString("Subcommands:\n")
	listed := make(map[string]bool, len(subcommands))
	for _, row := range subcommands {
		listed[row.Name] = true
		fmt.Fprintf(&b, "  %-16s %s\n", row.Name, row.Description)
	}
	// Subcommands this binary serves that bin/subcommands.tsv does not
	// describe. The table is the single source of truth for a subcommand's
	// public name and description wherever it has a row (it is also what
	// the packaged plugin's own generated wrapper is built from), so a name
	// already listed above is never repeated here -- the fallback text
	// below exists for the rows the table does not carry, not as a second,
	// competing description of the ones it does.
	for _, row := range []Subcommand{
		{Name: "generate-plugin", Description: "Regenerate a deagy/cadre-lifecycle checkout (requires --output)"},
		{Name: "generate-role-metadata", Description: "Regenerate roster/catalog.yaml and routing.json from role metadata"},
		{Name: "generate-authority-aides", Description: "Regenerate roster/authority/*-aide AGENT.md files"},
		{Name: "upgrade", Description: "Check for Cadre updates and upgrade the CLI (--check, --force, --help)"},
		{Name: "mcp-dispatch-server", Description: "Run the MCP dispatch server (stdio)"},
		{Name: "mcp-gitlab-server", Description: "Run the GitLab evidence MCP server (stdio; three create-only tools)"},
		{Name: "knowledge", Description: "Vectorized knowledge store (init, stats, search, context)"},
		{Name: "doctor", Description: "Report which cadre binary is running, what kind of install it is, and warn on a cwd/checkout mismatch"},
		{Name: "selection-telemetry", Description: "Summarize opt-in, local cadre select telemetry"},
		{Name: "schema-validate", Description: "Strict JSON Schema validation for roster/catalog.yaml and routing.json"},
		{Name: "role-fidelity", Description: "Measure whether role briefs survive a given model: context-budget analysis, or live probes"},
		{Name: "gitlab-evidence", Description: "Non-MCP CLI over the GitLab evidence tools (create-review-subtask/write-wiki-page/write-evidence-comment)"},
		{Name: "config", Description: "Show resolved operator settings, config file paths, or resolve one setting"},
		{Name: "resolve-shared", Description: "Resolve effective shared config for the current project"},
		{Name: "init", Description: "Guide a project through generating .agents/shared/ overlays (init_project.py)"},
		{Name: "context", Description: "Local agent context store: put/get/list/search/export/promote/drop (context-store/src/cli.py)"},
		{Name: "bootstrap-codex", Description: "Safely install namespaced Codex role wrappers (sync_codex_agents.py)"},
		{Name: "profile", Description: "Read-only provider/profile drift report against a consuming project's copy (profile_diff.py)"},
		{Name: "select", Description: "Deterministic agent/gate selection (select_agents.py)"},
	} {
		if listed[row.Name] {
			continue
		}
		fmt.Fprintf(&b, "  %-16s %s\n", row.Name, row.Description)
	}
	fmt.Fprintf(&b, "  %-16s %s\n", "sdlc", sdlcDescription)
	fmt.Fprintf(&b, "  %-16s %s\n", "help", "Show this message")
	b.WriteString("\n")
	b.WriteString("Each subcommand's own --help documents its arguments, e.g. `cadre sdlc plan --help`.\n\n")
	fmt.Fprintf(&b, "`%s`, given as the leading argument before the subcommand name (e.g. "+
		"`cadre %s select ...`), opts the dispatched subcommand into "+
		"roster/shared/src/settings.py's interactive configuration prompt (CADRE_INTERACTIVE=1, "+
		"passed via an explicit subprocess env= rather than mutating this process's own "+
		"environment) -- only honored when stdin/stdout are both a real terminal; a value entered "+
		"is offered a write to the project-local or user-global cadre config file.\n", InteractiveFlag, InteractiveFlag)
	b.WriteString("For `init`, this is distinct from `cadre init --interactive`, which starts the " +
		"shared-policy overlay questionnaire; use both flags when both prompt flows are needed.")
	return b.String()
}

// Deps collects the dispatcher's runtime dependencies: I/O streams, the
// repository root, and how to run a Python subcommand -- injected so tests
// never touch the real process's stdio or spawn a real interpreter.
type Deps struct {
	Stdout io.Writer
	Stderr io.Writer

	RepoRoot        string
	SubcommandsPath string
	SDLCDeps        SDLCDeps
}

// Run is an exact behavioral replica of bin/cadre.py's main(): parse
// --version, --interactive, the subcommand name, and dispatch to sdlc
// delegation, a Python subcommand script, help text, or an unknown-command
// error. Returns the process exit code (0 success, 1 error, 2 for a
// dispatcher-level argument problem -- though as in the Python original,
// most argument parsing is delegated to the subcommand itself and errors
// there surface as whatever exit code that subcommand chooses).
func Run(ctx context.Context, argv []string, deps Deps) int {
	if len(argv) == 1 && argv[0] == "--version" {
		version, err := CLIVersion(deps.RepoRoot)
		if err != nil {
			writef(deps.Stderr, "cadre: %s\n", err)
			return 1
		}
		writef(deps.Stdout, "cadre %s\n", version)
		return 0
	}

	interactive := false
	if len(argv) > 0 && argv[0] == InteractiveFlag {
		interactive = true
		argv = argv[1:]
	}

	// The subcommand table belongs to this CLI's own installation, not to
	// whatever project the caller happens to be standing in. Resolving it
	// from deps.RepoRoot alone -- an upward .git walk from the working
	// directory -- meant that running `cadre select --root <elsewhere>`
	// from any other repository failed with "bin/subcommands.tsv: no such
	// file or directory" before it had parsed a single argument, which is
	// how the Cline plugin's whole target-workspace surface broke.
	derived := deps.SubcommandsPath == ""
	subcommandsPath := deps.SubcommandsPath
	if derived {
		found, findErr := FindCadreFile(SubcommandsTableRelativePath)
		if findErr != nil {
			found = filepath.Join(deps.RepoRoot, filepath.FromSlash(SubcommandsTableRelativePath))
		}
		subcommandsPath = found
	}
	subcommands, err := LoadSubcommands(subcommandsPath)
	if err != nil {
		// A table this dispatcher went looking for and could not find is
		// survivable: every subcommand it serves is built in, and the table
		// only supplies usage text plus the Python-script fallback. A table
		// the *caller* named explicitly is not -- that is a configuration
		// error, and silently continuing without it would hide a typo.
		if !derived || !errors.Is(err, fs.ErrNotExist) {
			writef(deps.Stderr, "cadre: %s\n", err)
			return 1
		}
		subcommands = nil
	}

	command := "help"
	var rest []string
	if len(argv) > 0 {
		command = argv[0]
		rest = argv[1:]
	}

	if command == "help" || command == "-h" || command == "--help" {
		writeln(deps.Stdout, Usage(subcommands))
		return 0
	}

	if command == "sdlc" {
		return DispatchSDLC(ctx, deps.RepoRoot, rest, interactive, deps.SDLCDeps)
	}

	// Route Go-implemented generators
	if command == "generate-authority-aides" {
		return GenerateAuthorityAides(rest)
	}
	if command == "generate-role-metadata" {
		return GenerateRoleMetadata(rest)
	}
	if command == "generate-plugin" {
		return GeneratePlugin(rest)
	}

	// Route Go-implemented orchestration
	if command == "select" {
		return SelectAgentsWithOptions(ctx, rest, interactive)
	}
	if command == "selection-telemetry" {
		return SelectionTelemetryCmd(rest)
	}
	if command == "schema-validate" {
		return SchemaValidateCmd(rest)
	}
	if command == "role-fidelity" {
		return RoleFidelityCmd(rest)
	}
	if command == "gitlab-evidence" {
		return GitLabEvidenceCmd(rest)
	}
	if command == "config" {
		return ConfigCmd(rest)
	}
	if command == "resolve-shared" {
		return ResolveSharedCmd(rest)
	}
	if command == "init" {
		return InitCmd(rest)
	}
	if command == "context" {
		return ContextCmd(rest)
	}
	if command == "bootstrap-codex" {
		return BootstrapCodexCmd(rest)
	}
	if command == "profile" {
		return ProfileCmd(rest)
	}

	// Route Go-implemented knowledge store. The staged-record verbs
	// (propose, show-staged, import-staged, disposition-staged,
	// ingest-accepted, delete-staged) are handled separately in
	// knowledge_staged.go, where the authorship/approval separation checks
	// live; everything else goes to KnowledgeCmd.
	if command == "knowledge" {
		if KnowledgeStagedRoute(rest) {
			return KnowledgeStagedCmd(rest)
		}
		return KnowledgeCmd(rest)
	}
	if command == "doctor" {
		return DoctorCmd(rest)
	}

	if command == "upgrade" {
		return UpgradeCmd(rest)
	}

	if command == "mcp-dispatch-server" {
		return MCPDispatchServerCmd(rest, deps.Stdout, deps.Stderr)
	}

	if command == "mcp-gitlab-server" {
		return MCPGitLabServerCmd(rest, deps.Stdout, deps.Stderr)
	}

	// Every subcommand this dispatcher serves is routed above, in Go. There
	// used to be a fallback here that exec'd the Python script named by the
	// matching subcommands.tsv row -- unreachable since the last of those
	// routes landed, and removed with the script column that fed it.
	//
	// Reaching this point therefore means the name is not a subcommand at
	// all, including names the table still describes but nothing implements.
	writef(deps.Stderr, "cadre: unknown subcommand '%s'\n", command)
	writeln(deps.Stderr, Usage(subcommands))
	return 1
}

// writef and writeln write CLI output and deliberately discard the write
// error. deps.Stdout/deps.Stderr are ordinary process stdio in production
// (or an in-memory buffer in tests); a write failure there (a closed pipe,
// a full disk on the other end of a redirect) is not something this CLI can
// meaningfully react to differently than just exiting with the exit code
// it already computed -- there is no secondary error-reporting channel to
// escalate a write failure to. This mirrors bin/cadre.py's own behavior:
// Python's print() only raises on a write failure if the caller explicitly
// checks, which bin/cadre.py does not.
func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func writeln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}
