"""SQLite persistence compatible with the original Node implementation."""

from __future__ import annotations

import hashlib
import json
import sqlite3
import uuid
from contextlib import nullcontext
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


def _hash(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def _now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


SCHEMA = """
CREATE TABLE IF NOT EXISTS ingestion_runs (
  id TEXT PRIMARY KEY, source TEXT NOT NULL, source_uri TEXT, started_at TEXT NOT NULL,
  completed_at TEXT, status TEXT NOT NULL, message_count INTEGER NOT NULL DEFAULT 0,
  chunk_count INTEGER NOT NULL DEFAULT 0, error TEXT
);
CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY, source TEXT NOT NULL, source_uri TEXT, conversation_id TEXT NOT NULL,
  conversation_title TEXT, source_message_id TEXT NOT NULL, role TEXT NOT NULL, content TEXT NOT NULL,
  content_hash TEXT NOT NULL, created_at TEXT, classification TEXT NOT NULL,
  injection_risk INTEGER NOT NULL DEFAULT 0, redactions_json TEXT NOT NULL,
  metadata_json TEXT NOT NULL, ingested_at TEXT NOT NULL, retention_until TEXT,
  UNIQUE(source, conversation_id, source_message_id)
);
CREATE TABLE IF NOT EXISTS chunks (
  id TEXT PRIMARY KEY, message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL, content TEXT NOT NULL, content_hash TEXT NOT NULL,
  embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
  embedding_dimensions INTEGER NOT NULL, embedding_json TEXT NOT NULL,
  UNIQUE(message_id, ordinal, embedding_provider, embedding_model)
);
CREATE TABLE IF NOT EXISTS retrieval_runs (
  id TEXT PRIMARY KEY, query_hash TEXT NOT NULL, task_id TEXT NOT NULL, agent TEXT NOT NULL,
  classification TEXT NOT NULL, source_filter TEXT, embedding_provider TEXT NOT NULL,
  embedding_model TEXT NOT NULL, requested_top INTEGER NOT NULL, result_count INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_source ON messages(source);
CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id);
CREATE INDEX IF NOT EXISTS idx_messages_classification ON messages(classification);
CREATE INDEX IF NOT EXISTS idx_chunks_model ON chunks(embedding_provider, embedding_model);
CREATE INDEX IF NOT EXISTS idx_retrieval_runs_task ON retrieval_runs(task_id, agent);
"""


def _migrate_additive_columns(db: sqlite3.Connection) -> None:
    """Add columns introduced after a store's initial creation, additively.

    `CREATE TABLE IF NOT EXISTS` in SCHEMA only covers a store created fresh
    with the current schema; a store created before `retention_until` existed
    has a `messages` table with no such column, and `ALTER TABLE ... ADD
    COLUMN` has no `IF NOT EXISTS` form in SQLite. `retention_until` is
    nullable with no default, so this is purely additive: existing rows read
    back as `NULL` (no retention window recorded, matching the "unknown" case
    a pre-existing row legitimately has), and no existing query, index, or
    constraint changes shape. Idempotent and cheap enough to run on every
    open, matching this schema's existing "no separate migration step"
    convention.
    """
    existing_columns = {row["name"] for row in db.execute("PRAGMA table_info(messages)")}
    if "retention_until" not in existing_columns:
        db.execute("ALTER TABLE messages ADD COLUMN retention_until TEXT")


def open_store(database_path: str) -> sqlite3.Connection:
    path = Path(database_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    db = sqlite3.connect(path)
    db.row_factory = sqlite3.Row
    db.execute("PRAGMA foreign_keys = ON")
    db.execute("PRAGMA journal_mode = WAL")
    db.executescript(SCHEMA)
    _migrate_additive_columns(db)
    return db


def begin_run(db: sqlite3.Connection, source: str, source_uri: str | None) -> str:
    run_id = str(uuid.uuid4())
    with db:
        db.execute("INSERT INTO ingestion_runs (id, source, source_uri, started_at, status) VALUES (?, ?, ?, ?, 'running')", (run_id, source, source_uri, _now()))
    return run_id


def complete_run(db: sqlite3.Connection, run_id: str, message_count: int, chunk_count: int) -> None:
    with db:
        db.execute("UPDATE ingestion_runs SET completed_at = ?, status = 'complete', message_count = ?, chunk_count = ? WHERE id = ?", (_now(), message_count, chunk_count, run_id))


def fail_run(db: sqlite3.Connection, run_id: str, error: Exception) -> None:
    with db:
        db.execute("UPDATE ingestion_runs SET completed_at = ?, status = 'failed', error = ? WHERE id = ?", (_now(), str(error)[:2000], run_id))


def save_message(
    db: sqlite3.Connection,
    message: dict[str, Any],
    protected: dict[str, Any],
    chunks: list[str],
    vectors: list[list[float]],
    embedding: dict[str, Any],
    retention_until: str | None = None,
) -> None:
    message_id = _hash(f"{message['source']}|{message['conversation_id']}|{message['message_id']}")
    with (nullcontext() if db.in_transaction else db):
        db.execute("""
          INSERT INTO messages (id, source, source_uri, conversation_id, conversation_title,
            source_message_id, role, content, content_hash, created_at, classification,
            injection_risk, redactions_json, metadata_json, ingested_at, retention_until)
          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
          ON CONFLICT(source, conversation_id, source_message_id) DO UPDATE SET
            source_uri=excluded.source_uri, conversation_title=excluded.conversation_title,
            role=excluded.role, content=excluded.content, content_hash=excluded.content_hash,
            created_at=excluded.created_at, classification=excluded.classification,
            injection_risk=excluded.injection_risk, redactions_json=excluded.redactions_json,
            metadata_json=excluded.metadata_json, ingested_at=excluded.ingested_at,
            retention_until=excluded.retention_until
        """, (
            message_id, message["source"], message["source_uri"], message["conversation_id"],
            message["conversation_title"], message["message_id"], message["role"], protected["content"],
            _hash(protected["content"]), message["created_at"], message["classification"],
            1 if protected["injection_risk"] else 0,
            json.dumps(protected["redactions"], separators=(",", ":"), ensure_ascii=False),
            json.dumps(message["metadata"], separators=(",", ":"), ensure_ascii=False), _now(),
            retention_until,
        ))
        db.execute("DELETE FROM chunks WHERE message_id = ?", (message_id,))
        for ordinal, chunk in enumerate(chunks):
            chunk_id = _hash(f"{message_id}|{ordinal}|{embedding['provider']}|{embedding['model']}")
            vector = vectors[ordinal]
            db.execute("""
              INSERT INTO chunks (id, message_id, ordinal, content, content_hash,
                embedding_provider, embedding_model, embedding_dimensions, embedding_json)
              VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """, (
                chunk_id, message_id, ordinal, chunk, _hash(chunk), embedding["provider"],
                embedding["model"], len(vector), json.dumps(vector, separators=(",", ":")),
            ))


def load_chunks(db: sqlite3.Connection, embedding: dict[str, Any], filters: dict[str, Any]) -> list[sqlite3.Row]:
    clauses = ["c.embedding_provider = ?", "c.embedding_model = ?", "c.embedding_dimensions = ?"]
    values: list[Any] = [embedding["provider"], embedding["model"], embedding["dimensions"]]
    # A list, because one query may legitimately span a project's ingested
    # corpus and `proposed-knowledge` (the dedicated source steward-accepted
    # findings land under). Empty/absent still means "every source", which is
    # what `--all-sources` resolves to -- the scope gate in cli.py is what
    # decides whether that is allowed, not this function.
    sources = filters.get("sources")
    if sources:
        clauses.append(f"m.source IN ({', '.join('?' * len(sources))})")
        values.extend(sources)
    if not filters.get("classification"):
        raise ValueError("A classification filter is required")
    clauses.append("m.classification = ?")
    values.append(filters["classification"])
    return db.execute(f"""SELECT c.id AS chunk_id, c.ordinal, c.content, c.content_hash,
      c.embedding_json, m.source, m.source_uri, m.conversation_id, m.conversation_title,
      m.source_message_id AS message_id, m.role, m.created_at, m.classification, m.injection_risk
      FROM chunks c JOIN messages m ON m.id = c.message_id
      WHERE {' AND '.join(clauses)}""", values).fetchall()


def encode_source_filter(sources: list[str] | None) -> str | None:
    """Encode the retrieval scope for the `retrieval_runs.source_filter` column.

    JSON rather than a delimiter-joined string: a source identifier is
    caller-supplied and may contain any character, so no separator is safely
    unambiguous. NULL keeps its existing meaning of "no scope" -- an
    `--all-sources` read.

    **Rows written before `--source` became repeatable hold a bare source
    string, not JSON.** This column is an append-only audit log and is not
    rewritten, so a reader must accept both encodings: try `json.loads` and
    treat a failure (or a non-list result) as a single legacy source.
    """
    if not sources:
        return None
    return json.dumps(list(sources))


def record_retrieval(db: sqlite3.Connection, retrieval: dict[str, Any]) -> str:
    retrieval_id = str(uuid.uuid4())
    with db:
        db.execute("""INSERT INTO retrieval_runs (id, query_hash, task_id, agent, classification,
          source_filter, embedding_provider, embedding_model, requested_top, result_count, created_at)
          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""", (
            retrieval_id, retrieval["query_hash"], retrieval["task_id"], retrieval["agent"],
            retrieval["classification"], encode_source_filter(retrieval.get("sources")),
            retrieval["embedding"]["provider"],
            retrieval["embedding"]["model"], retrieval["requested_top"], retrieval["result_count"], _now(),
        ))
    return retrieval_id


def store_stats(db: sqlite3.Connection) -> dict[str, Any]:
    counts = db.execute("""SELECT
      (SELECT COUNT(*) FROM messages) AS messages,
      (SELECT COUNT(*) FROM chunks) AS chunks,
      (SELECT COUNT(*) FROM retrieval_runs) AS retrieval_runs,
      (SELECT COUNT(*) FROM ingestion_runs WHERE status = 'complete') AS completed_runs,
      (SELECT COUNT(*) FROM ingestion_runs WHERE status = 'failed') AS failed_runs""").fetchone()
    sources = [dict(row) for row in db.execute("SELECT source, COUNT(*) AS messages FROM messages GROUP BY source ORDER BY source")]
    return {**dict(counts), "sources": sources}
