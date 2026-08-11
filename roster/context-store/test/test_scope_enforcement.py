"""Scope read rules, and the deliberate absence of an `--all-sources` escape.

These rules are caller-asserted and unauthenticated on the CLI path, exactly as
classification is in the knowledge store. They reduce blast radius and produce
an audit trail; they are not access control. The tests assert the rules hold,
not that they are unbypassable by a caller willing to assert a different
identity -- and `SECURITY.md` says so in the same terms.
"""

from __future__ import annotations

import contextlib
import io
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "src"))

_SHARED_TEST_DIR = ROOT.parent / "shared" / "test"
if str(_SHARED_TEST_DIR) not in sys.path:
    sys.path.append(str(_SHARED_TEST_DIR))

import cli  # noqa: E402
from config import DEFAULTS, TIER_GLOBAL_FALLBACK, TIER_PROJECT_LOCAL, load_config  # noqa: E402
from database import open_store  # noqa: E402
from service import ContextStoreError, drop_entry, get_entry, list_entries, put_entry  # noqa: E402
from settings_test_helpers import isolate_settings  # noqa: E402


class ScopeTestCase(unittest.TestCase):
    def setUp(self) -> None:
        isolate_settings(self)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.config = json.loads(json.dumps(DEFAULTS))
        self.config["database"] = str(self.root / "context.db")
        self.db = open_store(self.config["database"])
        self.addCleanup(self.db.close)

    def put(self, **overrides: object) -> str:
        options = {
            "agent": "code-reviewer", "task_id": "TASK-1", "classification": "internal",
            "source": "demo", "label": "entry", "scope": "agent", "content": "material",
        }
        options.update(overrides)
        return put_entry(self.db, self.config, options)["handle"]

    def can_read(self, handle: str, **caller: object) -> bool:
        options = {
            "agent": "code-reviewer", "task_id": "TASK-1",
            "classification": "internal", "source": "demo", "handle": handle,
        }
        options.update(caller)
        return bool(get_entry(self.db, options)["results"])


class AgentScopeTests(ScopeTestCase):
    def test_owner_can_read(self) -> None:
        self.assertTrue(self.can_read(self.put()))

    def test_a_different_agent_cannot_read(self) -> None:
        handle = self.put()
        self.assertFalse(self.can_read(handle, agent="security-reviewer"))

    def test_the_same_agent_on_a_different_task_cannot_read(self) -> None:
        handle = self.put()
        self.assertFalse(self.can_read(handle, task_id="TASK-2"))


class DispatchScopeTests(ScopeTestCase):
    def test_a_peer_in_the_same_dispatch_can_read(self) -> None:
        handle = self.put(scope="dispatch", dispatch_id="DISPATCH-9")
        self.assertTrue(self.can_read(handle, agent="security-reviewer", dispatch_id="DISPATCH-9"))

    def test_a_peer_in_a_different_dispatch_cannot_read(self) -> None:
        handle = self.put(scope="dispatch", dispatch_id="DISPATCH-9")
        self.assertFalse(self.can_read(handle, agent="security-reviewer", dispatch_id="DISPATCH-8"))

    def test_a_caller_asserting_no_dispatch_cannot_read(self) -> None:
        handle = self.put(scope="dispatch", dispatch_id="DISPATCH-9")
        self.assertFalse(self.can_read(handle, agent="security-reviewer"))


class ProjectScopeTests(ScopeTestCase):
    def test_any_agent_in_the_project_can_read(self) -> None:
        handle = self.put(scope="project")
        self.assertTrue(self.can_read(handle, agent="release-engineer", task_id="TASK-77"))

    def test_another_project_cannot_read(self) -> None:
        handle = self.put(scope="project")
        self.assertFalse(self.can_read(handle, source="other-project"))


class ClassificationTests(ScopeTestCase):
    def test_classification_is_exact_match_not_hierarchical(self) -> None:
        handle = self.put(scope="project", classification="confidential")
        self.assertFalse(self.can_read(handle, classification="internal"))
        self.assertFalse(self.can_read(handle, classification="restricted"))
        self.assertTrue(self.can_read(handle, classification="confidential"))


class IndistinguishabilityTests(ScopeTestCase):
    def test_absent_expired_and_unreadable_are_indistinguishable(self) -> None:
        # Distinguishing them would let a caller probe for the existence of
        # entries it is not allowed to read.
        readable = self.put()
        unreadable = self.put(agent="someone-else")
        absent = "ctx_" + "0" * 32

        def bundle(handle: str, **caller: object) -> dict:
            options = {
                "agent": "code-reviewer", "task_id": "TASK-1", "classification": "internal",
                "source": "demo", "handle": handle,
            }
            options.update(caller)
            return get_entry(self.db, options)

        self.assertEqual(len(bundle(readable)["results"]), 1)
        for handle in (unreadable, absent):
            result = bundle(handle)
            self.assertEqual(result["results"], [])
            self.assertNotIn("error", result)
            self.assertNotIn("reason", result)


class ListingScopeTests(ScopeTestCase):
    def test_a_listing_only_shows_readable_entries(self) -> None:
        self.put(label="mine")
        self.put(label="theirs", agent="someone-else")
        self.put(label="project-wide", scope="project", agent="someone-else")
        bundle = list_entries(self.db, {
            "agent": "code-reviewer", "task_id": "TASK-1",
            "classification": "internal", "source": "demo",
        })
        labels = sorted(result["label"] for result in bundle["results"])
        self.assertEqual(labels, ["mine", "project-wide"])


class DispatchIdentityVersusFilterTests(ScopeTestCase):
    """`--dispatch-id` is who you are; `--filter-dispatch-id` is what you want.

    Regression: these were one flag, so supplying a dispatch identity in order
    to read a peer's entry also filtered out every agent-scoped entry of your
    own -- those carry no dispatch id to match. It stayed invisible until the
    MCP adapter started passing the ambient session id on every call, which is
    exactly the case a human running the CLI by hand would rarely hit.
    """

    def test_supplying_a_dispatch_identity_does_not_hide_your_own_entries(self) -> None:
        self.put(label="mine")
        bundle = list_entries(self.db, {
            "agent": "code-reviewer", "task_id": "TASK-1", "classification": "internal",
            "source": "demo", "dispatch_id": "SESSION-1",
        })
        self.assertEqual([r["label"] for r in bundle["results"]], ["mine"])

    def test_the_filter_still_narrows_when_asked(self) -> None:
        self.put(label="mine")
        self.put(label="teamwork", scope="dispatch", dispatch_id="SESSION-1")
        bundle = list_entries(self.db, {
            "agent": "code-reviewer", "task_id": "TASK-1", "classification": "internal",
            "source": "demo", "dispatch_id": "SESSION-1", "filter_dispatch_id": "SESSION-1",
        })
        self.assertEqual([r["label"] for r in bundle["results"]], ["teamwork"])

    def test_identity_still_gates_readability(self) -> None:
        self.put(label="teamwork", scope="dispatch", dispatch_id="SESSION-1")
        without = list_entries(self.db, {
            "agent": "code-reviewer", "task_id": "TASK-1",
            "classification": "internal", "source": "demo",
        })
        self.assertEqual(without["results"], [])


class DropIsGatedLikeAReadTests(ScopeTestCase):
    """Regression: `drop` once took no identity at all.

    It was the only command with no `_readable` check and no audit row -- and
    the only irreversible one. Handles circulate by design (the handoff
    contract tells agents to quote them), so any caller who learned one could
    destroy any entry in the database, across agents, scopes, and
    classifications, leaving no record of who did it.
    """

    def drop(self, handle: str, **caller: object) -> dict:
        options = {
            "handle": handle, "reason": "cleanup", "agent": "code-reviewer",
            "task_id": "TASK-1", "classification": "internal", "source": "demo",
        }
        options.update(caller)
        return drop_entry(self.db, options)

    def test_the_owner_can_drop(self) -> None:
        handle = self.put()
        self.assertEqual(self.drop(handle)["handle"], handle)
        self.assertFalse(self.can_read(handle))

    def test_another_agent_cannot_drop(self) -> None:
        handle = self.put()
        with self.assertRaises(ContextStoreError):
            self.drop(handle, agent="someone-else")
        self.assertTrue(self.can_read(handle), "the entry must survive a refused drop")

    def test_another_task_cannot_drop_an_agent_scoped_entry(self) -> None:
        handle = self.put()
        with self.assertRaises(ContextStoreError):
            self.drop(handle, task_id="TASK-2")
        self.assertTrue(self.can_read(handle))

    def test_a_different_classification_cannot_drop(self) -> None:
        handle = self.put(scope="project", classification="confidential")
        with self.assertRaises(ContextStoreError):
            self.drop(handle, classification="internal")
        self.assertTrue(self.can_read(handle, classification="confidential"))

    def test_another_project_cannot_drop(self) -> None:
        handle = self.put(scope="project")
        with self.assertRaises(ContextStoreError):
            self.drop(handle, source="other-project")
        self.assertTrue(self.can_read(handle))

    def test_a_refused_drop_is_indistinguishable_from_an_absent_handle(self) -> None:
        handle = self.put()
        with self.assertRaises(ContextStoreError) as unreadable:
            self.drop(handle, agent="someone-else")
        with self.assertRaises(ContextStoreError) as absent:
            self.drop("ctx_" + "0" * 32)
        self.assertEqual(
            str(unreadable.exception).replace(handle, "H"),
            str(absent.exception).replace("ctx_" + "0" * 32, "H"),
        )

    def test_a_drop_is_audited(self) -> None:
        self.drop(self.put())
        row = self.db.execute("SELECT * FROM access_runs WHERE operation = 'drop'").fetchone()
        self.assertIsNotNone(row, "an irreversible operation must be attributable")
        self.assertEqual(row["agent"], "code-reviewer")

    def test_a_refused_drop_writes_no_expiry_evidence(self) -> None:
        handle = self.put()
        with self.assertRaises(ContextStoreError):
            self.drop(handle, agent="someone-else")
        row = self.db.execute(
            "SELECT COUNT(*) AS n FROM expiry_evidence WHERE handle = ?", (handle,)
        ).fetchone()
        self.assertEqual(row["n"], 0)


class GlobalTierScopeTests(unittest.TestCase):
    """`--source` is mandatory against the shared store, with no opt-out."""

    def setUp(self) -> None:
        isolate_settings(self)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)

    def run_cli(self, argv: list[str], stdin: str = "") -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            original = sys.stdin
            sys.stdin = io.StringIO(stdin)
            try:
                code = cli.main(argv)
            finally:
                sys.stdin = original
        return code, out.getvalue(), err.getvalue()

    def test_global_tier_refuses_a_read_without_an_explicit_source(self) -> None:
        home = self.root / "global"
        home.mkdir()
        (home / "config.json").write_text(
            json.dumps({"database": str(home / "context.db")}), encoding="utf-8"
        )
        with mock.patch.dict("os.environ", {"CONTEXT_STORE_HOME": str(home)}):
            import settings

            settings.reset_cache()
            _, tier = load_config(return_tier=True)
            self.assertEqual(tier, TIER_GLOBAL_FALLBACK)
            code, _, err = self.run_cli(
                ["list", "--agent", "a", "--task-id", "T", "--classification", "internal"]
            )
        self.assertEqual(code, 1)
        self.assertIn("--source", err)

    def test_there_is_no_all_sources_flag(self) -> None:
        # Deliberately stricter than `cadre knowledge`, which offers
        # `--all-sources`. Cross-project reads of unreviewed working notes are
        # not an offered mode, so argparse must reject the flag outright rather
        # than the store quietly honouring it.
        with self.assertRaises(SystemExit):
            with contextlib.redirect_stderr(io.StringIO()):
                cli._parser().parse_args([
                    "list", "--agent", "a", "--task-id", "T",
                    "--classification", "internal", "--all-sources",
                ])

    def test_project_local_tier_does_not_require_an_explicit_source(self) -> None:
        project = self.root / "project"
        (project / ".agents" / "context-store").mkdir(parents=True)
        (project / ".git").mkdir()
        (project / ".agents" / "context-store" / "config.json").write_text(
            json.dumps({"database": "./context.db"}), encoding="utf-8"
        )
        original = os.getcwd()
        os.chdir(project)
        self.addCleanup(os.chdir, original)
        _, tier = load_config(return_tier=True)
        self.assertEqual(tier, TIER_PROJECT_LOCAL)
        code, _, err = self.run_cli(
            ["put", "--label", "x", "--agent", "a", "--task-id", "T", "--classification", "internal"],
            stdin="material",
        )
        self.assertEqual(code, 0, err)


if __name__ == "__main__":
    unittest.main()
