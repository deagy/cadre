# Proposed Knowledge: sampling cannot establish a universal claim

Status: proposed for knowledge-store-steward review
Classification: internal
Source task: review of PR #163 (`roster/orchestration/src/routing_health.py`,
`glob_containment.py`), 2026-08-08
Origin revision: `9cffcfa` (merged), superseding the withdrawn implementation on
the same branch
Recommended steward action: ingest
Sensitivity: none — no credentials, no local paths, no personal data
Conflicts or staleness: none known; supersedes nothing

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
