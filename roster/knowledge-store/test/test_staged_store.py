"""Round-trip and storage tests for the staged-record SQLite backend.

The load-bearing property is that `parse(serialize(record)) == record`. If it
does not hold, the export path from the proposal is a corruption vector rather
than a backup, and the loss would only be discovered when the store was gone.
So it is asserted here against every record committed to the repository today,
not against a synthetic fixture that happens to be easy to serialise.
"""

from __future__ import annotations

import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path

SRC = Path(__file__).resolve().parents[1] / "src"
RECORDS = Path(__file__).resolve().parents[1] / "proposed-knowledge"
if str(SRC) not in sys.path:
    sys.path.insert(0, str(SRC))

import staged_records  # noqa: E402
import staged_store  # noqa: E402


def _open_memory_store() -> sqlite3.Connection:
    db = sqlite3.connect(":memory:")
    db.row_factory = sqlite3.Row
    # Mirror open_store's pragmas. Without foreign_keys = ON these tests ran
    # under conditions the real CLI never sees, and that difference hid a
    # destructive bug: INSERT OR REPLACE in put_record deleted the row and
    # cascaded away the whole disposition history, invisibly here and for
    # real in production.
    db.execute("PRAGMA foreign_keys = ON")
    staged_store.install_schema(db)
    return db


def _committed_records() -> list[Path]:
    return sorted(RECORDS.glob("*.md"))


class SchemaTests(unittest.TestCase):
    def test_install_schema_is_idempotent(self) -> None:
        # Matches database.py's CREATE TABLE IF NOT EXISTS contract: an
        # existing store must pick the table up without a migration step.
        db = _open_memory_store()
        staged_store.install_schema(db)
        staged_store.install_schema(db)
        names = {row["name"] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        self.assertIn("staged_records", names)

    def test_schema_does_not_disturb_the_existing_store_tables(self) -> None:
        # The staged-record table is additive. Opening a real store and then
        # installing this schema must leave the existing tables intact.
        # tempfile + addCleanup rather than enterContext: that is 3.11+, and
        # this repository's declared floor and CI matrix both include 3.10.
        import database

        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        db = database.open_store(str(Path(directory.name) / "knowledge.db"))
        self.addCleanup(db.close)
        query = "SELECT name FROM sqlite_master WHERE type='table'"
        before = {row["name"] for row in db.execute(query)}
        self.assertIn("messages", before)
        staged_store.install_schema(db)
        after = {row["name"] for row in db.execute(query)}
        self.assertTrue(before <= after, "installing the staged-record schema dropped an existing table")
        self.assertIn("staged_records", after)


class RoundTripTests(unittest.TestCase):
    """The property the durability story depends on."""

    def test_every_committed_record_round_trips_through_the_store(self) -> None:
        records = _committed_records()
        self.assertGreaterEqual(len(records), 3, "expected the committed corpus to be non-trivial")
        db = _open_memory_store()
        for path in records:
            with self.subTest(record=path.name):
                original = path.read_text(encoding="utf-8")
                frontmatter, body = staged_records.parse_record(original)
                staged_store.put_record_text(db, original)

                exported = staged_store.get_record_text(db, frontmatter["id"])
                self.assertIsNotNone(exported)

                reparsed_fm, reparsed_body = staged_records.parse_record(exported)
                self.assertEqual(reparsed_fm, frontmatter, "frontmatter changed across the round trip")
                self.assertEqual(
                    staged_records.compute_digest(reparsed_body),
                    staged_records.compute_digest(body),
                    "body digest changed across the round trip",
                )
                self.assertEqual(
                    reparsed_fm["content_digest"],
                    staged_records.compute_digest(reparsed_body),
                    "the exported record's declared digest no longer matches its own body",
                )

    def test_exported_records_still_validate(self) -> None:
        # Round-tripping must produce something the contract accepts, not
        # merely something the parser can read back.
        db = _open_memory_store()
        for path in _committed_records():
            staged_store.put_record_text(db, path.read_text(encoding="utf-8"))
        for record_id, text in staged_store.export_records(db).items():
            with self.subTest(record=record_id):
                self.assertEqual(staged_records.validate_record(text), [])

    def test_round_trip_is_stable_under_repetition(self) -> None:
        # serialize(parse(serialize(x))) == serialize(x): a second pass must
        # not keep rewriting the record, or every export would show spurious
        # diffs and the durability path would be unreviewable.
        db = _open_memory_store()
        path = _committed_records()[0]
        staged_store.put_record_text(db, path.read_text(encoding="utf-8"))
        record_id = staged_records.parse_record(path.read_text(encoding="utf-8"))[0]["id"]
        once = staged_store.get_record_text(db, record_id)
        staged_store.put_record_text(db, once)
        twice = staged_store.get_record_text(db, record_id)
        self.assertEqual(once, twice)

    def test_disposition_history_survives_the_round_trip(self) -> None:
        # Two of the committed records carry a disposition. Losing it would be
        # losing the audit trail, which is the thing that made this mechanism
        # credible in the first place.
        db = _open_memory_store()
        dispositioned = 0
        for path in _committed_records():
            text = path.read_text(encoding="utf-8")
            frontmatter, _ = staged_records.parse_record(text)
            if "disposition" not in frontmatter:
                continue
            dispositioned += 1
            staged_store.put_record_text(db, text)
            reparsed, _ = staged_records.parse_record(
                staged_store.get_record_text(db, frontmatter["id"])
            )
            self.assertEqual(reparsed["disposition"], frontmatter["disposition"])
            self.assertNotEqual(reparsed["status"], "proposed")
        self.assertGreater(dispositioned, 0, "expected at least one dispositioned record in the corpus")


class CorruptionTests(unittest.TestCase):
    """A corrupted export must fail on the way back in, not import silently."""

    def _first_record(self) -> tuple[dict, str, str]:
        path = _committed_records()[0]
        text = path.read_text(encoding="utf-8")
        frontmatter, body = staged_records.parse_record(text)
        return frontmatter, body, text

    def test_a_body_edited_without_recomputing_the_digest_is_rejected(self) -> None:
        frontmatter, body, _ = self._first_record()
        db = _open_memory_store()
        with self.assertRaises(staged_store.StagedRecordError) as caught:
            staged_store.put_record(db, frontmatter, body + "\n\nsmuggled in after the digest was taken")
        self.assertIn("content_digest", str(caught.exception))

    def test_a_forbidden_action_is_rejected_on_write(self) -> None:
        frontmatter, body, _ = self._first_record()
        frontmatter = dict(frontmatter, recommended_action="delete")
        db = _open_memory_store()
        with self.assertRaises(staged_store.StagedRecordError) as caught:
            staged_store.put_record(db, frontmatter, body)
        self.assertIn("delete", str(caught.exception))

    def test_a_dropped_required_key_is_rejected_on_write(self) -> None:
        frontmatter, body, _ = self._first_record()
        frontmatter = {k: v for k, v in frontmatter.items() if k != "source_scope"}
        db = _open_memory_store()
        with self.assertRaises(staged_store.StagedRecordError) as caught:
            staged_store.put_record(db, frontmatter, body)
        self.assertIn("source_scope", str(caught.exception))

    def test_an_invalid_record_is_not_written(self) -> None:
        # Validation happens before the write, so a rejected record must leave
        # no row behind -- "never existed" rather than "caught later".
        frontmatter, body, _ = self._first_record()
        db = _open_memory_store()
        with self.assertRaises(staged_store.StagedRecordError):
            staged_store.put_record(db, dict(frontmatter, status="archived"), body)
        self.assertIsNone(staged_store.get_record(db, frontmatter["id"]))
        self.assertEqual(staged_store.list_records(db), [])


class SerialisationTests(unittest.TestCase):
    def test_booleans_survive_as_booleans(self) -> None:
        # Quoting a bool would round-trip `false` into the string "false",
        # which the contract's enum check would then accept as a value while
        # the automatic-defer rule silently stopped seeing it as false.
        frontmatter, body, _ = CorruptionTests()._first_record()
        text = staged_store.serialize_record(frontmatter, body)
        reparsed, _ = staged_records.parse_record(text)
        self.assertIs(reparsed["untrusted_instruction_risk"], frontmatter["untrusted_instruction_risk"])

    def test_a_string_containing_quotes_and_colons_round_trips(self) -> None:
        frontmatter, body, _ = CorruptionTests()._first_record()
        awkward = 'he said "yes": then a backslash \\ and a trailing space '
        frontmatter = dict(frontmatter, sensitivity_notes=awkward)
        text = staged_store.serialize_record(frontmatter, body)
        reparsed, _ = staged_records.parse_record(text)
        self.assertEqual(reparsed["sensitivity_notes"], awkward)

    def test_a_value_that_looks_like_a_yaml_keyword_stays_a_string(self) -> None:
        frontmatter, body, _ = CorruptionTests()._first_record()
        for awkward in ("null", "true", "~", "- not a list", "[]"):
            with self.subTest(value=awkward):
                candidate = dict(frontmatter, source_scope=awkward)
                reparsed, _ = staged_records.parse_record(
                    staged_store.serialize_record(candidate, body)
                )
                self.assertEqual(reparsed["source_scope"], awkward)

    def test_key_order_is_fixed_so_exports_diff_on_content(self) -> None:
        frontmatter, body, _ = CorruptionTests()._first_record()
        shuffled = dict(reversed(list(frontmatter.items())))
        self.assertEqual(
            staged_store.serialize_record(frontmatter, body),
            staged_store.serialize_record(shuffled, body),
        )

    def test_an_unrecognised_key_is_emitted_rather_than_dropped(self) -> None:
        # Silently discarding an unknown key is how an export loses data. It is
        # emitted, and the validator rejects it on the way back in -- loudly.
        frontmatter, body, _ = CorruptionTests()._first_record()
        text = staged_store.serialize_record(dict(frontmatter, unexpected="kept"), body)
        self.assertIn("unexpected", text)
        self.assertNotEqual(staged_records.validate_record(text), [])


class StorageTests(unittest.TestCase):
    def test_replacing_a_record_preserves_created_at(self) -> None:
        # A steward amending a disposition updates one record; it does not
        # create a second one, and the original staging time is part of the
        # audit trail.
        frontmatter, body, _ = CorruptionTests()._first_record()
        db = _open_memory_store()
        staged_store.put_record(db, frontmatter, body)
        first = db.execute(
            "SELECT created_at FROM staged_records WHERE id = ?", (frontmatter["id"],)
        ).fetchone()["created_at"]
        staged_store.put_record(db, frontmatter, body)
        rows = db.execute("SELECT created_at FROM staged_records").fetchall()
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["created_at"], first)

    def test_list_filters_by_status_and_is_ordered(self) -> None:
        db = _open_memory_store()
        for path in _committed_records():
            staged_store.put_record_text(db, path.read_text(encoding="utf-8"))
        everything = staged_store.list_records(db)
        self.assertEqual([r["id"] for r in everything], sorted(r["id"] for r in everything))
        proposed = staged_store.list_records(db, status="proposed")
        self.assertTrue(proposed)
        self.assertTrue(all(r["status"] == "proposed" for r in proposed))
        self.assertLess(len(proposed), len(everything), "corpus should include a dispositioned record")

    def test_get_and_export_return_nothing_for_an_unknown_id(self) -> None:
        db = _open_memory_store()
        self.assertIsNone(staged_store.get_record(db, "KS-20260101-nope"))
        self.assertIsNone(staged_store.get_record_text(db, "KS-20260101-nope"))
        self.assertEqual(staged_store.export_records(db), {})


if __name__ == "__main__":
    unittest.main()
