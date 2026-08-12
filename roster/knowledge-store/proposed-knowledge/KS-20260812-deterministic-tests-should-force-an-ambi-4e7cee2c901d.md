---
id: "KS-20260812-deterministic-tests-should-force-an-ambi-4e7cee2c901d"
title: "Deterministic tests should force an ambient dependency explicitly, not skipUnless on it"
status: "accepted"
evidence:
  - "roster/orchestration/test/test_selection_golden_corpus.py:135"
  - "roster/orchestration/test/test_selector.py:41, :964, :1104-1114"
  - "roster/orchestration/src/agentic_sdlc_contracts.py (_resolve_executable)"
  - "bin/agentic-sdlc (in-tree wrapper, no install needed)"
  - ".github/workflows/validate.yml:46"
origin:
  artifact: "roster/orchestration/test/test_selector.py"
  revision: "a482e68c"
  task: "cadre-review-portable-platform-2026-08-11 (PR #240 test-strategy review)"
proposed_classification: "internal"
source_scope: "deagy/cadre"
sensitivity_notes: "Describes a test-authoring pattern; no secrets and no paths outside the repository."
conflicts_or_staleness: "None known."
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "test-engineer"
content_digest: "4e7cee2c901d09bfbaf8a94dbb8dec0c3a7e8dc5ad0156e2d2d0d9c6ca97d761"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified test_selector.py uses AGENTIC_SDLC_AVAILABLE plus skipUnless (silent skip) at multiple call sites, contrasted with test_selection_golden_corpus.py's deterministic mock.patch forcing try_lifecycle_contract to None. Accurate, durable test-authoring guidance with no coupling to unshipped features."
---

roster/orchestration/test/test_selection_golden_corpus.py forces try_lifecycle_contract() to None regardless of host, making the corpus deterministic. The inverse case -- a test that needs the lifecycle kernel PRESENT -- has an existing but weaker pattern in test_selector.py: AGENTIC_SDLC_AVAILABLE plus @unittest.skipUnless, which silently SKIPS rather than deterministically forcing availability.

The repository already ships the stronger mechanism: an in-tree bin/agentic-sdlc wrapper needing no install, and CI already sets AGENTIC_SDLC_BIN at job level. No individual test uses it to force rather than merely gate on availability, so a bare checkout silently skips the assertions instead of running them.

Lesson: a skipUnless on ambient availability converts a test into a no-op on exactly the hosts least likely to have the dependency. Where an in-tree equivalent exists, patch the environment to point at it inside the test.