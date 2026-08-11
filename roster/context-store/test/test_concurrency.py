"""`open_store()`'s busy-timeout behaviour under real write contention.

This store's stated purpose is absorbing high-churn concurrent agent writes,
which makes overlapping writers the ordinary case rather than the exotic one.
`sqlite3.connect()` already retries a locked write for its own `timeout`
parameter (5s by default) before raising `OperationalError`, but that window
was an accident of the stdlib default -- nothing in `database.py` stated the
value or chose it deliberately. These tests exercise the explicit
`PRAGMA busy_timeout` `open_store()` now sets, using real connections and real
contention rather than mocking sqlite's locking behaviour, because a mock of
`sqlite3.OperationalError` would prove the code calls the right API and
nothing about whether concurrent writers actually survive contention.

    python3 -m unittest discover -s roster/context-store/test -p "test_*.py"
"""

from __future__ import annotations

import json
import sqlite3
import sys
import tempfile
import threading
import time
import unittest
import uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "src"))

from database import content_hash, insert_entry, now_iso, open_store  # noqa: E402

# Deliberately larger than sqlite3.connect()'s stdlib default (5s) so a test
# holding a write lock for longer than the default -- but less than this
# value -- fails against code that relies on the implicit default and passes
# once `open_store()` states the value explicitly. See database.py's
# `PRAGMA busy_timeout` comment for why this value was chosen.
EXPECTED_BUSY_TIMEOUT_MS = 10_000

# Longer than the stdlib's implicit 5s default, shorter than
# EXPECTED_BUSY_TIMEOUT_MS -- the gap a store relying only on the accidental
# default would fail inside, and the explicit setting should not.
LOCK_HOLD_SECONDS = 5.5


def _entry(handle: str) -> dict:
    content = f"material for {handle}"
    return {
        "handle": handle, "scope": "agent", "source": "demo", "task_id": "T", "agent": "a",
        "dispatch_id": None, "label": "entry", "tags": [], "content": content,
        "content_hash": content_hash(content), "byte_length": len(content.encode("utf-8")),
        "classification": "internal", "injection_risk": False, "untrusted_inputs": False,
        "derived_from": [], "redactions": [], "created_at": now_iso(), "expires_at": now_iso(),
    }


class BusyTimeoutTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.database_path = str(Path(self.tmp.name) / "context.db")


class PragmaValueTests(BusyTimeoutTestCase):
    def test_busy_timeout_is_set_explicitly_rather_than_left_to_the_stdlib_default(self) -> None:
        db = open_store(self.database_path, sweep=False)
        self.addCleanup(db.close)
        (configured,) = db.execute("PRAGMA busy_timeout").fetchone()
        self.assertEqual(configured, EXPECTED_BUSY_TIMEOUT_MS)

    def test_a_second_connection_to_the_same_database_gets_it_too(self) -> None:
        # Not a per-database-file setting -- every connection sets its own
        # busy timeout on open, so a second connection is not left relying on
        # whatever the first one happened to configure.
        first = open_store(self.database_path, sweep=False)
        self.addCleanup(first.close)
        second = open_store(self.database_path, sweep=False)
        self.addCleanup(second.close)
        self.assertEqual(second.execute("PRAGMA busy_timeout").fetchone()[0], EXPECTED_BUSY_TIMEOUT_MS)


class RealContentionTests(BusyTimeoutTestCase):
    """Two real `sqlite3` connections, two real threads, one real file lock."""

    def test_a_concurrent_writer_waits_out_the_lock_instead_of_failing(self) -> None:
        # sqlite3 connections are usable only from the thread that created
        # them, so each thread below opens (and closes) its own -- opening
        # `holder` here in the main thread and using it from a worker thread
        # would raise ProgrammingError before contention is ever exercised.
        released = threading.Event()
        outcome: dict[str, object] = {}

        def hold_the_write_lock() -> None:
            holder = open_store(self.database_path, sweep=False)
            try:
                # BEGIN IMMEDIATE takes the write lock up front, rather than
                # waiting for the first write inside the transaction to
                # acquire it -- that is what makes the timing in this test
                # deterministic.
                holder.execute("BEGIN IMMEDIATE")
                insert_entry(holder, _entry(f"ctx_{uuid.uuid4().hex}"))
                time.sleep(LOCK_HOLD_SECONDS)
                holder.commit()
                released.set()
            finally:
                holder.close()

        def contend_for_it() -> None:
            # A separate connection, opened and used entirely on this thread --
            # sqlite3 connections are not shareable across threads.
            contender = open_store(self.database_path, sweep=False)
            try:
                started = time.monotonic()
                try:
                    insert_entry(contender, _entry(f"ctx_{uuid.uuid4().hex}"))
                    outcome["status"] = "ok"
                except sqlite3.OperationalError as error:
                    outcome["status"] = f"error: {error}"
                outcome["elapsed"] = time.monotonic() - started
                outcome["lock_was_released_first"] = released.is_set()
            finally:
                contender.close()

        holder_thread = threading.Thread(target=hold_the_write_lock)
        contender_thread = threading.Thread(target=contend_for_it)
        holder_thread.start()
        time.sleep(0.2)  # let the holder actually take the lock first
        contender_thread.start()
        holder_thread.join(timeout=LOCK_HOLD_SECONDS + 5)
        contender_thread.join(timeout=LOCK_HOLD_SECONDS + 5)

        self.assertEqual(outcome.get("status"), "ok", outcome.get("status"))
        # The write only succeeded once the lock was actually released -- this
        # rules out a false pass from WAL letting an unrelated write through.
        self.assertTrue(outcome["lock_was_released_first"])
        self.assertGreaterEqual(outcome["elapsed"], LOCK_HOLD_SECONDS - 0.3)


if __name__ == "__main__":
    unittest.main()
