"""In-process console-script entry point for the pip/pipx-installable `cadre`
distribution.

This package is a second, independent distribution channel for the
`bin/cadre` / `bin/cadre.py` / `bin/cadre.ps1` checkout CLI (see
pyproject.toml and README.md's "pip / pipx install" section) — it does not
replace the checkout path, which stays completely unmodified.

Rather than duplicating `bin/cadre.py`'s subcommand table and dispatch logic
(REQ-PIP: prefer reuse over duplication), this module vendors a byte-for-byte
build-time copy of the real `bin/` and `roster/` (plus `.agents/skills/` and
`provider/`) trees
under `cadre_cli/_vendor/` (see the wheel's
`[tool.hatch.build.targets.wheel.force-include]` table) and then *loads and
calls* the vendored `bin/cadre.py`'s own `main()` function in-process.

Why this works with zero changes to `bin/cadre.py`: every dispatched script
under `roster/` resolves its own resource roots (roster/catalog.yaml,
roster/orchestration/routing.json, role AGENT.md files, etc.) purely from its
own `Path(__file__).resolve()` position, walking a fixed number of parents —
never from an environment variable or the caller's cwd. `bin/cadre.py` itself
computes `REPO_ROOT = Path(__file__).resolve().parent.parent` the same way.
As long as the vendored copy preserves the exact relative directory layout
the checkout has (`<root>/bin/cadre.py` next to `<root>/roster/...`), loading
the vendored `bin/cadre.py` from its vendored location makes every one of
those `__file__`-relative computations land inside the vendored tree
automatically, with no branching logic here and no edits to any dispatched
script.

`agentic_sdlc/__init__.py` (the sibling `agentic-sdlc` kernel distribution)
uses the same "bundled copy sits next to the package, checked for at import
time" shape for its single `contracts/` directory; this module applies the
same principle to a larger, multi-directory resource surface by vendoring
whole directory trees instead of one.
"""

from __future__ import annotations

import importlib.util
import sys
from pathlib import Path
from types import ModuleType

_PACKAGE_DIR = Path(__file__).resolve().parent

# Bundled (built wheel / installed package): resources live under
# cadre_cli/_vendor/{bin,roster,.agents,plugins}, copied in at build time by
# hatchling's force-include (see pyproject.toml). Editable/development
# install (`pip install -e .` from this checkout): no _vendor/ directory
# exists, so fall back to the real checkout root next to this package
# directory, exactly like agentic_sdlc/__init__.py's PLUGIN_ROOT fallback.
_BUNDLED_VENDOR_ROOT = _PACKAGE_DIR / "_vendor"
if (_BUNDLED_VENDOR_ROOT / "bin" / "cadre.py").is_file():
    VENDOR_ROOT = _BUNDLED_VENDOR_ROOT
else:
    VENDOR_ROOT = _PACKAGE_DIR.parent

_VENDORED_CADRE_PY = VENDOR_ROOT / "bin" / "cadre.py"

# `generate-plugin` (generate_global_plugin.py) reads roster/README.md,
# roster/RUNBOOK.md, the plugin repository's README.md, and the whole docs/
# tree, then *writes* regenerated output into a checkout of the plugin
# repository (deagy/cadre-lifecycle, successor to the now-archived
# deagy/cadre-plugin) named by --output -- a maintainer/
# regeneration operation driven from this repository's own tracked source
# (it reads this repository's git index), not something that makes sense
# pointed at an installed site-packages copy.
# `generate-authority-aides` (generate_authority_aides.py) is the same class
# of operation: it *writes* regenerated roster/authority/*/AGENT.md files
# back into this repository's own tree, so pointed at an installed
# distribution it would silently write into site-packages instead of
# failing -- same rationale, same fix.
# Rather than vendor the plugin repository's tree plus docs/ just to make
# these "work" from an install (which would make write-into-site-packages
# behavior appear supported when it isn't, and meaningfully bloat the
# wheel), these subcommand names fail closed here, in the installed
# (bundled _vendor/) dispatch path only -- the checkout path (`bin/cadre.py`
# run directly, or via bin/cadre / bin/cadre.ps1) is completely untouched
# and keeps working exactly as before.
#
# `generate-role-metadata` (generate_role_metadata.py) is a partial case:
# its default (write) mode has the exact same "writes back into this
# repository's own tree" problem -- from an installed distribution it would
# silently regenerate the *installed package's own vendored copy* of
# roster/catalog.yaml / roster/orchestration/routing.json under
# site-packages, never a real user project, which is pointless/misleading
# even though it doesn't crash. Its `--check` mode is different: it only
# reads, verifying the installed package's own bundled metadata is
# internally self-consistent -- a legitimate installed-mode use case. So
# this subcommand name is listed here too, but the dispatch check below only
# fails closed for it when `--check` is absent from argv; see
# _requires_checkout().
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


def _load_vendored_cadre_module() -> ModuleType:
    """Import the vendored (or, in an editable install, the real) bin/cadre.py
    as a module, without adding anything to sys.path and without ever
    touching the real bin/cadre.py file on disk.
    """
    if not _VENDORED_CADRE_PY.is_file():
        raise RuntimeError(
            f"cadre: could not locate bin/cadre.py under {VENDOR_ROOT} "
            "-- this installed distribution is missing its vendored resources"
        )
    spec = importlib.util.spec_from_file_location("_cadre_vendored_bin_cadre", _VENDORED_CADRE_PY)
    if spec is None or spec.loader is None:  # pragma: no cover - defensive
        raise RuntimeError(f"cadre: failed to load module spec for {_VENDORED_CADRE_PY}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main(argv: list[str] | None = None) -> int:
    """Console-script entry point (`[project.scripts] cadre = "cadre_cli:main"`).

    Reproduces `bin/cadre.py`'s dispatch behavior in-process (no
    subprocess-of-a-subprocess re-exec of a checkout shim) by calling the
    vendored module's own `main()`, so the subcommand table, `sdlc`
    delegation, and usage text are never duplicated here.
    """
    effective_argv = sys.argv[1:] if argv is None else argv
    command = effective_argv[0] if effective_argv else None
    if command is not None and _is_bundled_install() and _requires_checkout(command, effective_argv[1:]):
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
    cadre_module = _load_vendored_cadre_module()
    return cadre_module.main(effective_argv)


if __name__ == "__main__":  # pragma: no cover - convenience for `python -m cadre_cli`
    raise SystemExit(main())
