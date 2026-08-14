"""In-process console-script entry point for the pip/pipx-installable `cadre`
distribution.

This package is a second, independent distribution channel for the CLI (see
pyproject.toml and README.md's "pip / pipx install" section). It does not
replace the checkout path.

**What changed with the Python-to-Go migration (ADR-001-CLI-GO-REFACTOR.md),
and why this file is now a dispatcher rather than a loader.** Until that
migration, `bin/cadre.py` was the checkout dispatcher and this module simply
loaded the vendored copy of it and called its `main()`, so the subcommand
table lived in exactly one place (`bin/subcommands.tsv`). The migration
deleted `bin/cadre.py`, emptied `bin/subcommands.tsv`, and made
`bin/cadre` a shell shim that builds and execs `cmd/cadre` (Go). A Go binary
is not something a pure-Python wheel built by `python -m build` can carry --
that is a platform-tagged, cross-compiled artifact, and DISTRIBUTION.md's
"PyPI (Backward Compatibility)" section schedules it as its own piece of work
(roadmap item v0.24.0+), not as part of this migration.

So this channel keeps working the way it always did: it dispatches to the
**Python implementations under `roster/`, which are still in the tree and
still the only implementation of several subcommands** (see
REMAINING_PYTHON_SCOPE.md -- ~7,800 lines across `roster/shared/src`,
`roster/context-store/src`, `roster/orchestration/src` and
`roster/orchestration/mcp` are still Python-only). The one thing that had to
move here is the subcommand table itself, because `bin/subcommands.tsv` no
longer carries it. `_SUBCOMMANDS` below is that table, seeded from the last
non-empty `bin/subcommands.tsv` plus the three generator rows the Go CLI took
over in-checkout but which have no Go equivalent reachable from a wheel.

Why loading vendored scripts works with zero changes to any of them: every
dispatched script under `roster/` resolves its own resource roots
(roster/catalog.yaml, roster/orchestration/routing.json, role AGENT.md files,
...) purely from its own `Path(__file__).resolve()` position, walking a fixed
number of parents -- never from an environment variable or the caller's cwd.
As long as the vendored copy preserves the exact relative directory layout
the checkout has (`<root>/roster/...`), running a vendored script from its
vendored location makes every one of those `__file__`-relative computations
land inside the vendored tree automatically.

`agentic_sdlc/__init__.py` (the sibling `agentic-sdlc` kernel distribution)
uses the same "bundled copy sits next to the package, checked for at import
time" shape for its single `contracts/` directory; this module applies the
same principle to a larger, multi-directory resource surface.
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

from ._version import VERSION

__version__ = VERSION

_PACKAGE_DIR = Path(__file__).resolve().parent

# Bundled (built wheel / installed package): resources live under
# cadre_cli/_vendor/{roster,.agents,provider}, copied in at build time by
# hatchling's force-include (see pyproject.toml). Editable/development
# install (`pip install -e .` from this checkout): no _vendor/ directory
# exists, so fall back to the real checkout root next to this package
# directory, exactly like agentic_sdlc/__init__.py's PLUGIN_ROOT fallback.
#
# The probe file is roster/catalog.yaml rather than the old bin/cadre.py --
# that file no longer exists in either layout, and probing for it is how a
# built wheel would silently misreport itself as an editable install.
_BUNDLED_VENDOR_ROOT = _PACKAGE_DIR / "_vendor"
if (_BUNDLED_VENDOR_ROOT / "roster" / "catalog.yaml").is_file():
    VENDOR_ROOT = _BUNDLED_VENDOR_ROOT
else:
    VENDOR_ROOT = _PACKAGE_DIR.parent

INTERACTIVE_FLAG = "--interactive"
SDLC_DESCRIPTION = "Delegated Agentic SDLC CLI"
PROVIDER_MANIFEST = VENDOR_ROOT / "provider" / "provider.json"

# The subcommand table, `(name, script, description)` -- the same three
# columns `bin/subcommands.tsv` carried. Scripts are given relative to
# VENDOR_ROOT so the same strings address the checkout tree and the vendored
# tree identically.
#
# Two deliberate differences from the checkout CLI's own subcommand list:
#
#   * `knowledge` is absent. Its Python implementation
#     (roster/knowledge-store/src/) was deleted by the Go migration and the
#     replacement lives in Go (internal/knowledge/), which a pure-Python
#     wheel cannot carry. Listing it would ship a row that can only fail.
#     See _UNSHIPPED_SUBCOMMANDS for the message a caller gets instead.
#   * The three `generate-*` rows are present even though the checkout CLI
#     now serves them from Go, because the Python implementations are still
#     in-tree and `generate-role-metadata --check` is a legitimate,
#     read-only, installed-mode operation (it verifies the *installed
#     package's own* bundled metadata is self-consistent). The other two,
#     and this one's write mode, fail closed -- see
#     _CHECKOUT_ONLY_SUBCOMMANDS.
_SUBCOMMANDS: list[tuple[str, str, str]] = [
    ("select", "roster/orchestration/src/select_agents.py", "Deterministic agent/gate selection (select_agents.py)"),
    ("selection-telemetry", "roster/orchestration/src/selection_telemetry.py", "Summarize opt-in, local cadre select telemetry (selection_telemetry.py)"),
    ("context", "roster/context-store/src/cli.py", "Agent context store: park working material outside an agent's context window and get it back by handle (context-store/src/cli.py)"),
    ("generate-plugin", "roster/orchestration/src/generate_global_plugin.py", "Regenerate a packaged plugin distribution (generate_global_plugin.py; requires --output)"),
    ("generate-authority-aides", "roster/orchestration/src/generate_authority_aides.py", "Regenerate roster/authority/*-aide AGENT.md files (generate_authority_aides.py)"),
    ("generate-role-metadata", "roster/orchestration/src/generate_role_metadata.py", "Regenerate roster/catalog.yaml and routing.json knowledge_focus from role metadata (generate_role_metadata.py)"),
    ("bootstrap-codex", "roster/orchestration/src/sync_codex_agents.py", "Safely install namespaced Codex role wrappers"),
    ("resolve-shared", "roster/shared/src/resolve.py", "Resolve effective shared config for the current project (resolve.py)"),
    ("mcp-dispatch-server", "roster/orchestration/mcp/dispatch_server.py", "Run the Codex MCP dispatch server (stdio; requires the mcp package)"),
    ("init", "roster/shared/src/init_project.py", "Guide a project through generating .agents/shared/ overlays (init_project.py)"),
    ("profile", "roster/orchestration/src/profile_diff.py", "Read-only provider/profile drift report against a consuming project's copy (profile_diff.py)"),
    ("gitlab-evidence", "roster/orchestration/mcp/gitlab_cli.py", "Non-MCP CLI over the GitLab evidence tools (create-review-subtask/write-wiki-page/write-evidence-comment)"),
    ("config", "roster/shared/src/settings.py", "Show resolved operator settings, config file paths, or resolve one setting (settings.py; `show`/`path`/`resolve`)"),
    ("doctor", "roster/orchestration/src/doctor.py", "Report which cadre binary is running, what kind of install it is, and warn on a cwd/checkout mismatch (doctor.py)"),
    ("role-fidelity", "roster/orchestration/src/role_fidelity.py", "Measure whether role briefs survive a given model: context-budget analysis, or live probes (role_fidelity.py)"),
    ("upgrade", "roster/orchestration/src/upgrade.py", "Check for Cadre updates and upgrade the CLI (--check, --force, --help)"),
]

# Subcommands the checkout CLI serves but this distribution deliberately does
# not, mapped to the message explaining where they went. Recognising the name
# and explaining it is the point: an "unknown subcommand" error would be
# actively misleading for a name that is real and documented everywhere else.
_UNSHIPPED_SUBCOMMANDS = {
    "knowledge": (
        "cadre knowledge is implemented in Go (cmd/cadre) as of the "
        "Python-to-Go migration and is not part of this pure-Python "
        "distribution. Use a checkout's ./bin/cadre knowledge, or install "
        "the Go CLI, instead."
    ),
}

# `generate-plugin` (generate_global_plugin.py) reads roster/README.md,
# roster/RUNBOOK.md, and the whole docs/ tree, then *writes* regenerated
# output into a checkout named by --output -- a maintainer/regeneration
# operation driven from this repository's own tracked source (it reads this
# repository's git index), not something that makes sense pointed at an
# installed site-packages copy.
# `generate-authority-aides` (generate_authority_aides.py) is the same class
# of operation: it *writes* regenerated roster/authority/*/AGENT.md files
# back into this repository's own tree, so pointed at an installed
# distribution it would silently write into site-packages instead of
# failing -- same rationale, same fix.
# Rather than vendor docs/ and the plugin tree just to make these "work" from
# an install (which would make write-into-site-packages behavior appear
# supported when it isn't, and meaningfully bloat the wheel), these
# subcommand names fail closed here, in the installed (bundled _vendor/)
# dispatch path only -- an editable install from a real checkout is
# untouched.
#
# `generate-role-metadata` (generate_role_metadata.py) is a partial case:
# its default (write) mode has the exact same "writes back into this
# repository's own tree" problem -- from an installed distribution it would
# silently regenerate the *installed package's own vendored copy* of
# roster/catalog.yaml / roster/orchestration/routing.json under
# site-packages, never a real user project. Its `--check` mode is different:
# it only reads, verifying the installed package's own bundled metadata is
# internally self-consistent -- a legitimate installed-mode use case. So this
# subcommand name is listed here too, but _requires_checkout() only fails
# closed for it when `--check` is absent from argv.
_CHECKOUT_ONLY_SUBCOMMANDS = frozenset(
    {"generate-plugin", "generate-authority-aides", "generate-role-metadata"}
)

# Subcommands where only *some* invocations are checkout-only (see
# `generate-role-metadata` above): map the subcommand name to a predicate
# over the remaining argv (excluding the subcommand itself) that returns
# True when this particular invocation requires a full checkout. A
# subcommand present in _CHECKOUT_ONLY_SUBCOMMANDS but absent from this map
# is unconditionally checkout-only (e.g. generate-plugin,
# generate-authority-aides).
_PARTIAL_CHECKOUT_ONLY_PREDICATES = {
    "generate-role-metadata": lambda rest: "--check" not in rest,
}


def _requires_checkout(command: str, rest_argv: list[str]) -> bool:
    """True when this specific invocation of `command` must fail closed from
    a bundled (pip/pipx) install because it would write into the installed
    package's own site-packages tree instead of a real checkout/project.
    """
    if command not in _CHECKOUT_ONLY_SUBCOMMANDS:
        return False
    predicate = _PARTIAL_CHECKOUT_ONLY_PREDICATES.get(command)
    if predicate is None:
        return True
    return predicate(rest_argv)


def _is_bundled_install() -> bool:
    """True when running from the built/installed distribution (vendored
    copy under cadre_cli/_vendor/), False for an editable/dev checkout
    install where VENDOR_ROOT falls back to the real repository root.
    """
    return VENDOR_ROOT == _BUNDLED_VENDOR_ROOT


def cli_version() -> str:
    """Return this distribution's version.

    `cadre_cli/_version.py` is pyproject.toml's sole version source, and is
    also what the Go CLI parses (`internal/cli/version.go`) -- one version
    line for both channels. It deliberately differs from
    `provider/provider.json`'s version, which versions the Agentic SDLC
    provider manifest rather than this CLI.
    """
    return VERSION


def usage() -> str:
    lines = ["Usage: cadre <subcommand> [args...]", "", "Subcommands:"]
    for name, _script, description in _SUBCOMMANDS:
        lines.append(f"  {name:<24} {description}")
    lines.append(f"  {'sdlc':<24} {SDLC_DESCRIPTION}")
    lines.append(f"  {'help':<24} Show this message")
    lines.append("")
    lines.append("Each subcommand's own --help documents its arguments, e.g. `cadre sdlc plan --help`.")
    lines.append("")
    lines.append(
        f"`{INTERACTIVE_FLAG}`, given as the leading argument before the subcommand name (e.g. "
        f"`cadre {INTERACTIVE_FLAG} select ...`), opts the dispatched subcommand into "
        "roster/shared/src/settings.py's interactive configuration prompt (CADRE_INTERACTIVE=1, "
        "passed via an explicit subprocess env= rather than mutating this process's own "
        "environment) -- only honored when stdin/stdout are both a real terminal; a value entered "
        "is offered a write to the project-local or user-global cadre config file."
    )
    lines.append(
        "For `init`, this is distinct from `cadre init --interactive`, which starts the "
        "shared-policy overlay questionnaire; use both flags when both prompt flows are needed."
    )
    lines.append("")
    lines.append(
        "This is the pip/pipx distribution, which dispatches the Python implementations under "
        "roster/. A repository checkout's ./bin/cadre runs the Go CLI (cmd/cadre) and carries a "
        "wider subcommand set -- see README.md."
    )
    return "\n".join(lines)


def sdlc_install_message() -> str:
    """Point at the kernel range `provider.json` actually declares.

    Read lazily, and only on the failure path, so the dispatcher stays a
    thin shim on every successful invocation. Do not hardcode a version
    here: provider.json's own `version` and its `kernel_compatibility` are
    different version lines, and quoting the wrong one sent operators to a
    kernel ten minor versions too old.
    """
    requirement = "a compatible version"
    try:
        import json

        compatibility = json.loads(PROVIDER_MANIFEST.read_text(encoding="utf-8"))["kernel_compatibility"]
        requirement = f"v{compatibility['minimum']} or newer (below v{compatibility['maximum_exclusive']})"
    except (OSError, ValueError, KeyError, TypeError):
        pass
    return (
        f"cadre: Agentic SDLC {requirement} is required; install it from "
        "https://github.com/deagy/cadre"
    )


def _child_env(interactive: bool) -> dict[str, str] | None:
    if not interactive:
        return None
    child_env = dict(os.environ)
    child_env["CADRE_INTERACTIVE"] = "1"
    return child_env


def _import_settings():
    """Import the vendored `settings` module.

    Imported lazily, inside the one caller that needs it, rather than at
    module import time: `cadre help` and `cadre --version` must keep working
    even if the vendored shared/ tree is somehow incomplete, and a console
    script that cannot print its own usage is the worst possible failure
    mode for a packaging bug.
    """
    shared_src = str(VENDOR_ROOT / "roster" / "shared" / "src")
    if shared_src not in sys.path:
        sys.path.append(shared_src)
    import settings  # noqa: PLC0415  (deliberately lazy; see docstring)

    return settings


def dispatch_sdlc(rest: list[str], *, interactive: bool = False) -> int:
    settings = _import_settings()
    try:
        sdlc_bin = settings.resolve_optional(
            "agentic_sdlc.bin_path", env=_child_env(interactive) or os.environ
        )
    except settings.SettingsError as error:
        # resolve_optional() only ever raises for a global_only scope
        # violation (an untrusted project-local file setting
        # agentic_sdlc.bin_path) -- that's a security event this dispatcher
        # must surface, not a bare traceback out of a thin CLI shim.
        print(f"cadre: {error}", file=sys.stderr)
        return 1
    if not sdlc_bin:
        # A checkout ships the kernel in-tree at bin/agentic-sdlc, so an
        # editable install needs no separate install. A wheel does not vendor
        # bin/, so this simply does not resolve there and the caller gets the
        # install pointer below -- correct, since the kernel is its own pip
        # distribution. Either way an explicit env var, a configured
        # agentic_sdlc.bin_path, or an `agentic-sdlc` the operator installed
        # themselves still wins, because each is a choice the human made
        # about which kernel to run.
        in_tree = VENDOR_ROOT / "bin" / "agentic-sdlc"
        if in_tree.is_file():
            sdlc_bin = str(in_tree)
    if not sdlc_bin:
        print(sdlc_install_message(), file=sys.stderr)
        return 1
    rest, suppress_default = _resolve_provider_injection(rest)
    provider_args: list[str] = []
    if not suppress_default:
        provider_args = ["--provider", str(PROVIDER_MANIFEST)]
    result = subprocess.run(
        [sdlc_bin, *provider_args, *rest], env=_child_env(interactive)
    )
    return result.returncode


def _resolve_provider_injection(rest: list[str]) -> tuple[list[str], bool]:
    """Decide whether to inject Cadre's provider bundle (PP-FR-4).

    Returns the argv to forward, and whether to suppress the injection.

    Two ways to suppress it, and the first is why this exists at all:

    1. **The caller supplied their own `--provider`.** Injecting Cadre's
       alongside it is what made `cadre sdlc --provider <other> provider list`
       fail with `duplicates profile ids: ['generic']` -- the foreign manifest
       loaded correctly and was then rejected for colliding with a bundle the
       caller never asked for. A caller naming a provider has expressed which
       one they want.
    2. **`--no-default-provider`**, for suppressing Cadre's without supplying a
       replacement. Consumed here; never forwarded.

    Detection uses argparse rather than a string scan of `rest`: the kernel's
    `--provider` is `action="append"`, accepts both `--provider=X` and
    `--provider X`, and may repeat. The property that makes detection safe is
    not that this parser is clever but that it is the *same* parser -- for a
    genuinely ambiguous argv this reads it exactly as the kernel will.
    """
    import argparse  # noqa: PLC0415  (only needed on the sdlc path)

    class _Quiet(argparse.ArgumentParser):
        """Never speaks. On malformed argv the kernel reports it, in the
        kernel's wording, about the command the caller actually invoked --
        printing a usage block for a wrapper parser they never called would
        just be a second, more confusing error."""

        def error(self, message: str):  # noqa: D102
            raise SystemExit(2)

    parser = _Quiet(add_help=False, allow_abbrev=False)
    parser.add_argument("--no-default-provider", action="store_true")
    parser.add_argument("--provider", action="append")
    try:
        known, remainder = parser.parse_known_args(rest)
    except SystemExit:
        # A malformed flag is the kernel's error to report, with the kernel's
        # wording. Forward untouched and inject as before rather than failing
        # here with a message about a wrapper the caller did not invoke.
        return rest, False
    supplied = known.provider or []
    # Order preserved. The kernel's --provider is action="append", so the list
    # order is the caller's stated precedence -- an earlier draft prepended each
    # value in turn and silently reversed it for any caller passing more than
    # one.
    forwarded = [arg for value in supplied for arg in ("--provider", value)]
    forwarded.extend(remainder)
    return forwarded, bool(known.no_default_provider or supplied)


def main(argv: list[str] | None = None) -> int:
    """Console-script entry point (`[project.scripts] cadre = "cadre_cli:main"`)."""
    effective_argv = sys.argv[1:] if argv is None else argv

    if effective_argv == ["--version"]:
        print(f"cadre {cli_version()}")
        return 0

    interactive = False
    if effective_argv and effective_argv[0] == INTERACTIVE_FLAG:
        interactive = True
        effective_argv = effective_argv[1:]

    command = effective_argv[0] if effective_argv else "help"
    rest = effective_argv[1:]

    if command in ("help", "-h", "--help"):
        print(usage())
        return 0

    if command == "sdlc":
        return dispatch_sdlc(rest, interactive=interactive)

    if _is_bundled_install() and _requires_checkout(command, rest):
        if command == "generate-role-metadata":
            print(
                "cadre generate-role-metadata (without --check) requires a "
                "full repository checkout (it writes regenerated "
                "roster/catalog.yaml / roster/orchestration/routing.json back "
                "into a real project tree, not this installed package's own "
                "site-packages copy); use --check from an installed "
                "distribution to verify the installed metadata is current, "
                "or clone https://github.com/deagy/cadre and run "
                "./bin/cadre generate-role-metadata instead.",
                file=sys.stderr,
            )
        else:
            print(
                f"cadre {command}: requires a full repository checkout "
                "(this is a maintainer/regeneration tool, not available from a "
                "pip-installed distribution); clone "
                "https://github.com/deagy/cadre and run "
                f"./bin/cadre {command} instead.",
                file=sys.stderr,
            )
        return 1

    if command in _UNSHIPPED_SUBCOMMANDS:
        print(f"cadre: {_UNSHIPPED_SUBCOMMANDS[command]}", file=sys.stderr)
        return 1

    match = next((row for row in _SUBCOMMANDS if row[0] == command), None)
    if match is None:
        print(f"cadre: unknown subcommand '{command}'", file=sys.stderr)
        print(usage(), file=sys.stderr)
        return 1

    _name, script, _description = match
    script_path = VENDOR_ROOT / script
    if not script_path.is_file():
        # A vendored resource that the wheel was supposed to carry is
        # missing. Fail closed with the path rather than letting the
        # interpreter emit `can't open file ...` -- a packaging bug should
        # name itself.
        print(
            f"cadre {command}: this distribution is missing its vendored "
            f"implementation ({script}); reinstall the package or use a "
            "repository checkout.",
            file=sys.stderr,
        )
        return 1

    # subprocess.run, not os.execv: os.execv/os.spawnv join argv into a
    # command-line string without subprocess's list2cmdline quoting on
    # Windows, so any argument containing a space (e.g. --task "multi word
    # value") silently gets re-split by the child process. subprocess.run
    # quotes correctly on every platform this needs to run on.
    result = subprocess.run(
        [sys.executable, str(script_path), *rest], env=_child_env(interactive)
    )
    return result.returncode


if __name__ == "__main__":  # pragma: no cover - convenience for `python -m cadre_cli`
    raise SystemExit(main())
