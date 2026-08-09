# Proposed Knowledge: `glob_to_regex` newline and anchor asymmetries

Status: proposed for knowledge-store-steward review
Classification: internal
Source task: review of PR #163 (`roster/orchestration/src/routing.py`,
`glob_containment.py`), 2026-08-08
Origin revision: `44e3f4e` on branch `agent/routing-health-exclude-shadow`,
merged as `9cffcfa`
Recommended steward action: ingest
Sensitivity: none
Conflicts or staleness: this record describes current behaviour of a specific
function; it goes stale if `glob_to_regex` changes and should be re-verified
before reliance

## Summary

`roster/orchestration/src/routing.py`'s `glob_to_regex` has three behaviours
that are surprising and that any exact reasoning over the glob dialect must
model. All three were found during review, and two of them by independent
reviewers who had each assumed the opposite.

1. **`**` and `*` disagree about newline.** `**` compiles to `.`, which excludes
   `\n` because `re.DOTALL` is not set. `*` and `?` compile to `[^/]`, which
   *includes* `\n`. So `foo/a\nb` matches `foo/*` but not `foo/**` — the broader
   wildcard is narrower on exactly one character.
2. **`$` is not `\Z`.** The compiled pattern anchors with `$`, which also
   matches immediately before a single trailing newline. `glob_to_regex("a")`
   therefore matches `"a\n"`.
3. **Keyword matching is whole-word, not substring.** `_keyword_matches`
   (`routing.py:35`) uses `(?<![a-z0-9-])…(?![a-z0-9-])`, treating hyphens as
   word characters. So `"runner"` matches "the runner failed" but not
   "cross-runner", "runner-info", or "runners". Reviewers have twice assumed
   substring semantics and reasoned from it incorrectly.

## Why it matters

Any analysis that models this dialect — a containment decision procedure, a
coverage checker, a linter over `routing.yaml` — must reproduce these
asymmetries rather than the intuitive semantics, or it will disagree with the
matcher on real inputs. During PR #163 the correct fix was to model the
asymmetry in the analysis, **not** to normalize `glob_to_regex`: changing the
matcher would have silently altered routing behaviour for every consumer.

That is the general point. When an analysis and its subject disagree, the
analysis is usually the thing that should change, because the subject's
behaviour is already depended upon.

## Recommended Retrieval Use

Retrieve for build, code-review, and test agents working on
`roster/orchestration/` routing, glob matching, path coverage, or keyword
selection in this repository. Not relevant outside it — the dialect is
repository-specific.

## Steward Notes

Do not ingest until the steward verifies scope and classification. This is the
most implementation-coupled record in the batch and the most likely to need
re-verification later; consider a shorter retention or a review trigger tied to
`routing.py`.
