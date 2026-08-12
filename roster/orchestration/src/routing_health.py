"""Routing-coverage/orphan linter for catalog.yaml <-> routing.json.

Verifies two directions of consistency between this repository's own
`roster/catalog.yaml` role catalog and `roster/orchestration/routing.json`:

- Every catalog agent ID is reachable from at least one of routing.json's
  `routes`, `risk_rules`, `team_recipes`, `change_intake.agents`, or
  `cross_stack.support` entries (an "orphan" catalog agent otherwise).
- Every agent ID referenced from those routing.json structures actually
  exists as a catalog.yaml key (a "dangling" reference otherwise).

It also checks one property internal to routing.json: that no rule's
`exclude_paths` fully shadows one of its own `paths` globs (issue #162).
A shadowed glob is dead weight -- the rule keeps its `reviewers` and any
`human_gate` but matches on keywords alone, losing path coverage silently.

This module is pure static analysis: it never mutates routing.json or
catalog.yaml, and it reuses routing.py's existing `load_routing`/
`load_catalog`/`parse_catalog_entries` loaders rather than re-parsing either
file with a second implementation.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path
from typing import Any, Iterator

from glob_containment import CONTAINED, contains
from routing import load_catalog, load_routing

DEFAULT_CATALOG = Path(__file__).resolve().parents[2] / "catalog.yaml"
DEFAULT_ROUTING = Path(__file__).resolve().parents[1] / "routing.json"


def _iter_references(config: dict[str, Any]) -> Iterator[tuple[str, str]]:
    """Yield (structural_location, referenced_agent_id) for every
    primary/reviewers/support/members/role/agents reference in routing.json.

    `structural_location` names the exact field and index the reference came
    from (e.g. `routes[6] (id="orchestration").reviewers[0]`), matching the
    project's convention (see test_repository_health.py) of pointing at a
    precise location rather than reporting a bare "mismatch".
    """
    for index, route in enumerate(config.get("routes", []) or []):
        route_id = route.get("id", f"index {index}")
        for field in ("primary", "reviewers", "support"):
            for position, agent_id in enumerate(route.get(field, []) or []):
                yield f'routes[{index}] (id="{route_id}").{field}[{position}]', agent_id

    for index, rule in enumerate(config.get("risk_rules", []) or []):
        rule_id = rule.get("id", f"index {index}")
        for field in ("primary", "reviewers", "support"):
            for position, agent_id in enumerate(rule.get(field, []) or []):
                yield f'risk_rules[{index}] (id="{rule_id}").{field}[{position}]', agent_id

    for index, recipe in enumerate(config.get("team_recipes", []) or []):
        recipe_id = recipe.get("id", f"index {index}")
        for position, agent_id in enumerate(recipe.get("members", []) or []):
            yield f'team_recipes[{index}] (id="{recipe_id}").members[{position}]', agent_id
        if "role" in recipe:
            yield f'team_recipes[{index}] (id="{recipe_id}").role', recipe["role"]

    change_intake = config.get("change_intake", {}) or {}
    for position, agent_id in enumerate(change_intake.get("agents", []) or []):
        yield f"change_intake.agents[{position}]", agent_id

    cross_stack = config.get("cross_stack", {}) or {}
    for position, agent_id in enumerate(cross_stack.get("support", []) or []):
        yield f"cross_stack.support[{position}]", agent_id


def _iter_path_rules(config: dict[str, Any]) -> Iterator[tuple[str, dict[str, Any]]]:
    for section in ("routes", "risk_rules"):
        for index, rule in enumerate(config.get(section, []) or []):
            rule_id = rule.get("id", f"index {index}")
            yield f'{section}[{index}] (id="{rule_id}")', rule


def check_exclude_path_reachability(config: dict[str, Any]) -> list[str]:
    """Flag an include glob whose rule's `exclude_paths` swallow it whole.

    Such a glob is dead: the rule keeps its entry, its `reviewers` and any
    `human_gate`, but contributes nothing on paths -- it survives on keywords
    alone, or not at all if it has none (issue #162).

    The verdict is exact, not sampled. `glob_containment.contains` decides
    `L(paths[i]) subset-of union(L(exclude_paths))` for the whole dialect, so
    a finding means every path the glob could ever match is excluded -- not
    that some synthesized guesses were. A pattern the decision procedure
    cannot settle within its state budget returns UNDETERMINED and is skipped
    rather than reported, so the only imprecision is a missed finding.
    """
    findings: list[str] = []
    for location, rule in _iter_path_rules(config):
        excludes = rule.get("exclude_paths") or []
        if not excludes:
            continue
        has_keywords = bool(rule.get("keywords") or rule.get("keyword_groups"))
        remainder = (
            "so it contributes nothing and the rule matches on keywords alone"
            if has_keywords
            else "so it contributes nothing and the rule, having no keywords, can never match"
        )
        for position, include in enumerate(rule.get("paths", []) or []):
            if contains(include, excludes) != CONTAINED:
                continue
            findings.append(
                f"{location}.paths[{position}] {include!r} is fully shadowed by "
                f"exclude_paths {excludes!r}: every path it matches is excluded, {remainder}"
            )
    return findings


def check_routing_coverage(config: dict[str, Any], catalog_agent_ids: list[str]) -> list[str]:
    """Return a deterministic, sorted-where-applicable list of finding strings.

    Empty list means routing.json and catalog.yaml are fully consistent:
    every catalog agent is reachable, and every reference resolves to a
    catalog agent.
    """
    catalog_ids = set(catalog_agent_ids)
    references = list(_iter_references(config))
    reachable_ids = {agent_id for _, agent_id in references}

    findings: list[str] = []

    for agent_id in sorted(catalog_ids - reachable_ids):
        findings.append(
            f'catalog agent "{agent_id}" is not referenced as primary/reviewers/support in any '
            "routing.json route, risk_rule, team_recipe, change_intake.agents, or "
            "cross_stack.support entry"
        )

    for location, agent_id in references:
        if agent_id not in catalog_ids:
            findings.append(f'{location} references agent "{agent_id}", which is not a catalog.yaml agent')

    return findings


def run(catalog_path: Path = DEFAULT_CATALOG, routing_path: Path = DEFAULT_ROUTING) -> list[str]:
    catalog_agent_ids = load_catalog(catalog_path)
    routing_config = load_routing(routing_path)
    return check_routing_coverage(routing_config, catalog_agent_ids) + check_exclude_path_reachability(
        routing_config
    )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0] if __doc__ else None)
    parser.add_argument("--catalog", type=Path, default=DEFAULT_CATALOG)
    parser.add_argument("--routing", type=Path, default=DEFAULT_ROUTING)
    # Accepted as a no-op for symmetry with this repo's other drift guards
    # (`generate-role-metadata --check`, `generate-plugin --check`), which do
    # distinguish check from write. This tool only ever reports, so it is
    # already in "check" mode. `.pre-commit-config.yaml`'s `catalog-health`
    # hook passes it, and argparse rejecting it made that hook exit 2 without
    # ever running the check.
    parser.add_argument("--check", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args(argv)

    findings = run(args.catalog, args.routing)
    if findings:
        for finding in findings:
            print(finding, file=sys.stderr)
        return 1
    print(
        "routing coverage check passed: no orphan or dangling agent references, "
        "no exclude_paths-shadowed include glob"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
