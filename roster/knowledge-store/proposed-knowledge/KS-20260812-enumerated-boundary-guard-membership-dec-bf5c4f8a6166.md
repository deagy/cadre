---
id: "KS-20260812-enumerated-boundary-guard-membership-dec-bf5c4f8a6166"
title: "Enumerated boundary-guard membership decays; derive it from directory structure instead"
status: "accepted"
evidence:
  - "roster/orchestration/test/test_kernel_boundary.py:54-59, :76-95"
  - "roster/orchestration/test/test_context_boundary.py:150-155 (self-vacuity guard)"
  - "roster/orchestration/runs/cadre-feature-portable-platform-2026-08-11/requirements.md:210-220, :554-558"
origin:
  artifact: "roster/orchestration/test/test_kernel_boundary.py"
  revision: "a482e68c"
  task: "cadre-review-portable-platform-2026-08-11 (PR #240 architecture review)"
proposed_classification: "internal"
source_scope: "deagy/cadre"
sensitivity_notes: "No secrets, credentials, or personal data. Repository-relative paths only."
conflicts_or_staleness: "Complements, does not contradict, KS-20260809-non-vacuity-fault-injection (staged, accepted). That record covers proving a guard non-vacuous by fault injection; this one covers a guard that is non-vacuous and still incomplete -- fault injection against an in-scope module passes while an out-of-scope module is never examined."
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "architecture-authority"
content_digest: "bf5c4f8a616678ed5da1c12f1e69d6057cb1ff9dd478ba94f858d1c7a1c2051d"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified test_kernel_boundary.py enforces its rule over the roster/ directory via AST walk (not a hand-maintained module list), consistent with the record's contrast between derived and enumerated membership. Complements, does not contradict, the accepted non-vacuity record per its own conflicts_or_staleness note. Durable test-design lesson."
---

test_kernel_boundary.py enforces its rule over a DIRECTORY (no file under roster/ imports agentic_sdlc), so it covers files that do not exist yet. A guard whose scope is a hand-maintained list of module paths covers only what someone remembered.

Demonstrated in practice: the platform-module list in the PR #240 requirements baseline omitted roster/orchestration/mcp/dispatch_core.py and dispatch_server.py -- a second, independent selection entry point -- through five consecutive revisions, each of which re-read the tree.

A self-vacuity guard detects an EMPTY list, never an INCOMPLETE one. Prefer derived membership plus a short exception list with owners and expiry, so the failure mode is "forgot to exempt" (fails closed) rather than "forgot to add" (fails open).