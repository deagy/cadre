# Capability index

This page lists all 159 roles from [`roster/catalog.yaml`](../roster/catalog.yaml)
grouped by their `capability` and `phase` fields, so you can find every role
in a given class of change authority (for example, every role that can only
review, or every role that can operate a live environment) or every role
active in a given lifecycle stage. `roster/catalog.yaml` remains the
authoritative source; this is a generated-by-hand snapshot of it, not a live
filter — regenerate it after any `catalog.yaml` change (see "Keeping this
page in sync" below).

For a purpose-oriented view grouped by subject-matter domain instead, see the
[role index](role-index.md). See the [glossary](terminology.md) for the
"capability tier" definition and other recurring terms.

## By capability

`capability` is each role's `roster/catalog.yaml` field. It describes the
class of change authority a role has, not its subject-matter domain --
see each role's own `AGENT.md` "Authority" section for its exact scope.

### `read_only` (28 roles)

Reads and evaluates only; produces findings, decision packages, or approvals but does not edit the artifact it assesses.

| Role | Phase | Definition |
| --- | --- | --- |
| accessibility-reviewer | review | [AGENT.md](../roster/review/accessibility-reviewer/AGENT.md) |
| agent-performance-evaluator | operations | [AGENT.md](../roster/operations/agent-performance-evaluator/AGENT.md) |
| approval-router | review | [AGENT.md](../roster/governance/approval-router/AGENT.md) |
| architecture-authority | review | [AGENT.md](../roster/review/architecture-authority/AGENT.md) |
| claim-conformance | release | [AGENT.md](../roster/review/claim-conformance/AGENT.md) |
| classification-and-marking-gate | release | [AGENT.md](../roster/review/classification-and-marking-gate/AGENT.md) |
| code-reviewer | review | [AGENT.md](../roster/review/code-reviewer/AGENT.md) |
| compliance-reviewer | review | [AGENT.md](../roster/review/compliance-reviewer/AGENT.md) |
| deployment-realist | operations | [AGENT.md](../roster/operations/deployment-realist/AGENT.md) |
| doctrine-conformance | review | [AGENT.md](../roster/review/doctrine-conformance/AGENT.md) |
| engineering-lead-aide | authority | [AGENT.md](../roster/authority/engineering-lead-aide/AGENT.md) |
| falsification-agent | verify | [AGENT.md](../roster/testing/falsification-agent/AGENT.md) |
| first-principles-challenger | design | [AGENT.md](../roster/architecture/first-principles-challenger/AGENT.md) |
| governance-lead-aide | authority | [AGENT.md](../roster/authority/governance-lead-aide/AGENT.md) |
| halt-authority | review | [AGENT.md](../roster/review/halt-authority/AGENT.md) |
| infrastructure-reviewer | review | [AGENT.md](../roster/review/infrastructure-reviewer/AGENT.md) |
| phase-gate | release | [AGENT.md](../roster/review/phase-gate/AGENT.md) |
| pipeline-security-reviewer | review | [AGENT.md](../roster/review/pipeline-security-reviewer/AGENT.md) |
| product-owner-aide | authority | [AGENT.md](../roster/authority/product-owner-aide/AGENT.md) |
| release-authority-aide | authority | [AGENT.md](../roster/authority/release-authority-aide/AGENT.md) |
| release-owner-aide | authority | [AGENT.md](../roster/authority/release-owner-aide/AGENT.md) |
| scope-boundary | planning | [AGENT.md](../roster/planning/scope-boundary/AGENT.md) |
| security-lead-aide | authority | [AGENT.md](../roster/authority/security-lead-aide/AGENT.md) |
| security-reviewer | review | [AGENT.md](../roster/review/security-reviewer/AGENT.md) |
| service-owner-aide | authority | [AGENT.md](../roster/authority/service-owner-aide/AGENT.md) |
| subtraction-agent | review | [AGENT.md](../roster/review/subtraction-agent/AGENT.md) |
| supply-chain-security-reviewer | review | [AGENT.md](../roster/review/supply-chain-security-reviewer/AGENT.md) |
| system-architect-aide | authority | [AGENT.md](../roster/authority/system-architect-aide/AGENT.md) |

### `document_author` (24 roles)

Creates or edits documents, plans, and requirements (not application code).

| Role | Phase | Definition |
| --- | --- | --- |
| agent-version-control | operations | [AGENT.md](../roster/operations/agent-version-control/AGENT.md) |
| architecture-diagram-author | document | [AGENT.md](../roster/documentation/architecture-diagram-author/AGENT.md) |
| api-contract-engineer | design | [AGENT.md](../roster/architecture/api-contract-engineer/AGENT.md) |
| assumption-register | planning | [AGENT.md](../roster/planning/assumption-register/AGENT.md) |
| cloud-architect | design | [AGENT.md](../roster/architecture/cloud-architect/AGENT.md) |
| cost-capacity-planner | planning | [AGENT.md](../roster/operations/cost-capacity-planner/AGENT.md) |
| cryptographic-assurance-engineer | security | [AGENT.md](../roster/security/cryptographic-assurance-engineer/AGENT.md) |
| data-governance-engineer | design | [AGENT.md](../roster/data/data-governance-engineer/AGENT.md) |
| decision-record | document | [AGENT.md](../roster/documentation/decision-record/AGENT.md) |
| escalation-manager | support | [AGENT.md](../roster/support/escalation-manager/AGENT.md) |
| evidence-curator | evidence | [AGENT.md](../roster/documentation/evidence-curator/AGENT.md) |
| governance-planner | design | [AGENT.md](../roster/governance/governance-planner/AGENT.md) |
| interaction-designer | design | [AGENT.md](../roster/architecture/interaction-designer/AGENT.md) |
| visual-designer | design | [AGENT.md](../roster/architecture/visual-designer/AGENT.md) |
| ip-provenance-agent | evidence | [AGENT.md](../roster/documentation/ip-provenance-agent/AGENT.md) |
| premortem | planning | [AGENT.md](../roster/planning/premortem/AGENT.md) |
| delivery-sequencer | planning | [AGENT.md](../roster/planning/delivery-sequencer/AGENT.md) |
| product-intent-agent | planning | [AGENT.md](../roster/planning/product-intent-agent/AGENT.md) |
| quantum-timing-assurance-engineer | security | [AGENT.md](../roster/security/quantum-timing-assurance-engineer/AGENT.md) |
| requirements-agent | planning | [AGENT.md](../roster/planning/requirements-agent/AGENT.md) |
| support-triage-agent | support | [AGENT.md](../roster/support/support-triage-agent/AGENT.md) |
| technical-writer | document | [AGENT.md](../roster/documentation/technical-writer/AGENT.md) |
| threat-modeler | design | [AGENT.md](../roster/architecture/threat-modeler/AGENT.md) |
| vendor-register-steward | operations | [AGENT.md](../roster/operations/vendor-register-steward/AGENT.md) |

### `code_author` (20 roles)

Creates or edits application, infrastructure, pipeline, or policy-as-code source.

| Role | Phase | Definition |
| --- | --- | --- |
| ai-engineer | build | [AGENT.md](../roster/engineering/ai-engineer/AGENT.md) |
| application-engineer | build | [AGENT.md](../roster/engineering/application-engineer/AGENT.md) |
| backend-engineer | build | [AGENT.md](../roster/engineering/backend-engineer/AGENT.md) |
| cicd-engineer | build | [AGENT.md](../roster/engineering/cicd-engineer/AGENT.md) |
| database-reliability-engineer | operations | [AGENT.md](../roster/data/database-reliability-engineer/AGENT.md) |
| debugging-engineer | build | [AGENT.md](../roster/engineering/debugging-engineer/AGENT.md) |
| frontend-engineer | build | [AGENT.md](../roster/engineering/frontend-engineer/AGENT.md) |
| github-actions-implementer | build | [AGENT.md](../roster/engineering/github-actions-implementer/AGENT.md) |
| gitlab-ci-implementer | build | [AGENT.md](../roster/engineering/gitlab-ci-implementer/AGENT.md) |
| go-service-implementer | build | [AGENT.md](../roster/engineering/go-service-implementer/AGENT.md) |
| helm-chart-implementer | build | [AGENT.md](../roster/engineering/helm-chart-implementer/AGENT.md) |
| infrastructure-provisioner | build | [AGENT.md](../roster/engineering/infrastructure-provisioner/AGENT.md) |
| kubernetes-manifest-implementer | build | [AGENT.md](../roster/engineering/kubernetes-manifest-implementer/AGENT.md) |
| node-typescript-implementer | build | [AGENT.md](../roster/engineering/node-typescript-implementer/AGENT.md) |
| opentofu-module-implementer | build | [AGENT.md](../roster/engineering/opentofu-module-implementer/AGENT.md) |
| policy-as-code-engineer | security | [AGENT.md](../roster/security/policy-as-code-engineer/AGENT.md) |
| postgres-query-implementer | build | [AGENT.md](../roster/engineering/postgres-query-implementer/AGENT.md) |
| python-automation-implementer | build | [AGENT.md](../roster/engineering/python-automation-implementer/AGENT.md) |
| react-component-implementer | build | [AGENT.md](../roster/engineering/react-component-implementer/AGENT.md) |
| secrets-identity-engineer | security | [AGENT.md](../roster/security/secrets-identity-engineer/AGENT.md) |

### `test_author` (6 roles)

Creates or edits test artifacts and executes them against authorized non-production environments.

| Role | Phase | Definition |
| --- | --- | --- |
| black-box-tester | verify | [AGENT.md](../roster/testing/black-box-tester/AGENT.md) |
| end-user-tester | verify | [AGENT.md](../roster/testing/end-user-tester/AGENT.md) |
| performance-testing-engineer | verify | [AGENT.md](../roster/testing/performance-testing-engineer/AGENT.md) |
| red-team | verify | [AGENT.md](../roster/testing/red-team/AGENT.md) |
| selector-test-implementer | verify | [AGENT.md](../roster/testing/selector-test-implementer/AGENT.md) |
| test-engineer | verify | [AGENT.md](../roster/engineering/test-engineer/AGENT.md) |

### `environment_operator` (8 roles)

Operates authorized environments directly (observability, release, incident response, chaos, knowledge-store, cost/finops).

| Role | Phase | Definition |
| --- | --- | --- |
| chaos-resilience-engineer | verify | [AGENT.md](../roster/testing/chaos-resilience-engineer/AGENT.md) |
| decommission-engineer | operations | [AGENT.md](../roster/operations/decommission-engineer/AGENT.md) |
| finops-engineer | operations | [AGENT.md](../roster/operations/finops-engineer/AGENT.md) |
| incident-commander | support | [AGENT.md](../roster/support/incident-commander/AGENT.md) |
| knowledge-store-steward | knowledge | [AGENT.md](../roster/knowledge-store/AGENT.md) |
| observability-sre | operations | [AGENT.md](../roster/operations/observability-sre/AGENT.md) |
| release-engineer | release | [AGENT.md](../roster/engineering/release-engineer/AGENT.md) |
| retention-and-deletion-executor | operations | [AGENT.md](../roster/operations/retention-and-deletion-executor/AGENT.md) |

## By phase

`phase` is each role's `roster/catalog.yaml` field, used for lifecycle
sequencing. It does not always match the role's `AGENT.md` directory --
see the [role index](role-index.md) for the subject-matter-domain
grouping instead.

### `planning` (7 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| assumption-register | document_author | [AGENT.md](../roster/planning/assumption-register/AGENT.md) |
| cost-capacity-planner | document_author | [AGENT.md](../roster/operations/cost-capacity-planner/AGENT.md) |
| delivery-sequencer | document_author | [AGENT.md](../roster/planning/delivery-sequencer/AGENT.md) |
| premortem | document_author | [AGENT.md](../roster/planning/premortem/AGENT.md) |
| product-intent-agent | document_author | [AGENT.md](../roster/planning/product-intent-agent/AGENT.md) |
| requirements-agent | document_author | [AGENT.md](../roster/planning/requirements-agent/AGENT.md) |
| scope-boundary | read_only | [AGENT.md](../roster/planning/scope-boundary/AGENT.md) |

### `design` (8 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| api-contract-engineer | document_author | [AGENT.md](../roster/architecture/api-contract-engineer/AGENT.md) |
| cloud-architect | document_author | [AGENT.md](../roster/architecture/cloud-architect/AGENT.md) |
| data-governance-engineer | document_author | [AGENT.md](../roster/data/data-governance-engineer/AGENT.md) |
| first-principles-challenger | read_only | [AGENT.md](../roster/architecture/first-principles-challenger/AGENT.md) |
| governance-planner | document_author | [AGENT.md](../roster/governance/governance-planner/AGENT.md) |
| interaction-designer | document_author | [AGENT.md](../roster/architecture/interaction-designer/AGENT.md) |
| threat-modeler | document_author | [AGENT.md](../roster/architecture/threat-modeler/AGENT.md) |
| visual-designer | document_author | [AGENT.md](../roster/architecture/visual-designer/AGENT.md) |

### `security` (4 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| cryptographic-assurance-engineer | document_author | [AGENT.md](../roster/security/cryptographic-assurance-engineer/AGENT.md) |
| policy-as-code-engineer | code_author | [AGENT.md](../roster/security/policy-as-code-engineer/AGENT.md) |
| quantum-timing-assurance-engineer | document_author | [AGENT.md](../roster/security/quantum-timing-assurance-engineer/AGENT.md) |
| secrets-identity-engineer | code_author | [AGENT.md](../roster/security/secrets-identity-engineer/AGENT.md) |

### `build` (17 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| ai-engineer | code_author | [AGENT.md](../roster/engineering/ai-engineer/AGENT.md) |
| application-engineer | code_author | [AGENT.md](../roster/engineering/application-engineer/AGENT.md) |
| backend-engineer | code_author | [AGENT.md](../roster/engineering/backend-engineer/AGENT.md) |
| cicd-engineer | code_author | [AGENT.md](../roster/engineering/cicd-engineer/AGENT.md) |
| debugging-engineer | code_author | [AGENT.md](../roster/engineering/debugging-engineer/AGENT.md) |
| frontend-engineer | code_author | [AGENT.md](../roster/engineering/frontend-engineer/AGENT.md) |
| github-actions-implementer | code_author | [AGENT.md](../roster/engineering/github-actions-implementer/AGENT.md) |
| gitlab-ci-implementer | code_author | [AGENT.md](../roster/engineering/gitlab-ci-implementer/AGENT.md) |
| go-service-implementer | code_author | [AGENT.md](../roster/engineering/go-service-implementer/AGENT.md) |
| helm-chart-implementer | code_author | [AGENT.md](../roster/engineering/helm-chart-implementer/AGENT.md) |
| infrastructure-provisioner | code_author | [AGENT.md](../roster/engineering/infrastructure-provisioner/AGENT.md) |
| kubernetes-manifest-implementer | code_author | [AGENT.md](../roster/engineering/kubernetes-manifest-implementer/AGENT.md) |
| node-typescript-implementer | code_author | [AGENT.md](../roster/engineering/node-typescript-implementer/AGENT.md) |
| opentofu-module-implementer | code_author | [AGENT.md](../roster/engineering/opentofu-module-implementer/AGENT.md) |
| postgres-query-implementer | code_author | [AGENT.md](../roster/engineering/postgres-query-implementer/AGENT.md) |
| python-automation-implementer | code_author | [AGENT.md](../roster/engineering/python-automation-implementer/AGENT.md) |
| react-component-implementer | code_author | [AGENT.md](../roster/engineering/react-component-implementer/AGENT.md) |

### `verify` (8 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| black-box-tester | test_author | [AGENT.md](../roster/testing/black-box-tester/AGENT.md) |
| chaos-resilience-engineer | environment_operator | [AGENT.md](../roster/testing/chaos-resilience-engineer/AGENT.md) |
| end-user-tester | test_author | [AGENT.md](../roster/testing/end-user-tester/AGENT.md) |
| falsification-agent | read_only | [AGENT.md](../roster/testing/falsification-agent/AGENT.md) |
| performance-testing-engineer | test_author | [AGENT.md](../roster/testing/performance-testing-engineer/AGENT.md) |
| red-team | test_author | [AGENT.md](../roster/testing/red-team/AGENT.md) |
| selector-test-implementer | test_author | [AGENT.md](../roster/testing/selector-test-implementer/AGENT.md) |
| test-engineer | test_author | [AGENT.md](../roster/engineering/test-engineer/AGENT.md) |

### `review` (12 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| accessibility-reviewer | read_only | [AGENT.md](../roster/review/accessibility-reviewer/AGENT.md) |
| approval-router | read_only | [AGENT.md](../roster/governance/approval-router/AGENT.md) |
| architecture-authority | read_only | [AGENT.md](../roster/review/architecture-authority/AGENT.md) |
| code-reviewer | read_only | [AGENT.md](../roster/review/code-reviewer/AGENT.md) |
| compliance-reviewer | read_only | [AGENT.md](../roster/review/compliance-reviewer/AGENT.md) |
| doctrine-conformance | read_only | [AGENT.md](../roster/review/doctrine-conformance/AGENT.md) |
| halt-authority | read_only | [AGENT.md](../roster/review/halt-authority/AGENT.md) |
| infrastructure-reviewer | read_only | [AGENT.md](../roster/review/infrastructure-reviewer/AGENT.md) |
| pipeline-security-reviewer | read_only | [AGENT.md](../roster/review/pipeline-security-reviewer/AGENT.md) |
| security-reviewer | read_only | [AGENT.md](../roster/review/security-reviewer/AGENT.md) |
| subtraction-agent | read_only | [AGENT.md](../roster/review/subtraction-agent/AGENT.md) |
| supply-chain-security-reviewer | read_only | [AGENT.md](../roster/review/supply-chain-security-reviewer/AGENT.md) |

### `release` (4 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| claim-conformance | read_only | [AGENT.md](../roster/review/claim-conformance/AGENT.md) |
| classification-and-marking-gate | read_only | [AGENT.md](../roster/review/classification-and-marking-gate/AGENT.md) |
| phase-gate | read_only | [AGENT.md](../roster/review/phase-gate/AGENT.md) |
| release-engineer | environment_operator | [AGENT.md](../roster/engineering/release-engineer/AGENT.md) |

### `support` (3 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| escalation-manager | document_author | [AGENT.md](../roster/support/escalation-manager/AGENT.md) |
| incident-commander | environment_operator | [AGENT.md](../roster/support/incident-commander/AGENT.md) |
| support-triage-agent | document_author | [AGENT.md](../roster/support/support-triage-agent/AGENT.md) |

### `operations` (9 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| agent-performance-evaluator | read_only | [AGENT.md](../roster/operations/agent-performance-evaluator/AGENT.md) |
| agent-version-control | document_author | [AGENT.md](../roster/operations/agent-version-control/AGENT.md) |
| database-reliability-engineer | code_author | [AGENT.md](../roster/data/database-reliability-engineer/AGENT.md) |
| decommission-engineer | environment_operator | [AGENT.md](../roster/operations/decommission-engineer/AGENT.md) |
| deployment-realist | read_only | [AGENT.md](../roster/operations/deployment-realist/AGENT.md) |
| finops-engineer | environment_operator | [AGENT.md](../roster/operations/finops-engineer/AGENT.md) |
| observability-sre | environment_operator | [AGENT.md](../roster/operations/observability-sre/AGENT.md) |
| retention-and-deletion-executor | environment_operator | [AGENT.md](../roster/operations/retention-and-deletion-executor/AGENT.md) |
| vendor-register-steward | document_author | [AGENT.md](../roster/operations/vendor-register-steward/AGENT.md) |

### `document` (3 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| decision-record | document_author | [AGENT.md](../roster/documentation/decision-record/AGENT.md) |
| architecture-diagram-author | document_author | [AGENT.md](../roster/documentation/architecture-diagram-author/AGENT.md) |
| technical-writer | document_author | [AGENT.md](../roster/documentation/technical-writer/AGENT.md) |

### `evidence` (2 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| evidence-curator | document_author | [AGENT.md](../roster/documentation/evidence-curator/AGENT.md) |
| ip-provenance-agent | document_author | [AGENT.md](../roster/documentation/ip-provenance-agent/AGENT.md) |

### `knowledge` (1 role)

| Role | Capability | Definition |
| --- | --- | --- |
| knowledge-store-steward | environment_operator | [AGENT.md](../roster/knowledge-store/AGENT.md) |

### `authority` (8 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| engineering-lead-aide | read_only | [AGENT.md](../roster/authority/engineering-lead-aide/AGENT.md) |
| governance-lead-aide | read_only | [AGENT.md](../roster/authority/governance-lead-aide/AGENT.md) |
| product-owner-aide | read_only | [AGENT.md](../roster/authority/product-owner-aide/AGENT.md) |
| release-authority-aide | read_only | [AGENT.md](../roster/authority/release-authority-aide/AGENT.md) |
| release-owner-aide | read_only | [AGENT.md](../roster/authority/release-owner-aide/AGENT.md) |
| security-lead-aide | read_only | [AGENT.md](../roster/authority/security-lead-aide/AGENT.md) |
| service-owner-aide | read_only | [AGENT.md](../roster/authority/service-owner-aide/AGENT.md) |
| system-architect-aide | read_only | [AGENT.md](../roster/authority/system-architect-aide/AGENT.md) |

## Keeping this page in sync

This page is a snapshot, not generated tooling output. After adding, removing,
or reclassifying a role in `roster/catalog.yaml` (its `capability` or `phase`
field, or the role set itself), update the corresponding table(s) above in
the same change. `python3 -m unittest agents.orchestration.test.test_repository_health`
checks catalog/plugin drift but does not check this page against the catalog;
treat divergence here as a documentation bug to fix by hand.
