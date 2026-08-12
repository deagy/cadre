#!/usr/bin/env python3
"""Render a dispatch plan as text for a person, for `cadre select --format text`.

The JSON plan is the contract every downstream tool reads, and it stays the
default. But a routine task produces ~260 lines of it, and someone asking "who
should work on this?" had to read a schema to find out -- the answer is spread
across `agents`, `teams`, `dispatch_disposition`, and `human_gates`.

This renders the same plan, decision first. It is a pure function of the plan
dict: it never re-runs selection, never reads routing.json, and never adds a
fact the plan does not already contain. That matters for a plan whose whole
value is being deterministic -- a formatter that recomputed anything could
disagree with the JSON it claims to be showing.

Every field is read with `.get`. A plan from an older `schema_version`, or one
truncated by a failure, renders what it has instead of raising: a formatter is
a poor place to discover a schema change, and a traceback here would hide the
plan entirely.
"""

from __future__ import annotations

import textwrap
from typing import Any

_WIDTH = 78
_LABEL = 11


def _fill(text: str, *, initial: str = "", subsequent: str = "") -> str:
    """`textwrap.fill` with word-splitting disabled.

    Role ids are hyphenated (`kubernetes-manifest-implementer`), and the
    defaults break on hyphens -- which wraps a single role across two lines
    and reads as two different, non-existent roles. Long ids overrun the
    width instead; a slightly ragged edge is much cheaper than a name the
    reader cannot trust.
    """
    return textwrap.fill(
        text,
        width=_WIDTH,
        initial_indent=initial,
        subsequent_indent=subsequent,
        break_on_hyphens=False,
        break_long_words=False,
    )


def _wrap_values(label: str, values: list[str], *, separator: str = ", ") -> list[str]:
    """One labelled row, continuation lines aligned under the first value."""
    if not values:
        return []
    indent = " " * (_LABEL + 2)
    body = _fill(separator.join(values), subsequent=indent)
    return [f"  {label:<{_LABEL}}{body}"]


def _section(title: str) -> list[str]:
    return ["", title]


def _route_summary(match: dict[str, Any]) -> str:
    """`id (why)` -- the why is what makes a surprising route reviewable.

    Keywords are quoted and paths shown as the glob that fired, mirroring the
    JSON's `reasons` block rather than re-deriving anything.
    """
    route_id = str(match.get("id", "?"))
    reasons = match.get("reasons") or {}
    parts: list[str] = []

    keywords = [str(keyword) for keyword in reasons.get("keywords") or []]
    for group in reasons.get("keyword_groups") or []:
        keywords.extend(str(keyword) for keyword in group or [])
    if keywords:
        unique = sorted(set(keywords))
        shown = ", ".join(f'"{keyword}"' for keyword in unique[:3])
        if len(unique) > 3:
            shown += f", +{len(unique) - 3} more"
        parts.append(shown)

    patterns = []
    for path in reasons.get("paths") or []:
        pattern = path.get("pattern") if isinstance(path, dict) else None
        if pattern and pattern not in patterns:
            patterns.append(str(pattern))
    if patterns:
        shown = ", ".join(patterns[:2])
        if len(patterns) > 2:
            shown += f", +{len(patterns) - 2} more"
        parts.append(shown)

    return f"{route_id} ({'; '.join(parts)})" if parts else route_id


def format_plan_text(plan: dict[str, Any]) -> str:
    """Return the human-readable rendering of `plan`, newline-terminated."""
    lines: list[str] = []

    status = str(plan.get("status", "unknown"))
    task_id = str(plan.get("task_id") or "(no --task-id given)")
    inputs = plan.get("inputs") or {}

    lines.append(f"{task_id}  [{status}]")
    task = str(inputs.get("task", "")).strip()
    if task:
        lines.append(_fill(task, initial="  ", subsequent="  "))

    lines.extend(_section("PLAN"))
    lines.extend(_wrap_values("workflow", [str(plan.get("workflow", "unknown"))]))
    disposition = plan.get("dispatch_disposition") or {}
    if disposition.get("status"):
        lines.extend(_wrap_values("dispatch", [str(disposition["status"])]))
    changed = [str(path) for path in inputs.get("changed_files") or []]
    if changed:
        shown = changed[:5]
        if len(changed) > 5:
            shown.append(f"(+{len(changed) - 5} more)")
        lines.extend(_wrap_values("files", shown))

    # needs-triage is the case a JSON skim gets wrong: the plan is structurally
    # valid and every agent list is simply empty, which reads as success. Say
    # it in words, with the reason the plan already carries.
    if not any((plan.get("agents") or {}).get(group) for group in ("primary", "reviewers", "support")):
        lines.extend(_section("NO AGENTS SELECTED"))
        reason = str(disposition.get("reason") or "No route or risk rule matched this task.")
        lines.append(_fill(reason, initial="  ", subsequent="  "))
        lines.append("")
        lines.append("  Re-run with --explain to see which routes came closest and why.")
    else:
        agents = plan.get("agents") or {}
        lines.extend(_section("AGENTS"))
        for group in ("primary", "reviewers", "support"):
            members = [str(agent) for agent in agents.get(group) or []]
            # "(none)" rather than a bare "-": an empty reviewers slot is worth
            # noticing, and a lone dash at the end of a line is easily read as
            # a hyphenated id that wrapped.
            lines.extend(_wrap_values(group, members) or [f"  {group:<{_LABEL}}(none)"])

    teams = plan.get("teams") or []
    if teams:
        lines.extend(_section("TEAMS"))
        for team in teams:
            kind = str(team.get("type", "?"))
            mode = str(team.get("communication_mode", "?"))
            lines.append(f"  {team.get('id', '?')} ({kind}, {mode})")
            members = [str(member) for member in team.get("members") or []]
            lines.extend(_wrap_values("", members))

    matched_routes = plan.get("matched_routes") or []
    matched_risks = plan.get("matched_risks") or []
    if matched_routes or matched_risks:
        lines.extend(_section("MATCHED"))
        if matched_routes:
            lines.extend(_wrap_values("routes", [_route_summary(m) for m in matched_routes], separator="; "))
        if matched_risks:
            lines.extend(_wrap_values("risks", [str(risk.get("id", "?")) for risk in matched_risks]))

    # Human gates are the one part of a plan that is never advisory -- they
    # name a decision no agent may make. They get their own block, with the
    # reason attached, rather than an id in a list.
    human_gates = [gate for gate in plan.get("human_gates") or [] if gate.get("required", True)]
    if human_gates:
        lines.extend(_section("HUMAN APPROVAL REQUIRED"))
        for gate in human_gates:
            lines.append(f"  {gate.get('id', '?')}")
            reason = str(gate.get("reason") or "").strip()
            if reason:
                lines.append(_fill(reason, initial="    ", subsequent="    "))

    quality_gates = [gate for gate in plan.get("required_quality_gates") or [] if gate.get("required", True)]
    if quality_gates:
        lines.extend(_section("QUALITY GATES"))
        lines.extend(_wrap_values("required", [str(gate.get("id", "?")) for gate in quality_gates]))

    packs = [str(pack.get("id", pack)) if isinstance(pack, dict) else str(pack) for pack in plan.get("context_packs") or []]
    if packs:
        lines.extend(_section("CONTEXT PACKS"))
        lines.extend(_wrap_values("packs", packs))

    fingerprint = str(plan.get("dispatch_fingerprint") or "")
    if fingerprint:
        lines.extend(["", f"  fingerprint {fingerprint}"])

    return "\n".join(lines) + "\n"
