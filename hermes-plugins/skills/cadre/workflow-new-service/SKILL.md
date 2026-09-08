---
name: workflow-new-service
description: "run the cadre new service workflow end to end."
version: 0.1.0
author: deagy, Hermes Agent
license: MIT
platforms: [linux, macos, windows]
metadata:
  hermes:
    tags: [cadre, workflow, orchestration]
    related_skills: [cadre-orchestrator]
---

# New Service Workflow

Hermes analog of the cadre workflow playbook. Steps name cadre roles; each role is an installed Hermes skill under the `cadre/` category (skill name = role id). Dispatch each step's role via `delegate_task` (independent steps in one parallel batch), passing that skill's contract + the step's inputs as `context`. Consult `cadre-orchestrator` for role selection, gates, and escalation.

Where the playbook text references `cadre` CLI commands, `roster/` repository paths, or selector machinery, that describes the upstream system: substitute the `cadre-orchestrator` dispatch protocol and its knowledge-retrieval analog; the gate semantics, evidence requirements, and stop conditions still bind.


This workflow covers work that proceeds beyond intake into design and delivery.
Use `the installed `workflow-product-intake` skill` when the task only captures intent or baselines
requirements. Knowledge retrieval is required at every relevant phase under the
retrieval policy, but it is a cross-cutting prerequisite rather than G1-G10.

```mermaid
flowchart LR
    Intent -->|"G1: Product Owner"| Requirements
    Requirements -->|"G2: Product Owner + Engineering Lead"| Architecture
    Architecture -->|"G3: System Architect"| Governance["Governance & Data"]
    Governance -->|"G4: Governance Lead"| Security["Security & Crypto"]
    Security -->|"G5: Security Lead"| Implementation
    Implementation --> Verification
    Verification -->|"G6: Product Owner + Engineering Lead"| Evidence
    Evidence -->|"G7: Release Owner"| Readiness["Release Readiness"]
    Readiness -->|"G8: Release Owner"| Authorization["Deployment Authorization"]
    Authorization -->|"G9: Release Authority"| Deployment
    Deployment -->|"G10: Service Owner"| Runtime["Runtime & Feedback"]
```

Cross-checked against `roster/authority/aides.yaml`'s per-gate authority list.

1. **Intent and requirements:** Product Intent Agent drafts the versioned intent; the Human Product Owner decides G1. Requirements Agent derives stable, traceable functional, non-functional, control, test, and evidence obligations; the Product Owner and Engineering Lead decide G2.
2. **Early assurance:** Governance Planner, Data Governance Engineer, and Cryptographic Assurance Engineer classify applicability, populate the platform impact profile, and identify unresolved definitions. `unknown` applicable items fail closed.
3. **Architecture:** Cloud Architect maps the approved baseline and platform profile to boundaries, APIs, data/trust flows, ADRs, failure/recovery behavior, and validation obligations. The Human System Architect decides G3.
4. **Governance and data:** Policy, jurisdiction, accreditation, classification, lineage, residency, non-egress, retention/deletion, and derived-output evidence are independently reviewed. The authorities in G4 decide progression.
5. **Security and crypto:** Threat Modeler and relevant identity, supply-chain, pipeline, and crypto specialists produce bounded attestations. Independent Security Reviewer and G5 human authorities decide progression.
6. **Implementation:** Frontend, backend, application, infrastructure, database, identity, policy, CI/CD, observability, and capacity roles implement in parallel within approved constraints. None may approve its own output.
7. **Verification:** Test Engineer, Black-Box Tester, End-User Tester, and independent code/infrastructure/pipeline/supply-chain reviewers verify the exact revision and artifacts. When the service has stated SLO/capacity or RTO/RPO targets, Performance Testing Engineer and Chaos & Resilience Engineer validate those claims against a disposable environment rather than trusting them on paper. G6 requires requirements/control traceability and an independence declaration.
8. **Evidence:** Technical Writer updates approved documentation. Evidence Curator indexes, but does not manufacture or approve, source evidence and formally defined applicable BOMs. Compliance Reviewer and Release Owner decide G7.
9. **Release readiness and authorization:** Release Engineer assembles readiness evidence for G8. An Authorized Human Release Authority alone decides G9 for the exact artifact, target, identity, plan, window, rollback, and thresholds.
10. **Deployment and runtime:** Deploy progressively, verify, and follow `the installed `workflow-runtime-assurance` skill` for G10 and feedback.

Failed gates return to the responsible artifact owner and name the earliest
required re-entry gate. A material change invalidates that gate and every
dependent downstream gate. A reviewer who makes a material correction becomes
an author and must transfer approval to a different independent reviewer.
