#!/usr/bin/env python3
"""Resolve a project-local overlay of `roster/orchestration/routing.json`.

Implements idea #6 (`roster/orchestration/runs/cadre-idea-6-routing-overlay-
2026-07-29/requirements.md`, `REQ-CADRE-BACKLOG-6`) -- a project-local
customization surface for routing configuration, analogous to
`roster/shared/`'s `.agents/shared/<filename>` overlay (`roster/shared/src/
resolve.py`) but with per-`routing.json`-construct merge rules instead of a
single deep-merge/narrowing-only rule, because most of `routing.json`'s
sections carry gating/review-separation semantics `roster/shared/`'s
policy-preference files do not (RO-FR-3..RO-FR-15, requirements.md SS4/SS5).

Discovery (RO-FR-1, RO-NFR-1): a project expresses its overlay as a single
JSON file at `.agents/orchestration/routing-overlay.json`, found by walking
up from the current directory to the nearest `.git` boundary -- the exact
same algorithm `roster/shared/src/resolve.py::find_project_overlay` and
`roster/knowledge-store/src/config.py` already use, reused here via
`resolve.find_file_at_project_root` rather than reimplemented (this
mechanism is JSON-only, unlike `.agents/shared/`'s YAML-or-JSON files, so it
does not need a PyYAML dependency -- `routing.json` itself is JSON despite
its `.yaml` filename; see RO-NFR-3).

Per-section merge rules (RO-FR-3..RO-FR-15, `requirements.md` SS4/SS5):

- `routes[]` / `risk_rules[]`: an overlay may add a new `id`-keyed entry
  (must not collide with any `id` already present across `routes` +
  `risk_rules` + `team_recipes` combined -- RO-FR-3), and may widen an
  *existing* base entry's `keywords` / `keyword_groups` / `paths` by
  supplying a value for that field that is a superset of the base value
  (RO-FR-4); any other field present on a widen-patch entry must equal the
  base value exactly, or resolution fails closed (RO-FR-5). This applies
  uniformly to every base entry, not only ones that currently declare a
  `human_gate` (RO-FR-13) -- narrowing a base entry's matching conditions
  is treated as equivalent to weakening its `human_gate`/`reviewers`, even
  though those fields are never directly touched.
  `exclude_paths` is deliberately *not* a widen field: a new overlay entry
  may declare its own, but on an existing base entry it is immutable like
  `human_gate`. Its polarity is inverted relative to the widen fields --
  a superset of the base `exclude_paths` *narrows* the effective match --
  so `_widen_field_superset` would enforce exactly the wrong direction and
  let an overlay shed a base entry's `reviewers` coverage.
- `team_recipes[]`: purely additive. A new, non-colliding `id` may be added;
  an existing base `id` is fully immutable (no widen exception -- RO-FR-7).
- `change_intake`: `keywords` / `agents` / `quality_gates` are additive-only
  (RO-FR-8).
- `cross_stack`: `route_ids` / `support` are additive-only; `minimum_matches`
  may only decrease from the base value, never increase (RO-FR-9).
- `knowledge_focus`: ordinary structured-file deep-merge, overlay wins per
  key -- no narrowing restriction, since it carries no gating/dispatch/
  review-separation semantics (RO-FR-10).
- `ignored_gates`: may only shrink (remove an already-present entry), never
  grow (RO-FR-11, Gap G-1).
- `version`: fixed; an overlay may repeat the base value as a no-op but may
  not change it (RO-FR-12).

Fails closed (raises `RoutingOverlayError`, or exits non-zero from the CLI)
on: a malformed/unparsable overlay file, an unrecognized top-level or
per-section field, an `id` collision between an overlay-added entry and any
base `routes`/`risk_rules`/`team_recipes` entry, any attempt to add to
`ignored_gates` or change `version`, any attempt to change a base
`routes[]`/`risk_rules[]` entry's `primary`/`reviewers`/`support`/
`quality_gates`/`human_gate` field, and any attempt to narrow a base
entry's `keywords`/`keyword_groups`/`paths` (RO-NFR-2).

When no project-local overlay is found, the effective configuration is the
base file's own bytes, unchanged (RO-FR-2, AC-1) -- this module never
reformats `routing.json` when there is nothing to merge.

The materialized effective configuration is a plain JSON file in
`routing.json`'s own shape, so `routing_health.py --routing <path>` and
`schema_validate.py --routing <path>` (idea #10) can consume it with their
existing, unmodified `--routing` CLI argument -- zero code changes to either
checker (OD-4, RO-FR-16, RO-FR-17).

Run:

    python3 roster/orchestration/src/routing_overlay.py --out /tmp/effective-routing.json
    python3 roster/orchestration/src/routing_overlay.py --check

`--check` validates discovery + merge without requiring `--out`, in the same
spirit as `generate_role_metadata.py --check` / `generate_authority_aides.py
--check` (RO-NFR-6): non-zero exit and a stderr finding on any overlay
problem, zero exit (and a one-line confirmation) otherwise.
"""

from __future__ import annotations

import argparse
import json
import sys
import tempfile
from pathlib import Path
from typing import Any

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(Path(__file__).resolve().parent))
sys.path.insert(0, str(REPOSITORY_ROOT / "roster" / "shared" / "src"))

from resolve import deep_merge, find_file_at_project_root  # noqa: E402
from routing import load_routing  # noqa: E402

DEFAULT_ROUTING = REPOSITORY_ROOT / "roster" / "orchestration" / "routing.json"

# G-2 (requirements.md SS9): the overlay's own filename/location is a
# design-phase choice, not mandated by the requirements baseline. This
# mirrors `.agents/shared/<filename>`'s shape but lives under
# `.agents/orchestration/` (not `.agents/shared/`) because it overlays
# `roster/orchestration/routing.json`, a distinct artifact class from the
# `roster/shared/` policy-preference files. JSON-only (not YAML-or-JSON like
# `.agents/shared/`) because routing.json itself is JSON-shaped and this
# avoids a PyYAML dependency for a single small hand-authored file
# (RO-NFR-3).
OVERLAY_RELATIVE_PATH = Path(".agents") / "orchestration" / "routing-overlay.json"

_ROUTE_RISK_WIDEN_FIELDS = ("keywords", "keyword_groups", "paths")
_CHANGE_INTAKE_ADDITIVE_FIELDS = ("keywords", "agents", "quality_gates")
_CROSS_STACK_ADDITIVE_FIELDS = ("route_ids", "support")
_KNOWN_TOP_LEVEL_KEYS = frozenset(
    {
        "version",
        "ignored_gates",
        "change_intake",
        "routes",
        "risk_rules",
        "cross_stack",
        "team_recipes",
        "knowledge_focus",
    }
)


class RoutingOverlayError(ValueError):
    """A project-local routing.json overlay is malformed or violates a merge rule."""


def find_routing_overlay(start: Path | None = None) -> Path | None:
    """RO-FR-1/RO-NFR-1: discover `.agents/orchestration/routing-overlay.json`
    by walking up from `start` (default: cwd) to the nearest `.git`
    boundary, reusing `resolve.find_file_at_project_root` rather than a new
    walk-up implementation.
    """
    return find_file_at_project_root(OVERLAY_RELATIVE_PATH, start)


def _load_overlay(path: Path) -> dict[str, Any]:
    text = path.read_text(encoding="utf-8")
    try:
        loaded = json.loads(text)
    except json.JSONDecodeError as error:
        raise RoutingOverlayError(f"{path}: malformed overlay JSON: {error}") from error
    if not isinstance(loaded, dict):
        raise RoutingOverlayError(f"{path}: overlay root must be a JSON object")
    return loaded


def _get_optional_list(container: dict[str, Any], key: str, label: str) -> list[Any]:
    if key not in container or container[key] is None:
        return []
    value = container[key]
    if not isinstance(value, list):
        raise RoutingOverlayError(f"{label} must be a list")
    return value


def _entry_by_id(entries: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    return {entry.get("id"): entry for entry in entries}


def _widen_keyword_groups(section: str, entry_id: str, base_value: list[Any], overlay_value: list[Any]) -> list[Any]:
    """RO-FR-4/RO-FR-13 for `keyword_groups` specifically.

    `keyword_groups` is an AND-of-ORs: `routing.py::match_rule`'s
    `conjunctive_match` requires *every* outer group to have at least one
    matching keyword, while each group's own inner list is an OR. This makes
    "widen" the OPPOSITE of a plain list-superset check on the outer list --
    appending a brand-new outer group adds a new mandatory AND-condition,
    which *narrows* overall matching (some base matches are lost), even
    though it looks additive. The only operation that is actually safe to
    call "widening" here is adding a keyword to an EXISTING group's inner
    OR-list, which relaxes that one AND-clause without adding a new one.

    Rejects: a different number of outer groups (covers both "added a new
    mandatory AND-group" and "dropped an existing one" -- either changes
    which base match combinations remain reachable, so neither is treated
    as a safe no-questions-asked widen), and removing any keyword already
    present in an existing group's inner list.
    """
    if len(overlay_value) != len(base_value):
        raise RoutingOverlayError(
            f"{section} overlay entry {entry_id!r} changes the number of 'keyword_groups' outer "
            f"groups (base has {len(base_value)}, overlay has {len(overlay_value)}); each outer "
            "group is a mandatory AND-condition, so adding or removing one changes which task/file "
            "combinations match -- an overlay may only add keywords to an EXISTING group's inner "
            "OR-list, never add, remove, or reorder outer groups"
        )
    result_groups: list[Any] = []
    for index, (base_group, overlay_group) in enumerate(zip(base_value, overlay_value)):
        if not isinstance(overlay_group, list):
            raise RoutingOverlayError(
                f"{section} overlay entry {entry_id!r} field 'keyword_groups'[{index}] must be a list"
            )
        missing = [item for item in base_group if item not in overlay_group]
        if missing:
            raise RoutingOverlayError(
                f"{section} overlay entry {entry_id!r} narrows base 'keyword_groups'[{index}] by "
                f"omitting already-present value(s) {missing!r}; an overlay may only widen an "
                "existing group's inner OR-list (append), never remove or replace an element "
                "already present"
            )
        seen: list[Any] = []
        merged: list[Any] = []
        for item in overlay_group:
            if item in seen:
                continue
            seen.append(item)
            merged.append(item)
        result_groups.append(merged)
    return result_groups


def _widen_field_superset(section: str, entry_id: str, field: str, base_value: Any, overlay_value: Any) -> list[Any]:
    """RO-FR-4/RO-FR-13: `overlay_value` must be a superset of `base_value` --
    every element already present in the base entry's field must still be
    present in the overlay-supplied value, or this is treated as a narrowing
    attempt (functionally equivalent to weakening `human_gate`/`reviewers`
    without ever touching those fields) and rejected, regardless of whether
    this particular base entry currently declares a `human_gate` at all.

    `keyword_groups` has different (AND-of-ORs) semantics from the flat OR
    lists `keywords`/`paths` -- see `_widen_keyword_groups`, which this
    delegates to.
    """
    if not isinstance(overlay_value, list):
        raise RoutingOverlayError(f"{section} overlay entry {entry_id!r} field {field!r} must be a list")
    base_value = base_value or []
    if field == "keyword_groups":
        return _widen_keyword_groups(section, entry_id, base_value, overlay_value)
    missing = [item for item in base_value if item not in overlay_value]
    if missing:
        raise RoutingOverlayError(
            f"{section} overlay entry {entry_id!r} narrows base {field!r} by omitting "
            f"already-present value(s) {missing!r}; an overlay may only widen a base entry's "
            "matching conditions (append), never remove or replace an element already present"
        )
    seen: list[Any] = []
    result: list[Any] = []
    for item in overlay_value:
        if item in seen:
            continue
        seen.append(item)
        result.append(item)
    return result


def _apply_widen_patch(section: str, base_entry: dict[str, Any], overlay_entry: dict[str, Any]) -> dict[str, Any]:
    """RO-FR-5: every field on `overlay_entry` other than `id` and the widen
    fields must equal the base entry's value exactly (a no-op restatement is
    allowed; any actual change -- e.g. `human_gate`, `reviewers`, `primary`,
    `support`, `quality_gates` -- fails closed, naming the entry id and
    field, matching AC-6).
    """
    entry_id = overlay_entry.get("id")
    patched = dict(base_entry)
    for key, value in overlay_entry.items():
        if key == "id" or key in _ROUTE_RISK_WIDEN_FIELDS:
            continue
        if base_entry.get(key) != value:
            raise RoutingOverlayError(
                f"{section} overlay entry {entry_id!r} may not change field {key!r} "
                f"(base value: {base_entry.get(key)!r}, overlay value: {value!r}); only "
                f"{', '.join(_ROUTE_RISK_WIDEN_FIELDS)} may be widened on a base entry"
            )
    for field in _ROUTE_RISK_WIDEN_FIELDS:
        if field not in overlay_entry:
            continue
        patched[field] = _widen_field_superset(section, entry_id, field, base_entry.get(field), overlay_entry[field])
    return patched


def _merge_route_or_risk_rule_section(
    section: str,
    base_entries: list[dict[str, Any]],
    overlay_entries: list[Any],
    combined_ids_seen: set[Any],
) -> list[dict[str, Any]]:
    base_by_id = _entry_by_id(base_entries)
    effective = [dict(entry) for entry in base_entries]
    position_by_id = {entry.get("id"): index for index, entry in enumerate(effective)}
    for overlay_entry in overlay_entries:
        if not isinstance(overlay_entry, dict):
            raise RoutingOverlayError(f"{section} overlay entries must be objects")
        entry_id = overlay_entry.get("id")
        if not isinstance(entry_id, str) or not entry_id:
            raise RoutingOverlayError(f"{section} overlay entry is missing a non-empty string 'id'")
        if entry_id in base_by_id:
            effective[position_by_id[entry_id]] = _apply_widen_patch(section, base_by_id[entry_id], overlay_entry)
            continue
        if entry_id in combined_ids_seen:
            raise RoutingOverlayError(
                f"{section} overlay entry id {entry_id!r} collides with an existing routes/"
                "risk_rules/team_recipes id"
            )
        effective.append(dict(overlay_entry))
        combined_ids_seen.add(entry_id)
    return effective


def _merge_team_recipes(
    base_entries: list[dict[str, Any]], overlay_entries: list[Any], combined_ids_seen: set[Any]
) -> list[dict[str, Any]]:
    """RO-FR-7: purely additive -- a base team_recipes[] entry is fully
    immutable to an overlay, with no field-level widen exception.
    """
    base_by_id = _entry_by_id(base_entries)
    effective = [dict(entry) for entry in base_entries]
    for overlay_entry in overlay_entries:
        if not isinstance(overlay_entry, dict):
            raise RoutingOverlayError("team_recipes overlay entries must be objects")
        entry_id = overlay_entry.get("id")
        if not isinstance(entry_id, str) or not entry_id:
            raise RoutingOverlayError("team_recipes overlay entry is missing a non-empty string 'id'")
        if entry_id in base_by_id:
            raise RoutingOverlayError(
                f"team_recipes overlay may not modify base entry {entry_id!r}; base team "
                "recipes are fully immutable, only new team_recipes entries may be added"
            )
        if entry_id in combined_ids_seen:
            raise RoutingOverlayError(
                f"team_recipes overlay entry id {entry_id!r} collides with an existing "
                "routes/risk_rules/team_recipes id"
            )
        effective.append(dict(overlay_entry))
        combined_ids_seen.add(entry_id)
    return effective


def _merge_change_intake(base_ci: dict[str, Any], overlay_ci: Any) -> dict[str, Any]:
    """RO-FR-8: `keywords`/`agents`/`quality_gates` are additive-only."""
    if overlay_ci is None:
        return dict(base_ci)
    if not isinstance(overlay_ci, dict):
        raise RoutingOverlayError("change_intake overlay must be an object")
    unknown = set(overlay_ci) - set(_CHANGE_INTAKE_ADDITIVE_FIELDS)
    if unknown:
        raise RoutingOverlayError(f"change_intake overlay has unrecognized field(s): {sorted(unknown)}")
    merged = dict(base_ci)
    for field in _CHANGE_INTAKE_ADDITIVE_FIELDS:
        if field not in overlay_ci:
            continue
        addition = overlay_ci[field]
        if not isinstance(addition, list):
            raise RoutingOverlayError(f"change_intake.{field} overlay must be a list")
        base_values = base_ci.get(field, []) or []
        merged[field] = base_values + [item for item in addition if item not in base_values]
    return merged


def _merge_cross_stack(base_cs: dict[str, Any], overlay_cs: Any) -> dict[str, Any]:
    """RO-FR-9: `route_ids`/`support` are additive-only; `minimum_matches`
    may only decrease from the base value, never increase.
    """
    if overlay_cs is None:
        return dict(base_cs)
    if not isinstance(overlay_cs, dict):
        raise RoutingOverlayError("cross_stack overlay must be an object")
    unknown = set(overlay_cs) - set(_CROSS_STACK_ADDITIVE_FIELDS) - {"minimum_matches"}
    if unknown:
        raise RoutingOverlayError(f"cross_stack overlay has unrecognized field(s): {sorted(unknown)}")
    merged = dict(base_cs)
    for field in _CROSS_STACK_ADDITIVE_FIELDS:
        if field not in overlay_cs:
            continue
        addition = overlay_cs[field]
        if not isinstance(addition, list):
            raise RoutingOverlayError(f"cross_stack.{field} overlay must be a list")
        base_values = base_cs.get(field, []) or []
        merged[field] = base_values + [item for item in addition if item not in base_values]
    if "minimum_matches" in overlay_cs:
        overlay_value = overlay_cs["minimum_matches"]
        base_value = base_cs.get("minimum_matches")
        if not isinstance(overlay_value, int) or isinstance(overlay_value, bool):
            raise RoutingOverlayError("cross_stack.minimum_matches overlay must be an integer")
        if isinstance(base_value, int) and not isinstance(base_value, bool) and overlay_value > base_value:
            raise RoutingOverlayError(
                "cross_stack.minimum_matches overlay may only decrease the base value "
                f"({base_value}); overlay supplied {overlay_value}, which would require more "
                "matches to trigger cross-stack support and would reduce coverage"
            )
        merged["minimum_matches"] = overlay_value
    return merged


def _merge_knowledge_focus(base_kf: dict[str, Any], overlay_kf: Any) -> dict[str, Any]:
    """RO-FR-10: ordinary structured-file deep-merge, no narrowing
    restriction -- reuses `resolve.deep_merge` directly rather than a new
    merge implementation, since this section's semantics are identical to
    `roster/shared/`'s ordinary structured-file merge rule.
    """
    if overlay_kf is None:
        return dict(base_kf)
    if not isinstance(overlay_kf, dict):
        raise RoutingOverlayError("knowledge_focus overlay must be an object")
    return deep_merge(base_kf, overlay_kf)


def _merge_ignored_gates(base_gates: list[Any], overlay_gates: Any) -> list[Any]:
    """RO-FR-11 (Gap G-1): may only shrink, never grow."""
    if overlay_gates is None:
        return list(base_gates)
    if not isinstance(overlay_gates, list):
        raise RoutingOverlayError("ignored_gates overlay must be a list")
    base_set = set(base_gates)
    added = [gate for gate in overlay_gates if gate not in base_set]
    if added:
        raise RoutingOverlayError(
            f"ignored_gates overlay may not add new suppression(s) {added!r} not present in the "
            "base ignored_gates; an overlay may only remove already-present entries"
        )
    return list(overlay_gates)


def _check_version(base_version: Any, overlay: dict[str, Any]) -> None:
    """RO-FR-12: `version` is a fixed schema-version contract field."""
    if "version" in overlay and overlay["version"] != base_version:
        raise RoutingOverlayError(
            f"overlay may not change 'version' from {base_version!r} to {overlay['version']!r}; "
            "it is a fixed schema-version contract field, not a per-project dial"
        )


def merge_routing(base: dict[str, Any], overlay: dict[str, Any]) -> dict[str, Any]:
    """Apply every RO-FR-3..RO-FR-15 per-section merge rule and return the
    effective configuration. Raises `RoutingOverlayError` on any violation.
    """
    if not isinstance(overlay, dict):
        raise RoutingOverlayError("overlay root must be a JSON object")
    unknown = set(overlay) - _KNOWN_TOP_LEVEL_KEYS
    if unknown:
        raise RoutingOverlayError(f"overlay has unrecognized top-level field(s): {sorted(unknown)}")

    _check_version(base.get("version"), overlay)

    combined_ids_seen: set[Any] = {
        entry.get("id")
        for entry in [
            *base.get("routes", []),
            *base.get("risk_rules", []),
            *base.get("team_recipes", []),
        ]
    }

    effective = dict(base)
    effective["routes"] = _merge_route_or_risk_rule_section(
        "routes",
        base.get("routes", []),
        _get_optional_list(overlay, "routes", "routes overlay"),
        combined_ids_seen,
    )
    effective["risk_rules"] = _merge_route_or_risk_rule_section(
        "risk_rules",
        base.get("risk_rules", []),
        _get_optional_list(overlay, "risk_rules", "risk_rules overlay"),
        combined_ids_seen,
    )
    effective["team_recipes"] = _merge_team_recipes(
        base.get("team_recipes", []),
        _get_optional_list(overlay, "team_recipes", "team_recipes overlay"),
        combined_ids_seen,
    )
    effective["change_intake"] = _merge_change_intake(base.get("change_intake", {}), overlay.get("change_intake"))
    effective["cross_stack"] = _merge_cross_stack(base.get("cross_stack", {}), overlay.get("cross_stack"))
    effective["knowledge_focus"] = _merge_knowledge_focus(
        base.get("knowledge_focus", {}), overlay.get("knowledge_focus")
    )
    effective["ignored_gates"] = _merge_ignored_gates(base.get("ignored_gates", []), overlay.get("ignored_gates"))
    return effective


def _validate_effective(effective: dict[str, Any]) -> None:
    """Round-trip the merged configuration through `routing.load_routing`
    before it is ever returned/materialized -- reuses `load_routing`'s
    existing uniqueness/shape invariants (combined id-uniqueness,
    keyword_groups shape, team_recipes dynamic instance bounds) rather than
    duplicating that validation, matching `generate_role_metadata.py::
    _validate_routing_content`'s existing round-trip pattern.
    """
    with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False, encoding="utf-8") as handle:
        json.dump(effective, handle)
        temporary_path = Path(handle.name)
    try:
        load_routing(temporary_path)
    except ValueError as error:
        raise RoutingOverlayError(f"effective configuration failed validation: {error}") from error
    finally:
        temporary_path.unlink(missing_ok=True)


def _resolve(
    base_path: Path, start: Path | None, overlay_path: Path | None
) -> tuple[dict[str, Any], str, Path | None]:
    base_text = base_path.read_text(encoding="utf-8")
    base = json.loads(base_text)
    if not isinstance(base, dict):
        raise RoutingOverlayError(f"{base_path}: root must be a JSON object")
    resolved_overlay_path = overlay_path if overlay_path is not None else find_routing_overlay(start)
    if resolved_overlay_path is None:
        return base, base_text, None
    overlay = _load_overlay(resolved_overlay_path)
    effective = merge_routing(base, overlay)
    _validate_effective(effective)
    return effective, base_text, resolved_overlay_path


def resolve_effective_routing(
    base_path: Path = DEFAULT_ROUTING, start: Path | None = None, overlay_path: Path | None = None
) -> tuple[dict[str, Any], Path | None]:
    """RO-FR-2/AC-1: with no project-local overlay, returns the base
    configuration exactly (parsed, but content-identical). With an overlay,
    returns the merged effective configuration. Returns `(config,
    overlay_path_or_None)`.
    """
    effective, _base_text, resolved_overlay_path = _resolve(base_path, start, overlay_path)
    return effective, resolved_overlay_path


def materialize_effective_routing(
    out_path: Path,
    base_path: Path = DEFAULT_ROUTING,
    start: Path | None = None,
    overlay_path: Path | None = None,
) -> Path | None:
    """Write the effective configuration to `out_path`, for
    `routing_health.py`/`schema_validate.py --routing <out_path>` to consume
    (RO-FR-16/RO-FR-17, OD-4). RO-FR-2/AC-1: with no overlay, `out_path`
    receives the base file's own bytes verbatim -- never a re-serialized
    round-trip -- so the no-overlay case is byte-for-byte identical to
    `routing.json` itself, not merely equivalent JSON. Returns the overlay
    path that was applied, or `None` if no overlay was found.
    """
    effective, base_text, resolved_overlay_path = _resolve(base_path, start, overlay_path)
    if resolved_overlay_path is None:
        out_path.write_text(base_text, encoding="utf-8")
    else:
        out_path.write_text(json.dumps(effective, indent=2) + "\n", encoding="utf-8")
    return resolved_overlay_path


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="routing_overlay.py",
        description="Resolve a project-local overlay of roster/orchestration/routing.json",
    )
    parser.add_argument(
        "--routing", type=Path, default=DEFAULT_ROUTING, help="Base routing.json to resolve an overlay against"
    )
    parser.add_argument(
        "--project",
        type=Path,
        default=None,
        help="Directory to discover the project-local overlay from (default: cwd)",
    )
    parser.add_argument(
        "--overlay", type=Path, default=None, help="Explicit overlay file path, bypassing walk-up discovery"
    )
    parser.add_argument("--out", type=Path, default=None, help="Materialize the effective configuration to this path")
    parser.add_argument(
        "--check",
        action="store_true",
        help="Validate discovery and merge only; exit non-zero with findings on any overlay problem",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        if args.out is not None:
            resolved_overlay_path = materialize_effective_routing(
                args.out, base_path=args.routing, start=args.project, overlay_path=args.overlay
            )
            effective = None
        else:
            effective, resolved_overlay_path = resolve_effective_routing(
                base_path=args.routing, start=args.project, overlay_path=args.overlay
            )
    except (OSError, ValueError) as error:
        sys.stderr.write(f"error: {error}\n")
        return 1

    if args.check:
        if resolved_overlay_path is None:
            print(
                "routing overlay check passed: no project-local overlay found; "
                "effective configuration is the base routing.json unchanged"
            )
        else:
            print(f"routing overlay check passed: {resolved_overlay_path} resolves cleanly against {args.routing}")
        return 0

    if args.out is not None:
        suffix = f" (overlay: {resolved_overlay_path})" if resolved_overlay_path else " (no overlay found)"
        print(f"Wrote effective routing configuration to {args.out}{suffix}")
    else:
        sys.stdout.write(json.dumps(effective, indent=2) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
