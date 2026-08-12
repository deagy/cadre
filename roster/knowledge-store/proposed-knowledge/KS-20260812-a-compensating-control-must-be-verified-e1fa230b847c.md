---
id: "KS-20260812-a-compensating-control-must-be-verified-e1fa230b847c"
title: "A compensating control must be verified at every entry point sharing the mechanism it compensates for"
status: "accepted"
evidence:
  - "roster/orchestration/mcp/dispatch_server.py:48 (disable_project_tier_cwd_fallback), :63 (import-time load_routing)"
  - "roster/orchestration/mcp/dispatch_core.py:56"
  - "roster/orchestration/runs/cadre-feature-portable-platform-2026-08-11/product-intent.md (OD-2 disposition)"
  - "roster/orchestration/runs/cadre-feature-portable-platform-2026-08-11/requirements.md (PP-FR-1b)"
origin:
  artifact: "roster/orchestration/mcp/dispatch_server.py"
  revision: "a482e68c"
  task: "cadre-review-portable-platform-2026-08-11 (PR #240 delivery sequencing)"
proposed_classification: "internal"
source_scope: "deagy/cadre"
sensitivity_notes: "Describes a design/process lesson about an unshipped feature. No secrets or credentials."
conflicts_or_staleness: "Bears on OD-2 in the PR #240 records, currently marked RESOLVED. If OD-2 narrows to global-only, the specific gap dissolves but the lesson stands."
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "delivery-sequencer"
content_digest: "e1fa230b847c0654de36582b7eb8ff08bda7983b8ceb3847c8c3f8f0017a0dd2"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified against product-intent.md: OD-2 was reversed to global-only precisely because two reviewers found the compensating control (roster identity in the plan) verified against cadre select only, silently absent on the mcp-dispatch-server surface -- exactly this record's finding. Documents the reasoning behind the now-current approved decision, not a conflict with it. Durable lesson about enumerating every entry point sharing a risky mechanism."
---

OD-2 accepted a project-local roster redirect on the stated condition that the resolved roster's id and digest surface in the dispatch plan, making a silent redirect visible. That control was designed and verified against `cadre select` only.

`cadre mcp-dispatch-server` is a second, independently-resolving dispatch entry point (roster/orchestration/mcp/dispatch_core.py, mcp/dispatch_server.py) that emits no dispatch plan at all, and that the same project-local setting cannot currently drive -- dispatch_server.py deliberately calls settings.disable_project_tier_cwd_fallback() and loads routing at import time, before any call knows which project it concerns.

So the accepted compensating control is true for one surface and silently false for the other. It was found only by cross-referencing two independent reviews of the same requirement.

Lesson: when a control is accepted as the condition for permitting a risky mechanism, enumerate every entry point that shares the mechanism and verify the control at each. A control verified at the primary surface is not a control on the feature.