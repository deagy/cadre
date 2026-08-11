"""SQLite persistence for the context store.

A separate database file from the knowledge store, not a second set of tables
inside `knowledge.db`. That separation is doing more work than it looks like:
with two physically distinct files a cross-store `JOIN` is not merely
disallowed by policy, it cannot be written -- which is what turns "no path
exists from working context into the curated corpus without a steward
disposition" into a property of the deployment rather than a claim in a
document.

It also keeps high-churn context rows out of the blast radius of the knowledge
store's `delete_ingested()`, which scopes deletions by source/conversation/
message across its `messages` table, and lets the two stores hold opposite
durability postures: the knowledge store documents deletion as irreversible
with a backup as the only recovery, while this store is designed to lose things
on a timer.

Phase 1 has no `entry_chunks` table. Semantic retrieval arrives in phase 2, and
shipping the table early would leave schema that looks load-bearing while
nothing writes to it -- the same class of dead configuration the knowledge
store's config validator refuses outright for a "restricted" retention key.
"""

from __future__ import annotations

import hashlib
import json
import sqlite3
import uuid
from contextlib import nullcontext
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


def content_hash(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


SCHEMA = """
CREATE TABLE IF NOT EXISTS entries (
  handle TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  source TEXT NOT NULL,
  task_id TEXT NOT NULL,
  agent TEXT NOT NULL,
  dispatch_id TEXT,
  label TEXT NOT NULL,
  tags_json TEXT NOT NULL,
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  byte_length INTEGER NOT NULL,
  classification TEXT NOT NULL,
  injection_risk INTEGER NOT NULL DEFAULT 0,
  untrusted_inputs INTEGER NOT NULL DEFAULT 0,
  derived_from_json TEXT NOT NULL,
  redactions_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS entry_chunks (
  id TEXT PRIMARY KEY,
  handle TEXT NOT NULL REFERENCES entries(handle) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL, content TEXT NOT NULL, content_hash TEXT NOT NULL,
  embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
  embedding_dimensions INTEGER NOT NULL, embedding_json TEXT NOT NULL,
  UNIQUE(handle, ordinal, embedding_provider, embedding_model)
);
CREATE TABLE IF NOT EXISTS access_runs (
  id TEXT PRIMARY KEY, operation TEXT NOT NULL, handle TEXT, query_hash TEXT,
  task_id TEXT NOT NULL, agent TEXT NOT NULL, classification TEXT NOT NULL,
  scope_filter TEXT, source TEXT NOT NULL, result_count INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS expiry_evidence (
  id TEXT PRIMARY KEY, handle TEXT NOT NULL, content_hash TEXT NOT NULL,
  byte_length INTEGER NOT NULL, classification TEXT NOT NULL, source TEXT NOT NULL,
  created_at TEXT NOT NULL, expires_at TEXT NOT NULL, swept_at TEXT NOT NULL,
  reason TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_entries_source ON entries(source);
CREATE INDEX IF NOT EXISTS idx_entries_agent_task ON entries(agent, task_id);
CREATE INDEX IF NOT EXISTS idx_entries_dispatch ON entries(dispatch_id);
CREATE INDEX IF NOT EXISTS idx_entries_expires ON entries(expires_at);
CREATE INDEX IF NOT EXISTS idx_access_runs_task ON access_runs(task_id, agent);
CREATE INDEX IF NOT EXISTS idx_expiry_evidence_handle ON expiry_evidence(handle);
CREATE INDEX IF NOT EXISTS idx_entry_chunks_handle ON entry_chunks(handle);
CREATE INDEX IF NOT EXISTS idx_entry_chunks_model ON entry_chunks(embedding_provider, embedding_model);
"""


def _migrate_additive_columns(db: sqlite3.Connection) -> None:
    """Add columns introduced after a store's initial creation, additively.

    `CREATE TABLE IF NOT EXISTS` only covers a store created fresh with the
    current schema, and SQLite's `ALTER TABLE ... ADD COLUMN` has no
    `IF NOT EXISTS` form. `promoted_at` is nullable with no default, so this is
    purely additive: rows written before promotion existed read back as `NULL`
    ("never proposed"), which is exactly what was true of them. Idempotent and
    cheap enough to run on every open, matching the sibling store's convention.

    Deliberately *not* `promoted_record_id`. Recording which staged record an
    entry became would mean computing that record's id here, and the id is
    derived by knowledge-store code this store may not import
    (`roster/orchestration/test/test_context_boundary.py`). A timestamp is what
    can be known on this side of the boundary without reaching across it.
    """
    existing = {row["name"] for row in db.execute("PRAGMA table_info(entries)")}
    if "promoted_at" not in existing:
        db.execute("ALTER TABLE entries ADD COLUMN promoted_at TEXT")


def open_store(database_path: str, *, sweep: bool = True) -> sqlite3.Connection:
    """Open (creating if absent) and, by default, sweep expired entries.

    The sweep runs on open rather than behind a scheduler or a steward command.
    That is the sharpest operational asymmetry with the knowledge store, where
    deletion is steward-only and demands a reason, an authorized human, and
    retained evidence -- so it needs its justification stated rather than
    assumed:

    knowledge-store deletion destroys curated, dispositioned content, and the
    steward is accountable for it. Context expiry destroys working scratch
    whose entire contract is that it expires. Routing expiry through a steward
    would rebuild the very bottleneck this store exists to remove, and would
    make the steward accountable for content they never dispositioned.

    `sweep=False` exists for `expire --dry-run`, which needs to read the
    expired set without destroying it.
    """
    path = Path(database_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    db = sqlite3.connect(path)
    db.row_factory = sqlite3.Row
    db.execute("PRAGMA foreign_keys = ON")
    db.execute("PRAGMA journal_mode = WAL")
    db.executescript(SCHEMA)
    _migrate_additive_columns(db)
    if sweep:
        sweep_expired(db)
    return db


def expired_rows(db: sqlite3.Connection, as_of: str | None = None) -> list[sqlite3.Row]:
    moment = as_of or now_iso()
    return db.execute(
        "SELECT * FROM entries WHERE expires_at <= ? ORDER BY expires_at, handle", (moment,)
    ).fetchall()


def sweep_expired(db: sqlite3.Connection, as_of: str | None = None, reason: str = "ttl-expiry") -> list[dict[str, Any]]:
    """Delete expired entries, recording evidence that they existed.

    The evidence row carries the handle, hash, size, classification, and
    lifespan -- never the content. It records that an entry existed and what it
    hashed to, so a handle cited in a handoff can still be accounted for after
    the content is gone. It does not make expiry reversible.
    """
    moment = as_of or now_iso()
    rows = expired_rows(db, moment)
    if not rows:
        return []
    swept: list[dict[str, Any]] = []
    with db:
        for row in rows:
            db.execute(
                """INSERT INTO expiry_evidence (id, handle, content_hash, byte_length,
                     classification, source, created_at, expires_at, swept_at, reason)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    str(uuid.uuid4()), row["handle"], row["content_hash"], row["byte_length"],
                    row["classification"], row["source"], row["created_at"], row["expires_at"],
                    moment, reason,
                ),
            )
            db.execute("DELETE FROM entries WHERE handle = ?", (row["handle"],))
            swept.append({
                "handle": row["handle"],
                "content_hash": row["content_hash"],
                "byte_length": row["byte_length"],
                "classification": row["classification"],
                "expires_at": row["expires_at"],
                "reason": reason,
            })
    return swept


def insert_entry(db: sqlite3.Connection, entry: dict[str, Any]) -> None:
    # Joins an open transaction rather than committing its own, so a caller
    # can make the entry row and its chunks atomic. Without this, an
    # interrupted `put` commits a visible entry that `search` cannot see until
    # someone thinks to run `reindex`. Same pattern as the sibling store's
    # `save_message`.
    with (nullcontext() if db.in_transaction else db):
        db.execute(
            """INSERT INTO entries (handle, scope, source, task_id, agent, dispatch_id, label,
                 tags_json, content, content_hash, byte_length, classification, injection_risk,
                 untrusted_inputs, derived_from_json, redactions_json, created_at, expires_at)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                entry["handle"], entry["scope"], entry["source"], entry["task_id"], entry["agent"],
                entry["dispatch_id"], entry["label"],
                json.dumps(entry["tags"], separators=(",", ":"), ensure_ascii=False),
                entry["content"], entry["content_hash"], entry["byte_length"],
                entry["classification"], 1 if entry["injection_risk"] else 0,
                1 if entry["untrusted_inputs"] else 0,
                json.dumps(entry["derived_from"], separators=(",", ":"), ensure_ascii=False),
                json.dumps(entry["redactions"], separators=(",", ":"), ensure_ascii=False),
                entry["created_at"], entry["expires_at"],
            ),
        )


def replace_chunks(
    db: sqlite3.Connection,
    handle: str,
    chunks: list[str],
    vectors: list[list[float]],
    embedding: dict[str, Any],
) -> int:
    """Write an entry's chunks, replacing any it already had.

    Replace rather than append so re-indexing under a changed chunking or
    embedding configuration cannot leave a mix of old and new vectors behind,
    scored against each other as if comparable.

    Joins an open transaction rather than committing its own, for the same
    reason `insert_entry` does: `put_entry` wraps both so an entry and its
    chunks land together or not at all.
    """
    with (nullcontext() if db.in_transaction else db):
        db.execute("DELETE FROM entry_chunks WHERE handle = ?", (handle,))
        for ordinal, chunk in enumerate(chunks):
            vector = vectors[ordinal]
            db.execute(
                """INSERT INTO entry_chunks (id, handle, ordinal, content, content_hash,
                     embedding_provider, embedding_model, embedding_dimensions, embedding_json)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    content_hash(f"{handle}|{ordinal}|{embedding['provider']}|{embedding['model']}"),
                    handle, ordinal, chunk, content_hash(chunk), embedding["provider"],
                    embedding["model"], len(vector),
                    json.dumps(vector, separators=(",", ":")),
                ),
            )
    return len(chunks)


def load_searchable_chunks(
    db: sqlite3.Connection, embedding: dict[str, Any], filters: dict[str, Any]
) -> list[sqlite3.Row]:
    """Join chunks to their entries, filtered before any scoring happens.

    Rows whose stored embedding dimension does not match the configured one are
    excluded here rather than scored -- the same rule the knowledge store
    applies, for the same reason: a mismatched vector produces a meaningless
    similarity rather than a low one.
    """
    clauses = [
        "c.embedding_provider = ?", "c.embedding_model = ?", "c.embedding_dimensions = ?",
        "e.classification = ?", "e.source = ?",
    ]
    values: list[Any] = [
        embedding["provider"], embedding["model"], embedding["dimensions"],
        filters["classification"], filters["source"],
    ]
    if filters.get("scope"):
        clauses.append("e.scope = ?")
        values.append(filters["scope"])
    return db.execute(
        f"""SELECT c.id AS chunk_id, c.ordinal, c.content AS chunk_content, c.content_hash AS chunk_hash,
              c.embedding_json, e.*
            FROM entry_chunks c JOIN entries e ON e.handle = c.handle
            WHERE {' AND '.join(clauses)}""",
        values,
    ).fetchall()


def entries_missing_chunks(db: sqlite3.Connection, embedding: dict[str, Any]) -> list[sqlite3.Row]:
    """Entries with no chunks under the configured provider/model/dimensions."""
    return db.execute(
        """SELECT e.* FROM entries e
           WHERE NOT EXISTS (
             SELECT 1 FROM entry_chunks c
             WHERE c.handle = e.handle AND c.embedding_provider = ?
               AND c.embedding_model = ? AND c.embedding_dimensions = ?
           )
           ORDER BY e.created_at, e.handle""",
        (embedding["provider"], embedding["model"], embedding["dimensions"]),
    ).fetchall()


def fetch_entry(db: sqlite3.Connection, handle: str) -> sqlite3.Row | None:
    return db.execute("SELECT * FROM entries WHERE handle = ?", (handle,)).fetchone()


def fetch_entries(db: sqlite3.Connection, filters: dict[str, Any]) -> list[sqlite3.Row]:
    """Filter rows without ranking. Every clause is an exact match.

    `classification` is required by the caller contract (`service.list_entries`
    validates it) for the same reason the knowledge store requires it: an
    unfiltered read across classifications is never the safe default.
    """
    clauses: list[str] = ["classification = ?"]
    values: list[Any] = [filters["classification"]]
    for column, key in (
        ("source", "source"), ("agent", "agent"), ("task_id", "task_id"),
        ("dispatch_id", "dispatch_id"), ("scope", "scope"),
    ):
        if filters.get(key):
            clauses.append(f"{column} = ?")
            values.append(filters[key])
    return db.execute(
        f"SELECT * FROM entries WHERE {' AND '.join(clauses)} ORDER BY created_at DESC, handle",
        values,
    ).fetchall()


def delete_entry(db: sqlite3.Connection, handle: str, reason: str) -> dict[str, Any] | None:
    """Voluntary early release. Records the same evidence a sweep would."""
    row = fetch_entry(db, handle)
    if row is None:
        return None
    with db:
        db.execute(
            """INSERT INTO expiry_evidence (id, handle, content_hash, byte_length,
                 classification, source, created_at, expires_at, swept_at, reason)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                str(uuid.uuid4()), row["handle"], row["content_hash"], row["byte_length"],
                row["classification"], row["source"], row["created_at"], row["expires_at"],
                now_iso(), reason,
            ),
        )
        db.execute("DELETE FROM entries WHERE handle = ?", (handle,))
    return {
        "handle": row["handle"],
        "content_hash": row["content_hash"],
        "byte_length": row["byte_length"],
        "reason": reason,
    }


def mark_promoted(db: sqlite3.Connection, handle: str) -> str:
    """Record that a proposal was *emitted* for this entry.

    Not that one was accepted, or even staged -- `promote` writes nothing to
    the knowledge store, and this store has no way to observe what happened
    downstream. The timestamp answers "was this already proposed?", nothing
    more.
    """
    moment = now_iso()
    with db:
        db.execute("UPDATE entries SET promoted_at = ? WHERE handle = ?", (moment, handle))
    return moment


def record_access(db: sqlite3.Connection, access: dict[str, Any]) -> str:
    access_id = str(uuid.uuid4())
    with db:
        db.execute(
            """INSERT INTO access_runs (id, operation, handle, query_hash, task_id, agent,
                 classification, scope_filter, source, result_count, created_at)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                access_id, access["operation"], access.get("handle"), access.get("query_hash"),
                access["task_id"], access["agent"], access["classification"],
                access.get("scope"), access["source"], access["result_count"], now_iso(),
            ),
        )
    return access_id


def store_stats(db: sqlite3.Connection) -> dict[str, Any]:
    counts = db.execute(
        """SELECT
             (SELECT COUNT(*) FROM entries) AS entries,
             (SELECT COALESCE(SUM(byte_length), 0) FROM entries) AS bytes_stored,
             (SELECT COUNT(*) FROM entries WHERE untrusted_inputs = 1) AS untrusted_entries,
             (SELECT COUNT(*) FROM access_runs) AS access_runs,
             (SELECT COUNT(*) FROM expiry_evidence) AS expired_or_dropped"""
    ).fetchone()
    by_scope = [
        dict(row)
        for row in db.execute("SELECT scope, COUNT(*) AS entries FROM entries GROUP BY scope ORDER BY scope")
    ]
    by_source = [
        dict(row)
        for row in db.execute("SELECT source, COUNT(*) AS entries FROM entries GROUP BY source ORDER BY source")
    ]
    chunks = db.execute(
        """SELECT COUNT(*) AS chunks, COUNT(DISTINCT handle) AS indexed_entries FROM entry_chunks"""
    ).fetchone()
    return {**dict(counts), **dict(chunks), "by_scope": by_scope, "by_source": by_source}
