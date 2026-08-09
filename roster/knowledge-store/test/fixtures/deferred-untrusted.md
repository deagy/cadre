---
id: KS-20260103-fixture-untrusted
title: "a candidate carrying an injection-risk signal"
status: deferred
evidence:
  - "fixture"
origin:
  task: "fixture"
  artifact: "fixture"
  revision: "0000002"
proposed_classification: internal
source_scope: testing
sensitivity_notes: "derived from retrieved content"
conflicts_or_staleness: ""
recommended_action: defer
untrusted_instruction_risk: true
staged_by: fixture-author
content_digest: f538507194de3eac85389ba13c65aa3ee8820c488b732e934e586a0463a9ba8b
disposition:
  action: deferred
  reason: "automatic defer: the candidate carries untrusted_instruction_risk"
  classification_used: internal
  diverged_from_proposal: false
  decided_by: fixture-steward
---

## Summary

Exercises the automatic-defer rule: a record flagged
`untrusted_instruction_risk: true` may not be accepted.
