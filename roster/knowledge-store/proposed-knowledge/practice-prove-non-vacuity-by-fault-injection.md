# Proposed Knowledge: prove a guard is non-vacuous by injecting a fault

Status: proposed for knowledge-store-steward review
Classification: internal
Source task: accumulated across reviews of PRs #161, #163, #164 and their
predecessors, 2026-07 to 2026-08-09
Origin revision: practice, not code — no single revision
Recommended steward action: ingest
Sensitivity: none
Conflicts or staleness: none known; complements but does not duplicate the two
PR #163 records

## Summary

The most frequently rediscovered defect in this repository is a **test or guard
that passes while verifying nothing**. It has been found roughly a dozen times
in a single session, and in every instance it was surfaced by *executing*
something, never by reading the test.

Reading a test tells you what its author intended. It does not tell you whether
the assertion is reachable, whether the fixture still exercises the code path,
whether the corpus degenerated, or whether the subject under test is even
invoked. Those questions are only answerable by breaking the thing the test
claims to protect and watching what happens.

Instances found this way include: a soundness argument that ran backwards; a
differential test that passed with its subject disabled; a case-folding
regression invisible to all fifty tests covering its module; a drift guard
whose failure was hidden by redirecting generator output to `/dev/null`; and a
pre-commit hook that exited 2 without ever running its check because `argparse`
rejected a flag the hook passed.

## The practice

For any test, guard, linter, or CI check whose value depends on it failing when
something is wrong:

1. Inject a **real** fault — not a syntax error, but the specific defect the
   check exists to catch. Delete the feature, corrupt one field, disable the
   subject, remove one element from a list.
2. Run the check and confirm it **fails**.
3. Read the failure message and confirm it **names the real problem**. A guard
   that fails with an unhelpful message is only half working; the next person to
   hit it will spend the difference.
4. Revert the injection.
5. Confirm the tree is clean (`git status --short`) before moving on.

Report the exact commands and the observed output. "I verified the test is
non-vacuous" without the transcript is the same claim the vacuous tests were
already making.

## Corollaries

- **Never suppress a generator's or checker's output.** Redirecting to
  `/dev/null` has hidden at least one real failure that then reached CI. If
  output is too noisy to read, filter it — do not discard it.
- **A guard that catches real content on its first run has demonstrated its own
  non-vacuity**, and that is worth recording when it happens, because it is
  rare.
- **Distinguish verified from inferred** in any review report. A finding derived
  by reading is a hypothesis; a finding reproduced by running is evidence. Say
  which one you have.

## Recommended Retrieval Use

Retrieve for test, code-review, build, and verification agents whenever the task
involves adding, changing, or assessing a test, CI check, linter, schema
validator, drift guard, or pre-commit hook. Also retrieve when asked whether an
existing suite is adequate.

## Steward Notes

Do not ingest until the steward verifies scope and classification. This is the
highest-value candidate in the current batch: it is the general practice the
other records are instances of. Consider whether it belongs as operational
knowledge, as a decision overlay, or as both.
