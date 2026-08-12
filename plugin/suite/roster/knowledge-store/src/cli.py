#!/usr/bin/env python3
"""Command-line interface for the vectorized knowledge store."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

from config import TIER_GLOBAL_FALLBACK, load_config
from database import open_store, store_stats
from finding_record import FindingError, build_record
from ingested_deletion import SCOPES as INGESTED_DELETION_SCOPES
from ingested_deletion import TRIGGERS as INGESTED_DELETION_TRIGGERS
from ingested_deletion import IngestedDeletionError
from ingested_deletion import delete_ingested
from ingested_deletion import deletion_evidence as ingested_deletion_evidence
from ingested_deletion import install_schema as install_ingested_deletion_schema
from ingested_deletion import retention_report as build_retention_report
from service import build_agent_context, ingest_file, search_store, stable_query_id
from settings import SettingsError
from staged_records import STATUS_VALUES as STAGED_STATUSES
from staged_records import parse_record, validate_parsed
from accepted_ingest import ingest_accepted
from staged_store import (
    delete_record,
    deletion_evidence,
    disposition_record,
    export_records,
    get_history,
    get_record,
    install_schema,
    list_records,
    put_generated_record,
    put_record,
    put_record_text,
    serialize_record,
)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="cadre knowledge", description="Local agent knowledge store")
    subparsers = parser.add_subparsers(dest="command", required=True)

    def add_config(command: argparse.ArgumentParser) -> None:
        command.add_argument("--config")

    init = subparsers.add_parser("init")
    add_config(init)
    ingest = subparsers.add_parser("ingest")
    ingest.add_argument("--input", required=True)
    # No default here: the shared global-fallback tier requires an explicit,
    # caller-supplied --source (KS-FR-10); the "chat-export" default is
    # still applied, but only for project-local/explicit-config tiers, in
    # _enforce_ingest_scope below (KS-FR-11).
    ingest.add_argument("--source")
    ingest.add_argument("--classification")
    ingest.add_argument(
        "--retention-days",
        dest="retention_days",
        type=int,
        help=(
            "override the classification's configured retention default. Required for "
            "--classification restricted unless retention.refuse_restricted_without_window "
            "is disabled in config -- restricted has no configured default on purpose."
        ),
    )
    add_config(ingest)
    search = subparsers.add_parser("search")
    search.add_argument("--query", required=True)
    search.add_argument("--classification", required=True)
    search.add_argument("--top")
    # Repeatable: a caller may need more than one source in a single query --
    # a project's ingested corpus plus `proposed-knowledge`, the dedicated
    # source steward-accepted findings land under (`accepted_ingest.py`). That
    # source still has to be named, so it is reached deliberately rather than
    # by accident; being repeatable only removes the need for two queries.
    search.add_argument("--source", action="append", dest="sources", metavar="SOURCE")
    search.add_argument("--all-sources", action="store_true")
    add_config(search)
    context = subparsers.add_parser("context")
    context.add_argument("--agent", required=True)
    context.add_argument("--task-id", required=True, dest="task_id")
    context.add_argument("--query", required=True)
    context.add_argument("--classification", required=True)
    context.add_argument("--top")
    context.add_argument("--source", action="append", dest="sources", metavar="SOURCE")
    context.add_argument("--all-sources", action="store_true")
    add_config(context)
    stats = subparsers.add_parser("stats")
    add_config(stats)

    # Retention/deletion of *ingested* content (issue #184) -- distinct from,
    # and never to be confused with, delete-staged above. retention-report is
    # read-only: it never deletes anything, only lists what a steward could
    # act on with delete-ingested.
    retention_report_parser = subparsers.add_parser("retention-report")
    retention_report_parser.add_argument(
        "--as-of",
        dest="as_of",
        help=(
            "ISO-8601 date or timestamp to evaluate expiry against (default: now). "
            "A date alone means midnight UTC starting that day, so pass a full "
            "timestamp to include that day's expiries."
        ),
    )
    add_config(retention_report_parser)
    delete_ingested_parser = subparsers.add_parser("delete-ingested")
    delete_ingested_parser.add_argument("--scope", required=True, choices=INGESTED_DELETION_SCOPES)
    delete_ingested_parser.add_argument("--id", required=True, dest="scope_key")
    delete_ingested_parser.add_argument("--reason", required=True)
    delete_ingested_parser.add_argument("--deleted-by", required=True, dest="deleted_by")
    delete_ingested_parser.add_argument(
        "--authorized-by",
        required=True,
        dest="authorized_by",
        help="required for every scope and classification: an authorized human reversing ingestion",
    )
    delete_ingested_parser.add_argument("--trigger", required=True, choices=INGESTED_DELETION_TRIGGERS)
    delete_ingested_parser.add_argument(
        "--source",
        help=(
            "narrows the match to this source; required at the shared global-fallback config "
            "tier, mirroring ingest's own --source requirement there"
        ),
    )
    delete_ingested_parser.add_argument("--dry-run", dest="dry_run", action="store_true")
    add_config(delete_ingested_parser)

    # Staged knowledge records. These operate on the staging table, not on
    # ingested content: `propose` stages a candidate, and only a steward's
    # later disposition (and a separate ingest) puts anything in front of
    # retrieval.
    propose = subparsers.add_parser("propose")
    propose_input = propose.add_mutually_exclusive_group(required=True)
    propose_input.add_argument(
        "--input", help="a fully-authored record file (frontmatter + body), or - for stdin"
    )
    propose_input.add_argument(
        "--from-finding",
        dest="from_finding",
        help=(
            "a JSON file (or - for stdin) with the record's fields; id, content_digest, and "
            "status are generated -- see finding_record.FINDING_KEYS for the required keys"
        ),
    )
    propose.add_argument(
        "--render-only",
        dest="render_only",
        action="store_true",
        help=(
            "validate and print the record that would be staged, without writing it -- review "
            "before proposing, or preview what --from-finding would generate"
        ),
    )
    add_config(propose)
    list_staged = subparsers.add_parser("list-staged")
    list_staged.add_argument("--status", choices=STAGED_STATUSES)
    add_config(list_staged)
    show_staged = subparsers.add_parser("show-staged")
    show_staged.add_argument("--id", required=True, dest="record_id")
    add_config(show_staged)
    import_staged = subparsers.add_parser("import-staged")
    import_staged.add_argument("--directory", required=True)
    add_config(import_staged)
    disposition = subparsers.add_parser("disposition-staged")
    disposition.add_argument("--id", required=True, dest="record_id")
    disposition.add_argument("--action", required=True, choices=("accepted", "rejected", "deferred"))
    disposition.add_argument("--reason", required=True)
    disposition.add_argument("--classification-used", required=True, dest="classification_used")
    disposition.add_argument("--decided-by", required=True, dest="decided_by")
    disposition.add_argument(
        "--diverged-from-proposal",
        action="store_true",
        help="the classification actually applied differs from the one proposed",
    )
    add_config(disposition)
    ingest_accepted_parser = subparsers.add_parser(
        "ingest-accepted",
        help="make steward-accepted staged records retrievable (G-7)",
    )
    ingest_accepted_parser.add_argument(
        "--id",
        action="append",
        dest="record_ids",
        help="ingest only this record; repeatable. Omit to ingest every accepted record.",
    )
    ingest_accepted_parser.add_argument(
        "--dry-run",
        action="store_true",
        help="report what would be ingested and refused, without writing",
    )
    add_config(ingest_accepted_parser)
    delete_staged = subparsers.add_parser("delete-staged")
    delete_staged.add_argument("--id", required=True, dest="record_id")
    delete_staged.add_argument("--reason", required=True)
    delete_staged.add_argument("--deleted-by", required=True, dest="deleted_by")
    delete_staged.add_argument(
        "--authorized-by",
        dest="authorized_by",
        help="required to delete an accepted record: the authorized human reversing the decision",
    )
    add_config(delete_staged)
    deletion_log = subparsers.add_parser("deletion-evidence")
    deletion_log.add_argument("--source", help="scope evidence to one project's deletions")
    deletion_log.add_argument(
        "--all-sources",
        dest="all_sources",
        action="store_true",
        help="explicitly opt into reading every project's deletion evidence",
    )
    add_config(deletion_log)
    export_staged = subparsers.add_parser("export-staged")
    export_staged.add_argument("--output", required=True)
    export_staged.add_argument("--status", choices=STAGED_STATUSES)
    export_staged.add_argument(
        "--check",
        action="store_true",
        help=(
            "compare the store against --output and report drift, writing nothing. Local-only: "
            "the store is gitignored and machine-local, so CI has no store to compare against and "
            "cannot run this meaningfully -- only an operator's own machine can"
        ),
    )
    add_config(export_staged)
    return parser


def _enforce_retrieval_scope(tier: str, options: dict[str, Any]) -> None:
    """Gate `search`/`context` at the shared global-fallback tier only (KS-FR-4..9).

    Project-local and explicit-`--config` tiers already isolate by database
    or explicit caller choice, so no new requirement is imposed on them
    (KS-FR-7). This check runs before any embedding call or database query
    (KS-NFR-3).
    """
    if tier != TIER_GLOBAL_FALLBACK:
        return
    sources = options.get("sources")
    all_sources = options.get("all_sources")
    if sources and all_sources:
        raise ValueError(
            "Ambiguous scope: pass either --source <project-identifier> (repeatable) or "
            "--all-sources against the shared global knowledge store, not both."
        )
    if not sources and not all_sources:
        raise ValueError(
            "A project scope is required against the shared global knowledge store: "
            "pass --source <project-identifier> to scope this query, or --all-sources "
            "to explicitly opt into cross-project retrieval."
        )


def _read_source(source: str) -> str:
    return sys.stdin.read() if source == "-" else Path(source).read_text(encoding="utf-8")


def _render_result(frontmatter: dict[str, Any], body: str) -> dict[str, Any]:
    """Validate and format a not-yet-staged record for `--render-only`.

    Runs the real validator (not a preview-only approximation) so a
    render-only failure names the same problem `propose` would have refused
    on. The rendered text is exactly what `put_record_text`/`propose --input -`
    would accept, so a reviewer can pipe it straight through once satisfied.
    """
    findings = validate_parsed(frontmatter, body)
    if findings:
        raise ValueError("record does not satisfy the contract: " + "; ".join(findings))
    return {
        "status": "rendered",
        "id": frontmatter.get("id"),
        "content_digest": frontmatter.get("content_digest"),
        "text": serialize_record(frontmatter, body),
        "note": (
            "Not staged: --render-only was passed. Review this, then re-run the same command "
            "without --render-only (or pipe --text into `propose --input -`) to stage it."
        ),
    }


def _propose(db: Any, options: dict[str, Any]) -> dict[str, Any]:
    """Stage one record, from a fully-authored file or from a structured finding.

    Two input shapes, one write path. `--input` is the original, unchanged:
    a complete record (frontmatter + body) is read and validated as-is inside
    `put_record_text`, so a malformed record never reaches the table.

    `--from-finding` is the low-friction path this exists to add: a JSON
    mapping with the record's fields (see `finding_record.FINDING_KEYS`) is
    turned into a full record by `finding_record.build_record`, which
    generates `id`, `content_digest`, and `status` rather than asking the
    caller to hand-compute a sha256 or memorise the id pattern. It still goes
    through the *same* contract validator before anything is written, and the
    write itself goes through `put_generated_record`, which refuses (rather
    than silently overwrites) a generated id that collides with different
    existing content.

    `--render-only` short-circuits either path before the write: the record
    is built and validated, but never staged.
    """
    render_only = options.pop("render_only", False)
    from_finding = options.pop("from_finding", None)
    input_source = options.pop("input", None)

    if from_finding is not None:
        raw = _read_source(from_finding)
        try:
            finding = json.loads(raw)
        except json.JSONDecodeError as error:
            raise ValueError(f"--from-finding did not contain valid JSON: {error}") from error
        try:
            frontmatter, body = build_record(finding)
        except FindingError as error:
            raise ValueError(str(error)) from error
        if render_only:
            return _render_result(frontmatter, body)
        return put_generated_record(db, frontmatter, body)

    if render_only:
        frontmatter, body = parse_record(_read_source(input_source))
        return _render_result(frontmatter, body)

    text = _read_source(input_source)
    record_id = put_record_text(db, text)
    stored = list_records(db, None)
    summary = next(record for record in stored if record["id"] == record_id)
    return {
        "status": "staged",
        "id": record_id,
        "record_status": summary["status"],
        "content_digest": summary["content_digest"],
        "note": (
            "Staged for knowledge-store-steward disposition. Staging is not ingestion: "
            "nothing is retrievable until a steward accepts this record and it is ingested."
        ),
    }


def _show_staged(db: Any, record_id: str) -> dict[str, Any]:
    """Return one staged record in full.

    `list-staged` gives summaries, but a database row cannot be read in a diff
    the way a committed file could, so the full text has to be reachable or the
    corpus becomes invisible the moment it leaves git.
    """
    loaded = get_record(db, record_id)
    if loaded is None:
        raise ValueError(f"No staged record with id {record_id!r} in this store.")
    frontmatter, body = loaded
    return {
        "id": record_id,
        "frontmatter": frontmatter,
        "body": body,
        "text": serialize_record(frontmatter, body),
        "disposition_history": get_history(db, record_id),
    }


def _import_staged(db: Any, directory: str) -> dict[str, Any]:
    """Stage every record file in a directory, atomically across the batch.

    Migration is the intended use, so a partial import is the wrong outcome:
    a batch that half-succeeds leaves the operator unable to tell which
    records made it without diffing. Every file is validated first, and the
    batch is written only if all of them pass.
    """
    root = Path(directory)
    if not root.is_dir():
        raise ValueError(f"Not a directory: {directory}")
    sources = sorted(root.glob("*.md"))
    if not sources:
        raise ValueError(f"No .md staged-record files found in {directory}")
    parsed = []
    for path in sources:
        frontmatter, body = parse_record(path.read_text(encoding="utf-8"))
        findings = validate_parsed(frontmatter, body)
        if findings:
            raise ValueError(f"{path.name}: " + "; ".join(findings))
        parsed.append((path, frontmatter, body))
    imported = [put_record(db, frontmatter, body) for _, frontmatter, body in parsed]
    return {"status": "imported", "count": len(imported), "ids": sorted(imported)}


def _export_staged(db: Any, output: str, status: str | None) -> dict[str, Any]:
    """Write every stored record out as `<id>.md`.

    Filenames are the record id, not whatever the file was called before it
    was staged: the id is the durable identity, and two records could
    otherwise collide on a filename that means nothing to the contract. The
    verification diff is therefore by id and content, never by filename.
    """
    destination = Path(output)
    destination.mkdir(parents=True, exist_ok=True)
    exported = export_records(db, status)
    histories = 0
    for record_id, text in exported.items():
        (destination / f"{record_id}.md").write_text(text, encoding="utf-8")
        # The record carries its *current* disposition in frontmatter. Earlier
        # ones cannot go there -- the frontmatter dialect is deliberately one
        # level deep and holds no list of mappings -- so they are written
        # beside it. Without this the export would silently lose the audit
        # trail, which is exactly what the durability path exists to protect.
        history = get_history(db, record_id)
        if history:
            histories += 1
            (destination / f"{record_id}.history.json").write_text(
                json.dumps(history, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
            )
    return {
        "status": "exported",
        "count": len(exported),
        "histories": histories,
        "directory": str(destination),
        "ids": sorted(exported),
    }


def _check_staged_export(db: Any, output: str, status: str | None) -> dict[str, Any]:
    """Compare the store's records against a committed export snapshot, writing nothing.

    **Local-only signal.** `roster/knowledge-store/proposed-knowledge/` is a
    generated export of a store that `.gitignore` deliberately excludes from
    version control (`SECURITY.md`), so there is no store on a CI runner to
    compare against -- CI can validate that the committed snapshot is
    well-formed (`staged_records.py`'s own drift guard), but it cannot tell
    whether the snapshot still matches whatever the store currently holds.
    This command can only answer that question on the machine that holds the
    store, and only for that machine, at that moment. A clean result means
    "this store agrees with this snapshot right now", not "the snapshot is
    current" in any sense a build could rely on.

    Compares frontmatter+body text and disposition-history sidecars
    byte-for-byte against what `export-staged` (without `--check`) would
    write, so a clean check is exactly the guarantee a real export would have
    produced no diff. `--status` narrows which store records are compared,
    the same as it narrows a real export; comparing a status-filtered store
    against a snapshot exported without that filter will report the
    untouched records as `extra_in_snapshot`, which is a true statement about
    that mismatched comparison, not a bug -- keep `--status` consistent with
    how the snapshot being checked was produced.
    """
    destination = Path(output)
    exported = export_records(db, status)

    existing_records: dict[str, Path] = {}
    if destination.is_dir():
        for path in destination.glob("*.md"):
            if path.name == "README.md":
                continue
            existing_records[path.stem] = path

    missing_from_snapshot: list[str] = []
    stale_in_snapshot: list[str] = []
    for record_id, text in sorted(exported.items()):
        path = existing_records.pop(record_id, None)
        if path is None:
            missing_from_snapshot.append(record_id)
        elif path.read_text(encoding="utf-8") != text:
            stale_in_snapshot.append(record_id)
    extra_in_snapshot = sorted(existing_records)

    history_drift: list[str] = []
    for record_id in sorted(exported):
        history = get_history(db, record_id)
        sidecar = destination / f"{record_id}.history.json"
        if history:
            if not sidecar.is_file():
                history_drift.append(record_id)
            else:
                on_disk = json.loads(sidecar.read_text(encoding="utf-8"))
                if on_disk != history:
                    history_drift.append(record_id)
        elif sidecar.is_file():
            history_drift.append(record_id)

    clean = not (missing_from_snapshot or stale_in_snapshot or extra_in_snapshot or history_drift)
    return {
        "status": "checked",
        "clean": clean,
        "missing_from_snapshot": missing_from_snapshot,
        "stale_in_snapshot": stale_in_snapshot,
        "extra_in_snapshot": extra_in_snapshot,
        "history_drift": history_drift,
        "directory": str(destination),
        "note": (
            "Local-only signal: the store is gitignored and machine-local, so CI has no store to "
            "compare against and cannot run this meaningfully -- only an operator's own machine can. "
            "A clean result describes this machine's store right now, not a build guarantee."
        ),
    }


def _enforce_staging_scope(tier: str) -> None:
    """Refuse to stage records into the shared global-fallback store.

    The decision recorded in the proposal is that records are staged **per
    project**: a finding about this repository belongs in this repository's
    partition, not in a store shared with every other project on the machine.

    Enforced here rather than left to `--source` discipline, because
    `SECURITY.md` is explicit that caller flags are not authentication and that
    a project whose classification or tenancy cannot share infrastructure needs
    a real partition. A convention that nothing checks is the failure mode this
    contract was written to avoid.
    """
    if tier != TIER_GLOBAL_FALLBACK:
        return
    raise ValueError(
        "Staged knowledge records are per project, so they cannot be written to the "
        "shared global knowledge store. Create .agents/knowledge-store/config.json in "
        "this project (an empty {} is enough to claim a project-local partition), or "
        "pass --config pointing at the store this project owns."
    )


def _enforce_ingest_scope(tier: str, options: dict[str, Any]) -> None:
    """Gate `ingest` at the shared global-fallback tier only (KS-FR-10..12)."""
    if options.get("source"):
        return
    if tier == TIER_GLOBAL_FALLBACK:
        raise ValueError(
            "A project scope is required to ingest into the shared global knowledge "
            "store: pass --source <project-identifier> identifying the ingesting project."
        )
    options["source"] = "chat-export"


def _enforce_ingested_deletion_scope(tier: str, options: dict[str, Any]) -> None:
    """Gate `delete-ingested` at the shared global-fallback tier only, mirroring `_enforce_ingest_scope`.

    An explicit `--source` is required here regardless of `--scope`, not only
    for `--scope source`: a shared store holding every project's ingested
    content is exactly the setting where a steward deleting by conversation
    or message id, without also naming the source, is most likely to be
    correcting the wrong project's content by mistake.
    """
    if tier != TIER_GLOBAL_FALLBACK:
        return
    if not options.get("source"):
        raise ValueError(
            "A project scope is required to delete ingested content from the shared global "
            "knowledge store: pass --source <project-identifier> identifying the source."
        )


def _enforce_evidence_scope(tier: str, options: dict[str, Any]) -> None:
    """Gate `deletion-evidence` at the shared global-fallback tier only.

    Mirrors `_enforce_retrieval_scope` rather than `_enforce_staging_scope`:
    the answer at the shared tier is "say whose evidence you want", not
    "refused". Every other command that reads the shared store makes the
    caller name a scope; evidence rows carry the deleting project's
    identifier, its steward's free-text reason, and asserted
    `deleted_by`/`authorized_by` identities, so an unscoped read is a
    cross-project read and should have to say so.

    Project-local and explicit-`--config` tiers are already isolated by
    database, so no new requirement is imposed on them.
    """
    if tier != TIER_GLOBAL_FALLBACK:
        return
    source = options.get("source")
    all_sources = options.get("all_sources")
    if source and all_sources:
        raise ValueError(
            "Ambiguous scope: pass either --source <project-identifier> or "
            "--all-sources against the shared global knowledge store, not both."
        )
    if not source and not all_sources:
        raise ValueError(
            "A project scope is required to read deletion evidence from the shared global "
            "knowledge store: pass --source <project-identifier> to scope this read, or "
            "--all-sources to explicitly opt into cross-project evidence."
        )


def run(arguments: list[str] | None = None) -> dict[str, Any]:
    options = vars(_parser().parse_args(arguments))
    command = options.pop("command")
    config, tier = load_config(options.pop("config", None), return_tier=True)
    db = open_store(config["database"])
    try:
        if command == "init":
            return {"status": "initialized", "database": config["database"]}
        if command == "ingest":
            _enforce_ingest_scope(tier, options)
            options["classification"] = options.get("classification") or config["ingestion"]["default_classification"]
            return ingest_file(db, config, options)
        if command == "search":
            _enforce_retrieval_scope(tier, options)
            options.pop("all_sources", None)
            query = options.pop("query")
            results = search_store(db, config, query, options)
            return {"query_id": stable_query_id(query), "results": results}
        if command == "context":
            _enforce_retrieval_scope(tier, options)
            options.pop("all_sources", None)
            return build_agent_context(db, config, options.pop("query"), options)
        if command == "stats":
            return store_stats(db)
        if command == "retention-report":
            # Read-only, ungated at every tier -- like `stats`, it reports
            # metadata (source, classification, retention_until, counts),
            # never content, and deletes nothing.
            try:
                return build_retention_report(db, as_of=options.pop("as_of", None))
            except IngestedDeletionError as error:
                raise ValueError(str(error)) from error
        if command == "delete-ingested":
            _enforce_ingested_deletion_scope(tier, options)
            install_ingested_deletion_schema(db)
            try:
                return delete_ingested(
                    db,
                    scope=options.pop("scope"),
                    scope_key=options.pop("scope_key"),
                    reason=options.pop("reason"),
                    deleted_by=options.pop("deleted_by"),
                    authorized_by=options.pop("authorized_by"),
                    trigger=options.pop("trigger"),
                    source=options.pop("source", None),
                    dry_run=options.pop("dry_run", False),
                )
            except IngestedDeletionError as error:
                raise ValueError(str(error)) from error
        if command == "deletion-evidence":
            # Merges both evidence tables: staged-record deletions
            # (staged_store.py) and ingested-content deletions
            # (ingested_deletion.py) are two distinct capabilities with two
            # distinct schemas, so each entry carries "kind" to keep the
            # distinction visible in the merged output rather than blurring
            # it.
            #
            # Not gated by `_enforce_staging_scope` (which refuses outright at
            # the shared tier), because ingested content *can* be deleted from
            # the shared global-fallback store and hiding its evidence there
            # would make legitimate deletions invisible to the steward who
            # needs to see them. But "visible" is not "unscoped": a row carries
            # the deleting project's identifier, a free-text reason, and
            # asserted actor identities, so at the shared tier this reads like
            # `search`/`context` and takes the same explicit scope choice.
            _enforce_evidence_scope(tier, options)
            source = options.pop("source", None)
            options.pop("all_sources", None)
            install_schema(db)
            install_ingested_deletion_schema(db)
            # `--source` names an *ingest* source, and staged records have no
            # source to match -- so a source-scoped read returns
            # ingested-content evidence only, at every tier. In the shared
            # store there is nothing to omit (staged records cannot be written
            # there at all, per `_enforce_staging_scope`); in a project-local
            # store, ask without `--source` to see staged deletions too.
            # Returning them unfiltered under a scoped read would reopen the
            # gap this scoping closes.
            staged_evidence = (
                [] if source else [{"kind": "staged", **row} for row in deletion_evidence(db)]
            )
            ingested_evidence = [
                {"kind": "ingested", **row} for row in ingested_deletion_evidence(db, source=source)
            ]
            return {"deletions": staged_evidence + ingested_evidence}
        if command in (
            "propose",
            "list-staged",
            "show-staged",
            "import-staged",
            "export-staged",
            "disposition-staged",
            "delete-staged",
            "ingest-accepted",
        ):
            _enforce_staging_scope(tier)
            # Installed here rather than inside open_store: the staging table
            # is a staged-record concern, and database.py should not depend on
            # the module that implements the record contract. install_schema is
            # idempotent, so calling it per command is cheap and keeps an
            # existing store working with no migration step.
            install_schema(db)
            if command == "propose":
                return _propose(db, options)
            if command == "list-staged":
                return {"records": list_records(db, options.pop("status", None))}
            if command == "import-staged":
                return _import_staged(db, options.pop("directory"))
            if command == "export-staged":
                check = options.pop("check", False)
                output = options.pop("output")
                status = options.pop("status", None)
                if check:
                    return _check_staged_export(db, output, status)
                return _export_staged(db, output, status)
            if command == "delete-staged":
                return delete_record(
                    db,
                    options.pop("record_id"),
                    reason=options.pop("reason"),
                    deleted_by=options.pop("deleted_by"),
                    authorized_by=options.pop("authorized_by", None),
                )
            if command == "ingest-accepted":
                # The steward decision is already made and its
                # authorship/approval separation already enforced by
                # disposition_record. This executes that decision; it does not
                # take one, which is why it needs no --decided-by.
                return ingest_accepted(
                    db,
                    config,
                    record_ids=options.pop("record_ids", None),
                    dry_run=options.pop("dry_run", False),
                )
            if command == "disposition-staged":
                return disposition_record(
                    db,
                    options.pop("record_id"),
                    action=options.pop("action"),
                    reason=options.pop("reason"),
                    classification_used=options.pop("classification_used"),
                    diverged_from_proposal=options.pop("diverged_from_proposal"),
                    decided_by=options.pop("decided_by"),
                )
            return _show_staged(db, options.pop("record_id"))
        raise ValueError(f"Unknown command: {command}")
    finally:
        db.close()


def main() -> int:
    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure:
            reconfigure(encoding="utf-8", errors="strict", newline="\n")
    try:
        result = run()
        sys.stdout.write(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
        return 0
    except (OSError, ValueError, RuntimeError, json.JSONDecodeError, SettingsError) as error:
        sys.stderr.write(f"error: {error}\n")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
