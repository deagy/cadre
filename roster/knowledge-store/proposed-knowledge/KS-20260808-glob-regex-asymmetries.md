---
id: "KS-20260808-glob-regex-asymmetries"
title: "`glob_to_regex` newline and anchor asymmetries"
status: "accepted"
evidence:
  - "roster/orchestration/src/routing.py"
  - "roster/orchestration/src/glob_containment.py"
  - "PR #163"
origin:
  artifact: "roster/orchestration/src/routing.py, roster/orchestration/src/glob_containment.py"
  revision: "9cffcfa (merged from 44e3f4e)"
  task: "review of PR #163"
proposed_classification: "internal"
source_scope: "routing, build, code-review"
sensitivity_notes: ""
conflicts_or_staleness: "describes current behaviour of `glob_to_regex`; goes stale if the function changes and should be re-verified before reliance"
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "knowledge-store-steward"
content_digest: "96a3ce4953066f4575a764bb1ff38068d3820acbcdb0bdcded153f6e32e974c1"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "repository owner (Product Owner per roster/shared/team-profile.yaml)"
  diverged_from_proposal: false
  reason: "Accepted by the repository owner on 2026-08-09 with its staleness risk understood rather than dismissed. It documents current glob_to_regex behaviour and says so itself. Accepted because the cost of rediscovery is demonstrated: two reviewers independently assumed the opposite of the newline and anchor behaviours, and a dispatch brief asserted substring matching when the matcher is whole-word. No deletion capability exists, so if glob_to_regex changes this record must be corrected by update or reclassify, never removed."
---

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
