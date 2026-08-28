# Agent Review Response

## Decision

`approve | request-changes | needs-information | blocked`

## Reviewed scope

- Task identifier:
- Agent role:
- Source revision:
- Artifact or plan identifiers:
- Target environment:
- Inputs examined:
- Exclusions:
- Team communication mode (only when dispatched as part of a `team_recipes` entry from `roster/orchestration/routing.json`): state which `communication_mode` actually executed for that team — `peer` or its `orchestrator-relayed` fallback (see `.agents/skills/run-agent-orchestration/references/runner-adapters.md`). Leave blank/`not-applicable` for an ordinary non-team dispatch.

## Findings

Use `../shared/output-schemas/finding.schema.json` for each finding. Include severity, evidence, affected assets, remediation, owner, and control mappings. `owner` is null only for a coverage finding, where the point is that no role's remit covered the defect and the roster owns it; every other finding names an owner.

## Evidence

- Tests and scans:
- Plans and diffs:
- Architecture and threat-model references:
- Other snapshot/run evidence and integrity identifiers:
- Knowledge-store citations used:
- Conflicting or stale retrieved knowledge:

## Residual risk and assumptions

- Residual risk:
- Assumptions:
- Unresolved questions:

## Handoff

- Required next action:
- Receiving agent or approver:
- Completion criteria:
