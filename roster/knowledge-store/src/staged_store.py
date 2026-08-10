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
CREATE TABLE IF NOT EXISTS staged_record_dispositions (
  record_id TEXT NOT NULL REFERENCES staged_records(id) ON DELETE CASCADE,
  sequence INTEGER NOT NULL,
  action TEXT NOT NULL,
  reason TEXT NOT NULL,
  classification_used TEXT NOT NULL,
  diverged_from_proposal INTEGER NOT NULL,
  decided_by TEXT NOT NULL,
  decided_at TEXT NOT NULL,
  PRIMARY KEY (record_id, sequence)
);
CREATE TABLE IF NOT EXISTS staged_record_deletions (
  record_id TEXT NOT NULL,
  title TEXT NOT NULL,
  content_digest TEXT NOT NULL,
  status_at_deletion TEXT NOT NULL,
  reason TEXT NOT NULL,
  deleted_by TEXT NOT NULL,
  authorized_by TEXT,
  deleted_at TEXT NOT NULL,
  PRIMARY KEY (record_id, deleted_at)
);
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
        # Upsert, never INSERT OR REPLACE: REPLACE *deletes* the existing row
        # before reinserting, and with PRAGMA foreign_keys = ON that cascades
        # into staged_record_dispositions and silently erases the record's
        # entire disposition history. open_store enables that pragma, so the
        # difference is invisible on a bare sqlite3.connect and destructive in
        # the real CLI path.
        db.execute(
            "INSERT INTO staged_records "
            "(id, status, frontmatter_json, body, content_digest, created_at, updated_at) "
            "VALUES (?, ?, ?, ?, ?, ?, ?) "
            "ON CONFLICT(id) DO UPDATE SET "
            "status = excluded.status, frontmatter_json = excluded.frontmatter_json, "
            "body = excluded.body, content_digest = excluded.content_digest, "
            "updated_at = excluded.updated_at",
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


def put_generated_record(db: sqlite3.Connection, frontmatter: dict[str, Any], body: str) -> dict[str, Any]:
    """Store a record whose id was generated (`finding_record.build_record`), refusing to
    silently clobber whatever already occupies that id.

    `put_record` upserts by id unconditionally, which is correct when a human
    or steward is amending a record they know exists -- they typed its id on
    purpose. It is the wrong behaviour for a *generated* id: the caller never
    chose it, so a collision there is either the same finding proposed twice
    (harmless -- the digest is a pure function of the body, so identical
    content means nothing changed) or two different findings that happened to
    land on the same id (never harmless -- `put_record`'s upsert would
    silently overwrite one, including reverting an already-dispositioned
    record's status back to 'proposed').

    So: same id, same `content_digest` -> treated as already staged, nothing
    written, existing status left untouched. Same id, different
    `content_digest` -> refused loudly, nothing written. There is no third
    case where this function writes over an existing row.
    """
    record_id = frontmatter["id"]
    existing = get_record(db, record_id)
    if existing is not None:
        existing_frontmatter, _ = existing
        if existing_frontmatter.get("content_digest") == frontmatter.get("content_digest"):
            return {
                "status": "already-staged",
                "id": record_id,
                "record_status": existing_frontmatter.get("status"),
                "content_digest": frontmatter.get("content_digest"),
                "note": (
                    "A record with this generated id and identical content is already staged; "
                    "nothing was written, so its current disposition (if any) was left untouched."
                ),
            }
        raise StagedRecordError(
            f"generated id {record_id!r} collides with an existing staged record whose content "
            f"differs (existing content_digest {existing_frontmatter.get('content_digest')!r} != "
            f"{frontmatter.get('content_digest')!r}). Refused rather than overwritten: reword the "
            "finding's title so the generated id differs, or use `propose --input` with a "
            "hand-assigned id if this is deliberately meant to replace that exact record."
        )
    put_record(db, frontmatter, body)
    return {
        "status": "staged",
        "id": record_id,
        "record_status": frontmatter.get("status"),
        "content_digest": frontmatter.get("content_digest"),
        "note": (
            "Staged for knowledge-store-steward disposition. Staging is not ingestion: nothing is "
            "retrievable until a steward accepts this record and it is ingested."
        ),
    }


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


def _history_rows(db: sqlite3.Connection, record_id: str) -> list[dict[str, Any]]:
    rows = db.execute(
        "SELECT sequence, action, reason, classification_used, diverged_from_proposal, "
        "decided_by, decided_at FROM staged_record_dispositions WHERE record_id = ? "
        "ORDER BY sequence",
        (record_id,),
    ).fetchall()
    return [
        {
            "sequence": row["sequence"],
            "action": row["action"],
            "reason": row["reason"],
            "classification_used": row["classification_used"],
            "diverged_from_proposal": bool(row["diverged_from_proposal"]),
            "decided_by": row["decided_by"],
            "decided_at": row["decided_at"],
        }
        for row in rows
    ]


def get_history(db: sqlite3.Connection, record_id: str) -> list[dict[str, Any]]:
    """Every disposition ever recorded for this record, oldest first.

    Append-only by construction: `disposition_record` only ever inserts, and
    the frontmatter carries the *current* disposition while this carries all
    of them. A record deferred and later accepted retains both, which is the
    thing a single overwritten field would lose -- and losing it would make
    this audit trail worse than the git history it replaced.
    """
    return _history_rows(db, record_id)


def disposition_record(
    db: sqlite3.Connection,
    record_id: str,
    *,
    action: str,
    reason: str,
    classification_used: str,
    diverged_from_proposal: bool,
    decided_by: str,
) -> dict[str, Any]:
    """Record a steward decision, appending to history and updating the record.

    Enforces the separation invariant structurally: whoever staged a record
    cannot disposition it. That rule already exists in prose -- the steward
    role says an agent may not disposition its own proposal -- and prose that
    nothing checks is the failure mode this contract was written to avoid.

    The automatic-defer rule and the action/status agreement rule are not
    re-implemented here: the amended frontmatter goes back through
    `put_record`, so the contract's own validator is the single authority on
    whether the result is legal.
    """
    loaded = get_record(db, record_id)
    if loaded is None:
        raise StagedRecordError(f"No staged record with id {record_id!r} in this store.")
    frontmatter, body = loaded
    if decided_by == frontmatter.get("staged_by"):
        raise StagedRecordError(
            f"{decided_by!r} staged this record and cannot also disposition it. Authorship and "
            "approval are separate: a steward other than the proposer must decide, per "
            "roster/shared/agent-autonomy.yaml and the knowledge-store steward's role definition."
        )
    if not reason.strip():
        raise StagedRecordError(
            "a disposition requires a reason: an unexplained decision is not an audit trail"
        )
    disposition = {
        "action": action,
        "reason": reason,
        "classification_used": classification_used,
        "diverged_from_proposal": diverged_from_proposal,
        "decided_by": decided_by,
    }
    amended = dict(frontmatter, status=action, disposition=disposition)
    # Validated by put_record before anything is written, so an illegal
    # disposition (action disagreeing with status, or accepting a record
    # flagged untrusted_instruction_risk) leaves no history row behind.
    put_record(db, amended, body)
    next_sequence = 1 + len(_history_rows(db, record_id))
    with db:
        db.execute(
            "INSERT INTO staged_record_dispositions (record_id, sequence, action, reason, "
            "classification_used, diverged_from_proposal, decided_by, decided_at) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
            (
                record_id,
                next_sequence,
                action,
                reason,
                classification_used,
                1 if diverged_from_proposal else 0,
                decided_by,
                _now(),
            ),
        )
    return {"id": record_id, "status": action, "sequence": next_sequence}


def deletion_evidence(db: sqlite3.Connection) -> list[dict[str, Any]]:
    """Every deletion ever performed, oldest first.

    Deliberately carries no foreign key to `staged_records`: the whole point
    is that this outlives the row it describes. Evidence that vanished with
    its subject would make a deletion indistinguishable from data loss.
    """
    rows = db.execute(
        "SELECT record_id, title, content_digest, status_at_deletion, reason, deleted_by, "
        "authorized_by, deleted_at FROM staged_record_deletions ORDER BY deleted_at, record_id"
    ).fetchall()
    return [dict(row) for row in rows]


def delete_record(
    db: sqlite3.Connection,
    record_id: str,
    *,
    reason: str,
    deleted_by: str,
    authorized_by: str | None = None,
) -> dict[str, Any]:
    """Delete a staged record, leaving evidence behind.

    **This deletes a staging-table row, not ingested knowledge.** A staged
    record has never been embedded or made retrievable. This function never
    reaches any of the ingested-content tables in `database.py`'s SCHEMA
    (`test_scope_enforcement.py`'s `test_ac15_deletion_is_confined_to_staged_records`
    asserts this structurally, by name, against this very function's source
    -- see that test before editing this docstring, since naming those
    tables literally here would break it) -- deletion of ingested content is
    a separate capability, `ingested_deletion.delete_ingested` (issue #184,
    `cadre knowledge delete-ingested`), with its own evidence table and its
    own steward-only authorization requirements. Nothing here should be read
    as that capability, and nothing there reaches the staging table this
    function operates on.

    An `accepted` record requires `authorized_by`. Acceptance is a steward's
    decision that the record is durable knowledge, so removing it afterwards
    is a reversal that needs a human, per the escalation the steward's role
    definition already describes.

    Proposer-versus-decider check, decided deliberately (unlike the
    asymmetry `disposition_record` already closed, this was open until now):
    a still-`proposed` record has had no decision made about it yet, so its
    own proposer may delete it -- that is just withdrawing a draft, and
    nothing about the authorship/approval-separation invariant applies to a
    record nobody has approved or rejected. Once a record carries *any*
    disposition (`accepted`, `rejected`, or `deferred`), a decision exists,
    and the same separation `disposition_record` enforces for making that
    decision is enforced here for undoing evidence of it: the proposer may
    not also be the one who deletes a dispositioned record, full stop --
    independent of, and in addition to, the `accepted`/`authorized_by` check
    above (an accepted record needs both a non-proposer deleter *and* human
    authorization; the two checks protect different things). Without this,
    an agent whose finding was rejected could quietly delete the rejection
    and retry elsewhere, leaving only the retained deletion-evidence row --
    accurate, but not something anyone would think to check. This mirrors
    `roster/shared/agent-autonomy.yaml`'s
    `ingest_update_reclassify_or_delete: knowledge_store_steward_only` in the
    same spirit `disposition_record` already applies it.
    """
    loaded = get_record(db, record_id)
    if loaded is None:
        raise StagedRecordError(f"No staged record with id {record_id!r} in this store.")
    frontmatter, _ = loaded
    if not reason.strip():
        raise StagedRecordError(
            "a deletion requires a reason: an unexplained deletion is indistinguishable "
            "from data loss"
        )
    status = frontmatter.get("status")
    if status != "proposed" and deleted_by == frontmatter.get("staged_by"):
        raise StagedRecordError(
            f"{deleted_by!r} staged this record and it already carries a disposition ({status!r}), "
            "so deleting it must not be the proposer's own act either -- the same separation "
            "disposition_record enforces for deciding a record's outcome applies to erasing "
            "evidence of that decision. A steward other than the proposer must delete it. A "
            "record that is still 'proposed' has no decision yet to protect and may be withdrawn "
            "by its own proposer."
        )
    if status == "accepted" and not (authorized_by or "").strip():
        raise StagedRecordError(
            f"record {record_id!r} was accepted, so deleting it reverses a steward's decision and "
            "requires an authorized human: pass --authorized-by. Acceptance is not a draft state."
        )
    with db:
        db.execute(
            "INSERT INTO staged_record_deletions (record_id, title, content_digest, "
            "status_at_deletion, reason, deleted_by, authorized_by, deleted_at) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
            (
                record_id,
                frontmatter.get("title", ""),
                frontmatter.get("content_digest", ""),
                status,
                reason,
                deleted_by,
                authorized_by,
                _now(),
            ),
        )
        # History goes with the record (ON DELETE CASCADE); the evidence row
        # above is what survives, and it is written first so a failure here
        # cannot leave a deletion unrecorded.
        db.execute("DELETE FROM staged_records WHERE id = ?", (record_id,))
    return {
        "status": "deleted",
        "id": record_id,
        "content_digest": frontmatter.get("content_digest", ""),
        "status_at_deletion": status,
        "evidence_retained": True,
    }


__all__ = [
    "SCHEMA",
    "StagedRecordError",
    "RecordFormatError",
    "install_schema",
    "serialize_record",
    "put_record",
    "put_record_text",
    "put_generated_record",
    "get_record",
    "get_record_text",
    "list_records",
    "export_records",
    "disposition_record",
    "get_history",
    "delete_record",
    "deletion_evidence",
]
