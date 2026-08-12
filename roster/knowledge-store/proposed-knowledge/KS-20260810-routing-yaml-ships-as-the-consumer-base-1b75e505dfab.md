---
id: "KS-20260810-routing-yaml-ships-as-the-consumer-base-1b75e505dfab"
title: "routing.yaml ships as the consumer base ruleset, so a generic path glob in a route has downstream blast radius"
status: "accepted"
evidence:
  - "roster/orchestration/src/routing_overlay.py - consumers widen a base route, never narrow it"
  - "roster/orchestration/routing.yaml is copied verbatim to plugin/suite/roster/orchestration/routing.yaml by cadre generate-plugin"
  - "PR #195 review: root pyproject.toml added then removed; packaging/** kept then removed a revision later"
  - "deagy/cadre#196 records the resulting unrouted-file gap and why the obvious fix is blocked"
origin:
  artifact: "roster/orchestration/routing.yaml"
  revision: "d0dd3a5"
  task: "deagy/cadre#189 / PR #195 / #196"
proposed_classification: "internal"
source_scope: "deagy/cadre"
sensitivity_notes: "No secrets, credentials, or personal data. Repository-relative paths only."
conflicts_or_staleness: "Point-in-time against d0dd3a5. Holds only while routing.yaml is shipped in the plugin as a consumer base ruleset and routing_overlay.py remains widen-only; re-verify both if the distribution model changes."
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "orchestrator (run-agent-orchestration, issue-189-routing-plugin-tools-2026-08-09)"
content_digest: "1b75e505dfab93ac1aba3dba0df8392aa22f1eb7d0c47f5fd65838f22be4f798"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified routing_overlay.py: a project-local overlay may only widen (add), never narrow, a base route's keywords/paths -- confirming the record's claim that routing.yaml is a shipped consumer base ruleset with real downstream blast radius. Directly useful guidance for future edits to routing.yaml paths."
---

`roster/orchestration/routing.yaml` is not only this repository's selector config. `cadre generate-plugin` copies it verbatim to `plugin/suite/roster/orchestration/routing.yaml`, which ships as the **base ruleset** every consuming project's `cadre select` uses. `roster/orchestration/src/routing_overlay.py` lets a consumer only *widen* a base route, never narrow it.

A route's `paths` entry is therefore a claim made on every downstream repository, not just this one. Adding a filename or directory name that is generic across projects routes unrelated consumer content to agents chosen for this repository's concerns.

This surfaced twice in PR #195, both times in review rather than authoring. Root `pyproject.toml` was added to a new `packaging` route and removed once review pointed out that essentially every Python project has that file, and consumers would inherit a route whose keywords are about this repository's plugin manifests and Cline ports. `packaging/**` was then kept on the reasoning that `agent-suite-governance` already claimed it, and was removed a revision later on the counter-argument that a pre-existing imprecise glob is a reason to scope it, not a licence to copy the imprecision into a second route and double the blast radius.

The generalisable test when adding a path to a route: would this glob match content in an arbitrary downstream project that has nothing to do with the route's purpose? Compound repository-specific paths (`plugin/tools/**`) pass; bare conventional filenames and top-level directory names (`pyproject.toml`, `packaging/**`) do not.

This is also why the unrouted-`pyproject.toml` gap (deagy/cadre#196) is harder than it looks: the obvious fix is the thing review rejected twice.