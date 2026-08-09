"""Exact glob language containment (`glob_containment.contains`).

The routing self-shadowing check (issue #162) reports a finding on a
*universal* claim: every path this include glob matches is excluded. That
claim cannot be supported by sampling -- an incomplete sample makes a
universal claim easier to satisfy, which is the false-accusation direction --
so the verdict is decided exactly instead.

The load-bearing test here is the differential one against a brute-force
oracle that enumerates every string over a small alphabet and applies the
*real* `glob_to_regex` matchers used by `match_rule`. It caught a genuine
defect during development: `**/` was modelled as a self-loop plus an epsilon
skip on one state, which left the skip reachable after the loop had consumed
input, so `**/a` wrongly accepted `.a`.
"""

from __future__ import annotations

import itertools
import random
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "src"))

from glob_containment import CONTAINED, NOT_CONTAINED, UNDETERMINED, contains  # noqa: E402
from routing import glob_to_regex  # noqa: E402

# Deliberately tiny: `/` for structure, `.` because extensions are where the
# dialect's `*` behaviour is most easily got wrong, and two ordinary letters.
_ORACLE_ALPHABET = "ab/."
_ORACLE_MAX_LENGTH = 6

_PATTERNS = [
    "*", "**", "?", "a", "b", "a/b", "**/a", "a/**", "*.a", "**/*.a", "a/*/b",
    "**/a/**", "a?b", "*/*", "a/**/b", "**/*", "./a", "a.b", "*a*", "**/",
    "/a", "a/", "**/a/b", "?/?", "a/*", "a/*/**",
]


def _oracle(include: str, excludes: list[str]) -> tuple[str, str | None]:
    """Decide containment by exhaustive enumeration, using the real matchers.

    Bounded by `_ORACLE_MAX_LENGTH`, so it can only *miss* a long witness --
    it never invents one. A disagreement where the oracle says NOT_CONTAINED
    is therefore always a real engine defect.
    """
    include_matcher = glob_to_regex(include)
    exclude_matchers = [glob_to_regex(pattern) for pattern in excludes]
    for length in range(0, _ORACLE_MAX_LENGTH + 1):
        for letters in itertools.product(_ORACLE_ALPHABET, repeat=length):
            candidate = "".join(letters)
            if include_matcher.search(candidate) and not any(
                matcher.search(candidate) for matcher in exclude_matchers
            ):
                return NOT_CONTAINED, candidate
    return CONTAINED, None


class DifferentialAgainstBruteForceTests(unittest.TestCase):
    def test_engine_agrees_with_exhaustive_enumeration(self) -> None:
        generator = random.Random(20260809)
        checked = 0
        for _ in range(300):
            include = generator.choice(_PATTERNS)
            excludes = generator.sample(_PATTERNS, generator.randint(1, 3))
            verdict = contains(include, excludes)
            if verdict == UNDETERMINED:
                continue
            expected, witness = _oracle(include, excludes)
            if expected == CONTAINED and verdict == NOT_CONTAINED:
                # The oracle is length-bounded; a witness longer than its
                # bound is a legitimate disagreement in this direction only.
                continue
            checked += 1
            self.assertEqual(
                expected,
                verdict,
                f"include={include!r} excludes={excludes!r}: engine said {verdict}, "
                f"exhaustive enumeration says {expected} (witness {witness!r})",
            )
        self.assertGreater(checked, 100, "differential test degenerated to almost no comparisons")

    def test_the_doublestar_slash_commitment_case(self) -> None:
        """`**/a` must not match `.a`: `(?:.*/)?` is a choice between nothing
        and "anything, then `/`", and the second branch is committed to
        ending on a separator. Modelling it without that commitment made this
        containment wrongly hold.
        """
        self.assertFalse(glob_to_regex("**/a").search(".a"))
        self.assertEqual(NOT_CONTAINED, contains("**/*.a", ["**/a"]))


class ContainmentVerdictTests(unittest.TestCase):
    def test_identical_patterns_are_contained(self) -> None:
        self.assertEqual(CONTAINED, contains("foo/**", ["foo/**"]))

    def test_a_broader_exclude_contains_a_narrower_include(self) -> None:
        self.assertEqual(CONTAINED, contains("foo/**", ["**"]))

    def test_containment_can_require_the_union_of_several_excludes(self) -> None:
        """Neither exclusion alone covers `a/**`; together they do."""
        self.assertEqual(NOT_CONTAINED, contains("a/**", ["a/*"]))
        self.assertEqual(NOT_CONTAINED, contains("a/**", ["a/*/**"]))
        self.assertEqual(CONTAINED, contains("a/**", ["a/*", "a/*/**"]))

    def test_a_partial_carve_out_is_not_containment(self) -> None:
        self.assertEqual(NOT_CONTAINED, contains("**/architecture/**", ["roster/**"]))

    def test_a_depth_limited_exclude_is_not_containment(self) -> None:
        self.assertEqual(NOT_CONTAINED, contains("docs/**", ["docs/*"]))

    def test_no_excludes_is_never_containment(self) -> None:
        self.assertEqual(NOT_CONTAINED, contains("foo/**", []))

    def test_matching_is_case_insensitive_like_the_matcher(self) -> None:
        self.assertEqual(CONTAINED, contains("Foo/**", ["foo/**"]))
        self.assertTrue(glob_to_regex("foo/**").search("Foo/bar"))

    def test_backslashes_normalize_like_the_matcher(self) -> None:
        self.assertEqual(CONTAINED, contains("foo\\bar/**", ["foo/bar/**"]))

    def test_a_literal_pattern_is_contained_only_by_a_covering_pattern(self) -> None:
        self.assertEqual(CONTAINED, contains("a/b.md", ["a/b.md"]))
        self.assertEqual(CONTAINED, contains("a/b.md", ["a/*"]))
        self.assertEqual(NOT_CONTAINED, contains("a/b.md", ["a/c.md"]))

    def test_an_empty_include_pattern_is_contained_by_an_empty_exclude(self) -> None:
        self.assertEqual(CONTAINED, contains("", [""]))


class BudgetTests(unittest.TestCase):
    def test_the_state_budget_yields_undetermined_rather_than_a_verdict(self) -> None:
        """Exhausting the budget must never produce CONTAINED, because the
        caller reports on CONTAINED. A skipped pattern is a missed finding;
        a guessed one would be a false accusation.
        """
        import glob_containment

        original = glob_containment._MAX_PRODUCT_STATES
        try:
            glob_containment._MAX_PRODUCT_STATES = 1
            self.assertEqual(
                UNDETERMINED, glob_containment.contains("**/a/**/b/**/c.md", ["x/**", "y/**"])
            )
        finally:
            glob_containment._MAX_PRODUCT_STATES = original

    def test_the_real_routing_patterns_stay_far_inside_the_budget(self) -> None:
        from routing import load_routing

        config = load_routing(ROOT.parent / "orchestration" / "routing.yaml")
        for section in ("routes", "risk_rules"):
            for rule in config.get(section, []):
                excludes = rule.get("exclude_paths") or []
                if not excludes:
                    continue
                for include in rule.get("paths", []) or []:
                    with self.subTest(rule=rule["id"], include=include):
                        self.assertNotEqual(UNDETERMINED, contains(include, excludes))


if __name__ == "__main__":
    unittest.main()
