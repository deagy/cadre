"""SQLite storage for staged knowledge records.

Staged records used to live as Markdown files under `proposed-knowledge/`,
which meant capturing a finding cost a pull request and a full CI matrix. This
module moves the *instances* into the store while leaving the *contract* in
git: `proposed-knowledge.schema.json` still defines the shape, and
`staged_records.py` still enforces every rule.

That split is the point. This module is a second **storage backend**, never a
second implementation of the contract. Every write goes through
`staged_records.validate_parsed`, and `serialize_record` emits only constructs
`staged_records.parse_record` accepts, so the round trip is closed by
construction rather than by agreement between two hand-written formats. The
digest is never computed here -- `staged_records.compute_digest` is the only
implementation, for the reason its own docstring gives.

The round trip is the load-bearing property, because an export that silently
drops a field turns the durability backup into a corruption vector, and that
would only be discovered when the store was lost. `test_staged_store.py`
asserts `parse(serialize(record)) == record` over every committed record and
over the corruption cases.
"""

from __future__ import annotations

import json
import sqlite3
from datetime import datetime, timezone
from typing import Any

from staged_records import (
    DELIMITER,
    REQUIRED_KEYS,
    RecordFormatError,
    parse_record,
    validate_parsed,
)

# `disposition` is not in REQUIRED_KEYS -- it is absent until a steward acts --
# so it is serialised last, after every required key, in a fixed position.
_TRAILING_KEYS: tuple[str, ...] = ("disposition",)

SCHEMA = """
CREATE TABLE IF NOT EXISTS staged_records (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  frontmatter_json TEXT NOT NULL,
  body TEXT NOT NULL,
  content_digest TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_staged_records_status ON staged_records(status);
"""


class StagedRecordError(RuntimeError):
    """A staged record was rejected, with the contract findings attached."""

    def __init__(self, message: str, findings: list[str] | None = None) -> None:
        super().__init__(message)
        self.findings = findings or []


def _now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def install_schema(db: sqlite3.Connection) -> None:
    """Create the staged-record table if it is absent.

    Additive and idempotent, matching `database.py`'s own `CREATE TABLE IF NOT
    EXISTS` schema, so an existing store picks the table up without migration.
    """
    db.executescript(SCHEMA)


# ---------------------------------------------------------------------------
# Serialisation
# ---------------------------------------------------------------------------


def _scalar(value: Any) -> str:
    """Render one frontmatter scalar in a form `_parse_scalar` reads back exactly.

    Booleans and `None` are emitted bare, because the parser converts those
    tokens to their Python values and quoting them would round-trip a bool into
    the string "true". Everything else is double-quoted and escaped, which is
    always safe: quoting removes every ambiguity the parser would otherwise
    have to resolve (a leading `-`, an interior `: `, a value that looks like
    `null`, leading or trailing spaces).
    """
    if value is True:
        return "true"
    if value is False:
        return "false"
    if value is None:
        return "null"
    if not isinstance(value, str):
        raise StagedRecordError(
            f"cannot serialise frontmatter value of type {type(value).__name__!r}: the staged-record "
            "contract has only strings, booleans, string lists, and one level of nested mapping"
        )
    escaped = value.replace("\\", "\\\\").replace('"', '\\"')
    return f'"{escaped}"'


def _emit(key: str, value: Any, lines: list[str]) -> None:
    if isinstance(value, list):
        lines.append(f"{key}:")
        for item in value:
            lines.append(f"  - {_scalar(item)}")
        return
    if isinstance(value, dict):
        lines.append(f"{key}:")
        for sub_key, sub_value in value.items():
            if isinstance(sub_value, (list, dict)):
                raise StagedRecordError(
                    f"{key}.{sub_key}: the staged-record frontmatter parser supports one level of "
                    "nesting, so a nested list or mapping cannot be represented"
                )
            lines.append(f"  {sub_key}: {_scalar(sub_value)}")
        return
    lines.append(f"{key}: {_scalar(value)}")


def serialize_record(frontmatter: dict[str, Any], body: str) -> str:
    """Render `(frontmatter, body)` back to staged-record text.

    Key order is fixed -- `REQUIRED_KEYS` order, then `disposition` -- so the
    same record always serialises identically and an export diff shows content
    changes rather than dictionary ordering. Any key outside the contract is
    emitted after the known ones rather than dropped: silently discarding an
    unrecognised key is how an export loses data, and the validator will reject
    it on the way back in anyway.
    """
    known = list(REQUIRED_KEYS) + list(_TRAILING_KEYS)
    lines: list[str] = [DELIMITER]
    for key in known:
        if key in frontmatter:
            _emit(key, frontmatter[key], lines)
    for key, value in frontmatter.items():
        if key not in known:
            _emit(key, value, lines)
    lines.append(DELIMITER)
    text = "\n".join(lines) + "\n"
    normalized_body = body.replace("\r\n", "\n").replace("\r", "\n")
    if not normalized_body.startswith("\n"):
        text += "\n"
    return text + normalized_body


# ---------------------------------------------------------------------------
# Storage
# ---------------------------------------------------------------------------


def _validated(frontmatter: dict[str, Any], body: str) -> None:
    findings = validate_parsed(frontmatter, body)
    if findings:
        raise StagedRecordError(
            "staged record does not satisfy the contract: " + "; ".join(findings), findings
        )


def put_record(db: sqlite3.Connection, frontmatter: dict[str, Any], body: str) -> str:
    """Validate and store a record, returning its id.

    Validation happens *before* the write, so a malformed record never exists
    rather than being caught later by a checker. Replacing an existing id is
    deliberate and preserves `created_at`: a steward amending a disposition is
    updating the same record, not creating a second one.
    """
    _validated(frontmatter, body)
    record_id = frontmatter["id"]
    now = _now()
    existing = db.execute(
        "SELECT created_at FROM staged_records WHERE id = ?", (record_id,)
    ).fetchone()
    created_at = existing["created_at"] if existing else now
    with db:
        db.execute(
            "INSERT OR REPLACE INTO staged_records "
            "(id, status, frontmatter_json, body, content_digest, created_at, updated_at) "
            "VALUES (?, ?, ?, ?, ?, ?, ?)",
            (
                record_id,
                frontmatter["status"],
                json.dumps(frontmatter, ensure_ascii=False, sort_keys=True),
                body,
                frontmatter["content_digest"],
                created_at,
                now,
            ),
        )
    return record_id


def put_record_text(db: sqlite3.Connection, text: str) -> str:
    """Parse staged-record text, then store it. Raises on malformed text."""
    frontmatter, body = parse_record(text)
    return put_record(db, frontmatter, body)


def get_record(db: sqlite3.Connection, record_id: str) -> tuple[dict[str, Any], str] | None:
    row = db.execute(
        "SELECT frontmatter_json, body FROM staged_records WHERE id = ?", (record_id,)
    ).fetchone()
    if row is None:
        return None
    return json.loads(row["frontmatter_json"]), row["body"]


def get_record_text(db: sqlite3.Connection, record_id: str) -> str | None:
    loaded = get_record(db, record_id)
    if loaded is None:
        return None
    return serialize_record(*loaded)


def list_records(db: sqlite3.Connection, status: str | None = None) -> list[dict[str, Any]]:
    """Summaries for `cadre knowledge list`, ordered by id for determinism."""
    if status is None:
        rows = db.execute(
            "SELECT id, status, content_digest, created_at, updated_at, frontmatter_json "
            "FROM staged_records ORDER BY id"
        ).fetchall()
    else:
        rows = db.execute(
            "SELECT id, status, content_digest, created_at, updated_at, frontmatter_json "
            "FROM staged_records WHERE status = ? ORDER BY id",
            (status,),
        ).fetchall()
    summaries = []
    for row in rows:
        frontmatter = json.loads(row["frontmatter_json"])
        summaries.append(
            {
                "id": row["id"],
                "status": row["status"],
                "title": frontmatter.get("title", ""),
                "recommended_action": frontmatter.get("recommended_action", ""),
                "content_digest": row["content_digest"],
                "created_at": row["created_at"],
                "updated_at": row["updated_at"],
            }
        )
    return summaries


def export_records(db: sqlite3.Connection, status: str | None = None) -> dict[str, str]:
    """Every stored record as `{id: record_text}`, ready to write to files.

    This is the durability path from the proposal, so it must be lossless:
    what comes out must parse and validate as what went in, digest and
    disposition history included.
    """
    exported: dict[str, str] = {}
    for summary in list_records(db, status):
        text = get_record_text(db, summary["id"])
        if text is None:  # pragma: no cover - list and get cannot disagree
            raise StagedRecordError(f"record {summary['id']!r} vanished between list and export")
        exported[summary["id"]] = text
    return exported


__all__ = [
    "SCHEMA",
    "StagedRecordError",
    "RecordFormatError",
    "install_schema",
    "serialize_record",
    "put_record",
    "put_record_text",
    "get_record",
    "get_record_text",
    "list_records",
    "export_records",
]
