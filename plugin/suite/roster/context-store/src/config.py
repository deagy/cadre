"""Configuration loading and validation for the context store.

Deliberately a separate module, a separate config file, and a separate
resolution root from `roster/knowledge-store/src/config.py`. The resolution
*shape* is copied on purpose -- an operator who knows one store should not have
to learn a second set of rules -- but nothing is imported across the boundary
(`roster/orchestration/test/test_context_boundary.py`).
"""

from __future__ import annotations

import copy
import json
import sys
from pathlib import Path
from typing import Any

# Appended (never inserted at sys.path[0]), matching the discipline every other
# settings.py consumer in this repository follows: a caller's own same-named
# module always wins first.
_SHARED_SRC_DIR = Path(__file__).resolve().parents[2] / "shared" / "src"
if str(_SHARED_SRC_DIR) not in sys.path:
    sys.path.append(str(_SHARED_SRC_DIR))

import settings  # noqa: E402  (sys.path set above)


SCOPES = ("agent", "dispatch", "project")
CLASSIFICATIONS = ("public", "internal", "confidential", "restricted")

DEFAULTS: dict[str, Any] = {
    "database": "./data/context.db",
    "ingestion": {"redact_secrets": True},
    # Expiry is a safety mechanism here, not a retention policy.
    #
    # This is the one place the context store deliberately inverts the
    # knowledge store's default, so the reasoning belongs next to the values.
    # `roster/knowledge-store/src/config.py` ships every retention window as
    # `null` (indefinite) on purpose: its content is steward-dispositioned, and
    # recording "no window has been decided" keeps an open Product Owner
    # decision visible instead of letting a shipped number become policy by
    # inertia.
    #
    # Nothing about that reasoning transfers to this store. Entries here are
    # agent-written with no steward in front of them, so an entry that never
    # expires is durable, unreviewed content accumulating outside the gate --
    # which is the exact failure mode the sibling design exists to prevent.
    # Indefinite would not keep a decision visible; it would quietly defeat the
    # design. `database.py` therefore declares `expires_at NOT NULL`, and these
    # day counts are a safety default rather than a retention policy. The
    # retention-policy decision recorded in `roster/shared/team-profile.yaml`
    # governs how long *curated* content is kept, which is a different
    # question, and is not pre-empted by anything here.
    "expiry": {
        "default_ttl_days_by_scope": {"agent": 1, "dispatch": 7, "project": 30},
        "maximum_ttl_days": 90,
    },
    "limits": {"max_entry_bytes": 1_048_576},
    "chunking": {"max_characters": 2400, "overlap_characters": 240},
    # `hashing` is the only provider this store accepts, and the restriction is
    # enforced twice over: `load_config` rejects any other value, and
    # `roster/shared/src/text_embedding.py` -- the only embedding module this
    # store can import -- contains no remote code to reach even if the check
    # were removed. See that module's docstring for why the boundary, not the
    # check, is the real mechanism.
    "embedding": {"provider": "hashing", "model": "feature-hash-v1", "dimensions": 384},
}

# Extending this set is not a configuration change; it is a decision about
# whether unreviewed agent working material may leave the machine (OD-5). The
# error message below names that explicitly so nobody widens it by reflex while
# chasing a failing command.
SUPPORTED_EMBEDDING_PROVIDERS = ("hashing",)


def _merge(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    result = copy.deepcopy(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(result.get(key), dict):
            result[key] = _merge(result[key], value)
        else:
            result[key] = value
    return result


def _positive_integer(value: Any, name: str, minimum: int = 1) -> None:
    if isinstance(value, bool) or not isinstance(value, int) or value < minimum:
        raise ValueError(f"{name} must be an integer of at least {minimum}")


PROJECT_LOCAL_RELATIVE_PATH = Path(".agents") / "context-store" / "config.json"
MAXIMUM_WALK_DEPTH = 64

TIER_EXPLICIT_CONFIG = "explicit-config"
TIER_PROJECT_LOCAL = "project-local"
TIER_GLOBAL_FALLBACK = "global-fallback"


def find_project_local_config(start: Path) -> Path | None:
    """Walk upward from `start` for `.agents/context-store/config.json`.

    Stops at the first directory containing `.git` (the project boundary) or
    after MAXIMUM_WALK_DEPTH levels, so a config file above the project root is
    never picked up.
    """
    current = start.resolve()
    for _ in range(MAXIMUM_WALK_DEPTH):
        candidate = current / PROJECT_LOCAL_RELATIVE_PATH
        if candidate.is_file():
            return candidate
        if (current / ".git").exists():
            return None
        parent = current.parent
        if parent == current:
            return None
        current = parent
    return None


def default_config_path() -> Path:
    """Resolve the implicit config location.

    Priority: project-local `.agents/context-store/config.json` found by
    walking to the `.git` boundary, else `context_store.home`
    (`CONTEXT_STORE_HOME`), else `~/.agents/context-store`.

    May raise `settings.SettingsError` if a project-local `.agents/cadre.yaml`
    sets `context_store.home` -- that field is global-only, because a
    project-local file is untrusted, clonable content and this value picks
    where a database is read and written.
    """
    project_local = find_project_local_config(Path.cwd())
    if project_local:
        return project_local
    home = settings.resolve_optional("context_store.home")
    base = Path(home).expanduser() if home else Path.home() / ".agents" / "context-store"
    return base / "config.json"


def load_config(
    config_path: str | None = None, *, return_tier: bool = False
) -> dict[str, Any] | tuple[dict[str, Any], str]:
    """Load config, failing closed when an explicit path does not exist."""
    implicit_project_config = False
    if config_path:
        selected = Path(config_path).resolve()
        if not selected.is_file():
            raise FileNotFoundError(f"Explicit config file does not exist: {selected}")
        tier = TIER_EXPLICIT_CONFIG
    else:
        selected = default_config_path()
        implicit_project_config = find_project_local_config(Path.cwd()) == selected
        tier = TIER_PROJECT_LOCAL if implicit_project_config else TIER_GLOBAL_FALLBACK

    supplied: dict[str, Any] = {}
    if selected.is_file():
        with selected.open("r", encoding="utf-8") as handle:
            loaded = json.load(handle)
        if not isinstance(loaded, dict):
            raise ValueError("Configuration root must be a JSON object")
        supplied = loaded

    config = _merge(DEFAULTS, supplied)
    for section in ("ingestion", "expiry", "limits", "chunking", "embedding"):
        if not isinstance(config.get(section), dict):
            raise ValueError(f"{section} must be a JSON object")
    if not isinstance(config.get("database"), str) or not config["database"].strip():
        raise ValueError("database must be a non-empty string")

    base_directory = selected.parent
    database = Path(config["database"])
    resolved_database = (base_directory / database).resolve() if not database.is_absolute() else database.resolve()
    if implicit_project_config and (resolved_database != base_directory and base_directory not in resolved_database.parents):
        raise ValueError("project-local context-store database must remain under its config directory")
    config["database"] = str(resolved_database)

    expiry = config["expiry"]
    by_scope = expiry.get("default_ttl_days_by_scope")
    if not isinstance(by_scope, dict):
        raise ValueError("expiry.default_ttl_days_by_scope must be a JSON object")
    for scope in SCOPES:
        if scope not in by_scope:
            raise ValueError(f"expiry.default_ttl_days_by_scope must set a window for scope '{scope}'")
        # `None` is rejected rather than read as indefinite. There is no
        # indefinite entry in this store (see the DEFAULTS comment and
        # `database.py`'s `expires_at NOT NULL`), so a null here would be a
        # value that looks configurable but cannot be honoured -- the same
        # class of dead configuration that `roster/knowledge-store/src/
        # config.py` refuses for a "restricted" retention key.
        _positive_integer(by_scope[scope], f"expiry.default_ttl_days_by_scope.{scope}")
    unknown_scopes = sorted(set(by_scope) - set(SCOPES))
    if unknown_scopes:
        raise ValueError(
            "expiry.default_ttl_days_by_scope has unknown scope(s): "
            f"{', '.join(unknown_scopes)}. Known scopes: {', '.join(SCOPES)}."
        )
    _positive_integer(expiry.get("maximum_ttl_days"), "expiry.maximum_ttl_days")
    for scope in SCOPES:
        if by_scope[scope] > expiry["maximum_ttl_days"]:
            raise ValueError(
                f"expiry.default_ttl_days_by_scope.{scope} ({by_scope[scope]}) exceeds "
                f"expiry.maximum_ttl_days ({expiry['maximum_ttl_days']})"
            )

    _positive_integer(config["limits"].get("max_entry_bytes"), "limits.max_entry_bytes", 1024)

    if not isinstance(config["ingestion"].get("redact_secrets"), bool):
        raise ValueError("ingestion.redact_secrets must be a boolean")

    chunking = config["chunking"]
    _positive_integer(chunking.get("max_characters"), "chunking.max_characters")
    overlap = chunking.get("overlap_characters")
    if isinstance(overlap, bool) or not isinstance(overlap, int) or overlap < 0:
        raise ValueError("chunking.overlap_characters must be a non-negative integer")
    if overlap >= chunking["max_characters"]:
        raise ValueError("chunk overlap must be smaller than max_characters")

    embedding = config["embedding"]
    if embedding.get("provider") not in SUPPORTED_EMBEDDING_PROVIDERS:
        raise ValueError(
            f"Unsupported embedding provider for the context store: {embedding.get('provider')!r}. "
            f"Only {', '.join(SUPPORTED_EMBEDDING_PROVIDERS)} is accepted. Remote providers are "
            "refused here on purpose: entries are unreviewed agent working material, and whether "
            "it may be transmitted to a third-party endpoint is an open security decision (OD-5). "
            "This is not a limitation to work around by editing configuration -- the module that "
            "would perform a remote embedding is not importable from this store at all."
        )
    _positive_integer(embedding.get("dimensions"), "embedding.dimensions", 32)
    if not isinstance(embedding.get("model"), str) or not embedding["model"].strip():
        raise ValueError("embedding.model must be a non-empty string")

    if return_tier:
        return config, tier
    return config
