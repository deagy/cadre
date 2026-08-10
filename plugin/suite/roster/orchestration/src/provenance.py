"""Suite-input provenance binding for dispatch plans.

Implements PB-FR-1..3/6/7/9 from
`roster/orchestration/runs/cadre-idea-7-provenance-binding-2026-07-29/requirements.md`:
a sha256 content hash of the exact `catalog.yaml`/routing-configuration
bytes a dispatch plan was built from, plus a best-effort git commit
identity, assembled into the plan's `provenance` object.

This is deliberately a distinct, sibling concern to `dispatch_fingerprint`
(computed in `build_dispatch_plan.py`), not a replacement for it:

- `dispatch_fingerprint` is a self-consistency/determinism checksum over the
  plan's own emitted output -- "does this artifact match its own claimed
  content, and would the same inputs reproduce it."
- `provenance` binds the plan to the suite-input *content* that produced it
  -- "which exact `catalog.yaml`/routing configuration, and which git
  commit, generated this artifact" -- independently verifiable by an
  auditor who recomputes `sha256sum`/`git rev-parse HEAD` against a
  historical checkout, without needing to trust the generating process.

Nothing here reads `roster/runner-capabilities.json` (idea #8): per the
requirements baseline's PB-FR-5, the manifest is not bound at all (its
build-time influence is already transitively captured by hashing
`catalog.yaml`).

The PB-FR-4 overlay fields are now populated. Gap G-2 -- the missing
integration that left them reserved-but-absent -- was closed by wiring
`routing_overlay.resolve_effective_routing` into `select_agents.py`, so
there is a real causal read path behind them: the selector genuinely
dispatched against the merged configuration. They stay absent when no
overlay was discovered. Never fabricate a value -- omit the field entirely
when there is no actual causal read path behind it.
"""

from __future__ import annotations

import hashlib
import os
import subprocess
from pathlib import Path
from typing import Any

GIT_TIMEOUT_SECONDS = 10


def hash_file(path: Path) -> str:
    """Return "sha256:<hex>" over the exact bytes at path.

    Deliberately propagates on a read failure (missing/unreadable file):
    `catalog.yaml` and the routing configuration are already mandatory
    reads for plan generation to proceed at all (PB-NFR-3), so hashing adds
    no new failure surface here -- it must not silently degrade to a
    placeholder value.
    """
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def _run_git(args: list[str], cwd: Path) -> str | None:
    """Best-effort git invocation; never raises.

    Mirrors `select_agents._run_git`'s hardening against a caller-controlled
    checkout (neutralized system config, no interactive credential
    prompts, no fsmonitor hook, no optional locks) but always degrades to
    `None` instead of raising, because git identity is supplementary
    provenance (PB-FR-3) and must never turn a missing/unavailable git
    checkout into a hard plan-generation failure (PB-NFR-3, AC-5).
    """
    env = dict(os.environ)
    env["GIT_CONFIG_NOSYSTEM"] = "1"
    env["GIT_TERMINAL_PROMPT"] = "0"
    try:
        result = subprocess.run(
            ["git", "-c", "core.fsmonitor=false", "--no-optional-locks", *args],
            cwd=cwd,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
            env=env,
            timeout=GIT_TIMEOUT_SECONDS,
        )
    except (OSError, subprocess.TimeoutExpired):
        return None
    if result.returncode != 0:
        return None
    return result.stdout


def git_identity(catalog_path: Path, routing_path: Path) -> dict[str, Any]:
    """Best-effort `{"git_commit_sha": ..., "git_dirty_paths": [...]}`.

    Invoked with `catalog_path`'s parent directory as the git working
    directory -- git itself walks upward to find the enclosing `.git`
    boundary, so this needs no separate notion of "the repository root"
    (which, notably, is not necessarily `inputs.repository_root` in the
    emitted plan: that field is the *target* project `cadre select` was run
    against via `--root`, which may differ from the suite checkout that
    `catalog.yaml`/routing configuration actually live in).

    Returns an empty dict (no fields at all -- never a placeholder) when
    that directory is not inside a resolvable git working tree, the `git`
    binary is unavailable, or the lookup itself fails for any other reason
    (PB-FR-3, AC-5). `git_dirty_paths` is scoped to exactly the two files
    that were loaded to build the plan, not the whole working tree, so an
    unrelated dirty file elsewhere in the checkout doesn't make an
    unrelated plan look suspect. `status.relativePaths=true` is pinned
    explicitly (not left to ambient repo config) so the reported paths are
    deterministic across checkouts.
    """
    cwd = catalog_path.parent
    commit_sha = _run_git(["rev-parse", "HEAD"], cwd)
    if commit_sha is None:
        return {}
    commit_sha = commit_sha.strip()
    if not commit_sha:
        return {}
    identity: dict[str, Any] = {"git_commit_sha": commit_sha}
    status = _run_git(
        ["-c", "status.relativePaths=true", "status", "--short", "--", str(catalog_path), str(routing_path)],
        cwd,
    )
    if status is not None:
        identity["git_dirty_paths"] = sorted(
            {line[3:].strip() for line in status.splitlines() if line.strip()}
        )
    return identity


def build_provenance(
    *,
    catalog_path: Path,
    routing_path: Path,
    lifecycle_contract_version: int | None,
    overlay_path: Path | None = None,
) -> dict[str, Any]:
    """Assemble the dispatch plan's `provenance` object.

    Always includes the `catalog_content_hash`/`routing_content_hash` sha256
    bindings (PB-FR-1/PB-FR-2, mandatory whenever provenance is computed at
    all -- see `hash_file`'s fail-hard contract). Adds best-effort git
    identity (PB-FR-3) when available, and the already-consumed lifecycle
    contract `version` integer (PB-FR-8) when the caller supplies one.
    Overlay fields (PB-FR-4) and the runner-capability manifest hash
    (PB-FR-5) are never added here -- see this module's docstring.
    """
    provenance: dict[str, Any] = {
        "catalog_content_hash": hash_file(catalog_path),
        "routing_content_hash": hash_file(routing_path),
    }
    if overlay_path is not None:
        # `routing_content_hash` stays the *base* file's hash: an auditor
        # needs both halves to reproduce the merge, not a hash of a merged
        # artifact that exists on no disk.
        provenance["overlay_applied"] = True
        provenance["overlay_path"] = str(overlay_path)
        provenance["overlay_content_hash"] = hash_file(overlay_path)
    provenance.update(git_identity(catalog_path, routing_path))
    if lifecycle_contract_version is not None:
        provenance["agentic_sdlc_contract_version"] = lifecycle_contract_version
    return provenance
