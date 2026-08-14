package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deagy/cadre/cli/internal/interop"
)

// SelectorScriptRelativePath is where the authoritative deterministic
// selector lives, relative to a Cadre checkout (or to the vendored tree a
// pip/pipx install carries -- see cadre_cli/__init__.py, which addresses it
// by this same relative path).
const SelectorScriptRelativePath = "roster/orchestration/src/select_agents.py"

// maxAncestorWalkDepth bounds the upward search for this CLI's own tree, matching
// platform.FindProjectRoot's own bound rather than inventing a second one.
const maxAncestorWalkDepth = 64

// SelectAgents is the `cadre select` command: a deterministic local agent
// dispatch plan.
//
// # Why this dispatches rather than reimplements
//
// `cadre select` has a published, byte-level output contract. Its JSON plan
// is `selection.schema.json` version 7, it is vendored by consumers, it is
// compared byte-for-byte across invocation paths by
// roster/orchestration/test/test_repository_health.py, and it carries a
// `dispatch_fingerprint` that is a SHA-256 over the plan's own canonical
// form -- so any divergence at all, down to a key that sorts differently or
// a null that became an empty list, is a different plan.
//
// The Go port briefly shipped an independent reimplementation of the
// selector here. It answered to a smaller flag set (--base, --explain,
// --format, --require-sdlc, --root, --roster, --source and --top were all
// absent), it repurposed --output from "write the plan to this path" to
// "choose an output format", it defaulted to human text where the contract
// defaults to JSON, and it emitted a plan of its own invention rather than
// a schema-version-7 document. Every downstream consumer of `cadre select`
// -- the packaged plugin wrapper, the Cline plugins, the pip/pipx
// distribution, CI -- reads the version 7 plan.
//
// The version 7 plan is produced by roster/orchestration/src (routing.py,
// risk_classifier.py, build_dispatch_plan.py, provenance.py,
// agentic_sdlc_contracts.py, roster_manifest.py, routing_overlay.py,
// plan_text_format.py, route_near_miss.py). Those modules are still in the
// tree, are still what the packaged plugin's own `bin/cadre` execs, and are
// still what the pip/pipx distribution dispatches to (REMAINING_PYTHON_SCOPE.md).
// Until they are ported wholesale -- with the fingerprint, the canonical
// JSON encoding, the catalog ordering and the lifecycle-contract handshake
// all reproduced exactly -- a second, parallel implementation here can only
// produce a plan that disagrees with the one every other channel emits.
// So this dispatches to the one implementation instead of racing it.
//
// Consequences worth knowing: argv is passed through **unmodified**, so the
// flag surface, the defaults, the validation messages, the `cadre select`
// program name in usage errors, and the exit codes are argparse's, not
// ours; there is no second parser here to drift from that one.
func SelectAgents(args []string) int {
	return SelectAgentsWithOptions(context.Background(), args, false)
}

// SelectAgentsWithOptions is SelectAgents with the dispatcher's leading
// `--interactive` flag threaded through, so `cadre --interactive select`
// reaches roster/shared/src/settings.py's prompt the same way it does for
// any other dispatched subcommand (CADRE_INTERACTIVE=1 in an explicit child
// environment, never a mutation of this process's own).
func SelectAgentsWithOptions(ctx context.Context, args []string, interactive bool) int {
	script, err := FindCadreFile(SelectorScriptRelativePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
		return 1
	}

	code, err := interop.PythonSubcommand(ctx, script, args, interop.Options{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Env:    childEnv(interactive),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: %s\n", err)
		return 1
	}
	return code
}

// FindCadreFile locates a file belonging to *this CLI's own installation*
// -- a roster script, the subcommand table -- given its checkout-relative
// path.
//
// Deliberately NOT platform.RepoRoot(): that walks up from the *caller's*
// working directory, which is the right answer for "which project is being
// worked on" (and is what `cadre select`'s own --root defaults to) but the
// wrong answer for "where does my own implementation live". A `cadre`
// invoked from an unrelated checkout, or from a directory that is no
// repository at all, must still find its own roster -- see
// test_repository_health.py's symlink and packaged-wrapper cases, both of
// which run the CLI from a temporary directory on purpose.
//
// Resolution order, first hit wins:
//
//  1. $CADRE_REPO_ROOT, exported by bin/cadre and bin/cadre.ps1 so the
//     built binary under .cadre-build-cache/ knows which checkout produced
//     it without any filesystem guessing.
//  2. Upward from the running executable's own directory, which covers a
//     binary built into the checkout (or installed beside a vendored tree)
//     when no wrapper set the variable.
//  3. Upward from the working directory, the last resort, which is correct
//     whenever the caller happens to be inside a Cadre checkout.
func FindCadreFile(relativePath string) (string, error) {
	relative := filepath.FromSlash(relativePath)

	var candidates []string
	if root := os.Getenv("CADRE_REPO_ROOT"); root != "" {
		candidates = append(candidates, filepath.Join(root, relative))
	}
	if executable, err := os.Executable(); err == nil {
		if resolved, linkErr := filepath.EvalSymlinks(executable); linkErr == nil {
			executable = resolved
		}
		candidates = append(candidates, ancestorCandidates(filepath.Dir(executable), relative)...)
	}
	if workingDirectory, err := os.Getwd(); err == nil {
		candidates = append(candidates, ancestorCandidates(workingDirectory, relative)...)
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}

	return "", fmt.Errorf(
		"cannot locate %s; set CADRE_REPO_ROOT to a Cadre checkout, or run from inside one",
		relativePath,
	)
}

// ancestorCandidates joins relative onto start and each of its ancestors,
// nearest first.
func ancestorCandidates(start, relative string) []string {
	directory, err := filepath.Abs(start)
	if err != nil {
		return nil
	}

	candidates := make([]string, 0, maxAncestorWalkDepth)
	for i := 0; i < maxAncestorWalkDepth; i++ {
		candidates = append(candidates, filepath.Join(directory, relative))
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return candidates
}
