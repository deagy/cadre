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
import sys
import tempfile
import unittest
import yaml
from pathlib import Path
from unittest import mock


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
        config = select_agents.load_routing(select_agents.ORCHESTRATION_ROOT / "routing.yaml")
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
        # The exact set is pinned deliberately, so a lifecycle command cannot
        # be added without this assertion forcing the author to confront AC-15.
        # `propose`/`list-staged`/`show-staged` were added with the staged-record
        # backend; none of them deletes anything, and staging is not ingestion.
        # When deletion is implemented (step 7 of the staged-records proposal),
        # AC-15 itself has to be re-decided here rather than quietly widened --
        # the point of this test is that the decision is made, not avoided.
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
            },
            commands,
        )
        for forbidden in ("delete", "retention", "purge", "expire"):
            self.assertNotIn(forbidden, commands)


if __name__ == "__main__":
    unittest.main()
