"""Independent fitness-table harness for build_dispatch_plan()'s `workflow` field.

Proposal 03 from the orchestration review: selection_golden_corpus.json pins
build_dispatch_plan()'s current output as a regression baseline, but it is
derived FROM the code, so a bug in _select_workflow() (roster/orchestration/
src/build_dispatch_plan.py) and a fixture copied from that same buggy output
will always agree -- the golden corpus previously never even compared
`workflow` at all (see test_selection_golden_corpus.py's _AGENT_GROUP_FIELDS,
now extended). This module instead compares the real `workflow` output
against fixtures/workflow_fitness_table.json, a small hand-authored table
whose 'expected_workflow' values were each reasoned independently from
roster/workflows/*.md and routing.json, not from _select_workflow()'s
current behavior -- see that file's own '_comment' block for the full
methodology.

No fixture is currently marked "known_mismatch": true. Both cases that once
were have since been fixed in the code rather than weakened here -- roster
maintenance mislabelled as debugging (#154), and the unreachable rollback
workflow (#157). The mechanism is dormant, not gone: a future case that
disagrees with _select_workflow() should be added with that flag rather than
bent to match current behavior. Real disagreement with the code under test is
the signal a self-referential pinning test can never produce, and is the
entire point of an independent fitness table.
"""

from __future__ import annotations

import json
import unittest
from pathlib import Path
from typing import Any
from unittest import mock

ROOT = Path(__file__).resolve().parent.parent
AGENTS_ROOT = ROOT.parent
FIXTURES_PATH = Path(__file__).resolve().parent / "fixtures" / "workflow_fitness_table.json"

import sys  # noqa: E402

sys.path.insert(0, str(ROOT / "src"))

import build_dispatch_plan as build_dispatch_plan_module  # noqa: E402
from build_dispatch_plan import build_dispatch_plan  # noqa: E402
from routing import load_catalog, load_routing  # noqa: E402

CONFIG = load_routing(ROOT / "routing.json")
CATALOG = load_catalog(AGENTS_ROOT / "catalog.yaml")

def _load_workflow_enum() -> set[str]:
    schema_path = ROOT / "selection.schema.json"
    schema = json.loads(schema_path.read_text(encoding="utf-8"))
    return set(schema["properties"]["workflow"]["enum"])


WORKFLOW_ENUM = _load_workflow_enum()


def _load_cases() -> list[dict[str, Any]]:
    payload = json.loads(FIXTURES_PATH.read_text(encoding="utf-8"))
    cases = payload["cases"]
    ids = [case["id"] for case in cases]
    duplicates = {case_id for case_id in ids if ids.count(case_id) > 1}
    if duplicates:
        raise ValueError(f"duplicate fixture ids in {FIXTURES_PATH.name}: {sorted(duplicates)}")
    return cases


CASES = _load_cases()


def _run_case(case: dict[str, Any]) -> dict[str, Any]:
    values = {
        "task": case["task"],
        "changed_files": case["changed_files"],
        "changed_file_source": "test",
        "repository_root": str(AGENTS_ROOT.parent),
        "source": "example/repository",
        "classification": case.get("classification", "internal"),
        "task_id": case["task_id"],
    }
    # Force standalone mode, matching test_selection_golden_corpus.py, so this
    # table is reproducible without AGENTIC_SDLC_BIN/network access.
    with mock.patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
        return build_dispatch_plan(CONFIG, CATALOG, values)


class WorkflowFitnessTableStructureTests(unittest.TestCase):
    """Sanity checks on the table itself, independent of build_dispatch_plan()."""

    def test_table_is_non_empty_and_ids_are_unique(self) -> None:
        self.assertTrue(CASES)
        ids = [case["id"] for case in CASES]
        self.assertEqual(len(ids), len(set(ids)))

    def test_every_expected_workflow_is_a_valid_enum_value(self) -> None:
        for case in CASES:
            with self.subTest(fixture=case["id"]):
                self.assertIn(case["expected_workflow"], WORKFLOW_ENUM)

    def test_table_covers_every_workflow_enum_value(self) -> None:
        covered = {case["expected_workflow"] for case in CASES}
        for value in sorted(WORKFLOW_ENUM):
            with self.subTest(workflow=value):
                self.assertIn(
                    value,
                    covered,
                    f"workflow_fitness_table.json has no case asserting expected_workflow={value!r}",
                )


class WorkflowFitnessTableAgainstSelectorTests(unittest.TestCase):
    """The real check: does build_dispatch_plan() agree with independent judgment?

    Deliberately NOT using subTest here for the known_mismatch case --
    subTest failures are easy to skim past in a wall of output, and a
    disagreement between this table and current code is the specific finding
    this proposal exists to surface. Known-mismatch cases get their own named
    test methods instead so `-v` output names them explicitly, and the
    docstring on each explains exactly what is wrong and why it is expected.
    """

    def test_cases_without_known_mismatch_agree_with_current_code(self) -> None:
        clean_cases = [case for case in CASES if not case.get("known_mismatch", False)]
        self.assertTrue(clean_cases, "expected at least one non-known-mismatch fixture")
        failures = []
        for case in clean_cases:
            actual = _run_case(case)
            if actual["workflow"] != case["expected_workflow"]:
                failures.append(
                    f"{case['id']!r} ({case['description']}): "
                    f"expected_workflow={case['expected_workflow']!r}, "
                    f"actual workflow={actual['workflow']!r}"
                )
        if failures:
            self.fail(
                "fitness-table cases not marked known_mismatch disagreed with "
                "build_dispatch_plan():\n  " + "\n  ".join(failures)
            )


if __name__ == "__main__":
    unittest.main()
