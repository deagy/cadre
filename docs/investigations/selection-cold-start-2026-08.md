# Where a one-shot `cadre select` spends its time

**Date:** 2026-08-12 · **Status:** answered, and acted on ·
**Measured at:** `perf/selection-regex-memoization` (159 roles, 1,219 routing patterns)

Memoizing the two matcher helpers made *repeated* selection ~100x faster
(188ms → 2ms warm) but left a one-shot `cadre select` almost unchanged: within
a single plan build there are ~1,370 compiles across ~1,220 distinct patterns,
so there was nearly no redundancy inside one build for a cache to remove.

That left cold start as the dominant remaining term and raised the open
question this records: **is the cold cost irreducible, or is the selector
compiling patterns it never needed?**

**The answer is the second one, decisively.** A cold selection compiled every
keyword matcher in the ruleset while ~3.6 of 870 could possibly match.

## The cost is keywords, not globs

Compiling each set from scratch (`re.purge()` plus both memo caches cleared,
best of five):

| Pattern set | Count | Cold compile cost |
| --- | ---: | ---: |
| Keyword matchers | 870 | **99.7 ms** |
| Glob matchers | 349 | 5.0 ms |

Keywords are **95%** of compile cost while being 71% of the patterns. Per
pattern they are ~8x more expensive, and the reason is in their shape:

```
(?<![a-z0-9-])<escaped keyword>(?![a-z0-9-])
```

The two boundary lookarounds are what makes whole-word matching work — a
keyword must not be embedded in a longer token — and they are also what makes
each pattern costly to compile. Dropping them (measured separately, not a
proposal — they are load-bearing semantics) more than halves compile time.

Globs, by contrast, compile to a plain anchored expression and are cheap
enough that they are not worth attacking.

## Almost none of those 870 keywords can match

A keyword's pattern is its literal text plus boundary assertions. A single
space compiles to `\s+`, which still requires each literal token to be
present. So this is a **necessary** condition for a match:

> every whitespace-separated token of the keyword appears as a substring of
> the text

Measured against the 175 real tasks in
`test/fixtures/selection_golden_corpus.json`:

| Keywords surviving the condition, per task | |
| --- | ---: |
| min | 0 |
| median | 4 |
| mean | **3.6** |
| max | 10 |

**0.4% of the ruleset.** The other 99.6% were being compiled to answer a
question a substring test already answered.

Two properties make this safe to act on rather than merely promising:

- **It is necessary, not heuristic.** Zero false negatives across all
  152,250 (task, keyword) pairs — the condition never rejected a keyword the
  regex matched.
- **It never decides a match.** The regex remains the sole authority; the gate
  can only skip work that would have returned `False`.

Note the shape of the ruleset that makes this so effective: **605 of 870
keywords are multi-word phrases** (`"abstraction layer"`, `"abuse case"`).
Requiring *every* token to be present is far more selective than requiring one.

## Result

Implemented in `routing._keyword_matches` as a pre-compile gate.

| | before | after |
| --- | ---: | ---: |
| Cold in-process selection | 100.6 ms | **6.3 ms** (16x) |
| One-shot `cadre select`, end to end | 231 ms | **130 ms** (1.8x) |

The end-to-end saving (~101 ms) matches the keyword compile cost exactly,
which is the check that the model above is right. The residual ~130 ms is
interpreter startup, imports, and git inspection — not selection, and not
addressable here.

Selection output is unchanged: the 175-case golden corpus passes as-is, and
plans are byte-identical across sampled tasks.

## What this does not fix

- **Cold cost still grows with the ruleset**, just from a much lower base. The
  gate is O(keywords x tokens) in cheap substring tests instead of
  O(keywords) in regex compilation; the surviving compiles scale with how
  much of the task text overlaps the vocabulary, not with the roster.
- **Interpreter startup now dominates** a one-shot invocation. Anything
  further on that axis is about import cost and process model, not matching —
  a different investigation, and one with a much worse effort-to-payoff ratio
  than this had.
- **`re`'s 512-entry cache is still oversubscribed** at 1,219 patterns. That
  no longer matters, because neither matcher relies on it. It would matter
  again for any new code that compiles patterns inline;
  `test_selection_cost.py` is what would catch that.

## Guards

`test_selection_cost.py` gained four tests, on top of the three it already
carried for the memoization:

- Full cross-product equivalence between the gated and ungated
  implementations over the corpus — the property that makes the gate safe.
- Case-insensitivity, because the pattern carries `re.IGNORECASE` while the
  gate does substring tests. Every current caller lowercases first, which is
  precisely why a case-sensitive gate would have gone unnoticed.
- Multi-word adjacency, pinning that the gate checks *presence* while the
  regex still decides *order* — that is, that the gate has not quietly become
  the decision-maker.
- A cold-selection compile budget, generous and documented as a cliff
  detector rather than a benchmark.
