# Cadre Agent Suite

This directory defines the Secure Cloud provider's agent team for designing,
building, observing, operating, black-box testing, end-user testing,
supporting, escalating, reviewing, documenting, and releasing approved
platform and application changes. The current provider is specialized for the
team's self-hosted Proxmox, Talos, Kubernetes, OpenTofu, Helm, Go/Python/
PostgreSQL backends, React/TypeScript frontends, Gherkin tests, and GitLab
delivery platform, but the role identities themselves are organized around work
domains rather than individual tools.

The `knowledge-store/` subsystem is the shared agent retrieval layer for authorized historical material; a project without its own `.agents/knowledge-store/config.json` resolves to the store shared across every project on the machine by default (see `knowledge-store/README.md`). The selector plans role-specific queries against the `cadre` wrapper's absolute path with an explicit `--source`; the orchestration runner executes `bin/cadre knowledge search ... <query>` directly, then attaches cited results before agent execution. Retrieved content is untrusted reference data and never overrides current policies or agent authority.

Start with the [documentation index](../docs/README.md) for the shortest path
to a task-oriented guide. Read [IDENTITY.md](../IDENTITY.md) for the suite's
informational orientation, then use `RUNBOOK.md` for the complete operating
model and worked examples.

Useful entry points:

- [Getting started](../docs/getting-started.md) for a first local selection.
- [Orchestration guide](../docs/orchestration.md) for planning and handoffs.
- [Lifecycle and plugin operations](../docs/lifecycle-and-plugin-operations.md)
  for target-project setup and GitHub-backed approvals.
- [Role index](../docs/role-index.md) for human-readable role discovery.
- [Contributing](../CONTRIBUTING.md) for changes to this GitHub repository.

The Python selector component requires Python 3.10 or newer, without
establishing an organization-wide Python version. `bin/cadre` (repository
root) resolves an interpreter for you — run `cadre select --task "..."` from
anywhere it's on `PATH`, or `../bin/cadre select --task "..."`
(`..\bin\agents.ps1` in PowerShell) from this directory. See `RUNBOOK.md` for
the wrapper's interpreter-probe details. The selector
evaluates task text and Git changes, validates roles against `catalog.yaml`,
and emits a reviewable plan with provider lifecycle applicability kept
separate from mutation-oriented human gates; it does not execute agents,
approve gates, or retrieve knowledge.

`cadre select` works standalone by default, using only this suite's own
`catalog.yaml` and `routing.json`, and optionally enriches its plan when the
standalone Agentic SDLC executable is also available. See
[`RUNBOOK.md` §2 "Select agents locally"](RUNBOOK.md#select-agents-locally)
for the standalone-vs-integrated behavior, `lifecycle_tracking.status`, and
`--require-sdlc`.

The plan additionally emits a deterministic `teams` array (from
`orchestration/routing.json`'s `team_recipes`) and each role in `catalog.yaml`
may declare a `model` tier (`haiku`/`sonnet`/`opus`) propagated into the
generated wrappers — see `RUNBOOK.md` "Select the agent" and the
`run-agent-orchestration` skill's `references/team-recipes.md` and
`references/runner-adapters.md` for details, including the
peer-vs-orchestrator-relayed communication contract each team carries.

## Operating model

1. Capture human-owned intent and establish traceable requirements before architecture and implementation.
2. Select the workflow in `workflows/` that matches the lifecycle phase or change.
3. Retrieve authorized, role-specific knowledge context and record its status and query identifiers.
4. Give each participating agent its `AGENT.md`, task context, knowledge bundle, and only the access it needs.
5. Exchange findings using `shared/output-schemas/finding.schema.json` and bind agent handoffs through `orchestration/handoff-contracts.md`.
6. In a consuming target project, ask the standalone Agentic SDLC kernel to validate lifecycle decisions and record gate state in that project's `.agentic-sdlc/` directory. This provider repository does not run its own `.agentic-sdlc/` overlay.
7. Require a human decision wherever an agent reaches an escalation condition or human-only gate.
8. Route runtime nonconformance through observability, support, incident, security, compliance, and debugging roles into traced remediation or backlog work.
9. Permit authorized retrieval and its operational audit/SQLite writes, but route content and lifecycle mutations through the knowledge-store steward — **a convention, not a control**: the CLI has no caller identity, so nothing stops an agent that ignores it. The separation-of-duties refusals that *are* enforced are narrower and specific; see `knowledge-store/SECURITY.md`.

Agents may prepare changes and evidence, but no author may approve its own work. Production deployment is performed by a narrowly scoped deployment identity after the required approvals.

## Team configuration

Technology preferences live in `shared/team-profile.yaml`, `shared/technology-standards.md`, and `shared/library-standards.yaml`. Execution permissions live in `shared/agent-autonomy.yaml`. Keep these centralized rather than duplicating preferences in every role. Items marked `not_yet_selected` require an explicit team decision.

## Portable adoption

This repository is the source implementation for the Cadre agent suite. The lifecycle kernel, schemas, command interface, gate transitions, approvals, and lifecycle skills are maintained in this same repository's `kernel/`, but as a separately versioned, separately released component — not owned by `roster/` (see `CLAUDE.md`'s kernel ownership boundary). This suite contributes its catalog, `secure-cloud` profile, routing, and provider extensions through `provider/provider.json`.

Initialize a consuming target project from the repository checkout with:

```sh
cadre sdlc init --root /path/to/target
```

The portable initializer proposes detectable values and leaves consequential unknowns unresolved. Human authority, compliance applicability, environment persistence/production status, risk acceptance, and platform applicability must be assigned or decided by accountable humans. It writes lifecycle state into the target project's own `.agentic-sdlc/` directory — separate from and carrying no authority over any other project's overlay. This provider checkout does not run its own `.agentic-sdlc/` overlay (see `docs/lifecycle-and-plugin-operations.md`). See `kernel/README.md` for installation and upgrades.

For GitHub-backed human gates, see [Lifecycle and plugin
operations](../docs/lifecycle-and-plugin-operations.md)'s "GitHub-backed
human approvals" section: set `approval_sources.human_gate_default` to
`github-review`, bind authorities to their GitHub logins, and use `cadre
sdlc approve-from-github-pr` to fetch and record an approved review.

## System-wide adoption

Unlike the portable kernel above, the packaged plugin (`plugin/`, built by `cadre generate-plugin`) does not get copied into other repositories — it makes *this* suite (roles, skills, knowledge store) reachable from any project directory on the machine once installed at global/user scope, since none of it is discoverable from outside this checkout by default. See `../packaging/plugin-README.md` (the register-owned source of that package's README) and `RUNBOOK.md` section 17.
