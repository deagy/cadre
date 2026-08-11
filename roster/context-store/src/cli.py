#!/usr/bin/env python3
"""Command-line interface for the agent context store."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

from config import SCOPES, TIER_GLOBAL_FALLBACK, load_config
from database import expired_rows, open_store, store_stats, sweep_expired
from export import ExportError
from service import (
    RECOMMENDED_ACTIONS as PROMOTE_ACTIONS,
    ContextStoreError,
    drop_entry,
    export_entries,
    get_entry,
    list_entries,
    promote_entry,
    put_entry,
    reindex_entries,
    search_entries,
)
from settings import SettingsError


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="cli.py", description="Local agent context store")
    subparsers = parser.add_subparsers(dest="command", required=True)

    def add_config(command: argparse.ArgumentParser) -> None:
        command.add_argument("--config")

    def add_caller(command: argparse.ArgumentParser) -> None:
        """Identity every read and write is attributed by, in `access_runs`.

        Required for the same reason `cadre knowledge context` requires them:
        an unattributable read of agent working material is exactly the read
        that cannot be reviewed afterward.
        """
        command.add_argument("--agent", required=True)
        command.add_argument("--task-id", required=True, dest="task_id")
        command.add_argument("--classification", required=True)
        command.add_argument("--source")

    init = subparsers.add_parser("init")
    add_config(init)

    put = subparsers.add_parser("put")
    put.add_argument("--label", required=True)
    put.add_argument("--input", help="file to read, or '-' for stdin (default: stdin)")
    put.add_argument("--scope", default="agent", choices=SCOPES)
    put.add_argument("--dispatch-id", dest="dispatch_id")
    put.add_argument("--tag", action="append", dest="tags", default=[])
    put.add_argument("--derived-from", action="append", dest="derived_from", default=[])
    put.add_argument("--ttl-days", dest="ttl_days", type=int)
    add_caller(put)
    add_config(put)

    get = subparsers.add_parser("get")
    get.add_argument("--handle", required=True)
    get.add_argument("--dispatch-id", dest="dispatch_id")
    add_caller(get)
    add_config(get)

    listing = subparsers.add_parser("list")
    listing.add_argument("--scope", choices=SCOPES)
    # Two distinct meanings, deliberately two flags. `--dispatch-id` is the
    # caller's own dispatch identity, used to decide whether a dispatch-scoped
    # entry is readable at all; `--filter-dispatch-id` narrows the results to
    # one dispatch. Collapsing them -- as this parser originally did -- means a
    # caller that supplies its identity in order to read a peer's entry
    # silently loses every agent-scoped entry of its own, because those carry
    # no dispatch id to match.
    listing.add_argument("--dispatch-id", dest="dispatch_id")
    listing.add_argument("--filter-dispatch-id", dest="filter_dispatch_id")
    listing.add_argument("--filter-agent", dest="filter_agent")
    listing.add_argument("--filter-task-id", dest="filter_task_id")
    listing.add_argument("--tag", action="append", dest="tags", default=[])
    listing.add_argument("--top")
    add_caller(listing)
    add_config(listing)

    search = subparsers.add_parser("search")
    search.add_argument("--query", required=True)
    search.add_argument("--scope", choices=SCOPES)
    search.add_argument("--dispatch-id", dest="dispatch_id")
    search.add_argument("--top")
    add_caller(search)
    add_config(search)

    reindex = subparsers.add_parser("reindex")
    reindex.add_argument(
        "--force",
        action="store_true",
        help="rebuild every entry, not only those with no vectors under the current settings",
    )
    add_config(reindex)

    export = subparsers.add_parser("export")
    export.add_argument("--output", required=True)
    export.add_argument("--handle", action="append", dest="handles", default=[])
    export.add_argument("--scope", choices=SCOPES)
    export.add_argument("--dispatch-id", dest="dispatch_id")
    export.add_argument("--filter-dispatch-id", dest="filter_dispatch_id")
    export.add_argument(
        "--acknowledge-commit",
        dest="acknowledge_commit",
        action="store_true",
        help="required for confidential entries: the destination is normally committed and cloneable",
    )
    export.add_argument(
        "--include-untrusted",
        dest="include_untrusted",
        action="store_true",
        help="required to export entries flagged untrusted_inputs; exported copies carry a banner",
    )
    add_caller(export)
    add_config(export)

    promote = subparsers.add_parser("promote")
    promote.add_argument("--handle", required=True)
    promote.add_argument("--artifact", required=True, help="what the finding is about (repo-relative, never an absolute local path)")
    promote.add_argument("--revision", required=True, help="the source revision the observation was made against")
    promote.add_argument("--sensitivity-notes", required=True, dest="sensitivity_notes")
    promote.add_argument("--conflicts-or-staleness", required=True, dest="conflicts_or_staleness")
    promote.add_argument(
        "--recommended-action", required=True, dest="recommended_action", choices=PROMOTE_ACTIONS
    )
    promote.add_argument(
        "--finding-only",
        dest="finding_only",
        action="store_true",
        help="print just the finding object, ready to pipe into `cadre knowledge propose --from-finding -`",
    )
    add_caller(promote)
    add_config(promote)

    drop = subparsers.add_parser("drop")
    drop.add_argument("--handle", required=True)
    drop.add_argument("--reason", required=True)
    add_config(drop)

    expire = subparsers.add_parser("expire")
    expire.add_argument("--as-of", dest="as_of")
    expire.add_argument("--dry-run", dest="dry_run", action="store_true")
    add_config(expire)

    stats = subparsers.add_parser("stats")
    add_config(stats)

    return parser


def _enforce_scope(tier: str, source: str | None) -> None:
    """Require an explicit project scope against the shared global store.

    Deliberately stricter than the knowledge store, which offers `--all-sources`
    as an explicit opt-in to cross-project retrieval. There is no such flag
    here and no plan to add one: cross-project retrieval of curated, steward-
    dispositioned knowledge has a defensible use case, while cross-project
    retrieval of another project's unreviewed agent working notes does not, and
    it would be the widest laundering channel the design could offer.

    A reader who knows the sibling store will expect the flag, so its absence
    is documented rather than left to be discovered.
    """
    if tier != TIER_GLOBAL_FALLBACK:
        return
    if not source:
        raise ContextStoreError(
            "A project scope is required against the shared global context store: pass "
            "--source <project-identifier>. Unlike `cadre knowledge`, there is no "
            "--all-sources equivalent -- cross-project reads of unreviewed agent working "
            "notes are not an offered mode. Use a project-local "
            ".agents/context-store/config.json for a real partition."
        )


def _read_input(source: str | None) -> str:
    if source is None or source == "-":
        return sys.stdin.read()
    return Path(source).read_text(encoding="utf-8")


def _emit(payload: Any) -> int:
    print(json.dumps(payload, indent=2, ensure_ascii=False))
    return 0


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        config, tier = load_config(args.config, return_tier=True)

        if args.command == "init":
            db = open_store(config["database"])
            db.close()
            return _emit({"initialized": config["database"], "tier": tier})

        if args.command == "stats":
            db = open_store(config["database"])
            try:
                return _emit({"database": config["database"], "tier": tier, **store_stats(db)})
            finally:
                db.close()

        if args.command == "expire":
            # --dry-run must not sweep on open, or the report would describe
            # entries the act of asking had already destroyed.
            db = open_store(config["database"], sweep=not args.dry_run)
            try:
                if args.dry_run:
                    rows = expired_rows(db, args.as_of)
                    return _emit({
                        "dry_run": True,
                        "as_of": args.as_of,
                        "expired": [
                            {
                                "handle": row["handle"],
                                "classification": row["classification"],
                                "byte_length": row["byte_length"],
                                "expires_at": row["expires_at"],
                            }
                            for row in rows
                        ],
                        "count": len(rows),
                    })
                swept = sweep_expired(db, args.as_of)
                return _emit({"dry_run": False, "as_of": args.as_of, "swept": swept, "count": len(swept)})
            finally:
                db.close()

        if args.command == "drop":
            db = open_store(config["database"])
            try:
                return _emit(drop_entry(db, {"handle": args.handle, "reason": args.reason}))
            finally:
                db.close()

        if args.command == "reindex":
            db = open_store(config["database"])
            try:
                return _emit(reindex_entries(db, config, force=args.force))
            finally:
                db.close()

        _enforce_scope(tier, args.source)
        source = args.source or "local"
        db = open_store(config["database"])
        try:
            if args.command == "put":
                return _emit(put_entry(db, config, {
                    "label": args.label,
                    "content": _read_input(args.input),
                    "scope": args.scope,
                    "dispatch_id": args.dispatch_id,
                    "tags": args.tags,
                    "derived_from": args.derived_from,
                    "ttl_days": args.ttl_days,
                    "agent": args.agent,
                    "task_id": args.task_id,
                    "classification": args.classification,
                    "source": source,
                }))
            if args.command == "get":
                return _emit(get_entry(db, {
                    "handle": args.handle,
                    "dispatch_id": args.dispatch_id,
                    "agent": args.agent,
                    "task_id": args.task_id,
                    "classification": args.classification,
                    "source": source,
                }))
            if args.command == "export":
                return _emit(export_entries(db, {
                    "output": args.output,
                    "handles": args.handles,
                    "scope": args.scope,
                    "dispatch_id": args.dispatch_id,
                    "filter_dispatch_id": args.filter_dispatch_id,
                    "acknowledge_commit": args.acknowledge_commit,
                    "include_untrusted": args.include_untrusted,
                    "agent": args.agent,
                    "task_id": args.task_id,
                    "classification": args.classification,
                    "source": source,
                }))
            if args.command == "promote":
                result = promote_entry(db, {
                    "handle": args.handle,
                    "artifact": args.artifact,
                    "revision": args.revision,
                    "sensitivity_notes": args.sensitivity_notes,
                    "conflicts_or_staleness": args.conflicts_or_staleness,
                    "recommended_action": args.recommended_action,
                    "agent": args.agent,
                    "task_id": args.task_id,
                    "classification": args.classification,
                    "source": source,
                })
                if result["untrusted_instruction_risk"]:
                    print(
                        "cadre context: this entry derives from material that tripped injection "
                        "detection, so the proposal carries untrusted_instruction_risk=true. The "
                        "knowledge store's staged-record contract will defer it automatically; "
                        "that is the intended path, not a failure.",
                        file=sys.stderr,
                    )
                # `--finding-only` prints exactly what `propose --from-finding -`
                # consumes, so the pipe needs no intermediate `jq`.
                return _emit(result["finding"] if args.finding_only else result)
            if args.command == "search":
                return _emit(search_entries(db, config, args.query, {
                    "scope": args.scope,
                    "dispatch_id": args.dispatch_id,
                    "top": args.top,
                    "agent": args.agent,
                    "task_id": args.task_id,
                    "classification": args.classification,
                    "source": source,
                }))
            if args.command == "list":
                return _emit(list_entries(db, {
                    "scope": args.scope,
                    "dispatch_id": args.dispatch_id,
                    "filter_dispatch_id": args.filter_dispatch_id,
                    "filter_agent": args.filter_agent,
                    "filter_task_id": args.filter_task_id,
                    "tags": args.tags,
                    "top": args.top,
                    "agent": args.agent,
                    "task_id": args.task_id,
                    "classification": args.classification,
                    "source": source,
                }))
        finally:
            db.close()

        raise ContextStoreError(f"Unknown command: {args.command}")
    except (ContextStoreError, ExportError, SettingsError, FileNotFoundError, ValueError, OSError, json.JSONDecodeError) as error:
        print(f"cadre context: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
