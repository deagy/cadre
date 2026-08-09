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
from service import build_agent_context, ingest_file, search_store, stable_query_id
from settings import SettingsError
from staged_records import STATUS_VALUES as STAGED_STATUSES
from staged_records import parse_record, validate_parsed
from staged_store import (
    delete_record,
    deletion_evidence,
    disposition_record,
    export_records,
    get_history,
    get_record,
    install_schema,
    list_records,
    put_record,
    put_record_text,
    serialize_record,
)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="cli.py", description="Local agent knowledge store")
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
    add_config(ingest)
    search = subparsers.add_parser("search")
    search.add_argument("--query", required=True)
    search.add_argument("--classification", required=True)
    search.add_argument("--top")
    search.add_argument("--source")
    search.add_argument("--all-sources", action="store_true")
    add_config(search)
    context = subparsers.add_parser("context")
    context.add_argument("--agent", required=True)
    context.add_argument("--task-id", required=True, dest="task_id")
    context.add_argument("--query", required=True)
    context.add_argument("--classification", required=True)
    context.add_argument("--top")
    context.add_argument("--source")
    context.add_argument("--all-sources", action="store_true")
    add_config(context)
    stats = subparsers.add_parser("stats")
    add_config(stats)

    # Staged knowledge records. These operate on the staging table, not on
    # ingested content: `propose` stages a candidate, and only a steward's
    # later disposition (and a separate ingest) puts anything in front of
    # retrieval.
    propose = subparsers.add_parser("propose")
    propose.add_argument("--input", required=True, help="record file, or - for stdin")
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
    add_config(deletion_log)
    export_staged = subparsers.add_parser("export-staged")
    export_staged.add_argument("--output", required=True)
    export_staged.add_argument("--status", choices=STAGED_STATUSES)
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
    source = options.get("source")
    all_sources = options.get("all_sources")
    if source and all_sources:
        raise ValueError(
            "Ambiguous scope: pass either --source <project-identifier> or "
            "--all-sources against the shared global knowledge store, not both."
        )
    if not source and not all_sources:
        raise ValueError(
            "A project scope is required against the shared global knowledge store: "
            "pass --source <project-identifier> to scope this query, or --all-sources "
            "to explicitly opt into cross-project retrieval."
        )


def _propose(db: Any, source: str) -> dict[str, Any]:
    """Stage one record read from a file or stdin.

    Validation happens inside `put_record_text` before the write, so a
    malformed record never reaches the table -- the failure is "it was never
    staged", not "something invalid is staged and a checker will catch it".
    """
    text = sys.stdin.read() if source == "-" else Path(source).read_text(encoding="utf-8")
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
        if command in (
            "propose",
            "list-staged",
            "show-staged",
            "import-staged",
            "export-staged",
            "disposition-staged",
            "delete-staged",
            "deletion-evidence",
        ):
            _enforce_staging_scope(tier)
            # Installed here rather than inside open_store: the staging table
            # is a staged-record concern, and database.py should not depend on
            # the module that implements the record contract. install_schema is
            # idempotent, so calling it per command is cheap and keeps an
            # existing store working with no migration step.
            install_schema(db)
            if command == "propose":
                return _propose(db, options.pop("input"))
            if command == "list-staged":
                return {"records": list_records(db, options.pop("status", None))}
            if command == "import-staged":
                return _import_staged(db, options.pop("directory"))
            if command == "export-staged":
                return _export_staged(db, options.pop("output"), options.pop("status", None))
            if command == "delete-staged":
                return delete_record(
                    db,
                    options.pop("record_id"),
                    reason=options.pop("reason"),
                    deleted_by=options.pop("deleted_by"),
                    authorized_by=options.pop("authorized_by", None),
                )
            if command == "deletion-evidence":
                return {"deletions": deletion_evidence(db)}
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
