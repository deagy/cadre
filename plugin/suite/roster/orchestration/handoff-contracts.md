<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Handoff Contracts

Every handoff includes:

- Task identifier, scope, owner, and target environment.
- Exact source revision and immutable artifact identifiers.
- Inputs examined and outputs produced.
- Assumptions, exclusions, and unresolved questions.
- Structured findings with evidence and severity.
- Knowledge retrieval status, query identifiers, citations used, and stale/conflicting material.
- `knowledge_steward_handoffs`: a list of durable decisions, findings, lessons,
  root causes, reusable patterns, stale guidance, or other store-worthy
  candidates discovered during the task, field list and redaction rule per
  `roster/shared/knowledge-use-policy.md`; an empty list means none. This is a
  proposal only, not approval to ingest or mutate the knowledge store.
- Required approvals and their status.
- Recommended next agent and explicit acceptance criteria.
- Intent record and requirements-baseline identifiers when supplied by the
  target project's lifecycle record.
- Trace links from requirements to architecture, controls, implementation,
  tests, findings, and evidence using the target project's lifecycle contract.
- platform impact-profile reference when applicable, including every `unknown`
  applicability or undefined-semantics blocker.
- For any `team_recipes` dispatch (`roster/orchestration/routing.yaml`): the
  `communication_mode` that actually executed — `peer` or its
  `orchestrator-relayed` fallback — per team, stated explicitly rather than
  assumed from the recipe's declared default. This applies regardless of
  which runner hosted the dispatch; see
  `.agents/skills/run-agent-orchestration/references/runner-adapters.md` for
  what determines the actual mode on each runner.
- For black-box, UAT, or support cases: user-visible steps, expected and actual behavior, affected persona or reporter class, client/browser version, timestamps, request IDs, sanitized attachments, workaround status, and user-safe communication draft when applicable.

A listed field may be omitted, or stated as a one-line "not applicable", when
it does not materially apply to the task. Every field that does materially
apply remains mandatory in full — see `roster/shared/documentation-style.md`
for the proportionality principle this follows; it never excuses dropping an
audit-trail, citation, evidence-integrity, approval-status, or
assumption/unresolved-question field once that field is applicable.

The receiving agent verifies completeness and rejects an ambiguous or unauditable handoff. A rejected handoff returns to its author without being treated as approval.

Material changes must be reported to the target project's lifecycle kernel for
impact analysis and any required gate invalidation. A receiving reviewer who
makes a material correction becomes an author and cannot approve that revision;
another independent reviewer must decide it.
