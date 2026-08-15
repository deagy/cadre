#!/usr/bin/env python3
"""Which Python modules are reachable from a real entry point?

    python3 roster/orchestration/test/probe_python_reachability.py

`PYTHON_ELIMINATION_PLAN.md` deletes only what is *provably* dead, and a
symbol grep is not proof -- one wrongly flagged three files as removable
earlier in this migration. This walks actual entry points and follows real
imports, and every phase of that plan re-runs it to answer "what is dead
now?" after the previous phase changed the answer.

This is a probe, not a test: it reports, it never fails a build. A module
being unreachable is a finding to investigate, not a defect -- see the
false-positive classes below, all of which cost real time to learn.

## Entry points, and why each one counts

1. `cadre_cli/_SUBCOMMANDS` -- the PyPI wheel still dispatches to Python, so
   a module reachable only from here is live for `pip install cadre` users
   even though the checkout and plugin channels never touch it. This is
   currently what keeps almost all of `roster/` alive.
2. `bin/subcommands.tsv` -- the checkout fallback table.
3. `.github/workflows/*.yml` -- anything CI invokes by path.
4. **Commands documented in markdown.** `python3 roster/.../foo.py` in
   RUNBOOK.md is an operator interface with users, and two modules are
   reachable *only* this way (`routing_health.py`,
   `validate_runner_capabilities.py`). Omitting this class was the first
   version's biggest error: it reported both as dead, and neither has a Go
   equivalent, so deleting them would have removed capability outright.
5. Test files -- a test importing its subject keeps it reachable. Run with
   `--production` to exclude these and find modules nothing but their own
   test uses.
6. MCP entry scripts -- invoked by Codex over stdio, never by the CLI.

## False positives this deliberately still has

- A module invoked only through a string built at runtime.
- A module referenced only from a file type not scanned here.
- The `probe_*.py` scripts themselves, which are run by hand.

So treat output as a candidate list to verify individually, never as a
delete list. Every candidate this has produced so far turned out to be
alive.
"""
from __future__ import annotations

import ast
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
ROOTS = [ROOT / "roster", ROOT / "cadre_cli", ROOT / "plugin" / "tools"]

# Entry scripts of the MCP surface. Named explicitly rather than treating the
# whole directory as entry points, so a genuinely orphaned helper in there is
# still reported.
MCP_ENTRY_SCRIPTS = ("dispatch_server", "gitlab_cli", "api_runner")

SKIP_PARTS = ("/.venv/", "/__pycache__/", "/plugin/suite/", "/node_modules/")


def _skip(path: Path) -> bool:
    return any(part in str(path) for part in SKIP_PARTS)


def all_modules() -> dict[str, Path]:
    """Module stem -> path, for every analysable .py in the checkout.

    `plugin/suite/` is excluded because it is a generated copy of `roster/`;
    counting it would make every roster module look doubly reachable.
    """
    found: dict[str, Path] = {}
    for base in ROOTS:
        if not base.exists():
            continue
        for path in base.rglob("*.py"):
            if not _skip(path):
                found[path.stem] = path
    return found


def imports_of(path: Path, modules: dict[str, Path]) -> set[str]:
    """Modules `path` depends on: real imports, plus `"name.py"` literals.

    The string-literal pass exists because several modules here invoke a
    sibling as a subprocess rather than importing it, and a pure AST walk
    would call the target dead.
    """
    try:
        body = path.read_text(encoding="utf-8")
    except OSError:
        return set()

    names: set[str] = set()
    try:
        tree = ast.parse(body, filename=str(path))
    except SyntaxError:
        tree = None
    if tree is not None:
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                names.update(alias.name.split(".")[0] for alias in node.names)
            elif isinstance(node, ast.ImportFrom) and node.module:
                names.add(node.module.split(".")[0])

    names.update(re.findall(r'["\']([a-z_][a-z0-9_]*)\.py["\']', body))
    return {name for name in names if name in modules}


def entry_points(modules: dict[str, Path], include_tests: bool) -> dict[str, set[str]]:
    entries: dict[str, set[str]] = {}

    def add(stem: str, why: str) -> None:
        if stem in modules:
            entries.setdefault(stem, set()).add(why)

    cli = ROOT / "cadre_cli" / "__init__.py"
    if cli.exists():
        for script in re.findall(r'"((?:roster|kernel)/[^"]+\.py)"', cli.read_text(encoding="utf-8")):
            add(Path(script).stem, "PyPI wheel (cadre_cli/_SUBCOMMANDS)")

    tsv = ROOT / "bin" / "subcommands.tsv"
    if tsv.exists():
        for line in tsv.read_text(encoding="utf-8").splitlines():
            fields = line.split("\t")
            if len(fields) >= 2 and fields[1].endswith(".py"):
                add(Path(fields[1]).stem, "bin/subcommands.tsv")

    workflows = ROOT / ".github" / "workflows"
    if workflows.exists():
        for workflow in workflows.glob("*.yml"):
            body = workflow.read_text(encoding="utf-8")
            for script in re.findall(r"((?:roster|plugin|kernel)/[\w/\-]+\.py)", body):
                add(Path(script).stem, f"CI: {workflow.name}")

    # Documented operator commands. See the module docstring -- this class is
    # why routing_health.py and validate_runner_capabilities.py are alive.
    for markdown in ROOT.rglob("*.md"):
        if _skip(markdown):
            continue
        try:
            body = markdown.read_text(encoding="utf-8", errors="ignore")
        except OSError:
            continue
        for script in re.findall(r"python3?\s+((?:roster|plugin|kernel)/[\w/\-]+\.py)", body):
            add(Path(script).stem, f"documented in {markdown.name}")

    for name in MCP_ENTRY_SCRIPTS:
        add(name, "MCP entry script")

    if include_tests:
        for base in ROOTS:
            if not base.exists():
                continue
            for path in base.rglob("test_*.py"):
                if not _skip(path):
                    add(path.stem, "is a test")

    return entries


def reachable_from(entries: dict[str, set[str]], modules: dict[str, Path]) -> dict[str, str]:
    found: dict[str, str] = {}
    queue = [(stem, "; ".join(sorted(whys))) for stem, whys in entries.items()]
    while queue:
        stem, why = queue.pop()
        if stem in found:
            continue
        found[stem] = why
        path = modules.get(stem)
        if path is None:
            continue
        for imported in imports_of(path, modules):
            if imported not in found:
                queue.append((imported, f"imported by {stem}"))
    return found


def main(argv: list[str]) -> int:
    production_only = "--production" in argv
    modules = all_modules()
    entries = entry_points(modules, include_tests=not production_only)
    found = reachable_from(entries, modules)

    candidates = {
        stem: path for stem, path in modules.items()
        if not stem.startswith("test_") and "/test" not in str(path)
    }
    unreachable = sorted(stem for stem in candidates if stem not in found)

    mode = "production entry points only" if production_only else "all entry points"
    print(f"reachability: {mode}")
    print(f"  modules analysed:    {len(modules)}")
    print(f"  non-test modules:    {len(candidates)}")
    print(f"  reachable:           {len(found)}")
    print(f"  NOT reachable:       {len(unreachable)}")

    if not unreachable:
        print("\nNothing is unreachable. Every non-test module has a live entry point.")
        return 0

    print("\nCandidates -- verify each individually before deleting anything:")
    total = 0
    for stem in unreachable:
        path = modules[stem]
        lines = len(path.read_text(encoding="utf-8").splitlines())
        total += lines
        print(f"  {lines:6d}  {path.relative_to(ROOT)}")
    print(f"  {total:6d}  total lines")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
