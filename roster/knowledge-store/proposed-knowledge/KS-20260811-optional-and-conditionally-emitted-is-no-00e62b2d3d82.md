---
id: "KS-20260811-optional-and-conditionally-emitted-is-no-00e62b2d3d82"
title: "Optional-and-conditionally-emitted is not the same as additive on a closed, vendored schema"
status: "accepted"
evidence:
  - "roster/RUNBOOK.md:118 -- the governing rule: any change to the emitted field set increments schema_version, because selection.schema.json is closed (additionalProperties: false) and vendored away from the producer."
  - "Empirical, ISSUE-214: a plan carrying undeclared_workflow_shape_routes validates against HEAD's schema and is rejected by b1c3d9c's pinned v5 copy with 'Additional properties are not allowed', on a document truthfully reporting schema_version: 5."
  - "roster/orchestration/selection.schema.json -- contrasts provenance (partial exemplar) with undeclared_workflow_shape_routes (bumped to 6)."
  - "CHANGELOG.md:176 -- records the same reasoning error occurring once before, on dispatch_disposition."
  - "pyproject.toml:131,133 -- the schema is force-included into the pip wheel from source, which is why a consumer's copy is pinned at their installed release."
origin:
  artifact: "roster/orchestration/selection.schema.json"
  revision: "b1f7bc1e"
  task: "ISSUE-214-overlay-shape-signal"
proposed_classification: "internal"
source_scope: "deagy/cadre"
sensitivity_notes: "None. Contains no secrets, credentials, personal data, or customer data; derived entirely from repository files."
conflicts_or_staleness: "Supersedes the reading that provenance is a general precedent for shipping a new plan property without a schema_version bump. provenance is at most a partial example -- it is emitted by default on an ordinary cadre select run, so it does not satisfy the carve-out it is cited for. The exception is now stated by purpose ('optional and that the consumer in question never receives') rather than by declaration shape."
recommended_action: "ingest"
untrusted_instruction_risk: false
staged_by: "orchestrator:run-agent-orchestration"
content_digest: "00e62b2d3d821eb5998f6e54d0805190079dc0901c94329842278c4387c12afb"
disposition:
  action: "accepted"
  classification_used: "internal"
  decided_by: "knowledge-store-steward"
  diverged_from_proposal: false
  reason: "Verified against current RUNBOOK.md ('When schema_version increments' section) and selection.schema.json (schema_version const: 6, matching the record's undeclared_workflow_shape_routes 5->6 bump claim). The lesson is now also enforced by an automated check (#224, test_schema_release_drift.py) -- this record predates and matches that formalization rather than conflicting with it. Accurate and durable."
---

When adding a property to `roster/orchestration/selection.schema.json`, do not reason from how `provenance` is declared. `provenance` is optional and absent from the top-level `required` array, and that shape is easy to copy while missing the condition that makes its carve-out valid: the exception applies only to a property the consumer in question never actually receives.

The schema is closed (`additionalProperties: false`) and vendored away from the producer, into the pip wheel and the plugin distribution. A consumer therefore validates freshly generated plans against a schema copy pinned at whatever release they installed. On such a schema there is no purely additive field: a pinned older copy rejects a document carrying a property it has never seen. If the version did not change, the plan reports the exact `schema_version` that copy claims to handle, so the consumer gets an `additionalProperties` error naming the wrong cause instead of a `const` failure naming the real one. The bump produces the strictly better failure.

The test to apply is existential over consumers, not universal over code paths: a field emitted to any real consumer population bumps the version, however conditional its emission looks in the code. A field behind `--verbose` bumps; a field reachable only from an in-process fixture does not.

This reasoning error has now occurred three times in this repository -- on `dispatch_disposition`, on `undeclared_workflow_shape_routes`, and again when an independent reviewer reached for the same analogy before checking. Fingerprint churn is not a reason to hesitate; it happens either way and the bump adds none.