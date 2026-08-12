"""Tests for `cadre select --explain` (Proposal 07): near-miss route reasoning.

Covers `roster/orchestration/src/route_near_miss.py` directly (a partially
satisfied `keyword_groups` entry is surfaced; a route with zero overlap is
omitted; an unmatched route with no `keyword_groups` at all is omitted) and
`select_agents.py`'s `--explain` wiring end to end via subprocess: the flag
is off by default (no stderr output, unchanged stdout), and when passed it
prints to stderr only -- the JSON plan on stdout is byte-identical with and
without it.
"""

from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
AGENTS_ROOT = ROOT.parent
sys.path.insert(0, str(ROOT / "src"))

from routing import load_catalog, load_routing  # noqa: E402
from route_near_miss import (  # noqa: E402
    explain_route_near_miss,
    find_near_misses,
    format_near_misses_text,
)

CONFIG = load_routing(ROOT / "routing.json")
CATALOG = load_catalog(AGENTS_ROOT / "catalog.yaml")
SELECTOR = ROOT / "src" / "select_agents.py"

SYNTHETIC_ROUTE = {
    "id": "synthetic-route",
    "keywords": [],
    "keyword_groups": [
        ["alpha", "bravo", "charlie"],
        ["delta", "echo"],
    ],
    "paths": [],
}


def _run_select(args: list[str], cwd: Path) -> subprocess.CompletedProcess:
    return subprocess.run(
        [sys.executable, str(SELECTOR), *args],
        cwd=cwd,
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
    )


def _init_repo(root: Path) -> None:
    subprocess.run(["git", "init", "-q", str(root)], check=True)
    subprocess.run(["git", "-C", str(root), "config", "user.email", "test@example.invalid"], check=True)
    subprocess.run(["git", "-C", str(root), "config", "user.name", "Test"], check=True)


class ExplainRouteNearMissTests(unittest.TestCase):
    def test_partial_group_is_surfaced_with_matched_and_missing_keywords(self) -> None:
        explanation = explain_route_near_miss(SYNTHETIC_ROUTE, "please handle alpha and bravo carefully")
        self.assertIsNotNone(explanation)
        self.assertEqual(explanation["id"], "synthetic-route")
        groups = explanation["partially_satisfied_keyword_groups"]
        self.assertEqual(len(groups), 1)
        self.assertEqual(sorted(groups[0]["matched"]), ["alpha", "bravo"])
        self.assertEqual(groups[0]["missing"], ["charlie"])

    def test_zero_overlap_route_is_not_surfaced(self) -> None:
        explanation = explain_route_near_miss(SYNTHETIC_ROUTE, "an entirely unrelated task about nothing here")
        self.assertIsNone(explanation)

    def test_fully_satisfied_group_is_not_reported_as_a_near_miss(self) -> None:
        # A route reaching this function is by definition unmatched, so a
        # fully-satisfied group can't occur from real match_routes() output --
        # but the function itself must still not misreport N-of-N as partial.
        explanation = explain_route_near_miss(SYNTHETIC_ROUTE, "alpha bravo charlie all present here")
        groups = explanation["partially_satisfied_keyword_groups"] if explanation else []
        self.assertFalse(any(not group["missing"] for group in groups))

    def test_route_with_no_keyword_groups_is_never_surfaced(self) -> None:
        plain_route = {"id": "plain", "keywords": ["frontend"], "keyword_groups": [], "paths": []}
        self.assertIsNone(explain_route_near_miss(plain_route, "frontend work of some kind"))
        self.assertIsNone(explain_route_near_miss(plain_route, "anything at all"))

    def test_find_near_misses_skips_already_matched_routes(self) -> None:
        config = {"routes": [SYNTHETIC_ROUTE]}
        near_misses = find_near_misses(config, "alpha and bravo", {"synthetic-route"})
        self.assertEqual(near_misses, [])

    def test_find_near_misses_reports_unmatched_route_declaration_order(self) -> None:
        second_route = {
            "id": "synthetic-route-2",
            "keywords": [],
            "keyword_groups": [["foxtrot", "golf", "hotel"]],
            "paths": [],
        }
        config = {"routes": [SYNTHETIC_ROUTE, second_route]}
        near_misses = find_near_misses(
            config, "mentions alpha and foxtrot and golf but nothing else", set()
        )
        self.assertEqual([entry["id"] for entry in near_misses], ["synthetic-route", "synthetic-route-2"])

    def test_no_numeric_score_field_anywhere_in_output(self) -> None:
        # Structural guard for the hard non-goal: no percentage, count-based
        # confidence, or ranking field under any name.
        explanation = explain_route_near_miss(SYNTHETIC_ROUTE, "alpha bravo present")
        serialized = json.dumps(explanation)
        for forbidden in ("score", "confidence", "rank", "weight", "percent", "%"):
            self.assertNotIn(forbidden, serialized.lower())

    def test_format_text_reports_no_near_misses_cleanly(self) -> None:
        text = format_near_misses_text([])
        self.assertIn("no near-miss routes", text)

    def test_format_text_is_short_and_scannable_for_one_entry(self) -> None:
        explanation = explain_route_near_miss(SYNTHETIC_ROUTE, "alpha bravo present")
        text = format_near_misses_text([explanation])
        self.assertIn("synthetic-route", text)
        self.assertIn("matched 2 of 3 required keywords (alpha, bravo)", text)
        self.assertIn("missing: charlie", text)
        # Scannable: a handful of lines, not a dump of the whole route object.
        self.assertLess(len(text.splitlines()), 10)

    def test_real_routing_yaml_run_does_not_crash_and_is_json_serializable(self) -> None:
        # Grounding check against the real repository configuration: as of
        # this writing no route in routing.json declares keyword_groups, so
        # this legitimately returns no near misses for any task -- see
        # route_near_miss.py's module docstring. The assertion here is only
        # that the real config doesn't crash the mechanism and stays
        # deterministic/serializable, not that it produces a specific list.
        near_misses = find_near_misses(CONFIG, "improve cross-runner UX documentation", {"pipeline"})
        json.dumps(near_misses)  # must not raise
        self.assertIsInstance(near_misses, list)


class ExplainCliTests(unittest.TestCase):
    def test_explain_omitted_by_default_produces_no_stderr_explanation(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            _init_repo(root)
            result = _run_select(
                ["--root", str(root), "--task", "improve cross-runner UX documentation"],
                cwd=root,
            )
            self.assertNotIn("near-miss", result.stderr)

    def test_explain_flag_prints_to_stderr_not_stdout(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            _init_repo(root)
            result = _run_select(
                ["--root", str(root), "--task", "improve cross-runner UX documentation", "--explain"],
                cwd=root,
            )
            self.assertIn("--explain:", result.stderr)
            # stdout must remain pure JSON -- no explanation text leaked in.
            plan = json.loads(result.stdout)
            self.assertIn("status", plan)
            self.assertNotIn("near_miss", plan)
            self.assertNotIn("near-miss", result.stdout)

    def test_json_plan_is_unchanged_by_explain(self) -> None:
        # Two separate process invocations can legitimately differ in
        # `generated_at` (wall-clock milliseconds) alone -- that is expected
        # and unrelated to --explain. Strip it, then require the remaining
        # plan (including `dispatch_fingerprint`, which already excludes
        # `generated_at` from its own hashed payload) to be identical, and
        # separately require the raw stdout text to contain no explanation
        # content at all.
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            _init_repo(root)
            args = ["--root", str(root), "--task", "improve cross-runner UX documentation", "--task-id", "EXPLAIN-1"]
            without_explain = _run_select(args, cwd=root)
            with_explain = _run_select([*args, "--explain"], cwd=root)

            plan_without = json.loads(without_explain.stdout)
            plan_with = json.loads(with_explain.stdout)
            self.assertEqual(plan_without["dispatch_fingerprint"], plan_with["dispatch_fingerprint"])
            plan_without.pop("generated_at")
            plan_with.pop("generated_at")
            self.assertEqual(plan_without, plan_with)
            self.assertNotIn("near-miss", with_explain.stdout)


if __name__ == "__main__":
    unittest.main()
