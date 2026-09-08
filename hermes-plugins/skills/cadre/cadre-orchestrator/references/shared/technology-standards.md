# Technology Standards

These standards specialize `team-profile.yaml`. Where a value remains `not_yet_selected`, agents must present alternatives or request a decision rather than silently choosing an organization-wide standard.

## Proxmox and OpenTofu

- Treat OpenTofu (Terraform-protocol-compatible; chosen over Terraform CLI to avoid BUSL license terms, see `team-profile.yaml`'s `infrastructure_as_code_note`) as the desired-state source for Proxmox infrastructure and supporting resources within its managed scope.
- Keep reusable modules versioned and separate cluster-wide primitives from workload-specific resources.
- Do not make undocumented console changes, edit OpenTofu/Terraform state manually, or import/adopt resources without explicit approval and a recovery plan.
- Bind plans to an exact source revision, state snapshot, workspace, variables, provider versions, and Proxmox target.
- Highlight VM replacement, disk/storage changes, network changes, node placement, privilege changes, and lifecycle exceptions.

## Talos and Kubernetes

- Treat Talos as an immutable, API-managed operating system. Do not propose SSH-based administration, package installation on nodes, or unmanaged host changes.
- Manage Talos machine and cluster configuration declaratively, protect machine secrets and trust material, and plan quorum-safe upgrades and recovery.
- Keep Kubernetes resources declarative, namespace-scoped where practical, and least-privileged through service accounts and RBAC.
- Require resource requests/limits, health probes, disruption considerations, network policy, security context, observability, backup, and recovery appropriate to workload risk.

## Helm

- Use Helm for Kubernetes package deployment. Keep charts and values reviewable, deterministic, and environment differences explicit.
- Pin chart and image versions; avoid mutable tags for releasable workloads.
- Render and validate manifests before deployment. Review hooks, custom resources, cluster-scoped objects, RBAC, secrets references, and deletion/rollback behavior.
- Do not store secret values in chart values or rendered artifacts.

## Go and Python

- Follow `library-standards.yaml` for preferred Go libraries, tools, import paths, constraints, and exception handling.
- Prefer Go for services, operators, CLIs, and long-lived automation.
- Use Python when it materially simplifies a bounded task, integration, data transformation, or test utility; document why it is preferable for that component.
- Pin dependencies, use supported project-defined versions, run `gofmt`, `goimports`, `go vet`, `go test`, and `golangci-lint`, and avoid introducing a second implementation path without need.
- Keep interfaces and operational behavior consistent across languages.

## AI/ML product features

These apply to AI behavior a consuming project ships to its own users. They do
not govern the models this suite's own agents run on (the per-role `model` tier
recorded in the role catalog) or the agent knowledge store
(`knowledge-use-policy.md`).

- Do not establish an organization-wide model provider, eval framework, or vector store while `team-profile.yaml`'s `ai` block records them as `not_yet_selected`. Present alternatives with cost, latency, data-residency, and exit-cost tradeoffs, and request a decision.
- Treat model output as untrusted data on the same footing as retrieved knowledge and tool output. Validate and constrain it before it reaches a downstream system, a rendered surface, or any privileged action; model output never authorizes a privileged or destructive action on its own.
- Establish an eval baseline before changing a prompt, model, or retrieval strategy, and report the measured effect rather than the expected one. Version prompts as reviewable artifacts, not as inline string literals edited in place.
- State what data crosses the trust boundary on each model call, including anything assembled into context by retrieval, and check it against the feature's classification and residency constraints before implementing.
- Record inference cost and latency per call path, including retries and fallbacks, and define behavior for provider unavailability, timeout, truncation, refusal, and malformed output. A feature with no defined degraded mode is not finished.
- Pin model identifiers and provider SDK versions. A floating model alias makes a behavior change indistinguishable from a regression in your own code.

## React frontends

- Use React with TypeScript for new frontend application code. Use JavaScript only when TypeScript is impractical and record the reason.
- Use Node.js for frontend build and development tooling; pin the Node and dependency-manager versions once selected.
- Do not establish a React framework, build tool, package manager, styling system, component library, or test runner as an organization-wide default until the team records that decision.
- Prefer web-platform semantics, accessible HTML, keyboard operation, responsive layouts, explicit loading/error/empty states, and secure browser/API boundaries.
- Keep authentication material out of browser-persisted storage unless the security design explicitly permits it. Prevent XSS, CSRF, unsafe redirects, dependency injection, and sensitive-data leakage through bundles, logs, analytics, or source maps.
- Keep API clients typed and generated or validated from an authoritative contract where practical.
- When running frontend development tooling in read-only local containers, provide explicit tmpfs-backed cache/temp paths and verify the tool's config loader does not write under immutable project or dependency directories.

## PostgreSQL backends

- Use PostgreSQL as the backend datastore and `github.com/jackc/pgx/v5` for Go access unless a documented exception is approved.
- Keep schema migrations versioned, ordered, reviewable, reversible where practical, and compatible with the deployment/rollback strategy.
- Use parameterized queries, least-privilege database roles, TLS where applicable, bounded connection pools, context deadlines, transaction boundaries, and observable slow-query behavior.
- Design backup, restore, point-in-time recovery, high availability, capacity, maintenance, and schema ownership before production use.
- Never place database credentials in source, frontend bundles, Helm values, OpenTofu/Terraform output, CI logs, or generated documentation.
- For PostgreSQL 18+ containerized disposable stacks, mount persistent database storage at `/var/lib/postgresql` rather than `/var/lib/postgresql/data` unless the image documentation for that exact tag says otherwise. Treat old named volumes with the prior layout as disposable reset candidates only after confirming the environment is local/demo.

## Disposable local container stacks

- Treat Docker Compose and Podman Compose as local/development conveniences unless a production architecture explicitly approves them.
- Validate Compose files with the intended runtime and provider, because Docker Compose, `podman-compose`, Docker Desktop, and rootless Podman differ in labels, dependency cleanup, named-volume behavior, and supported mount options.
- For rootless Podman or Docker Desktop named volumes, do not assume `chown` or `chmod` will succeed on mounted volume roots. Prefer runtime-compatible initialization, document any local-only relaxed-permission flags, and keep production-shaped images non-root.
- For read-only local containers, identify all runtime write paths used by language tooling, including frontend bundler caches and generated config-loader files. Redirect those paths to explicit tmpfs mounts rather than weakening the entire filesystem.
- Cleanup instructions for disposable stacks may remove only project-labeled containers, networks, and volumes. Name the exact labels/resources and call out data loss before removing database or object volumes.

## Gherkin testing

- Express integration and regression behavior in Gherkin using business- or operator-visible outcomes.
- Keep scenarios deterministic, independent, tagged by capability/risk, and traceable to requirements or defects.
- Avoid coupling feature text to UI selectors, internal function names, or incidental implementation details.
- Include negative, authorization, failure, recovery, upgrade, rollback, and tenant/isolation scenarios when applicable.
- Treat skipped, quarantined, or flaky scenarios as explicit findings with owners and expiry dates.

## GitLab VCS and CI/CD

- Deliver changes through GitLab merge requests against protected branches.
- Use GitLab CI/CD with least-privilege job tokens, protected variables/environments, isolated runner trust tiers, and short-lived infrastructure credentials.
- Prevent untrusted merge-request pipelines from accessing protected variables, privileged runners, or deployment identities.
- Pin included pipeline definitions, container images, and third-party tooling to reviewed immutable versions.
- Build once and promote the same immutable artifact through environments. Preserve pipeline, job, artifact, approval, and deployment evidence.
