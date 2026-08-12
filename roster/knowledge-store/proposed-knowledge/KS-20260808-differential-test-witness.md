---
id: "KS-20260808-differential-test-witness"
title: "a differential test can pass with its subject disabled"
status: "accepted"
evidence:
  - "roster/orchestration/test/test_glob_containment.py"
  - "PR #163"
origin:
  artifact: "roster/orchestration/test/test_glob_containment.py"
  revision: "9cffcfa (merged from 05a9b97)"
  task: "review of PR #163"
proposed_classification: "internal"
source_scope: "testing, code-review, build"
sensitivity_notes: ""
conflicts_or_staleness: ""
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "knowledge-store-steward"
content_digest: "fa099dd5615ea638b13b1fd57eb0d5f95cff392c66ff11ef3af326c34f8d941e"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "daniel.eagy@gmail.com"
  diverged_from_proposal: false
  reason: "Durable test-design lesson, not tied to code that can move: a differential test comparing an engine to an oracle can pass with the engine disabled when only one verdict direction is exercised. Cited test_glob_containment.py resolves in the current tree. Human decision, recorded because knowledge-store-steward staged this record and cannot disposition its own proposal."
---

## Summary

A differential test that compares an engine against a brute-force oracle can
pass while the engine is entirely disabled, if the comparison only ever
exercises one direction of the verdict.

The containment engine was tested against an oracle that enumerated short paths
over a small alphabet and checked them with the real matcher. The test compared
the engine's verdict to the oracle's on thousands of pattern pairs and passed.
It also passed when the engine was stubbed out — because the corpus was
dominated by cases where both sides agreed on the *same* answer for structural
reasons, and nothing forced the engine to have produced its verdict by actually
deciding anything.

Two changes fixed it, and both are the general lesson:

1. **Make the engine produce a checkable artifact, not just a verdict.**
   `contains_with_witness()` returns a concrete counterexample path alongside a
   NOT_CONTAINED verdict. The test then validates that witness against
   `glob_to_regex` independently — the include must match it and no exclude may.
   A stubbed engine cannot fabricate a witness that survives that check.
2. **Assert a floor in both directions.** The test now requires a minimum
   number of CONTAINED verdicts as well as NOT_CONTAINED ones, so a corpus that
   silently degenerates into one answer fails rather than passes.

## Reusable rule

A test that compares two implementations proves only that they agree. Agreement
is not correctness, and it is not even evidence that both ran. Where possible,
require the implementation under test to emit something that can be verified
independently of the oracle — a witness, a certificate, a reconstruction — and
check that artifact.

Separately: any test whose corpus is generated rather than enumerated by hand
should assert that the corpus actually covered the cases it claims to, because
a generator that drifts into a degenerate distribution weakens the test
silently.

## Recommended Retrieval Use

Retrieve for test, code-review, and build agents writing differential tests,
property-based tests, oracle comparisons, or fuzzing harnesses. Also relevant to
any agent assessing whether an existing test suite is load-bearing.

## Steward Notes

Do not ingest until the steward verifies scope and classification. This record
generalizes past the specific module; it should be retrievable for testing work
anywhere, not only for glob or routing work.
