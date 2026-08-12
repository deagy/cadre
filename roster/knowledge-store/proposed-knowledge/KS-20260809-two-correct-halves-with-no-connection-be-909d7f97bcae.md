---
id: "KS-20260809-two-correct-halves-with-no-connection-be-909d7f97bcae"
title: "two correct halves with no connection between them"
status: "accepted"
evidence:
  - "PR #183 -- dispatch contract said summary, implementation required body"
  - "PR #170 -- untrusted_instruction_risk had no field to travel in"
  - "PR #171 -- teardown wait held only by call-site convention"
  - "roster/knowledge-store/src/finding_record.py"
origin:
  artifact: "dispatch-contract.md and finding_record.py"
  revision: "3cf35c2"
  task: "issue-165 review-time capture"
proposed_classification: "internal"
source_scope: "code review, contract design, multi-agent orchestration, testing"
sensitivity_notes: ""
conflicts_or_staleness: "sharpens KS-20260809-a-rule-whose-signal-cannot-reach-it: that record is about a rule that cannot fire, this is about two halves that never connect"
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "orchestrator"
content_digest: "909d7f97bcae75624dbffe1a97f5a34d02295be5af45ee8383bb9ba86be83c11"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Substantially overlaps KS-20260809-a-rule-whose-signal-cannot-reach-it (two of three cited instances are shared) but contributes a genuinely new instance -- the dispatch-contract summary/body mismatch verified against roster/knowledge-store/src/finding_record.py -- and frames a distinct, complementary rule (run the documented path end-to-end rather than trusting that both halves read correctly). Accepting both; the steward notes the overlap for future consolidation rather than treating it as disqualifying."
---

## Summary

A contract and its implementation can each be internally consistent, fully
tested, and still not meet. The seam between them is owned by neither side, so
neither side's tests cover it.

Three instances in one day:

- A steward rule required deferring any candidate flagged as an injection risk.
  The retrieval layer surfaced that flag; the handoff item that repackaged the
  content had no field to carry it. The rule could never fire.
- A teardown wait was registered by one test helper. Nothing required callers to
  use the helper, so the protection was call-site convention rather than
  structure. Disabling the registration reproduced the original race.
- A dispatch contract told agents to return `summary`. The implementation that
  consumed it required `body`. Both halves passed their own suites.

In every case the halves were produced separately -- by different agents, or by
the same author on different days -- and each was correct in isolation.

## Reusable rule

When a contract and its consumer are authored separately, neither side's tests
cover the seam. Run the documented path end to end, in the shape the contract
tells the other side to produce, and watch it work. Reading both halves is not
sufficient: they were both written to be readable, and they both read correctly.

Specifically: take the exact input the documentation instructs a producer to
emit, feed it to the real consumer, and confirm the result. If the two were
written by different people or different agents, assume the seam is broken until
an execution proves otherwise.

## Recommended Retrieval Use

Retrieve for review, test, and build agents whenever a change spans a contract
and its implementation, or when work has been split across parallel authors.
Also retrieve when consolidating multi-agent output, where each contributor sees
only its own half.