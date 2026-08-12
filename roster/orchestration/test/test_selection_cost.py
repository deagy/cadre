"""Cliff detector for selection's regex-compilation cost.

`routing.match_rule` runs every route's and risk rule's keyword and glob
matchers on every selection. Before the memoization in `routing.py`, each of
those matchers was rebuilt per call and the module relied on `re`'s own compile
cache to absorb the repetition -- which it stopped doing once the ruleset grew
past that cache's 512 entries. The result was ~1,220 compiles per
`build_dispatch_plan()` call and roughly 94% of selection wall time spent
inside `re._compile`, a ~21x slowdown that accumulated silently across three
roster expansions because nothing measured it.

These tests are that missing measurement. They are deliberately *cliff
detectors*, not precise benchmarks:

- The compile budget below is generous on purpose. A tight bound would need
  raising every time the routing table legitimately grows, and a threshold
  people learn to raise reflexively catches nothing. This one only trips on an
  order-of-magnitude regression -- exactly the failure that actually occurred.
- Nothing here asserts wall time. Wall-clock thresholds are flaky on shared CI
  runners, and they fail for reasons unrelated to the defect being guarded.

`test_memoized_matchers_do_not_recompile` is the tighter, more direct of the
two: it pins the invariant (a warm ruleset compiles nothing further) rather
than a number, so it stays meaningful no matter how large the roster grows.
"""

from __future__ import annotations

import json
import re
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
import routing  # noqa: E402
from build_dispatch_plan import build_dispatch_plan  # noqa: E402
from routing import load_catalog, load_routing  # noqa: E402

CONFIG = load_routing(ROOT / "routing.json")
CATALOG = load_catalog(AGENTS_ROOT / "catalog.yaml")

# Compiles permitted during one selection whose ruleset is already warm.
#
# The real number after memoization is 0 -- every keyword and glob matcher is
# served from cache. 200 leaves generous room for incidental compilation
# elsewhere in the plan-building path (and for future code that legitimately
# compiles a handful of patterns) while still failing loudly on a return of the
# per-call recompilation this guards: that regime compiled ~1,220.
MAX_COMPILES_PER_WARM_SELECTION = 200

# Compiles permitted during one selection in a *cold* process, where nothing is
# cached yet. This is the budget the `_keyword_matches` compile-avoidance gate
# defends: without it a cold selection builds every keyword matcher it touches
# (~1,220 patterns, ~100ms, 95% of it keywords); with it, only the handful of
# keywords whose literal tokens actually occur in the task. Real number on the
# sample case is ~40 (the globs, which are cheap and mostly unavoidable, plus a
# few surviving keywords). 400 is generous enough to absorb ruleset growth and
# a differently-shaped task while still failing on a return of compile-
# everything behaviour.
MAX_COMPILES_PER_COLD_SELECTION = 400


def _sample_case() -> dict[str, Any]:
    """Reuse the golden corpus's first fixture rather than inventing a task.

    A hand-written task here could drift into matching nothing, which would
    make this guard pass by doing no work at all -- the corpus is already
    maintained to keep its fixtures matching real routes.
    """
    payload = json.loads(FIXTURES_PATH.read_text(encoding="utf-8"))
    return payload["cases"][0]


CASE = _sample_case()


def _run_selection() -> dict[str, Any]:
    values = {
        "task": CASE["task"],
        "changed_files": CASE["changed_files"],
        "changed_file_source": "test",
        "repository_root": str(AGENTS_ROOT.parent),
        "sources": ["example/repository"],
        "classification": CASE.get("classification", "internal"),
        "task_id": CASE["task_id"],
    }
    # Standalone mode, for the same reason the golden corpus forces it: the
    # lifecycle contract shells out, and whether it resolves is a property of
    # the host, not of selection cost.
    with mock.patch.object(build_dispatch_plan_module, "try_lifecycle_contract", return_value=None):
        return build_dispatch_plan(CONFIG, CATALOG, values)


class SelectionCompilationCostTest(unittest.TestCase):
    def test_memoized_matchers_do_not_recompile_a_warm_ruleset(self) -> None:
        """A second selection over the same ruleset compiles nothing new.

        This is the invariant the memoization exists to provide, asserted
        directly through `lru_cache`'s own bookkeeping rather than through a
        threshold -- so it keeps holding as the roster grows.
        """
        _run_selection()  # warm every matcher

        glob_before = routing.glob_to_regex.cache_info()
        keyword_before = routing._keyword_regex.cache_info()

        _run_selection()

        glob_after = routing.glob_to_regex.cache_info()
        keyword_after = routing._keyword_regex.cache_info()

        self.assertEqual(
            glob_after.misses,
            glob_before.misses,
            "glob_to_regex recompiled on a warm ruleset -- its memoization is not "
            "holding (cache too small, or a caller is passing freshly-built "
            "pattern strings).",
        )
        self.assertEqual(
            keyword_after.misses,
            keyword_before.misses,
            "_keyword_regex recompiled on a warm ruleset -- its memoization is not "
            "holding. Check that nothing keys the cache on task text.",
        )
        # A cache that never *hits* would satisfy the equalities above by
        # never being consulted at all, so pin that it is genuinely in use.
        self.assertGreater(
            keyword_after.hits,
            keyword_before.hits,
            "_keyword_regex served no cached pattern during a selection -- the "
            "matcher is no longer routed through it.",
        )

    def test_pattern_caches_are_large_enough_for_the_whole_ruleset(self) -> None:
        """Every distinct pattern fits in cache simultaneously.

        The original defect was not a missing cache but an *oversubscribed*
        one: `re`'s 512 entries against ~1,220 patterns, which evicts faster
        than it fills. A cache smaller than the ruleset reintroduces exactly
        that, so compare the two directly.
        """
        _run_selection()

        for name, info in (
            ("glob_to_regex", routing.glob_to_regex.cache_info()),
            ("_keyword_regex", routing._keyword_regex.cache_info()),
        ):
            with self.subTest(cache=name):
                self.assertIsNotNone(
                    info.maxsize,
                    f"{name} is unbounded; see routing._PATTERN_CACHE_SIZE for why "
                    "it is deliberately bounded instead.",
                )
                self.assertLess(
                    info.currsize,
                    info.maxsize,
                    f"{name} is full ({info.currsize}/{info.maxsize}) and will begin "
                    "evicting patterns it still needs -- raise "
                    "routing._PATTERN_CACHE_SIZE.",
                )

    @unittest.skipUnless(
        hasattr(re, "_compile"),
        "re._compile is unavailable on this interpreter; the invariant test above "
        "still covers the memoization itself.",
    )
    def test_warm_selection_stays_under_the_compile_budget(self) -> None:
        """Total compilations per warm selection stay off the cliff.

        Counts `re._compile` rather than `re.compile`: the module-level
        `re.search(pattern, ...)` form routes through the former and bypasses
        the latter, and that form was where the larger half of the original
        regression lived. A guard that only saw `re.compile` would have missed
        it.
        """
        _run_selection()  # warm every matcher first

        real_compile = re._compile
        calls = 0

        def counting_compile(*args: Any, **kwargs: Any) -> Any:
            nonlocal calls
            calls += 1
            return real_compile(*args, **kwargs)

        with mock.patch.object(re, "_compile", counting_compile):
            _run_selection()

        self.assertLessEqual(
            calls,
            MAX_COMPILES_PER_WARM_SELECTION,
            f"a warm selection compiled {calls} regexes, over the "
            f"{MAX_COMPILES_PER_WARM_SELECTION} budget. This is the signature of "
            "per-call regex construction returning to the matching path (see this "
            "module's docstring). Fix the recompilation rather than raising the "
            "budget -- it is sized to ignore ordinary growth.",
        )


class KeywordGateTest(unittest.TestCase):
    """The compile-avoidance gate in `_keyword_matches` must change nothing.

    It skips building a keyword's regex when a necessary condition fails. If
    that condition were ever *not* necessary, the gate would start deciding
    matches -- returning False for a keyword the regex would have matched, and
    silently changing which routes fire. These tests are what makes the gate
    safe to keep.
    """

    def _all_keywords(self) -> list[str]:
        keywords: set[str] = set()

        def walk(node: Any) -> None:
            if isinstance(node, dict):
                for key, value in node.items():
                    if key == "keywords" and isinstance(value, list):
                        keywords.update(value)
                    elif key == "keyword_groups" and isinstance(value, list):
                        for group in value:
                            keywords.update(group or [])
                    else:
                        walk(value)
            elif isinstance(node, list):
                for item in node:
                    walk(item)

        walk(CONFIG)
        return sorted(keywords)

    def test_gate_agrees_with_the_regex_on_every_corpus_task(self) -> None:
        """Full cross-product equivalence against the unguarded implementation."""
        keywords = self._all_keywords()
        self.assertGreater(len(keywords), 100, "ruleset keywords failed to load")

        cases = json.loads(FIXTURES_PATH.read_text(encoding="utf-8"))["cases"]
        tasks = [case["task"] for case in cases]

        disagreements: list[str] = []
        for task in tasks:
            for keyword in keywords:
                gated = routing._keyword_matches(task, keyword)
                # The regex alone, with no gate -- the pre-gate behaviour.
                ungated = routing._keyword_regex(keyword).search(task) is not None
                if gated != ungated:
                    disagreements.append(f"{keyword!r} vs {task!r}: gate={gated} regex={ungated}")

        self.assertEqual(
            [],
            disagreements[:10],
            f"{len(disagreements)} keyword/task pair(s) where the compile-avoidance "
            "gate disagrees with the regex. The gate must only skip work that "
            "would have returned False.",
        )

    def test_gate_is_case_insensitive_like_the_pattern(self) -> None:
        """A mixed-case caller must not be silently rejected.

        The pattern carries re.IGNORECASE, so a case-sensitive gate would fail
        a keyword the regex matches. Every current caller lowercases first,
        which is exactly why this would go unnoticed.
        """
        self.assertTrue(routing._keyword_matches("Rotate the SIGNING keys", "signing"))
        self.assertTrue(routing._keyword_matches("TERRAFORM module", "terraform"))

    def test_multi_word_keywords_still_require_adjacency(self) -> None:
        """The gate checks presence; the regex still decides order/adjacency.

        Both tokens present but not adjacent must NOT match -- otherwise the
        gate would have become the decision-maker.
        """
        self.assertTrue(routing._keyword_matches("an abstraction layer", "abstraction layer"))
        self.assertFalse(
            routing._keyword_matches("the layer sits above every abstraction", "abstraction layer")
        )

    @unittest.skipUnless(hasattr(re, "_compile"), "re._compile unavailable on this interpreter")
    def test_cold_selection_stays_under_the_compile_budget(self) -> None:
        """The cold path is the one the gate exists for."""
        real_compile = re._compile
        calls = 0

        def counting_compile(*args: Any, **kwargs: Any) -> Any:
            nonlocal calls
            calls += 1
            return real_compile(*args, **kwargs)

        # A genuinely cold process: both memo caches empty and re's own cache
        # purged, so every compile that happens is counted.
        re.purge()
        routing.glob_to_regex.cache_clear()
        routing._keyword_regex.cache_clear()
        routing._keyword_tokens.cache_clear()

        with mock.patch.object(re, "_compile", counting_compile):
            _run_selection()

        self.assertLessEqual(
            calls,
            MAX_COMPILES_PER_COLD_SELECTION,
            f"a cold selection compiled {calls} regexes, over the "
            f"{MAX_COMPILES_PER_COLD_SELECTION} budget. The compile-avoidance gate in "
            "routing._keyword_matches is no longer filtering -- check that it still "
            "runs before _keyword_regex, not after.",
        )


if __name__ == "__main__":
    unittest.main()
