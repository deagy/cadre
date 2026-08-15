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
// # Why the history here matters
//
// `cadre select` has a published, byte-level output contract. Its JSON plan
// is `selection.schema.json`, it is vendored by consumers, it is compared
// byte-for-byte across invocation paths, and it carries a
// `dispatch_fingerprint` that is a SHA-256 over the plan's own canonical
// form -- so any divergence at all, down to a key that sorts differently or
// a null that became an empty list, is a different plan.
//
// This file once shipped an independent Go reimplementation of the selector,
// written from the outside in. It answered to a smaller flag set (--base,
// --explain, --format, --require-sdlc, --root, --roster, --source and --top
// were all absent), it repurposed --output from "write the plan to this
// path" to "choose an output format", it defaulted to human text where the
// contract defaults to JSON, and it emitted a plan of its own invention.
// Every downstream consumer -- the packaged plugin wrapper, the Cline
// plugins, the pip/pipx distribution, CI -- reads the real plan. So it was
// removed, and this dispatched to Python instead of racing it.
//
// The Go implementation is now the default again. What changed is not
// confidence but evidence: internal/selector was built increment by
// increment against roster/orchestration/test/test_select_differential.py,
// which runs both implementations on the same machine in the same run and
// requires byte equality including dispatch_fingerprint. Each layer was
// additionally compared against Python over a space chosen to exercise it --
// 4,725 rule evaluations for the matcher, 44,101 decisions for workflow
// precedence, 69 documents for the overlay merge, 190 cases for the text
// rendering -- because the corpus alone reaches only a handful of shapes.
//
// Python remains in the tree: it is the other half of that gate, and
// CADRE_SELECT_IMPL=python is the escape hatch if a real invocation finds
// something the corpus never did.
//
// Consequences worth knowing about the Python path: argv is passed through
// **unmodified**, so the flag surface, the defaults, the validation
// messages, the `cadre select`
// program name in usage errors, and the exit codes are argparse's, not
// ours; there is no second parser here to drift from that one.
func SelectAgents(args []string) int {
	return SelectAgentsWithOptions(context.Background(), args, false)
}

// SelectImplEnv chooses which selector implementation runs.
//
// The Go implementation is the default. `CADRE_SELECT_IMPL=python` selects
// the Python one, which remains in the tree as an escape hatch and as the
// other half of the differential gate.
//
// The escape hatch is not decoration. The parity gate compares 25 corpus
// plans, plus discovery, overlay, presentation and telemetry behaviour, and
// that is a large space -- but it is not every task anyone will ever run
// through this. If a real invocation turns up a divergence the corpus never
// reached, `CADRE_SELECT_IMPL=python` restores the previous behaviour
// immediately, without a release, and the divergence becomes a new corpus
// case rather than an outage.
const SelectImplEnv = "CADRE_SELECT_IMPL"

// SelectImplPython is the escape-hatch value. Any other value, including
// unset, runs the Go implementation.
//
// Deliberately not a boolean-ish "disable" flag: a caller reading
// CADRE_SELECT_IMPL=python in a CI script can tell what it does without
// consulting anything.
const SelectImplPython = "python"

// SelectGoNotImplementedExit is returned when the Go selector is requested
// but does not exist yet. Deliberately distinct from 1 (a selection error)
// and 2 (a usage error): the differential harness has to be able to tell
// "not built yet" from "built and wrong", because a gate that cannot tell
// them apart reports green against a port that never ran.
const SelectGoNotImplementedExit = 3

// SelectAgentsWithOptions is SelectAgents with the dispatcher's leading
// `--interactive` flag threaded through, so `cadre --interactive select`
// reaches roster/shared/src/settings.py's prompt the same way it does for
// any other dispatched subcommand (CADRE_INTERACTIVE=1 in an explicit child
// environment, never a mutation of this process's own).
func SelectAgentsWithOptions(ctx context.Context, args []string, interactive bool) int {
	if os.Getenv(SelectImplEnv) != SelectImplPython {
		return selectAgentsGo(ctx, args, interactive)
	}
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

// selectAgentsGo runs the native Go selector in internal/selector, which is
// the default path.
//
// It reproduces, and is gated on reproducing: schema-version-correct output,
// the canonical JSON encoding the fingerprint is computed over (sort_keys,
// no whitespace, a fixed exclusion set), catalog ordering, risk
// classification, team-recipe expansion, the lifecycle-contract handshake,
// project-local routing overlays, git-derived inputs, the text and
// near-miss renderings, selection telemetry, and argparse's flag surface,
// defaults and exit codes.
func selectAgentsGo(_ context.Context, args []string, _ bool) int {
	return runSelectGo(args)
}
