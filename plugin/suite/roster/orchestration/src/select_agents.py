#!/usr/bin/env python3
"""Command-line entry point for deterministic local agent selection."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import urlparse

ORCHESTRATION_ROOT = Path(__file__).resolve().parent.parent
ROSTER_ROOT = ORCHESTRATION_ROOT.parent
REPOSITORY_ROOT = ROSTER_ROOT.parent

# Not relying on agentic_sdlc_contracts' own sys.path append (transitively
# reached via build_dispatch_plan below) for this -- appended directly so
# the `from settings import SettingsError` import below is correct even if
# that transitive chain is ever reordered.
_SHARED_SRC_DIR = ROSTER_ROOT / "shared" / "src"
if str(_SHARED_SRC_DIR) not in sys.path:
    sys.path.append(str(_SHARED_SRC_DIR))

from build_dispatch_plan import build_dispatch_plan  # noqa: E402
from route_near_miss import find_near_misses, format_near_misses_text  # noqa: E402
from routing import load_catalog, load_routing  # noqa: E402
from selection_telemetry import (  # noqa: E402
    include_task_enabled,
    is_enabled as telemetry_is_enabled,
    record_selection,
)
from settings import SettingsError  # noqa: E402


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Emit a deterministic local agent dispatch plan.",
        allow_abbrev=False,
    )
    parser.add_argument("--task", required=True, help="Task objective used for routing")
    parser.add_argument(
        "--root",
        help="Target repository root (defaults to the caller's working directory)",
    )
    parser.add_argument("--files", action="append", help="Changed path or comma-separated paths; repeatable")
    parser.add_argument("--base", help="Git base ref used with <base>...HEAD")
    parser.add_argument("--task-id", help="Stable caller-supplied task identifier")
    parser.add_argument("--classification", help="Authorized knowledge classification")
    parser.add_argument("--source", help="Optional knowledge-store source filter")
    parser.add_argument("--top", help="Maximum knowledge results per agent", default="5")
    parser.add_argument("--output", help="Write the JSON plan to this path")
    parser.add_argument(
        "--require-sdlc",
        action="store_true",
        help="Fail instead of degrading to standalone mode if Agentic SDLC isn't available",
    )
    parser.add_argument(
        "--record-telemetry",
        action="store_true",
        help=(
            "Opt in to appending a local, structural-only outcome record to "
            "selection-telemetry.jsonl (see selection_telemetry.py); off by "
            "default, equivalent to CADRE_SELECTION_TELEMETRY=1"
        ),
    )
    parser.add_argument(
        "--record-telemetry-include-task",
        action="store_true",
        help=(
            "With --record-telemetry (or CADRE_SELECTION_TELEMETRY=1), also "
            "record the raw task text and changed files; off by default even "
            "when telemetry recording is enabled, equivalent to "
            "CADRE_SELECTION_TELEMETRY_INCLUDE_TASK=1"
        ),
    )
    parser.add_argument(
        "--telemetry-path",
        help="Override the telemetry JSON-lines file path (default: CADRE_SELECTION_TELEMETRY_PATH or <root>/.agents/orchestration/selection-telemetry.jsonl)",
    )
    parser.add_argument(
        "--explain",
        action="store_true",
        help=(
            "Additionally print, to stderr, near-miss reasoning for routes that did NOT "
            "match this task (see route_near_miss.py). Diagnostic only: never alters the "
            "JSON plan on stdout/--output, and never adds a numeric score or ranking -- "
            "off by default"
        ),
    )
    return parser


def _run_git(args: list[str], repository_root: Path) -> str:
    # --root is caller-controlled and may point at an untrusted checkout;
    # neutralize the config-driven RCE surface (fsmonitor hook, system-wide
    # config, interactive credential prompts) before reading its .git state.
    env = dict(os.environ)
    env["GIT_CONFIG_NOSYSTEM"] = "1"
    env["GIT_TERMINAL_PROMPT"] = "0"
    result = subprocess.run(
        ["git", "-c", "core.fsmonitor=false", "--no-optional-locks", *args],
        cwd=repository_root,
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
        env=env,
    )
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or f"git {' '.join(args)} failed")
    return result.stdout


def discover_changed_files(base: str | None, repository_root: Path | None = None) -> dict[str, object]:
    repository_root = (repository_root or REPOSITORY_ROOT).resolve()
    if base:
        files = [
            line
            for line in _run_git(
                ["diff", "--name-only", f"{base}...HEAD"], repository_root
            ).splitlines()
            if line
        ]
        return {"source": f"git-diff:{base}...HEAD", "files": files}
    # -z gives NUL-separated, never-quoted paths; git's default --short quotes
    # paths containing non-ASCII/special characters (core.quotePath), which
    # plain line[3:] parsing would leave mangled. Renamed/copied entries add
    # one extra NUL-separated original-path field we don't need and must skip.
    fields = _run_git(
        ["status", "--short", "-z", "--untracked-files=all"], repository_root
    ).split("\0")
    files = []
    index = 0
    while index < len(fields):
        entry = fields[index]
        index += 1
        if not entry:
            continue
        status, path = entry[:2], entry[3:]
        files.append(path)
        if "R" in status or "C" in status:
            index += 1
    return {"source": "git-status", "files": files}


def explicit_files(values: list[str] | None) -> list[str] | None:
    if not values:
        return None
    files = []
    for value in values:
        files.extend(entry.strip() for entry in value.split(",") if entry.strip())
    return list(dict.fromkeys(files))


def _origin_slug(repository_root: Path) -> str | None:
    try:
        origin = _run_git(["remote", "get-url", "origin"], repository_root).strip()
    except RuntimeError:
        return None
    if not origin:
        return None
    # Accept https://host/owner/repo.git, ssh://git@host/owner/repo.git,
    # and SCP-style git@host:owner/repo.git origins.
    path = urlparse(origin).path if "://" in origin else origin.split(":", 1)[-1]
    parts = [part for part in path.strip("/").split("/") if part]
    if len(parts) < 2:
        return None
    owner, repository = parts[-2], re.sub(r"\.git$", "", parts[-1], flags=re.IGNORECASE)
    if not owner or not repository:
        return None
    slug = f"{owner}/{repository}".lower()
    return slug if re.fullmatch(r"[a-z0-9._-]+/[a-z0-9._-]+", slug) else None


def resolve_knowledge_source(repository_root: Path) -> str:
    slug = _origin_slug(repository_root)
    if slug:
        return slug
    digest = hashlib.sha256(str(repository_root.resolve()).encode("utf-8")).hexdigest()[:12]
    basename = re.sub(r"[^a-z0-9._-]+", "-", repository_root.name.lower()).strip("-") or "repository"
    return f"local-{basename}-{digest}"


def main(argv: list[str] | None = None) -> int:
    options = _parser().parse_args(argv)
    repository_root = Path(options.root).expanduser().resolve() if options.root else Path.cwd().resolve()
    if not repository_root.is_dir():
        raise ValueError(f"Repository root is not a directory: {repository_root}")
    supplied_files = explicit_files(options.files)
    if supplied_files is not None and options.base:
        raise ValueError("--base cannot be combined with --files")
    changes = (
        {"source": "explicit", "files": supplied_files}
        if supplied_files is not None
        else discover_changed_files(options.base, repository_root)
    )
    source = options.source or resolve_knowledge_source(repository_root)
    catalog_path = ROSTER_ROOT / "catalog.yaml"
    routing_path = ORCHESTRATION_ROOT / "routing.yaml"
    config = load_routing(routing_path)
    catalog = load_catalog(catalog_path)
    plan = build_dispatch_plan(
        config,
        catalog,
        {
            "task": options.task,
            "task_id": options.task_id,
            "repository_root": str(repository_root),
            "base": options.base,
            "changed_files": [str(file_name).replace("\\", "/") for file_name in changes["files"]],
            "changed_file_source": changes["source"],
            "classification": options.classification,
            "source": source,
            "top": options.top,
        },
        require_sdlc=options.require_sdlc,
        catalog_path=catalog_path,
        routing_path=routing_path,
    )
    if telemetry_is_enabled(options.record_telemetry):
        record_selection(
            plan,
            repository_root=repository_root,
            telemetry_path=options.telemetry_path,
            include_task=include_task_enabled(options.record_telemetry_include_task),
        )
    serialized = f"{json.dumps(plan, indent=2, ensure_ascii=False)}\n"
    if options.output:
        output_path = Path(options.output).resolve()
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_bytes(serialized.encode("utf-8"))
    else:
        sys.stdout.buffer.write(serialized.encode("utf-8"))
    if options.explain:
        # Printed to stderr, after the machine-readable plan, and derived
        # only from data the plan already exposes (matched_routes' ids) plus
        # a fresh read of routing.yaml -- this never touches `plan` or
        # `serialized`, so the JSON plan is byte-identical with and without
        # --explain.
        matched_route_ids = {match["id"] for match in plan["matched_routes"]}
        near_misses = find_near_misses(config, options.task, matched_route_ids)
        sys.stderr.write(format_near_misses_text(near_misses))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, RuntimeError, ValueError, json.JSONDecodeError, SettingsError) as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1) from error
