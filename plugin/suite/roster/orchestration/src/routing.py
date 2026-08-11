"""Routing configuration loading and deterministic rule matching."""

from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any, Iterator, Pattern


# Token kinds for the selector's glob dialect. `iter_glob_tokens` is the one
# traversal of that dialect; every consumer maps these tokens to its own
# representation rather than re-walking the pattern, so a new dialect case is
# a single edit that forces every consumer to handle it.
GLOB_DOUBLESTAR_SLASH = "doublestar_slash"  # `**/` -- any number of leading segments
GLOB_DOUBLESTAR = "doublestar"  # `**`  -- anything, `/` included
GLOB_STAR = "star"  # `*`   -- anything within one segment
GLOB_QUESTION = "question"  # `?`   -- one character, not `/`
GLOB_LITERAL = "literal"  # any other character, matched exactly

# The delivery shapes a route may declare through `workflow_shape` (#210).
# Deliberately a strict subset of selection.schema.json's `workflow` enum:
# the other shapes there (rollback, production-release, support-escalation,
# runtime-assurance, knowledge-ingestion, debugging, agent-suite-maintenance,
# product-intake) are decided by _select_workflow()'s precedence *conditions*
# -- a route id combined with an exclusion, a keyword having actually fired,
# or a risk rule -- which no per-route constant can express. "unclassified"
# is the explicit "this route claims no delivery shape" declaration, not the
# absence of one.
WORKFLOW_SHAPES = frozenset(
    {"new-service", "infrastructure-change", "pipeline-change", "unclassified"}
)


def iter_glob_tokens(pattern: str) -> Iterator[tuple[str, str]]:
    """Yield `(kind, text)` for each construct in `pattern`.

    Backslashes are normalized to `/` first, so a Windows-style path pattern
    tokenizes identically to its POSIX spelling.
    """
    normalized = pattern.replace("\\", "/")
    index = 0
    while index < len(normalized):
        character = normalized[index]
        if character == "*" and index + 1 < len(normalized) and normalized[index + 1] == "*":
            index += 1
            if index + 1 < len(normalized) and normalized[index + 1] == "/":
                index += 1
                yield GLOB_DOUBLESTAR_SLASH, "**/"
            else:
                yield GLOB_DOUBLESTAR, "**"
        elif character == "*":
            yield GLOB_STAR, "*"
        elif character == "?":
            yield GLOB_QUESTION, "?"
        else:
            yield GLOB_LITERAL, character
        index += 1


_GLOB_TOKEN_REGEX = {
    GLOB_DOUBLESTAR_SLASH: "(?:.*/)?",
    GLOB_DOUBLESTAR: ".*",
    GLOB_STAR: "[^/]*",
    GLOB_QUESTION: "[^/]",
}


def glob_to_regex(pattern: str) -> Pattern[str]:
    """Translate the selector's small glob dialect to a compiled regex."""
    expression = "^"
    for kind, text in iter_glob_tokens(pattern):
        expression += _GLOB_TOKEN_REGEX[kind] if kind in _GLOB_TOKEN_REGEX else re.escape(text)
    return re.compile(f"{expression}$", re.IGNORECASE)


def _keyword_matches(text: str, keyword: str) -> bool:
    """Case-insensitive whole-word match: `keyword` must not be embedded in a
    longer token, for the boundary characters this checks. A hyphen is
    treated as a word character (not a boundary), so `"runner"` matches "the
    runner failed" but not "cross-runner", "runner-info", or "runners" — a
    hyphenated compound or a plural/suffixed form is a different token, not
    the keyword on its own. Internal spaces in a multi-word keyword still
    match across any run of whitespace.

    The boundary class is `[a-z0-9-]` only — it does NOT exclude underscore
    or `.`, so a keyword containing either of those characters CAN match
    embedded in a longer token. `routing.yaml`'s `bootstrap_sdlc.py` keyword
    is the one keyword in the current ruleset with this shape: it matches
    inside `legacy_bootstrap_sdlc.py_old` and `my_bootstrap_sdlc.py_v2`, not
    just the exact filename on its own. This is a known, accepted quirk of
    that one keyword (see `test_bootstrap_sdlc_keyword_matches_embedded_in_a_longer_token`
    in `roster/orchestration/test/test_selector.py`), not a general property
    of whole-word matching — do not assume it for other keywords.
    """
    escaped = re.escape(keyword.lower()).replace(r"\ ", r"\s+")
    return re.search(rf"(?<![a-z0-9-]){escaped}(?![a-z0-9-])", text, re.IGNORECASE) is not None


def match_rule(rule: dict[str, Any], task_text: str, changed_files: list[str]) -> dict[str, Any]:
    normalized_task = task_text.lower()
    matched_keywords = [
        keyword for keyword in rule.get("keywords", []) if _keyword_matches(normalized_task, keyword)
    ]
    matched_keyword_groups = [
        [keyword for keyword in group if _keyword_matches(normalized_task, keyword)]
        for group in rule.get("keyword_groups", [])
    ]
    conjunctive_match = bool(matched_keyword_groups) and all(matched_keyword_groups)
    # `exclude_paths` subtracts at the *file* level, not the rule level: a
    # route whose include glob is deliberately broad can carve out the paths
    # that glob was never meant to reach, while still matching on any other
    # changed file. Without it the only fix for a false positive is to narrow
    # the include, which trades it for false negatives -- see the architecture
    # and `**/*.py` cases this mechanism exists to undo.
    excluders = [glob_to_regex(pattern) for pattern in rule.get("exclude_paths", [])]
    matched_paths: list[dict[str, str]] = []
    for pattern in rule.get("paths", []):
        matcher = glob_to_regex(pattern)
        for file_name in changed_files:
            normalized_file = file_name.replace("\\", "/")
            if any(excluder.search(normalized_file) for excluder in excluders):
                continue
            if matcher.search(normalized_file):
                matched_paths.append({"pattern": pattern, "file": file_name})
    return {
        "matched": bool(matched_keywords or conjunctive_match or matched_paths),
        "keywords": matched_keywords,
        "keyword_groups": matched_keyword_groups if conjunctive_match else [],
        "paths": matched_paths,
    }


def load_routing(file_path: Path) -> dict[str, Any]:
    with file_path.open("r", encoding="utf-8") as source:
        config = json.load(source)
    return validate_routing_config(config)


def validate_routing_config(config: dict[str, Any]) -> dict[str, Any]:
    """Assert routing's structural invariants on an already-parsed config.

    Split out of `load_routing` so a configuration assembled in memory --
    notably the effective config `routing_overlay.resolve_effective_routing`
    returns after merging a project-local overlay -- passes exactly the same
    checks as one read from disk, instead of a merged config reaching the
    selector unvalidated.
    """
    if (
        config.get("version") != 1
        or not isinstance(config.get("routes"), list)
        or not isinstance(config.get("risk_rules"), list)
    ):
        raise ValueError("routing.yaml must contain version 1 routes and risk_rules")
    ids = [
        rule.get("id")
        for rule in [*config["routes"], *config["risk_rules"], *config.get("team_recipes", [])]
    ]
    if len(set(ids)) != len(ids):
        raise ValueError("Routing, risk rule, and team recipe IDs must be unique")
    for rule in [*config["routes"], *config["risk_rules"]]:
        groups = rule.get("keyword_groups", [])
        if groups and (
            not isinstance(groups, list)
            or any(
                not isinstance(group, list)
                or not group
                or any(not isinstance(keyword, str) or not keyword for keyword in group)
                for group in groups
            )
        ):
            raise ValueError(
                f"{rule.get('id', 'rule')} keyword_groups must contain non-empty string groups"
            )
    # `workflow_shape` is validated by value, not required by presence. Every
    # route in this repository's own routing.yaml declares one and
    # test_selector.py::WorkflowShapeDeclarationTests fails the build if one
    # stops doing so; a project-local overlay (routing_overlay.py) may add a
    # route without the field, which still contributes no delivery shape.
    # Requiring presence here would break every existing overlay that adds a
    # route -- a trade considered and rejected in #210 review and again in
    # #214. What #214 changed is that the omission is no longer *silent*:
    # build_dispatch_plan._undeclared_workflow_shape_routes names any matched
    # route with no declared shape in the plan's optional
    # `undeclared_workflow_shape_routes` field. Report, don't reject -- so
    # this loop stays a value check.
    # A *misspelled* shape is still the case worth failing on: it would
    # silently contribute nothing while looking declared.
    for route in config["routes"]:
        shape = route.get("workflow_shape")
        if shape is not None and shape not in WORKFLOW_SHAPES:
            raise ValueError(
                f"{route.get('id', 'route')} workflow_shape must be one of "
                f"{sorted(WORKFLOW_SHAPES)}, got {shape!r}"
            )
    context_packs = config.get("context_packs", [])
    if not isinstance(context_packs, list):
        raise ValueError("routing.yaml context_packs must be a list")
    # Context pack ids live in the SAME namespace as route, risk rule, and
    # team recipe ids -- not a private one. A dispatch plan puts
    # `matched_routes[].id`, `matched_risks[].id`, and `context_packs[].id`
    # side by side, so an id claimed by both a route and a pack is ambiguous
    # for any consumer keying on plan ids. `schema_validate.validate_routing`
    # checks each array only against itself, so pooling here is the only
    # place the cross-array collision is caught.
    claimed_ids = set(ids)
    for pack in context_packs:
        if not isinstance(pack, dict):
            raise ValueError("routing.yaml context_packs entries must be objects")
        pack_id, definition, version = pack.get("id"), pack.get("definition"), pack.get("version")
        if not isinstance(pack_id, str) or not pack_id or not isinstance(definition, str) or not definition:
            raise ValueError("routing.yaml context_packs entries require non-empty id and definition")
        if not isinstance(version, int) or isinstance(version, bool) or version < 1:
            raise ValueError(f"{pack_id} context pack version must be a positive integer")
        if pack_id in claimed_ids:
            raise ValueError(f"duplicate context pack id: {pack_id}")
        claimed_ids.add(pack_id)
    for recipe in config.get("team_recipes", []):
        if recipe.get("type") == "dynamic":
            instances = recipe.get("instances", {})
            minimum, maximum = instances.get("min"), instances.get("max")
            if (
                not isinstance(minimum, int)
                or isinstance(minimum, bool)
                or not isinstance(maximum, int)
                or isinstance(maximum, bool)
                or minimum < 1
                or maximum < minimum
            ):
                raise ValueError(f"{recipe.get('id', 'team recipe')} instances must satisfy 1 <= min <= max")
    return config


def parse_keyed_entries(content: str, fields: tuple[str, ...]) -> dict[str, dict[str, str]]:
    """Parse a `  <id>:\\n    <field>: <value>` block list into id -> metadata.

    The shared low-level primitive behind this repo's line-oriented
    (non-PyYAML) config tables: catalog.yaml's `agents:` block, via
    parse_catalog_entries() below, and aides.yaml's `aides:` block, via
    generate_authority_aides.py. One parser for the shape means a fix to it
    (e.g. field-order handling) benefits every table built on it instead of
    only the one it was written for. `fields` restricts which `key:` lines
    under an id are captured, so unrelated fields present in the file are
    ignored rather than misparsed. Raises on a duplicate id rather than
    silently keeping the last occurrence.
    """
    entries: dict[str, dict[str, str]] = {}
    current: str | None = None
    field_prefixes = tuple(f"{field}:" for field in fields)
    for line_number, line in enumerate(content.splitlines(), start=1):
        match = re.match(r"^  ([a-z0-9-]+):\s*$", line)
        if match:
            current = match.group(1)
            if current in entries:
                raise ValueError(f"line {line_number}: duplicate id {current!r}")
            entries[current] = {}
            continue
        if current and line.strip().startswith(field_prefixes):
            key, value = line.strip().split(":", 1)
            entries[current][key] = value.strip()
    return entries


def parse_catalog_entries(content: str) -> dict[str, dict[str, str]]:
    """Parse catalog.yaml's line-oriented agent blocks into id -> metadata.

    Shared by this module (which only needs agent IDs) and
    generate_global_plugin.py (which needs the full per-agent metadata) so
    the two never silently diverge on catalog.yaml's format.
    """
    return parse_keyed_entries(
        content, ("definition", "phase", "capability", "model", "codex_model", "reasoning_effort")
    )


def load_catalog(file_path: Path) -> list[str]:
    agents = parse_catalog_entries(file_path.read_text(encoding="utf-8"))
    if not agents:
        raise ValueError("No agents found in catalog.yaml")
    return list(agents.keys())


def match_routes(
    config: dict[str, Any], task_text: str, changed_files: list[str]
) -> list[dict[str, Any]]:
    matches = []
    for route in config["routes"]:
        reasons = match_rule(route, task_text, changed_files)
        if reasons["matched"]:
            matches.append({"id": route["id"], "reasons": reasons, "rule": route})
    return matches


def match_context_packs(
    config: dict[str, Any], task_text: str, changed_files: list[str]
) -> list[dict[str, Any]]:
    """Select non-authoring context packs using the ordinary route grammar."""
    matches = []
    for pack in config.get("context_packs", []):
        reasons = match_rule(pack, task_text, changed_files)
        if reasons["matched"]:
            matches.append({"id": pack["id"], "reasons": reasons, "rule": pack})
    return matches
