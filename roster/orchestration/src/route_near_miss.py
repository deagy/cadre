#!/usr/bin/env python3
"""Near-miss route explanations for `cadre select --explain` (Proposal 07).

Proposal 01 (`matched_routes[].reasons`, see `build_dispatch_plan.py`'s
`_reasons()`) answers "why did this route match?" from the plan itself. This
module answers the complementary question that proposal explicitly left out
of scope -- "why did this route NOT match, and how close did it come?" -- as
a `--explain` CLI presentation concern, never as a plan field. A prior design
note recorded in this repository's history (see
`roster/orchestration/runs/cadre-proposal-01-route-match-reasons-2026-08-08/
requirements.md` §6, "Out of scope") explicitly reserved near-miss reasoning
for this proposal and kept it out of `matched_routes`/`matched_risks` to
avoid an ever-growing set of matched_*/unmatched_* schema fields -- so this
module is deliberately a side-channel the CLI prints, never something
`build_dispatch_plan()` emits or `selection.schema.json` describes.

Why `keyword_groups` is the only graded signal: `match_rule()`
(`routing.py`) defines a route as matched when `matched_keywords OR
conjunctive_match OR matched_paths` is true. Plain `keywords` and `paths`
are disjunctive (OR) triggers -- if even one had fired, the route would
already be in `matched_routes` and would never reach this module. So for an
UNMATCHED route, bare keyword and path relevance is always exactly zero;
there is no partial state to report, and printing "0 of N keywords matched"
for every one of the ~30-60 unmatched routes on every call would be exactly
the noise this feature exists to avoid. `keyword_groups` is different: it is
a conjunctive (AND) requirement, so a group can be genuinely "partially
satisfied" -- some but not all of its keywords present in the task text --
which is the one place a route can come recognizably close without having
matched. That is the sole relevance threshold this module applies: a route
is surfaced only when at least one of its `keyword_groups` entries has
1 <= matched < len(group). A route with no `keyword_groups`, or where every
group sits at 0-of-N or N-of-N, is omitted entirely -- 0-of-N is irrelevant
noise and N-of-N would mean the route actually matched (a contradiction for
a route reaching this module at all).

As of this writing no entry under `routes:` in `routing.yaml` declares
`keyword_groups` (only `risk_rules:` entries do), so a real invocation
against the current file may legitimately report no near misses for any
task -- that is a correct, honest answer, not a bug in this module; the
mechanism is exercised directly by this file's test suite via a synthetic
route, and will start reporting real near misses the day a route gains a
`keyword_groups` entry.

Deliberately descriptive, never scored: this module reports which literal
keywords are present/absent per group and nothing else. No percentage,
count-based confidence, "closeness", or cross-route ranking is computed or
emitted, under any field name -- this repository's selection is deterministic,
not agent judgment (`CLAUDE.md`), and a prior review explicitly rejected
numeric confidence/score/weight/ranking on a match under any name (see the
same requirements.md, §6, "Out of scope").

This module only reads `routing.yaml` data already loaded by the caller and
never mutates it, never retrieves knowledge, and never dispatches anything.
"""

from __future__ import annotations

from typing import Any

from routing import _keyword_matches


def explain_route_near_miss(route: dict[str, Any], task_text: str) -> dict[str, Any] | None:
    """Explain how close an UNMATCHED `route` came to matching `task_text`,
    or return `None` if it does not clear the relevance threshold documented
    at module level (no partially satisfied `keyword_groups` entry).

    Callers are responsible for only invoking this on routes NOT already
    present in a plan's `matched_routes` -- this function does not itself
    check `route`'s own `keywords`/`paths` against `task_text`, since (per
    the module docstring) an unmatched route's plain-keyword/path relevance
    is always zero by construction.
    """
    normalized_task = task_text.lower()
    partial_groups: list[dict[str, list[str]]] = []
    for group in route.get("keyword_groups", []):
        matched = [keyword for keyword in group if _keyword_matches(normalized_task, keyword)]
        missing = [keyword for keyword in group if keyword not in matched]
        if matched and missing:
            partial_groups.append({"matched": matched, "missing": missing})
    if not partial_groups:
        return None
    return {"id": route["id"], "partially_satisfied_keyword_groups": partial_groups}


def find_near_misses(
    config: dict[str, Any], task_text: str, matched_route_ids: set[str]
) -> list[dict[str, Any]]:
    """Near-miss explanations for every route in `config["routes"]` that is
    NOT in `matched_route_ids`, in `routing.yaml` declaration order.

    Routes below `explain_route_near_miss`'s relevance threshold are omitted
    from the result entirely rather than included with an empty reasoning
    block -- this is a filtered "worth looking at" list, not a full dump of
    every unmatched route's internal state.
    """
    near_misses = []
    for route in config["routes"]:
        if route["id"] in matched_route_ids:
            continue
        explanation = explain_route_near_miss(route, task_text)
        if explanation is not None:
            near_misses.append(explanation)
    return near_misses


def format_near_misses_text(near_misses: list[dict[str, Any]]) -> str:
    """Render `find_near_misses()`'s result as a short, scannable block for
    `cadre select --explain` to print to stderr, never to the JSON plan on
    stdout (see `select_agents.py`)."""
    if not near_misses:
        return (
            "--explain: no near-miss routes for this task -- no unmatched route had a "
            "partially satisfied keyword_groups entry (see route_near_miss.py's relevance "
            "threshold; most routes in the current routing.yaml use plain keywords/paths, "
            "which have no partial-match state to report).\n"
        )
    lines = ["--explain: near-miss routes (did not match, but came close)", ""]
    for entry in near_misses:
        lines.append(f"{entry['id']}:")
        for index, group in enumerate(entry["partially_satisfied_keyword_groups"], start=1):
            matched = ", ".join(group["matched"])
            missing = ", ".join(group["missing"])
            total = len(group["matched"]) + len(group["missing"])
            lines.append(
                f"  keyword_groups[{index}]: matched {len(group['matched'])} of {total} "
                f"required keywords ({matched}); missing: {missing}"
            )
        lines.append("")
    return "\n".join(lines).rstrip() + "\n"
