#!/usr/bin/env python3
"""Guide a project through generating `.agents/shared/<filename>` overlays.

Implements REQ-SC-GENERIC-0001 rev 2 / ARCH-SC-GENERIC-0001 (G3-approved):
`cadre init` walks a project through RG-A (stack/tooling opinions), RG-B
(governance/autonomy narrowing), and RG-C (platform impact-profile guided fill-in)
and writes the resulting overlays through a single, containment-checked write
chokepoint. It never invents a new config format: every file it writes is
exactly the same `.agents/shared/<filename>` overlay `roster/shared/src/
resolve.py` already knows how to resolve, and every generated overlay is
validated by calling `resolve_shared_config()` against it in-process before
success is reported.

Defaults come first: a run with no answer source at all is legal and keeps
every shipped default, planning no writes -- overlays are sparse, so "keep
the default" means "write no overlay for that field", and a project with no
overlay resolves to exactly the shipped values. `--set [REGION:]PATH=VALUE`
overrides individual fields from there; `--answers` and `--interactive`
remain available for a full pass, but neither is required.

Nothing is written without `--force`; omitting it (the default) previews
only. See roster/shared/README.md for the underlying overlay/merge rules this
module builds on top of, and AGENT.md / REQ-SC-GENERIC-0001 rev 2 for the
requirement numbers referenced throughout this file's docstrings and error
messages.
"""

from __future__ import annotations

import argparse
import difflib
import hashlib
import json
import os
import sys
import tempfile
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

SRC_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SRC_DIR))

from resolve import (  # noqa: E402
    _AUTONOMY_FILENAME,
    _AUTONOMY_FIXED_KEYS,
    _AUTONOMY_RESTRICTIVENESS_RANK,
    _autonomy_leaf_paths,
    _autonomy_rank,
    _check_autonomy_overlay,
    _is_same_or_descendant,
    _load_structured,
    _require_yaml,
    _resolve_existing_ancestor,
    OverlayError,
    PROJECT_OVERLAY_RELATIVE_DIR,
    SHARED_DEFAULTS_DIR,
    deep_merge,
    resolve_shared_config,
)

REPO_ROOT = SRC_DIR.parents[2]
PRESETS_DIR = SHARED_DEFAULTS_DIR / "init-presets"

TEAM_PROFILE_FILENAME = "team-profile.yaml"
TECHNOLOGY_STANDARDS_FILENAME = "technology-standards.md"
LIBRARY_STANDARDS_FILENAME = "library-standards.yaml"
GUARDRAILS_FILENAME = "cloud-guardrails.md"
PLATFORM_FILENAME = "platform-impact-profile.yaml"

# Per roster/shared/platform-impact-profile.yaml's own header comment: the only
# three recognized applicability values (C-002/finding-3). Exported so the
# interactive collector presents this as a closed choice rather than free
# text, mirroring the autonomy allowlist pattern.
PLATFORM_APPLICABILITY_VALUES = ("applicable", "not-applicable", "unknown")

ALL_SECTIONS = ("rg-a-stack", "rg-b-governance", "rg-c-platform")

# Requirement B-004 / THREAT-MODEL-HARDENING-2: a heuristic safety net, not a
# complete solution. Case-insensitive substring match against phrasing that
# would read as an override/negation of the global guardrail baseline rather
# than a genuinely additive project-specific guardrail.
GUARDRAILS_DENYLIST = (
    "does not apply",
    "is exempt",
    "overrides the above",
    "instead of",
    "replaces",
    "supersedes",
    "no longer applies",
)

MANAGED_START = "<!-- agents-init:managed:start -->"
MANAGED_END = "<!-- agents-init:managed:end -->"

FIELD_DECISION_STATUSES = ("kept", "overridden", "deferred")
FIELD_DECISION_CATEGORIES = ("stack", "governance")


class InitError(Exception):
    """A generated overlay, answer set, or write request is invalid."""


# --------------------------------------------------------------------------
# A-002: containment / self-checkout guard and the single write chokepoint.
# --------------------------------------------------------------------------


def _self_checkout_markers_present(root: Path) -> bool:
    return (root / "roster" / "shared" / TEAM_PROFILE_FILENAME).is_file() and (
        root / "bin" / "subcommands.tsv"
    ).is_file()


def _refuse_if_self_checkout_resolved(resolved: Path) -> None:
    """Same check as `refuse_if_self_checkout`, but operating on a path the
    CALLER has already resolved.

    Callers that also need the resolved identity for something else (e.g.
    the write chokepoint below, which needs it to compute the destination
    path too) must resolve `target_root` exactly once and pass that SAME
    resolved `Path` object here, immediately before using it, rather than
    resolving it again independently (a TOCTOU gap: an earlier, separate
    `resolve()` used only for the check could diverge from a later, separate
    `resolve()` actually used for the write, e.g. via a symlink swapped in
    between calls)."""
    if _is_same_or_descendant(resolved, REPO_ROOT):
        raise InitError(
            f"refusing to write into this agents suite's own checkout ({REPO_ROOT}); "
            "`cadre init` writes only into a consuming project's .agents/shared/, never here"
        )
    # Walk from the target up through its ancestors (not just the target
    # itself), so a target that is a genuine subdirectory of an unrelated
    # clone of this same suite is also refused.
    current = _resolve_existing_ancestor(resolved)
    while True:
        if _self_checkout_markers_present(current):
            raise InitError(
                f"refusing to write: {current} contains roster/shared/{TEAM_PROFILE_FILENAME} "
                "and bin/subcommands.tsv, so it looks like another checkout of this same agents "
                "suite rather than a consuming project"
            )
        parent = current.parent
        if parent == current:
            break
        current = parent


def refuse_if_self_checkout(target_root: Path) -> None:
    """Raise InitError if `target_root` is this suite's own checkout (or a
    descendant of it), whether that's *this* clone (compared by filesystem
    identity, not path string) or an unrelated clone of the same repository
    (detected by marker files present at the target or any of its
    ancestors).

    Standalone convenience wrapper for call sites (CLI entry, tests) that
    only need the check itself and have no other use for the resolved path.
    `_write_overlay` below does NOT use this wrapper — see its own docstring
    for why it resolves `target_root` once and calls
    `_refuse_if_self_checkout_resolved` directly on that same value."""
    _refuse_if_self_checkout_resolved(target_root.resolve(strict=False))


def _write_overlay(target_root: Path, filename: str, content: str) -> Path:
    """The ONLY function in this module allowed to write generated overlay
    output to disk. Resolves target_root/.agents/shared/filename, requires it
    stays under target_root.resolve() (rejecting symlink escapes the same way
    the Agentic SDLC kernel's confined_path() does), and refuses self-checkout
    targets (A-002).

    Finding B (TOCTOU): `target_root` is resolved exactly ONCE, into
    `resolved_root`, and that same resolved value is what both the
    destination path is built from AND what the self-checkout check runs
    against, immediately before `mkdir`/`write_text`. There is no separate,
    independent `resolve()` of `target_root` anywhere else in this function
    whose result could diverge from the one actually used for the write.
    """
    resolved_root = target_root.resolve()
    if not resolved_root.is_dir():
        raise InitError(f"--target does not exist or is not a directory: {target_root}")
    dest = resolved_root / PROJECT_OVERLAY_RELATIVE_DIR / filename
    resolved_dest = dest.resolve(strict=False)
    if not _is_same_or_descendant(resolved_dest, resolved_root):
        raise InitError(f"refusing to write outside target root (possible symlink escape): {dest}")
    # Re-verify self-checkout identity against `resolved_root` itself,
    # immediately before the write, using the SAME resolved value computed
    # above rather than resolving target_root again.
    _refuse_if_self_checkout_resolved(resolved_root)
    resolved_dest.parent.mkdir(parents=True, exist_ok=True)
    resolved_dest.write_text(content, encoding="utf-8")
    return resolved_dest


def _existing_overlay_path(target_root: Path, filename: str) -> Path:
    return target_root.resolve() / PROJECT_OVERLAY_RELATIVE_DIR / filename


def _read_existing_overlay_text(target_root: Path, filename: str) -> str | None:
    path = _existing_overlay_path(target_root, filename)
    if path.is_file():
        return path.read_text(encoding="utf-8")
    return None


# --------------------------------------------------------------------------
# A-001: validate every generated overlay by calling resolve_shared_config()
# against it in-process, in a throwaway project tree, before reporting
# success.
# --------------------------------------------------------------------------


def validate_overlay_content(filename: str, content: str) -> Any:
    with tempfile.TemporaryDirectory(prefix="agents-init-validate-") as tmp:
        root = Path(tmp)
        (root / ".git").mkdir()
        overlay_dir = root / PROJECT_OVERLAY_RELATIVE_DIR
        overlay_dir.mkdir(parents=True)
        (overlay_dir / filename).write_text(content, encoding="utf-8")
        project = root / "project"
        project.mkdir()
        return resolve_shared_config(filename, start=project)


# --------------------------------------------------------------------------
# Structured (YAML) overlay content builders — RG-A team-profile.yaml /
# library-standards.yaml, RG-C platform-impact-profile.yaml.
# --------------------------------------------------------------------------


def _dump_yaml(data: dict[str, Any]) -> str:
    return _require_yaml().safe_dump(data, sort_keys=False)


def build_structured_overlay(
    target_root: Path, filename: str, fragment: dict[str, Any]
) -> tuple[str, dict[str, Any]] | None:
    """Deep-merge `fragment` over the EXISTING overlay file (not the global
    default), so a manually-edited field the current run's fragment doesn't
    touch survives untouched (A-004/C-006 idempotency). Returns None if there
    is nothing to write (no fragment and no existing file)."""
    existing_text = _read_existing_overlay_text(target_root, filename)
    existing: dict[str, Any] = _require_yaml().safe_load(existing_text) or {} if existing_text else {}
    if not fragment and existing_text is None:
        return None
    merged = deep_merge(existing, fragment) if fragment else existing
    return _dump_yaml(merged), merged


# --------------------------------------------------------------------------
# Markdown addenda — RG-A technology-standards.md, RG-B cloud-guardrails.md.
# Managed-block markers keep re-runs from clobbering manually added prose
# outside the block the tool owns.
# --------------------------------------------------------------------------


def _replace_managed_block(existing_text: str | None, managed_body: str) -> str:
    block = f"{MANAGED_START}\n{managed_body.rstrip()}\n{MANAGED_END}"
    if existing_text is None:
        return block + "\n"
    if MANAGED_START in existing_text and MANAGED_END in existing_text:
        before, rest = existing_text.split(MANAGED_START, 1)
        _, after = rest.split(MANAGED_END, 1)
        return f"{before}{block}{after}"
    separator = "" if existing_text.endswith("\n") else "\n"
    return f"{existing_text}{separator}{block}\n"


def _extract_managed_block_body(existing_text: str | None) -> str | None:
    """Return the raw text currently inside the managed block, or None if
    there is no existing text or no managed block yet. Used so a rebuild of
    the managed block can be merged with (rather than replace) whatever a
    prior run already wrote there (A-004)."""
    if not existing_text:
        return None
    if MANAGED_START in existing_text and MANAGED_END in existing_text:
        _, rest = existing_text.split(MANAGED_START, 1)
        body, _ = rest.split(MANAGED_END, 1)
        return body.strip("\n")
    return None


# Separator between prior-run addendum entries inside technology-standards.md's
# managed block (finding 1): each `cadre init` run that supplies a new
# addendum appends it as its own entry rather than replacing whatever a prior
# run already wrote there. Kept distinct from ordinary blank-line paragraph
# breaks so a single multi-paragraph addendum from one run is never split
# apart and mistaken for multiple entries on the next merge.
PROSE_ADDENDUM_ENTRY_MARKER = "<!-- agents-init:addendum-entry -->"


def _extract_prose_addendum_entries(existing_text: str | None) -> list[str]:
    body = _extract_managed_block_body(existing_text)
    if not body:
        return []
    return [entry.strip() for entry in body.split(PROSE_ADDENDUM_ENTRY_MARKER) if entry.strip()]


def build_prose_addendum_overlay(
    target_root: Path, filename: str, addendum_text: str | None
) -> str | None:
    """Appends `addendum_text` as its own dated/labeled entry inside the
    managed block, merged with (never replacing) whatever addendum entries a
    prior run already wrote there (A-004/finding-1)."""
    existing_text = _read_existing_overlay_text(target_root, filename)
    if not addendum_text and existing_text is None:
        return None
    if not addendum_text:
        return existing_text
    entries = _extract_prose_addendum_entries(existing_text)
    if addendum_text not in entries:
        entries.append(addendum_text)
    body = f"\n\n{PROSE_ADDENDUM_ENTRY_MARKER}\n\n".join(entries)
    return _replace_managed_block(existing_text, body)


def scan_guardrail_bullet(bullet: str) -> str | None:
    """Return a rejection reason, or None if the bullet is acceptable."""
    lowered = bullet.lower()
    for phrase in GUARDRAILS_DENYLIST:
        if phrase in lowered:
            return (
                f"contains override/negation phrasing ({phrase!r}); guardrail addenda must be "
                "purely additive, not a relaxation of the global baseline"
            )
    return None


def _extract_guardrail_bullets(existing_text: str | None) -> list[str]:
    body = _extract_managed_block_body(existing_text)
    if not body:
        return []
    bullets = []
    for line in body.splitlines():
        stripped = line.strip()
        if stripped.startswith("- "):
            bullets.append(stripped[2:])
    return bullets


def build_guardrails_overlay(
    target_root: Path, bullets: list[str]
) -> tuple[str | None, list[tuple[str, str]]]:
    """Returns (content_or_None, rejected) where rejected is a list of
    (bullet, reason) pairs. Content is None (no write) if there is nothing to
    write and no rejections either need surfacing.

    Bullets already present in the existing managed block are read back out
    and unioned with this run's newly accepted bullets (order-preserving,
    exact-duplicate deduped) rather than discarded, so a prior run's accepted
    bullet always survives a later run that doesn't re-supply it (A-004/
    finding-1)."""
    rejected: list[tuple[str, str]] = []
    accepted: list[str] = []
    for bullet in bullets:
        reason = scan_guardrail_bullet(bullet)
        if reason is not None:
            rejected.append((bullet, reason))
        else:
            accepted.append(bullet)
    existing_text = _read_existing_overlay_text(target_root, GUARDRAILS_FILENAME)
    if not accepted and existing_text is None:
        return None, rejected
    if not accepted:
        return existing_text, rejected
    merged_bullets = _extract_guardrail_bullets(existing_text)
    for bullet in accepted:
        if bullet not in merged_bullets:
            merged_bullets.append(bullet)
    body = "## Project-specific additional guardrails\n\n" + "\n".join(f"- {b}" for b in merged_bullets)
    return _replace_managed_block(existing_text, body), rejected


# --------------------------------------------------------------------------
# RG-B autonomy overlay — B-002/B-003 allowlist + fixed-key enforcement reuses
# resolve.py's ranking/validation directly; this module never re-implements
# or re-ranks autonomy values itself.
# --------------------------------------------------------------------------


def autonomy_allowed_choices(default_value: str) -> list[str]:
    """Every ranked value at or above (more restrictive than) the default's
    rank — the exhaustive, closed set `cadre init` may ever offer or accept
    for an agent-autonomy.yaml field. No free text is ever accepted."""
    default_rank = _autonomy_rank("<candidate>", default_value)
    return [
        value
        for value, rank in sorted(_AUTONOMY_RESTRICTIVENESS_RANK.items(), key=lambda kv: kv[1])
        if rank >= default_rank
    ]


class AutonomyOverlayRejected(InitError):
    """An agent-autonomy.yaml overlay was rejected by resolve.py's
    narrowing/allowlist check (B-002).

    Deliberately does NOT interpolate the rejected value into its message:
    resolve.py's own OverlayError does include the raw value, because that
    message is shared/reused by other resolve.py callers with their own
    needs, but this module must never let that raw value reach stderr,
    `plan_writes`'s errors list, or any other human-readable output. Only
    the field path (never secret) and a hash of the value (recoverable from
    the audit log) are ever shown."""

    def __init__(self, field_path: str, value: Any):
        self.field_path = field_path
        self.value = value
        self.value_sha256 = hashlib.sha256(str(value).encode("utf-8")).hexdigest()
        super().__init__(
            f"autonomy overlay rejected for {field_path} "
            f"(see audit log for hash {self.value_sha256})"
        )


def _identify_offending_autonomy_field(
    base: dict[str, Any], fragment: dict[str, Any], merged: dict[str, Any]
) -> tuple[str, Any]:
    """Best-effort identification of which leaf in `fragment` (this run's own
    answers, never the whole merged/existing overlay) tripped
    `_check_autonomy_overlay`, so the caller can report a redacted rejection
    naming only the field path. Reuses `_autonomy_rank` directly (the same
    helper `autonomy_allowed_choices` already reuses elsewhere in this
    module) rather than re-implementing resolve.py's ranking rules."""
    base_values = dict(_autonomy_leaf_paths(base))
    for path, value in _autonomy_leaf_paths(fragment):
        if path not in base_values:
            return path, value
        default_value = base_values[path]
        try:
            default_rank = _autonomy_rank(path, default_value)
            overlay_rank = _autonomy_rank(path, value)
        except OverlayError:
            return path, value
        if overlay_rank < default_rank:
            return path, value
    # Couldn't pinpoint the offending leaf within this run's own fragment
    # (e.g. a value from a prior run, now merged in, is what actually
    # failed); fall back to naming the first field this run touched, or a
    # generic marker if this run's fragment was empty.
    leaves = _autonomy_leaf_paths(fragment)
    if leaves:
        return leaves[0]
    return "<agent-autonomy.yaml>", None


def _redact_autonomy_overlay_error(
    error: OverlayError, base: dict[str, Any], fragment: dict[str, Any], merged: dict[str, Any]
) -> AutonomyOverlayRejected:
    """Shared redaction wrapper (finding C): the ONE place that converts an
    `OverlayError` raised by any autonomy-overlay validation call site into a
    redacted `AutonomyOverlayRejected`.

    There are two independent call sites that can each raise `OverlayError`
    for the same autonomy overlay content: `_check_autonomy_overlay` called
    directly here in `build_autonomy_overlay`, and the second, separate
    round-trip `plan_writes` performs through `validate_overlay_content` ->
    `resolve_shared_config` -> `_check_autonomy_overlay` again (currently
    unreachable given the first call already accepted `merged`, but not
    structurally guaranteed to stay unreachable). Both call sites route
    through this one function so neither can leak a raw value via a bare
    `except Exception` fallback that forgets to redact."""
    field_path, offending_value = _identify_offending_autonomy_field(base, fragment, merged)
    return AutonomyOverlayRejected(field_path, offending_value)


def build_autonomy_overlay(
    target_root: Path, fragment: dict[str, Any]
) -> tuple[str, dict[str, Any]] | None:
    for fixed_key in _AUTONOMY_FIXED_KEYS:
        if fixed_key in fragment:
            raise InitError(
                f"agent-autonomy.yaml field {fixed_key!r} is fixed policy contract and may never "
                "be set through `cadre init` (B-003)"
            )
    existing_text = _read_existing_overlay_text(target_root, _AUTONOMY_FILENAME)
    existing: dict[str, Any] = _require_yaml().safe_load(existing_text) or {} if existing_text else {}
    if not fragment and existing_text is None:
        return None
    merged = deep_merge(existing, fragment) if fragment else existing
    base = _load_structured(SHARED_DEFAULTS_DIR / _AUTONOMY_FILENAME)
    # Reuse resolve.py's own narrowing/allowlist check directly (B-002): this
    # is the single source of truth for what ranks as a valid narrowing.
    try:
        _check_autonomy_overlay(base, merged)
    except OverlayError as error:
        raise _redact_autonomy_overlay_error(error, base, fragment, merged) from error
    return _dump_yaml(merged), merged


def validate_autonomy_overlay_content(
    content: str, fragment: dict[str, Any], merged: dict[str, Any]
) -> Any:
    """Finding C: applies the SAME redaction discipline as
    `build_autonomy_overlay`'s own `_check_autonomy_overlay` call to the
    second, independent validation `plan_writes` also performs for the
    autonomy file — `validate_overlay_content` round-trips through
    `resolve_shared_config`, which calls resolve.py's `_check_autonomy_overlay`
    a second time against the same content. If that second, independent
    invocation ever raises `OverlayError` (not currently reachable, since it
    re-validates content the first call already accepted, but not
    structurally guaranteed to stay that way), this converts it the same way
    `build_autonomy_overlay` does rather than letting a generic exception
    handler at the call site leak the raw value."""
    try:
        return validate_overlay_content(_AUTONOMY_FILENAME, content)
    except OverlayError as error:
        base = _load_structured(SHARED_DEFAULTS_DIR / _AUTONOMY_FILENAME)
        raise _redact_autonomy_overlay_error(error, base, fragment, merged) from error


# --------------------------------------------------------------------------
# RG-C platform-impact-profile.yaml — C-002 applicable-requires-citation, C-004
# template immutability (only per-key overrides on existing entries; the
# category/BOM list itself is never touched here).
# --------------------------------------------------------------------------


def validate_platform_fragment(fragment: dict[str, Any]) -> None:
    for section in ("impact_categories", "specialized_boms"):
        entries = fragment.get(section)
        if not entries:
            continue
        for key, entry in entries.items():
            if not isinstance(entry, dict):
                raise InitError(f"platform-impact-profile.yaml {section}.{key} override must be a mapping")
            applicability = entry.get("applicability")
            if applicability is not None and applicability not in PLATFORM_APPLICABILITY_VALUES:
                raise InitError(
                    f"platform-impact-profile.yaml {section}.{key}: applicability must be one of "
                    f"{PLATFORM_APPLICABILITY_VALUES}, got {applicability!r} (C-002)"
                )
            if entry.get("applicability") == "applicable":
                if not entry.get("definition_reference"):
                    raise InitError(
                        f"platform-impact-profile.yaml {section}.{key}: applicability=applicable requires "
                        "a definition_reference (C-002)"
                    )
                if not entry.get("owner"):
                    raise InitError(
                        f"platform-impact-profile.yaml {section}.{key}: applicability=applicable requires "
                        "an owner (C-002)"
                    )


def build_platform_overlay(target_root: Path, fragment: dict[str, Any]) -> tuple[str, dict[str, Any]] | None:
    validate_platform_fragment(fragment)
    existing_text = _read_existing_overlay_text(target_root, PLATFORM_FILENAME)
    existing: dict[str, Any] = _require_yaml().safe_load(existing_text) or {} if existing_text else {}
    if not fragment and existing_text is None:
        return None
    base = _load_structured(SHARED_DEFAULTS_DIR / PLATFORM_FILENAME)
    merged: dict[str, Any] = dict(existing)
    for section in ("impact_categories", "specialized_boms"):
        overrides = fragment.get(section) or {}
        if not overrides and section not in existing:
            continue
        id_key = "id" if section == "impact_categories" else "type"
        # Start from whatever this overlay already has for the section
        # (list-of-mappings, same shape as the template); fall back to a
        # bare id/type skeleton per template entry so untouched entries
        # aren't invented, only referenced.
        existing_by_id = {entry[id_key]: dict(entry) for entry in existing.get(section, [])}
        base_ids = [entry[id_key] for entry in base.get(section, [])]
        for entry_id, entry_fragment in overrides.items():
            if entry_id not in base_ids:
                raise InitError(f"platform-impact-profile.yaml {section} has no entry {entry_id!r} to override")
            current = existing_by_id.get(entry_id, {id_key: entry_id})
            existing_by_id[entry_id] = deep_merge(current, entry_fragment)
        if existing_by_id:
            # Preserve template order.
            merged[section] = [existing_by_id[i] for i in base_ids if i in existing_by_id]
    return _dump_yaml(merged), merged


# --------------------------------------------------------------------------
# Field-decision tracking (A-006 rev 2) and audit logging
# (B-006/THREAT-MODEL-HARDENING-3/4).
# --------------------------------------------------------------------------


@dataclass
class FieldDecision:
    path: str
    status: str
    category: str
    source_value: Any = None
    new_value: Any = None


def _leaf_paths(node: dict[str, Any], prefix: str = "") -> list[str]:
    paths: list[str] = []
    for key, value in node.items():
        path = f"{prefix}{key}" if not prefix else f"{prefix}.{key}"
        if isinstance(value, dict):
            paths.extend(_leaf_paths(value, path))
        else:
            paths.append(path)
    return paths


def _leaf_values(node: dict[str, Any], prefix: str = "") -> dict[str, Any]:
    """`_leaf_paths`, but keeping each leaf's value — used to show a `--set`
    override the shipped default it is replacing (its `source_value`)."""
    values: dict[str, Any] = {}
    for key, value in node.items():
        path = f"{prefix}{key}" if not prefix else f"{prefix}.{key}"
        if isinstance(value, dict):
            values.update(_leaf_values(value, path))
        else:
            values[path] = value
    return values


def _set_by_path(node: dict[str, Any], path: str, value: Any) -> None:
    parts = path.split(".")
    current = node
    for part in parts[:-1]:
        current = current.setdefault(part, {})
    current[parts[-1]] = value


def existing_team_profile_deferrals() -> dict[str, dict[str, Any]]:
    """Surface team-profile.yaml's own already-recorded deferrals
    (version_policy.components_needing_pins, out_of_scope_standards) so a
    field touching them is shown its existing rationale/owner rather than
    being silently re-deferred (A-006 rev 2)."""
    path = SHARED_DEFAULTS_DIR / TEAM_PROFILE_FILENAME
    base = _load_structured(path) if path.is_file() else {}
    deferrals: dict[str, dict[str, Any]] = {}
    for component in base.get("version_policy", {}).get("components_needing_pins", []):
        deferrals[f"version_policy.components_needing_pins[{component}]"] = {
            "rationale": "exact version pin deferred to the version manifest at adoption time",
            "owner": "Engineering Lead",
            "source": "team-profile.yaml version_policy.components_needing_pins",
        }
    for item in base.get("out_of_scope_standards", []):
        key = item.get("description", "<unlabeled out-of-scope standard>")
        deferrals[f"out_of_scope_standards[{key}]"] = {
            "rationale": item.get("decision"),
            "owner": item.get("owner"),
            "review_by": item.get("review_by"),
            "source": "team-profile.yaml out_of_scope_standards",
        }
    return deferrals


def parse_field_decisions(raw: dict[str, Any]) -> dict[str, FieldDecision]:
    decisions: dict[str, FieldDecision] = {}
    for path, entry in raw.items():
        if not isinstance(entry, dict):
            raise InitError(f"field_decisions[{path!r}] must be a mapping")
        status = entry.get("status")
        category = entry.get("category")
        if status not in FIELD_DECISION_STATUSES:
            raise InitError(
                f"field_decisions[{path!r}].status must be one of {FIELD_DECISION_STATUSES}, got {status!r}"
            )
        if category not in FIELD_DECISION_CATEGORIES:
            raise InitError(
                f"field_decisions[{path!r}].category must be one of {FIELD_DECISION_CATEGORIES}, got {category!r}"
            )
        decisions[path] = FieldDecision(
            path=path,
            status=status,
            category=category,
            source_value=entry.get("source_value"),
            new_value=entry.get("new_value"),
        )
    return decisions


def require_field_decisions_cover(
    touched_paths: list[tuple[str, str]], decisions: dict[str, FieldDecision]
) -> None:
    """A-006 rev 2: no field may reach flow output without a recorded
    kept/overridden/deferred decision. `touched_paths` is a list of
    (path, expected_category) pairs drawn from the fragments an answer set
    actually supplies values for."""
    missing = [path for path, _category in touched_paths if path not in decisions]
    if missing:
        raise InitError(
            "field_decisions is missing an entry for: " + ", ".join(sorted(missing)) + " (A-006 rev 2)"
        )
    mismatched = [
        path
        for path, expected_category in touched_paths
        if path in decisions and decisions[path].category != expected_category
    ]
    if mismatched:
        raise InitError(
            "field_decisions category mismatch (stack vs governance, B-005) for: "
            + ", ".join(sorted(mismatched))
        )


def default_audit_log_path() -> Path:
    override = os.environ.get("AGENTS_INIT_AUDIT_LOG")
    if override:
        return Path(override)
    base = Path(os.environ.get("XDG_CACHE_HOME") or (Path.home() / ".cache"))
    return base / "agents-init" / "audit.jsonl"


def _sha256_hex(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def append_audit_entries(entries: list[dict[str, Any]], log_path: Path | None = None) -> Path:
    path = log_path or default_audit_log_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as handle:
        for entry in entries:
            handle.write(json.dumps(entry, sort_keys=True) + "\n")
    return path


def audit_entry(
    *, kind: str, category: str, context: str, detail: str, value: str | None = None, hash_only: bool = False
) -> dict[str, Any]:
    entry: dict[str, Any] = {
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "kind": kind,
        "category": category,
        "context": context,
        "detail": detail,
    }
    if value is not None:
        if hash_only or kind == "rejected":
            entry["value_sha256"] = _sha256_hex(value)
        else:
            entry["value"] = value
    return entry


# --------------------------------------------------------------------------
# Answer-set loading and the RG-A/B/C orchestration.
# --------------------------------------------------------------------------


@dataclass
class PlannedWrite:
    filename: str
    section: str
    category: str
    content: str


@dataclass
class InitResult:
    planned: list[PlannedWrite] = field(default_factory=list)
    rejected_guardrails: list[tuple[str, str]] = field(default_factory=list)
    # (field_path, rejected_value) pairs (finding 2): the value is carried
    # here ONLY so run_init can log it as a hash via audit_entry(); it must
    # never be placed into `errors` or printed anywhere.
    rejected_autonomy: list[tuple[str, Any]] = field(default_factory=list)
    written: list[Path] = field(default_factory=list)
    audit_log_path: Path | None = None
    # System-derived ground truth for which dotted paths are actually
    # agent-autonomy.yaml leaves this run touched, computed in `plan_writes`
    # directly from the real fragment structure (the same walk `touched` is
    # built from for `require_field_decisions_cover`) rather than from
    # anything the answer file merely claims about itself (e.g. a
    # `field_decisions[<path>].category` label, which an answer-file author
    # fully controls and can mislabel). `_redact_answers_for_echo` unions
    # this set with the declared category (fail-safe OR) when deciding
    # whether a `field_decisions` entry's value must be redacted, so a
    # mislabeled category can never let a real governance leaf's raw value
    # reach `--print-answers` output.
    #
    # There is no equivalent ground truth for cloud-guardrails.md: guardrail
    # addenda are free-text bullets in a list, not dotted leaf paths against
    # a schema, so a `field_decisions` entry describing a guardrails
    # decision is gated by its declared `category` alone.
    governance_touched_paths: set[str] = field(default_factory=set)


def _redact_answers_for_echo(answers: dict[str, Any], result: InitResult) -> dict[str, Any]:
    """Build a copy of `answers` safe to echo via `--print-answers`. Must
    only ever be called AFTER `plan_writes` has run, so the accepted/
    rejected status this redaction reports is the real, post-validation
    outcome, not a guess made from the raw, unvalidated answer set.

    - `rg_b_autonomy`: every leaf field this run touched is replaced with its
      field path and a post-validation `accepted`/`rejected` status plus a
      sha256 hash of the raw value (recoverable from the audit log) — never
      the raw value itself, matching the audit log's own hash-only
      discipline for rejected autonomy values. If the autonomy overlay build
      failed for ANY reason, every touched field is reported `rejected` (the
      fragment was not applied as a unit; a fine-grained accepted/rejected
      split per field isn't something `plan_writes` itself tracks when the
      whole build fails).
    - `rg_b_guardrails_addendum`: bullets that passed the denylist scan are
      echoed in full, since an accepted bullet already passed the same scan
      that gates it becoming visible file content anyway; bullets
      `plan_writes` rejected are reduced to their hash.
    - `field_decisions[<path>]`: an entry has its `new_value`/`source_value`
      put through the identical accepted/rejected-hash treatment as the
      corresponding `rg_b_autonomy` leaf, keyed by the same dotted field
      path, if EITHER of two independent signals says it is
      governance-restricted (fail-safe union, never a single gate):

        1. its declared `category` field says `"governance"`, OR
        2. its dotted path appears in `result.governance_touched_paths` —
           ground truth computed in `plan_writes` straight from the real
           agent-autonomy.yaml fragment structure, independent of anything
           the answer file claims about itself.

      `category` is a free-form string the answer-file author fully
      controls (an answer file can set a real governance leaf via
      `rg_b_autonomy` while mislabeling that same path's
      `field_decisions[...].category` as `"stack"`), so it can never be the
      sole gate for a security-relevant redaction decision — signal 2
      exists to catch exactly that mislabeling, independent of whether
      `require_field_decisions_cover`'s own category-mismatch check has run
      or what it concluded. `source_value` is redacted too, even though it
      is normally just the shipped repo default, because an overlay-of-an-
      overlay run could populate it from a prior project's overlay value
      instead. An entry that matches neither signal (a genuine `category:
      stack` path that is also absent from `governance_touched_paths`, e.g.
      a team-profile.yaml/technology-standards.md/library-standards.yaml
      field) is left untouched.

      There is no equivalent ground-truth path set for cloud-guardrails.md
      (see `InitResult.governance_touched_paths`'s docstring): guardrail
      addenda are free-text bullets, not dotted leaf paths against a schema,
      so a `field_decisions` entry describing a guardrails-addendum decision
      is gated by its declared `category` alone.
    - Every other top-level key (including `rg_a_*`, `rg_c_platform`) is echoed
      verbatim: those fields are not free-text secret-shaped input and
      aren't in scope for this redaction.

    The top-level `rg_b_autonomy`/`rg_b_guardrails_addendum` KEYS themselves
    do not need the same ground-truth treatment as `field_decisions[...]
    .category`: the key name an answer file uses is what `plan_writes`
    dispatches on structurally (`answers.get("rg_b_autonomy")` is only ever
    fed to `build_autonomy_overlay`, `answers.get("rg_b_guardrails_addendum")`
    is only ever fed to `build_guardrails_overlay`), so there is no way to
    mislabel a value under the wrong top-level key and have it silently take
    a different, less-restricted code path the way a mislabeled `category`
    string can.
    """
    redacted = dict(answers)

    autonomy_fragment = answers.get("rg_b_autonomy") or {}
    rejected_paths = {path for path, _value in result.rejected_autonomy}
    fragment_failed = bool(result.rejected_autonomy)

    def _status_label(path: str, value: Any) -> str:
        value_hash = _sha256_hex(str(value))
        if path in rejected_paths or fragment_failed:
            return f"rejected (hash {value_hash})"
        return f"accepted (hash {value_hash})"

    if autonomy_fragment:
        leaves = _autonomy_leaf_paths(autonomy_fragment)
        redacted_autonomy: dict[str, str] = {}
        for path, value in leaves:
            redacted_autonomy[path] = _status_label(path, value)
        redacted["rg_b_autonomy"] = redacted_autonomy

    field_decisions_raw = answers.get("field_decisions")
    if isinstance(field_decisions_raw, dict) and field_decisions_raw:
        redacted_field_decisions: dict[str, Any] = {}
        for path, entry in field_decisions_raw.items():
            if not isinstance(entry, dict):
                redacted_field_decisions[path] = entry
                continue
            # Round-4 fix (fail-safe union): redact if EITHER the declared
            # category says governance OR ground truth says this path is a
            # real agent-autonomy.yaml leaf this run touched — never rely on
            # the self-declared category alone. See this function's
            # docstring for the full reasoning.
            declared_governance = entry.get("category") == "governance"
            ground_truth_governance = path in result.governance_touched_paths
            if not (declared_governance or ground_truth_governance):
                redacted_field_decisions[path] = entry
                continue
            entry_copy = dict(entry)
            for value_key in ("new_value", "source_value"):
                if entry_copy.get(value_key) is not None:
                    entry_copy[value_key] = _status_label(path, entry_copy[value_key])
            redacted_field_decisions[path] = entry_copy
        redacted["field_decisions"] = redacted_field_decisions

    guardrail_bullets = answers.get("rg_b_guardrails_addendum")
    if isinstance(guardrail_bullets, list) and guardrail_bullets:
        rejected_texts = {bullet for bullet, _reason in result.rejected_guardrails}
        redacted_bullets = []
        for bullet in guardrail_bullets:
            if bullet in rejected_texts:
                redacted_bullets.append(f"<rejected, hash {_sha256_hex(bullet)}>")
            else:
                redacted_bullets.append(bullet)
        redacted["rg_b_guardrails_addendum"] = redacted_bullets

    return redacted


def load_answers(path: Path) -> dict[str, Any]:
    text = path.read_text(encoding="utf-8")
    data = _require_yaml().safe_load(text)
    if not isinstance(data, dict):
        raise InitError(f"{path}: answer file root must be a mapping")
    if data.get("schema_version") != 1:
        raise InitError(f"{path}: unsupported schema_version {data.get('schema_version')!r}, expected 1")
    return data


def load_stack_preset(preset_id: str) -> dict[str, Any]:
    path = PRESETS_DIR / f"{preset_id}.yaml"
    if not path.is_file():
        available = sorted(p.stem for p in PRESETS_DIR.glob("*.yaml")) if PRESETS_DIR.is_dir() else []
        raise InitError(f"unknown --stack preset {preset_id!r}; available: {available}")
    preset = _load_structured(path)
    # THREAT-MODEL-HARDENING-5: presets are a static, reviewed RG-A-only
    # starter fragment, never a detection heuristic over target-repo content,
    # and are structurally forbidden from touching governance fields.
    forbidden = set(preset) - {"rg_a_stack", "rg_a_libraries", "rg_a_prose_addenda"}
    if forbidden:
        raise InitError(
            f"stack preset {preset_id!r} may only contain rg_a_stack/rg_a_libraries/"
            f"rg_a_prose_addenda, found: {sorted(forbidden)}"
        )
    return preset


def merge_answers_with_preset(answers: dict[str, Any], preset: dict[str, Any] | None) -> dict[str, Any]:
    if not preset:
        return answers
    merged = dict(answers)
    for key in ("rg_a_stack", "rg_a_libraries"):
        merged[key] = deep_merge(preset.get(key, {}), answers.get(key, {}) or {})
    if "rg_a_prose_addenda" in preset and "rg_a_prose_addenda" not in answers:
        merged["rg_a_prose_addenda"] = preset["rg_a_prose_addenda"]
    return merged


# --------------------------------------------------------------------------
# `--set` flag-driven overrides.
#
# The defaults-first counterpart to `--interactive`: `cadre init --target X`
# with no answer source at all is a legal, no-op run that leaves the project
# on the shipped defaults (overlays are sparse, so "no overlay" and "an
# overlay agreeing with every default" resolve identically), and `--set`
# overrides individual fields from there without an answer file.
#
# The region a path belongs to is DERIVED by looking the path up in the
# shipped default files; it is never supplied by the operator. That is what
# keeps `field_decisions[...].category` honest — a `--set` can no more
# mislabel a governance field as `stack` than the interactive collector can,
# which matters because that label feeds `--print-answers` redaction (see
# `_redact_answers_for_echo`'s round-4 note on why a claimed category is not
# trusted as a sole signal).
# --------------------------------------------------------------------------

# region -> (answers key, shipped default filename, decision category, section)
SET_REGIONS: dict[str, tuple[str, str, str, str]] = {
    "stack": ("rg_a_stack", TEAM_PROFILE_FILENAME, "stack", "rg-a-stack"),
    "libraries": ("rg_a_libraries", LIBRARY_STANDARDS_FILENAME, "stack", "rg-a-stack"),
    "autonomy": ("rg_b_autonomy", _AUTONOMY_FILENAME, "governance", "rg-b-governance"),
    "platform": ("rg_c_platform", PLATFORM_FILENAME, "stack", "rg-c-platform"),
}

# The per-entry fields an RG-C `--set` may address, mirroring what the
# interactive RG-C collector fills in for one impact category / BOM entry.
PLATFORM_ENTRY_FIELDS = ("applicability", "definition_reference", "owner", "rationale")


def _shipped_default(filename: str) -> dict[str, Any]:
    path = SHARED_DEFAULTS_DIR / filename
    return _load_structured(path) if path.is_file() else {}


def region_leaf_values(region: str) -> dict[str, Any]:
    """Every field `--set` may address in `region`, mapped to its shipped
    default value. `cadre init` only ever overrides fields that already exist
    in a shipped default (it "never invents a new config format"), so this
    doubles as the validity check for a `--set` path."""
    _answers_key, filename, _category, _section = SET_REGIONS[region]
    base = _shipped_default(filename)
    if region == "autonomy":
        # resolve.py owns what counts as an autonomy leaf (fixed keys, list
        # handling); never re-derive it here.
        return dict(_autonomy_leaf_paths(base))
    if region == "platform":
        values: dict[str, Any] = {}
        for section, id_key in (("impact_categories", "id"), ("specialized_boms", "type")):
            for entry in base.get(section, []) or []:
                entry_id = entry.get(id_key)
                if not entry_id:
                    continue
                for entry_field in PLATFORM_ENTRY_FIELDS:
                    values[f"{section}.{entry_id}.{entry_field}"] = entry.get(entry_field)
        return values
    return _leaf_values(base)


def parse_set_override(raw: str) -> tuple[str | None, str, str]:
    """Split one `--set` argument into (explicit region or None, path, raw
    value text). Accepts `PATH=VALUE` and `REGION:PATH=VALUE`."""
    if "=" not in raw:
        raise InitError(f"--set {raw!r} must be of the form [REGION:]PATH=VALUE")
    lhs, _, value_text = raw.partition("=")
    lhs = lhs.strip()
    region: str | None = None
    if ":" in lhs:
        region_text, _, lhs = (part.strip() for part in lhs.partition(":"))
        if region_text not in SET_REGIONS:
            raise InitError(
                f"--set {raw!r}: unknown region {region_text!r}; known regions: {sorted(SET_REGIONS)}"
            )
        region = region_text
    if not lhs:
        raise InitError(f"--set {raw!r} must name a field path before '='")
    return region, lhs, value_text


def resolve_set_region(path: str, explicit: str | None) -> str:
    """Derive which shipped default file owns `path`, failing closed on a
    field no shipped default defines and on a path that is ambiguous across
    regions (rather than silently picking one, which could land a governance
    field in a `stack`-categorized fragment)."""
    if explicit is not None:
        if path not in region_leaf_values(explicit):
            raise InitError(
                f"--set {explicit}:{path}: no such field in shipped {SET_REGIONS[explicit][1]}"
            )
        return explicit
    matches = [region for region in SET_REGIONS if path in region_leaf_values(region)]
    if not matches:
        raise InitError(
            f"--set {path}: no shipped default under roster/shared/ defines this field; "
            "`cadre init` only overrides fields that already exist"
        )
    if len(matches) > 1:
        raise InitError(
            f"--set {path}: ambiguous across regions {sorted(matches)}; "
            f"qualify it as <region>:{path}"
        )
    return matches[0]


def _parse_set_value(text: str) -> Any:
    """Same permissive scalar parse the interactive collector uses for a
    typed-in stack value: YAML scalar if it parses, otherwise the raw string."""
    try:
        return _require_yaml().safe_load(text)
    except Exception:  # noqa: BLE001
        return text


def _set_decision_path(region: str, path: str) -> str:
    """The key `plan_writes`' `touched` list will use for this field. RG-C is
    tracked per *entry*, not per entry field, so a `--set` on
    `impact_categories.<id>.owner` records its decision against
    `rg_c_platform.impact_categories.<id>`."""
    if region == "platform":
        section, _, remainder = path.partition(".")
        entry_id = remainder.split(".")[0]
        return f"rg_c_platform.{section}.{entry_id}"
    return path


def apply_set_overrides(
    answers: dict[str, Any], raw_overrides: list[str], sections: list[str]
) -> dict[str, Any]:
    """Fold each `--set` into `answers`, recording the `field_decisions` entry
    the answer schema requires. Applied last, so a `--set` wins over both an
    answer file and a `--stack` preset."""
    for raw in raw_overrides:
        explicit, path, value_text = parse_set_override(raw)
        region = resolve_set_region(path, explicit)
        answers_key, _filename, category, section = SET_REGIONS[region]
        if section not in sections:
            raise InitError(
                f"--set {path} targets {section}, which is excluded by --sections"
            )
        value = _parse_set_value(value_text)
        fragment = answers.setdefault(answers_key, {})
        if not isinstance(fragment, dict):
            raise InitError(f"cannot apply --set {path}: {answers_key} is not a mapping")
        _set_by_path(fragment, path, value)
        decisions = answers.setdefault("field_decisions", {})
        decisions[_set_decision_path(region, path)] = {
            "status": "overridden",
            # Derived from the region, never from operator input.
            "category": category,
            "source_value": region_leaf_values(region)[path],
            "new_value": value,
        }
    return answers


def synthesize_preset_field_decisions(answers: dict[str, Any]) -> dict[str, Any]:
    """Record an `overridden` decision for every RG-A leaf that reached the
    answer set without one.

    Only ever called on a defaults-mode run, where the sole way an unrecorded
    leaf can be present is a `--stack` preset (`load_stack_preset` structurally
    forbids a preset from touching anything but RG-A, so this can never
    fabricate a governance decision). A hand-authored `--answers` file still
    fails closed on a missing decision, which is the A-006 rev 2 contract for
    answer files."""
    decisions = answers.setdefault("field_decisions", {})
    for region in ("stack", "libraries"):
        answers_key, _filename, category, _section = SET_REGIONS[region]
        shipped = region_leaf_values(region)
        for path, value in _leaf_values(answers.get(answers_key) or {}).items():
            if path in decisions:
                continue
            decisions[path] = {
                "status": "overridden",
                "category": category,
                "source_value": shipped.get(path),
                "new_value": value,
            }
    return answers


def plan_writes(
    target_root: Path, answers: dict[str, Any], sections: list[str]
) -> tuple[InitResult, list[str]]:
    """Build every planned overlay write, validating everything first
    (A-005 fail-closed). Returns (result, errors); if errors is non-empty,
    result.planned must be treated as invalid and nothing may be written."""
    result = InitResult()
    errors: list[str] = []
    touched: list[tuple[str, str]] = []

    decisions = parse_field_decisions(answers.get("field_decisions") or {})

    if "rg-a-stack" in sections:
        stack_fragment = answers.get("rg_a_stack") or {}
        libraries_fragment = answers.get("rg_a_libraries") or {}
        touched += [(p, "stack") for p in _leaf_paths(stack_fragment)]
        touched += [(p, "stack") for p in _leaf_paths(libraries_fragment)]
        try:
            built = build_structured_overlay(target_root, TEAM_PROFILE_FILENAME, stack_fragment)
            if built is not None:
                content, _merged = built
                validate_overlay_content(TEAM_PROFILE_FILENAME, content)
                result.planned.append(PlannedWrite(TEAM_PROFILE_FILENAME, "rg-a-stack", "stack", content))
        except Exception as error:  # noqa: BLE001
            errors.append(f"{TEAM_PROFILE_FILENAME}: {error}")

        try:
            built = build_structured_overlay(target_root, LIBRARY_STANDARDS_FILENAME, libraries_fragment)
            if built is not None:
                content, _merged = built
                validate_overlay_content(LIBRARY_STANDARDS_FILENAME, content)
                result.planned.append(
                    PlannedWrite(LIBRARY_STANDARDS_FILENAME, "rg-a-stack", "stack", content)
                )
        except Exception as error:  # noqa: BLE001
            errors.append(f"{LIBRARY_STANDARDS_FILENAME}: {error}")

        addendum = (answers.get("rg_a_prose_addenda") or {}).get(TECHNOLOGY_STANDARDS_FILENAME)
        try:
            content = build_prose_addendum_overlay(target_root, TECHNOLOGY_STANDARDS_FILENAME, addendum)
            if content is not None:
                validate_overlay_content(TECHNOLOGY_STANDARDS_FILENAME, content)
                result.planned.append(
                    PlannedWrite(TECHNOLOGY_STANDARDS_FILENAME, "rg-a-stack", "stack", content)
                )
        except Exception as error:  # noqa: BLE001
            errors.append(f"{TECHNOLOGY_STANDARDS_FILENAME}: {error}")

    if "rg-b-governance" in sections:
        autonomy_fragment = answers.get("rg_b_autonomy") or {}
        autonomy_touched_paths = _autonomy_leaf_path_strings(autonomy_fragment)
        touched += [(p, "governance") for p in autonomy_touched_paths]
        # Ground truth (round-4 fix): recorded independently of
        # `field_decisions` or anything else the answer file claims about
        # itself, straight from the real fragment structure — the same
        # source `touched`/`require_field_decisions_cover` already use.
        # `_redact_answers_for_echo` reads this back through `result` to
        # decide what must be redacted, rather than trusting a
        # `field_decisions[<path>].category` label alone.
        result.governance_touched_paths.update(autonomy_touched_paths)
        try:
            built = build_autonomy_overlay(target_root, autonomy_fragment)
            if built is not None:
                content, merged = built
                # Finding C: this second validation call site gets the same
                # AutonomyOverlayRejected redaction as build_autonomy_overlay's
                # own check, so it can never leak a raw value even if it ever
                # diverges and rejects content the first check accepted.
                validate_autonomy_overlay_content(content, autonomy_fragment, merged)
                result.planned.append(PlannedWrite(_AUTONOMY_FILENAME, "rg-b-governance", "governance", content))
        except AutonomyOverlayRejected as error:
            # finding 2: the rejected value is captured here for hash-only
            # audit logging in run_init and MUST NOT be interpolated into
            # `errors` (str(error) is already redacted to the field path).
            result.rejected_autonomy.append((error.field_path, error.value))
            errors.append(f"{_AUTONOMY_FILENAME}: {error}")
        except Exception as error:  # noqa: BLE001
            errors.append(f"{_AUTONOMY_FILENAME}: {error}")

        guardrail_bullets = answers.get("rg_b_guardrails_addendum") or []
        if not isinstance(guardrail_bullets, list):
            errors.append("rg_b_guardrails_addendum must be a list of additive bullet strings (B-004)")
        else:
            try:
                content, rejected = build_guardrails_overlay(target_root, guardrail_bullets)
                result.rejected_guardrails.extend(rejected)
                if rejected:
                    for bullet, reason in rejected:
                        errors.append(f"{GUARDRAILS_FILENAME} bullet rejected: {reason}")
                elif content is not None:
                    validate_overlay_content(GUARDRAILS_FILENAME, content)
                    result.planned.append(
                        PlannedWrite(GUARDRAILS_FILENAME, "rg-b-governance", "governance", content)
                    )
            except Exception as error:  # noqa: BLE001
                errors.append(f"{GUARDRAILS_FILENAME}: {error}")

    if "rg-c-platform" in sections:
        platform_fragment = answers.get("rg_c_platform") or {}
        for section in ("impact_categories", "specialized_boms"):
            for entry_id in (platform_fragment.get(section) or {}):
                touched.append((f"rg_c_platform.{section}.{entry_id}", "stack"))
        try:
            built = build_platform_overlay(target_root, platform_fragment)
            if built is not None:
                content, _merged = built
                validate_overlay_content(PLATFORM_FILENAME, content)
                result.planned.append(PlannedWrite(PLATFORM_FILENAME, "rg-c-platform", "stack", content))
        except Exception as error:  # noqa: BLE001
            errors.append(f"{PLATFORM_FILENAME}: {error}")

    try:
        require_field_decisions_cover(touched, decisions)
    except InitError as error:
        errors.append(str(error))

    return result, errors


def _autonomy_leaf_path_strings(fragment: dict[str, Any]) -> list[str]:
    return [path for path, _value in _autonomy_leaf_paths(fragment)] if fragment else []


# --------------------------------------------------------------------------
# CLI
# --------------------------------------------------------------------------


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="cadre init",
        description="Guide a project through generating .agents/shared/ overlays",
    )
    parser.add_argument("--target", required=True, type=Path, help="Project root to write overlays into (required, no cwd default)")
    parser.add_argument("--stack", help="Named starter preset id from roster/shared/init-presets/*.yaml")
    parser.add_argument("--answers", type=Path, help="Non-interactive answer file (schema_version: 1)")
    parser.add_argument(
        "--interactive",
        action="store_true",
        help=(
            "Prompt-flow mode. Optional: with no answer source at all, init keeps every "
            "shipped default and writes nothing"
        ),
    )
    parser.add_argument(
        "--set",
        action="append",
        dest="set_values",
        metavar="[REGION:]PATH=VALUE",
        help=(
            "Override one shipped-default field without an answer file, repeatable "
            f"(regions: {', '.join(sorted(SET_REGIONS))}; the region is derived from the "
            "path unless ambiguous). Wins over --answers and --stack"
        ),
    )
    parser.add_argument(
        "--sections",
        default=",".join(ALL_SECTIONS),
        help="Comma-separated subset of rg-a-stack,rg-b-governance,rg-c-platform (default: all)",
    )
    parser.add_argument("--dry-run", action="store_true", help="Preview only (default unless --force)")
    parser.add_argument("--force", action="store_true", help="Required to actually write")
    parser.add_argument(
        "--print-answers",
        action="store_true",
        help=(
            "Echo the resolved answer set AFTER validation, with rg_b_autonomy and "
            "rg_b_guardrails_addendum redacted to accepted/rejected status and hashes "
            "for anything not accepted (finding A)"
        ),
    )
    return parser


def run_init(args: argparse.Namespace) -> int:
    if args.answers and args.interactive:
        print("cadre init: --answers and --interactive are mutually exclusive", file=sys.stderr)
        return 2
    set_overrides = list(getattr(args, "set_values", None) or [])
    if args.interactive and set_overrides:
        # A prompt loop shows each field's shipped default, so a --set value
        # would either be invisible or silently overwritten by whatever the
        # operator typed; refuse rather than pick one of those for them.
        print("cadre init: --set and --interactive are mutually exclusive", file=sys.stderr)
        return 2

    target_root = args.target
    try:
        refuse_if_self_checkout(target_root)
    except InitError as error:
        print(f"cadre init: {error}", file=sys.stderr)
        return 1
    if not target_root.is_dir():
        print(f"cadre init: --target does not exist or is not a directory: {target_root}", file=sys.stderr)
        return 1

    sections = [s.strip() for s in args.sections.split(",") if s.strip()]
    unknown_sections = [s for s in sections if s not in ALL_SECTIONS]
    if unknown_sections:
        print(f"cadre init: unknown --sections value(s): {unknown_sections}", file=sys.stderr)
        return 2

    preset = load_stack_preset(args.stack) if args.stack else None

    defaults_mode = not args.interactive and not args.answers
    if args.interactive:
        from init_project_interactive import run_interactive_flow  # local import: optional prompt UI

        answers = run_interactive_flow(
            target_root=target_root, sections=sections, preset=preset, preset_id=args.stack
        )
    elif defaults_mode:
        # No answer source: keep every shipped default. Sparse overlays make
        # this a genuine no-op rather than a run that writes out the defaults
        # again -- resolve.py falls back to the shipped file for any leaf a
        # project overlay does not mention.
        answers = {
            "schema_version": 1,
            "target_project": str(target_root),
            "stack_preset": args.stack,
            "field_decisions": {},
        }
        answers = merge_answers_with_preset(answers, preset)
    else:
        try:
            answers = load_answers(args.answers)
        except InitError as error:
            print(f"cadre init: {error}", file=sys.stderr)
            return 1
        answers = merge_answers_with_preset(answers, preset)

    try:
        if set_overrides:
            answers = apply_set_overrides(answers, set_overrides, sections)
        if defaults_mode:
            answers = synthesize_preset_field_decisions(answers)
    except InitError as error:
        print(f"cadre init: {error}", file=sys.stderr)
        return 1

    result, errors = plan_writes(target_root, answers, sections)

    if args.print_answers:
        # Finding A: echoed only AFTER plan_writes has validated everything,
        # and with rg_b_autonomy/rg_b_guardrails_addendum redacted to their
        # post-validation accepted/rejected status (never the raw value for
        # anything not accepted) — printing the raw, unvalidated answer set
        # before validation ran would bypass redaction entirely for a field
        # that was going to be rejected anyway.
        #
        # Round-4 note on ordering: this still runs BEFORE the `errors`
        # check below (`require_field_decisions_cover`'s category-mismatch
        # error, among others, is only surfaced there), which is exactly
        # what let a mislabeled `category` leak a raw value in rounds 1-3.
        # That ordering is intentionally NOT changed here as a second,
        # defense-in-depth gate: `_redact_answers_for_echo` no longer relies
        # on `field_decisions[...].category` as its sole signal (see its
        # docstring) — it additionally consults `result.
        # governance_touched_paths`, ground truth computed directly from the
        # real fragment structure in `plan_writes`, independent of whether
        # `require_field_decisions_cover` has run or what it concluded. That
        # ground-truth signal is already available by this point in
        # `run_init` regardless of the mismatch error's position in
        # `errors`, so gating this echo on the error check would be
        # redundant with a fix that already closes the underlying gap
        # structurally, not incidentally via ordering. Deferring the echo
        # would also change existing, intentional behavior (tests assert
        # `--print-answers` output is still produced on a fail-closed run,
        # e.g. to let an operator see WHY a run was rejected) for no added
        # security benefit, so it is left as-is.
        print(_dump_yaml(_redact_answers_for_echo(answers, result)))

    audit_entries = []
    for path, reason in result.rejected_guardrails:
        audit_entries.append(
            audit_entry(
                kind="rejected",
                category="governance",
                context="cloud-guardrails.md addendum bullet",
                detail=reason,
                value=path,
            )
        )
    for field_path, value in result.rejected_autonomy:
        # finding 2: only ever logged as a hash (kind="rejected" forces
        # hash-only in audit_entry()); the raw value never reaches stderr,
        # `errors`, or any other printed output.
        audit_entries.append(
            audit_entry(
                kind="rejected",
                category="governance",
                context=f"agent-autonomy.yaml field {field_path}",
                detail="autonomy overlay rejected (unrecognized value or a loosening of the default)",
                value=str(value),
            )
        )

    if errors:
        for error in errors:
            print(f"cadre init: error: {error}", file=sys.stderr)
        audit_entries.append(
            audit_entry(
                kind="rejected",
                category="stack",
                context="run",
                detail="run failed validation; no writes performed (A-005 fail-closed)",
            )
        )
        log_path = append_audit_entries(audit_entries)
        print(f"cadre init: no files written (fail-closed); audit log: {log_path}", file=sys.stderr)
        return 1

    force = args.force and not args.dry_run
    try:
        for planned in result.planned:
            existing_text = _read_existing_overlay_text(target_root, planned.filename)
            dest = _existing_overlay_path(target_root, planned.filename)
            diff = "\n".join(
                difflib.unified_diff(
                    (existing_text or "").splitlines(),
                    planned.content.splitlines(),
                    fromfile=f"{dest} (current)",
                    tofile=f"{dest} (proposed)",
                    lineterm="",
                )
            )
            print(f"--- {'would write' if not force else 'writing'}: {dest} ---")
            if diff:
                print(diff)
            else:
                print("(no change)")
            audit_entries.append(
                audit_entry(
                    kind="accepted",
                    category=planned.category,
                    context=planned.filename,
                    detail="dry-run preview" if not force else "written",
                )
            )
            if force:
                written_path = _write_overlay(target_root, planned.filename, planned.content)
                # THREAT-MODEL-HARDENING-4: re-read through resolve_shared_config
                # and confirm it matches what we intended to write.
                resolve_shared_config(planned.filename, start=target_root)
                written_text = written_path.read_text(encoding="utf-8")
                if written_text != planned.content:
                    verification_error = InitError(f"post-write verification failed for {written_path}")
                    # This run's earlier iterations may have already written
                    # real files to disk before this failure; record the
                    # failure itself so the audit trail flushed below (in
                    # `finally`) reflects it alongside those prior successes,
                    # rather than only being flushed on a clean exit.
                    audit_entries.append(
                        audit_entry(
                            kind="rejected",
                            category=planned.category,
                            context=planned.filename,
                            detail=str(verification_error),
                        )
                    )
                    raise verification_error
                audit_entries.append(
                    audit_entry(
                        kind="written",
                        category=planned.category,
                        context=planned.filename,
                        detail="post-write resolve_shared_config verification passed",
                        value=written_text,
                        hash_only=True,
                    )
                )
                result.written.append(written_path)
    finally:
        # Flush whatever this run actually did -- including a partial,
        # multi-file run that failed partway through -- rather than only
        # persisting the audit trail on a clean exit. A mid-loop failure
        # still leaves earlier files genuinely written to disk, so the audit
        # log must not silently lose the record of them (or of the failure
        # that stopped the run).
        log_path = append_audit_entries(audit_entries)
        result.audit_log_path = log_path

    if not result.planned:
        # Distinguish "nothing to do" from "something went wrong": a
        # defaults-only run legitimately plans no writes, because keeping a
        # shipped default means writing no overlay for it at all.
        print(
            f"cadre init: no overlays needed -- {target_root} keeps every shipped default "
            "(override individual fields with --set, or answer the full flow with "
            f"--interactive); audit log: {log_path}"
        )
    elif force:
        print(f"cadre init: wrote {len(result.written)} file(s); audit log: {log_path}")
    else:
        print(
            f"cadre init: dry-run only, no files written (pass --force to write); audit log: {log_path}"
        )
    return 0


def main() -> int:
    args = _parser().parse_args()
    try:
        return run_init(args)
    except InitError as error:
        print(f"cadre init: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
