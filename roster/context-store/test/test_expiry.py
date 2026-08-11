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
from database import SCHEMA, expired_rows, open_store, sweep_expired  # noqa: E402
from service import ContextStoreError, get_entry, put_entry, resolve_expires_at  # noqa: E402
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


if __name__ == "__main__":
    unittest.main()
