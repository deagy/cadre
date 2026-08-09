"""Routing-coverage/orphan linter for catalog.yaml <-> routing.yaml.

Verifies two directions of consistency between this repository's own
`roster/catalog.yaml` role catalog and `roster/orchestration/routing.yaml`:

- Every catalog agent ID is reachable from at least one of routing.yaml's
  `routes`, `risk_rules`, `team_recipes`, `change_intake.agents`, or
  `cross_stack.support` entries (an "orphan" catalog agent otherwise).
- Every agent ID referenced from those routing.yaml structures actually
  exists as a catalog.yaml key (a "dangling" reference otherwise).

It also checks one property internal to routing.yaml: that no rule's
`exclude_paths` fully shadows one of its own `paths` globs (issue #162).
A shadowed glob is dead weight -- the rule keeps its `reviewers` and any
`human_gate` but matches on keywords alone, losing path coverage silently.

This module is pure static analysis: it never mutates routing.yaml or
catalog.yaml, and it reuses routing.py's existing `load_routing`/
`load_catalog`/`parse_catalog_entries` loaders rather than re-parsing either
file with a second implementation.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path
from typing import Any, Iterator

from routing import glob_to_regex, load_catalog, load_routing

DEFAULT_CATALOG = Path(__file__).resolve().parents[2] / "catalog.yaml"
DEFAULT_ROUTING = Path(__file__).resolve().parents[1] / "routing.yaml"


def _iter_references(config: dict[str, Any]) -> Iterator[tuple[str, str]]:
    """Yield (structural_location, referenced_agent_id) for every
    primary/reviewers/support/members/role/agents reference in routing.yaml.

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


# Fillers substituted for each wildcard when synthesizing probe paths. More
# than one per wildcard class matters: a single-depth filler alone would call
# `paths: ["docs/**"]` + `exclude_paths: ["docs/*"]` fully shadowed, when in
# truth only its top level is excluded and `docs/a/b.md` still matches.
_DOUBLESTAR_SLASH_FILLERS = ("", "probe/", "probe/nested/")
_DOUBLESTAR_FILLERS = ("probe.txt", "probe/nested.txt")
_STAR_FILLERS = ("probe",)
_QUESTION_FILLERS = ("x",)
_MAX_PROBES_PER_PATTERN = 64


def _probe_paths(pattern: str) -> list[str]:
    """Synthesize candidate concrete paths for one include glob.

    Returns only probes that the pattern itself actually matches. That filter
    is what makes the caller's conclusion sound: an unrepresentative probe can
    cause a missed finding, never a false one.
    """
    normalized = pattern.replace("\\", "/")
    segments: list[str] = [""]
    index = 0

    def extend(fillers: tuple[str, ...]) -> None:
        nonlocal segments
        segments = [prefix + filler for prefix in segments for filler in fillers][:_MAX_PROBES_PER_PATTERN]

    while index < len(normalized):
        character = normalized[index]
        if character == "*" and index + 1 < len(normalized) and normalized[index + 1] == "*":
            index += 1
            if index + 1 < len(normalized) and normalized[index + 1] == "/":
                index += 1
                extend(_DOUBLESTAR_SLASH_FILLERS)
            else:
                extend(_DOUBLESTAR_FILLERS)
        elif character == "*":
            extend(_STAR_FILLERS)
        elif character == "?":
            extend(_QUESTION_FILLERS)
        else:
            extend((character,))
        index += 1

    matcher = glob_to_regex(pattern)
    # Deduplicate while preserving order, so findings are deterministic.
    seen: dict[str, None] = {}
    for candidate in segments:
        if candidate and matcher.search(candidate):
            seen.setdefault(candidate, None)
    return list(seen)


def _iter_path_rules(config: dict[str, Any]) -> Iterator[tuple[str, dict[str, Any]]]:
    for section in ("routes", "risk_rules"):
        for index, rule in enumerate(config.get(section, []) or []):
            rule_id = rule.get("id", f"index {index}")
            yield f'{section}[{index}] (id="{rule_id}")', rule


def check_exclude_path_reachability(config: dict[str, Any]) -> list[str]:
    """Flag an include glob whose rule's `exclude_paths` swallow it whole.

    Such a glob is dead: the rule keeps its entry, its `reviewers` and any
    `human_gate`, but contributes nothing on paths -- it survives on keywords
    alone, silently, with no other check complaining (issue #162).

    Deliberately one-directional. A pattern is reported only when every probe
    the pattern itself matches is excluded, so an under-representative probe
    set yields a missed finding rather than a false accusation. A pattern this
    cannot synthesize a matching probe for is skipped rather than guessed at.
    """
    findings: list[str] = []
    for location, rule in _iter_path_rules(config):
        excludes = rule.get("exclude_paths") or []
        if not excludes:
            continue
        excluders = [glob_to_regex(pattern) for pattern in excludes]
        for position, include in enumerate(rule.get("paths", []) or []):
            probes = _probe_paths(include)
            if not probes:
                continue
            if all(any(excluder.search(probe) for excluder in excluders) for probe in probes):
                findings.append(
                    f"{location}.paths[{position}] {include!r} is fully shadowed by "
                    f"exclude_paths {excludes!r}: every path it matches is excluded, so it "
                    "contributes nothing and the rule matches on keywords alone "
                    f"(probes tried: {probes!r})"
                )
    return findings


def check_routing_coverage(config: dict[str, Any], catalog_agent_ids: list[str]) -> list[str]:
    """Return a deterministic, sorted-where-applicable list of finding strings.

    Empty list means routing.yaml and catalog.yaml are fully consistent:
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
            "routing.yaml route, risk_rule, team_recipe, change_intake.agents, or "
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
