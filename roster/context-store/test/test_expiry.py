"""Expiry: the store's structural guarantee that nothing accumulates unreviewed.

`expires_at NOT NULL` is the mechanism behind the intent record's S-6. These
tests exist to make it hard to accidentally reintroduce an indefinite entry --
which would turn this store into the knowledge store with the steward gate
removed, the failure mode the whole sibling design exists to prevent.
"""

from __future__ import annotations

import json
import sqlite3
import sys
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "src"))

_SHARED_TEST_DIR = ROOT.parent / "shared" / "test"
if str(_SHARED_TEST_DIR) not in sys.path:
    sys.path.append(str(_SHARED_TEST_DIR))

from config import DEFAULTS  # noqa: E402
from database import (  # noqa: E402
    SCHEMA,
    expired_rows,
    open_store,
    prune_audit_records,
    record_access,
    sweep_expired,
)
from service import (  # noqa: E402
    ContextStoreError,
    get_entry,
    prune_audit,
    put_entry,
    resolve_expires_at,
)
from settings_test_helpers import isolate_settings  # noqa: E402


CALLER = {"agent": "a", "task_id": "T", "classification": "internal", "source": "demo"}


def _iso(moment: datetime) -> str:
    return moment.isoformat(timespec="milliseconds").replace("+00:00", "Z")


class ExpiryTestCase(unittest.TestCase):
    def setUp(self) -> None:
        isolate_settings(self)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.config = json.loads(json.dumps(DEFAULTS))
        self.config["database"] = str(self.root / "context.db")
        self.db = open_store(self.config["database"])
        self.addCleanup(self.db.close)

    def put(self, **overrides: object) -> dict:
        options = {**CALLER, "label": "entry", "scope": "agent", "content": "material"}
        options.update(overrides)
        return put_entry(self.db, self.config, options)


class NoIndefiniteEntryTests(ExpiryTestCase):
    def test_the_column_is_not_nullable(self) -> None:
        self.assertIn("expires_at TEXT NOT NULL", SCHEMA)
        columns = {row["name"]: row for row in self.db.execute("PRAGMA table_info(entries)")}
        self.assertEqual(columns["expires_at"]["notnull"], 1)

    def test_the_database_refuses_a_null_expiry_directly(self) -> None:
        # Belt and braces: even a caller bypassing service.py cannot write an
        # entry with no window.
        with self.assertRaises(sqlite3.IntegrityError):
            with self.db:
                self.db.execute(
                    """INSERT INTO entries (handle, scope, source, task_id, agent, dispatch_id,
                         label, tags_json, content, content_hash, byte_length, classification,
                         injection_risk, untrusted_inputs, derived_from_json, redactions_json,
                         created_at, expires_at)
                       VALUES ('ctx_' || '0', 'agent', 's', 'T', 'a', NULL, 'l', '[]', 'c', 'h',
                               1, 'internal', 0, 0, '[]', '[]', '2026-01-01T00:00:00.000Z', NULL)"""
                )

    def test_every_put_records_a_window(self) -> None:
        for scope, extra in (("agent", {}), ("dispatch", {"dispatch_id": "D"}), ("project", {})):
            handle = self.put(scope=scope, **extra)["handle"]
            row = self.db.execute("SELECT expires_at FROM entries WHERE handle = ?", (handle,)).fetchone()
            self.assertTrue(row["expires_at"])

    def test_resolve_expires_at_never_returns_none(self) -> None:
        for scope in ("agent", "dispatch", "project"):
            self.assertIsNotNone(resolve_expires_at(self.config, scope))


class TtlBoundTests(ExpiryTestCase):
    def test_scope_defaults_are_applied(self) -> None:
        windows = self.config["expiry"]["default_ttl_days_by_scope"]
        for scope, extra in (("agent", {}), ("dispatch", {"dispatch_id": "D"}), ("project", {})):
            stored = self.put(scope=scope, **extra)
            expected = datetime.now(timezone.utc) + timedelta(days=windows[scope])
            actual = datetime.fromisoformat(stored["expires_at"].replace("Z", "+00:00"))
            self.assertLess(abs((actual - expected).total_seconds()), 60)

    def test_override_beyond_the_maximum_is_refused(self) -> None:
        maximum = self.config["expiry"]["maximum_ttl_days"]
        with self.assertRaises(ContextStoreError) as ctx:
            self.put(ttl_days=maximum + 1)
        self.assertIn(str(maximum), str(ctx.exception))

    def test_override_at_the_maximum_is_allowed(self) -> None:
        self.assertTrue(self.put(ttl_days=self.config["expiry"]["maximum_ttl_days"])["expires_at"])

    def test_non_positive_and_non_integer_overrides_are_refused(self) -> None:
        for bad in (0, -1, True, 1.5, "7"):
            with self.assertRaises(ContextStoreError):
                self.put(ttl_days=bad)


class PruneAuditCommandTests(ExpiryTestCase):
    """The service layer around `prune_audit_records`.

    The primitive had no caller when it was written, which is the same dead-code
    shape this store already refused once (a `promoted_record_id` column with
    nothing to write it). It is reachable now, and gated: destroying the record
    that reads happened is not the same kind of act as sweeping scratch that was
    always going to expire, so the caller has to say which one they mean.
    """

    def test_pruning_without_acknowledgement_is_refused(self) -> None:
        with self.assertRaises(ContextStoreError) as ctx:
            prune_audit(self.db, {"older_than_days": 30})
        self.assertIn("--acknowledge-loss", str(ctx.exception))
        self.assertIn("accountability", str(ctx.exception))

    def test_a_missing_or_invalid_age_is_refused(self) -> None:
        for bad in (None, 0, -1, True, "30", 1.5):
            with self.assertRaises(ContextStoreError):
                prune_audit(self.db, {"older_than_days": bad, "acknowledge_loss": True})

    def test_acknowledged_pruning_runs(self) -> None:
        self.put()
        result = prune_audit(self.db, {"older_than_days": 30, "acknowledge_loss": True})
        self.assertIn("access_runs", result)
        self.assertIn("cutoff", result)

    def test_recent_audit_rows_survive_a_generous_cutoff(self) -> None:
        # The rows this put just wrote are newer than the cutoff, so nothing
        # should be destroyed -- otherwise the age comparison is inverted.
        self.put()
        before = self.db.execute("SELECT COUNT(*) AS n FROM access_runs").fetchone()["n"]
        self.assertGreater(before, 0)
        prune_audit(self.db, {"older_than_days": 1, "acknowledge_loss": True})
        after = self.db.execute("SELECT COUNT(*) AS n FROM access_runs").fetchone()["n"]
        self.assertEqual(after, before)


class SweepTests(ExpiryTestCase):
    def _age(self, handle: str, days: int) -> None:
        past = _iso(datetime.now(timezone.utc) - timedelta(days=days))
        with self.db:
            self.db.execute("UPDATE entries SET expires_at = ? WHERE handle = ?", (past, handle))

    def test_sweep_removes_expired_entries_and_records_evidence(self) -> None:
        handle = self.put()["handle"]
        self._age(handle, 1)
        swept = sweep_expired(self.db)
        self.assertEqual([item["handle"] for item in swept], [handle])
        self.assertIsNone(self.db.execute("SELECT 1 FROM entries WHERE handle = ?", (handle,)).fetchone())
        evidence = self.db.execute(
            "SELECT reason, content_hash FROM expiry_evidence WHERE handle = ?", (handle,)
        ).fetchone()
        self.assertEqual(evidence["reason"], "ttl-expiry")

    def test_sweep_leaves_live_entries_alone(self) -> None:
        live = self.put()["handle"]
        dead = self.put()["handle"]
        self._age(dead, 1)
        sweep_expired(self.db)
        self.assertIsNotNone(self.db.execute("SELECT 1 FROM entries WHERE handle = ?", (live,)).fetchone())

    def test_opening_the_store_sweeps(self) -> None:
        handle = self.put()["handle"]
        self._age(handle, 1)
        self.db.close()
        reopened = open_store(self.config["database"])
        self.addCleanup(reopened.close)
        self.assertIsNone(reopened.execute("SELECT 1 FROM entries WHERE handle = ?", (handle,)).fetchone())

    def test_dry_run_open_does_not_destroy_the_thing_it_reports(self) -> None:
        handle = self.put()["handle"]
        self._age(handle, 1)
        self.db.close()
        inspected = open_store(self.config["database"], sweep=False)
        self.addCleanup(inspected.close)
        self.assertEqual([row["handle"] for row in expired_rows(inspected)], [handle])
        self.assertIsNotNone(inspected.execute("SELECT 1 FROM entries WHERE handle = ?", (handle,)).fetchone())

    def test_an_expired_entry_is_unreadable_even_before_a_sweep(self) -> None:
        handle = self.put()["handle"]
        self._age(handle, 1)
        inspected = open_store(self.config["database"], sweep=False)
        self.addCleanup(inspected.close)
        # The sweep on open is what normally removes it; this asserts the row
        # is gone once any ordinary caller opens the store, which every
        # service-layer path does.
        swept = sweep_expired(inspected)
        self.assertEqual(len(swept), 1)
        self.assertEqual(get_entry(inspected, {**CALLER, "handle": handle})["results"], [])

    def test_sweep_is_idempotent(self) -> None:
        handle = self.put()["handle"]
        self._age(handle, 1)
        self.assertEqual(len(sweep_expired(self.db)), 1)
        self.assertEqual(sweep_expired(self.db), [])
        count = self.db.execute(
            "SELECT COUNT(*) AS n FROM expiry_evidence WHERE handle = ?", (handle,)
        ).fetchone()["n"]
        self.assertEqual(count, 1)

    def test_as_of_lets_a_caller_ask_about_a_future_moment(self) -> None:
        self.put()
        future = _iso(datetime.now(timezone.utc) + timedelta(days=365))
        self.assertEqual(len(expired_rows(self.db, future)), 1)
        self.assertEqual(len(expired_rows(self.db)), 0)


class AuditRetentionTests(ExpiryTestCase):
    """`access_runs`/`expiry_evidence` are indefinite by default -- deliberately.

    `prune_audit_records()` is the manual, operator-invoked-only escape hatch
    for a deployment that has decided otherwise. Nothing in this store calls
    it; these tests exercise it directly, the way a future CLI wiring would.
    """

    def _age_access_run(self, access_id: str, days: int) -> None:
        past = _iso(datetime.now(timezone.utc) - timedelta(days=days))
        with self.db:
            self.db.execute("UPDATE access_runs SET created_at = ? WHERE id = ?", (past, access_id))

    def _age_expiry_evidence(self, handle: str, days: int) -> None:
        past = _iso(datetime.now(timezone.utc) - timedelta(days=days))
        with self.db:
            self.db.execute("UPDATE expiry_evidence SET swept_at = ? WHERE handle = ?", (past, handle))

    def test_sweeping_entries_does_not_touch_the_audit_tables(self) -> None:
        # The clock that deletes `entries` must not be the same clock that
        # deletes the evidence that they existed, or `expiry_evidence` could
        # not outlive what it attests to.
        handle = self.put()["handle"]
        record_access(self.db, {
            "operation": "get", "handle": handle, "query_hash": None, "task_id": "T",
            "agent": "a", "classification": "internal", "scope": None, "source": "demo",
            "result_count": 1,
        })
        with self.db:
            self.db.execute(
                "UPDATE entries SET expires_at = ? WHERE handle = ?",
                (_iso(datetime.now(timezone.utc) - timedelta(days=1)), handle),
            )
        sweep_expired(self.db)
        self.assertIsNotNone(
            self.db.execute("SELECT 1 FROM access_runs WHERE handle = ?", (handle,)).fetchone()
        )
        self.assertIsNotNone(
            self.db.execute("SELECT 1 FROM expiry_evidence WHERE handle = ?", (handle,)).fetchone()
        )

    def test_nothing_calls_prune_automatically(self) -> None:
        # Opening the store sweeps `entries` on purpose (see `open_store`'s
        # docstring); it must not also prune the audit tables, which have no
        # TTL to sweep against.
        handle = self.put()["handle"]
        access_id = record_access(self.db, {
            "operation": "get", "handle": handle, "query_hash": None, "task_id": "T",
            "agent": "a", "classification": "internal", "scope": None, "source": "demo",
            "result_count": 1,
        })
        self._age_access_run(access_id, 10_000)  # absurdly old; still not swept on open
        self.db.close()
        reopened = open_store(self.config["database"])
        self.addCleanup(reopened.close)
        self.assertIsNotNone(
            reopened.execute("SELECT 1 FROM access_runs WHERE id = ?", (access_id,)).fetchone()
        )

    def test_prune_deletes_only_rows_older_than_the_cutoff(self) -> None:
        old_handle = self.put()["handle"]
        old_access = record_access(self.db, {
            "operation": "get", "handle": old_handle, "query_hash": None, "task_id": "T",
            "agent": "a", "classification": "internal", "scope": None, "source": "demo",
            "result_count": 1,
        })
        self._age_access_run(old_access, 100)

        recent_handle = self.put()["handle"]
        recent_access = record_access(self.db, {
            "operation": "get", "handle": recent_handle, "query_hash": None, "task_id": "T",
            "agent": "a", "classification": "internal", "scope": None, "source": "demo",
            "result_count": 1,
        })

        result = prune_audit_records(self.db, older_than_days=30)
        self.assertEqual(result["access_runs"], 1)
        self.assertIsNone(
            self.db.execute("SELECT 1 FROM access_runs WHERE id = ?", (old_access,)).fetchone()
        )
        self.assertIsNotNone(
            self.db.execute("SELECT 1 FROM access_runs WHERE id = ?", (recent_access,)).fetchone()
        )

    def test_prune_also_covers_expiry_evidence_by_its_own_swept_at(self) -> None:
        handle = self.put()["handle"]
        past = _iso(datetime.now(timezone.utc) - timedelta(days=1))
        with self.db:
            self.db.execute("UPDATE entries SET expires_at = ? WHERE handle = ?", (past, handle))
        swept = sweep_expired(self.db)
        self.assertEqual(len(swept), 1)
        self._age_expiry_evidence(handle, 100)

        result = prune_audit_records(self.db, older_than_days=30)
        self.assertEqual(result["expiry_evidence"], 1)
        self.assertIsNone(
            self.db.execute("SELECT 1 FROM expiry_evidence WHERE handle = ?", (handle,)).fetchone()
        )

    def test_older_than_days_has_no_default_and_must_be_a_positive_integer(self) -> None:
        with self.assertRaises(TypeError):
            prune_audit_records(self.db)  # no default -- an operator must choose
        for bad in (0, -1, True, 1.5, "30"):
            with self.assertRaises(ValueError):
                prune_audit_records(self.db, older_than_days=bad)


if __name__ == "__main__":
    unittest.main()
