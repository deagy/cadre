#!/usr/bin/env python3
"""CLI entrypoint for `gitlab_core.py`'s three GitLab evidence functions.

Exists for callers that cannot speak MCP directly (stdio JSON-RPC) but can
invoke a subprocess and read a line of JSON from stdout -- e.g. Cline's
`@cline/sdk` native-tool contributions, which run in a sandboxed worker with
no MCP client of its own. `gitlab_server.py` remains the MCP-transport
adapter for MCP-capable callers (Claude Code, Codex); this module is a
second, MCP-independent adapter over the exact same `gitlab_core.py` safety
core -- no GitLab HTTP logic, validation, quick-action neutralization,
confirmation gating, or audit logging is duplicated here. Every argument this
module accepts is handed to `gitlab_core` unchanged; every behavior
(idempotency, size caps, the write_wiki_page confirmation round-trip,
create-only invariant) is `gitlab_core`'s, not reimplemented here.

Each subcommand prints exactly one line of JSON -- the same result dict
`gitlab_core` returns -- to stdout and exits 0, whether that result is
`status="ok"`, `"confirmation_required"`, `"denied"`, or `"unavailable"`. A
non-JSON, nonzero-exit outcome only ever means this CLI's own argument
parsing failed or an unexpected exception escaped `gitlab_core` (a bug, not
an expected GitLab-evidence outcome) -- callers should treat "exit 0, parse
stdout as JSON, branch on status" as the normal path and reserve nonzero-exit
handling for that unexpected case.

    python3 gitlab_cli.py create-review-subtask --parent-issue-iid 5 \\
        --title "..." --description "..." --gate-id G5 --task-id TASK-42
    python3 gitlab_cli.py write-wiki-page --slug foo --title "..." \\
        --content "..." [--format markdown] [--confirmation-token ...]
    python3 gitlab_cli.py write-evidence-comment --issue-iid 5 \\
        --content "..." --task-id TASK-42

Also runnable as `cadre gitlab-evidence <subcommand> [args...]` once wired
into `bin/subcommands.tsv`.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

_MODULE_DIR = Path(__file__).resolve().parent
if str(_MODULE_DIR) not in sys.path:
    sys.path.insert(0, str(_MODULE_DIR))

import gitlab_core as core  # noqa: E402  (sys.path set above)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="cadre gitlab-evidence",
        description="GitLab evidence CLI: create-review-subtask / write-wiki-page / write-evidence-comment",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    create_review_subtask = subparsers.add_parser(
        "create-review-subtask", help="Create (or return an existing) review-subtask issue"
    )
    create_review_subtask.add_argument("--parent-issue-iid", type=int, required=True)
    create_review_subtask.add_argument("--title", required=True)
    create_review_subtask.add_argument("--description", required=True)
    create_review_subtask.add_argument("--gate-id", required=True)
    create_review_subtask.add_argument("--task-id", required=True)

    write_wiki_page = subparsers.add_parser(
        "write-wiki-page", help="Create or update a wiki page (requires a confirmation round-trip)"
    )
    write_wiki_page.add_argument("--slug", required=True)
    write_wiki_page.add_argument("--title", required=True)
    write_wiki_page.add_argument("--content", required=True)
    write_wiki_page.add_argument("--format", default="markdown", choices=["markdown", "rdoc", "asciidoc", "org"])
    write_wiki_page.add_argument("--confirmation-token", default=None)

    write_evidence_comment = subparsers.add_parser(
        "write-evidence-comment", help="Add an evidence comment to an existing issue"
    )
    write_evidence_comment.add_argument("--issue-iid", type=int, required=True)
    write_evidence_comment.add_argument("--content", required=True)
    write_evidence_comment.add_argument("--task-id", required=True)

    return parser


def dispatch(args: argparse.Namespace) -> dict[str, Any]:
    if args.command == "create-review-subtask":
        return core.create_review_subtask(
            args.parent_issue_iid, args.title, args.description, args.gate_id, args.task_id
        )
    if args.command == "write-wiki-page":
        return core.write_wiki_page(
            args.slug, args.title, args.content, args.format, args.confirmation_token
        )
    if args.command == "write-evidence-comment":
        return core.write_evidence_comment(args.issue_iid, args.content, args.task_id)
    raise AssertionError(f"unreachable: unknown command {args.command!r}")  # argparse enforces `command`


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    result = dispatch(args)
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
