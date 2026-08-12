#!/usr/bin/env python3
"""Opt-in, local-only telemetry for `cadre select` outcomes.

This module has two independent jobs:

1. Recording (`record_selection`) -- called from `select_agents.py`'s CLI
   entry point, and only when the caller has explicitly opted in. It appends
   one JSON-lines record per `cadre select` invocation describing the
   *outcome* of selection (matched routes/risks, status, workflow, team
   names, agent counts) to a local file. This is a side effect only; it
   never changes `cadre select`'s stdout/`--output` JSON, which stays
   governed solely by `roster/orchestration/selection.schema.json`.
2. Summarizing (`summarize`, and this module's own `--summarize` CLI) --
   reads an accumulated JSON-lines file back and reports aggregate stats
   (route-firing frequency, needs-triage rate, workflow/team frequency) so
   the recorded data is actually useful to a suite maintainer, not just
   inert log lines.

Hard design constraints (see the backlog item this implements: "Selection
outcome telemetry (opt-in, local)"):

- OFF by default. Recording only happens when the caller passes
  `--record-telemetry` to `cadre select` or sets `CADRE_SELECTION_TELEMETRY=1`
  in the environment. With neither present, `is_enabled()` returns False,
  `record_selection` is never called, and zero bytes are written anywhere.
- Local file only, never a network call. This module does not import
  `socket`, `urllib`, `requests`, `http.client`, or any other networking
  primitive, and must not gain one -- see
  `roster/orchestration/test/test_selection_telemetry.py`'s source-grep
  boundary test, which fails the build if that ever changes.
- Records deliberately exclude the raw task text and raw changed-file paths.
  Task descriptions and file paths can carry sensitive project content that
  has no business sitting in a plaintext log file a maintainer might forget
  about; see `roster/knowledge-store/SECURITY.md`'s classification/handling
  posture, which this module mirrors on purpose. What gets recorded is
  *structural* facts about the outcome only: which routes/risks matched by
  id, resulting status/workflow, classification label (already a coarse,
  non-content field), source filter (already a project slug/hash, not raw
  content), team ids, and agent counts per group. A maintainer who
  deliberately wants raw task capture for their own local debugging can opt
  into that *additionally* via `--record-telemetry-include-task` (or
  `CADRE_SELECTION_TELEMETRY_INCLUDE_TASK=1`), which is off even when
  ordinary telemetry recording is on -- an explicit, separate, documented
  tradeoff rather than a default.

Default file location: `.agents/orchestration/selection-telemetry.jsonl`
under the target repository root (the same root `cadre select --root`
resolves against), overridable with `CADRE_SELECTION_TELEMETRY_PATH` (an
exact file path) or `--telemetry-path`. This mirrors the knowledge store's
project-local-first convention (`roster/knowledge-store/src/config.py`)
rather than a single machine-wide file, so telemetry for one project's
selections doesn't mix into another's.

Summarize accumulated telemetry:

    python3 roster/orchestration/src/selection_telemetry.py --summarize \\
        .agents/orchestration/selection-telemetry.jsonl

Exits non-zero and reports on stderr if the file contains a malformed line;
prints a JSON summary object to stdout on success.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, TextIO

# 1 -> 2: `source_filter` follows the plan's retype from a bare string to an
# array of sources. Records are local, append-only, and never validated on
# read, so nothing rejects a version-1 row -- the bump exists so a log holding
# both encodings stays self-describing to whoever reads it later.
SCHEMA_VERSION = 2
ENV_ENABLE = "CADRE_SELECTION_TELEMETRY"
ENV_INCLUDE_TASK = "CADRE_SELECTION_TELEMETRY_INCLUDE_TASK"
ENV_PATH = "CADRE_SELECTION_TELEMETRY_PATH"
DEFAULT_RELATIVE_PATH = Path(".agents") / "orchestration" / "selection-telemetry.jsonl"

_TRUE_VALUES = {"1", "true", "yes", "on"}


def _env_flag(name: str) -> bool:
    return os.environ.get(name, "").strip().lower() in _TRUE_VALUES


def is_enabled(cli_flag: bool = False) -> bool:
    """Telemetry is enabled only by explicit opt-in: the CLI flag or the env var."""
    return bool(cli_flag) or _env_flag(ENV_ENABLE)


def include_task_enabled(cli_flag: bool = False) -> bool:
    """Raw task-text capture is a second, separate opt-in on top of `is_enabled`."""
    return bool(cli_flag) or _env_flag(ENV_INCLUDE_TASK)


def resolve_telemetry_path(repository_root: Path, override: str | None = None) -> Path:
    """Resolve where telemetry records are appended.

    Priority: an explicit `--telemetry-path`/`override` argument, else
    `CADRE_SELECTION_TELEMETRY_PATH`, else the project-local default under
    `repository_root`.
    """
    if override:
        return Path(override).expanduser()
    env_path = os.environ.get(ENV_PATH)
    if env_path:
        return Path(env_path).expanduser()
    return repository_root / DEFAULT_RELATIVE_PATH


def build_record(
    plan: dict[str, Any],
    *,
    include_task: bool = False,
) -> dict[str, Any]:
    """Derive a telemetry record from a completed dispatch plan.

    Deliberately omits `inputs.task` and `inputs.changed_files` unless
    `include_task` is explicitly set -- see module docstring.

    Note: `task_id` is always included, even without `include_task`, and is
    caller-controlled free text when the caller passes an explicit
    `--task-id` (the auto-derived `local-<hash>` fallback used when it's
    omitted is opaque, but an explicit value is not constrained in shape).
    If you care about the "no raw task content in the base record" property,
    keep `--task-id` a short opaque identifier rather than a descriptive
    string.
    """
    agents = plan.get("agents", {})
    record: dict[str, Any] = {
        "schema_version": SCHEMA_VERSION,
        "recorded_at": datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z"),
        "task_id": plan.get("task_id"),
        "status": plan.get("status"),
        "workflow": plan.get("workflow"),
        # Both are reduced to bare ids: a plan's `reasons.paths[].file` entries
        # are changed-file paths, and a record deliberately carries structural
        # facts about the outcome, never raw task text or file paths.
        "matched_routes": [route.get("id") for route in plan.get("matched_routes", []) if isinstance(route, dict)],
        "matched_risks": [risk.get("id") for risk in plan.get("matched_risks", []) if isinstance(risk, dict)],
        "classification": plan.get("inputs", {}).get("classification"),
        "source_filter": plan.get("inputs", {}).get("source_filter"),
        "agent_counts": {
            "primary": len(agents.get("primary", [])),
            "reviewers": len(agents.get("reviewers", [])),
            "support": len(agents.get("support", [])),
        },
        "teams": [team.get("id") for team in plan.get("teams", []) if isinstance(team, dict)],
        "lifecycle_tracking_status": plan.get("lifecycle_tracking", {}).get("status"),
        "required_quality_gate_count": len(plan.get("required_quality_gates", [])),
        "human_gate_count": len(plan.get("human_gates", [])),
    }
    if include_task:
        record["task"] = plan.get("inputs", {}).get("task")
        record["changed_files"] = list(plan.get("inputs", {}).get("changed_files", []))
    return record


def record_selection(
    plan: dict[str, Any],
    *,
    repository_root: Path,
    telemetry_path: str | None = None,
    include_task: bool = False,
) -> Path:
    """Append exactly one JSON-lines record for `plan` to the resolved telemetry file.

    Callers must gate this behind `is_enabled(...)` themselves -- this
    function always writes when called, by design, so that "off by default"
    is enforced at the one CLI call site rather than duplicated here.
    """
    path = resolve_telemetry_path(repository_root, telemetry_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    record = build_record(plan, include_task=include_task)
    # A single write() call, not two -- under concurrent invocations (e.g. a
    # busy CI environment) two separate .write() calls have no atomicity
    # guarantee against each other even though a single write() under
    # O_APPEND does (POSIX, for sizes under PIPE_BUF). Coalescing into one
    # call is what actually makes concurrent appends safe, not an
    # implementation detail of buffering that could silently stop applying.
    line = json.dumps(record, sort_keys=True, ensure_ascii=False) + "\n"
    with path.open("a", encoding="utf-8") as handle:
        handle.write(line)
    return path


def _load_records(handle: TextIO) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    for line_number, raw_line in enumerate(handle, start=1):
        line = raw_line.strip()
        if not line:
            continue
        try:
            record = json.loads(line)
        except json.JSONDecodeError as error:
            raise ValueError(f"line {line_number}: malformed JSON ({error})") from error
        if not isinstance(record, dict):
            raise ValueError(f"line {line_number}: record is not a JSON object")
        records.append(record)
    return records


def summarize(path: Path) -> dict[str, Any]:
    """Aggregate a JSON-lines telemetry file into maintainer-facing stats."""
    if not path.is_file():
        raise FileNotFoundError(f"Telemetry file does not exist: {path}")
    with path.open("r", encoding="utf-8") as handle:
        records = _load_records(handle)

    total = len(records)
    status_counts = Counter(record.get("status") for record in records)
    workflow_counts = Counter(record.get("workflow") for record in records)
    route_counts: Counter[str] = Counter()
    risk_counts: Counter[str] = Counter()
    team_counts: Counter[str] = Counter()
    for record in records:
        route_counts.update(route_id for route_id in record.get("matched_routes", []) if route_id)
        risk_counts.update(risk_id for risk_id in record.get("matched_risks", []) if risk_id)
        team_counts.update(team_id for team_id in record.get("teams", []) if team_id)

    needs_triage = status_counts.get("needs-triage", 0)
    return {
        "total_records": total,
        "status_counts": dict(sorted(status_counts.items(), key=lambda item: (item[0] is None, item[0]))),
        "needs_triage_rate": (needs_triage / total) if total else None,
        "workflow_counts": dict(sorted(workflow_counts.items(), key=lambda item: (-item[1], str(item[0])))),
        "route_frequency": dict(sorted(route_counts.items(), key=lambda item: (-item[1], item[0]))),
        "risk_frequency": dict(sorted(risk_counts.items(), key=lambda item: (-item[1], item[0]))),
        "team_frequency": dict(sorted(team_counts.items(), key=lambda item: (-item[1], item[0]))),
    }


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="cadre selection-telemetry",
        description="Summarize accumulated cadre select telemetry (opt-in, local; see module docstring).",
        allow_abbrev=False,
    )
    parser.add_argument("--summarize", required=True, metavar="PATH", help="Path to a selection-telemetry JSON-lines file")
    return parser


def main(argv: list[str] | None = None) -> int:
    options = _parser().parse_args(argv)
    report = summarize(Path(options.summarize).expanduser())
    print(json.dumps(report, indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, FileNotFoundError) as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1) from error
