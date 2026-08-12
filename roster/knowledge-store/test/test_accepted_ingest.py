"""G-7: steward-accepted staged records become retrievable.

The gap these tests pin is not a bug in any one function — every part worked.
Findings were captured, staged, and dispositioned correctly; `search_store()`
scored `chunks`; staged records lived in `staged_records`. **Nothing joined the
two**, so an accepted finding was permanently unreachable by any query.

The cost was measured before it was fixed: a session re-derived from scratch two
findings that were sitting in `staged_records`, accepted, describing the exact
problems it was re-solving.

`test_an_accepted_record_becomes_retrievable` is the one that matters — it fails
against the store as it was, and no other test in this suite could.
"""

from __future__ import annotations

import json
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "src"
if str(SRC) not in sys.path:
    sys.path.insert(0, str(SRC))
if str(ROOT.parent / "shared" / "src") not in sys.path:
    sys.path.insert(0, str(ROOT.parent / "shared" / "src"))

from accepted_ingest import (  # noqa: E402
    STAGED_SOURCE,
    already_ingested,
    ingest_accepted,
)
from config import load_config  # noqa: E402
from database import open_store  # noqa: E402
from service import search_store  # noqa: E402
from staged_records import compute_digest  # noqa: E402
from staged_store import disposition_record, install_schema, put_record  # noqa: E402

BODY = (
    "Prove a guard is non-vacuous by injecting a fault: plant the defect, "
    "confirm the check fails naming the real cause, revert, confirm clean. "
    "A guard that has never failed is a comment."
)


def _frontmatter(record_id: str, **overrides) -> dict:
    base = {
        "id": record_id,
        "title": "Prove a guard is non-vacuous by injecting a fault",
        "status": "proposed",
        "evidence": ["roster/orchestration/test/test_context_boundary.py:150-155"],
        "origin": {"task": "T-1", "artifact": "a/b.py", "revision": "abc1234"},
        "proposed_classification": "internal",
        "source_scope": "deagy/cadre",
        "sensitivity_notes": "none",
        "conflicts_or_staleness": "none",
        "recommended_action": "ingest",
        "untrusted_instruction_risk": False,
        "staged_by": "proposing-agent",
    }
    base.update(overrides)
    # compute_digest is the only authority on the digest; recomputing it here
    # rather than hardcoding keeps the fixture honest if the body changes.
    base["content_digest"] = compute_digest(BODY)
    return base


class _Store:
    def __init__(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        root = Path(self._tmp.name)
        (root / ".agents" / "knowledge-store").mkdir(parents=True)
        (root / ".agents" / "knowledge-store" / "config.json").write_text("{}", encoding="utf-8")
        self.config = load_config(str(root / ".agents" / "knowledge-store" / "config.json"))
        self.db = open_store(self.config["database"])
        install_schema(self.db)

    def __enter__(self) -> "_Store":
        return self

    def __exit__(self, *exc) -> None:
        self.db.close()
        self._tmp.cleanup()

    def stage_accepted(self, record_id: str, **overrides) -> str:
        put_record(self.db, _frontmatter(record_id, **overrides), BODY)
        disposition_record(
            self.db, record_id,
            action="accepted", reason="useful and correct",
            classification_used=overrides.get("classification_used", "internal"),
            diverged_from_proposal=False, decided_by="knowledge-store-steward",
        )
        return record_id


class TestTheGapItself(unittest.TestCase):
    def test_an_accepted_record_becomes_retrievable(self) -> None:
        """G-7, stated as a test. Fails against the store as it was."""
        with _Store() as store:
            store.stage_accepted("KS-20260101-non-vacuity")

            before = search_store(
                store.db, store.config,
                "prove a guard is non-vacuous by injecting a fault",
                {"classification": "internal", "sources": [STAGED_SOURCE], "top": 5},
            )
            self.assertEqual(
                [], before,
                "an accepted record was retrievable before ingestion, which would "
                "mean staging confers retrievability after all",
            )

            report = ingest_accepted(store.db, store.config)
            self.assertEqual(1, len(report["ingested"]))
            self.assertEqual([], report["refused"])

            after = search_store(
                store.db, store.config,
                "prove a guard is non-vacuous by injecting a fault",
                {"classification": "internal", "sources": [STAGED_SOURCE], "top": 5},
            )
            self.assertTrue(
                after,
                "an accepted, ingested record is still unreachable — the pipeline "
                "still stops one action short of usefulness (G-7)",
            )
            self.assertEqual(
                "KS-20260101-non-vacuity", after[0]["citation"]["conversation_id"]
            )


class TestOnlyAcceptedRecordsAreIngested(unittest.TestCase):
    def test_a_proposed_record_is_not_ingested(self) -> None:
        """Staging is not approval, and this must not quietly make it so."""
        with _Store() as store:
            put_record(store.db, _frontmatter("KS-20260101-proposed"), BODY)
            report = ingest_accepted(store.db, store.config)
            self.assertEqual([], report["ingested"])
            self.assertFalse(already_ingested(store.db, "KS-20260101-proposed"))

    def test_selecting_by_id_ignores_records_not_named(self) -> None:
        with _Store() as store:
            store.stage_accepted("KS-20260101-one")
            store.stage_accepted("KS-20260101-two")
            report = ingest_accepted(store.db, store.config, record_ids=["KS-20260101-one"])
            self.assertEqual(["KS-20260101-one"], [r["id"] for r in report["ingested"]])
            self.assertFalse(already_ingested(store.db, "KS-20260101-two"))

    def test_an_unknown_id_is_reported_not_silently_dropped(self) -> None:
        with _Store() as store:
            store.stage_accepted("KS-20260101-real")
            report = ingest_accepted(store.db, store.config, record_ids=["KS-20260101-ghost"])
            self.assertIn("KS-20260101-ghost", report["not_accepted"])


class TestRefusals(unittest.TestCase):
    def test_untrusted_instruction_risk_is_refused(self) -> None:
        """The contract already forces such a record to 'deferred'. This refuses
        it anyway rather than trusting an invariant enforced elsewhere."""
        with _Store() as store:
            record_id = "KS-20260101-risky"
            put_record(store.db, _frontmatter(record_id), BODY)
            # Bypass the contract deliberately: the point is that this layer
            # does not depend on the other one having held.
            store.db.execute(
                "UPDATE staged_records SET status = 'accepted', frontmatter_json = ? WHERE id = ?",
                (
                    json.dumps({
                        **_frontmatter(record_id),
                        "status": "accepted",
                        "untrusted_instruction_risk": True,
                        "disposition": {
                            "action": "accepted", "reason": "r",
                            "classification_used": "internal",
                            "diverged_from_proposal": False,
                            "decided_by": "knowledge-store-steward",
                        },
                    }),
                    record_id,
                ),
            )
            store.db.commit()
            report = ingest_accepted(store.db, store.config)
            self.assertEqual([], report["ingested"])
            self.assertEqual(1, len(report["refused"]))
            self.assertIn("untrusted_instruction_risk", report["refused"][0]["reason"])

    def test_a_self_approved_record_is_refused(self) -> None:
        """The stager cannot also be the decider -- checked here too.

        `staged_store.disposition_record` refuses `decided_by == staged_by`,
        and `propose` now refuses a record that arrives already dispositioned.
        Neither covers `import-staged`, which legitimately takes decided
        records and is how an outside corpus enters. Ingestion is the step
        that makes a record retrievable, so it is the last place a
        self-approval can still be stopped, and it does not assume the
        earlier two held. Same reasoning as the untrusted-risk refusal above.
        """
        with _Store() as store:
            record_id = "KS-20260101-self-approved"
            put_record(store.db, _frontmatter(record_id), BODY)
            store.db.execute(
                "UPDATE staged_records SET status = 'accepted', frontmatter_json = ? WHERE id = ?",
                (
                    json.dumps({
                        **_frontmatter(record_id),
                        "status": "accepted",
                        "disposition": {
                            "action": "accepted", "reason": "approved during review",
                            "classification_used": "internal",
                            "diverged_from_proposal": False,
                            # The same actor as `staged_by` in _frontmatter.
                            "decided_by": "proposing-agent",
                        },
                    }),
                    record_id,
                ),
            )
            store.db.commit()
            report = ingest_accepted(store.db, store.config)
            self.assertEqual([], report["ingested"])
            self.assertEqual(1, len(report["refused"]))
            self.assertIn("proposing-agent", report["refused"][0]["reason"])
            self.assertFalse(already_ingested(store.db, record_id))


class TestClassification(unittest.TestCase):
    def test_the_stewards_classification_wins_over_the_proposers(self) -> None:
        """A proposer must not be able to widen classification by asking.

        The disposition is the decision; `proposed_classification` is a request.
        """
        with _Store() as store:
            record_id = "KS-20260101-narrowed"
            put_record(store.db, _frontmatter(record_id, proposed_classification="public"), BODY)
            disposition_record(
                store.db, record_id, action="accepted", reason="narrower than proposed",
                classification_used="confidential", diverged_from_proposal=True,
                decided_by="knowledge-store-steward",
            )
            report = ingest_accepted(store.db, store.config)
            self.assertEqual("confidential", report["ingested"][0]["classification"])

            row = store.db.execute(
                "SELECT classification FROM messages WHERE conversation_id = ?", (record_id,)
            ).fetchone()
            self.assertEqual("confidential", row[0])


class TestIdempotence(unittest.TestCase):
    def test_a_second_run_skips_rather_than_duplicating(self) -> None:
        with _Store() as store:
            store.stage_accepted("KS-20260101-twice")
            first = ingest_accepted(store.db, store.config)
            second = ingest_accepted(store.db, store.config)
            self.assertEqual(1, len(first["ingested"]))
            self.assertEqual([], second["ingested"])
            self.assertEqual(["KS-20260101-twice"], [s["id"] for s in second["skipped"]])
            count = store.db.execute(
                "SELECT COUNT(*) FROM messages WHERE source = ?", (STAGED_SOURCE,)
            ).fetchone()[0]
            self.assertEqual(1, count)

    def test_ingested_state_is_derived_from_the_corpus(self) -> None:
        """Not stored twice. A flag on the staged record could disagree with the
        corpus, and then two places would claim to know."""
        with _Store() as store:
            store.stage_accepted("KS-20260101-derived")
            ingest_accepted(store.db, store.config)
            self.assertTrue(already_ingested(store.db, "KS-20260101-derived"))
            store.db.execute("DELETE FROM messages WHERE conversation_id = ?", ("KS-20260101-derived",))
            store.db.commit()
            self.assertFalse(already_ingested(store.db, "KS-20260101-derived"))


class TestDryRun(unittest.TestCase):
    def test_dry_run_reports_without_writing(self) -> None:
        with _Store() as store:
            store.stage_accepted("KS-20260101-dry")
            report = ingest_accepted(store.db, store.config, dry_run=True)
            self.assertTrue(report["dry_run"])
            self.assertEqual(1, len(report["ingested"]))
            self.assertFalse(already_ingested(store.db, "KS-20260101-dry"))


if __name__ == "__main__":
    unittest.main()
