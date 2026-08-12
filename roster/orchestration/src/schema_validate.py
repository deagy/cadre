#!/usr/bin/env python3
"""Strict, standalone JSON Schema validation for `roster/catalog.yaml` and
`roster/orchestration/routing.json`.

This is a distinct, complementary check from two existing ones -- it does
not replace or overlap either:

- `routing_health.py` (idea #1): reachability/orphan/dangling-reference
  coverage between already-well-typed catalog.yaml and routing.json. It
  presumes both files already parsed and are internally well-shaped.
- `generate_role_metadata.py --check`: generation-drift detection ("did you
  forget to regenerate after editing AGENT.md frontmatter"), scoped to
  catalog.yaml and routing.json's `knowledge_focus` block, and only useful
  when the corresponding AGENT.md sources are available to regenerate
  against.

This module instead asks a third, independent question -- "is this file's
own shape/type/enum content valid" -- answerable standalone, without
AGENT.md frontmatter, without invoking any generator, and without depending
on `load_catalog`/`load_routing` raising and halting before other
independent defects are found. It reports every finding in one pass,
location-precise (JSON pointer per `jsonschema`'s own error-path
convention), not just the first.

Two JSON Schema documents (Draft 2020-12, matching the existing
`roster/orchestration/selection.schema.json` precedent) hold the bulk of the
contract: `roster/catalog.schema.json` and
`roster/orchestration/routing.schema.json`. A small number of cross-field
consistency checks a JSON Schema document cannot cleanly express (a
duplicate YAML/JSON object key, a filesystem existence check, an integer
property compared against a sibling array's length) are implemented here as
supplementary Python checks, run in addition to -- never instead of -- the
schema validation.

Regenerate nothing; this module never mutates catalog.yaml or routing.json.

Run:

    python3 roster/orchestration/src/schema_validate.py

Exits non-zero with every finding on stderr when either file is invalid;
exits zero with a summary line on stdout when both are clean.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any

import jsonschema
import yaml

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_ROSTER_ROOT = REPOSITORY_ROOT / "roster"
DEFAULT_CATALOG = DEFAULT_ROSTER_ROOT / "catalog.yaml"
DEFAULT_ROUTING = DEFAULT_ROSTER_ROOT / "orchestration" / "routing.json"
DEFAULT_CATALOG_SCHEMA = DEFAULT_ROSTER_ROOT / "catalog.schema.json"
DEFAULT_ROUTING_SCHEMA = DEFAULT_ROSTER_ROOT / "orchestration" / "routing.schema.json"
DEFAULT_ROSTER_MANIFEST = DEFAULT_ROSTER_ROOT / "roster.json"
DEFAULT_ROSTER_SCHEMA = DEFAULT_ROSTER_ROOT / "orchestration" / "roster.schema.json"

# Matches routing.py::parse_keyed_entries's own id-line character class
# exactly, so the two line-oriented parsers can't silently diverge.
_ID_LINE = re.compile(r"^  ([a-z0-9-]+):\s*$")


def load_catalog_yaml(path: Path) -> dict[str, Any]:
    with path.open("r", encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def load_routing_json(path: Path) -> dict[str, Any]:
    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def _format_error(error: jsonschema.exceptions.ValidationError) -> str:
    pointer = "$" + "".join(
        f"[{part!r}]" if isinstance(part, str) else f"[{part}]" for part in error.absolute_path
    )
    message = f"{pointer}: {error.message}"
    if error.context:
        # `oneOf`/`allOf` failures (e.g. catalog.yaml's model/codex_model/
        # reasoning_effort tier-consistency check, routing.json's
        # team_recipes[] fixed-vs-dynamic field-set check) report only the
        # aggregate "not valid under any of the given schemas" at the
        # container path by default -- surface jsonschema's own best-guess
        # most-specific sub-error too, so the finding names the actual
        # offending field rather than only the containing object.
        best = jsonschema.exceptions.best_match(error.context)
        if best is not None:
            best_pointer = "$" + "".join(
                f"[{part!r}]" if isinstance(part, str) else f"[{part}]" for part in best.absolute_path
            )
            message += f" (most specific: {best_pointer}: {best.message})"
    return message


def _schema_errors(instance: Any, schema: dict[str, Any]) -> list[str]:
    validator = jsonschema.Draft202012Validator(schema)
    errors = sorted(validator.iter_errors(instance), key=lambda error: list(map(str, error.absolute_path)))
    return [_format_error(error) for error in errors]


def _find_duplicate_catalog_ids(catalog_text: str) -> list[str]:
    """Detect duplicate `agents:` block role ids in catalog.yaml's raw text.

    A supplementary check because `yaml.safe_load` (and this module's own
    schema validation, which operates on the already-parsed object) can no
    longer see a duplicate key by the time a plain dict comes back -- the
    later occurrence has already silently overwritten the earlier one, and
    PyYAML's default loader does not fail on this. This line-oriented raw-
    text scan is the sole layer that detects it, and it reports every
    duplicate found (SV-FR-6: report all, not just the first) rather than
    aborting the parse on the first one -- deliberately, so a duplicate id
    and an unrelated schema defect elsewhere in the same file can both
    surface in one run (AC-6).
    """
    seen: dict[str, int] = {}
    findings: list[str] = []
    in_agents_block = False
    for line_number, line in enumerate(catalog_text.splitlines(), start=1):
        if line.rstrip() == "agents:":
            in_agents_block = True
            continue
        if not in_agents_block:
            continue
        match = _ID_LINE.match(line)
        if not match:
            continue
        role_id = match.group(1)
        if role_id in seen:
            findings.append(
                f"$.agents[{role_id!r}]: duplicate role id (first seen at line {seen[role_id]}, "
                f"again at line {line_number})"
            )
        else:
            seen[role_id] = line_number
    return findings


def _find_missing_definitions(catalog: dict[str, Any], agents_root: Path) -> list[str]:
    """SV-FR-2: `definition` must resolve, relative to `roster/`, to a file
    that exists on disk. A filesystem check, not expressible in JSON Schema.
    """
    findings: list[str] = []
    agents = catalog.get("agents")
    if not isinstance(agents, dict):
        return findings
    for role_id, record in agents.items():
        if not isinstance(record, dict):
            continue
        definition = record.get("definition")
        if not isinstance(definition, str) or not definition:
            continue
        if not (agents_root / definition).is_file():
            findings.append(
                f"$.agents[{role_id!r}].definition: {definition!r} does not resolve to a file under {agents_root}"
            )
    return findings


def validate_catalog(catalog_text: str, catalog: Any, schema: dict[str, Any], agents_root: Path) -> list[str]:
    findings: list[str] = []
    findings.extend(_schema_errors(catalog, schema))
    findings.extend(_find_duplicate_catalog_ids(catalog_text))
    findings.extend(_find_missing_definitions(catalog, agents_root))
    return findings


def _find_duplicate_array_ids(items: Any, array_name: str) -> list[str]:
    """SV-FR-12: report every duplicate `id` within a single routes[] /
    risk_rules[] / team_recipes[] array, per-array rather than only the
    combined-arrays uniqueness `load_routing()` already enforces (and stops
    at the first violation of). Not expressible as a JSON Schema
    `uniqueItems` constraint because `uniqueItems` compares whole array
    elements, not one field of an object element.
    """
    findings: list[str] = []
    if not isinstance(items, list):
        return findings
    seen: dict[str, int] = {}
    for index, item in enumerate(items):
        if not isinstance(item, dict):
            continue
        item_id = item.get("id")
        if not isinstance(item_id, str) or not item_id:
            continue
        if item_id in seen:
            findings.append(
                f"$.{array_name}[{index}].id: duplicate id {item_id!r} "
                f"(also present at {array_name}[{seen[item_id]}])"
            )
        else:
            seen[item_id] = index
    return findings


def _find_cross_stack_inconsistency(routing: dict[str, Any]) -> list[str]:
    """SV-FR-20: `cross_stack.minimum_matches` must not exceed
    `len(cross_stack.route_ids)`. A cross-field numeric comparison between
    two sibling properties, which JSON Schema (without a bespoke
    `$data`-style extension this repo does not use elsewhere) cannot express
    cleanly -- implemented here instead of forced through the schema.
    """
    findings: list[str] = []
    cross_stack = routing.get("cross_stack")
    if not isinstance(cross_stack, dict):
        return findings
    route_ids = cross_stack.get("route_ids")
    minimum_matches = cross_stack.get("minimum_matches")
    if isinstance(route_ids, list) and isinstance(minimum_matches, int) and not isinstance(minimum_matches, bool):
        if minimum_matches > len(route_ids):
            findings.append(
                f"$.cross_stack.minimum_matches: {minimum_matches} exceeds "
                f"len(cross_stack.route_ids) == {len(route_ids)}"
            )
    return findings


def _find_team_recipe_inconsistencies(routing: dict[str, Any]) -> list[str]:
    """Same class of check as `_find_cross_stack_inconsistency`, for each
    `type: "fixed"` team_recipes[] entry's `minimum_matches` vs.
    `route_ids` and `minimum_members_selected` vs. `members` (SV-FR-22).
    """
    findings: list[str] = []
    recipes = routing.get("team_recipes")
    if not isinstance(recipes, list):
        return findings
    for index, recipe in enumerate(recipes):
        if not isinstance(recipe, dict) or recipe.get("type") != "fixed":
            continue
        recipe_id = recipe.get("id", f"index {index}")
        route_ids = recipe.get("route_ids")
        minimum_matches = recipe.get("minimum_matches")
        if (
            isinstance(route_ids, list)
            and isinstance(minimum_matches, int)
            and not isinstance(minimum_matches, bool)
            and minimum_matches > len(route_ids)
        ):
            findings.append(
                f'$.team_recipes[{index}] (id={recipe_id!r}).minimum_matches: {minimum_matches} '
                f"exceeds len(route_ids) == {len(route_ids)}"
            )
        members = recipe.get("members")
        minimum_members_selected = recipe.get("minimum_members_selected")
        if (
            isinstance(members, list)
            and isinstance(minimum_members_selected, int)
            and not isinstance(minimum_members_selected, bool)
            and minimum_members_selected > len(members)
        ):
            findings.append(
                f'$.team_recipes[{index}] (id={recipe_id!r}).minimum_members_selected: '
                f"{minimum_members_selected} exceeds len(members) == {len(members)}"
            )
    return findings


def validate_routing(routing: dict[str, Any], schema: dict[str, Any]) -> list[str]:
    findings: list[str] = []
    findings.extend(_schema_errors(routing, schema))
    if isinstance(routing, dict):
        findings.extend(_find_duplicate_array_ids(routing.get("routes"), "routes"))
        findings.extend(_find_duplicate_array_ids(routing.get("risk_rules"), "risk_rules"))
        findings.extend(_find_duplicate_array_ids(routing.get("team_recipes"), "team_recipes"))
        findings.extend(_find_duplicate_array_ids(routing.get("context_packs"), "context_packs"))
        findings.extend(_find_cross_stack_inconsistency(routing))
        findings.extend(_find_team_recipe_inconsistencies(routing))
    return findings


def run(
    catalog_path: Path = DEFAULT_CATALOG,
    routing_path: Path = DEFAULT_ROUTING,
    catalog_schema_path: Path = DEFAULT_CATALOG_SCHEMA,
    routing_schema_path: Path = DEFAULT_ROUTING_SCHEMA,
    agents_root: Path | None = None,
    roster_manifest_path: Path = DEFAULT_ROSTER_MANIFEST,
    roster_schema_path: Path = DEFAULT_ROSTER_SCHEMA,
) -> list[str]:
    """Return a deterministic, ordered list of finding strings. Empty means
    both files are schema-valid. A structurally-invalid file (malformed
    YAML/JSON) is itself reported as a finding, not swallowed or raised
    past the caller.
    """
    resolved_agents_root = agents_root if agents_root is not None else catalog_path.parent
    catalog_schema = json.loads(catalog_schema_path.read_text(encoding="utf-8"))
    routing_schema = json.loads(routing_schema_path.read_text(encoding="utf-8"))

    findings: list[str] = []

    # PP-FR-2's third instance/schema pair. Until Revision 5 this module was
    # described as needing "no new validation machinery"; it hardwires exactly
    # two pairs, so a third document genuinely needs new arguments and a new
    # call. Absent manifest is not a finding: a roster package predating the
    # manifest is the selector's error to raise by name, not this validator's.
    if roster_manifest_path.is_file():
        roster_schema = json.loads(roster_schema_path.read_text(encoding="utf-8"))
        try:
            roster_manifest = json.loads(roster_manifest_path.read_text(encoding="utf-8"))
        except json.JSONDecodeError as error:
            findings.append(f"{roster_manifest_path}: invalid JSON: {error}")
        else:
            findings.extend(
                f"{roster_manifest_path}: {message}"
                for message in _schema_errors(roster_manifest, roster_schema)
            )

    catalog_text = catalog_path.read_text(encoding="utf-8")
    try:
        catalog = load_catalog_yaml(catalog_path)
    except yaml.YAMLError as error:
        findings.append(f"{catalog_path}: invalid YAML: {error}")
    else:
        findings.extend(
            f"{catalog_path} {finding}"
            for finding in validate_catalog(catalog_text, catalog, catalog_schema, resolved_agents_root)
        )

    try:
        routing = load_routing_json(routing_path)
    except json.JSONDecodeError as error:
        findings.append(f"{routing_path}: invalid JSON: {error}")
    else:
        findings.extend(f"{routing_path} {finding}" for finding in validate_routing(routing, routing_schema))

    return findings


def _cli_description() -> str | None:
    if not __doc__:
        return None
    paragraph: list[str] = []
    for line in __doc__.strip().splitlines():
        stripped = line.strip()
        if not stripped:
            break
        paragraph.append(stripped)
    return " ".join(paragraph) if paragraph else None


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=_cli_description())
    parser.add_argument("--catalog", type=Path, default=DEFAULT_CATALOG)
    parser.add_argument("--routing", type=Path, default=DEFAULT_ROUTING)
    parser.add_argument("--catalog-schema", type=Path, default=DEFAULT_CATALOG_SCHEMA)
    parser.add_argument("--routing-schema", type=Path, default=DEFAULT_ROUTING_SCHEMA)
    parser.add_argument("--roster-manifest", type=Path, default=DEFAULT_ROSTER_MANIFEST)
    parser.add_argument("--roster-schema", type=Path, default=DEFAULT_ROSTER_SCHEMA)
    parser.add_argument(
        "--agents-root",
        type=Path,
        default=None,
        help="Base directory catalog.yaml's `definition` fields resolve against (default: --catalog's parent)",
    )
    args = parser.parse_args(argv)

    findings = run(
        args.catalog,
        args.routing,
        args.catalog_schema,
        args.routing_schema,
        args.agents_root,
        args.roster_manifest,
        args.roster_schema,
    )
    if findings:
        for finding in findings:
            print(finding, file=sys.stderr)
        return 1
    # Names every document actually checked. A success line that under-reports
    # its own scope is how a validator quietly stops covering something.
    validated = [str(args.catalog), str(args.routing)]
    if args.roster_manifest.is_file():
        validated.append(str(args.roster_manifest))
    print(f"schema validation passed: {', '.join(validated)} are schema-valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
