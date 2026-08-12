---
id: "KS-20260812-a-default-that-no-contract-declares-fire-ccba329bbcab"
title: "A default that no contract declares fires universally, and a stubbed-contract test corpus cannot see it"
status: "accepted"
evidence:
  - "roster/orchestration/src/build_dispatch_plan.py:107, :547-551, :673"
  - "kernel/contracts/lifecycle-gates.json (zero occurrences of review_agents/author_agents)"
  - "roster/orchestration/test/test_selection_golden_corpus.py:135"
origin:
  artifact: "roster/orchestration/src/build_dispatch_plan.py"
  revision: "a482e68c"
  task: "cadre-review-portable-platform-2026-08-11 (PR #240 architecture review)"
proposed_classification: "internal"
source_scope: "deagy/cadre"
sensitivity_notes: "No secrets, credentials, or personal data. Repository-relative paths only."
conflicts_or_staleness: "Extends OD-9 as recorded in the PR #240 records, which frame the defect as output churn only. If OD-9 is resolved by moving the default into roster or profile data, the file:line citations go stale but the pattern does not."
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "architecture-authority"
content_digest: "ccba329bbcab29ca71362b676fae40349d507eafa9ef33ddc103915594bb62f3"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified: current build_dispatch_plan.py:_gate_agents docstring confirms the exact defect (OD-9) and states it has since been fixed by making the default roster-supplied. The specific line citations are now stale (the hardcode is gone, replaced by a parameter) but the record is explicit that its citations would go stale under a fix while the pattern (untested default from an external contract, corpus stubs the contract so it's blind to the default) remains generalizable and durable. Retained for the pattern, with staleness noted."
---

build_dispatch_plan.py:107 defaults a lifecycle gate's reviewers to ["code-reviewer"] when the gate contract declares no review_agents. No gate in kernel/contracts/lifecycle-gates.json declares review_agents OR author_agents, so the default fires for every configured gate on every lifecycle-aware plan and is appended to `support` (:673). The 175-case golden corpus cannot observe it because test_selection_golden_corpus.py:135 patches try_lifecycle_contract to None.

Generalizable lesson: a `.get(key, default)` against an external contract that never declares `key` is not a fallback -- it is an unconditional hardcode wearing a fallback's clothes, and a corpus that stubs the contract for determinism is structurally blind to it.

Second consequence, found only by tracing to _validate_agents (:547-551): the same default makes plans against a foreign catalog raise ValueError rather than degrade.