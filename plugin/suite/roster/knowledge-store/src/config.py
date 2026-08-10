"""Configuration loading and validation for the knowledge store."""

from __future__ import annotations

import copy
import json
import os
import sys
from pathlib import Path
from typing import Any

# Appended (never inserted at sys.path[0]): this module is itself imported
# under the name "config", and roster/shared/src/settings.py has no
# same-named module here to shadow, but keeping this at the end of sys.path
# (rather than the front) means a caller's own same-named module always
# wins first, matching every other settings.py consumer's discipline.
_SHARED_SRC_DIR = Path(__file__).resolve().parents[2] / "shared" / "src"
if str(_SHARED_SRC_DIR) not in sys.path:
    sys.path.append(str(_SHARED_SRC_DIR))

import settings  # noqa: E402  (sys.path set above)


DEFAULTS: dict[str, Any] = {
    "database": "./data/knowledge.db",
    "embedding": {
        "provider": "hashing",
        "model": "feature-hash-v1",
        "dimensions": 384,
        "base_url": None,
        "api_key_env": "KNOWLEDGE_EMBEDDING_API_KEY",
        "batch_size": 32,
        "timeout_seconds": 30,
    },
    "chunking": {"max_characters": 2400, "overlap_characters": 240},
    "ingestion": {"default_classification": "internal", "redact_secrets": True},
    # Retention Option B (issue #184): `ingest` records a retention window per
    # message rather than leaving retention a paper obligation tracked
    # outside the store. `null` means indefinite (no window recorded).
    # `restricted` is deliberately absent from this map: `refuse_restricted_without_window`
    # makes its omission meaningful -- ingesting `restricted` content always
    # requires an explicit caller-supplied window, never a silent default.
    "retention": {
        "default_days_by_classification": {"internal": 365, "confidential": 90, "public": None},
        "refuse_restricted_without_window": True,
    },
}


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


PROJECT_LOCAL_RELATIVE_PATH = Path(".agents") / "knowledge-store" / "config.json"
MAXIMUM_WALK_DEPTH = 64

# Config-resolution tiers (KS-FR-1). Exposed so callers (cli.py) can gate
# behavior — e.g. requiring an explicit project scope — only at the shared
# global-fallback tier, without altering the resolution order itself
# (KS-FR-2).
TIER_EXPLICIT_CONFIG = "explicit-config"
TIER_PROJECT_LOCAL = "project-local"
TIER_GLOBAL_FALLBACK = "global-fallback"


def find_project_local_config(start: Path) -> Path | None:
    """Walk upward from `start` for a project-local `.agents/knowledge-store/config.json`.

    Stops at the first directory containing `.git` (the project boundary) or
    after MAXIMUM_WALK_DEPTH levels if no `.git` is found, so a config file
    above the project root is never picked up.
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

    Priority: a project-local `.agents/knowledge-store/config.json` found by
    walking up from the current working directory to the project's `.git`
    boundary, else KNOWLEDGE_STORE_HOME, else ~/.agents/knowledge-store. The
    global tier is a single store shared across every project on the machine
    by design — see roster/knowledge-store/SECURITY.md for the source-based
    partitioning this requires of callers when no project-local override
    exists. A project opts into its own private store simply by creating the
    project-local file.

    May raise `settings.SettingsError` (specifically `SettingsScopeError`)
    if a project-local `.agents/cadre.yaml`/`.json` sets `knowledge_store.home`
    -- that field is global-only, since a project-local file is untrusted,
    clonable content and this value picks where a database is read/written.
    `cli.py`'s top-level handler catches `SettingsError` alongside its other
    caught exception types so this surfaces as a clean CLI error, not a
    traceback.
    """
    project_local = find_project_local_config(Path.cwd())
    if project_local:
        return project_local
    home = settings.resolve_optional("knowledge_store.home")
    base = Path(home).expanduser() if home else Path.home() / ".agents" / "knowledge-store"
    return base / "config.json"


def load_config(
    config_path: str | None = None, *, return_tier: bool = False
) -> dict[str, Any] | tuple[dict[str, Any], str]:
    """Load config, failing closed when an explicit path does not exist.

    By default returns only the merged, resolved config dict, exactly as
    before. Pass `return_tier=True` to additionally receive which of the
    three resolution tiers (`TIER_EXPLICIT_CONFIG`, `TIER_PROJECT_LOCAL`,
    `TIER_GLOBAL_FALLBACK`) supplied the config actually loaded — this is a
    read-only exposure of a fact resolution already determines internally
    (KS-FR-1); it does not change resolution order or precedence (KS-FR-2),
    and the existing fail-closed `FileNotFoundError` for a missing explicit
    `--config` still fires before the tier is even computed (KS-FR-3).
    """
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
    for section in ("embedding", "chunking", "ingestion", "retention"):
        if not isinstance(config.get(section), dict):
            raise ValueError(f"{section} must be a JSON object")
    if not isinstance(config.get("database"), str) or not config["database"].strip():
        raise ValueError("database must be a non-empty string")
    base_directory = selected.parent
    database = Path(config["database"])
    resolved_database = (base_directory / database).resolve() if not database.is_absolute() else database.resolve()
    if implicit_project_config and (resolved_database != base_directory and base_directory not in resolved_database.parents):
        raise ValueError("project-local knowledge-store database must remain under its config directory")
    config["database"] = str(resolved_database)

    embedding = config["embedding"]
    if embedding["provider"] not in {"hashing", "openai-compatible"}:
        raise ValueError(f"Unsupported embedding provider: {embedding['provider']}")
    if implicit_project_config and embedding["provider"] != "hashing":
        raise ValueError("project-local configuration cannot enable remote embeddings")
    _positive_integer(embedding["dimensions"], "embedding.dimensions", 32)
    _positive_integer(embedding["batch_size"], "embedding.batch_size")
    timeout = embedding.get("timeout_seconds", 30)
    if isinstance(timeout, bool) or not isinstance(timeout, (int, float)) or timeout <= 0 or timeout > 300:
        raise ValueError("embedding.timeout_seconds must be greater than 0 and at most 300")
    if not isinstance(embedding.get("model"), str) or not embedding["model"].strip():
        raise ValueError("embedding.model must be a non-empty string")

    chunking = config["chunking"]
    _positive_integer(chunking["max_characters"], "chunking.max_characters")
    if isinstance(chunking["overlap_characters"], bool) or not isinstance(chunking["overlap_characters"], int) or chunking["overlap_characters"] < 0:
        raise ValueError("chunking.overlap_characters must be a non-negative integer")
    if chunking["overlap_characters"] >= chunking["max_characters"]:
        raise ValueError("chunk overlap must be smaller than max_characters")

    retention = config["retention"]
    by_classification = retention.get("default_days_by_classification")
    if not isinstance(by_classification, dict):
        raise ValueError("retention.default_days_by_classification must be a JSON object")
    if "restricted" in by_classification:
        # Fail loudly rather than silently: service.resolve_retention_until
        # never reads a "restricted" entry from this map at all -- restricted
        # always either raises (refuse_restricted_without_window, the
        # default) or falls through to indefinite, controlled solely by that
        # boolean, never by a configured day count. A "restricted" key here
        # would be dead configuration that looks load-bearing but silently
        # does nothing, which is worse than simply rejecting it up front.
        raise ValueError(
            "retention.default_days_by_classification must not set 'restricted': restricted "
            "content's retention window is controlled only by "
            "retention.refuse_restricted_without_window and an explicit --retention-days at "
            "ingest time, never by a configured default -- a 'restricted' entry here would be "
            "silently ignored, so it is rejected instead."
        )
    for classification, days in by_classification.items():
        if days is None:
            continue
        _positive_integer(days, f"retention.default_days_by_classification.{classification}")
    if not isinstance(retention.get("refuse_restricted_without_window"), bool):
        raise ValueError("retention.refuse_restricted_without_window must be a boolean")

    if return_tier:
        return config, tier
    return config
