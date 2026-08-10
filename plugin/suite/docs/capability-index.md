<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

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

### `document_author` (26 roles)

Creates or edits documents, plans, and requirements (not application code).

| Role | Phase | Definition |
| --- | --- | --- |
| adr-writer | document | [AGENT.md](../roster/documentation/adr-writer/AGENT.md) |
| agent-version-control | operations | [AGENT.md](../roster/operations/agent-version-control/AGENT.md) |
| api-contract-engineer | design | [AGENT.md](../roster/architecture/api-contract-engineer/AGENT.md) |
| architecture-diagram-author | document | [AGENT.md](../roster/documentation/architecture-diagram-author/AGENT.md) |
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
| technical-documentation-implementer | document | [AGENT.md](../roster/documentation/technical-documentation-implementer/AGENT.md) |
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

### `code_author` (80 roles)

Creates or edits application, infrastructure, pipeline, or policy-as-code source.

| Role | Phase | Definition |
| --- | --- | --- |
| agent-workflow-implementer | build | [AGENT.md](../roster/engineering/agent-workflow-implementer/AGENT.md) |
| ai-engineer | build | [AGENT.md](../roster/engineering/ai-engineer/AGENT.md) |
| ai-observability-implementer | build | [AGENT.md](../roster/engineering/ai-observability-implementer/AGENT.md) |
| ansible-automation-implementer | build | [AGENT.md](../roster/engineering/ansible-automation-implementer/AGENT.md) |
| application-engineer | build | [AGENT.md](../roster/engineering/application-engineer/AGENT.md) |
| backend-engineer | build | [AGENT.md](../roster/engineering/backend-engineer/AGENT.md) |
| bare-metal-provisioning-implementer | build | [AGENT.md](../roster/engineering/bare-metal-provisioning-implementer/AGENT.md) |
| bgp-routing-implementer | build | [AGENT.md](../roster/engineering/bgp-routing-implementer/AGENT.md) |
| c-systems-implementer | build | [AGENT.md](../roster/engineering/c-systems-implementer/AGENT.md) |
| cicd-engineer | build | [AGENT.md](../roster/engineering/cicd-engineer/AGENT.md) |
| cmake-build-implementer | build | [AGENT.md](../roster/engineering/cmake-build-implementer/AGENT.md) |
| compose-stack-implementer | build | [AGENT.md](../roster/engineering/compose-stack-implementer/AGENT.md) |
| cpp-systems-implementer | build | [AGENT.md](../roster/engineering/cpp-systems-implementer/AGENT.md) |
| css-layout-implementer | build | [AGENT.md](../roster/engineering/css-layout-implementer/AGENT.md) |
| data-transformation-implementer | build | [AGENT.md](../roster/engineering/data-transformation-implementer/AGENT.md) |
| database-reliability-engineer | operations | [AGENT.md](../roster/data/database-reliability-engineer/AGENT.md) |
| debugging-engineer | build | [AGENT.md](../roster/engineering/debugging-engineer/AGENT.md) |
| dependency-remediation-implementer | build | [AGENT.md](../roster/engineering/dependency-remediation-implementer/AGENT.md) |
| device-driver-implementer | build | [AGENT.md](../roster/engineering/device-driver-implementer/AGENT.md) |
| distributed-storage-implementer | build | [AGENT.md](../roster/engineering/distributed-storage-implementer/AGENT.md) |
| ebpf-implementer | build | [AGENT.md](../roster/engineering/ebpf-implementer/AGENT.md) |
| edge-cloud-integration-implementer | build | [AGENT.md](../roster/engineering/edge-cloud-integration-implementer/AGENT.md) |
| embedded-c-implementer | build | [AGENT.md](../roster/engineering/embedded-c-implementer/AGENT.md) |
| embedded-linux-platform-implementer | build | [AGENT.md](../roster/engineering/embedded-linux-platform-implementer/AGENT.md) |
| embedding-index-implementer | build | [AGENT.md](../roster/engineering/embedding-index-implementer/AGENT.md) |
| eval-dataset-implementer | build | [AGENT.md](../roster/engineering/eval-dataset-implementer/AGENT.md) |
| firmware-implementer | build | [AGENT.md](../roster/engineering/firmware-implementer/AGENT.md) |
| frontend-accessibility-remediator | build | [AGENT.md](../roster/engineering/frontend-accessibility-remediator/AGENT.md) |
| frontend-engineer | build | [AGENT.md](../roster/engineering/frontend-engineer/AGENT.md) |
| git-operations-implementer | build | [AGENT.md](../roster/engineering/git-operations-implementer/AGENT.md) |
| github-actions-implementer | build | [AGENT.md](../roster/engineering/github-actions-implementer/AGENT.md) |
| gitlab-ci-implementer | build | [AGENT.md](../roster/engineering/gitlab-ci-implementer/AGENT.md) |
| gitops-delivery-implementer | build | [AGENT.md](../roster/engineering/gitops-delivery-implementer/AGENT.md) |
| go-service-implementer | build | [AGENT.md](../roster/engineering/go-service-implementer/AGENT.md) |
| guardrail-policy-implementer | build | [AGENT.md](../roster/engineering/guardrail-policy-implementer/AGENT.md) |
| helm-chart-implementer | build | [AGENT.md](../roster/engineering/helm-chart-implementer/AGENT.md) |
| inference-gateway-implementer | build | [AGENT.md](../roster/engineering/inference-gateway-implementer/AGENT.md) |
| infrastructure-provisioner | build | [AGENT.md](../roster/engineering/infrastructure-provisioner/AGENT.md) |
| javascript-maintenance-implementer | build | [AGENT.md](../roster/engineering/javascript-maintenance-implementer/AGENT.md) |
| kernel-module-implementer | build | [AGENT.md](../roster/engineering/kernel-module-implementer/AGENT.md) |
| kubernetes-manifest-implementer | build | [AGENT.md](../roster/engineering/kubernetes-manifest-implementer/AGENT.md) |
| kubernetes-networking-implementer | build | [AGENT.md](../roster/engineering/kubernetes-networking-implementer/AGENT.md) |
| kubernetes-operator-implementer | build | [AGENT.md](../roster/engineering/kubernetes-operator-implementer/AGENT.md) |
| kyverno-policy-implementer | build | [AGENT.md](../roster/engineering/kyverno-policy-implementer/AGENT.md) |
| linux-systems-implementer | build | [AGENT.md](../roster/engineering/linux-systems-implementer/AGENT.md) |
| mcp-server-implementer | build | [AGENT.md](../roster/engineering/mcp-server-implementer/AGENT.md) |
| model-routing-implementer | build | [AGENT.md](../roster/engineering/model-routing-implementer/AGENT.md) |
| network-config-implementer | build | [AGENT.md](../roster/engineering/network-config-implementer/AGENT.md) |
| network-management-automation-implementer | build | [AGENT.md](../roster/engineering/network-management-automation-implementer/AGENT.md) |
| network-observability-implementer | build | [AGENT.md](../roster/engineering/network-observability-implementer/AGENT.md) |
| network-security-policy-implementer | build | [AGENT.md](../roster/engineering/network-security-policy-implementer/AGENT.md) |
| node-typescript-implementer | build | [AGENT.md](../roster/engineering/node-typescript-implementer/AGENT.md) |
| opentofu-module-implementer | build | [AGENT.md](../roster/engineering/opentofu-module-implementer/AGENT.md) |
| pkcs11-hsm-integration-implementer | security | [AGENT.md](../roster/security/pkcs11-hsm-integration-implementer/AGENT.md) |
| pki-certificate-lifecycle-implementer | security | [AGENT.md](../roster/security/pki-certificate-lifecycle-implementer/AGENT.md) |
| policy-as-code-engineer | security | [AGENT.md](../roster/security/policy-as-code-engineer/AGENT.md) |
| postgres-query-implementer | build | [AGENT.md](../roster/engineering/postgres-query-implementer/AGENT.md) |
| pqc-integration-implementer | security | [AGENT.md](../roster/security/pqc-integration-implementer/AGENT.md) |
| precision-timing-implementer | build | [AGENT.md](../roster/engineering/precision-timing-implementer/AGENT.md) |
| prompt-artifact-implementer | build | [AGENT.md](../roster/engineering/prompt-artifact-implementer/AGENT.md) |
| protocol-integration-implementer | build | [AGENT.md](../roster/engineering/protocol-integration-implementer/AGENT.md) |
| proxmox-opentofu-implementer | build | [AGENT.md](../roster/engineering/proxmox-opentofu-implementer/AGENT.md) |
| python-automation-implementer | build | [AGENT.md](../roster/engineering/python-automation-implementer/AGENT.md) |
| qkd-qkms-integration-implementer | build | [AGENT.md](../roster/engineering/qkd-qkms-integration-implementer/AGENT.md) |
| quantum-network-integration-implementer | build | [AGENT.md](../roster/engineering/quantum-network-integration-implementer/AGENT.md) |
| rbac-manifest-implementer | security | [AGENT.md](../roster/security/rbac-manifest-implementer/AGENT.md) |
| react-component-implementer | build | [AGENT.md](../roster/engineering/react-component-implementer/AGENT.md) |
| release-automation-implementer | build | [AGENT.md](../roster/engineering/release-automation-implementer/AGENT.md) |
| retrieval-pipeline-implementer | build | [AGENT.md](../roster/engineering/retrieval-pipeline-implementer/AGENT.md) |
| rtos-integration-implementer | build | [AGENT.md](../roster/engineering/rtos-integration-implementer/AGENT.md) |
| secret-hygiene-implementer | security | [AGENT.md](../roster/security/secret-hygiene-implementer/AGENT.md) |
| secrets-identity-engineer | security | [AGENT.md](../roster/security/secrets-identity-engineer/AGENT.md) |
| secure-boot-implementer | security | [AGENT.md](../roster/security/secure-boot-implementer/AGENT.md) |
| secure-channel-integration-implementer | security | [AGENT.md](../roster/security/secure-channel-integration-implementer/AGENT.md) |
| shell-automation-implementer | build | [AGENT.md](../roster/engineering/shell-automation-implementer/AGENT.md) |
| sonicos-config-implementer | build | [AGENT.md](../roster/engineering/sonicos-config-implementer/AGENT.md) |
| sql-query-implementer | build | [AGENT.md](../roster/engineering/sql-query-implementer/AGENT.md) |
| starlingx-config-implementer | build | [AGENT.md](../roster/engineering/starlingx-config-implementer/AGENT.md) |
| supply-chain-remediation-implementer | security | [AGENT.md](../roster/security/supply-chain-remediation-implementer/AGENT.md) |
| talos-config-implementer | build | [AGENT.md](../roster/engineering/talos-config-implementer/AGENT.md) |

### `test_author` (17 roles)

Creates or edits test artifacts and executes them against authorized non-production environments.

| Role | Phase | Definition |
| --- | --- | --- |
| black-box-tester | verify | [AGENT.md](../roster/testing/black-box-tester/AGENT.md) |
| browser-test-implementer | verify | [AGENT.md](../roster/testing/browser-test-implementer/AGENT.md) |
| end-user-tester | verify | [AGENT.md](../roster/testing/end-user-tester/AGENT.md) |
| eval-harness-implementer | verify | [AGENT.md](../roster/testing/eval-harness-implementer/AGENT.md) |
| example-fixture-implementer | verify | [AGENT.md](../roster/testing/example-fixture-implementer/AGENT.md) |
| gherkin-test-implementer | verify | [AGENT.md](../roster/testing/gherkin-test-implementer/AGENT.md) |
| go-test-implementer | verify | [AGENT.md](../roster/testing/go-test-implementer/AGENT.md) |
| hardware-test-implementer | verify | [AGENT.md](../roster/testing/hardware-test-implementer/AGENT.md) |
| interoperability-test-implementer | verify | [AGENT.md](../roster/testing/interoperability-test-implementer/AGENT.md) |
| migration-test-implementer | verify | [AGENT.md](../roster/testing/migration-test-implementer/AGENT.md) |
| performance-testing-engineer | verify | [AGENT.md](../roster/testing/performance-testing-engineer/AGENT.md) |
| protocol-fuzzing-implementer | verify | [AGENT.md](../roster/testing/protocol-fuzzing-implementer/AGENT.md) |
| python-test-implementer | verify | [AGENT.md](../roster/testing/python-test-implementer/AGENT.md) |
| red-team | verify | [AGENT.md](../roster/testing/red-team/AGENT.md) |
| selector-test-implementer | verify | [AGENT.md](../roster/testing/selector-test-implementer/AGENT.md) |
| test-engineer | verify | [AGENT.md](../roster/engineering/test-engineer/AGENT.md) |
| typescript-test-implementer | verify | [AGENT.md](../roster/testing/typescript-test-implementer/AGENT.md) |

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

### `security` (12 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| cryptographic-assurance-engineer | document_author | [AGENT.md](../roster/security/cryptographic-assurance-engineer/AGENT.md) |
| pkcs11-hsm-integration-implementer | code_author | [AGENT.md](../roster/security/pkcs11-hsm-integration-implementer/AGENT.md) |
| pki-certificate-lifecycle-implementer | code_author | [AGENT.md](../roster/security/pki-certificate-lifecycle-implementer/AGENT.md) |
| policy-as-code-engineer | code_author | [AGENT.md](../roster/security/policy-as-code-engineer/AGENT.md) |
| pqc-integration-implementer | code_author | [AGENT.md](../roster/security/pqc-integration-implementer/AGENT.md) |
| quantum-timing-assurance-engineer | document_author | [AGENT.md](../roster/security/quantum-timing-assurance-engineer/AGENT.md) |
| rbac-manifest-implementer | code_author | [AGENT.md](../roster/security/rbac-manifest-implementer/AGENT.md) |
| secret-hygiene-implementer | code_author | [AGENT.md](../roster/security/secret-hygiene-implementer/AGENT.md) |
| secrets-identity-engineer | code_author | [AGENT.md](../roster/security/secrets-identity-engineer/AGENT.md) |
| secure-boot-implementer | code_author | [AGENT.md](../roster/security/secure-boot-implementer/AGENT.md) |
| secure-channel-integration-implementer | code_author | [AGENT.md](../roster/security/secure-channel-integration-implementer/AGENT.md) |
| supply-chain-remediation-implementer | code_author | [AGENT.md](../roster/security/supply-chain-remediation-implementer/AGENT.md) |

### `build` (69 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| agent-workflow-implementer | code_author | [AGENT.md](../roster/engineering/agent-workflow-implementer/AGENT.md) |
| ai-engineer | code_author | [AGENT.md](../roster/engineering/ai-engineer/AGENT.md) |
| ai-observability-implementer | code_author | [AGENT.md](../roster/engineering/ai-observability-implementer/AGENT.md) |
| ansible-automation-implementer | code_author | [AGENT.md](../roster/engineering/ansible-automation-implementer/AGENT.md) |
| application-engineer | code_author | [AGENT.md](../roster/engineering/application-engineer/AGENT.md) |
| backend-engineer | code_author | [AGENT.md](../roster/engineering/backend-engineer/AGENT.md) |
| bare-metal-provisioning-implementer | code_author | [AGENT.md](../roster/engineering/bare-metal-provisioning-implementer/AGENT.md) |
| bgp-routing-implementer | code_author | [AGENT.md](../roster/engineering/bgp-routing-implementer/AGENT.md) |
| c-systems-implementer | code_author | [AGENT.md](../roster/engineering/c-systems-implementer/AGENT.md) |
| cicd-engineer | code_author | [AGENT.md](../roster/engineering/cicd-engineer/AGENT.md) |
| cmake-build-implementer | code_author | [AGENT.md](../roster/engineering/cmake-build-implementer/AGENT.md) |
| compose-stack-implementer | code_author | [AGENT.md](../roster/engineering/compose-stack-implementer/AGENT.md) |
| cpp-systems-implementer | code_author | [AGENT.md](../roster/engineering/cpp-systems-implementer/AGENT.md) |
| css-layout-implementer | code_author | [AGENT.md](../roster/engineering/css-layout-implementer/AGENT.md) |
| data-transformation-implementer | code_author | [AGENT.md](../roster/engineering/data-transformation-implementer/AGENT.md) |
| debugging-engineer | code_author | [AGENT.md](../roster/engineering/debugging-engineer/AGENT.md) |
| dependency-remediation-implementer | code_author | [AGENT.md](../roster/engineering/dependency-remediation-implementer/AGENT.md) |
| device-driver-implementer | code_author | [AGENT.md](../roster/engineering/device-driver-implementer/AGENT.md) |
| distributed-storage-implementer | code_author | [AGENT.md](../roster/engineering/distributed-storage-implementer/AGENT.md) |
| ebpf-implementer | code_author | [AGENT.md](../roster/engineering/ebpf-implementer/AGENT.md) |
| edge-cloud-integration-implementer | code_author | [AGENT.md](../roster/engineering/edge-cloud-integration-implementer/AGENT.md) |
| embedded-c-implementer | code_author | [AGENT.md](../roster/engineering/embedded-c-implementer/AGENT.md) |
| embedded-linux-platform-implementer | code_author | [AGENT.md](../roster/engineering/embedded-linux-platform-implementer/AGENT.md) |
| embedding-index-implementer | code_author | [AGENT.md](../roster/engineering/embedding-index-implementer/AGENT.md) |
| eval-dataset-implementer | code_author | [AGENT.md](../roster/engineering/eval-dataset-implementer/AGENT.md) |
| firmware-implementer | code_author | [AGENT.md](../roster/engineering/firmware-implementer/AGENT.md) |
| frontend-accessibility-remediator | code_author | [AGENT.md](../roster/engineering/frontend-accessibility-remediator/AGENT.md) |
| frontend-engineer | code_author | [AGENT.md](../roster/engineering/frontend-engineer/AGENT.md) |
| git-operations-implementer | code_author | [AGENT.md](../roster/engineering/git-operations-implementer/AGENT.md) |
| github-actions-implementer | code_author | [AGENT.md](../roster/engineering/github-actions-implementer/AGENT.md) |
| gitlab-ci-implementer | code_author | [AGENT.md](../roster/engineering/gitlab-ci-implementer/AGENT.md) |
| gitops-delivery-implementer | code_author | [AGENT.md](../roster/engineering/gitops-delivery-implementer/AGENT.md) |
| go-service-implementer | code_author | [AGENT.md](../roster/engineering/go-service-implementer/AGENT.md) |
| guardrail-policy-implementer | code_author | [AGENT.md](../roster/engineering/guardrail-policy-implementer/AGENT.md) |
| helm-chart-implementer | code_author | [AGENT.md](../roster/engineering/helm-chart-implementer/AGENT.md) |
| inference-gateway-implementer | code_author | [AGENT.md](../roster/engineering/inference-gateway-implementer/AGENT.md) |
| infrastructure-provisioner | code_author | [AGENT.md](../roster/engineering/infrastructure-provisioner/AGENT.md) |
| javascript-maintenance-implementer | code_author | [AGENT.md](../roster/engineering/javascript-maintenance-implementer/AGENT.md) |
| kernel-module-implementer | code_author | [AGENT.md](../roster/engineering/kernel-module-implementer/AGENT.md) |
| kubernetes-manifest-implementer | code_author | [AGENT.md](../roster/engineering/kubernetes-manifest-implementer/AGENT.md) |
| kubernetes-networking-implementer | code_author | [AGENT.md](../roster/engineering/kubernetes-networking-implementer/AGENT.md) |
| kubernetes-operator-implementer | code_author | [AGENT.md](../roster/engineering/kubernetes-operator-implementer/AGENT.md) |
| kyverno-policy-implementer | code_author | [AGENT.md](../roster/engineering/kyverno-policy-implementer/AGENT.md) |
| linux-systems-implementer | code_author | [AGENT.md](../roster/engineering/linux-systems-implementer/AGENT.md) |
| mcp-server-implementer | code_author | [AGENT.md](../roster/engineering/mcp-server-implementer/AGENT.md) |
| model-routing-implementer | code_author | [AGENT.md](../roster/engineering/model-routing-implementer/AGENT.md) |
| network-config-implementer | code_author | [AGENT.md](../roster/engineering/network-config-implementer/AGENT.md) |
| network-management-automation-implementer | code_author | [AGENT.md](../roster/engineering/network-management-automation-implementer/AGENT.md) |
| network-observability-implementer | code_author | [AGENT.md](../roster/engineering/network-observability-implementer/AGENT.md) |
| network-security-policy-implementer | code_author | [AGENT.md](../roster/engineering/network-security-policy-implementer/AGENT.md) |
| node-typescript-implementer | code_author | [AGENT.md](../roster/engineering/node-typescript-implementer/AGENT.md) |
| opentofu-module-implementer | code_author | [AGENT.md](../roster/engineering/opentofu-module-implementer/AGENT.md) |
| postgres-query-implementer | code_author | [AGENT.md](../roster/engineering/postgres-query-implementer/AGENT.md) |
| precision-timing-implementer | code_author | [AGENT.md](../roster/engineering/precision-timing-implementer/AGENT.md) |
| prompt-artifact-implementer | code_author | [AGENT.md](../roster/engineering/prompt-artifact-implementer/AGENT.md) |
| protocol-integration-implementer | code_author | [AGENT.md](../roster/engineering/protocol-integration-implementer/AGENT.md) |
| proxmox-opentofu-implementer | code_author | [AGENT.md](../roster/engineering/proxmox-opentofu-implementer/AGENT.md) |
| python-automation-implementer | code_author | [AGENT.md](../roster/engineering/python-automation-implementer/AGENT.md) |
| qkd-qkms-integration-implementer | code_author | [AGENT.md](../roster/engineering/qkd-qkms-integration-implementer/AGENT.md) |
| quantum-network-integration-implementer | code_author | [AGENT.md](../roster/engineering/quantum-network-integration-implementer/AGENT.md) |
| react-component-implementer | code_author | [AGENT.md](../roster/engineering/react-component-implementer/AGENT.md) |
| release-automation-implementer | code_author | [AGENT.md](../roster/engineering/release-automation-implementer/AGENT.md) |
| retrieval-pipeline-implementer | code_author | [AGENT.md](../roster/engineering/retrieval-pipeline-implementer/AGENT.md) |
| rtos-integration-implementer | code_author | [AGENT.md](../roster/engineering/rtos-integration-implementer/AGENT.md) |
| shell-automation-implementer | code_author | [AGENT.md](../roster/engineering/shell-automation-implementer/AGENT.md) |
| sonicos-config-implementer | code_author | [AGENT.md](../roster/engineering/sonicos-config-implementer/AGENT.md) |
| sql-query-implementer | code_author | [AGENT.md](../roster/engineering/sql-query-implementer/AGENT.md) |
| starlingx-config-implementer | code_author | [AGENT.md](../roster/engineering/starlingx-config-implementer/AGENT.md) |
| talos-config-implementer | code_author | [AGENT.md](../roster/engineering/talos-config-implementer/AGENT.md) |

### `verify` (19 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| black-box-tester | test_author | [AGENT.md](../roster/testing/black-box-tester/AGENT.md) |
| browser-test-implementer | test_author | [AGENT.md](../roster/testing/browser-test-implementer/AGENT.md) |
| chaos-resilience-engineer | environment_operator | [AGENT.md](../roster/testing/chaos-resilience-engineer/AGENT.md) |
| end-user-tester | test_author | [AGENT.md](../roster/testing/end-user-tester/AGENT.md) |
| eval-harness-implementer | test_author | [AGENT.md](../roster/testing/eval-harness-implementer/AGENT.md) |
| example-fixture-implementer | test_author | [AGENT.md](../roster/testing/example-fixture-implementer/AGENT.md) |
| falsification-agent | read_only | [AGENT.md](../roster/testing/falsification-agent/AGENT.md) |
| gherkin-test-implementer | test_author | [AGENT.md](../roster/testing/gherkin-test-implementer/AGENT.md) |
| go-test-implementer | test_author | [AGENT.md](../roster/testing/go-test-implementer/AGENT.md) |
| hardware-test-implementer | test_author | [AGENT.md](../roster/testing/hardware-test-implementer/AGENT.md) |
| interoperability-test-implementer | test_author | [AGENT.md](../roster/testing/interoperability-test-implementer/AGENT.md) |
| migration-test-implementer | test_author | [AGENT.md](../roster/testing/migration-test-implementer/AGENT.md) |
| performance-testing-engineer | test_author | [AGENT.md](../roster/testing/performance-testing-engineer/AGENT.md) |
| protocol-fuzzing-implementer | test_author | [AGENT.md](../roster/testing/protocol-fuzzing-implementer/AGENT.md) |
| python-test-implementer | test_author | [AGENT.md](../roster/testing/python-test-implementer/AGENT.md) |
| red-team | test_author | [AGENT.md](../roster/testing/red-team/AGENT.md) |
| selector-test-implementer | test_author | [AGENT.md](../roster/testing/selector-test-implementer/AGENT.md) |
| test-engineer | test_author | [AGENT.md](../roster/engineering/test-engineer/AGENT.md) |
| typescript-test-implementer | test_author | [AGENT.md](../roster/testing/typescript-test-implementer/AGENT.md) |

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

### `document` (5 roles)

| Role | Capability | Definition |
| --- | --- | --- |
| adr-writer | document_author | [AGENT.md](../roster/documentation/adr-writer/AGENT.md) |
| architecture-diagram-author | document_author | [AGENT.md](../roster/documentation/architecture-diagram-author/AGENT.md) |
| decision-record | document_author | [AGENT.md](../roster/documentation/decision-record/AGENT.md) |
| technical-documentation-implementer | document_author | [AGENT.md](../roster/documentation/technical-documentation-implementer/AGENT.md) |
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
