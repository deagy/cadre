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

from glob_containment import (  # noqa: E402
    CONTAINED,
    NOT_CONTAINED,
    UNDETERMINED,
    contains,
    contains_with_witness,
)
from routing import (  # noqa: E402
    GLOB_DOUBLESTAR,
    GLOB_DOUBLESTAR_SLASH,
    GLOB_LITERAL,
    GLOB_QUESTION,
    GLOB_STAR,
    glob_to_regex,
    iter_glob_tokens,
)

# `/` for structure; `.` because extensions are where `*` behaviour is most
# easily got wrong; `a`/`b` as ordinary literals; `A` because case folding is
# where a one-line regression is otherwise invisible (dropping `.lower()`
# from `_alphabet` made `contains("Foo/**", ["bar/**"])` CONTAINED -- an
# unrelated exclude condemning a live glob -- with every test still green);
# `z` appears in no pattern,
# so it exercises the abstract alphabet's "every other character" sentinel
# deliberately rather than by luck; and `\n`, because the matcher treats it
# inconsistently -- `**` compiles to `.` which excludes it, while `*`/`?`
# compile to `[^/]` which includes it. Omitting `\n` here is what let that
# divergence ship: the engine modelled `**` as consuming it and reported
# `contains("foo/*", ["foo/**"])` as CONTAINED.
_ORACLE_ALPHABET = "aAb/.z\n"
_ORACLE_MAX_LENGTH = 5

_PATTERNS = [
    "*", "**", "?", "a", "b", "a/b", "**/a", "a/**", "*.a", "**/*.a", "a/*/b",
    "**/a/**", "a?b", "*/*", "a/**/b", "**/*", "./a", "a.b", "*a*", "**/",
    "/a", "a/", "**/a/b", "?/?", "a/*", "a/*/**",
    # Mixed case, so the oracle exercises the literal case-folding path.
    "A", "A/**", "**/A.a", "*.A", "Ab/*",
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
        """Both directions, and neither on trust.

        A NOT_CONTAINED verdict is checked twice: against the oracle, and by
        validating the engine's own witness with the real matchers. Without
        that second check the whole direction was unverifiable -- the oracle
        is length-bounded, so an engine that simply returned NOT_CONTAINED
        for everything passed this test with the engine fully disabled.
        """
        generator = random.Random(20260809)
        checked = 0
        contained_checked = 0
        for _ in range(300):
            include = generator.choice(_PATTERNS)
            excludes = generator.sample(_PATTERNS, generator.randint(1, 3))
            verdict, witness = contains_with_witness(include, excludes)
            if verdict == UNDETERMINED:
                continue

            if verdict == NOT_CONTAINED:
                # Independently checkable, regardless of the oracle's bound.
                self.assertIsNotNone(
                    witness, f"include={include!r} excludes={excludes!r}: NOT_CONTAINED without a witness"
                )
                self.assertTrue(
                    glob_to_regex(include).search(witness),
                    f"witness {witness!r} does not match its own include {include!r}",
                )
                for exclude in excludes:
                    self.assertFalse(
                        glob_to_regex(exclude).search(witness),
                        f"witness {witness!r} is excluded by {exclude!r}, so it proves nothing",
                    )

            expected, oracle_witness = _oracle(include, excludes)
            if expected == CONTAINED and verdict == NOT_CONTAINED:
                # The oracle is length-bounded; a witness longer than its
                # bound is a legitimate disagreement in this direction only,
                # and the engine's witness was validated above regardless.
                continue
            checked += 1
            contained_checked += expected == CONTAINED
            self.assertEqual(
                expected,
                verdict,
                f"include={include!r} excludes={excludes!r}: engine said {verdict}, "
                f"exhaustive enumeration says {expected} (witness {oracle_witness!r})",
            )
        self.assertGreater(checked, 100, "differential test degenerated to almost no comparisons")
        self.assertGreater(
            contained_checked,
            25,
            "no meaningful number of CONTAINED verdicts was compared; the direction that "
            "actually produces findings is going unverified",
        )

    def test_the_doublestar_slash_commitment_case(self) -> None:
        """`**/a` must not match `.a`: `(?:.*/)?` is a choice between nothing
        and "anything, then `/`", and the second branch is committed to
        ending on a separator. Modelling it without that commitment made this
        containment wrongly hold.
        """
        self.assertFalse(glob_to_regex("**/a").search(".a"))
        self.assertEqual(NOT_CONTAINED, contains("**/*.a", ["**/a"]))


    def test_the_newline_asymmetry_between_doublestar_and_star(self) -> None:
        """`**` compiles to `.`, which excludes `\\n`; `*` compiles to `[^/]`,
        which includes it. Modelling `**` as consuming everything made
        `contains("foo/*", ["foo/**"])` wrongly CONTAINED -- a false
        accusation, since `foo/a\\nb` matches the include and not the exclude.
        """
        self.assertTrue(glob_to_regex("foo/*").search("foo/a\nb"))
        self.assertFalse(glob_to_regex("foo/**").search("foo/a\nb"))
        self.assertEqual(NOT_CONTAINED, contains("foo/*", ["foo/**"]))
        self.assertEqual(NOT_CONTAINED, contains("??", ["**"]))

    def test_the_trailing_newline_anchor_quirk(self) -> None:
        """`glob_to_regex` anchors with `$`, which also matches before a
        single trailing newline, so `a` really does match `"a\\n"`. Modelling
        acceptance as the final state alone would make the include's language
        smaller than the matcher's -- again the false-accusation direction.
        """
        self.assertTrue(glob_to_regex("a").search("a\n"))
        self.assertFalse(glob_to_regex("a").search("a\n\n"))
        self.assertEqual(NOT_CONTAINED, contains("a", ["a\n"]))


class GlobTokenizerTests(unittest.TestCase):
    """`iter_glob_tokens` is the single traversal of the dialect, shared by
    `glob_to_regex` (every route and risk rule matches through it) and the
    containment NFA. A tokenizer regression moves both together, so the
    differential oracle cannot see it -- these are its direct pin.
    """

    def test_token_sequences(self) -> None:
        cases = {
            "*": [(GLOB_STAR, "*")],
            "**": [(GLOB_DOUBLESTAR, "**")],
            "***": [(GLOB_DOUBLESTAR, "**"), (GLOB_STAR, "*")],
            "**/": [(GLOB_DOUBLESTAR_SLASH, "**/")],
            "?": [(GLOB_QUESTION, "?")],
            "a**b": [(GLOB_LITERAL, "a"), (GLOB_DOUBLESTAR, "**"), (GLOB_LITERAL, "b")],
            "**/a": [(GLOB_DOUBLESTAR_SLASH, "**/"), (GLOB_LITERAL, "a")],
            "a/*": [(GLOB_LITERAL, "a"), (GLOB_LITERAL, "/"), (GLOB_STAR, "*")],
            "a\\b": [(GLOB_LITERAL, "a"), (GLOB_LITERAL, "/"), (GLOB_LITERAL, "b")],
            "": [],
        }
        for pattern, expected in cases.items():
            with self.subTest(pattern=pattern):
                self.assertEqual(expected, list(iter_glob_tokens(pattern)))

    def test_compiled_expressions_are_unchanged(self) -> None:
        expected = {
            "**/architecture/**": r"^(?:.*/)?architecture/.*$",
            "docs/**": r"^docs/.*$",
            "*": r"^[^/]*$",
            "a?b": r"^a[^/]b$",
        }
        for pattern, regex in expected.items():
            with self.subTest(pattern=pattern):
                self.assertEqual(regex, glob_to_regex(pattern).pattern)


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

    def test_case_folding_does_not_collapse_unrelated_literals(self) -> None:
        """The positive case above is satisfied vacuously by an engine whose
        alphabet has desynced from its literals -- everything becomes
        CONTAINED. This negative case is what actually pins the folding:
        dropping `.lower()` from `_alphabet` makes it fail.
        """
        self.assertEqual(NOT_CONTAINED, contains("Foo/**", ["bar/**"]))
        self.assertEqual(NOT_CONTAINED, contains("A/**", ["b/**"]))

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
