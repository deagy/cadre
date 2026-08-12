---
id: "KS-20260808-abstraction-fold"
title: "test an abstraction at the boundary it folds"
status: "accepted"
evidence:
  - "roster/orchestration/src/glob_containment.py"
  - "PR #163"
origin:
  artifact: "roster/orchestration/src/glob_containment.py"
  revision: "9cffcfa (merged from e036c3b..05a9b97)"
  task: "review of PR #163"
proposed_classification: "internal"
source_scope: "testing, code-review"
sensitivity_notes: ""
conflicts_or_staleness: ""
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "knowledge-store-steward"
content_digest: "0d43776baaa96640a330e6d3c504cfd515aad5b54e7d270d06bd1f2ec8a1d59d"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "daniel.eagy@gmail.com"
  diverged_from_proposal: false
  reason: "Accepted: an implementation that collapses distinct values (case folding, bucketing, sentinels) is untested unless inputs vary across the fold. Applies to any normalizing abstraction, not only the containment engine. Cited glob_containment.py resolves. Human decision; the staging steward could not decide it."
---

## Summary

When an implementation deliberately collapses distinct values into one — case
folding, normalization, bucketing, a sentinel for "everything else" — a test
suite that never varies inputs *across* that fold cannot detect the fold
breaking.

The containment engine decides over an abstract alphabet: the literal characters
appearing in the patterns, plus `/`, plus one sentinel for every other
character. Literals are folded with `str.lower()` to mirror the matcher's
`re.IGNORECASE`. Removing that `.lower()` makes `contains("Foo/**", ["bar/**"])`
return CONTAINED — a false accusation. All fifty tests covering the module
passed with the fold removed, because every pattern in every test was already
lowercase. The suite had no way to notice.

The fix was to vary the inputs across the fold: an uppercase-bearing oracle
alphabet, uppercase patterns in the generated corpus, and a direct negative
assertion pinning the specific wrong answer.

A second instance in the same module: the `_OTHER` sentinel standing for
"any character appearing in no pattern" is a multi-character token (`"\0other"`)
precisely so that no single-character pattern literal can ever equal it.
Dropping that precaution is likewise invisible to any test whose patterns
contain no NUL.

## Reusable rule

For every deliberate collapse in an implementation, ask: what input would
distinguish the two sides of this fold, and does any test contain it? If not,
the fold is untested regardless of how many tests touch the module.

This applies to case folding, Unicode normalization, whitespace collapsing,
path canonicalization, numeric bucketing, hash-based partitioning, and sentinel
values. It is a specific, checkable instance of the broader non-vacuity
practice: coverage of a module is not coverage of its abstractions.

## Recommended Retrieval Use

Retrieve for test, code-review, and build agents working on normalization,
canonicalization, case-insensitive matching, abstract interpretation, or any
code path that maps many inputs onto one representative.

## Steward Notes

Do not ingest until the steward verifies scope and classification. Overlaps
deliberately with the non-vacuity practice record; the steward may prefer to
merge them or to keep this one as the concrete, checkable sub-case.
