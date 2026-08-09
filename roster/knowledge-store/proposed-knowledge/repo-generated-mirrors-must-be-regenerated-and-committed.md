---
id: KS-20260809-plugin-regeneration
title: any tracked source change may require regenerating `plugin/`
status: proposed
evidence:
  - "PR #163 (7aef327)"
  - "PR #166"
  - ".github/workflows/validate.yml generated-content job"
  - "cadre generate-plugin"
origin:
  task: PR #163 and PR #166
  artifact: cadre generate-plugin workflow and plugin distribution
  revision: "7aef327 and agent/durable-knowledge-capture-proposal"
proposed_classification: internal
source_scope: build, documentation, release
sensitivity_notes: ""
conflicts_or_staleness: "repository-specific; goes stale if the generated half of plugin/ stops being committed"
recommended_action: ingest
untrusted_instruction_risk: false
staged_by: knowledge-store-steward
content_digest: 51eb18c2a34e21104d2fee3b5585169f8ced3cf2cd5c00ec62579e7f6e35bd14
---

## Summary

The generated half of `plugin/` is committed deliberately, and
`.github/workflows/validate.yml`'s `generated-content` job re-runs
`cadre generate-plugin --check` so drift cannot outlive a pull request. Two
distinct mistakes around this cost time in consecutive pull requests:

1. **A new source file was never mirrored.** A new module under
   `roster/orchestration/src/` was added without regenerating, and CI caught it.
   The reason it was missed is the important part: the generator had been run
   with `>/dev/null 2>&1`, which hid its failure. Suppressing a tool's output
   suppresses its diagnosis.
2. **A documentation-only change also makes the mirror stale.** `cadre
   generate-plugin` copies `docs/` into `plugin/suite/docs/`. Adding a single
   file under `docs/proposals/` is enough to fail `--check`, which is not
   obvious from the phrase "generated content".

The reliable procedure is to run `cadre generate-plugin --output plugin --check`
before pushing *any* tracked change, not only code changes, and to read its
output rather than discard it. When it reports staleness, confirm the drift is
yours — stash the change and re-run — before regenerating, so a pre-existing
drift is not silently absorbed into an unrelated commit.

Related: `cadre generate-role-metadata --check` is the parallel guard for
register-side derived files, and must be run after editing any `AGENT.md`,
`roster/catalog.yaml`, or `roster/catalog-order.txt`. Editing
`roster/authority/aides.yaml` or its template requires
`cadre generate-authority-aides` *first*.

## Not mirrored

`roster/knowledge-store/proposed-knowledge/` is not copied into `plugin/suite/`
(verified 2026-08-09), and `pyproject.toml` excludes it from the wheel as a
"dev-only/generated-at-runtime" path. Records added there therefore do not
require regeneration — the one confirmed exception in this area.

## Recommended Retrieval Use

Retrieve for any agent making a tracked change in this repository, particularly
build, documentation, and release agents. Repository-specific; not applicable to
consumer projects.

## Steward Notes

Do not ingest until the steward verifies scope and classification. This record
is operational rather than conceptual and is tightly coupled to current
packaging arrangements — consider a review trigger tied to
`generate_global_plugin.py` or `pyproject.toml`.
