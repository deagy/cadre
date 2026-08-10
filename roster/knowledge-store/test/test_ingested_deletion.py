"""Tests for retention windows and steward-only deletion of ingested content (issue #184).

Covers `config.py`'s `retention` block, `service.resolve_retention_until`,
`database.py`'s additive `retention_until` migration, and
`ingested_deletion.py`'s `retention_report`/`delete_ingested`/
`deletion_evidence`. `test_scope_enforcement.py`'s `test_ac15b_*` tests cover
the structural AC-15b guarantees (never a side effect, evidence columns,
evidence-before-delete, refusal leaves no evidence); this file covers the
functional behavior those structural guarantees sit on top of.
"""

from __future__ import annotations

import json
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "src"))

_SHARED_TEST_DIR = ROOT.parent / "shared" / "test"
if str(_SHARED_TEST_DIR) not in sys.path:
    sys.path.append(str(_SHARED_TEST_DIR))

import config as config_module  # noqa: E402
import ingested_deletion  # noqa: E402
from cli import run  # noqa: E402
from database import open_store, store_stats  # noqa: E402
from service import ingest_file, resolve_retention_until  # noqa: E402
from settings_test_helpers import isolate_settings  # noqa: E402  (sys.path set above)


def test_config(database: Path, **retention_overrides: object) -> dict[str, object]:
    retention = {
        "default_days_by_classification": {"internal": 365, "confidential": 90, "public": None},
        "refuse_restricted_without_window": True,
    }
    retention.update(retention_overrides)
    return {
        "database": str(database),
        "embedding": {
            "provider": "hashing", "model": "feature-hash-v1", "dimensions": 32,
            "batch_size": 32, "base_url": None, "api_key_env": "UNUSED", "timeout_seconds": 5,
        },
        "chunking": {"max_characters": 2400, "overlap_characters": 240},
        "ingestion": {"default_classification": "internal", "redact_secrets": True},
        "retention": retention,
    }


class RetentionWindowTests(unittest.TestCase):
    def test_defaults_by_classification(self) -> None:
        config = test_config(Path("unused.db"))
        internal = resolve_retention_until(config, "internal")
        confidential = resolve_retention_until(config, "confidential")
        public = resolve_retention_until(config, "public")
        self.assertIsNotNone(internal)
        self.assertIsNotNone(confidential)
        self.assertIsNone(public, "public's configured default is null: indefinite")
        # confidential (90 days) expires sooner than internal (365 days).
        self.assertLess(confidential, internal)

    def test_shipped_defaults_are_indefinite_pending_the_retention_decision(self) -> None:
        """The *shipped* defaults record no window for any classification.

        Distinct from `test_defaults_by_classification`, which exercises the
        day-count mechanism with a fixture that configures real numbers. This
        pins what the demo ships when nobody has configured anything: concrete
        windows are an open Product Owner / Engineering Lead decision in
        `roster/shared/team-profile.yaml`, and shipping working numbers ahead
        of it would let them become policy by default inertia.

        `restricted` is deliberately excluded from that placeholder: it still
        refuses at ingest, so the one tier where "kept forever because nobody
        decided" is least acceptable cannot reach that state by omission.
        """
        import config as config_module

        defaults = config_module.DEFAULTS["retention"]
        self.assertEqual(
            {"internal": None, "confidential": None, "public": None},
            defaults["default_days_by_classification"],
            "shipped retention defaults are indefinite until the decision lands -- if real "
            "windows were ratified, update team-profile.yaml and this test together",
        )
        self.assertTrue(
            defaults["refuse_restricted_without_window"],
            "restricted must keep refusing while the other windows are open",
        )
        self.assertNotIn("restricted", defaults["default_days_by_classification"])

    def test_restricted_refused_without_explicit_window(self) -> None:
        config = test_config(Path("unused.db"))
        with self.assertRaisesRegex(ValueError, "restricted content requires an explicit retention window"):
            resolve_retention_until(config, "restricted")

    def test_restricted_accepted_with_explicit_override(self) -> None:
        config = test_config(Path("unused.db"))
        until = resolve_retention_until(config, "restricted", 30)
        self.assertIsNotNone(until)

    def test_restricted_refusal_can_be_disabled_in_config(self) -> None:
        config = test_config(Path("unused.db"), refuse_restricted_without_window=False)
        self.assertIsNone(resolve_retention_until(config, "restricted"))

    def test_explicit_override_wins_over_every_classification_default(self) -> None:
        config = test_config(Path("unused.db"))
        overridden = resolve_retention_until(config, "public", 7)
        self.assertIsNotNone(overridden, "an explicit override applies even to public, whose default is null")

    def test_retention_days_must_be_a_positive_integer(self) -> None:
        config = test_config(Path("unused.db"))
        for bad in (0, -1, True, "30"):
            with self.subTest(bad=bad):
                with self.assertRaises(ValueError):
                    resolve_retention_until(config, "internal", bad)

    def test_config_validates_retention_block(self) -> None:
        with tempfile.TemporaryDirectory(prefix="ks-retention-config-") as directory:
            config_path = Path(directory) / "config.json"
            config_path.write_text(
                json.dumps({"retention": {"default_days_by_classification": {"internal": -5}}}),
                encoding="utf-8",
            )
            with self.assertRaises(ValueError):
                config_module.load_config(str(config_path))

    def test_config_rejects_restricted_in_default_days_map(self) -> None:
        """A 'restricted' entry in default_days_by_classification would be dead
        configuration -- resolve_retention_until never reads it -- so config
        validation must reject it loudly rather than silently ignore it.
        """
        with tempfile.TemporaryDirectory(prefix="ks-retention-restricted-dead-config-") as directory:
            config_path = Path(directory) / "config.json"
            config_path.write_text(
                json.dumps({"retention": {"default_days_by_classification": {"restricted": 30}}}),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, "must not set 'restricted'"):
                config_module.load_config(str(config_path))

    def test_ingest_refuses_restricted_before_any_write(self) -> None:
        """Refusal at resolve time -- ingest_file must not begin_run first."""
        with tempfile.TemporaryDirectory(prefix="ks-restricted-refusal-") as directory:
            config = test_config(Path(directory) / "store.db")
            db = open_store(config["database"])
            try:
                with self.assertRaises(ValueError):
                    ingest_file(db, config, {
                        "input": str(ROOT / "examples" / "chat-export.json"),
                        "source": "proj-a", "classification": "restricted",
                    })
                stats = store_stats(db)
                self.assertEqual(0, stats["messages"])
                self.assertEqual(0, stats["completed_runs"])
                self.assertEqual(0, stats["failed_runs"])
            finally:
                db.close()

    def test_ingest_records_retention_until_per_message(self) -> None:
        with tempfile.TemporaryDirectory(prefix="ks-retention-ingest-") as directory:
            config = test_config(Path(directory) / "store.db")
            db = open_store(config["database"])
            try:
                result = ingest_file(db, config, {
                    "input": str(ROOT / "examples" / "chat-export.json"),
                    "source": "proj-a", "classification": "internal",
                })
                self.assertIsNotNone(result["retention_until"])
                rows = db.execute("SELECT retention_until FROM messages").fetchall()
                self.assertTrue(rows)
                for row in rows:
                    self.assertEqual(result["retention_until"], row["retention_until"])
            finally:
                db.close()

    def test_ingest_public_records_no_window_by_default(self) -> None:
        with tempfile.TemporaryDirectory(prefix="ks-retention-public-") as directory:
            config = test_config(Path(directory) / "store.db")
            db = open_store(config["database"])
            try:
                ingest_file(db, config, {
                    "input": str(ROOT / "examples" / "chat-export.json"),
                    "source": "proj-a", "classification": "public",
                })
                rows = db.execute("SELECT retention_until FROM messages").fetchall()
                self.assertTrue(all(row["retention_until"] is None for row in rows))
            finally:
                db.close()


class MigrationTests(unittest.TestCase):
    """The additive `retention_until` migration must not fail against a
    store created before it existed."""

    def test_pre_existing_store_without_retention_column_opens_cleanly(self) -> None:
        with tempfile.TemporaryDirectory(prefix="ks-migration-") as directory:
            database_path = Path(directory) / "legacy.db"
            # Build a store using the pre-#184 schema shape by hand: the
            # same SCHEMA minus retention_until, exactly what a real
            # pre-existing store's `messages` table looks like.
            legacy = sqlite3.connect(database_path)
            legacy.executescript("""
                CREATE TABLE ingestion_runs (
                  id TEXT PRIMARY KEY, source TEXT NOT NULL, source_uri TEXT, started_at TEXT NOT NULL,
                  completed_at TEXT, status TEXT NOT NULL, message_count INTEGER NOT NULL DEFAULT 0,
                  chunk_count INTEGER NOT NULL DEFAULT 0, error TEXT
                );
                CREATE TABLE messages (
                  id TEXT PRIMARY KEY, source TEXT NOT NULL, source_uri TEXT, conversation_id TEXT NOT NULL,
                  conversation_title TEXT, source_message_id TEXT NOT NULL, role TEXT NOT NULL, content TEXT NOT NULL,
                  content_hash TEXT NOT NULL, created_at TEXT, classification TEXT NOT NULL,
                  injection_risk INTEGER NOT NULL DEFAULT 0, redactions_json TEXT NOT NULL,
                  metadata_json TEXT NOT NULL, ingested_at TEXT NOT NULL,
                  UNIQUE(source, conversation_id, source_message_id)
                );
                CREATE TABLE chunks (
                  id TEXT PRIMARY KEY, message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
                  ordinal INTEGER NOT NULL, content TEXT NOT NULL, content_hash TEXT NOT NULL,
                  embedding_provider TEXT NOT NULL, embedding_model TEXT NOT NULL,
                  embedding_dimensions INTEGER NOT NULL, embedding_json TEXT NOT NULL,
                  UNIQUE(message_id, ordinal, embedding_provider, embedding_model)
                );
                CREATE TABLE retrieval_runs (
                  id TEXT PRIMARY KEY, query_hash TEXT NOT NULL, task_id TEXT NOT NULL, agent TEXT NOT NULL,
                  classification TEXT NOT NULL, source_filter TEXT, embedding_provider TEXT NOT NULL,
                  embedding_model TEXT NOT NULL, requested_top INTEGER NOT NULL, result_count INTEGER NOT NULL,
                  created_at TEXT NOT NULL
                );
            """)
            legacy.execute(
                "INSERT INTO messages (id, source, source_uri, conversation_id, conversation_title, "
                "source_message_id, role, content, content_hash, created_at, classification, "
                "injection_risk, redactions_json, metadata_json, ingested_at) VALUES "
                "('m1', 'legacy-source', NULL, 'c1', NULL, 'sm1', 'user', 'hello', 'hash', NULL, "
                "'internal', 0, '[]', '{}', '2025-01-01T00:00:00.000Z')"
            )
            legacy.commit()
            legacy.close()

            # open_store must not fail against this pre-existing file, and
            # the pre-existing row must read back with retention_until NULL
            # (additive, nullable -- not a destructive rewrite).
            db = open_store(str(database_path))
            try:
                row = db.execute("SELECT retention_until FROM messages WHERE id = 'm1'").fetchone()
                self.assertIsNone(row["retention_until"])
                # A second open (idempotency) must also not fail.
                db.close()
                reopened = open_store(str(database_path))
                reopened.close()
            finally:
                db.close()


class IngestedDeletionTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="ks-ingested-deletion-")
        self.directory = Path(self.temporary.name)
        self.config = test_config(self.directory / "store.db")
        self.db = open_store(self.config["database"])
        ingested_deletion.install_schema(self.db)

    def tearDown(self) -> None:
        self.db.close()
        self.temporary.cleanup()

    def _ingest(self, source: str, classification: str = "internal") -> dict:
        return ingest_file(self.db, self.config, {
            "input": str(ROOT / "examples" / "chat-export.json"),
            "source": source, "classification": classification,
        })

    def test_dry_run_writes_nothing(self) -> None:
        self._ingest("proj-a")
        before = store_stats(self.db)
        result = ingested_deletion.delete_ingested(
            self.db, scope="source", scope_key="proj-a", reason="test dry run",
            deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
            dry_run=True,
        )
        self.assertEqual("dry-run", result["status"])
        after = store_stats(self.db)
        self.assertEqual(before, after)
        self.assertEqual([], ingested_deletion.deletion_evidence(self.db))

    def test_delete_by_source_removes_messages_and_cascades_chunks(self) -> None:
        self._ingest("proj-a")
        before = store_stats(self.db)
        self.assertGreater(before["messages"], 0)
        self.assertGreater(before["chunks"], 0)

        result = ingested_deletion.delete_ingested(
            self.db, scope="source", scope_key="proj-a", reason="incident cleanup",
            deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
        )
        self.assertEqual("deleted", result["status"])
        self.assertEqual(before["messages"], result["message_count"])
        self.assertEqual(before["chunks"], result["chunk_count"])

        after = store_stats(self.db)
        self.assertEqual(0, after["messages"])
        self.assertEqual(0, after["chunks"], "chunks must cascade off deleted messages")

        evidence = ingested_deletion.deletion_evidence(self.db)
        self.assertEqual(1, len(evidence))
        row = evidence[0]
        self.assertNotIn("content", row)
        self.assertNotIn("body", row)
        self.assertNotIn("embedding_json", row)
        self.assertNotIn("source_uri", row)
        self.assertNotIn("conversation_title", row)
        self.assertNotIn("title", row)

    def test_content_digest_is_over_content_hashes_not_raw_content(self) -> None:
        self._ingest("proj-a")
        content_hashes = sorted(
            row["content_hash"] for row in self.db.execute("SELECT content_hash FROM messages")
        )
        import hashlib

        # message_digests preserve id order (ORDER BY id), not sorted order;
        # recompute the expected digest the same way _plan does, from the
        # id-ordered rows, rather than assuming sort order coincides.
        ordered_hashes = [
            row["content_hash"]
            for row in self.db.execute("SELECT content_hash FROM messages ORDER BY id")
        ]
        expected_digest = hashlib.sha256("".join(ordered_hashes).encode("utf-8")).hexdigest()

        result = ingested_deletion.delete_ingested(
            self.db, scope="source", scope_key="proj-a", reason="verify digest",
            deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
        )
        self.assertEqual(expected_digest, result["content_digest"])
        self.assertEqual(sorted(content_hashes), sorted(ordered_hashes))

    def test_delete_by_source_redacts_ingestion_runs_never_deletes_them(self) -> None:
        self._ingest("proj-a")
        run_before = self.db.execute("SELECT id, source_uri, error FROM ingestion_runs").fetchall()
        self.assertTrue(run_before)

        result = ingested_deletion.delete_ingested(
            self.db, scope="source", scope_key="proj-a", reason="incident cleanup",
            deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
        )
        run_after = self.db.execute("SELECT id, source_uri, error FROM ingestion_runs").fetchall()
        self.assertEqual(len(run_before), len(run_after), "ingestion_runs rows are never deleted")
        for row in run_after:
            self.assertIsNone(row["source_uri"])
            self.assertIsNone(row["error"])
        self.assertEqual(sorted(row["id"] for row in run_before), sorted(result["ingestion_runs_redacted"]))

    def test_delete_by_conversation_scope(self) -> None:
        self._ingest("proj-a")
        conversation_id = self.db.execute("SELECT conversation_id FROM messages LIMIT 1").fetchone()[0]
        result = ingested_deletion.delete_ingested(
            self.db, scope="conversation", scope_key=conversation_id, reason="wrong conversation",
            deleted_by="steward-a", authorized_by="human-a", trigger="classification-error",
        )
        self.assertEqual("deleted", result["status"])
        remaining = self.db.execute(
            "SELECT COUNT(*) FROM messages WHERE conversation_id = ?", (conversation_id,)
        ).fetchone()[0]
        self.assertEqual(0, remaining)
        # ingestion_runs untouched for conversation scope -- only source
        # scope redacts run provenance.
        self.assertEqual([], result["ingestion_runs_redacted"])

    def test_delete_by_message_scope_uses_internal_primary_key(self) -> None:
        self._ingest("proj-a")
        message_id, source_message_id = self.db.execute(
            "SELECT id, source_message_id FROM messages LIMIT 1"
        ).fetchone()
        # The public-facing source_message_id is NOT a legal --id for
        # message scope -- only the internal primary key is unambiguous.
        with self.assertRaises(ingested_deletion.IngestedDeletionError):
            ingested_deletion.delete_ingested(
                self.db, scope="message", scope_key=source_message_id, reason="wrong id shape",
                deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
            )
        result = ingested_deletion.delete_ingested(
            self.db, scope="message", scope_key=message_id, reason="single message removal",
            deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
        )
        self.assertEqual(1, result["message_count"])
        remaining = self.db.execute("SELECT COUNT(*) FROM messages WHERE id = ?", (message_id,)).fetchone()[0]
        self.assertEqual(0, remaining)

    def test_no_match_refuses_rather_than_writing_empty_evidence(self) -> None:
        self._ingest("proj-a")
        with self.assertRaisesRegex(ingested_deletion.IngestedDeletionError, "Nothing to delete"):
            ingested_deletion.delete_ingested(
                self.db, scope="source", scope_key="no-such-source", reason="test",
                deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
            )
        self.assertEqual([], ingested_deletion.deletion_evidence(self.db))

    def test_every_field_required_for_every_scope_and_classification(self) -> None:
        self._ingest("proj-a")
        cases = [
            {"reason": ""},
            {"deleted_by": ""},
            {"authorized_by": ""},
        ]
        for overrides in cases:
            with self.subTest(overrides=overrides):
                kwargs = dict(
                    scope="source", scope_key="proj-a", reason="r", deleted_by="d",
                    authorized_by="a", trigger="steward-decision",
                )
                kwargs.update(overrides)
                with self.assertRaises(ingested_deletion.IngestedDeletionError):
                    ingested_deletion.delete_ingested(self.db, **kwargs)
        # No authorized_by supplied at all (not just empty) also refuses --
        # restricted classification content included, showing the
        # requirement is not classification-conditional.
        with self.assertRaises(TypeError):
            ingested_deletion.delete_ingested(  # type: ignore[call-arg]
                self.db, scope="source", scope_key="proj-a", reason="r",
                deleted_by="d", trigger="steward-decision",
            )

    def test_reclaim_status_is_recorded_and_never_fails_the_deletion(self) -> None:
        self._ingest("proj-a")
        result = ingested_deletion.delete_ingested(
            self.db, scope="source", scope_key="proj-a", reason="test",
            deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
        )
        # Asserted against the specific expected normal-path value, not just
        # membership in the allowed set: a regression that always reports
        # "skipped" (e.g. wal_checkpoint or VACUUM silently no-op'ing) would
        # pass a membership-only assertion silently.
        self.assertEqual("vacuumed", result["reclaim_status"])
        evidence = ingested_deletion.deletion_evidence(self.db)
        self.assertEqual("vacuumed", evidence[0]["reclaim_status"])

    def test_delete_status_completed_on_success(self) -> None:
        self._ingest("proj-a")
        result = ingested_deletion.delete_ingested(
            self.db, scope="source", scope_key="proj-a", reason="test",
            deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
        )
        self.assertEqual("completed", result["delete_status"])
        evidence = ingested_deletion.deletion_evidence(self.db)
        self.assertEqual("completed", evidence[0]["delete_status"])

    def test_open_store_has_foreign_keys_on_before_delete_ingested_runs(self) -> None:
        """Regression-proof the happy path, not only the defensive check:
        the ordinary `open_store` connection this module is always handed in
        production (this test's `self.db`, from `setUp`, with no test-only
        pragma tweak) must already satisfy `_assert_foreign_keys_on`.
        """
        row = self.db.execute("PRAGMA foreign_keys").fetchone()
        self.assertEqual(1, row[0])

    def test_pragma_foreign_keys_must_be_on(self) -> None:
        self._ingest("proj-a")
        self.db.execute("PRAGMA foreign_keys = OFF")
        with self.assertRaisesRegex(ingested_deletion.IngestedDeletionError, "PRAGMA foreign_keys"):
            ingested_deletion.delete_ingested(
                self.db, scope="source", scope_key="proj-a", reason="test",
                deleted_by="steward-a", authorized_by="human-a", trigger="steward-decision",
            )
        self.assertEqual([], ingested_deletion.deletion_evidence(self.db))


class RetentionReportTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="ks-retention-report-")
        self.directory = Path(self.temporary.name)
        self.config = test_config(self.directory / "store.db")
        self.db = open_store(self.config["database"])

    def tearDown(self) -> None:
        self.db.close()
        self.temporary.cleanup()

    def test_report_lists_only_expired_content_never_bodies(self) -> None:
        ingest_file(self.db, self.config, {
            "input": str(ROOT / "examples" / "chat-export.json"),
            "source": "expiring", "classification": "internal", "retention_days": 1,
        })
        ingest_file(self.db, self.config, {
            "input": str(ROOT / "examples" / "chat-export.json"),
            "source": "durable", "classification": "public",
        })
        # As-of far in the future: only the 1-day-window source is expired.
        future = "2099-01-01T00:00:00.000Z"
        report = ingested_deletion.retention_report(self.db, as_of=future)
        self.assertGreater(report["expired_message_count"], 0)
        sources = {item["source"] for item in report["items"]}
        self.assertEqual({"expiring"}, sources)
        for item in report["items"]:
            self.assertNotIn("content", item)
            self.assertIn("id", item)
            self.assertIn("retention_until", item)

    def test_report_never_deletes(self) -> None:
        ingest_file(self.db, self.config, {
            "input": str(ROOT / "examples" / "chat-export.json"),
            "source": "expiring", "classification": "internal", "retention_days": 1,
        })
        before = store_stats(self.db)
        ingested_deletion.retention_report(self.db, as_of="2099-01-01T00:00:00.000Z")
        after = store_stats(self.db)
        self.assertEqual(before, after)


class CliWiringTests(unittest.TestCase):
    """`delete-ingested`/`retention-report` reachable through `cli.run`, and
    the shared global-fallback tier's --source requirement."""

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory(prefix="ks-cli-ingested-deletion-")
        self.directory = Path(self.temporary.name)
        self.xdg_config_home = isolate_settings(self)

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _project_env(self):
        from unittest import mock
        import os

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

    def test_cli_round_trip_project_local(self) -> None:
        env_patch, cwd_patch = self._project_env()
        with env_patch, cwd_patch:
            run(["init"])
            # Explicit window, because the shipped defaults are all indefinite
            # until the retention decision lands (config.py's `retention`
            # block): with no window recorded, nothing ever expires and this
            # round trip would report nothing to act on. The window is
            # incidental here -- what this test covers is the CLI wiring from
            # report through dry-run, delete, and evidence.
            run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--source", "proj-a", "--classification", "internal",
                "--retention-days", "30",
            ])
            report = run(["retention-report", "--as-of", "2099-01-01T00:00:00.000Z"])
            self.assertGreater(report["expired_message_count"], 0)

            dry = run([
                "delete-ingested", "--scope", "source", "--id", "proj-a",
                "--reason", "cli round trip", "--deleted-by", "steward-a",
                "--authorized-by", "human-a", "--trigger", "steward-decision", "--dry-run",
            ])
            self.assertEqual("dry-run", dry["status"])

            deleted = run([
                "delete-ingested", "--scope", "source", "--id", "proj-a",
                "--reason", "cli round trip", "--deleted-by", "steward-a",
                "--authorized-by", "human-a", "--trigger", "steward-decision",
            ])
            self.assertEqual("deleted", deleted["status"])

            evidence = run(["deletion-evidence"])
            kinds = {entry["kind"] for entry in evidence["deletions"]}
            self.assertIn("ingested", kinds)

    def test_global_fallback_requires_explicit_source(self) -> None:
        from unittest import mock
        import os

        cwd = self.directory / "no-project-local"
        cwd.mkdir()
        home = self.directory / "global-home"
        home.mkdir()
        env_patch = mock.patch.dict(os.environ, {"KNOWLEDGE_STORE_HOME": str(home)})
        cwd_patch = mock.patch("config.Path.cwd", return_value=cwd)
        with env_patch, cwd_patch:
            run(["init"])
            run([
                "ingest", "--input", str(ROOT / "examples" / "chat-export.json"),
                "--source", "proj-a", "--classification", "internal",
            ])
            with self.assertRaisesRegex(ValueError, "A project scope is required"):
                run([
                    "delete-ingested", "--scope", "source", "--id", "proj-a",
                    "--reason", "cli round trip", "--deleted-by", "steward-a",
                    "--authorized-by", "human-a", "--trigger", "steward-decision",
                ])
            deleted = run([
                "delete-ingested", "--scope", "source", "--id", "proj-a", "--source", "proj-a",
                "--reason", "cli round trip", "--deleted-by", "steward-a",
                "--authorized-by", "human-a", "--trigger", "steward-decision",
            ])
            self.assertEqual("deleted", deleted["status"])


if __name__ == "__main__":
    unittest.main()
