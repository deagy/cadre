---
id: "KS-20260812-a-guard-scoped-to-the-violation-you-alre-49bad7b59d3c"
title: "A guard scoped to the violation you already know about passes green with the coverage wrong"
status: "accepted"
evidence:
  - "roster/orchestration/test/test_roster_boundary.py (TestNoCadreRoleIdsInPlatformCode)"
  - "roster/orchestration/mcp/dispatch_core.py (the module the guard did not cover)"
  - "roster/orchestration/runs/cadre-feature-portable-platform-2026-08-11/phase-0-and-d-evidence.md"
origin:
  artifact: "roster/orchestration/test/test_roster_boundary.py"
  revision: "fb948ec8"
  task: "cadre-feature-portable-platform-2026-08-11 (Phase C'-1)"
proposed_classification: "internal"
source_scope: "deagy/cadre"
sensitivity_notes: "Describes a test-authoring pattern. No secrets or credentials."
conflicts_or_staleness: "Complements KS-20260809-non-vacuity-fault-injection (staged, accepted), which covers proving a guard CAN fail. This covers a guard that can fail and still does not cover its own rule -- a distinct failure the first record does not reach."
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "orchestrating-session"
content_digest: "49bad7b59d3c56ddba717533ef670c6fe85a3855b909cd39be9aa2e5bcf02dae"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified test_roster_boundary.py's TestNoCadreRoleIdsInPlatformCode docstring directly corroborates the record: the guard originally scoped to build_dispatch_plan.py passed 12/12 with a hole, found only by planting the defect in mcp/dispatch_core.py. Role ids are now read from the catalog rather than hand-listed, matching the record's recommended practice. Accurate, durable, and the fix is already shipped."
---

A boundary guard was written with its category-A check scoped to the single module where the known defect lived. It passed 12 of 12 tests. Fault injection then planted the same class of defect in a *different* in-scope module and the guard did not fire.

The distinction this draws is finer than the usual non-vacuity bar. Fault injection normally asks 'can this guard fail at all?' -- and this one could, provably, against the module it was written for. What it did not do was cover the rule it claimed to enforce. A guard can be non-vacuous and still incomplete, and the two failures look identical from a green test run.

Two practices follow. Plant the defect in the module *least* likely to be in scope rather than the one the guard was written against -- here that was a module five revisions of review had forgotten existed. And derive the forbidden-token set from generated data (the role catalog) rather than hand-listing it, so extending the system extends the guard without anyone remembering to.

A third correction came from the same run: matching identifiers as substrings produced a false positive, because a gate id contained a role id as a prefix and was unrelated. A guard that cries wolf teaches its next reader to loosen it rather than fix the code, so kebab-case identifiers must match as whole tokens.