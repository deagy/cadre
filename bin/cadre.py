#!/usr/bin/env python3
"""Subcommand dispatcher for this repository's `cadre` CLI.

bin/cadre (POSIX sh) and bin/cadre.ps1 (PowerShell) are thin, per-platform
shims whose only job is finding a Python 3.10+ interpreter and handing off to
this file — that part can't move into Python, since a plain shebang can't
probe multiple interpreter candidates and version-check them before any
Python code is safe to run. Everything past that (the subcommand table, the
`sdlc` delegation to the standalone Agentic SDLC kernel, usage text, and
dispatch) lives here once instead of being duplicated in both shell
languages.

Also runnable directly: `python bin/cadre.py <subcommand> [args...]`.
"""

from __future__ import annotations

import argparse
import ast
import os
import subprocess
import sys
from pathlib import Path

BIN_DIR = Path(__file__).resolve().parent
REPO_ROOT = BIN_DIR.parent
SUBCOMMANDS_PATH = BIN_DIR / "subcommands.tsv"
SDLC_DESCRIPTION = "Delegated Agentic SDLC CLI"
PROVIDER_MANIFEST = REPO_ROOT / "provider" / "provider.json"


def cli_version() -> str:
    """Return Cadre's pip/pipx distribution version without importing it.

    ``cadre_cli/_version.py`` is pyproject.toml's sole version source.  It
    deliberately differs from provider.json's version, which versions the
    Agentic SDLC provider manifest rather than this CLI distribution.

    The source-checkout dispatcher lives at ``<repo>/bin/cadre.py``; a built
    wheel vendors that same dispatcher at ``cadre_cli/_vendor/bin/cadre.py``.
    Locate the marker in either layout.  Parse its single literal assignment
    instead of importing it so asking for a version cannot run package code.
    """
    checkout_marker = REPO_ROOT / "cadre_cli" / "_version.py"
    version_marker = checkout_marker if checkout_marker.is_file() else REPO_ROOT.parent / "_version.py"
    try:
        module = ast.parse(version_marker.read_text(encoding="utf-8"), filename=str(version_marker))
    except (OSError, SyntaxError) as error:
        raise RuntimeError(f"could not read Cadre version marker: {error}") from error

    for statement in module.body:
        if not isinstance(statement, ast.Assign):
            continue
        if not any(isinstance(target, ast.Name) and target.id == "VERSION" for target in statement.targets):
            continue
        if isinstance(statement.value, ast.Constant) and isinstance(statement.value.value, str):
            return statement.value.value

    raise RuntimeError(f"could not find VERSION in Cadre version marker: {version_marker}")


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

_SHARED_SRC_DIR = REPO_ROOT / "roster" / "shared" / "src"
if str(_SHARED_SRC_DIR) not in sys.path:
    sys.path.append(str(_SHARED_SRC_DIR))

import settings  # noqa: E402  (sys.path set above)

INTERACTIVE_FLAG = "--interactive"


def load_subcommands(path: Path = SUBCOMMANDS_PATH) -> list[tuple[str, str, str]]:
    rows = []
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line:
            continue
        name, script, description = line.split("\t")
        rows.append((name, script, description))
    return rows


def usage(subcommands: list[tuple[str, str, str]]) -> str:
    lines = ["Usage: cadre <subcommand> [args...]", "", "Subcommands:"]
    for name, _script, description in subcommands:
        lines.append(f"  {name:<16} {description}")
    lines.append(f"  {'sdlc':<16} {SDLC_DESCRIPTION}")
    lines.append(f"  {'help':<16} Show this message")
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
    return "\n".join(lines)


def _child_env(interactive: bool) -> dict[str, str] | None:
    if not interactive:
        return None
    child_env = dict(os.environ)
    child_env["CADRE_INTERACTIVE"] = "1"
    return child_env


def dispatch_sdlc(rest: list[str], *, interactive: bool = False) -> int:
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
        # The kernel ships in this repository since the monorepo merge, so a
        # checkout needs no install and no AGENTIC_SDLC_BIN. This is the last
        # resort deliberately: an explicit env var or a configured
        # agentic_sdlc.bin_path still wins, and so does an `agentic-sdlc`
        # the operator installed themselves, because either is a choice the
        # human made about which kernel to run.
        in_tree = REPO_ROOT / "bin" / "agentic-sdlc"
        if in_tree.is_file():
            sdlc_bin = str(in_tree)
    if not sdlc_bin:
        print(sdlc_install_message(), file=sys.stderr)
        return 1
    rest, suppress_default = _resolve_provider_injection(rest)
    provider_args: list[str] = []
    if not suppress_default:
        provider_args = ["--provider", str(REPO_ROOT / "provider" / "provider.json")]
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

    **On detecting case 1 with argparse rather than by scanning `rest`.** The
    implementation review recommended an explicit flag *instead of* detection,
    on the ground that the kernel's `--provider` is `action="append"`, accepts
    both `--provider=X` and `--provider X`, may repeat, and that a wrapper-side
    scan has to reproduce argparse's tokenisation exactly or it silently over-
    or under-suppresses. That objection is correct about *string scanning* and
    is answered by not doing any: this parses with argparse, so the tokenisation
    is not reproduced, it is the same implementation.

    The explicit flag is added too, because it covers a case detection cannot --
    running the kernel with no provider at all.

    The property that makes detection safe is not that this parser is clever but
    that it is the *same* parser: for a genuinely ambiguous argv -- a flag value
    that happens to look like `--provider` -- this reads it exactly as the kernel
    will, because both are argparse with `--provider` as an appending option. A
    wrapper that guessed differently from the kernel would be the actual hazard,
    and that is the one thing this cannot do.
    """
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


def main(argv: list[str]) -> int:
    if argv == ["--version"]:
        try:
            print(f"cadre {cli_version()}")
        except RuntimeError as error:
            print(f"cadre: {error}", file=sys.stderr)
            return 1
        return 0

    interactive = False
    if argv and argv[0] == INTERACTIVE_FLAG:
        interactive = True
        argv = argv[1:]

    subcommands = load_subcommands()
    command = argv[0] if argv else "help"
    rest = argv[1:]

    if command in ("help", "-h", "--help"):
        print(usage(subcommands))
        return 0

    if command == "sdlc":
        return dispatch_sdlc(rest, interactive=interactive)

    match = next((row for row in subcommands if row[0] == command), None)
    if match is None:
        print(f"cadre: unknown subcommand '{command}'", file=sys.stderr)
        print(usage(subcommands), file=sys.stderr)
        return 1

    _name, script, _description = match
    # subprocess.run, not os.execv: os.execv/os.spawnv join argv into a
    # command-line string without subprocess's list2cmdline quoting on
    # Windows, so any argument containing a space (e.g. --task "multi word
    # value") silently gets re-split by the child process. subprocess.run
    # quotes correctly on every platform this needs to run on.
    result = subprocess.run(
        [sys.executable, str(REPO_ROOT / script), *rest], env=_child_env(interactive)
    )
    return result.returncode


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
