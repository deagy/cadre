"""Tests for enforced knowledge-store scope (idea #9, KS-FR-1..18, AC-1..15).

Covers the global-fallback-tier-only enforcement added to `search`/`context`
(require `--source` or `--all-sources`, mutually exclusive) and `ingest`
(require explicit `--source`), while confirming project-local and explicit
`--config` tiers, and `cadre select`'s own call path, are unaffected.
"""

from __future__ import annotations

import argparse
import json
import os
import sqlite3
import sys
import tempfile
import unittest
import uuid
import yaml
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest import mock


def tzinfo_offset(hours: int) -> timezone:
    return timezone(timedelta(hours=hours))


ROOT = Path(__file__).resolve().parent.parent
REPO_ROOT = ROOT.parent.parent
sys.path.insert(0, str(ROOT / "src"))

from cli import run  # noqa: E402
from config import (  # noqa: E402
    TIER_EXPLICIT_CONFIG,
    TIER_GLOBAL_FALLBACK,
    TIER_PROJECT_LOCAL,
    load_config,
)


class ScopeEnforcementTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="knowledge-store-scope-")
        self.directory = Path(self.temporary.name)

    def tearDown(self) -> None:
        self.temporary.cleanup()

    # -- fixture helpers -------------------------------------------------

    def _global_fallback_cwd(self) -> Path:
        """A cwd with no project-local config anywhere in its ancestry."""
        cwd = self.directory / "no-project-local"
        cwd.mkdir()
        return cwd

    def _global_home(self) -> Path:
        home = self.directory / "global-home"
        home.mkdir()
        return home

    def _global_fallback_env(self):
        """Context manager patching cwd + KNOWLEDGE_STORE_HOME to resolve to the global-fallback tier."""
        home = self._global_home()
        cwd = self._global_fallback_cwd()
        return (
            mock.patch.dict(os.environ, {"KNOWLEDGE_STORE_HOME": str(home)}),
            mock.patch("config.Path.cwd", return_value=cwd),
        )

    def _project_local_env(self):
        """Context manager patching cwd + KNOWLEDGE_STORE_HOME to resolve to a project-local config."""
        project = self.directory / "project"
        project.mkdir()
        (project / ".git").mkdir()
        local_store = project / ".agents" / "knowledge-store"
        local_store.mkdir(parents=True)
        (local_store / "config.json").write_text(
            json.dumps({"database": "project.db", "embedding": {"dimensions": 32}}),
            encoding="utf-8",
        )
        home = self.directory / "unused-global-home"
        home.mkdir()
        return (
            mock.patch.dict(os.environ, {"KNOWLEDGE_STORE_HOME": str(home)}),
            mock.patch("config.Path.cwd", return_value=project),
        )

    def _run(self, args: list[str]) -> dict:
        return run(args)

    # -- AC-1 / AC-8: project-local backward compatibility ---------------

    def test_ac1_ac8_project_local_search_context_ingest_unaffected(self) -> None:
        env_patch, cwd_patch = self._project_local_env()
        with env_patch, cwd_patch:
            init_result = self._run(["init"])
            self.assertEqual("initialized", init_result["status"])

            ingest_result = self._run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--classification", "internal",
            ])
            self.assertIn("run_id", ingest_result)

            # Item 4's specific claim -- the restored "chat-export" default
            # is genuinely faithful, not just code-inspection-plausible --
            # verified by querying the actually-stored citation source,
            # not just that ingest returned a run_id.
            default_source_result = self._run([
                "search", "--query", "production release approval",
                "--classification", "internal", "--source", "chat-export",
            ])
            self.assertTrue(default_source_result["results"])
            self.assertTrue(
                all(item["citation"]["source"] == "chat-export" for item in default_source_result["results"])
            )

            search_result = self._run([
                "search", "--query", "production release approval",
                "--classification", "internal",
            ])
            self.assertIn("results", search_result)

            context_result = self._run([
                "context", "--agent", "release-engineer", "--task-id", "REL-1",
                "--query", "production release approval", "--classification", "internal",
            ])
            self.assertIsNone(context_result["source_filter"])

    # -- AC-2: cadre select backward compatibility ------------------------

    def test_ac2_cadre_select_context_path_always_supplies_source(self) -> None:
        select_src = REPO_ROOT / "roster" / "orchestration" / "src"
        sys.path.insert(0, str(select_src))
        import importlib

        select_agents = importlib.import_module("select_agents")
        build_dispatch_plan = importlib.import_module("build_dispatch_plan")

        source = select_agents.resolve_knowledge_source(REPO_ROOT)
        self.assertTrue(source)

        catalog = select_agents.load_catalog(select_agents.ROSTER_ROOT / "catalog.yaml")
        # Mirrors select_agents' own load path, which resolves a project-local
        # routing overlay before dispatch (#202) rather than reading the base
        # file directly. REPO_ROOT has no overlay, so this is the base config.
        config, _overlay = select_agents.resolve_effective_routing(
            select_agents.ORCHESTRATION_ROOT / "routing.json", start=REPO_ROOT
        )
        select_agents.validate_routing_config(config)
        plan = build_dispatch_plan.build_dispatch_plan(
            config,
            catalog,
            {
                "task": "Fix a bug in the login form",
                "task_id": "TASK-SCOPE-1",
                "repository_root": str(REPO_ROOT),
                "base": None,
                "changed_files": ["frontend/login.tsx"],
                "changed_file_source": "explicit",
                "classification": "internal",
                "source": source,
                "top": 5,
            },
            require_sdlc=False,
        )
        knowledge = plan.get("knowledge_context")
        self.assertIsNotNone(knowledge)
        self.assertEqual(source, knowledge["source_filter"])
        for request in knowledge["requests"]:
            args = request["invocation"]["args"]
            self.assertIn("--source", args)
            supplied_source = args[args.index("--source") + 1]
            self.assertTrue(supplied_source)

    # -- AC-3: core enforcement, unscoped shared-store retrieval rejected --

    def test_ac3_global_fallback_search_context_reject_when_unscoped(self) -> None:
        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            with self.assertRaises(ValueError) as captured:
                self._run(["search", "--query", "x", "--classification", "internal"])
            message = str(captured.exception)
            self.assertIn("--source", message)
            self.assertIn("--all-sources", message)

            with self.assertRaises(ValueError) as captured:
                self._run([
                    "context", "--agent", "release-engineer", "--task-id", "REL-2",
                    "--query", "x", "--classification", "internal",
                ])
            message = str(captured.exception)
            self.assertIn("--source", message)
            self.assertIn("--all-sources", message)

    # -- AC-4: scoped retrieval unaffected ---------------------------------

    def test_ac4_global_fallback_explicit_source_succeeds(self) -> None:
        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            self._run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--source", "proj-a", "--classification", "internal",
            ])
            result = self._run([
                "search", "--query", "production release approval",
                "--classification", "internal", "--source", "proj-a",
            ])
            self.assertTrue(result["results"])
            self.assertTrue(all(item["citation"]["source"] == "proj-a" for item in result["results"]))

    # -- AC-5: explicit cross-project opt-in preserved ---------------------

    def test_ac5_global_fallback_all_sources_spans_multiple_sources(self) -> None:
        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            self._run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--source", "proj-a", "--classification", "internal",
            ])
            self._run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--source", "proj-b", "--classification", "internal",
            ])
            scoped = self._run([
                "search", "--query", "production release approval",
                "--classification", "internal", "--source", "proj-a",
            ])
            unscoped = self._run([
                "search", "--query", "production release approval",
                "--classification", "internal", "--all-sources",
            ])
            self.assertTrue(unscoped["results"])
            sources_seen = {item["citation"]["source"] for item in unscoped["results"]}
            self.assertTrue({"proj-a", "proj-b"} <= sources_seen)
            self.assertLessEqual(len(scoped["results"]), len(unscoped["results"]))

    # -- AC-6: ambiguous flags rejected -------------------------------------

    def test_ac6_global_fallback_both_source_and_all_sources_rejected(self) -> None:
        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            with self.assertRaises(ValueError) as captured:
                self._run([
                    "search", "--query", "x", "--classification", "internal",
                    "--source", "proj-a", "--all-sources",
                ])
            self.assertIn("Ambiguous", str(captured.exception))

    def test_ac6_context_command_also_rejects_both_flags(self) -> None:
        # AC-6 names both search and context; the search-only test above
        # doesn't independently prove context (which routes through the
        # same _enforce_retrieval_scope) is actually wired the same way --
        # a command-specific regression here would still catch a future
        # divergence between the two argparse subcommands.
        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            with self.assertRaises(ValueError) as captured:
                self._run([
                    "context", "--agent", "release-engineer", "--task-id", "REL-3",
                    "--query", "x", "--classification", "internal",
                    "--source", "proj-a", "--all-sources",
                ])
            self.assertIn("Ambiguous", str(captured.exception))

    # -- AC-7: ingest enforcement --------------------------------------------

    def test_ac7_global_fallback_ingest_requires_explicit_source(self) -> None:
        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            with self.assertRaises(ValueError) as captured:
                self._run(["ingest", "--input", str(ROOT / "examples" / "chat-export.json"), "--classification", "internal"])
            self.assertIn("--source", str(captured.exception))

            result = self._run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--source", "proj-a", "--classification", "internal",
            ])
            self.assertIn("run_id", result)

    # -- AC-9: fail-closed --config unweakened -------------------------------

    def test_ac9_missing_explicit_config_fails_closed_before_scope_check(self) -> None:
        missing = self.directory / "missing.json"
        with self.assertRaises(FileNotFoundError):
            # Deliberately omit --source/--all-sources too: FileNotFoundError
            # must fire first, not the new scope-enforcement ValueError.
            self._run(["search", "--config", str(missing), "--query", "x", "--classification", "internal"])

    # -- AC-10: resolution order unchanged -----------------------------------

    def test_ac10_resolution_order_unchanged_project_local_wins(self) -> None:
        project = self.directory / "project-precedence"
        project.mkdir()
        (project / ".git").mkdir()
        local_store = project / ".agents" / "knowledge-store"
        local_store.mkdir(parents=True)
        (local_store / "config.json").write_text(
            json.dumps({"database": "project.db", "embedding": {"dimensions": 32}}), encoding="utf-8"
        )
        home = self.directory / "global-home-precedence"
        home.mkdir()
        with mock.patch.dict(os.environ, {"KNOWLEDGE_STORE_HOME": str(home)}):
            with mock.patch("config.Path.cwd", return_value=project):
                config, tier = load_config(return_tier=True)
        self.assertEqual(TIER_PROJECT_LOCAL, tier)
        self.assertEqual(str((local_store / "project.db").resolve()), config["database"])

    def test_tier_signal_matches_each_resolution_tier(self) -> None:
        # explicit config
        explicit_path = self.directory / "explicit.json"
        explicit_path.write_text(json.dumps({"database": "explicit.db", "embedding": {"dimensions": 32}}), encoding="utf-8")
        _, tier = load_config(str(explicit_path), return_tier=True)
        self.assertEqual(TIER_EXPLICIT_CONFIG, tier)

        # project-local
        env_patch, cwd_patch = self._project_local_env()
        with env_patch, cwd_patch:
            _, tier = load_config(return_tier=True)
        self.assertEqual(TIER_PROJECT_LOCAL, tier)

        # global fallback
        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            _, tier = load_config(return_tier=True)
        self.assertEqual(TIER_GLOBAL_FALLBACK, tier)

        # default (no return_tier) behavior is unchanged: a bare dict
        with env_patch, cwd_patch:
            config_only = load_config()
        self.assertIsInstance(config_only, dict)

    # -- AC-11: schema/citation shape unchanged ------------------------------

    def test_ac11_source_filter_null_for_all_sources_matches_schema(self) -> None:
        schema = json.loads((ROOT / "agent-context.schema.json").read_text(encoding="utf-8"))
        source_filter_type = schema["properties"]["source_filter"]["type"]
        self.assertEqual(["string", "null"], source_filter_type)

        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            self._run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--source", "proj-a", "--classification", "internal",
            ])
            all_sources_bundle = self._run([
                "context", "--agent", "release-engineer", "--task-id", "REL-3",
                "--query", "production release approval", "--classification", "internal",
                "--all-sources",
            ])
            self.assertIsNone(all_sources_bundle["source_filter"])

            scoped_bundle = self._run([
                "context", "--agent", "release-engineer", "--task-id", "REL-4",
                "--query", "production release approval", "--classification", "internal",
                "--source", "proj-a",
            ])
            self.assertEqual("proj-a", scoped_bundle["source_filter"])

    # -- AC-12: authority boundary unchanged ---------------------------------

    def test_ac12_agent_autonomy_knowledge_store_authority_unchanged(self) -> None:
        autonomy = yaml.safe_load((REPO_ROOT / "roster" / "shared" / "agent-autonomy.yaml").read_text(encoding="utf-8"))
        knowledge_store = autonomy["knowledge_store"]
        self.assertEqual("allowed", knowledge_store["retrieve_authorized_context"])
        self.assertEqual("knowledge_store_steward_only", knowledge_store["ingest_update_reclassify_or_delete"])

    # -- AC-13: no authentication introduced ---------------------------------

    def test_ac13_source_and_all_sources_remain_unauthenticated_caller_assertions(self) -> None:
        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            # Any caller-supplied string is accepted at face value, with no
            # credential/identity verification of any kind.
            result = self._run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--source", "anyone-can-claim-this-project-name", "--classification", "internal",
            ])
            self.assertIn("run_id", result)

    # -- AC-14: classification enforcement unchanged -------------------------

    def test_ac14_classification_validation_unchanged(self) -> None:
        from service import CLASSIFICATIONS

        self.assertEqual({"public", "internal", "confidential", "restricted"}, CLASSIFICATIONS)
        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            with self.assertRaisesRegex(ValueError, "Invalid classification"):
                self._run([
                    "search", "--query", "x", "--classification", "not-a-real-classification",
                    "--source", "proj-a",
                ])

    # -- AC-15: no retention/deletion dependency -----------------------------

    def test_ac15_no_retention_or_deletion_command_added(self) -> None:
        from cli import _parser

        parser = _parser()
        subparsers_action = next(
            action for action in parser._actions if isinstance(action, argparse._SubParsersAction)
        )
        commands = set(subparsers_action.choices.keys())
        # AC-15 re-decided a THIRD time, 2026-08-09, when `delete-ingested` and
        # `retention-report` were implemented (issue #184).
        #
        # The first re-decision (2026-08-09, `delete-staged`, issue #181)
        # established that AC-15's guarantee is specifically about the
        # lifecycle of *ingested content* -- material that has been
        # normalised, chunked, embedded and made retrievable -- not about
        # every command whose name mentions deletion.
        #
        # This second re-decision on the same date does something different
        # in kind: `delete-ingested` and `retention-report` reach *ingested*
        # content on purpose. That does not mean AC-15 was widened or
        # weakened -- it means the guarantee AC-15 protects (no *automatic*,
        # unaudited deletion; every deletion is steward-authorized, evidenced
        # before it happens, and never a side effect of ingest/search/
        # context/stats) now has a name and a command, replacing "the demo
        # implements no such capability" with "the demo implements exactly
        # this capability, deliberately and auditably". test_ac15b_* in this
        # file assert the new guarantee structurally, the same way
        # test_ac15_deletion_is_confined_to_staged_records (AC-15a, left
        # verbatim below) asserts the older one.
        #
        # The exact set stays pinned so the next lifecycle command still forces
        # this decision rather than pattern-matching on the diff.
        self.assertEqual(
            {
                "init",
                "ingest",
                "search",
                "context",
                "stats",
                "propose",
                "list-staged",
                "show-staged",
                "import-staged",
                "export-staged",
                "disposition-staged",
                "delete-staged",
                "deletion-evidence",
                "delete-ingested",
                "retention-report",
            },
            commands,
        )
        # The bare-name tripwire is untouched and must still pass: neither
        # new command is a bare "delete"/"retention"/"purge"/"expire" name, so
        # a future accidental bare name still fails loudly rather than
        # silently matching one of these.
        for forbidden in ("delete", "retention", "purge", "expire"):
            self.assertNotIn(forbidden, commands)

    def test_ac15_deletion_is_confined_to_staged_records(self) -> None:
        """The re-decision above, asserted rather than only commented.

        If `delete-staged` ever reaches ingested content -- messages, chunks,
        or embeddings -- AC-15 is genuinely broken and this fails.
        """
        import inspect

        import staged_store

        source = inspect.getsource(staged_store.delete_record)
        for ingested_table in ("messages", "chunks", "ingestion_runs", "retrieval_runs"):
            self.assertNotIn(
                ingested_table,
                source,
                f"delete_record touches {ingested_table!r}: staged-record deletion must not reach "
                "ingested content, which is the capability AC-15 withholds",
            )
        self.assertIn("staged_records", source)

    # -- AC-15b: ingested-content deletion, deliberately, auditably ----------
    #
    # issue #184 adds the capability AC-15 previously withheld entirely: a
    # steward-only, evidenced path that reaches `messages`/`chunks`
    # (`ingested_deletion.delete_ingested`, via `delete-ingested`). These
    # tests assert the new guarantee structurally, the same way AC-15a above
    # asserts the older one: no *other* command path may reach that same
    # capability as a side effect, the evidence table holds no content, and
    # evidence never lags behind (or is fabricated by) an attempted delete.

    def _store_with_one_message(self) -> tuple[sqlite3.Connection, Path]:
        import database
        import ingested_deletion
        import service

        database_path = self.directory / f"ac15b-{uuid.uuid4().hex}.db"
        db = database.open_store(str(database_path))
        self.addCleanup(db.close)
        ingested_deletion.install_schema(db)
        config = {
            "database": str(database_path),
            "embedding": {
                "provider": "hashing", "model": "feature-hash-v1", "dimensions": 32,
                "batch_size": 32, "base_url": None, "api_key_env": "UNUSED", "timeout_seconds": 5,
            },
            "chunking": {"max_characters": 2400, "overlap_characters": 240},
            "ingestion": {"default_classification": "internal", "redact_secrets": True},
            "retention": {
                "default_days_by_classification": {"internal": 365, "confidential": 90, "public": None},
                "refuse_restricted_without_window": True,
            },
        }
        service.ingest_file(db, config, {
            "input": str(ROOT / "examples" / "chat-export.json"),
            "source": "proj-a",
            "classification": "internal",
        })
        return db, database_path

    def test_ac15b_ingested_deletion_is_never_a_side_effect(self) -> None:
        """No ingest/search/context/stats/*-staged handler deletes or updates
        `messages`/`chunks`, except `save_message`'s own chunk rebuild
        (`database.py`, `DELETE FROM chunks WHERE message_id = ?`) -- which
        clears one message's own chunks immediately before reinserting them
        on re-ingestion, never removes the message row itself, and is not a
        deletion capability. Only `ingested_deletion.delete_ingested` may.
        """
        import inspect
        import re

        import cli
        import database
        import service

        mutation_pattern = re.compile(r"\b(DELETE|UPDATE)\b")
        handler_functions = [
            service.ingest_file,
            service.search_store,
            service.build_agent_context,
            database.store_stats,
            cli._propose,
            cli._show_staged,
            cli._import_staged,
            cli._export_staged,
            cli._check_staged_export,
        ]
        for function in handler_functions:
            source = inspect.getsource(function)
            matches = mutation_pattern.findall(source)
            self.assertFalse(
                matches,
                f"{function.__qualname__} contains {matches} against messages/chunks -- "
                "ingest/search/context/stats/staged handlers must never mutate ingested "
                "content as a side effect; only ingested_deletion.delete_ingested may.",
            )

        save_message_source = inspect.getsource(database.save_message)
        self.assertIn(
            "DELETE FROM chunks WHERE message_id",
            save_message_source,
            "the one documented exception moved or was removed -- update this test's "
            "allow-list comment if that was deliberate",
        )

        # Behavioural half: ingest once, then repeatedly call context and
        # stats, and confirm the stored counts never move as a side effect.
        env_patch, cwd_patch = self._project_local_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            self._run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--classification", "internal",
            ])
            baseline = self._run(["stats"])
            for task in ("REL-1", "REL-2"):
                self._run([
                    "context", "--agent", "release-engineer", "--task-id", task,
                    "--query", "production release approval", "--classification", "internal",
                ])
            after = self._run(["stats"])
            self.assertEqual(baseline["messages"], after["messages"])
            self.assertEqual(baseline["chunks"], after["chunks"])

    def test_ac15b_no_handler_issues_a_mutating_statement_against_ingested_content(self) -> None:
        """The load-bearing half of AC-15b: observed SQL, not handler source text.

        `test_ac15b_ingested_deletion_is_never_a_side_effect` reads each
        handler's own body with `inspect.getsource`, which is worth keeping as
        a cheap tripwire but cannot be the guarantee: it does not follow calls,
        so moving `DELETE FROM messages` into any helper the handler invokes
        passes it. Its behavioural half (comparing `stats` counts) is no
        backstop either -- a mutation that deletes nothing observable, or
        deletes rows the assertion does not count, moves no counts.

        This test instead installs a trace callback on the connection the CLI
        actually opens, so every statement reaching SQLite is recorded no
        matter which function issued it, and asserts none of them mutates
        `messages`/`chunks`. The one documented exception is `save_message`'s
        own chunk rebuild during `ingest`, allowed by exact shape.
        """
        import re

        import cli as cli_module

        statements: list[str] = []
        real_open_store = cli_module.open_store

        def recording_open_store(*args, **kwargs):
            db = real_open_store(*args, **kwargs)
            db.set_trace_callback(statements.append)
            return db

        mutating = re.compile(
            r"\b(?:DELETE\s+FROM|UPDATE|INSERT\s+(?:OR\s+\w+\s+)?INTO)\s+[\"'`\[]?(messages|chunks)\b",
            re.IGNORECASE,
        )
        chunk_rebuild = re.compile(r"DELETE\s+FROM\s+chunks\s+WHERE\s+message_id", re.IGNORECASE)

        def offending(allow_ingest_writes: bool) -> list[str]:
            found = [line for line in statements if mutating.search(line)]
            if allow_ingest_writes:
                # `ingest` legitimately writes ingested content; what it must
                # never do is remove a message row or rebuild anything beyond
                # the one message's own chunks.
                found = [
                    line
                    for line in found
                    if not chunk_rebuild.search(line)
                    and not re.search(r"INSERT\s+(?:OR\s+\w+\s+)?INTO\s+(messages|chunks)", line, re.IGNORECASE)
                ]
            return found

        env_patch, cwd_patch = self._project_local_env()
        with env_patch, cwd_patch, mock.patch.object(cli_module, "open_store", recording_open_store):
            self._run(["init"])

            statements.clear()
            self._run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--classification", "internal",
            ])
            self.assertEqual(
                [],
                offending(allow_ingest_writes=True),
                "ingest issued a mutating statement against messages/chunks beyond inserting "
                "content and rebuilding the one message's own chunks",
            )

            for command in (
                ["search", "--query", "production release approval", "--classification", "internal"],
                [
                    "context", "--agent", "release-engineer", "--task-id", "REL-1",
                    "--query", "production release approval", "--classification", "internal",
                ],
                ["stats"],
                ["retention-report"],
            ):
                statements.clear()
                self._run(command)
                self.assertEqual(
                    [],
                    offending(allow_ingest_writes=False),
                    f"`{command[0]}` mutated messages/chunks -- read paths must never write "
                    "ingested content, whether directly or through a helper",
                )

    def test_deletion_evidence_requires_a_scope_at_the_shared_tier(self) -> None:
        """`deletion-evidence` is scoped like `search`, not left open.

        Evidence rows are not content, but they carry the deleting project's
        identifier, a steward's free-text reason, and asserted actor
        identities -- cross-project metadata in a store several projects
        share. Every other command reading that store makes the caller name a
        scope; this one must too, rather than being the single exception.
        """
        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            with self.assertRaises(ValueError) as unscoped:
                self._run(["deletion-evidence"])
            self.assertIn("--source", str(unscoped.exception))

            with self.assertRaises(ValueError) as ambiguous:
                self._run(["deletion-evidence", "--source", "proj-a", "--all-sources"])
            self.assertIn("Ambiguous scope", str(ambiguous.exception))

            # Explicit cross-project opt-in stays available -- the point is
            # that it is stated, not that it is forbidden.
            self.assertIn("deletions", self._run(["deletion-evidence", "--all-sources"]))

    def test_deletion_evidence_does_not_disclose_another_projects_deletions(self) -> None:
        """A scoped read returns only the named project's evidence."""
        env_patch, cwd_patch = self._global_fallback_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            for source in ("proj-a", "proj-b"):
                self._run([
                    "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                    "--classification", "internal", "--source", source,
                ])
                self._run([
                    "delete-ingested", "--scope", "source", "--id", source, "--source", source,
                    "--reason", f"cleanup for {source}", "--trigger", "steward-decision",
                    "--deleted-by", f"steward-{source}", "--authorized-by", f"human-{source}",
                ])

            scoped = self._run(["deletion-evidence", "--source", "proj-a"])["deletions"]
            self.assertEqual(["proj-a"], sorted({entry["source"] for entry in scoped}))
            joined = json.dumps(scoped)
            self.assertNotIn("proj-b", joined)
            self.assertNotIn("steward-proj-b", joined)

            everything = self._run(["deletion-evidence", "--all-sources"])["deletions"]
            self.assertEqual({"proj-a", "proj-b"}, {entry["source"] for entry in everything})

    def test_retention_report_rejects_an_unparseable_as_of(self) -> None:
        env_patch, cwd_patch = self._project_local_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            with self.assertRaises(ValueError) as refused:
                self._run(["retention-report", "--as-of", "last tuesday"])
            self.assertIn("ISO-8601", str(refused.exception))

    def test_retention_report_as_of_is_compared_as_an_instant_not_a_string(self) -> None:
        """`--as-of` is an instant, not a string sorted against stored text.

        `retention_until` is stored in one canonical shape and compared with
        `<=`, so any other valid ISO-8601 spelling sorts by character rather
        than by clock. A non-UTC offset is the case where that diverges
        silently in the dangerous direction: `T15:23+02:00` is *earlier* than
        a `T14:23Z` expiry, but sorts *after* it, so an unnormalised cutoff
        reports content as expired that has not expired -- inviting a steward
        to delete it early, with a report that looks correct either way.

        Pinned on the count, which differs between the two behaviours, rather
        than on the echoed `as_of` string, which a normaliser could satisfy
        without the comparison using it.
        """
        env_patch, cwd_patch = self._project_local_env()
        with env_patch, cwd_patch:
            self._run(["init"])
            self._run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--classification", "internal", "--retention-days", "1",
            ])
            eventually = self._run(["retention-report", "--as-of", "2999-01-01T00:00:00Z"])
            total = eventually["expired_message_count"]
            self.assertGreater(total, 0)
            expiry = max(item["retention_until"] for item in eventually["items"])
            self.assertTrue(expiry.endswith("Z"), expiry)

            # One hour before the expiry, written in +02:00. Nothing has
            # expired at that instant; naive string comparison says otherwise
            # because "T15" sorts above "T14".
            earlier = datetime.strptime(expiry, "%Y-%m-%dT%H:%M:%S.%f%z") - timedelta(hours=1)
            as_of = earlier.astimezone(tzinfo_offset(2)).isoformat(timespec="seconds")
            self.assertGreater(as_of, expiry, "fixture no longer exercises the sort/clock divergence")
            self.assertEqual(
                0,
                self._run(["retention-report", "--as-of", as_of])["expired_message_count"],
                f"content expiring at {expiry} was reported expired as of {as_of}, which is earlier",
            )

            # The same instant one hour *after* the expiry does report it,
            # so the assertion above is not passing by reporting nothing.
            later = (earlier + timedelta(hours=2)).astimezone(tzinfo_offset(2)).isoformat(timespec="seconds")
            self.assertEqual(
                total, self._run(["retention-report", "--as-of", later])["expired_message_count"]
            )

    def test_ac15b_evidence_columns_exclude_content(self) -> None:
        db, _ = self._store_with_one_message()
        columns = {row["name"] for row in db.execute("PRAGMA table_info(ingested_content_deletions)")}
        self.assertEqual(
            {
                "id", "scope", "scope_key", "source", "classification", "message_count",
                "chunk_count", "content_digest", "message_digests_json", "embedding_provider",
                "embedding_model", "trigger", "reason", "deleted_by", "authorized_by",
                "ingestion_runs_redacted_json", "delete_status", "reclaim_status", "deleted_at",
            },
            columns,
        )
        for forbidden in ("content", "body", "title", "conversation_title", "embedding_json", "source_uri"):
            self.assertNotIn(
                forbidden,
                columns,
                f"ingested_content_deletions must never gain a {forbidden!r} column -- evidence "
                "holds digests and counts, never the content it describes",
            )

    def _failing_connection(
        self, database_path: Path, statement_prefix: str | tuple[str, ...]
    ) -> sqlite3.Connection:
        """Reopen `database_path` through a `sqlite3.Connection` subclass whose
        `execute` raises for one or more statement shapes.

        `sqlite3.Connection` is an immutable built-in type -- it accepts
        neither an instance attribute override nor a `mock.patch.object`
        class-level override of `execute`. Subclassing via SQLite's own
        supported `factory=` extension point is the supported way to inject
        a fault at this layer.
        """
        prefixes = (statement_prefix,) if isinstance(statement_prefix, str) else statement_prefix

        class FailingConnection(sqlite3.Connection):
            def execute(self, sql, *args, **kwargs):  # type: ignore[override]
                if isinstance(sql, str) and sql.strip().startswith(prefixes):
                    raise sqlite3.OperationalError(f"simulated failure: {sql.strip()[:60]}")
                return super().execute(sql, *args, **kwargs)

        failing_db = sqlite3.connect(database_path, factory=FailingConnection)
        self.addCleanup(failing_db.close)
        failing_db.row_factory = sqlite3.Row
        failing_db.execute("PRAGMA foreign_keys = ON")
        return failing_db

    def test_ac15b_evidence_precedes_delete(self) -> None:
        """Evidence exists, as delete_status='failed' (or 'attempted'), even
        when the delete step fails afterward -- and the messages are NOT
        removed, because delete_status='completed' is only ever set in the
        same atomic transaction as the actual DELETE (see
        `ingested_deletion.delete_ingested`'s docstring).
        """
        import ingested_deletion

        db, database_path = self._store_with_one_message()
        before = db.execute("SELECT COUNT(*) FROM messages").fetchone()[0]
        self.assertGreater(before, 0)
        db.close()

        failing_db = self._failing_connection(database_path, "DELETE FROM messages")

        with self.assertRaises(sqlite3.OperationalError):
            ingested_deletion.delete_ingested(
                failing_db, scope="source", scope_key="proj-a", reason="incident cleanup",
                deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
            )

        evidence = ingested_deletion.deletion_evidence(failing_db)
        self.assertEqual(1, len(evidence))
        self.assertEqual("proj-a", evidence[0]["source"])
        self.assertEqual("incident cleanup", evidence[0]["reason"])
        self.assertEqual(before, evidence[0]["message_count"])
        # The evidence row exists and records the attempt, but is explicitly
        # NOT 'completed': the DELETE itself never ran (the failing
        # connection raised before it could), so the best-effort follow-up
        # UPDATE marks it 'failed'.
        self.assertEqual("failed", evidence[0]["delete_status"])
        # The messages themselves were NOT removed, because the delete step
        # failed before the DELETE could run at all -- this is the property
        # this test exists to prove.
        remaining = failing_db.execute("SELECT COUNT(*) FROM messages").fetchone()[0]
        self.assertEqual(before, remaining)

    def test_ac15b_evidence_update_failure_rolls_back_the_delete_too(self) -> None:
        """Txn 2's atomicity, proven rather than assumed: an injected
        failure of the evidence UPDATE (delete_status='completed') inside
        txn 2 must roll back the DELETE FROM messages it was grouped with,
        not just leave the marker un-set while content is actually gone.
        """
        import ingested_deletion

        db, database_path = self._store_with_one_message()
        before = db.execute("SELECT COUNT(*) FROM messages").fetchone()[0]
        self.assertGreater(before, 0)
        db.close()

        failing_db = self._failing_connection(
            database_path, "UPDATE ingested_content_deletions SET delete_status = 'completed'"
        )

        with self.assertRaises(sqlite3.OperationalError):
            ingested_deletion.delete_ingested(
                failing_db, scope="source", scope_key="proj-a", reason="incident cleanup",
                deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
            )

        # The DELETE ran earlier in the SAME transaction as the failing
        # UPDATE -- if txn 2 were not atomic, the messages would be gone
        # here even though delete_status never reached 'completed'. They
        # must NOT be gone: that mismatch is exactly what atomicity rules out.
        remaining = failing_db.execute("SELECT COUNT(*) FROM messages").fetchone()[0]
        self.assertEqual(before, remaining, "the DELETE must have rolled back with the failed UPDATE")

        evidence = ingested_deletion.deletion_evidence(failing_db)
        self.assertEqual(1, len(evidence))
        self.assertEqual("failed", evidence[0]["delete_status"])

    def test_ac15b_double_failure_leaves_the_txn1_seed_value_not_completed(self) -> None:
        """Pins txn 1's INSERT seed value, adversarially.

        Nothing else in this file distinguishes an INSERT seeded
        `delete_status='attempted'` from one seeded `delete_status='completed'`:
        the success path re-sets 'completed' redundantly, and every other
        failure test here triggers the except-handler's fallback
        `UPDATE ... SET delete_status = 'failed'`, which masks a wrong seed by
        overwriting it. This test fails BOTH the first statement inside txn 2
        (`DELETE FROM messages`) AND the fallback failed-marker `UPDATE` --
        the disk-full/crash-class double failure where neither the real
        completion nor the best-effort failure marker can be written. Under
        that double failure, the persisted evidence row must still read
        whatever txn 1 originally seeded: if that seed were ever wrongly
        'completed' instead of 'attempted', this is the one test that would
        catch a steward being told content was removed when it was not.
        """
        import ingested_deletion

        db, database_path = self._store_with_one_message()
        before = db.execute("SELECT COUNT(*) FROM messages").fetchone()[0]
        self.assertGreater(before, 0)
        db.close()

        failing_db = self._failing_connection(
            database_path,
            (
                "DELETE FROM messages",
                "UPDATE ingested_content_deletions SET delete_status = 'failed'",
            ),
        )

        with self.assertRaises(sqlite3.OperationalError):
            ingested_deletion.delete_ingested(
                failing_db, scope="source", scope_key="proj-a", reason="double failure",
                deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
            )

        evidence = ingested_deletion.deletion_evidence(failing_db)
        self.assertEqual(1, len(evidence))
        # Neither the real completion nor the fallback failure marker could
        # be written, so this is exactly the txn 1 INSERT's seed value --
        # pinned here to 'attempted', never 'completed'.
        self.assertEqual("attempted", evidence[0]["delete_status"])
        remaining = failing_db.execute("SELECT COUNT(*) FROM messages").fetchone()[0]
        self.assertEqual(before, remaining)

    def test_ac15b_refusal_leaves_no_evidence(self) -> None:
        import ingested_deletion

        db, _ = self._store_with_one_message()
        before = db.execute("SELECT COUNT(*) FROM messages").fetchone()[0]
        with self.assertRaises(ingested_deletion.IngestedDeletionError):
            ingested_deletion.delete_ingested(
                db, scope="source", scope_key="proj-a", reason="",  # empty reason: refused
                deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
            )
        self.assertEqual([], ingested_deletion.deletion_evidence(db))
        remaining = db.execute("SELECT COUNT(*) FROM messages").fetchone()[0]
        self.assertEqual(before, remaining)


if __name__ == "__main__":
    unittest.main()
