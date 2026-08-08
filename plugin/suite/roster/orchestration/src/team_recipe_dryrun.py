#!/usr/bin/env python3
"""Dry-run visualizer for `roster/orchestration/routing.yaml`'s `team_recipes[]`.

A recipe author/debugger currently has no way to see, for a hypothetical or
real task, WHY a given team recipe did or didn't trigger without reading
`_build_teams()` in `build_dispatch_plan.py` directly or running a full
`cadre select` and inspecting the emitted `teams` field (which only shows
recipes that *fired* -- it never explains a near-miss). This module answers
that question for every recipe, fired or not, with the specific condition
values that decided it.

Two input modes, matching this repository's existing dry-run conventions:

- Task mode (`--task`, plus `--files`/`--base`/`--root`): the same inputs
  `cadre select` takes. Routes/risks are matched for real against
  `routing.yaml`, and `build_dispatch_plan()` is called to obtain the exact
  `matched_routes` and selected-agent set a real dispatch would produce --
  this reuses the authoritative selection logic rather than reimplementing
  it, so the two can never silently diverge.
- Synthetic mode (`--matched-routes`, `--selected-agents`): a recipe author
  supplies a hypothetical set of already-matched routes and already-selected
  agents directly, without needing a real task/files pair or a route/agent
  match at all -- the more useful mode for a recipe *author* iterating on a
  new or edited recipe definition before it can naturally fire from routing.
  `--task` may still be supplied in this mode purely to exercise a dynamic
  recipe's keyword condition (no route/agent matching is derived from it).

Both modes converge on the same three signals `_build_teams()` in
`build_dispatch_plan.py` actually consumes: a matched-route-id set, a
selected-agent set, and task text for dynamic-recipe keyword matching. This
module deliberately mirrors that function's exact condition order and
short-circuit semantics (see `explain_fixed_recipe`/`explain_dynamic_recipe`
below) so the dry-run answer can never disagree with a real dispatch.

This module never mutates routing.yaml, catalog.yaml, or any run artifact,
and it never retrieves knowledge, invokes agents, or dispatches anything --
it only explains what a hypothetical or real signal set would produce.

Run:

    python3 roster/orchestration/src/team_recipe_dryrun.py \\
        --matched-routes frontend,backend \\
        --selected-agents frontend-engineer,backend-engineer

    python3 roster/orchestration/src/team_recipe_dryrun.py \\
        --task "Add a React upload form backed by a PostgreSQL API" \\
        --files frontend/src/Upload.tsx,services/upload/main.go

Exits 0 when the dry-run runs to completion (regardless of which recipes
fired), non-zero only on an input/usage error (e.g. an unknown `--recipe`
id, or a `--matched-routes`/`--selected-agents` value routing.yaml doesn't
recognize).
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

ORCHESTRATION_ROOT = Path(__file__).resolve().parent.parent
AGENTS_ROOT = ORCHESTRATION_ROOT.parent
REPOSITORY_ROOT = AGENTS_ROOT.parent
DEFAULT_ROUTING = ORCHESTRATION_ROOT / "routing.yaml"
DEFAULT_CATALOG = AGENTS_ROOT / "catalog.yaml"

sys.path.insert(0, str(Path(__file__).resolve().parent))

from build_dispatch_plan import build_dispatch_plan  # noqa: E402
from routing import _keyword_matches, load_catalog, load_routing  # noqa: E402
from select_agents import discover_changed_files, explicit_files  # noqa: E402


def _csv_values(values: list[str] | None) -> list[str]:
    """Flatten repeatable, comma-separated CLI values (same convention as
    `select_agents.explicit_files`), de-duplicated while preserving order."""
    if not values:
        return []
    flattened: list[str] = []
    for value in values:
        flattened.extend(entry.strip() for entry in value.split(",") if entry.strip())
    return list(dict.fromkeys(flattened))


def explain_fixed_recipe(
    recipe: dict[str, Any], matched_route_ids: set[str], selected_agents: set[str]
) -> dict[str, Any]:
    """Explain a `type: "fixed"` recipe exactly per `_build_teams()`'s logic:
    fires when at least `minimum_matches` of `route_ids` matched AND at least
    `minimum_members_selected` (default 2) of `members` were already
    selected agents."""
    route_ids = recipe["route_ids"]
    triggering_routes = sorted(matched_route_ids & set(route_ids))
    unmatched_routes = sorted(set(route_ids) - matched_route_ids)
    minimum_matches = recipe["minimum_matches"]
    routes_satisfied = len(triggering_routes) >= minimum_matches

    members = recipe["members"]
    minimum_members_selected = recipe.get("minimum_members_selected", 2)
    selected_members = [member for member in members if member in selected_agents]
    unselected_members = [member for member in members if member not in selected_agents]
    members_satisfied = len(selected_members) >= minimum_members_selected

    return {
        "id": recipe["id"],
        "type": "fixed",
        "description": recipe.get("description", ""),
        "fires": routes_satisfied and members_satisfied,
        "routes": {
            "candidate_route_ids": route_ids,
            "matched_route_ids": triggering_routes,
            "unmatched_route_ids": unmatched_routes,
            "minimum_matches": minimum_matches,
            "actual_matches": len(triggering_routes),
            "satisfied": routes_satisfied,
        },
        "members": {
            "candidate_members": members,
            "selected_members": selected_members,
            "unselected_members": unselected_members,
            "minimum_members_selected": minimum_members_selected,
            "actual_selected": len(selected_members),
            "satisfied": members_satisfied,
        },
    }


def explain_dynamic_recipe(
    recipe: dict[str, Any], matched_route_ids: set[str], selected_agents: set[str], task_text: str
) -> dict[str, Any]:
    """Explain a `type: "dynamic"` recipe exactly per `_build_teams()`'s
    logic: fires when `role` is already a selected agent AND (`requires_route`
    is falsy, or it is a matched route) AND at least one of `keywords`
    matches the task text. An empty `keywords` list can never fire, matching
    `_build_teams()`'s `if not matched_keywords: continue`.

    `routing.schema.json` currently requires `requires_route` to be a
    non-empty string on every `type: dynamic` recipe, so a falsy-but-present
    value (`""`) can't occur in schema-valid data today -- the truthy check
    (matching `_build_teams()`'s `if recipe.get("requires_route") and ...`
    exactly, not just its `is None` case) is kept anyway so this stays a
    faithful mirror even if that schema constraint is ever relaxed.
    """
    role = recipe["role"]
    role_selected = role in selected_agents

    requires_route = recipe.get("requires_route")
    route_satisfied = not requires_route or requires_route in matched_route_ids

    keywords = recipe.get("keywords", [])
    normalized_task = task_text.lower()
    matched_keywords = [keyword for keyword in keywords if _keyword_matches(normalized_task, keyword)]
    unmatched_keywords = [keyword for keyword in keywords if keyword not in matched_keywords]
    keywords_satisfied = bool(matched_keywords)

    return {
        "id": recipe["id"],
        "type": "dynamic",
        "description": recipe.get("description", ""),
        "fires": role_selected and route_satisfied and keywords_satisfied,
        "role": {
            "candidate_role": role,
            "selected": role_selected,
        },
        "requires_route": {
            "candidate_route_id": requires_route,
            "matched": route_satisfied,
        },
        "keywords": {
            "candidate_keywords": keywords,
            "matched_keywords": matched_keywords,
            "unmatched_keywords": unmatched_keywords,
            "satisfied": keywords_satisfied,
        },
        "instances": recipe.get("instances"),
    }


def explain_recipes(
    config: dict[str, Any],
    matched_route_ids: set[str],
    selected_agents: set[str],
    task_text: str,
    recipe_id: str | None = None,
) -> list[dict[str, Any]]:
    recipes = config.get("team_recipes", [])
    if recipe_id is not None:
        recipes = [recipe for recipe in recipes if recipe["id"] == recipe_id]
        if not recipes:
            known = sorted(recipe["id"] for recipe in config.get("team_recipes", []))
            raise ValueError(f"Unknown team recipe id {recipe_id!r}; known ids: {known}")

    explanations = []
    for recipe in recipes:
        if recipe["type"] == "fixed":
            explanations.append(explain_fixed_recipe(recipe, matched_route_ids, selected_agents))
        elif recipe["type"] == "dynamic":
            explanations.append(explain_dynamic_recipe(recipe, matched_route_ids, selected_agents, task_text))
        else:
            raise ValueError(f"{recipe['id']}: unknown team recipe type {recipe['type']!r}")
    return explanations


def expand_recipe_to_members(
    config: dict[str, Any],
    recipe_id: str,
    matched_route_ids: set[str],
    selected_agents: set[str],
    task_text: str = "",
    *,
    shared_brief: str | None = None,
    member_briefs: dict[str, str] | None = None,
    instance_briefs: list[str] | None = None,
    instance_count: int | None = None,
) -> list[dict[str, str]]:
    """Expand a `team_recipes[]` entry into a concrete `[{"role_id", "brief"},
    ...]` list ready to hand to `dispatch_team()`
    (`roster/orchestration/mcp/dispatch_core.py`) -- the piece
    `explain_fixed_recipe`/`explain_dynamic_recipe` deliberately stop short
    of (they report pass/fail and the same primitive fields
    `_build_teams()` uses, never briefs).

    Reuses `explain_fixed_recipe`/`explain_dynamic_recipe` for the "does this
    recipe actually fire" check rather than reimplementing `_build_teams()`'s
    condition logic a third time; raises `ValueError` (never silently
    expands) if the recipe wouldn't actually have fired for this
    matched_route_ids/selected_agents/task_text signal set, if the recipe id
    or type is unrecognized, or if the caller-supplied briefs don't cover
    every member this recipe would actually produce.
    """
    matching = [recipe for recipe in config.get("team_recipes", []) if recipe["id"] == recipe_id]
    if not matching:
        known = sorted(recipe["id"] for recipe in config.get("team_recipes", []))
        raise ValueError(f"Unknown team recipe id {recipe_id!r}; known ids: {known}")
    recipe = matching[0]

    if recipe["type"] == "fixed":
        explanation = explain_fixed_recipe(recipe, matched_route_ids, selected_agents)
        if not explanation["fires"]:
            raise ValueError(
                f"team recipe {recipe_id!r} did not fire for this matched_route_ids/selected_agents "
                "signal set; refusing to expand a recipe that would not actually have triggered"
            )
        role_ids = explanation["members"]["selected_members"]
        member_briefs = member_briefs or {}
        members: list[dict[str, str]] = []
        for role_id in role_ids:
            brief = member_briefs.get(role_id, shared_brief)
            if brief is None:
                raise ValueError(
                    f"team recipe {recipe_id!r} member {role_id!r} has no brief: supply it in "
                    "member_briefs or provide shared_brief"
                )
            members.append({"role_id": role_id, "brief": brief})
        return members

    if recipe["type"] == "dynamic":
        explanation = explain_dynamic_recipe(recipe, matched_route_ids, selected_agents, task_text)
        if not explanation["fires"]:
            raise ValueError(
                f"team recipe {recipe_id!r} did not fire for this matched_route_ids/selected_agents/task_text "
                "signal set; refusing to expand a recipe that would not actually have triggered"
            )
        instances = recipe["instances"]
        minimum, maximum = instances["min"], instances["max"]
        count = instance_count if instance_count is not None else minimum
        if not (minimum <= count <= maximum):
            raise ValueError(
                f"team recipe {recipe_id!r} instance_count {count} is outside its declared "
                f"[{minimum}, {maximum}] range"
            )
        # A shared identical brief across instances defeats the point of a
        # dynamic recipe like competing-hypotheses-debugging (distinct
        # hypotheses per instance) -- require one brief per instance rather
        # than silently falling back to shared_brief for every member.
        if instance_briefs is None or len(instance_briefs) != count:
            raise ValueError(
                f"team recipe {recipe_id!r} requires exactly {count} instance_briefs entries "
                f"(one per instance); got {0 if instance_briefs is None else len(instance_briefs)}"
            )
        role_id = recipe["role"]
        return [{"role_id": role_id, "brief": brief} for brief in instance_briefs]

    raise ValueError(f"{recipe_id!r}: unknown team recipe type {recipe['type']!r}")


def _resolve_task_mode_signals(
    config: dict[str, Any],
    catalog: list[str],
    task: str,
    root: str | None,
    files: list[str] | None,
    base: str | None,
) -> tuple[set[str], set[str], str]:
    repository_root = Path(root).expanduser().resolve() if root else Path.cwd().resolve()
    if not repository_root.is_dir():
        raise ValueError(f"Repository root is not a directory: {repository_root}")
    supplied_files = explicit_files(files)
    if supplied_files is not None and base:
        raise ValueError("--base cannot be combined with --files")
    changes = (
        {"source": "explicit", "files": supplied_files}
        if supplied_files is not None
        else discover_changed_files(base, repository_root)
    )
    plan = build_dispatch_plan(
        config,
        catalog,
        {
            "task": task,
            "task_id": None,
            "repository_root": str(repository_root),
            "base": base,
            "changed_files": [str(file_name).replace("\\", "/") for file_name in changes["files"]],
            "changed_file_source": changes["source"],
            "classification": None,
            "source": "team-recipe-dryrun",
            "top": "5",
        },
    )
    matched_route_ids = {route["id"] for route in plan["matched_routes"]}
    selected_agents = {*plan["agents"]["primary"], *plan["agents"]["reviewers"], *plan["agents"]["support"]}
    return matched_route_ids, selected_agents, task


def _resolve_synthetic_mode_signals(
    config: dict[str, Any],
    matched_routes: list[str],
    selected_agents: list[str],
    task: str,
) -> tuple[set[str], set[str], str]:
    known_route_ids = {route["id"] for route in config.get("routes", [])}
    unknown_routes = sorted(set(matched_routes) - known_route_ids)
    if unknown_routes:
        raise ValueError(f"--matched-routes contains unknown route id(s): {unknown_routes}")
    return set(matched_routes), set(selected_agents), task


def _format_text(explanations: list[dict[str, Any]]) -> str:
    lines: list[str] = []
    for explanation in explanations:
        verdict = "FIRE   " if explanation["fires"] else "NO-FIRE"
        lines.append(f"[{verdict}] {explanation['id']} ({explanation['type']})")
        if explanation["description"]:
            lines.append(f"  {explanation['description']}")
        if explanation["type"] == "fixed":
            routes = explanation["routes"]
            lines.append(
                f"  routes: {routes['actual_matches']}/{routes['minimum_matches']} required matched "
                f"-> {'satisfied' if routes['satisfied'] else 'NOT satisfied'}"
            )
            lines.append(f"    matched:   {routes['matched_route_ids'] or '(none)'}")
            lines.append(f"    unmatched: {routes['unmatched_route_ids'] or '(none)'}")
            members = explanation["members"]
            lines.append(
                f"  members: {members['actual_selected']}/{members['minimum_members_selected']} required "
                f"already selected -> {'satisfied' if members['satisfied'] else 'NOT satisfied'}"
            )
            lines.append(f"    selected:   {members['selected_members'] or '(none)'}")
            lines.append(f"    unselected: {members['unselected_members'] or '(none)'}")
        else:
            role = explanation["role"]
            lines.append(
                f"  role: {role['candidate_role']} -> "
                f"{'selected' if role['selected'] else 'NOT selected'}"
            )
            requires_route = explanation["requires_route"]
            if requires_route["candidate_route_id"] is None:
                lines.append("  requires_route: (none) -> satisfied")
            else:
                lines.append(
                    f"  requires_route: {requires_route['candidate_route_id']} -> "
                    f"{'matched' if requires_route['matched'] else 'NOT matched'}"
                )
            keywords = explanation["keywords"]
            lines.append(
                f"  keywords: {len(keywords['matched_keywords'])} of {len(keywords['candidate_keywords'])} "
                f"matched -> {'satisfied' if keywords['satisfied'] else 'NOT satisfied'}"
            )
            lines.append(f"    matched:   {keywords['matched_keywords'] or '(none)'}")
            lines.append(f"    unmatched: {keywords['unmatched_keywords'] or '(none)'}")
            if explanation["instances"]:
                lines.append(f"  instances: {explanation['instances']}")
        lines.append("")
    return "\n".join(lines).rstrip() + "\n"


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Explain, for every (or one) team_recipes[] entry in routing.yaml, whether it "
            "would fire and exactly why/why not."
        ),
        allow_abbrev=False,
    )
    parser.add_argument("--routing", type=Path, default=DEFAULT_ROUTING, help="Path to routing.yaml")
    parser.add_argument("--catalog", type=Path, default=DEFAULT_CATALOG, help="Path to catalog.yaml")
    parser.add_argument("--recipe", help="Only explain this team recipe id")
    parser.add_argument("--format", choices=["text", "json"], default="text")

    parser.add_argument(
        "--matched-routes",
        action="append",
        help="Synthetic mode: comma-separated route id(s) to treat as already matched; repeatable",
    )
    parser.add_argument(
        "--selected-agents",
        action="append",
        help="Synthetic mode: comma-separated agent id(s) to treat as already selected; repeatable",
    )

    parser.add_argument(
        "--task",
        help=(
            "Task mode: objective used for real route/risk matching (with --files/--base/--root). "
            "Synthetic mode: optional task text used only for dynamic-recipe keyword matching."
        ),
    )
    parser.add_argument("--root", help="Task mode: target repository root (defaults to cwd)")
    parser.add_argument("--files", action="append", help="Task mode: changed path(s); repeatable, comma-separated")
    parser.add_argument("--base", help="Task mode: git base ref used with <base>...HEAD")
    return parser


def main(argv: list[str] | None = None) -> int:
    options = _parser().parse_args(argv)
    config = load_routing(options.routing)
    catalog = load_catalog(options.catalog)

    synthetic_routes = _csv_values(options.matched_routes)
    synthetic_agents = _csv_values(options.selected_agents)
    synthetic_mode = bool(options.matched_routes or options.selected_agents)

    if synthetic_mode:
        if options.base or options.files:
            raise ValueError("--base/--files require task mode; omit --matched-routes/--selected-agents to use it")
        matched_route_ids, selected_agents, task_text = _resolve_synthetic_mode_signals(
            config, synthetic_routes, synthetic_agents, options.task or ""
        )
    elif options.task:
        matched_route_ids, selected_agents, task_text = _resolve_task_mode_signals(
            config, catalog, options.task, options.root, options.files, options.base
        )
    else:
        raise ValueError(
            "Provide either --task (task mode) or --matched-routes/--selected-agents (synthetic mode)"
        )

    explanations = explain_recipes(config, matched_route_ids, selected_agents, task_text, options.recipe)

    if options.format == "json":
        output = {
            "mode": "synthetic" if synthetic_mode else "task",
            "matched_route_ids": sorted(matched_route_ids),
            "selected_agents": sorted(selected_agents),
            "task_text": task_text,
            "recipes": explanations,
        }
        print(json.dumps(output, indent=2, ensure_ascii=False))
    else:
        print(f"mode: {'synthetic' if synthetic_mode else 'task'}")
        print(f"matched_route_ids: {sorted(matched_route_ids) or '(none)'}")
        print(f"selected_agents:   {sorted(selected_agents) or '(none)'}")
        if task_text:
            print(f"task_text:         {task_text!r}")
        print()
        print(_format_text(explanations), end="")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, RuntimeError, ValueError, json.JSONDecodeError) as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1) from error
