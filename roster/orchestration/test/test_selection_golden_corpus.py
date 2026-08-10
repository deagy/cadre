"""Golden-corpus regression harness for build_dispatch_plan()'s deterministic
selector.

Fixtures live in fixtures/selection_golden_corpus.json (a git-tracked,
reviewable data file — see B-FR-1/B-FR-4/B-FR-6 in the requirements
baseline) rather than as Python literals in this module, so a routing.yaml
change and the corresponding fixture update show up as one focused data-file
diff instead of being buried inside test code.

This harness calls build_dispatch_plan() in-process (no subprocess, per
B-FR-2) with the Agentic SDLC lifecycle contract forced unavailable
(build_dispatch_plan.try_lifecycle_contract patched to return None, the same
pattern test_selector.py's test_standalone_mode_still_* tests use). That
keeps the corpus deterministic and runnable without AGENTIC_SDLC_BIN or
network access (B-FR-4) — without forcing standalone mode, a host that
happens to have the Agentic SDLC kernel installed could pull additional
quality-gate agents into `support` (see build_dispatch_plan._gate_agents),
making the same fixture pass or fail depending on the environment.

On mismatch, each fixture reports the specific field delta (e.g.
"reviewers: expected=[...], got=[...]") via _mismatches() below, rather than
unittest's generic assertEqual diff (B-FR-3). A fixture that unexpectedly
resolves to status "needs-triage" is its own explicit, separately reported
failure unless the fixture opts in with expect_needs_triage: true (B-FR-7) —
this guards against a routing edit that accidentally makes a fixture's
task/files stop matching anything, which could otherwise coincide with a
copy-pasted all-empty `expected` block and pass silently.

Comparison is exact-match (expected set/list == actual set/list) with no
quarantine mechanism: a fixture either matches build_dispatch_plan()'s
current output or the whole suite fails. This is a deliberate v1 design
choice (see the requirements baseline), not an oversight.

Every fixture's "expected.workflow" was added by Proposal 03 of the
orchestration review and independently checked against roster/workflows/*.md
before being set (see _AGENT_GROUP_FIELDS's comment above). One fixture,
AGENT-SUITE-GOVERNANCE-1, is a deliberate, documented exception: its
"expected.workflow" is "unclassified" (the value independent judgment
supports) while build_dispatch_plan() currently returns "debugging" for
every agent-suite-governance route match regardless of whether the task is
actually a defect fix. That one fixture is therefore CURRENTLY FAILING by
design, not by accident -- see its "description" field and
fixtures/workflow_fitness_table.json's WF-AGENT-SUITE-GOVERNANCE-MISLABEL-1
for the full reasoning. Fixing _select_workflow() to make this fixture pass
is out of scope for the change that added this comment; do not "fix" the
test suite by reverting this fixture's expected workflow back to
"debugging".

Route-category coverage: test_corpus_covers_every_required_route_category
below derives its required-route set from routing.yaml's routes[] array at
import time (CONFIG["routes"]), rather than a hardcoded literal list, so a
routing.yaml edit that adds or removes a route category is itself a failure
here (an added route needs a new fixture; a removed route needs the
corresponding fixture(s) pruned) instead of the assertion silently going
stale. The corpus covers every route category currently in routing.yaml's
routes[] array (see the fixtures file's _comment block for the rationale for
cases where two routes' paths or keywords genuinely overlap), matching
B-FR-5's "initial corpus covers every existing route category" requirement.
The route id count is not hardcoded here or in the fixtures file's own
comment because it drifts every time a route is added, removed, or renamed
-- routing.yaml's routes[] array is the live list. The same test also
requires the 'production' and 'destructive' risk_rules to each be pinned by a fixture
independently (they have distinct keyword_groups, human_gate, and
reviewers in routing.yaml; one triggering does not excuse the other from
also being exercised).
"""

from __future__ import annotations

import json
import unittest
from pathlib import Path
from typing import Any
from unittest import mock

ROOT = Path(__file__).resolve().parent.parent
AGENTS_ROOT = ROOT.parent
FIXTURES_PATH = Path(__file__).resolve().parent / "fixtures" / "selection_golden_corpus.json"

import sys  # noqa: E402

sys.path.insert(0, str(ROOT / "src"))

import build_dispatch_plan as build_dispatch_plan_module  # noqa: E402
from build_dispatch_plan import build_dispatch_plan  # noqa: E402
from routing import load_catalog, load_routing  # noqa: E402

CONFIG = load_routing(ROOT / "routing.yaml")
CATALOG = load_catalog(AGENTS_ROOT / "catalog.yaml")

# The four fields the requirements baseline (B-FR-1/B-FR-2) requires every
# fixture to pin. "team_ids" is an optional fifth field (see _mismatches)
# asserted only for fixtures whose purpose is to pin a team_recipes trigger.
_AGENT_GROUP_FIELDS = ("primary", "reviewers", "support")

# "workflow" was historically never compared here at all -- a fixture could
# assert primary/reviewers/support/matched_routes and still leave
# _select_workflow()'s output completely unchecked. Fixtures now carry an
# expected "workflow" value alongside the existing "expected" block (see
# _mismatches). Unlike primary/reviewers/support/matched_routes, these
# expected values are not a blind copy of current output: each was checked
# against roster/workflows/*.md before being added (see the PR that
# introduced this field for which ones were spot-checked, and
# fixtures/workflow_fitness_table.json for the independent methodology this
# addition is modeled on). One fixture (AGENT-SUITE-GOVERNANCE-1) is a
# documented, deliberate exception -- see its "workflow" comment below.


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
    # Force standalone mode so the corpus is deterministic regardless of
    # whether AGENTIC_SDLC_BIN/agentic-sdlc happens to be resolvable on the
    # host running the suite (see module docstring).
    with mock.patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
        return build_dispatch_plan(CONFIG, CATALOG, values)


def _mismatches(case: dict[str, Any], actual: dict[str, Any]) -> list[str]:
    mismatches: list[str] = []
    expected = case["expected"]
    for field in _AGENT_GROUP_FIELDS:
        expected_value = expected[field]
        actual_value = actual["agents"][field]
        if actual_value != expected_value:
            mismatches.append(f"{field}: expected={expected_value!r}, got={actual_value!r}")

    expected_workflow = expected["workflow"]
    actual_workflow = actual["workflow"]
    if actual_workflow != expected_workflow:
        mismatches.append(f"workflow: expected={expected_workflow!r}, got={actual_workflow!r}")

    # Fixtures pin *which* routes matched, not why: a plan's route entries
    # carry a full `reasons` record, and hand-maintaining every pattern/file
    # pair across ~60 cases would bury the routing assertion these fixtures
    # exist to make. Reason content is asserted in test_selector.py instead.
    expected_routes = expected["matched_routes"]
    actual_routes = [route["id"] for route in actual["matched_routes"]]
    if actual_routes != expected_routes:
        mismatches.append(f"matched_routes: expected={expected_routes!r}, got={actual_routes!r}")

    if "team_ids" in expected:
        expected_teams = sorted(expected["team_ids"])
        actual_teams = sorted(team["id"] for team in actual["teams"])
        if actual_teams != expected_teams:
            mismatches.append(f"team_ids: expected={expected_teams!r}, got={actual_teams!r}")

    return mismatches


class SelectionGoldenCorpusTests(unittest.TestCase):
    def test_corpus_is_non_empty_and_ids_are_unique(self) -> None:
        self.assertTrue(CASES)
        ids = [case["id"] for case in CASES]
        self.assertEqual(len(ids), len(set(ids)))

    def test_corpus_covers_every_required_route_category(self) -> None:
        # Pins B-FR-5's coverage requirement: a future edit that removes the
        # last fixture covering one of these categories is itself a failure,
        # not a silent corpus shrink. The required set is derived from
        # routing.yaml itself (not a hardcoded literal) so a route category
        # added or removed there is caught here too, per the module
        # docstring's "Route-category coverage" note.
        matched_route_ids = {
            route_id for case in CASES for route_id in case["expected"]["matched_routes"]
        }
        required_route_ids = {route["id"] for route in CONFIG["routes"]}
        for required_route in sorted(required_route_ids):
            with self.subTest(route=required_route):
                self.assertIn(required_route, matched_route_ids)

        matched_risk_ids = {
            risk["id"]
            for case in CASES
            for risk in _run_case(case)["matched_risks"]
        }
        # 'production' and 'destructive' are distinct risk_rules with their
        # own keyword_groups/human_gate/reviewers in routing.yaml; each must
        # be independently pinned by a fixture rather than accepting either
        # one silently substituting for the other (see PRODUCTION-RISK-1 and
        # DESTRUCTIVE-RISK-1 in the fixtures file).
        self.assertIn("production", matched_risk_ids, "corpus must cover the production risk rule")
        self.assertIn("destructive", matched_risk_ids, "corpus must cover the destructive risk rule")

        cross_stack_cases = [
            case
            for case in CASES
            if len(case["expected"]["matched_routes"]) >= 2
        ]
        self.assertTrue(cross_stack_cases, "corpus must cover at least one cross-stack/minimum-match case")

        team_ids = {
            team_id for case in CASES for team_id in case["expected"].get("team_ids", [])
        }
        fixed_recipe_ids = {recipe["id"] for recipe in CONFIG["team_recipes"] if recipe["type"] == "fixed"}
        dynamic_recipe_ids = {recipe["id"] for recipe in CONFIG["team_recipes"] if recipe["type"] == "dynamic"}
        self.assertTrue(team_ids & fixed_recipe_ids, "corpus must cover at least one fixed team_recipe trigger")
        self.assertTrue(team_ids & dynamic_recipe_ids, "corpus must cover at least one dynamic team_recipe trigger")

    def test_golden_corpus(self) -> None:
        for case in CASES:
            with self.subTest(fixture=case["id"]):
                actual = _run_case(case)

                expect_needs_triage = case.get("expect_needs_triage", False)
                if actual["status"] == "needs-triage" and not expect_needs_triage:
                    self.fail(
                        f"fixture {case['id']!r} unexpectedly resolved to status "
                        "'needs-triage' (no route/risk matched task/changed_files) "
                        "but is not marked expect_needs_triage=true; either the "
                        "fixture's task/changed_files no longer match any route "
                        "(routing.yaml drifted) or the fixture needs updating."
                    )
                if expect_needs_triage and actual["status"] != "needs-triage":
                    self.fail(
                        f"fixture {case['id']!r} is marked expect_needs_triage=true "
                        f"but resolved to status {actual['status']!r} instead"
                    )

                mismatches = _mismatches(case, actual)
                if mismatches:
                    self.fail(
                        f"fixture {case['id']!r} ({case.get('description', 'no description')}) "
                        "mismatched build_dispatch_plan() output:\n  " + "\n  ".join(mismatches)
                    )


if __name__ == "__main__":
    unittest.main()
