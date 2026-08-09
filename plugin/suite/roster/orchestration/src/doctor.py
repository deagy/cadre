#!/usr/bin/env python3
"""`cadre doctor` -- report which `cadre` binary is actually executing.

This exists to kill one specific DX trap: a bare `cadre` on `PATH` can
resolve to (a) this checkout's own `bin/cadre`, (b) a pip/pipx-installed
`cadre_cli` distribution, or (c) a stale Claude Code plugin-cache copy
(`~/.claude/plugins/cache/<marketplace>/<plugin>/<version>/bin/cadre`).
These are separately maintained, potentially differently-versioned
implementations of the same CLI. A developer editing
`roster/orchestration/src/select_agents.py` and testing with a bare
`cadre select` may silently exercise a stale vendored or installed copy
instead of their edit, with no warning at all. `doctor` makes that
diagnosable in one command instead of a "why isn't my change showing up"
debugging session.

Dispatched exactly like every other subcommand in `bin/subcommands.tsv`:
`bin/cadre.py` invokes `sys.executable <resolved-repo-root>/<this file>`,
so `Path(__file__).resolve()` inside this process *is* "the actual running
binary" bin/cadre.py chose -- no separate resolution logic is needed to
answer "which script is executing," only to classify what kind of install
that resolved location belongs to.

Run: `cadre doctor` (`--json` for machine-readable output).

Exit code: 0 when the picture is internally consistent (or nothing could be
determined either way); 1 when the cwd sits inside a Cadre checkout but the
binary that actually ran is demonstrably a *different* location than that
checkout's own `bin/cadre.py` -- the exact DX trap this command exists to
catch, and the one condition worth failing a CI/script check over.
"""

from __future__ import annotations

import json
import platform
import sys
from pathlib import Path

# roster/orchestration/src/doctor.py -> src -> orchestration -> roster -> repo root.
_THIS_FILE = Path(__file__).resolve()
_RUNNING_REPO_ROOT = _THIS_FILE.parents[3]

# Tracks pyproject.toml's `requires-python = ">=3.10"` at the repo root.
# Kept as a plain constant rather than parsed from pyproject.toml at runtime:
# pyproject.toml is deliberately *not* vendored into the pip/pipx
# distribution (see that file's [tool.hatch.build.targets.wheel.force-include]
# table), so a bundled install has nothing to parse -- a hardcoded floor is
# the only value doctor can report from every install kind alike. Update
# this alongside pyproject.toml's requires-python if that floor ever moves.
MIN_PYTHON = (3, 10)

# The one Claude Code plugin-cache path shape this repository documents
# elsewhere (roster/orchestration/mcp/SECURITY-CONTROLS.md,
# roster/orchestration/mcp/dispatch_core.py):
#   ~/.claude/plugins/cache/<marketplace>/<plugin>/<version>/...
# There is no known API or env var that reports "this process is running
# from a plugin cache" more reliably than this path shape, and Claude Code
# does not document one as of this writing -- so this check is a heuristic,
# and it says so in its own output rather than asserting certainty.
_PLUGIN_CACHE_MARKER = ("plugins", "cache")


def _repo_markers_present(root: Path) -> bool:
    """True when `root` looks like a Cadre checkout root by real filesystem
    signals, not by name: a `.git` entry (directory for a normal checkout,
    file for a worktree -- see `git worktree`), plus the two files every
    checkout has and no packaged/vendored copy renames."""
    return (root / ".git").exists() and (root / "roster" / "catalog.yaml").is_file() and (root / "bin" / "cadre.py").is_file()


def find_checkout_root(start: Path) -> Path | None:
    """Walk upward from `start` looking for Cadre checkout markers, the same
    "walk to the nearest boundary" shape `roster/shared/src/resolve.py`'s
    `find_file_at_project_root` uses for project-local config discovery.
    Stops at the filesystem root; does not cross it."""
    current = start.resolve()
    while True:
        if _repo_markers_present(current):
            return current
        if current.parent == current:
            return None
        current = current.parent


def _plugin_cache_root(path: Path) -> Path | None:
    """If `path` sits under a `.../plugins/cache/<marketplace>/<plugin>/<version>/...`
    shape, return the `<version>` directory (the plugin install's own root,
    analogous to a checkout root) -- else None."""
    parts = path.parts
    for index in range(len(parts) - len(_PLUGIN_CACHE_MARKER)):
        if tuple(parts[index : index + len(_PLUGIN_CACHE_MARKER)]) == _PLUGIN_CACHE_MARKER:
            # parts[index] == "plugins", [index+1] == "cache", [index+2] ==
            # marketplace, [index+3] == plugin, [index+4] == version.
            version_index = index + len(_PLUGIN_CACHE_MARKER) + 2
            if version_index < len(parts):
                return Path(*parts[: version_index + 1])
            return None
    return None


def _site_packages_root(path: Path) -> Path | None:
    """If `path` sits under a `.../site-packages/...` directory (a pip/pipx
    install, editable or not), return that `site-packages` directory."""
    parts = path.parts
    for index, part in enumerate(parts):
        if part == "site-packages":
            return Path(*parts[: index + 1])
    return None


def classify_running_binary(running_file: Path) -> tuple[str, Path, str]:
    """Classify the checkout root that `running_file` (this module's own
    resolved `__file__`) actually executed from.

    Returns (kind, root, detail):
      - ("checkout", repo_root, "...")   -- a real Cadre git checkout
      - ("pip-install", site_packages, "...") -- under a site-packages tree
      - ("plugin-cache", plugin_root, "...")  -- under a Claude Code plugin cache
      - ("unknown", running_file.parent, "...") -- none of the above matched;
        reported honestly rather than guessed at, per this command's brief.
    """
    site_packages = _site_packages_root(running_file)
    if site_packages is not None:
        return (
            "pip-install",
            site_packages,
            f"running from a site-packages install under {site_packages}",
        )

    plugin_root = _plugin_cache_root(running_file)
    if plugin_root is not None:
        return (
            "plugin-cache",
            plugin_root,
            f"running from a Claude Code plugin cache copy under {plugin_root} "
            "(path-shape heuristic: ~/.claude/plugins/cache/<marketplace>/<plugin>/<version>/...; "
            "not a documented, guaranteed-stable Claude Code contract)",
        )

    # roster/orchestration/src/doctor.py -> repo root is 3 parents up.
    candidate_root = running_file.parents[3] if len(running_file.parents) >= 3 else running_file.parent
    if _repo_markers_present(candidate_root):
        return ("checkout", candidate_root, f"running from a Cadre git checkout at {candidate_root}")

    return (
        "unknown",
        running_file.parent,
        f"could not classify the install kind for {running_file} as a checkout, "
        "a pip/pipx site-packages install, or a Claude Code plugin cache -- reporting "
        "the raw resolved path only rather than guessing",
    )


def _python_version_line() -> tuple[str, bool]:
    info = sys.version_info
    meets_floor = (info.major, info.minor) >= MIN_PYTHON
    label = f"{info.major}.{info.minor}.{info.micro} ({platform.python_implementation()})"
    return label, meets_floor


def gather_report(cwd: Path | None = None, running_file: Path | None = None) -> dict:
    cwd = Path.cwd() if cwd is None else cwd
    # `running_file` is injectable so tests can deterministically simulate
    # "this process is actually running from a different install location"
    # without spawning a real subprocess against a second copy of this repo
    # -- production callers (main()) always get the real _THIS_FILE.
    running_file = _THIS_FILE if running_file is None else running_file
    kind, install_root, detail = classify_running_binary(running_file)
    python_label, python_ok = _python_version_line()

    cwd_checkout_root = find_checkout_root(cwd)
    mismatch = False
    mismatch_detail = None
    if cwd_checkout_root is not None:
        # The DX trap this command exists to catch: cwd is inside a real
        # Cadre checkout, but the code that actually ran is not that
        # checkout's own bin/cadre.py -- either a different install kind
        # entirely, or (kind == "checkout" but) a *different* checkout root,
        # e.g. two clones on disk and PATH picked the wrong one.
        if kind != "checkout" or install_root != cwd_checkout_root:
            mismatch = True
            mismatch_detail = (
                f"cwd {cwd} is inside a Cadre checkout rooted at {cwd_checkout_root}, "
                f"but the binary that actually ran is {detail}. "
                f"Run {cwd_checkout_root / 'bin' / 'cadre'} explicitly (or put it first on "
                "PATH) instead of a bare `cadre` to exercise this checkout's own code."
            )

    return {
        "running_file": str(running_file),
        "python_executable": sys.executable,
        "python_version": python_label,
        "python_version_ok": python_ok,
        "python_min_version": ".".join(str(part) for part in MIN_PYTHON),
        "install_kind": kind,
        "install_root": str(install_root),
        "install_detail": detail,
        "cwd": str(cwd),
        "cwd_checkout_root": str(cwd_checkout_root) if cwd_checkout_root is not None else None,
        "mismatch": mismatch,
        "mismatch_detail": mismatch_detail,
    }


def _render_human(report: dict) -> str:
    lines = [
        "cadre doctor",
        "",
        f"  running file:       {report['running_file']}",
        f"  python interpreter: {report['python_executable']}",
        f"  python version:     {report['python_version']}"
        + ("" if report["python_version_ok"] else f" (below the required {report['python_min_version']}+)"),
        f"  install kind:       {report['install_kind']}",
        f"  detail:             {report['install_detail']}",
        f"  cwd:                {report['cwd']}",
        f"  cwd checkout root:  {report['cwd_checkout_root'] or 'not inside a Cadre checkout'}",
        "",
    ]
    if not report["python_version_ok"]:
        lines.append(
            f"WARNING: this interpreter is below the declared floor "
            f"(pyproject.toml requires-python >= {report['python_min_version']}); "
            "some subcommands may fail in ways unrelated to your change."
        )
        lines.append("")
    if report["mismatch"]:
        lines.append(f"WARNING: {report['mismatch_detail']}")
    else:
        lines.append("OK: the binary that ran matches the checkout your cwd is in (or your cwd isn't in one).")
    return "\n".join(lines)


def main(argv: list[str] | None = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    as_json = "--json" in argv
    unexpected = [arg for arg in argv if arg not in ("--json", "-h", "--help")]
    if "-h" in argv or "--help" in argv:
        print("usage: cadre doctor [--json]", file=sys.stderr)
        return 0
    if unexpected:
        print(f"cadre doctor: unknown argument(s): {' '.join(unexpected)}", file=sys.stderr)
        print("usage: cadre doctor [--json]", file=sys.stderr)
        return 2

    report = gather_report()
    if as_json:
        print(json.dumps(report, indent=2, sort_keys=True))
    else:
        print(_render_human(report))

    return 1 if report["mismatch"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
