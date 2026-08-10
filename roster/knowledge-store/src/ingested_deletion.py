"""Retention reporting and steward-only deletion of *ingested* content.

This is the capability issue #184 adds and #181 explicitly withheld:
`delete-staged` (`staged_store.py`) removes a row from the staging table,
never anything that has been normalised, chunked, embedded, and made
retrievable. This module is the other half -- it reaches `messages` and
`chunks`, which `staged_store.delete_record` structurally never does
(`test_scope_enforcement.py`'s `test_ac15_deletion_is_confined_to_staged_records`).

Two capabilities, one evidence discipline carried over from
`staged_record_deletions`, with one deliberate divergence:

- Evidence is written *before* the delete is attempted, so an authorization
  refusal (which raises before any write) leaves no row, and an attempt that
  is made -- whether it goes on to succeed or fail -- is never silently lost.
  This *narrower* claim, not "same transaction as the delete", is the actual
  guarantee: `staged_store.delete_record` genuinely does share one
  transaction between its evidence insert and its delete, because a staged
  record's deletion is a single-statement, effectively-unfailing `DELETE ...
  WHERE id = ?`. Ingested-content deletion is not: it spans a
  multi-statement delete plus `ingestion_runs` redaction, over content a
  steward specifically needs to trust was actually removed, so this module
  tracks *attempted* versus *completed* explicitly with `delete_status`
  rather than mirroring staged-record deletion's single-transaction shape --
  see `delete_ingested`'s docstring for the exact two-phase sequence and why.
- The evidence table (`ingested_content_deletions`) has **no foreign key** to
  anything it describes, for the same reason `staged_record_deletions` has
  none: evidence that vanished with its subject would make a deletion
  indistinguishable from data loss. It must outlive the row.
- The evidence table holds digests, never bodies: `content_digest` is a
  sha256 over the *existing* `messages.content_hash` values, in id order --
  never a re-hash of raw content, because the whole point of deleting is
  that the raw content is gone by the time anyone might want to prove what
  was deleted.
- `delete_status` (`attempted` | `completed` | `failed`) is the only field
  that says whether content was actually removed. **Only `completed` means
  the content is gone.** `attempted` or `failed` both mean the content may
  still be present -- treat them identically as "not confirmed removed"; the
  distinction between those two is diagnostic (did the attempt crash before
  the delete transaction ran at all, or did the delete transaction itself
  fail and roll back), not a difference in what a steward should trust. This
  is unrelated to `reclaim_status`, which reports only whether post-delete
  disk-residue cleanup (`VACUUM`/checkpoint) ran -- it says nothing about
  whether content was removed and must never be read as if it did.

Deliberately absent from the evidence schema, and forbidden from ever being
added: `content`, `body`, `title`/`conversation_title`, chunk text,
`embedding_json`, `source_uri`. `conversation_title` in particular is source
content, not metadata -- unlike `staged_record_deletions.title`, which is a
steward-authored record title, safe to retain. That is a deliberate
divergence from the staged-record precedent, not an oversight.

No sweep/apply command exists here. `retention_report` is read-only: it lists
what is expired, never deletes it. Nothing reachable from `ingest`, `search`,
`context`, or `stats` deletes anything -- only `delete_ingested`, invoked
through the steward-only `delete-ingested` CLI command, does.
"""

from __future__ import annotations

import hashlib
import json
import sqlite3
import uuid
from datetime import datetime, timezone
from typing import Any

SCOPES = ("source", "conversation", "message")
TRIGGERS = (
    "steward-decision",
    "source-authority-revoked",
    "classification-error",
    "source-owner-request",
    "retention-expiry",
)
# Only "completed" means content was actually removed. See module docstring
# and `delete_ingested`'s docstring for the full disambiguation rule.
DELETE_STATUSES = ("attempted", "completed", "failed")

# No foreign keys, deliberately: see module docstring and staged_store.py's
# identical reasoning for staged_record_deletions (staged_store.py:457-459).
# Evidence must survive the row(s) it describes.
SCHEMA = """
CREATE TABLE IF NOT EXISTS ingested_content_deletions (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  scope_key TEXT NOT NULL,
  source TEXT NOT NULL,
  classification TEXT,
  message_count INTEGER NOT NULL,
  chunk_count INTEGER NOT NULL,
  content_digest TEXT NOT NULL,
  message_digests_json TEXT NOT NULL,
  embedding_provider TEXT,
  embedding_model TEXT,
  trigger TEXT NOT NULL,
  reason TEXT NOT NULL,
  deleted_by TEXT NOT NULL,
  authorized_by TEXT NOT NULL,
  ingestion_runs_redacted_json TEXT NOT NULL,
  delete_status TEXT NOT NULL,
  reclaim_status TEXT NOT NULL,
  deleted_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ingested_content_deletions_source
  ON ingested_content_deletions(source);
"""


class IngestedDeletionError(RuntimeError):
    """A `delete-ingested` request was refused; nothing was written."""


def _now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def _canonical_timestamp(value: str) -> str:
    """Normalise a caller-supplied ISO-8601 timestamp to the stored format.

    `retention_until` is compared as TEXT, so a cutoff is only meaningful if
    it has the exact shape `_now()` produces. Comparing an unnormalised
    string is not a near-miss, it is silently wrong in either direction: a
    date-only `2026-08-10` sorts *below* `2026-08-10T08:00:00.000Z`, so
    everything expiring on that day would be reported as not-yet-expired,
    with no error to signal it. A retention report that under-reports
    expired content while looking correct is the worst failure available
    here, so unparseable input is refused rather than compared as-is.

    A date-only value means midnight UTC at the *start* of that day; pass a
    full timestamp to include a day's expiries. A naive value (no offset) is
    read as UTC, matching how every stored value is written.
    """
    try:
        parsed = datetime.fromisoformat(value.strip().replace("Z", "+00:00"))
    except ValueError as error:
        raise IngestedDeletionError(
            f"--as-of must be an ISO-8601 date or timestamp (got {value!r}). "
            "Examples: 2026-08-10, 2026-08-10T08:00:00Z."
        ) from error
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    canonical = parsed.astimezone(timezone.utc)
    return canonical.isoformat(timespec="milliseconds").replace("+00:00", "Z")


def install_schema(db: sqlite3.Connection) -> None:
    """Create the evidence table if absent. Additive and idempotent."""
    db.executescript(SCHEMA)


def _assert_foreign_keys_on(db: sqlite3.Connection) -> None:
    """Refuse to delete anything unless cascade integrity is guaranteed.

    Chunks cascade off `messages` via `ON DELETE CASCADE`
    (`database.py`'s SCHEMA); that guarantee only holds while
    `PRAGMA foreign_keys` is `ON` for this connection. `open_store` always
    enables it, but this function does not trust that indirectly -- an
    orphaned vector left behind by a chunk that failed to cascade is the
    worst failure mode this command could have, so the deletion path checks
    directly rather than assuming its caller configured the connection
    correctly.
    """
    row = db.execute("PRAGMA foreign_keys").fetchone()
    if not row or row[0] != 1:
        raise IngestedDeletionError(
            "PRAGMA foreign_keys is not ON for this connection: refusing to delete ingested "
            "content, because chunk cascade is not guaranteed and an orphaned vector is the "
            "worst failure mode this command could produce."
        )


def _scope_clause(scope: str, scope_key: str, source: str | None) -> tuple[str, list[Any]]:
    if scope == "source":
        clauses = ["source = ?"]
        values: list[Any] = [scope_key]
    elif scope == "conversation":
        clauses = ["conversation_id = ?"]
        values = [scope_key]
    elif scope == "message":
        # scope_key is the internal primary key (messages.id), not the
        # source-supplied message_id: source_message_id is only unique
        # together with (source, conversation_id) -- see database.py's
        # UNIQUE constraint -- so it cannot unambiguously identify one row on
        # its own, while messages.id already is a deterministic hash of all
        # three and is globally unique by construction.
        clauses = ["id = ?"]
        values = [scope_key]
    else:
        raise IngestedDeletionError(
            f"Unsupported scope {scope!r}: must be one of {SCOPES}. Chunk and run scope are "
            "deliberately not offered -- chunks cascade with their message, and messages have "
            "no run_id, so a run scope would have to be faked by joining source and time."
        )
    if source:
        clauses.append("source = ?")
        values.append(source)
    return " AND ".join(clauses), values


def _plan(db: sqlite3.Connection, scope: str, scope_key: str, source: str | None) -> dict[str, Any]:
    """Compute what a deletion would do, without writing anything.

    Shared by `dry_run=True` and the real deletion path so the numbers a
    dry-run reports are exactly the numbers the real deletion would act on --
    not a separately maintained approximation.
    """
    clause, values = _scope_clause(scope, scope_key, source)
    messages = db.execute(
        f"SELECT id, source, classification, content_hash FROM messages WHERE {clause} ORDER BY id",
        values,
    ).fetchall()
    message_ids = [row["id"] for row in messages]
    chunk_count = 0
    embedding_providers: set[str] = set()
    embedding_models: set[str] = set()
    if message_ids:
        placeholders = ",".join("?" for _ in message_ids)
        chunk_rows = db.execute(
            f"SELECT embedding_provider, embedding_model, COUNT(*) AS n FROM chunks "
            f"WHERE message_id IN ({placeholders}) GROUP BY embedding_provider, embedding_model",
            message_ids,
        ).fetchall()
        for row in chunk_rows:
            chunk_count += row["n"]
            embedding_providers.add(row["embedding_provider"])
            embedding_models.add(row["embedding_model"])

    message_digests = [{"id": row["id"], "content_hash": row["content_hash"]} for row in messages]
    # No separator between joined hashes: safe only because content_hash is
    # always a fixed-length sha256 hex digest (64 chars), so concatenation
    # cannot introduce an ambiguous boundary the way it could for
    # variable-length inputs.
    content_digest = hashlib.sha256(
        "".join(row["content_hash"] for row in messages).encode("utf-8")
    ).hexdigest()
    sources = sorted({row["source"] for row in messages})
    classifications = sorted({row["classification"] for row in messages})
    return {
        "message_ids": message_ids,
        "message_count": len(message_ids),
        "chunk_count": chunk_count,
        "content_digest": content_digest,
        "message_digests": message_digests,
        # A comma-joined sorted set, not JSON: content re-ingested under a
        # different provider/model could span more than one distinct value,
        # and the evidence schema holds a single scalar column for each.
        "embedding_provider": ",".join(sorted(embedding_providers)) or None,
        "embedding_model": ",".join(sorted(embedding_models)) or None,
        "source_summary": ",".join(sources) if sources else (source or scope_key),
        "classification_summary": ",".join(classifications) if classifications else None,
    }


def retention_report(db: sqlite3.Connection, *, as_of: str | None = None) -> dict[str, Any]:
    """List expired content: id, source, classification, retention_until, counts. Never bodies.

    Read-only. No code path here deletes anything -- pair this with
    `delete-ingested` (scoped by source/conversation/message) to act on what
    it reports.
    """
    cutoff = _canonical_timestamp(as_of) if as_of else _now()
    rows = db.execute(
        "SELECT id, source, conversation_id, classification, retention_until FROM messages "
        "WHERE retention_until IS NOT NULL AND retention_until <= ? ORDER BY retention_until, id",
        (cutoff,),
    ).fetchall()
    by_source: dict[str, dict[str, Any]] = {}
    items = []
    for row in rows:
        items.append({
            "id": row["id"],
            "source": row["source"],
            "conversation_id": row["conversation_id"],
            "classification": row["classification"],
            "retention_until": row["retention_until"],
        })
        bucket = by_source.setdefault(row["source"], {"source": row["source"], "message_count": 0})
        bucket["message_count"] += 1
    return {
        "as_of": cutoff,
        "expired_message_count": len(items),
        "by_source": sorted(by_source.values(), key=lambda entry: entry["source"]),
        "items": items,
    }


def _reclaim(db: sqlite3.Connection) -> str:
    """Best-effort residue reclaim after a commit. Never fails the deletion.

    The evidence row already committed by the time this runs, so a reclaim
    failure degrades the outcome recorded in `reclaim_status` rather than
    raising -- the deletion itself already succeeded and must not be
    reported as failed because housekeeping afterward didn't fully land.
    """
    try:
        db.execute("PRAGMA wal_checkpoint(TRUNCATE)")
    except sqlite3.Error:
        return "skipped"
    try:
        db.execute("VACUUM")
        return "vacuumed"
    except sqlite3.Error:
        return "checkpoint-only"


def delete_ingested(
    db: sqlite3.Connection,
    *,
    scope: str,
    scope_key: str,
    reason: str,
    deleted_by: str,
    authorized_by: str,
    trigger: str,
    source: str | None = None,
    dry_run: bool = False,
) -> dict[str, Any]:
    """Delete ingested content by scope, with evidence that outlives it.

    Every deletion, every scope, every classification requires `reason`,
    `deleted_by`, and `authorized_by` -- steward-only semantics per
    `roster/shared/agent-autonomy.yaml`'s
    `ingest_update_reclassify_or_delete: knowledge_store_steward_only`, with
    no proposer-may-withdraw-their-own-draft exception the way
    `staged_store.delete_record` has for a still-`proposed` staged record:
    ingested content has already been made retrievable to other agents, so
    there is no equivalent "nobody has acted on this yet" state to exempt.

    Every authority check below raises before anything is written, so a
    refusal writes no evidence at all.

    The write sequence is two transactions, not one, and `delete_status`
    exists specifically to make that safe to reason about:

    1. **Txn 1** inserts the evidence row with `delete_status='attempted'`
       and commits immediately. An attempt is now durably on the record even
       if the process dies before txn 2 ever starts.
    2. **Txn 2** performs the actual removal -- `DELETE FROM messages`
       (chunks cascade), the `ingestion_runs` redaction for source scope,
       and the `UPDATE ingested_content_deletions SET delete_status =
       'completed', ingestion_runs_redacted_json = ...` for this same row --
       all in **one atomic transaction**. This is the property that makes
       `delete_status = 'completed'` trustworthy: the marker and the actual
       removal either both happen or neither does, so `completed` can never
       diverge from "content is actually gone". A failure anywhere in txn 2
       -- including in the final evidence `UPDATE` itself -- rolls the
       *whole* transaction back, including the `DELETE`, per SQLite's normal
       transaction semantics; this module does not special-case that away.
    3. If txn 2 raises, a best-effort **separate** `UPDATE ...
       SET delete_status = 'failed'` is attempted (its own failure is
       swallowed, not raised, so it cannot mask the original error), and the
       original exception is re-raised. A row that never reaches `'failed'`
       because the process died mid-attempt stays `'attempted'` -- both
       `'attempted'` and `'failed'` mean the same thing to a reader:
       "not confirmed removed", per the module docstring's disambiguation
       rule.

    This intentionally differs from `staged_store.delete_record`'s single
    shared transaction -- see the module docstring for why a staged record's
    single-statement delete does not need this and ingested content's
    multi-statement delete does.
    """
    if scope not in SCOPES:
        raise IngestedDeletionError(f"Unsupported scope {scope!r}: must be one of {SCOPES}.")
    if not (scope_key or "").strip():
        raise IngestedDeletionError("--id is required and must not be empty.")
    if not (reason or "").strip():
        raise IngestedDeletionError(
            "a deletion requires a reason: an unexplained deletion is indistinguishable "
            "from data loss."
        )
    if not (deleted_by or "").strip():
        raise IngestedDeletionError("--deleted-by is required.")
    if not (authorized_by or "").strip():
        raise IngestedDeletionError(
            "--authorized-by is required for every deletion of ingested content, every scope, "
            "every classification: this reaches content already made retrievable to other "
            "agents, so it always requires an authorized human, unlike staged-record deletion's "
            "narrower accepted-only requirement."
        )
    if trigger not in TRIGGERS:
        raise IngestedDeletionError(f"Unsupported trigger {trigger!r}: must be one of {TRIGGERS}.")
    if scope == "source" and source and source != scope_key:
        raise IngestedDeletionError(
            f"Ambiguous scope: --scope source --id {scope_key!r} disagrees with --source "
            f"{source!r}. Pass the same value in both, or omit --source for a source-scope "
            "deletion."
        )

    _assert_foreign_keys_on(db)

    plan = _plan(db, scope, scope_key, source)
    if plan["message_count"] == 0:
        raise IngestedDeletionError(
            f"No ingested messages match scope={scope!r} id={scope_key!r}"
            + (f" source={source!r}" if source else "") + ". Nothing to delete."
        )

    evidence_id = str(uuid.uuid4())
    deleted_at = _now()
    evidence_row = {
        "id": evidence_id,
        "scope": scope,
        "scope_key": scope_key,
        "source": plan["source_summary"],
        "classification": plan["classification_summary"],
        "message_count": plan["message_count"],
        "chunk_count": plan["chunk_count"],
        "content_digest": plan["content_digest"],
        "message_digests_json": json.dumps(plan["message_digests"], separators=(",", ":"), ensure_ascii=False),
        "embedding_provider": plan["embedding_provider"],
        "embedding_model": plan["embedding_model"],
        "trigger": trigger,
        "reason": reason,
        "deleted_by": deleted_by,
        "authorized_by": authorized_by,
        "deleted_at": deleted_at,
    }

    if dry_run:
        return {
            "status": "dry-run",
            **{k: v for k, v in evidence_row.items() if k != "message_digests_json"},
            "message_digests": plan["message_digests"],
            "note": "Dry run: nothing was written. Re-run without --dry-run to perform this deletion.",
        }

    # PRAGMA secure_delete=ON before the transaction, per the design: SQLite
    # overwrites freed pages with zeros on delete rather than leaving stale
    # content behind for a later page to expose. Set outside the transaction
    # (pragmas are not transactional in the same sense DML is).
    db.execute("PRAGMA secure_delete = ON")

    # Txn 1: evidence row, delete_status='attempted', committed immediately.
    # See the docstring above for why this is a separate transaction from
    # txn 2 rather than the two being merged into one.
    with db:
        db.execute(
            "INSERT INTO ingested_content_deletions (id, scope, scope_key, source, classification, "
            "message_count, chunk_count, content_digest, message_digests_json, embedding_provider, "
            "embedding_model, trigger, reason, deleted_by, authorized_by, "
            "ingestion_runs_redacted_json, delete_status, reclaim_status, deleted_at) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                evidence_row["id"], evidence_row["scope"], evidence_row["scope_key"],
                evidence_row["source"], evidence_row["classification"], evidence_row["message_count"],
                evidence_row["chunk_count"], evidence_row["content_digest"],
                evidence_row["message_digests_json"], evidence_row["embedding_provider"],
                evidence_row["embedding_model"], evidence_row["trigger"], evidence_row["reason"],
                evidence_row["deleted_by"], evidence_row["authorized_by"],
                "[]",  # placeholder, set for real inside txn 2 below
                "attempted",
                "pending",  # placeholder, updated after reclaim runs (post txn 2)
                evidence_row["deleted_at"],
            ),
        )

    # Txn 2: the actual removal, plus the evidence row's delete_status flip
    # to 'completed', ALL in one atomic transaction -- so 'completed' can
    # never be true unless the content is actually gone. A failure anywhere
    # in this block, including in the final UPDATE, rolls the whole
    # transaction back: the DELETE is undone along with it.
    redacted_run_ids: list[str] = []
    try:
        with db:
            # chunks cascade off messages via ON DELETE CASCADE (database.py).
            placeholders = ",".join("?" for _ in plan["message_ids"])
            db.execute(f"DELETE FROM messages WHERE id IN ({placeholders})", plan["message_ids"])

            if scope == "source":
                # ingestion_runs are NEVER deleted -- only the
                # source-identifying fields are redacted in place, because a
                # run row is ingestion-provenance evidence in its own right,
                # not ingested content itself. retrieval_runs is untouched:
                # no linkage from a retrieval run to the messages it once
                # returned exists, so there is nothing there to redact
                # (forced, not chosen).
                run_ids = [
                    row["id"]
                    for row in db.execute(
                        "SELECT id FROM ingestion_runs WHERE source = ?", (plan["source_summary"],)
                    ).fetchall()
                ]
                if run_ids:
                    db.execute(
                        "UPDATE ingestion_runs SET source_uri = NULL, error = NULL WHERE source = ?",
                        (plan["source_summary"],),
                    )
                    redacted_run_ids = run_ids

            db.execute(
                "UPDATE ingested_content_deletions SET delete_status = 'completed', "
                "ingestion_runs_redacted_json = ? WHERE id = ?",
                (json.dumps(sorted(redacted_run_ids), separators=(",", ":")), evidence_id),
            )
    except Exception:
        # Best-effort, separate transaction: swallow any failure here so it
        # cannot mask the original exception below. A row that never even
        # reaches 'failed' (process death before this runs) stays
        # 'attempted' -- both mean "not confirmed removed" to a reader.
        try:
            with db:
                db.execute(
                    "UPDATE ingested_content_deletions SET delete_status = 'failed' WHERE id = ?",
                    (evidence_id,),
                )
        except sqlite3.Error:
            pass
        raise

    # reclaim runs after txn 2 commits, per the design: content removal must
    # already be durable before best-effort housekeeping that may itself
    # fail. Recorded via a third, separate transaction so a reclaim failure
    # never rolls back or hides the deletion that already completed.
    reclaim_status = _reclaim(db)
    with db:
        db.execute(
            "UPDATE ingested_content_deletions SET reclaim_status = ? WHERE id = ?",
            (reclaim_status, evidence_id),
        )

    return {
        "status": "deleted",
        "id": evidence_id,
        "scope": scope,
        "scope_key": scope_key,
        "message_count": plan["message_count"],
        "chunk_count": plan["chunk_count"],
        "content_digest": plan["content_digest"],
        "ingestion_runs_redacted": redacted_run_ids,
        "delete_status": "completed",
        "reclaim_status": reclaim_status,
        "evidence_retained": True,
    }


def deletion_evidence(db: sqlite3.Connection, *, source: str | None = None) -> list[dict[str, Any]]:
    """Every ingested-content deletion ever attempted, oldest first.

    No foreign key to anything it describes -- see module docstring. Never
    exposes `content`, `body`, `title`/`conversation_title`, chunk text,
    `embedding_json`, or `source_uri`: this returns digests and counts only.

    "Ever attempted", not "ever performed": a row can carry
    `delete_status` `attempted` or `failed`, meaning content may still be
    present. Only `delete_status == "completed"` means the content was
    actually removed -- see module docstring's disambiguation rule.

    `source` filters to one project's deletions. Digests are not content, but
    a row still carries the deleting project's identifier, a free-text
    `reason`, and asserted `deleted_by`/`authorized_by` identities -- so in a
    store shared by several projects this is cross-project metadata, and the
    caller decides whose evidence it is asking for. Rows whose `source` spans
    several projects (a deletion that matched more than one) are matched when
    the requested source is one of them, so a scoped read never hides a
    deletion that touched the caller's own content.
    """
    rows = db.execute(
        "SELECT id, scope, scope_key, source, classification, message_count, chunk_count, "
        "content_digest, message_digests_json, embedding_provider, embedding_model, trigger, "
        "reason, deleted_by, authorized_by, ingestion_runs_redacted_json, delete_status, "
        "reclaim_status, deleted_at FROM ingested_content_deletions ORDER BY deleted_at, id"
    ).fetchall()
    if source is not None:
        # `source` holds `_plan`'s comma-joined summary, so this splits on the
        # same separator rather than matching the column. An exact match would
        # miss a multi-source deletion that included this caller, and a `LIKE`
        # would treat `%`/`_` in a project identifier as wildcards.
        rows = [row for row in rows if source in str(row["source"]).split(",")]
    results = []
    for row in rows:
        entry = dict(row)
        entry["message_digests"] = json.loads(entry.pop("message_digests_json"))
        entry["ingestion_runs_redacted"] = json.loads(entry.pop("ingestion_runs_redacted_json"))
        results.append(entry)
    return results


__all__ = [
    "SCOPES",
    "TRIGGERS",
    "DELETE_STATUSES",
    "SCHEMA",
    "IngestedDeletionError",
    "install_schema",
    "retention_report",
    "delete_ingested",
    "deletion_evidence",
]
