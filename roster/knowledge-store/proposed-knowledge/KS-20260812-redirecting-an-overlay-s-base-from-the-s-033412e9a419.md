---
id: "KS-20260812-redirecting-an-overlay-s-base-from-the-s-033412e9a419"
title: "Redirecting an overlay's base from the same trust tier defeats the overlay's narrowing guarantees"
status: "accepted"
evidence:
  - "roster/orchestration/src/routing_overlay.py:56-63"
  - "roster/orchestration/src/select_agents.py:203-204, :212"
  - "roster/shared/src/settings.py:533-540 (one project-tier field in the whole registry), :681-696"
  - "roster/orchestration/test/test_kernel_boundary.py:129-140"
origin:
  artifact: "roster/orchestration/src/routing_overlay.py"
  revision: "a482e68c"
  task: "cadre-review-portable-platform-2026-08-11 (PR #240 architecture review)"
proposed_classification: "internal"
source_scope: "deagy/cadre"
sensitivity_notes: "Describes a hypothetical weakening of an unshipped design, not an exploitable defect in shipped code -- roster.root does not exist today and every path setting is currently global-only. No secrets or personal data."
conflicts_or_staleness: "Bears directly on OD-2 in the PR #240 records, which is currently marked RESOLVED on the opposite reading. If OD-2 is reaffirmed unchanged, this record should be retained as the recorded counter-argument rather than deleted."
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "architecture-authority"
content_digest: "033412e9a41998e82fe9f075783e395c1d8a22c8bde33a22fa3c2503abaaaaee"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified against roster/orchestration/runs/cadre-feature-portable-platform-2026-08-11/product-intent.md: OD-2 was REVERSED to global-only at Revision 6, precisely on the grounds this record and its sibling (compensating-control record) raised, and G2-approved 2026-08-11. This record documents the reasoning behind the now-current approved decision rather than conflicting with it -- no escalation trigger fires. Sound general control-design lesson about layered-config trust boundaries, applicable beyond routing."
---

routing_overlay.py permits a project-local .agents/orchestration/routing-overlay.json under fail-closed, narrowing-only rules with human_gate/reviewers/primary/support/quality_gates immutable. Those guarantees are all relative to a BASE FILE.

If the same project-local trust tier can also choose WHICH base file is merged, the guarantees evaporate: a checkout that cannot remove a human_gate from a route can instead supply a base that never had one, and the overlay validator confirms nothing was narrowed.

Generalizable control-design lesson: a narrowing-only merge is only as strong as the trust boundary around its base, and citing such a mechanism as precedent for a total-replacement mechanism inverts it. Applies to any layered-config design -- policy overlays, profile inheritance, provider bundles -- not only routing.