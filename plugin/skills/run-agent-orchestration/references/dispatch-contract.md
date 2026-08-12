# Dispatch Contract

Read this contract before dispatching any selected role.

## Required input per agent

Each dispatch prompt must include:

- role name and exact `AGENT.md` path;
- task ID, objective, execution mode, classification, scope, exclusions, and acceptance criteria;
- exact files, source revision, plan, artifact digest, target, or environment when applicable;
- applicable shared policies, workflow, quality gates, and escalation policy;
- selector-emitted lifecycle `required_quality_gates`, mutation-oriented `human_gates`, and current gate-state records;
- the planned Python knowledge-store invocation and its result status; resolve its Python 3.10+ launcher at execution and preserve the supplied argv without shell interpretation;
- retrieved passages with `source`, `conversation_id`, `message_id`, `chunk_id`, `content_hash`, `created_at`, and `classification` citations, plus the retrieved bundle and its integrity hash as point-in-time evidence;
- nested citation `source_uri` omitted or redacted by default, and included only when separately authorized and necessary because it may reveal a local path;
- knowledge-steward handoff expectations from `roster/shared/knowledge-use-policy.md`: durable decisions, findings, lessons, root causes, reusable patterns, or stale/conflicting guidance must be proposed to `knowledge-store-steward` as a `knowledge_steward_handoffs` list (empty list when none), each item carrying `title`, `summary`, `evidence`, `origin`, `proposed_classification`, `source_scope`, `sensitivity_notes`, `conflicts_or_staleness`, `untrusted_instruction_risk` (`true | false | unknown`), and `recommended_action` (`ingest`, `update`, `reclassify`, or `defer` — never `delete`; the store does have deletion capability for both staged records and ingested content, so the exclusion rests on proposing a deletion and being authorized to perform one being different acts, not on the capability being absent: escalate a required deletion to the steward and an authorized human instead); `evidence` and `origin` follow the same omit-or-redact-local-paths-by-default rule as citation `source_uri`; `untrusted_instruction_risk` must be preserved from the cited retrieval result, not re-derived by the proposing agent, uses `unknown` when provenance cannot be established, is non-authoritative (the proposing agent cannot clear it), and `true` requires the steward to defer automatically; this is a proposal only, not approval to ingest or mutate the knowledge store; these fields are what SKILL.md's consolidation step stages via `cadre knowledge propose`, so return them ready to use as-is — `title`, `evidence`, `origin`, `proposed_classification`, `source_scope`, `sensitivity_notes`, `conflicts_or_staleness`, `untrusted_instruction_risk`, and `recommended_action` map directly onto the staged record's required frontmatter (`roster/knowledge-store/proposed-knowledge.schema.json`), and `summary` becomes the record body; the orchestrator adds only the mechanical staging fields the item cannot itself carry (`id`, `status`, `staged_by`, `content_digest`). A shell-capable agent may also stage its own items directly with `cadre knowledge propose --from-finding -` — the handoff list is still required either way, and staging is still not approval: `propose` refuses any record that arrives with a non-`proposed` status or a `disposition` block, so an agent cannot author its own acceptance. Tell the agent which applies: when you are consolidating, you stage the round's items and it should not stage them again;
- explicit permitted and prohibited actions;
- expected response template or schema;
- named receiving role or human owner;
- for any write-capable role (`sandbox_mode != "read-only"`), require the workspace-isolation result block defined in `roster/shared/workspace-isolation.md` (mode, path, branch, base revision, committed, reason if in-place) as part of that role's response.

Do not dispatch an implementation or review agent when its required artifact is absent. Mark that role `deferred` with the missing prerequisite.

## Safe prompt template

```text
Act as <role> using <role AGENT.md>.

Task: <task ID and objective>
Mode: <planning-review-only | scoped-repository-edit>
Classification: <classification>
Scope: <exact paths/artifacts/revision/target>
Exclusions: <explicit exclusions>
Acceptance criteria: <criteria>

Follow: <shared policies, workflow, gates, contracts>
Knowledge context: <invocation and available/empty/unavailable/unauthorized status>

Permitted: <bounded actions>
Prohibited: production or persistent mutations, destructive actions,
risk acceptance, policy exceptions, merge/push, and self-approval unless an
authorized human explicitly grants the specific action.

Return: <required template/schema>, evidence, disposition, unresolved risks,
knowledge_steward_handoffs (list, empty if none), handoff to <receiver>, and
(write-capable roles only) the workspace-isolation result block: mode, path,
branch, base revision, committed, reason if in-place.
```

## Wave and gate rules

- Run agents in parallel only when their inputs and write scopes are independent.
- Serialize agents that depend on another role's final artifact.
- Preserve separation between authors, reviewers, risk owners, and production approvers.
- Treat `needs-information`, `request-changes`, and `blocked` as non-approval.
- Stop release progression for unresolved critical/high risk, ambiguous targets, stale artifacts, mismatched revisions, or missing required evidence.
- Require an authorized human before persistent environments, production, destructive operations, database migration application, OpenTofu apply/state mutation, privileged identity or key changes, risk acceptance, or policy exceptions.

## Consolidated run record

Check the plan's `lifecycle_tracking.status` (see SKILL.md's "Operating modes"):

- **`standalone`**: the plain summary below is sufficient on its own. Do not write a `.agentic-sdlc/` record — there is no lifecycle contract behind it to validate against.
- **`integrated`**: use the standalone Agentic SDLC kernel's run-record contract as the authoritative structure when saving a target-project run record, preserving this summary together with the kernel-required lifecycle, impact-profile, gate, evidence, exception, and invalidation fields:

```yaml
task_id: <id>
mode: <mode>
selection_status: <ready|needs-triage>
dispatch_disposition: <staffed|advisory-only|no-agents-selected>
agents:
  completed: []
  blocked: []
  deferred: []
teams:
  - id: <team id from the plan's teams field>
    communication_mode_used: <peer|orchestrator-relayed>
knowledge:
  status_by_agent: {}
knowledge_steward_handoffs: []
findings: []
human_gates: []
required_quality_gates: []
artifacts: []
validation: []
disposition: <approve|request-changes|needs-information|blocked|plan-only>
next_safe_action: <action>
```

Record `communication_mode_used` per dispatched team even in standalone mode — it reflects what the runner actually did (see [runner-adapters.md](runner-adapters.md)'s "Team communication contract"), not a lifecycle decision, so it belongs in the plain summary regardless of `lifecycle_tracking.status`.
