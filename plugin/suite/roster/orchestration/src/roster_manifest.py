"""Load and validate a roster package manifest (PP-FR-2).

A roster package declares itself with a `roster.json` at its root. The selector
reads it to find the catalog, the routing configuration, the role definitions,
and the shared policy directory. **The Agentic SDLC kernel never reads this
file** — that is the whole reason it is a sibling of `provider.json` rather than
an extension of it (OD-5): `provider.json`'s key set is closed in two
independent implementations, one of them the kernel this work is constrained not
to touch.

Two decisions are load-bearing here and are cheap to reverse by accident, so
they are stated rather than left to the schema:

**Unknown `schema_version` is rejected, not ignored** (OD-11). That decision
declined a `platform_compatibility` window, accepting that a manifest written
against different selector semantics fails however its differences happen to
present. Rejecting an unrecognised version is the mitigation adopted with it,
and it is the only signal a mismatched manifest gets.

**Path containment is enforced here rather than borrowed.** The logic is ported
from the kernel's `provider_resource()` (`kernel/agentic_sdlc/__init__.py:159-169`)
rather than imported, because `test_kernel_boundary.py:76-95` forbids importing
kernel code from `roster/` and that guard must keep passing. It is deliberately
*not* `glob_containment.py`, which answers a different question — glob-language
subset containment for `exclude_paths` shadowing, not filesystem path escape.
Reaching for it here would produce a check that compiles, passes its own tests,
and contains nothing.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any

MANIFEST_FILENAME = "roster.json"
SUPPORTED_SCHEMA_VERSIONS = (1,)

_REQUIRED_PATH_FIELDS = ("catalog", "routing", "role_root", "shared_policy_root")
_REQUIRED_FIELDS = ("schema_version", "id", "version", *_REQUIRED_PATH_FIELDS)


class RosterManifestError(ValueError):
    """A roster manifest is missing, malformed, or declares an unusable path."""


@dataclass(frozen=True)
class RosterManifest:
    """A validated manifest with every declared path already resolved."""

    root: Path
    manifest_path: Path
    id: str
    version: str
    catalog: Path
    routing: Path
    role_root: Path
    shared_policy_root: Path


def default_roster_root() -> Path:
    """This checkout's own roster, derived from the platform's location.

    Never routed through the `roster.root` setting: that setting's *default* is
    this value, so computing it from the setting would be circular.
    """
    return Path(__file__).resolve().parents[2]


def _resource(root: Path, value: Any, field: str, *, directory: bool) -> Path:
    """Resolve one declared path, rejecting anything that escapes `root`.

    `root` must already be `.resolve()`d by the caller — the containment check
    compares resolved paths, so an unresolved root both under-rejects (a symlink
    inside it looks like an escape) and reads confusingly.
    """
    if not isinstance(value, str) or not value:
        raise RosterManifestError(
            f"roster manifest field {field!r} must be a non-empty relative path"
        )
    candidate = (root / value).resolve()
    if not candidate.is_relative_to(root):
        # Also catches an absolute value: `Path("/a") / "/etc/passwd"` is
        # `Path("/etc/passwd")` under pathlib's join semantics, so the escape
        # surfaces here rather than at an is_absolute() guard. Tested
        # explicitly, because that is a quirk a later refactor could remove
        # without noticing it was load-bearing.
        raise RosterManifestError(
            f"roster manifest field {field!r} escapes its manifest directory: {value!r}"
        )
    if directory and not candidate.is_dir():
        raise RosterManifestError(
            f"roster manifest field {field!r} names a directory that does not exist: {value!r}"
        )
    if not directory and not candidate.is_file():
        raise RosterManifestError(
            f"roster manifest field {field!r} names a file that does not exist: {value!r}"
        )
    return candidate


def load_roster_manifest(root: Path) -> RosterManifest:
    """Read and validate `<root>/roster.json`.

    Fails closed and by name at every step (intent §7 C4): a missing manifest, a
    malformed one, an unknown schema version, a missing required field, and a
    path that escapes or does not exist each raise with the offending item
    named.
    """
    root = Path(root).expanduser().resolve()
    manifest_path = root / MANIFEST_FILENAME
    if not manifest_path.is_file():
        raise RosterManifestError(
            f"roster package at {root} is missing {MANIFEST_FILENAME}"
        )

    try:
        raw = json.loads(manifest_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        raise RosterManifestError(
            f"{manifest_path}: {MANIFEST_FILENAME} must contain JSON ({error})"
        ) from error
    if not isinstance(raw, dict):
        raise RosterManifestError(f"{manifest_path}: root must be a JSON object")

    missing = [field for field in _REQUIRED_FIELDS if field not in raw]
    if missing:
        raise RosterManifestError(
            f"{manifest_path}: missing required field(s): {', '.join(sorted(missing))}"
        )

    # OD-11's mitigation. Rejecting rather than ignoring is what converts the
    # most likely version mismatch from silent misbehaviour into an error
    # naming the manifest -- and with no compatibility window it is the only
    # such signal a roster author gets.
    version = raw["schema_version"]
    if version not in SUPPORTED_SCHEMA_VERSIONS:
        raise RosterManifestError(
            f"{manifest_path}: unsupported schema_version {version!r}; "
            f"this selector supports {', '.join(str(v) for v in SUPPORTED_SCHEMA_VERSIONS)}"
        )

    for field in ("id", "version"):
        if not isinstance(raw[field], str) or not raw[field]:
            raise RosterManifestError(
                f"{manifest_path}: field {field!r} must be a non-empty string"
            )

    return RosterManifest(
        root=root,
        manifest_path=manifest_path,
        id=raw["id"],
        version=raw["version"],
        catalog=_resource(root, raw["catalog"], "catalog", directory=False),
        routing=_resource(root, raw["routing"], "routing", directory=False),
        role_root=_resource(root, raw["role_root"], "role_root", directory=True),
        shared_policy_root=_resource(
            root, raw["shared_policy_root"], "shared_policy_root", directory=True
        ),
    )
