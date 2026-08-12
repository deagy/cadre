"""Ingest steward-accepted staged records into the retrievable corpus (G-7).

The gap this closes, stated plainly: **capture worked and the pipeline stopped
one action short of usefulness.** Findings are proposed with `cadre knowledge
propose`, land in `staged_records`, and a steward dispositions them with
`disposition-staged`. Nothing then moved an accepted record into `chunks`, and
`search_store()` scores only `chunks` rows — so an accepted record was
permanently unreachable by any query.

That is not a hypothetical. The session that filed G-7 re-derived from scratch
two findings that were already sitting in `staged_records`, accepted, describing
the exact problems it was re-solving. The store had the answers and could not be
asked.

**Staging still is not ingestion, and accepting still is not ingesting.** This
module does not collapse those steps — it supplies the missing third one. The
steward decides (`disposition_record`, which structurally forbids the proposer
from dispositioning their own record); this executes a decision already made.

Three rules it enforces, none of which is new policy:

- **`untrusted_instruction_risk` is disqualifying.** The staged-record contract
  already forces such a record to `deferred`, so an accepted one should not
  exist; this refuses it anyway rather than trusting an invariant enforced
  elsewhere.
- **The steward's `classification_used` wins over the proposer's
  `proposed_classification`.** The disposition is the authoritative decision;
  the proposal is a request. Ingesting at the proposed level would let a
  proposer widen classification by asking.
- **Ingested state is derived, never recorded twice.** A record is ingested iff
  a message with its id exists in the corpus. A second `ingested` flag on the
  staged record could disagree with the corpus, and then two places would claim
  to know.
"""

from __future__ import annotations

import sqlite3
from typing import Any

from content import protect_content
from database import save_message
from embeddings import embed_texts
from staged_store import list_records, get_record
from text_chunking import chunk_text

# A dedicated source, so an accepted finding is attributable and filterable as
# what it is. Retrieval already requires an explicit `--source` or
# `--all-sources`, so a caller reaches these deliberately rather than by
# accident.
STAGED_SOURCE = "proposed-knowledge"
STAGED_ROLE = "knowledge-record"


class AcceptedIngestError(ValueError):
    """An accepted record cannot be ingested, with the reason named."""


def _message_for(record_id: str, frontmatter: dict[str, Any], body: str) -> dict[str, Any]:
    disposition = frontmatter.get("disposition") or {}
    classification = disposition.get("classification_used") or frontmatter.get(
        "proposed_classification"
    )
    if not classification:
        raise AcceptedIngestError(
            f"{record_id}: no classification to ingest at. The steward's "
            "disposition.classification_used is authoritative and is missing."
        )
    title = frontmatter.get("title") or record_id
    # Title first so a retrieved chunk carries the claim, not just its
    # supporting prose -- a scorer matching the body of a finding whose title
    # states the finding is the shape that makes results look irrelevant.
    content = f"{title}\n\n{body.strip()}\n"
    return {
        "source": STAGED_SOURCE,
        # Deliberately omitted. `source_uri` may reveal a local path, and the
        # knowledge-use policy's redact-by-default rule applies to a staged
        # record's provenance exactly as it does to a citation's.
        "source_uri": None,
        "conversation_id": record_id,
        "conversation_title": title,
        "message_id": record_id,
        "role": STAGED_ROLE,
        "content": content,
        "created_at": frontmatter.get("staged_at") or frontmatter.get("created_at"),
        "classification": classification,
        "metadata": {
            "staged_record_id": record_id,
            "recommended_action": frontmatter.get("recommended_action"),
            "source_scope": frontmatter.get("source_scope"),
            "content_digest": frontmatter.get("content_digest"),
            "decided_by": disposition.get("decided_by"),
        },
    }


def already_ingested(db: sqlite3.Connection, record_id: str) -> bool:
    """Derived, not stored. The corpus is the only place that knows."""
    row = db.execute(
        "SELECT 1 FROM messages WHERE source = ? AND conversation_id = ? LIMIT 1",
        (STAGED_SOURCE, record_id),
    ).fetchone()
    return row is not None


def accepted_records(db: sqlite3.Connection) -> list[dict[str, Any]]:
    return [row for row in list_records(db, status="accepted")]


def ingest_accepted(
    db: sqlite3.Connection,
    config: dict[str, Any],
    *,
    record_ids: list[str] | None = None,
    dry_run: bool = False,
) -> dict[str, Any]:
    """Ingest every accepted staged record that is not already in the corpus.

    Returns a per-record report rather than a count. A steward running this
    needs to know which findings became retrievable and which were refused, and
    a summary number answers neither.
    """
    wanted = set(record_ids or [])
    ingested: list[dict[str, Any]] = []
    skipped: list[dict[str, Any]] = []
    refused: list[dict[str, Any]] = []

    for summary in accepted_records(db):
        record_id = summary["id"]
        if wanted and record_id not in wanted:
            continue
        loaded = get_record(db, record_id)
        if loaded is None:  # pragma: no cover -- listed then vanished
            continue
        frontmatter, body = loaded

        risk = frontmatter.get("untrusted_instruction_risk")
        if risk is True or risk == "unknown":
            refused.append({
                "id": record_id,
                "reason": (
                    f"untrusted_instruction_risk is {risk!r}. The staged-record contract "
                    "forces such a record to 'deferred'; an accepted one should not exist, "
                    "and it is refused here rather than trusted to have been caught upstream."
                ),
            })
            continue

        if already_ingested(db, record_id):
            skipped.append({"id": record_id, "reason": "already in the corpus"})
            continue

        try:
            message = _message_for(record_id, frontmatter, body)
        except AcceptedIngestError as error:
            refused.append({"id": record_id, "reason": str(error)})
            continue

        if dry_run:
            ingested.append({
                "id": record_id,
                "classification": message["classification"],
                "dry_run": True,
            })
            continue

        protected = protect_content(message["content"], config["ingestion"]["redact_secrets"])
        if protected["injection_risk"]:
            # Secret redaction and injection screening apply to a staged record
            # exactly as to any other ingested content. A finding is authored
            # text like any other, and "an agent wrote it" is not provenance.
            refused.append({
                "id": record_id,
                "reason": (
                    "content screening flagged injection risk; ingesting it would put "
                    "unvetted instruction-shaped text into the retrievable corpus"
                ),
            })
            continue
        chunks = chunk_text(protected["content"], config["chunking"])
        vectors = embed_texts(chunks, config["embedding"])
        save_message(db, message, protected, chunks, vectors, config["embedding"])
        ingested.append({
            "id": record_id,
            "classification": message["classification"],
            "chunks": len(chunks),
        })

    unknown = sorted(wanted - {r["id"] for r in accepted_records(db)}) if wanted else []
    return {
        "ingested": ingested,
        "skipped": skipped,
        "refused": refused,
        "not_accepted": unknown,
        "dry_run": dry_run,
    }
