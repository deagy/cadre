<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Role index

This index is a human-readable view of the 86 roles in
[`roster/catalog.yaml`](../roster/catalog.yaml). The catalog and each linked
`AGENT.md` remain authoritative.

Sections below group roles by lifecycle `phase` (the catalog field), which
does not always match a role's `AGENT.md` directory — directories group by
subject-matter domain instead. `cost-capacity-planner` is the clearest case:
`phase: planning` (it estimates demand before commitments are made) but its
definition lives under `roster/operations/` (capacity and cost are an
operations concern once a workload is live). Treat `catalog.yaml`'s `phase`
as authoritative for sequencing; the directory is only a filing convenience.
For a phase/capability-tier browsable view instead, see the
[capability index](capability-index.md); for term definitions (capability
tier, route, quality gate, human gate, ...), see the
[glossary](terminology.md).

## Planning and governance

| Role | Phase | Purpose | Definition |
| --- | --- | --- | --- |
| product-intent-agent | planning | Translate a human mission into a reviewable intent record. | [AGENT.md](../roster/planning/product-intent-agent/AGENT.md) |
| requirements-agent | planning | Decompose approved intent into testable, traceable obligations. | [AGENT.md](../roster/planning/requirements-agent/AGENT.md) |
| governance-planner | design | Plan governance, policy, control, jurisdiction, and evidence obligations. | [AGENT.md](../roster/governance/governance-planner/AGENT.md) |
| cost-capacity-planner | planning | Estimate capacity, resource demand, storage, utilization, and cost tradeoffs. | [AGENT.md](../roster/operations/cost-capacity-planner/AGENT.md) |

## Architecture, security, and data

| Role | Phase | Purpose | Definition |
| --- | --- | --- | --- |
| cloud-architect | design | Design secure, resilient, operable, cost-aware architecture. | [AGENT.md](../roster/architecture/cloud-architect/AGENT.md) |
| threat-modeler | design | Identify credible threats and translate them into testable requirements. | [AGENT.md](../roster/architecture/threat-modeler/AGENT.md) |
| api-contract-engineer | design | Own cross-service API/schema contract design, versioning, and compatibility. | [AGENT.md](../roster/architecture/api-contract-engineer/AGENT.md) |
| interaction-designer | design | Own user-facing interaction/UX design, flow states, and accessibility intent upstream of implementation. | [AGENT.md](../roster/architecture/interaction-designer/AGENT.md) |
| visual-designer | design | Own the visual system: design tokens, component specifications, and usage rules, downstream of interaction design. | [AGENT.md](../roster/architecture/visual-designer/AGENT.md) |
| data-governance-engineer | design | Define classification, ownership, lineage, residency, retention, and deletion requirements. | [AGENT.md](../roster/data/data-governance-engineer/AGENT.md) |
| cryptographic-assurance-engineer | security | Assess cryptographic inventory, algorithms, keys, certificates, and agility. | [AGENT.md](../roster/security/cryptographic-assurance-engineer/AGENT.md) |
| quantum-timing-assurance-engineer | security | Validate that physical measurements from quantum and timing sources are trustworthy enough to act on. | [AGENT.md](../roster/security/quantum-timing-assurance-engineer/AGENT.md) |
| secrets-identity-engineer | security | Review secrets, workload identity, credentials, RBAC, and access boundaries. | [AGENT.md](../roster/security/secrets-identity-engineer/AGENT.md) |
| policy-as-code-engineer | security | Design machine-enforced guardrails for infrastructure and delivery policy. | [AGENT.md](../roster/security/policy-as-code-engineer/AGENT.md) |
| database-reliability-engineer | operations | Assess PostgreSQL reliability, migrations, backups, recovery, and performance risk. | [AGENT.md](../roster/data/database-reliability-engineer/AGENT.md) |
| observability-sre | operations | Design telemetry, SLOs, alerts, dashboards, and day-2 readiness. | [AGENT.md](../roster/operations/observability-sre/AGENT.md) |
| finops-engineer | operations | Monitor live cost/utilization drift against the approved capacity model. | [AGENT.md](../roster/operations/finops-engineer/AGENT.md) |
| decommission-engineer | operations | Plan and verify preconditions for retiring a capability or service after G10. | [AGENT.md](../roster/operations/decommission-engineer/AGENT.md) |

## Engineering and delivery

The small-context execution specialists below implement a bounded, approved
slice under the applicable existing accountable role. They do not replace that
role's responsibility for scope, design decisions, security posture, review
coordination, or escalation; the routing plan retains the accountable role and
the required independent reviewers. Like every artifact author, a specialist
cannot approve its own output, accept risk, or authorize a persistent or
production mutation.

| Role | Phase | Purpose | Definition |
| --- | --- | --- | --- |
| application-engineer | build | Own routine changes to this suite's own tooling, catalog, and orchestration source (not a target project's application code). | [AGENT.md](../roster/engineering/application-engineer/AGENT.md) |
| frontend-engineer | build | Build secure, accessible React and TypeScript frontends. | [AGENT.md](../roster/engineering/frontend-engineer/AGENT.md) |
| backend-engineer | build | Build secure Go backend services with PostgreSQL. | [AGENT.md](../roster/engineering/backend-engineer/AGENT.md) |
| ai-engineer | build | Build a product's model-facing layer: model selection, prompts, retrieval, evals, and inference cost/latency. | [AGENT.md](../roster/engineering/ai-engineer/AGENT.md) |
| infrastructure-provisioner | build | Create reusable infrastructure-as-code and reviewable plans. | [AGENT.md](../roster/engineering/infrastructure-provisioner/AGENT.md) |
| cicd-engineer | build | Build secure pipelines for tests, scans, artifacts, promotion, and rollback. | [AGENT.md](../roster/engineering/cicd-engineer/AGENT.md) |
| debugging-engineer | build | Reproduce failures, identify root cause, and apply scoped authorized fixes. | [AGENT.md](../roster/engineering/debugging-engineer/AGENT.md) |
| release-engineer | release | Coordinate artifact promotion and release execution after required gates. | [AGENT.md](../roster/engineering/release-engineer/AGENT.md) |
| python-automation-implementer | build | Implement bounded Python tooling, automation, data transforms, and tests under accountable engineering. | [AGENT.md](../roster/engineering/python-automation-implementer/AGENT.md) |
| go-service-implementer | build | Implement bounded Go services, CLIs, libraries, generators, and tests. | [AGENT.md](../roster/engineering/go-service-implementer/AGENT.md) |
| react-component-implementer | build | Implement bounded React components, hooks, routing, state, and component tests. | [AGENT.md](../roster/engineering/react-component-implementer/AGENT.md) |
| github-actions-implementer | build | Implement bounded GitHub Actions workflows and artifact/identity steps. | [AGENT.md](../roster/engineering/github-actions-implementer/AGENT.md) |
| gitlab-ci-implementer | build | Implement bounded GitLab CI pipelines, runner/environment/artifact/promotion steps. | [AGENT.md](../roster/engineering/gitlab-ci-implementer/AGENT.md) |
| opentofu-module-implementer | build | Implement bounded OpenTofu modules, variables, validations, and plans. | [AGENT.md](../roster/engineering/opentofu-module-implementer/AGENT.md) |
| helm-chart-implementer | build | Implement bounded Helm charts, values schemas, render tests, hooks, and release notes. | [AGENT.md](../roster/engineering/helm-chart-implementer/AGENT.md) |
| kubernetes-manifest-implementer | build | Implement bounded Kubernetes manifests, RBAC, and policy artifacts. | [AGENT.md](../roster/engineering/kubernetes-manifest-implementer/AGENT.md) |
| postgres-query-implementer | build | Implement bounded PostgreSQL queries, indexes, migrations, fixtures, and pgx integration. | [AGENT.md](../roster/engineering/postgres-query-implementer/AGENT.md) |
| node-typescript-implementer | build | Implement bounded non-React TypeScript/Node tools, SDKs, plugins, and typed tests. | [AGENT.md](../roster/engineering/node-typescript-implementer/AGENT.md) |

## Verification and review

| Role | Phase | Purpose | Definition |
| --- | --- | --- | --- |
| test-engineer | verify | Design and execute risk-based application, infrastructure, pipeline, and resilience tests. | [AGENT.md](../roster/engineering/test-engineer/AGENT.md) |
| selector-test-implementer | verify | Implement bounded Cadre selector, routing, golden-corpus, and generated-content regressions. | [AGENT.md](../roster/testing/selector-test-implementer/AGENT.md) |
| black-box-tester | verify | Validate external behavior without implementation or privileged shortcuts. | [AGENT.md](../roster/testing/black-box-tester/AGENT.md) |
| end-user-tester | verify | Evaluate whether users can safely complete intended workflows. | [AGENT.md](../roster/testing/end-user-tester/AGENT.md) |
| performance-testing-engineer | verify | Validate throughput, latency, and capacity assumptions against a candidate build. | [AGENT.md](../roster/testing/performance-testing-engineer/AGENT.md) |
| chaos-resilience-engineer | verify | Inject controlled faults in disposable environments to verify RTO/RPO and alerting claims. | [AGENT.md](../roster/testing/chaos-resilience-engineer/AGENT.md) |
| code-reviewer | review | Independently assess application correctness, security, maintainability, and tests. | [AGENT.md](../roster/review/code-reviewer/AGENT.md) |
| accessibility-reviewer | review | Independently verify browser-facing changes against the accessibility target. | [AGENT.md](../roster/review/accessibility-reviewer/AGENT.md) |
| infrastructure-reviewer | review | Independently assess infrastructure security, correctness, resilience, and impact. | [AGENT.md](../roster/review/infrastructure-reviewer/AGENT.md) |
| pipeline-security-reviewer | review | Independently review CI/CD trust boundaries, identities, runners, artifacts, and deployment controls. | [AGENT.md](../roster/review/pipeline-security-reviewer/AGENT.md) |
| supply-chain-security-reviewer | review | Review dependency, build, package, SBOM, provenance, signing, and image risks. | [AGENT.md](../roster/review/supply-chain-security-reviewer/AGENT.md) |
| security-reviewer | review | Evaluate the end-to-end change against threats, policy, guardrails, and risk tolerance. | [AGENT.md](../roster/review/security-reviewer/AGENT.md) |
| compliance-reviewer | review | Assess applicable controls and durable audit-ready evidence. | [AGENT.md](../roster/review/compliance-reviewer/AGENT.md) |

## Documentation, support, and knowledge

`architecture-diagram-author` is likewise an execution specialist: it renders
approved source material without changing the architecture, and does not hold
architecture or approval authority.

| Role | Phase | Purpose | Definition |
| --- | --- | --- | --- |
| technical-writer | document | Create accurate, task-oriented documentation from approved sources. | [AGENT.md](../roster/documentation/technical-writer/AGENT.md) |
| architecture-diagram-author | document | Create bounded source-backed Mermaid architecture, flow, sequence, dependency, and state diagrams without altering architecture. | [AGENT.md](../roster/documentation/architecture-diagram-author/AGENT.md) |
| evidence-curator | evidence | Collect, normalize, index, protect, and retain delivery and compliance evidence. | [AGENT.md](../roster/documentation/evidence-curator/AGENT.md) |
| knowledge-store-steward | knowledge | Operate the authorized, provenance-preserving agent knowledge store. | [AGENT.md](../roster/knowledge-store/AGENT.md) |
| support-triage-agent | support | Classify user reports, protect sensitive data, and route actionable cases. | [AGENT.md](../roster/support/support-triage-agent/AGENT.md) |
| escalation-manager | support | Coordinate escalations so urgent or high-risk issues stop at the right gate. | [AGENT.md](../roster/support/escalation-manager/AGENT.md) |
| incident-commander | support | Coordinate major incidents while preserving safety, evidence, and communication. | [AGENT.md](../roster/support/incident-commander/AGENT.md) |

## Governance, authority, and challenge functions

Cross-cutting roles that gate, route, capture provenance for, or deliberately challenge other work, rather than producing it. Distinct from the human authority aides below, which prepare a decision package for one named human lifecycle authority; these roles instead produce a finding, register entry, or determination usable at any applicable gate.

| Role | Phase | Purpose | Definition |
| --- | --- | --- | --- |
| halt-authority | review | Hold the cross-cutting stop-control finding: arrest work in progress on a doctrine, architecture, evidence-chain, or safety condition. | [AGENT.md](../roster/review/halt-authority/AGENT.md) |
| approval-router | review | Encode the authority matrix and block work until the required signature is present. | [AGENT.md](../roster/governance/approval-router/AGENT.md) |
| doctrine-conformance | review | Verify narrative, framing, and terminology against the project's doctrine and terminology register. | [AGENT.md](../roster/review/doctrine-conformance/AGENT.md) |
| architecture-authority | review | Enforce the abstraction-layer rule; reject any change reaching infrastructure without an approved boundary. | [AGENT.md](../roster/review/architecture-authority/AGENT.md) |
| scope-boundary | planning | Reject work that drifts outside the stated build boundary into future-state capability. | [AGENT.md](../roster/planning/scope-boundary/AGENT.md) |
| phase-gate | release | Verify a build phase's exit criteria are met and evidenced before the next phase begins. | [AGENT.md](../roster/review/phase-gate/AGENT.md) |
| assumption-register | planning | Track what the build depends on being true and what observation would invalidate it. | [AGENT.md](../roster/planning/assumption-register/AGENT.md) |
| decision-record | document | Capture decision provenance: who decided, when, on what basis, and what alternatives were rejected. | [AGENT.md](../roster/documentation/decision-record/AGENT.md) |
| red-team | verify | Run adversarial assessment against the system as actually deployed, not as designed. | [AGENT.md](../roster/testing/red-team/AGENT.md) |
| premortem | planning | Assume a committed initiative already failed and work backward to plausible causes. | [AGENT.md](../roster/planning/premortem/AGENT.md) |
| delivery-sequencer | planning | Map dependencies, the critical path, and delivery sequence — order only, never priority or dates. | [AGENT.md](../roster/planning/delivery-sequencer/AGENT.md) |
| first-principles-challenger | design | Challenge whether an inherited design constraint is real or just unexamined. | [AGENT.md](../roster/architecture/first-principles-challenger/AGENT.md) |
| subtraction-agent | review | Argue for removal on any scope increase, feature addition, or interface expansion. | [AGENT.md](../roster/review/subtraction-agent/AGENT.md) |
| falsification-agent | verify | Demand the disproving test for any claim of correctness, resilience, or continuity. | [AGENT.md](../roster/testing/falsification-agent/AGENT.md) |
| deployment-realist | operations | Assess operability at real scale with real participants, not demonstrated feasibility. | [AGENT.md](../roster/operations/deployment-realist/AGENT.md) |
| classification-and-marking-gate | release | Determine whether an artifact is correctly classified and marked, and may leave the environment. | [AGENT.md](../roster/review/classification-and-marking-gate/AGENT.md) |
| claim-conformance | release | Verify an external-facing artifact does not assert more than the project can demonstrate. | [AGENT.md](../roster/review/claim-conformance/AGENT.md) |
| vendor-register-steward | operations | Maintain the vendor/tooling register and detect drift as repositories and workflows change. | [AGENT.md](../roster/operations/vendor-register-steward/AGENT.md) |
| retention-and-deletion-executor | operations | Execute already-approved retention and deletion obligations, with evidence. | [AGENT.md](../roster/operations/retention-and-deletion-executor/AGENT.md) |
| agent-performance-evaluator | operations | Assess whether the roles in this catalog are producing correct output. | [AGENT.md](../roster/operations/agent-performance-evaluator/AGENT.md) |
| agent-version-control | operations | Maintain provenance for the agent definitions themselves as they change. | [AGENT.md](../roster/operations/agent-version-control/AGENT.md) |
| ip-provenance-agent | evidence | Apply the current IP rule version to an artifact's provenance record and produce a determination. | [AGENT.md](../roster/documentation/ip-provenance-agent/AGENT.md) |

## Human authority aides

Read-only agents that prepare the decision package a named human lifecycle
authority needs for their assigned gate(s) — never approve, recommend a
disposition, or hold delegated authority themselves. See
[`docs/proposals/human-authority-role-agents.md`](proposals/human-authority-role-agents.md)
for the design rationale, including why they never state a recommended
disposition and why delegated approval authority was deliberately not built
here.

| Role | Phase | Purpose | Definition |
| --- | --- | --- | --- |
| product-owner-aide | authority | Prepare G1/G2/G6 decision packages for the human Product Owner. | [AGENT.md](../roster/authority/product-owner-aide/AGENT.md) |
| engineering-lead-aide | authority | Prepare G2/G6 decision packages for the human Engineering Lead. | [AGENT.md](../roster/authority/engineering-lead-aide/AGENT.md) |
| system-architect-aide | authority | Prepare G3 decision packages for the human System Architect. | [AGENT.md](../roster/authority/system-architect-aide/AGENT.md) |
| governance-lead-aide | authority | Prepare G4 decision packages for the human Governance Lead. | [AGENT.md](../roster/authority/governance-lead-aide/AGENT.md) |
| security-lead-aide | authority | Prepare G5 decision packages for the human Security Lead. | [AGENT.md](../roster/authority/security-lead-aide/AGENT.md) |
| release-owner-aide | authority | Prepare G7/G8 decision packages for the human Release Owner. | [AGENT.md](../roster/authority/release-owner-aide/AGENT.md) |
| release-authority-aide | authority | Prepare G9 decision packages for the human Release Authority. | [AGENT.md](../roster/authority/release-authority-aide/AGENT.md) |
| service-owner-aide | authority | Prepare G10 decision packages for the human Service Owner. | [AGENT.md](../roster/authority/service-owner-aide/AGENT.md) |
