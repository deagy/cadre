#!/usr/bin/env python3
"""Resolve the effective content of an roster/shared/<filename> default.

A project may extend or, for structured files, override this repository's
shared defaults by placing a same-named file at .roster/shared/<filename> in
its own tree (found by walking up from the current directory to the nearest
.git boundary, the same convention roster/knowledge-store/src/config.py uses
for its project-local config.json). See roster/shared/README.md for the
precedence order and the merge rule for each file type.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any

SHARED_DEFAULTS_DIR = Path(__file__).resolve().parents[1]
PROJECT_OVERLAY_RELATIVE_DIR = Path(".agents") / "shared"
MAXIMUM_WALK_DEPTH = 64

# Every leaf value that appears in agent-autonomy.yaml today, ranked from
# least restrictive (0) to most restrictive (10). This is a genuine policy
# judgment call, not an arbitrary implementation detail, so the rationale for
# each tier is recorded here for the next maintainer:
#
#  0  allowed
#       Unconditional permission. No precondition, no gate.
#  1  allowed_within_selected_scope
#       Unconditional within the agent's already-assigned task/scope; the
#       "condition" (staying in scope) is something the agent is expected to
#       satisfy anyway, so it is only a hair more restrictive than plain
#       "allowed".
#  2  allowed_with_explicit_read_only_credentials
#       Unconditional once a real, separately-provisioned constraint (read-
#       only credentials) is satisfied. That provisioning step is an actual
#       operational barrier beyond "stay in scope", so it ranks above
#       allowed_within_selected_scope.
#  3  on_request
#       The agent does not act on its own initiative; it acts only once a
#       human asks for that specific action in the moment. Lighter than a
#       formal authorization or approval process, but strictly more
#       restrictive than any unconditional/self-serve "allowed" variant, and
#       strictly lighter than needing a standing authorization or an
#       approval decision. Sits, as intended, between "always allowed" and
#       "needs a human decision".
#  4  explicit_task_authorization
#       Requires the dispatched task itself to carry a documented, scoped
#       authorization for this action (e.g. a disposable test environment
#       explicitly called out in the task). Heavier than a bare human
#       request because it must be established as part of task setup, not
#       just asked for in-session.
#  5  explicitly_authorized
#       Requires a standing authorization decision, made outside normal task
#       flow, before the agent may read a shared system at all. Heavier than
#       explicit_task_authorization because it is not just a per-task grant;
#       it is an access decision about a shared system that outlives any one
#       task.
#  6  explicitly_authorized_and_minimum_scope
#       explicitly_authorized plus a minimum-necessary-scope constraint on
#       top; strictly more restrictive than plain explicitly_authorized.
#  7  human_approval_except_authorized_disposable_test
#       Requires human approval in general, but carves out an exception for
#       an already-authorized disposable test environment. Because that
#       exception exists, it is less restrictive than unconditional
#       human_approval, but the default case still blocks on a human, so it
#       ranks above every value that never requires human approval.
#  8  human_approval
#       Requires a human approval decision for every instance, with no
#       exception.
#  9  knowledge_store_steward_only
#       Restricted to a single named role rather than "any human approves".
#       A narrower approver population is a stricter gate than general
#       human_approval, so it ranks above it.
# 10  never
#       Categorically forbidden. The maximum restriction.
#
# No two values in the current vocabulary were judged genuinely
# incomparable; if a future value cannot be placed in this order with
# confidence, add it as a new tier (or introduce real incomparability
# handling) rather than guessing.
_AUTONOMY_RESTRICTIVENESS_RANK: dict[str, int] = {
    "allowed": 0,
    "allowed_within_selected_scope": 1,
    "allowed_with_explicit_read_only_credentials": 2,
    "on_request": 3,
    "explicit_task_authorization": 4,
    "explicitly_authorized": 5,
    "explicitly_authorized_and_minimum_scope": 6,
    "human_approval_except_authorized_disposable_test": 7,
    "human_approval": 8,
    "knowledge_store_steward_only": 9,
    "never": 10,
}
_AUTONOMY_FILENAME = "agent-autonomy.yaml"
# The autonomy contract itself, not a per-project dial; an overlay may not
# touch these two keys at all.
_AUTONOMY_FIXED_KEYS = {"policy_version", "default_rule"}


class OverlayError(ValueError):
    """A project-local overlay is malformed or violates a merge rule."""


def find_file_at_project_root(
    relative_path: Path, start: Path | None = None, maximum_depth: int = MAXIMUM_WALK_DEPTH
) -> Path | None:
    """Walk upward from `start` for a project-local file at `relative_path`.

    Stops at the first directory containing .git (the project boundary) or
    after `maximum_depth` levels if no .git is found, so a file above the
    project root is never picked up. This is the single implementation of
    the walk-up-to-.git discovery convention shared across this repository's
    project-local override mechanisms: `find_project_overlay` (below, for
    `.roster/shared/<filename>`), `roster/knowledge-store/src/config.py`'s
    project-local `config.json` lookup, and
    `roster/orchestration/src/routing_overlay.py`'s project-local routing
    overlay lookup. Extracted here (rather than reimplemented per consumer)
    per this repository's own "don't introduce a fourth distinct find-the-
    project-root convention" rule.
    """
    current = (start or Path.cwd()).resolve()
    for _ in range(maximum_depth):
        candidate = current / relative_path
        if candidate.is_file():
            return candidate
        if (current / ".git").exists():
            return None
        parent = current.parent
        if parent == current:
            return None
        current = parent
    return None


def _resolve_existing_ancestor(path: Path) -> Path:
    """Return the nearest existing ancestor of `path` (or `path` itself, if
    it already exists), resolved. Used so filesystem-identity comparisons
    still work against a path that does not exist yet (e.g. a write target
    about to be created), by anchoring the comparison at whatever prefix of
    it is already real on disk.

    Shared by `roster/shared/src/init_project.py` (self-checkout / write
    containment) and `roster/shared/src/settings.py` (project-local config
    write containment) -- moved here so neither module duplicates it.
    """
    current = path.resolve(strict=False)
    while True:
        if current.exists():
            return current.resolve()
        parent = current.parent
        if parent == current:
            return current
        current = parent


def _is_same_or_descendant(path: Path, ancestor: Path) -> bool:
    """Filesystem-identity containment check: True if `path` IS `ancestor`,
    or is located under it.

    Uses `os.path.samestat` (device/inode identity) rather than string or
    `Path.resolve()` equality, which stays case-sensitive on POSIX `pathlib`
    regardless of whether the underlying filesystem is actually case-
    insensitive (e.g. macOS APFS/HFS+ default). Two differently-cased paths
    that are the identical on-disk directory on such a filesystem compare
    equal here even though their string forms differ, closing the bypass a
    pure string/path comparison would miss.

    `ancestor` is required to already exist. `path` may not exist yet; its
    nearest existing ancestor is used as the anchor for the walk up.

    Shared by `init_project.py` and `settings.py` -- see
    `_resolve_existing_ancestor`'s docstring.
    """
    resolved_ancestor = ancestor.resolve()
    probe = _resolve_existing_ancestor(path)
    while True:
        try:
            if os.path.samestat(os.stat(probe), os.stat(resolved_ancestor)):
                return True
        except OSError:
            return False
        parent = probe.parent
        if parent == probe:
            return False
        probe = parent


def find_project_overlay(filename: str, start: Path | None = None) -> Path | None:
    """Walk upward from `start` for a project-local .roster/shared/<filename>.

    Stops at the first directory containing .git (the project boundary) or
    after MAXIMUM_WALK_DEPTH levels if no .git is found, so an overlay above
    the project root is never picked up.
    """
    return find_file_at_project_root(PROJECT_OVERLAY_RELATIVE_DIR / filename, start)


def _require_yaml():
    try:
        import yaml
    except ImportError as error:
        raise RuntimeError(
            "PyYAML is required to resolve a YAML shared config; see "
            "roster/shared/requirements-validation.txt"
        ) from error
    return yaml


def _load_structured(path: Path) -> dict[str, Any]:
    text = path.read_text(encoding="utf-8")
    if not text.strip():
        # An emptied structured file is a deliberate "no content recorded"
        # state (e.g. a project intentionally clearing a shared default),
        # not malformed input -- treat it the same as an absent file rather
        # than raising, matching build_structured_overlay()'s own empty-file
        # handling below.
        return {}
    if path.suffix.lower() == ".json":
        loaded = json.loads(text)
    else:
        loaded = _require_yaml().safe_load(text)
    if not isinstance(loaded, dict):
        raise OverlayError(f"{path}: root must be a mapping")
    return loaded


def deep_merge(base: dict[str, Any], overlay: dict[str, Any]) -> dict[str, Any]:
    """Recursively merge `overlay` over `base`; overlay wins per key.

    Mirrors roster/knowledge-store/src/config.py's _merge: only dict values
    recurse, everything else (including lists) is replaced wholesale by the
    overlay's value.
    """
    result = dict(base)
    for key, value in overlay.items():
        if isinstance(value, dict) and isinstance(result.get(key), dict):
            result[key] = deep_merge(result[key], value)
        else:
            result[key] = value
    return result


def _autonomy_leaf_paths(node: dict[str, Any], prefix: str = "") -> list[tuple[str, str]]:
    paths: list[tuple[str, str]] = []
    for key, value in node.items():
        path = f"{prefix}{key}" if not prefix else f"{prefix}.{key}"
        if isinstance(value, dict):
            paths.extend(_autonomy_leaf_paths(value, path))
        else:
            paths.append((path, value))
    return paths


def _autonomy_rank(path: str, value: Any) -> int:
    """Return the restrictiveness rank of a leaf value, or fail closed.

    Rejects anything that is not an exact, recognized member of
    _AUTONOMY_RESTRICTIVENESS_RANK: wrong type, wrong case, and stray
    whitespace are all rejected rather than silently passed through.
    """
    if isinstance(value, str) and value in _AUTONOMY_RESTRICTIVENESS_RANK:
        return _AUTONOMY_RESTRICTIVENESS_RANK[value]
    raise OverlayError(
        f"{path}: {value!r} is not a recognized agent-autonomy.yaml permission "
        f"value; expected one of {sorted(_AUTONOMY_RESTRICTIVENESS_RANK)}"
    )


def _check_autonomy_overlay(base: dict[str, Any], overlay: dict[str, Any]) -> None:
    """Enforce that an agent-autonomy.yaml overlay only narrows autonomy.

    Raises OverlayError if the overlay touches the fixed policy_version /
    default_rule keys, references a category or key the default doesn't
    define, sets a value outside the recognized ranked vocabulary (including
    wrong type, wrong case, or extra whitespace), or moves a leaf value to a
    strictly lower (less restrictive) rank than the base default.
    """
    for fixed_key in _AUTONOMY_FIXED_KEYS:
        if fixed_key in overlay:
            raise OverlayError(
                f"agent-autonomy.yaml overlay may not set {fixed_key!r}; "
                "it is the fixed autonomy contract, not a per-project dial"
            )
    base_values = dict(_autonomy_leaf_paths(base))
    for path, overlay_value in _autonomy_leaf_paths(overlay):
        if path not in base_values:
            raise OverlayError(
                f"agent-autonomy.yaml overlay references undefined key {path!r}"
            )
        default_value = base_values[path]
        if default_value == overlay_value:
            # Still validate that an unchanged value is itself recognized;
            # a corrupted base default should not be able to smuggle a
            # bogus value through as a no-op.
            _autonomy_rank(path, default_value)
            continue
        default_rank = _autonomy_rank(path, default_value)
        overlay_rank = _autonomy_rank(path, overlay_value)
        if overlay_rank < default_rank:
            raise OverlayError(
                f"{path}: overlay may not loosen {default_value!r} "
                f"(rank {default_rank}) to {overlay_value!r} (rank {overlay_rank})"
            )


def resolve_shared_config(filename: str, start: Path | None = None) -> Any:
    """Return the effective content for roster/shared/<filename>.

    Structured files (.yaml/.yml/.json) are deep-merged with the project
    overlay winning per key; agent-autonomy.yaml additionally rejects any
    overlay that loosens a restriction. Markdown files are returned as the
    base text with the overlay appended as a project addendum — an overlay
    never replaces prose, it only adds to it.

    Returns a dict for structured files, a str for Markdown.
    """
    default_path = SHARED_DEFAULTS_DIR / filename
    if not default_path.is_file():
        raise FileNotFoundError(f"No such shared default: {default_path}")
    overlay_path = find_project_overlay(filename, start)

    suffix = default_path.suffix.lower()
    if suffix == ".md":
        base_text = default_path.read_text(encoding="utf-8")
        if overlay_path is None:
            return base_text
        addendum = overlay_path.read_text(encoding="utf-8")
        return f"{base_text}\n## Project addendum ({overlay_path})\n\n{addendum}"

    base = _load_structured(default_path)
    if overlay_path is None:
        return base
    overlay = _load_structured(overlay_path)
    if filename == _AUTONOMY_FILENAME:
        _check_autonomy_overlay(base, overlay)
    return deep_merge(base, overlay)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="cadre resolve-shared",
        description="Resolve an effective roster/shared/ config for the current project",
    )
    parser.add_argument("filename", help="Shared default filename, e.g. agent-autonomy.yaml")
    parser.add_argument("--project", type=Path, help="Directory to resolve overlays from (default: cwd)")
    return parser


def main() -> int:
    arguments = _parser().parse_args()
    try:
        resolved = resolve_shared_config(arguments.filename, start=arguments.project)
    except (FileNotFoundError, OverlayError, RuntimeError) as error:
        sys.stderr.write(f"error: {error}\n")
        return 1
    if isinstance(resolved, str):
        sys.stdout.write(resolved if resolved.endswith("\n") else resolved + "\n")
    elif arguments.filename.lower().endswith(".json"):
        sys.stdout.write(json.dumps(resolved, indent=2) + "\n")
    else:
        sys.stdout.write(_require_yaml().safe_dump(resolved, sort_keys=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
