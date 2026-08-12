---
id: "KS-20260721-compose-lessons"
title: "Compose Runtime Lessons"
status: "rejected"
evidence:
  - "local compose troubleshooting experience"
  - "technology-standards.md reference to PostgreSQL and Podman Compose"
origin:
  artifact: "operational experience"
  revision: "2026-07-21"
  task: "local compose troubleshooting"
proposed_classification: "internal"
source_scope: "operations, testing, documentation"
sensitivity_notes: ""
conflicts_or_staleness: ""
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "knowledge-store-steward"
content_digest: "3fd21ac8a4a16c8d5bbdaa8da2c92720f70e172fc2a85a922b0f0dd17d3e977f"
disposition:
  action: "rejected"
  classification_used: "internal"
  decided_by: "daniel.eagy@gmail.com"
  diverged_from_proposal: false
  reason: "Rejected as redundant, not as wrong. This content is already retrievable: it is a 98% match to the chunk ingested under source deagy/cadre on 2026-08-01. Accepting it would place a near-identical copy under proposed-knowledge, and dispatch plans now query both sources, so every matching retrieval would return the same compose lesson twice and spend two of an agent's five result slots on it. Reject at the staged stage is the cheap correction; delete-ingested afterwards would require a named authorized human. Human decision; the staging steward could not decide it."
---

## Summary

Local startup troubleshooting exposed three reusable lessons for agents working on disposable local container-orchestration stacks in this provider profile, using Docker/Podman Compose here as the concrete example:

- Compose project resources can survive failed runs. A stale project network without the expected `com.docker.compose.network` label can cause Compose to refuse reuse. Cleanup must target only project-labeled disposable containers/networks.
- PostgreSQL 18 Docker images expect the persistent mount at `/var/lib/postgresql`, not `/var/lib/postgresql/data`. Old local volumes with the prior layout should be removed only when confirmed disposable.
- Docker Desktop and rootless Podman named volumes may reject `chown`/`chmod` on mounted volume roots. Local demo stacks should avoid assuming ownership changes on volume roots, document any local-only relaxed-permission flag, and keep production-shaped images and Helm contracts non-root.
- Vite 8 development servers using bundled TypeScript config loading may try to write `.vite-temp` under `node_modules`. In read-only local containers, use a config-loader mode and cache directory that write to tmpfs, such as `--configLoader runner` with `VITE_CACHE_DIR=/tmp/vite-cache`.

## Recommended Retrieval Use

Retrieve this note for backend, infrastructure, test, code-review, and documentation agents when work touches local Compose-based workflows, PostgreSQL container storage, named volumes, or comparable local runtime troubleshooting in the current stack.

## Steward Notes

Do not ingest until the steward verifies scope, classification, and whether this should be represented as operational knowledge, a decision overlay, or both.
