"""Regression guard for orchestration's bounded runtime stewardship wave.

This is a contract test over the runner-neutral orchestration skill rather
than a fake dispatch: the skill is the implementation surface that stages
handoffs and launches follow-up roles. It pins the safety properties that make
runtime curation different from sweeping a shared queue: only newly staged
records are reviewable, the steward is independent, untrusted provenance is
deferred, and ingestion is exact-ID-only.
"""

from __future__ import annotations

import unittest
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
SKILL = REPOSITORY_ROOT / ".agents" / "skills" / "run-agent-orchestration" / "SKILL.md"


class RuntimeStewardshipWaveTests(unittest.TestCase):
    def test_skill_exists(self) -> None:
        self.assertTrue(SKILL.is_file(), SKILL)

    def test_runtime_stewardship_is_bounded_and_independent(self) -> None:
        text = SKILL.read_text(encoding="utf-8")
        self.assertIn("post-consolidation knowledge-store-steward wave", text)
        self.assertIn("status: staged", text)
        self.assertIn("already-staged", text)
        self.assertIn("staged by `knowledge-store-steward`", text)
        self.assertIn("untrusted_instruction_risk: true` **or** `unknown` requires `deferred`", text)

    def test_eligible_ids_defined_as_newly_staged_excluding_steward(self) -> None:
        text = SKILL.read_text(encoding="utf-8")
        self.assertIn("eligible IDs (eligible = newly staged by this run, excluding any staged by `knowledge-store-steward`)", text)

    def test_steward_cannot_disposition_its_own_proposals(self) -> None:
        text = SKILL.read_text(encoding="utf-8")
        self.assertIn("Filter out from this wave any ID whose `staged_by` value is `knowledge-store-steward`", text)
        self.assertIn("a steward cannot disposition its own proposals", text)

    def test_originating_role_captured_from_handoff(self) -> None:
        text = SKILL.read_text(encoding="utf-8")
        self.assertIn("Set `staged_by` to the handoff item's `source_role`", text)

    def test_edge_case_all_ids_steward_staged(self) -> None:
        text = SKILL.read_text(encoding="utf-8")
        self.assertIn("If no newly-staged eligible IDs remain after filtering", text)
        self.assertIn("state that no stewardship wave ran because all staged proposals were from the steward", text)

    def test_ingestion_is_limited_to_accepted_ids_from_the_wave(self) -> None:
        text = SKILL.read_text(encoding="utf-8")
        self.assertIn("cadre knowledge disposition-staged --id <id>", text)
        self.assertIn("cadre knowledge ingest-accepted --id <id>", text)
        self.assertIn("Never omit `--id`", text)


if __name__ == "__main__":
    unittest.main()
