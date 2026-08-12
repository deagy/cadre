---
id: "KS-20260808-sampling-universal-claim"
title: "sampling cannot establish a universal claim"
status: "accepted"
evidence:
  - "roster/orchestration/src/routing_health.py"
  - "roster/orchestration/src/glob_containment.py"
  - "PR #163"
origin:
  artifact: "roster/orchestration/src/routing_health.py, roster/orchestration/src/glob_containment.py"
  revision: "9cffcfa (merged)"
  task: "review of PR #163"
proposed_classification: "internal"
source_scope: "testing, code-review, build, validation"
sensitivity_notes: ""
conflicts_or_staleness: ""
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "knowledge-store-steward"
content_digest: "e3c19604b74866645efb8354c018b7469c4b763857f2be6d622c08e6007d6232"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "daniel.eagy@gmail.com"
  diverged_from_proposal: false
  reason: "Accepted: a universal claim cannot be established by sampling, and inverting that produces false accusations rather than missed findings. Generalizes beyond the exclude_paths check it came from. Both cited files (routing_health.py, glob_containment.py) resolve. Human decision; the staging steward could not decide it."
---

## Summary

A check whose finding is a *universal* claim ("every path this glob matches is
excluded") cannot be implemented by sampling, and getting this backwards
produces false accusations rather than missed findings.

The first implementation of the `exclude_paths`-shadowing check synthesized
probe paths from an include glob and reported the glob as fully shadowed when
every probe was excluded. The intuition — "sampling is incomplete, so it will
miss things" — is the wrong direction for this shape of claim. Because the
finding asserts something about *all* matching paths, an incomplete sample makes
the assertion **easier** to satisfy: fewer probes means fewer chances to find
the counterexample that would withdraw the finding. The failure mode is
therefore a correct `routing.yaml` failing CI, not a defect slipping through.

Concrete false positives that the sampling implementation produced:

- `paths: ["**/*.go"]` with `exclude_paths: ["**/probe.go"]`
- `paths: ["a/?.go"]` with `exclude_paths: ["a/x.go"]`
- `paths: ["roster/**"]` with `exclude_paths: ["**/*.txt"]` — reported as fully
  shadowed only because every synthesized probe happened to end in `.txt`

The fix was to replace the guess with a decision procedure. The glob dialect
(`**/`, `**`, `*`, `?`, literals) describes regular languages, so
`L(include) ⊆ ⋃L(excludes)` is decidable exactly: compile each glob to an
ε-NFA over a finite abstract alphabet, then search the product of the include
NFA against the determinized union of the exclude NFAs for a reachable state
accepting the include and no exclude.

## Reusable rule

Before implementing a check, classify its finding. If the finding is universally
quantified over an infinite or large domain, sampling cannot support it —
either find a decision procedure, or invert the check so that what is sampled is
the *existence* of a counterexample (which sampling can legitimately establish,
in the safe direction).

When no decision procedure is affordable, the check must return a third verdict
— "undetermined" — and report nothing on it, so that imprecision costs a missed
finding instead of a false accusation.

## Recommended Retrieval Use

Retrieve for code-review, test, and build agents designing any static check,
linter, or CI guard, particularly one that reports a property holding over all
inputs. Also relevant to agents working on routing, glob matching, or path
coverage analysis in this repository.

## Steward Notes

Do not ingest until the steward verifies scope and classification. The
implementation this describes is merged and current; the record is a general
rule derived from it, not a description of the code, so it should not go stale
if `glob_containment.py` changes.
