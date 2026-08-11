#!/usr/bin/env python3
"""Interactive prompt flow for `cadre init --interactive`.

Kept in a module of its own, with no import of any stack-detection code
(there is none in this project — THREAT-MODEL-HARDENING-5 — but the
separation is structural, not just conventional, so a future detection
heuristic cannot accidentally end up on the same call path as governance
answer collection). Governance fields (RG-B) always start blank and are only
ever offered a closed allowlist of choices; nothing here can pre-fill a
governance answer.

Produces exactly the answer-file schema documented in init_project.py /
REQ-SC-GENERIC-0001 rev 2, so an interactive session is fully reproducible
via --print-answers.
"""

from __future__ import annotations

import sys
from pathlib import Path
from typing import Any, Callable

from init_project import (
    SHARED_DEFAULTS_DIR,
    PLATFORM_APPLICABILITY_VALUES,
    PLATFORM_FILENAME,
    TECHNOLOGY_STANDARDS_FILENAME,
    _AUTONOMY_FILENAME,
    _AUTONOMY_FIXED_KEYS,
    _leaf_paths,
    _load_structured,
    _require_yaml,
    _set_by_path,
    autonomy_allowed_choices,
    existing_team_profile_deferrals,
    merge_answers_with_preset,
    scan_guardrail_bullet,
)
from resolve import _autonomy_leaf_paths  # noqa: E402


def _get_by_path(node: dict[str, Any], path: str) -> Any:
    current: Any = node
    for part in path.split("."):
        current = current[part]
    return current


def _try_get_by_path(node: dict[str, Any], path: str) -> tuple[bool, Any]:
    """Like `_get_by_path`, but returns (False, None) instead of raising when
    any segment of `path` is missing from `node` (used to probe an optional
    `--stack` preset fragment, which is only ever a partial overlay of the
    full team-profile.yaml leaf set)."""
    current: Any = node
    for part in path.split("."):
        if not isinstance(current, dict) or part not in current:
            return False, None
        current = current[part]
    return True, current


def _prompt(input_func: Callable[[str], str], message: str) -> str:
    sys.stdout.write(message)
    sys.stdout.flush()
    return input_func("")


def _group_of(path: str) -> str:
    return path.split(".")[0]


def _ask_group(input_func: Callable[[str], str], group: str, count: int, noun: str) -> bool:
    """Gate a whole group of fields behind one question.

    The shipped defaults are the answer for anything not reviewed, so
    declining a group is exactly the defaults-mode outcome for those fields --
    which is why this is safe for governance groups too: resolve.py's autonomy
    check only ever permits *narrowing*, so a field nobody looked at keeps the
    most restrictive value it shipped with and can never end up loosened."""
    answer = _prompt(
        input_func,
        f"\nReview {count} {noun} under {group!r}? [y/N] (Enter=keep shipped defaults): ",
    ).strip().lower()
    return answer in ("y", "yes")


def _prompt_stack_leaf(
    input_func: Callable[[str], str],
    path: str,
    default_value: Any,
    deferrals: dict[str, dict[str, Any]],
    preset_note: str | None = None,
) -> tuple[Any, str, Any]:
    """Returns (new_value, status, source_value) for one RG-A/RG-C stack-
    category leaf field. `default_value` is what Enter/blank will keep; when
    a `--stack` preset supplied a value for this path, the caller has already
    substituted it in as `default_value`, and `preset_note` names the preset
    so the operator can see the default came from a preset rather than the
    shipped team-profile.yaml."""
    for key, info in deferrals.items():
        if path in key:
            print(f"  (existing deferral on {key}: {info.get('rationale')}, owner={info.get('owner')})")
    if preset_note is not None:
        print(f"  (default overridden by --stack preset {preset_note!r})")
    answer = _prompt(
        input_func,
        f"{path} [default: {default_value!r}] (Enter=keep, 'defer', or new value): ",
    ).strip()
    if answer == "":
        return default_value, "kept", default_value
    if answer.lower() == "defer":
        return default_value, "deferred", default_value
    try:
        parsed = _require_yaml().safe_load(answer)
    except Exception:  # noqa: BLE001
        parsed = answer
    return parsed, "overridden", default_value


def collect_stack_answers(
    input_func: Callable[[str], str] = input,
    preset: dict[str, Any] | None = None,
    preset_id: str | None = None,
) -> tuple[dict[str, Any], dict[str, dict[str, Any]]]:
    """RG-A: team-profile.yaml + library-standards.yaml fragments plus their
    field_decisions. Never touches agent-autonomy.yaml or
    cloud-guardrails.md.

    `preset`/`preset_id` (a `--stack` preset's own `rg_a_stack`/
    `rg_a_prose_addenda` fragment and its id) are used ONLY to choose what is
    shown as each prompt's default -- a preset-supplied value is shown (and
    labeled as coming from the preset) in place of the shipped
    team-profile.yaml default, so the operator can accept it with Enter or
    type a replacement. The fragment this function returns still records
    only the leaves the operator actually typed a new value for ("overridden"
    status); the caller is responsible for deep-merging that fragment over
    the preset's own fragment so an accepted (unmodified) preset default is
    not lost just because it never became an "overridden" leaf here."""
    print("\n=== RG-A: stack & tooling opinions (team-profile.yaml / library-standards.yaml) ===")
    team_profile_path = SHARED_DEFAULTS_DIR / "team-profile.yaml"
    if team_profile_path.is_file():
        team_profile_base = _load_structured(team_profile_path)
    else:
        print("(no shared team-profile.yaml recorded -- skipping stack-opinion prompts)")
        team_profile_base = {}
    deferrals = existing_team_profile_deferrals()
    preset_stack = (preset or {}).get("rg_a_stack") or {}
    fragment: dict[str, Any] = {}
    decisions: dict[str, dict[str, Any]] = {}
    all_paths = _leaf_paths(team_profile_base)
    grouped: dict[str, list[str]] = {}
    for path in all_paths:
        grouped.setdefault(_group_of(path), []).append(path)
    print(
        f"{len(all_paths)} field(s) in {len(grouped)} group(s); "
        "decline a group to keep its shipped defaults."
    )
    for group, paths in grouped.items():
        if not _ask_group(input_func, group, len(paths), "stack field(s)"):
            # Not reviewed, so nothing is written for these -- but still
            # recorded as `kept`, so a --print-answers echo replayed through
            # --answers reproduces this run exactly. `source_value` has to
            # prefer the preset for the same reason the reviewed branch below
            # does: what is kept is whatever would have been SHOWN, and the
            # preset value is what reaches the overlay via the caller's merge.
            for path in paths:
                preset_found, preset_value = _try_get_by_path(preset_stack, path)
                default_value = preset_value if preset_found else _get_by_path(team_profile_base, path)
                decisions[path] = {
                    "status": "kept",
                    "category": "stack",
                    "source_value": default_value,
                    "new_value": None,
                }
            continue
        for path in paths:
            preset_found, preset_value = _try_get_by_path(preset_stack, path)
            if preset_found:
                default_value = preset_value
                preset_note = preset_id
            else:
                default_value = _get_by_path(team_profile_base, path)
                preset_note = None
            new_value, status, source_value = _prompt_stack_leaf(
                input_func, path, default_value, deferrals, preset_note
            )
            decisions[path] = {
                "status": status,
                "category": "stack",
                "source_value": source_value,
                "new_value": new_value if status == "overridden" else None,
            }
            if status == "overridden":
                _set_by_path(fragment, path, new_value)

    preset_prose = (preset or {}).get("rg_a_prose_addenda") or {}
    preset_addendum = preset_prose.get(TECHNOLOGY_STANDARDS_FILENAME)
    default_note = f" [default from --stack preset {preset_id!r}: {preset_addendum!r}]" if preset_addendum else ""
    addendum = _prompt(
        input_func,
        f"\nOptional technology-standards.md addendum bullet text{default_note} "
        "(blank to keep default, anything else to add a new addendum instead): ",
    ).strip()
    result: dict[str, Any] = {"rg_a_stack": fragment, "rg_a_libraries": {}}
    # Only include `rg_a_prose_addenda` at all when the operator actually
    # typed something: an explicit empty-dict entry here would make
    # `merge_answers_with_preset`'s "not in answers" check treat a preset's
    # own addendum default as already answered and discard it, even though
    # blank/Enter means "keep the shown default", not "clear it".
    if addendum:
        result["rg_a_prose_addenda"] = {TECHNOLOGY_STANDARDS_FILENAME: addendum}
    return result, decisions


def _collect_autonomy_group(
    input_func: Callable[[str], str],
    leaves: list[tuple[str, Any]],
    fragment: dict[str, Any],
    decisions: dict[str, dict[str, Any]],
) -> None:
    """Prompt one accepted group's autonomy fields, each as a closed choice."""
    for path, default_value in leaves:
        choices = autonomy_allowed_choices(default_value)
        print(f"\n{path} (current default: {default_value!r})")
        for index, choice in enumerate(choices):
            marker = " (current default)" if choice == default_value else ""
            print(f"  [{index}] {choice}{marker}")
        print("  [d] defer")
        selection = _prompt(input_func, "select: ").strip().lower()
        if selection == "d":
            decisions[path] = {
                "status": "deferred",
                "category": "governance",
                "source_value": default_value,
                "new_value": None,
            }
            continue
        try:
            chosen = choices[int(selection)]
        except (ValueError, IndexError):
            print("  invalid selection, keeping default")
            chosen = default_value
        status = "kept" if chosen == default_value else "overridden"
        decisions[path] = {
            "status": status,
            "category": "governance",
            "source_value": default_value,
            "new_value": chosen if status == "overridden" else None,
        }
        if status == "overridden":
            _set_by_path(fragment, path, chosen)


def collect_governance_answers(
    input_func: Callable[[str], str] = input,
) -> tuple[dict[str, Any], dict[str, dict[str, Any]]]:
    """RG-B: agent-autonomy.yaml (closed allowlist only, never free text) and
    cloud-guardrails.md (additive bullets only, denylist-scanned). Visibly
    distinct header/confirmation step from RG-A (B-005)."""
    print("\n=== RG-B: GOVERNANCE / AUTONOMY POSTURE (agent-autonomy.yaml) — separate confirmation ===")
    print("Every choice below is a closed list; no free text is ever accepted for governance fields.")
    autonomy_base = _load_structured(SHARED_DEFAULTS_DIR / _AUTONOMY_FILENAME)
    fragment: dict[str, Any] = {}
    decisions: dict[str, dict[str, Any]] = {}
    # B-003 fixed-policy keys are not offerable: `build_autonomy_overlay`
    # refuses them outright, and they hold values (a version number, a prose
    # default rule) that are not permission words, so `autonomy_allowed_choices`
    # raises OverlayError on them -- which `main()` does not catch. Excluding
    # them here is what stops the group gate presenting a choice that can only
    # ever crash or be rejected.
    autonomy_leaves = [
        (path, default_value)
        for path, default_value in _autonomy_leaf_paths(autonomy_base)
        if _group_of(path) not in _AUTONOMY_FIXED_KEYS
    ]
    grouped: dict[str, list[tuple[str, Any]]] = {}
    for path, default_value in autonomy_leaves:
        grouped.setdefault(_group_of(path), []).append((path, default_value))
    print(
        f"{len(autonomy_leaves)} field(s) in {len(grouped)} group(s). Every choice only ever "
        "narrows; declining a group keeps its shipped (most restrictive) default."
    )
    for group, leaves in grouped.items():
        if not _ask_group(input_func, group, len(leaves), "autonomy field(s)"):
            for path, default_value in leaves:
                decisions[path] = {
                    "status": "kept",
                    "category": "governance",
                    "source_value": default_value,
                    "new_value": None,
                }
            continue
        # Prompt this group's fields immediately, the same way RG-A does, so
        # an operator who opts into one group is not made to answer every
        # remaining gate question before seeing the fields they asked for.
        _collect_autonomy_group(input_func, leaves, fragment, decisions)

    print("\nAdditive cloud-guardrails.md bullets (one per line, blank line to finish):")
    bullets: list[str] = []
    while True:
        line = _prompt(input_func, "> ").strip()
        if not line:
            break
        reason = scan_guardrail_bullet(line)
        if reason is not None:
            print(f"  rejected: {reason}; please rephrase")
            continue
        bullets.append(line)

    return {"rg_b_autonomy": fragment, "rg_b_guardrails_addendum": bullets}, decisions


def collect_platform_answers(
    input_func: Callable[[str], str] = input,
) -> tuple[dict[str, Any], dict[str, dict[str, Any]]]:
    print("\n=== RG-C: guided platform impact-profile fill-in (platform-impact-profile.yaml) ===")
    base = _load_structured(SHARED_DEFAULTS_DIR / PLATFORM_FILENAME)
    categories: dict[str, Any] = {}
    boms: dict[str, Any] = {}
    decisions: dict[str, dict[str, Any]] = {}

    def _one(entry_id: str, id_key: str, target: dict[str, Any], field_prefix: str) -> None:
        print(f"\n{entry_id}")
        # Closed choice only (C-002/finding-3): no free-typed applicability
        # value is ever accepted, mirroring the autonomy allowlist pattern.
        print("  applicability:")
        for index, choice in enumerate(PLATFORM_APPLICABILITY_VALUES):
            marker = " (default)" if choice == "unknown" else ""
            print(f"    [{index}] {choice}{marker}")
        print("    [d] defer")
        selection = _prompt(input_func, "  select (Enter=unknown): ").strip().lower()
        path = f"{field_prefix}.{entry_id}"
        if selection == "d":
            decisions[path] = {
                "status": "deferred",
                "category": "stack",
                "source_value": "unknown",
                "new_value": None,
            }
            return
        if selection == "":
            applicability = "unknown"
        else:
            try:
                applicability = PLATFORM_APPLICABILITY_VALUES[int(selection)]
            except (ValueError, IndexError):
                print("  invalid selection, defaulting to unknown")
                applicability = "unknown"
        if applicability == "unknown":
            decisions[path] = {
                "status": "kept",
                "category": "stack",
                "source_value": "unknown",
                "new_value": None,
            }
            return
        entry: dict[str, Any] = {"applicability": applicability}
        if applicability == "applicable":
            entry["definition_reference"] = _prompt(input_func, "  definition_reference (required): ").strip()
            entry["owner"] = _prompt(input_func, "  owner (required): ").strip()
        entry["rationale"] = _prompt(input_func, "  rationale (optional): ").strip() or None
        target[entry_id] = entry
        decisions[path] = {
            "status": "overridden",
            "category": "stack",
            "source_value": "unknown",
            "new_value": applicability,
        }

    for section, id_key, target in (
        ("impact_categories", "id", categories),
        ("specialized_boms", "type", boms),
    ):
        items = base.get(section, []) or []
        if not items:
            continue
        if not _ask_group(input_func, section, len(items), "entr(ies)"):
            # Left at the template's own `unknown`, exactly as a defaults-mode
            # run would leave them. `unknown` is a blocking state by design in
            # whatever lifecycle enforces it, so this defers rather than
            # silently resolves.
            for item in items:
                decisions[f"rg_c_platform.{section}.{item[id_key]}"] = {
                    "status": "deferred",
                    "category": "stack",
                    "source_value": "unknown",
                    "new_value": None,
                }
            continue
        for item in items:
            _one(item[id_key], id_key, target, f"rg_c_platform.{section}")

    # Omit empty sections rather than sending `{"impact_categories": {},
    # "specialized_boms": {}}` downstream: that dict is truthy, so it defeats
    # `build_platform_overlay`'s "nothing to write" short-circuit and plans a
    # write of a literal `{}` overlay. Declining every group has to mean the
    # same thing here as everywhere else -- no file.
    fragment: dict[str, Any] = {}
    if categories:
        fragment["impact_categories"] = categories
    if boms:
        fragment["specialized_boms"] = boms
    return {"rg_c_platform": fragment}, decisions


def run_interactive_flow(
    target_root: Path,
    sections: list[str],
    preset: dict[str, Any] | None,
    input_func: Callable[[str], str] = input,
    preset_id: str | None = None,
) -> dict[str, Any]:
    answers: dict[str, Any] = {
        "schema_version": 1,
        "target_project": str(target_root),
        "stack_preset": preset_id if preset else None,
        "field_decisions": {},
    }
    if preset:
        for key in ("rg_a_stack", "rg_a_libraries", "rg_a_prose_addenda"):
            if key in preset:
                answers[key] = preset[key]

    if "rg-a-stack" in sections:
        stack_answers, stack_decisions = collect_stack_answers(input_func, preset=preset, preset_id=preset_id)
        if preset:
            # Preset values are prompt defaults, not a foregone conclusion:
            # merge the operator's actual overridden leaves OVER the
            # preset's own seeded fragment (human input wins), rather than
            # letting `answers.update` below blindly replace the
            # preset-seeded rg_a_stack/rg_a_libraries/rg_a_prose_addenda
            # with a fragment that only contains this run's overrides.
            stack_answers = merge_answers_with_preset(stack_answers, preset)
        answers.update(stack_answers)
        answers["field_decisions"].update(stack_decisions)
    if "rg-b-governance" in sections:
        governance_answers, governance_decisions = collect_governance_answers(input_func)
        answers.update(governance_answers)
        answers["field_decisions"].update(governance_decisions)
    if "rg-c-platform" in sections:
        platform_answers, platform_decisions = collect_platform_answers(input_func)
        answers.update(platform_answers)
        answers["field_decisions"].update(platform_decisions)

    return answers
