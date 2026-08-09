<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Proposal: small-context execution specialist roles

Status: Draft
Task ID: `small-context-execution-roles-2026-08-08`
Classification: internal
Author role: Cadre orchestrator
Requested by: repository owner

## Summary

Cadre should add narrow execution-specialist implementer roles for runners with smaller
context windows. These roles should not replace the current accountable roles
such as `backend-engineer`, `frontend-engineer`, `cicd-engineer`,
`infrastructure-provisioner`, `technical-writer`, or the review roles. They
should act as implementation workers with compact, task-specific instructions,
while broader roles keep architectural accountability, cross-cutting policy
context, and independent review authority.

The current role model is sound for frontier models with large context windows:
layer- and responsibility-oriented roles reduce authority fragmentation. The
model becomes less efficient when a runner has a constrained context window.
Broad roles carry policy, adjacent-domain checks, and disambiguation text that
can crowd out the immediate implementation task. Narrow execution roles give the
selector a way to dispatch smaller prompts without weakening approval or review
separation.

## Goals

- Preserve existing accountable role boundaries and human gates.
- Add compact implementation roles for recurring, well-bounded execution tasks.
- Improve routing quality for small-context runners and low-cost models.
- Keep each execution specialist incapable of approving its own work.
- Make language, framework, forge, diagramming, and platform tasks selectable
  without forcing every worker to carry a broad cross-stack role definition.

## Non-goals

- Do not replace high-level roles with technology titles.
- Do not create new approval authorities.
- Do not let execution roles decide organization-wide standards.
- Do not duplicate reviewer roles. Review remains with independent review
  agents such as `code-reviewer`, `security-reviewer`,
  `pipeline-security-reviewer`, `accessibility-reviewer`, and
  `infrastructure-reviewer`.
- Do not remove the prior shipped decision that language and forge concerns are
  not sufficient by themselves for lifecycle accountability. This proposal adds
  execution workers below that accountability layer.

## Design principle

Each execution specialist should have:

- `phase: build` except where the role is explicitly verification or
  documentation execution.
- `capability: code_author`, `test_author`, or `document_author` as narrowly as
  possible.
- Compact role text focused on one technology surface.
- Required inheritance of shared policy files, but not repeated full policy
  elaboration.
- Escalation to the broader accountable role when scope, standards, security,
  architecture, data, production, or approval questions arise.
- Mandatory handoff to an independent reviewer.

Routing should select a narrow execution role when path/keyword evidence is
specific enough, and include the broader accountable role as support or reviewer
context where useful. If routing is ambiguous, prefer the existing broader role.

## Proposed execution roles

Naming convention: use `*-implementer` for bounded artifact authors. Avoid
`*-engineer`, `*-architect`, `*-lead`, `*-owner`, or `*-reviewer` for this tier
because those names imply accountability or approval authority. A role that
authors documents or diagrams may use `*-author`, but it still follows the same
non-approval boundary.

### Language and runtime specialists

| Role ID | Accountable parent | Purpose |
| --- | --- | --- |
| `go-service-implementer` | `backend-engineer` or `application-engineer` | Implement Go services, CLIs, libraries, tests, and generators with `gofmt`, `goimports`, `go vet`, and Go test expectations. |
| `python-automation-implementer` | `application-engineer` or `backend-engineer` | Implement Python tooling, services, data transforms, tests, and automation without inheriting Go/PostgreSQL assumptions unless routed by task. |
| `node-typescript-implementer` | `frontend-engineer` or `application-engineer` | Implement TypeScript code outside strictly React-specific work, including SDKs, Node tools, plugin code, and typed tests. |
| `javascript-maintenance-implementer` | `frontend-engineer` or `application-engineer` | Maintain JavaScript where TypeScript is impractical or already established, with an explicit TypeScript-escalation rule. |
| `sql-query-implementer` | `backend-engineer` or `database-reliability-engineer` | Author bounded SQL, query changes, and migration-adjacent scripts while escalating schema lifecycle and operational risk. |
| `shell-automation-implementer` | `application-engineer` or `cicd-engineer` | Maintain small shell scripts, bootstrap commands, local automation, and CI snippets with strict quoting, idempotency, and secret-handling rules. |

### Frontend and browser specialists

| Role ID | Accountable parent | Purpose |
| --- | --- | --- |
| `react-component-implementer` | `frontend-engineer` | Implement React components, hooks, routing, state handling, and component tests with accessibility handoff. |
| `css-layout-implementer` | `frontend-engineer` or `visual-designer` | Implement CSS Modules, responsive layout, tokens consumption, and browser rendering fixes without owning visual-system decisions. |
| `browser-test-implementer` | `test-engineer` and `frontend-engineer` | Implement Vitest, Testing Library, and Playwright coverage for browser-facing behavior. |
| `frontend-accessibility-remediator` | `frontend-engineer` | Apply concrete accessibility fixes identified by `accessibility-reviewer`; cannot approve conformance. |

### Data, AI, and retrieval specialists

| Role ID | Accountable parent | Purpose |
| --- | --- | --- |
| `postgres-query-implementer` | `backend-engineer` or `database-reliability-engineer` | Implement PostgreSQL queries, indexes, migrations, fixtures, and pgx integration within approved schema strategy. |
| `data-transformation-implementer` | `backend-engineer` or `data-governance-engineer` | Implement ETL/ELT and batch data movement with lineage, classification, and retry/idempotency escalation. |
| `retrieval-pipeline-implementer` | `ai-engineer` | Implement retrieval, chunking, prompt assembly, citations, and eval harness plumbing without owning model-provider selection. |
| `prompt-artifact-implementer` | `ai-engineer` | Edit prompt artifacts, prompt tests, and prompt versioning records after an approved eval baseline exists. |
| `eval-harness-implementer` | `ai-engineer` or `test-engineer` | Implement model and prompt eval datasets, harnesses, scoring scripts, and regression checks. |

### Infrastructure and platform specialists

| Role ID | Accountable parent | Purpose |
| --- | --- | --- |
| `opentofu-module-implementer` | `infrastructure-provisioner` | Author OpenTofu modules, variables, validation, and plans; no apply/state authority. |
| `kubernetes-manifest-implementer` | `infrastructure-provisioner` | Implement Kubernetes manifests, RBAC, policies, and dry-run validation. |
| `helm-chart-implementer` | `infrastructure-provisioner` | Maintain Helm charts, values schemas, rendering tests, hooks, and release notes. |
| `talos-config-implementer` | `infrastructure-provisioner` | Maintain Talos configuration artifacts and validation commands; no cluster mutation authority. |
| `compose-stack-implementer` | `infrastructure-provisioner` | Maintain disposable local Docker/Podman Compose stacks, volumes, health checks, and local reset guidance. |
| `proxmox-opentofu-implementer` | `infrastructure-provisioner` | Implement Proxmox-specific OpenTofu resources and validation while escalating replacement/storage/network blast radius. |
| `kyverno-policy-implementer` | `policy-as-code-engineer` | Implement Kyverno policies, tests, exceptions, and namespace-scoped guardrails. |

### CI, forge, and release mechanics specialists

| Role ID | Accountable parent | Purpose |
| --- | --- | --- |
| `github-actions-implementer` | `cicd-engineer` | Implement GitHub Actions workflows, permissions, OIDC, environments, reusable workflows, and artifact upload/signing steps. |
| `gitlab-ci-implementer` | `cicd-engineer` | Implement GitLab CI pipelines, runner tags, protected variables/environments, artifacts, includes, and promotion jobs. |
| `git-operations-implementer` | `version-control-workflow` skill owner / `application-engineer` | Execute scoped branch, rebase, merge-conflict, and history-repair tasks when explicitly authorized; no merge approval. |
| `release-automation-implementer` | `release-engineer` | Implement package manifests, changelog fragments, checksums, SBOM/provenance generation scripts, and release artifact assembly. |
| `dependency-remediation-implementer` | `supply-chain-security-reviewer` as reviewer | Apply routine pinned dependency updates and generated-lockfile refreshes, with review handoff for license/security changes. |

### Documentation, diagrams, and examples specialists

| Role ID | Accountable parent | Purpose |
| --- | --- | --- |
| `architecture-diagram-author` | `technical-writer`, `cloud-architect`, or `api-contract-engineer` | Create and maintain Mermaid diagrams for flows, architecture, sequence, dependency, and state-machine views. |
| `technical-documentation-implementer` | `technical-writer` | Edit procedural docs, runbooks, examples, and role indexes with source-bound accuracy. |
| `adr-writer` | `decision-record` or `technical-writer` | Draft architecture decision records from approved decisions and alternatives. |
| `gherkin-test-implementer` | `test-engineer` | Author Gherkin feature files and scenario outlines from requirements and acceptance criteria. |
| `example-fixture-implementer` | `test-engineer` or `technical-writer` | Maintain sample projects, fixtures, golden corpus data, and executable examples. |

### Testing and quality specialists

| Role ID | Accountable parent | Purpose |
| --- | --- | --- |
| `go-test-implementer` | `test-engineer` | Implement Go unit, integration, race, and Godog tests. |
| `python-test-implementer` | `test-engineer` | Implement Python `unittest` tests, fixtures, parser tests, and CLI regression coverage. |
| `typescript-test-implementer` | `test-engineer` | Implement Vitest and TypeScript test coverage for plugins and frontend/tooling packages. |
| `selector-test-implementer` | `test-engineer` or `application-engineer` | Maintain Cadre selector, routing, golden corpus, and generated-content regression tests. |
| `migration-test-implementer` | `test-engineer` and `database-reliability-engineer` | Test database migration up/down/rollback and compatibility behavior in disposable environments. |

### Security implementation specialists

| Role ID | Accountable parent | Purpose |
| --- | --- | --- |
| `secret-hygiene-implementer` | `secrets-identity-engineer` | Apply concrete secret-removal, redaction, config-loading, and logging fixes identified by reviewers. |
| `rbac-manifest-implementer` | `secrets-identity-engineer` or `infrastructure-provisioner` | Implement scoped RBAC, service accounts, and least-privilege manifests after access requirements are approved. |
| `supply-chain-remediation-implementer` | `supply-chain-security-reviewer` as reviewer | Apply dependency pinning, checksum, provenance, SBOM, and artifact-integrity fixes. |

## Routing model

Add a new routing tier for execution specialists:

1. Match specific paths and keywords to execution roles first.
2. Keep the existing accountable role selected as support when the execution
   role is narrower than the lifecycle responsibility.
3. Keep independent reviewers unchanged.
4. Use team recipes only when two or more execution specialists must coordinate
   contracts; otherwise prefer cheap independent dispatch.

Examples:

| Task | Candidate execution role | Accountable/support role | Reviewer |
| --- | --- | --- | --- |
| `services/api/main.go` | `go-service-implementer` | `backend-engineer` | `code-reviewer`, `test-engineer` |
| `scripts/import_users.py` | `python-automation-implementer` | `application-engineer` or `backend-engineer` by route | `code-reviewer`, `test-engineer` |
| `frontend/src/Upload.tsx` | `react-component-implementer` | `frontend-engineer` | `accessibility-reviewer`, `code-reviewer`, `test-engineer` |
| `diagrams/system.mmd` | `architecture-diagram-author` | `technical-writer` or `cloud-architect` | `code-reviewer` or `architecture-authority` when architecture claims are made |
| `.github/workflows/release.yml` | `github-actions-implementer` | `cicd-engineer` | `pipeline-security-reviewer` |
| `.gitlab-ci.yml` | `gitlab-ci-implementer` | `cicd-engineer` | `pipeline-security-reviewer` |
| `infra/main.tf` | `opentofu-module-implementer` | `infrastructure-provisioner` | `infrastructure-reviewer` |
| `charts/app/values.yaml` | `helm-chart-implementer` | `infrastructure-provisioner` | `infrastructure-reviewer` |

## Implementation requirements

For each accepted role:

1. Add `roster/<domain>/<role-id>/AGENT.md` with compact frontmatter and role
   body.
2. Add the role ID to `roster/catalog-order.txt` near its accountable parent.
3. Run `cadre generate-role-metadata`.
4. Update `roster/orchestration/routing.yaml` route bodies outside the generated
   `knowledge_focus` block.
5. Add targeted selector tests and golden corpus fixtures.
6. Regenerate the plugin with `cadre generate-plugin --output plugin`.
7. Port/update Cline agents and tests under `cline-plugins/` where applicable.
8. Update `docs/role-index.md`, `docs/capability-index.md`, and any affected
   proposal or runbook examples.

## Test requirements

- Exact selector coverage for every new execution role.
- Negative coverage proving broad roles remain selected when specific evidence
  is absent.
- Golden corpus cases for Go, Python, TypeScript, React, GitHub Actions, GitLab
  CI, Mermaid, OpenTofu, Kubernetes, Helm, PostgreSQL, RAG, and Gherkin.
- Cline staffed-dispatch test for at least one execution role.
- Generated-content drift checks for catalog, provider, plugin, Codex wrappers,
  and Cline presets.
- Review-separation checks proving execution roles are not read-only reviewers
  and cannot approve their own artifacts.
- Runner tests proving author roles run before independent reviewers, and that
  reviewers receive a revision-bound evidence package rather than the original
  implementation prompt.
- Provider/model and tool-policy negative tests proving absent mappings,
  unapproved providers, unknown tools, or missing allowlists fail closed.
- Policy-envelope hash-drift tests if compact role prompts reference shared
  policy by hash rather than embedding it wholesale.

## Security and governance guardrails

- Execution roles may author scoped artifacts only.
- Execution roles must not approve, merge, deploy, mutate persistent
  environments, accept risk, choose organization-wide standards, or bypass gates.
- Any task involving credentials, production, privileged identity, persistent
  infrastructure, regulated data, public exposure, or policy exceptions escalates
  to the existing accountable and review roles.
- Forge-specific roles must not generalize GitHub controls to GitLab or GitLab
  controls to GitHub.
- Language-specific roles must not override project standards or introduce
  unapproved dependencies.
- Documentation and diagramming roles must treat diagrams as claims requiring
  source evidence.
- Dispatch sequencing must be author first, immutable revision/evidence second,
  independent reviewer third. Parallel author/reviewer fan-out is not acceptable
  for approval-like outputs.
- Provider and model mappings must be explicit, approved, and fail-closed.
  Small-context optimization must not silently route repository content to an
  unapproved provider or downgrade a security/review role.
- Tool allowlists must be explicit and generated. Missing tool policy denies
  execution rather than granting broad tools.
- Compact prompts should carry a versioned policy envelope containing role ID,
  authority, allowed tools, task path scope, relevant policy hashes, and only
  task-relevant policy excerpts. The canonical shared policies remain the source
  of truth.

## Rollout plan

1. Start with the highest-value gap set:
   `architecture-diagram-author`, `python-automation-implementer`,
   `go-service-implementer`, `react-component-implementer`,
   `github-actions-implementer`, `gitlab-ci-implementer`,
   `opentofu-module-implementer`, `helm-chart-implementer`,
   `kubernetes-manifest-implementer`, `postgres-query-implementer`,
   `node-typescript-implementer`, and `selector-test-implementer`.
2. Add routing/golden fixtures for those roles.
3. Validate dispatch behavior on both large-context and small-context runners.
4. Add the remaining specialists only where repeated real tasks show measurable
   context savings or routing clarity.
5. Revisit role count after usage telemetry or maintainer experience shows
   whether the added specificity improves completion quality.

## Open decisions

- Whether execution specialists should be visible as first-class user-facing
  roles or treated as internal dispatch targets.
- Whether each execution specialist needs a distinct role file, or whether a
  parameterized lightweight-role template can generate them without duplicating
  policy text.
- Whether `git-operations-implementer` should be a role, a skill enhancement, or
  only a workflow wrapper around `version-control-workflow`.
- Whether `architecture-diagram-author` should live under documentation or
  architecture.
- How small a context window this proposal optimizes for, and how much shared
  policy text can be safely referenced instead of embedded.
- Whether the Cline dispatch implementation must be fixed before any compact
  execution role is exposed there.

## Recommendation

Proceed with a first wave of execution specialists. Treat them as narrow
implementation workers beneath existing accountable roles. The strongest initial
additions are `architecture-diagram-author`, `python-automation-implementer`,
`go-service-implementer`, `react-component-implementer`,
`github-actions-implementer`, `gitlab-ci-implementer`,
`opentofu-module-implementer`, `helm-chart-implementer`,
`kubernetes-manifest-implementer`, `postgres-query-implementer`,
`node-typescript-implementer`, and `selector-test-implementer`.

This gives small-context runners compact task prompts while preserving Cadre's
current lifecycle accountability and independent-review model. Implementation
should not proceed until the dispatch sequencing, provider/model, and tool-policy
fail-closed controls above are either already satisfied by the target runner or
tracked as blocking prerequisites for that runner.
