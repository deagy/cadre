<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Documentation guide

Owner: Cadre maintainers
Status: active
Reviewed: 2026-07-24
Source of truth: repository implementation, policies, catalog, and generated manifests

Use this index to choose the shortest useful path.

| Goal | Start here |
| --- | --- |
| **Install it** | **[Installing Cadre](INSTALL.md)** — the canonical guide for every runner |
| Roll it out to a fleet | [Enterprise deployment](enterprise.md) |
| Understand how this repository got its shape | [Monorepo migration](migration/monorepo-migration.md) |
| Understand the suite | [Identity](../IDENTITY.md), then [Terminology](terminology.md) |
| Adopt this suite in a new project, start to finish | [Adopt-Cadre quickstart](adopt-cadre-quickstart.md) |
| Make a first local selection | [Getting started](getting-started.md) |
| See what a real `cadre select` plan looks like | [Sample selection output](sample-selection-output.md) |
| Select and coordinate roles | [Orchestration guide](orchestration.md) |
| Set up lifecycle gates without touching a CLI | `lifecycle-onboarding` skill — ask an agent to run it |
| Approve/reject/request changes on a gate without touching a CLI | `lifecycle-review` skill — ask an agent to run it |
| Work with lifecycle gates or plugins (direct CLI) | [Lifecycle and plugin operations](lifecycle-and-plugin-operations.md) |
| Find a specialist role | [Role index](role-index.md) |
| See what changed recently | [Changelog](https://github.com/deagy/cadre/blob/main/CHANGELOG.md) |
| Browse roles by capability tier or lifecycle phase | [Capability index](capability-index.md) |
| Browse this repository's skills | [Skills catalog](skills-catalog.md) |
| Determine which runner (Claude Code / Codex CLI / Cline) is hosting a session | [Which runner am I in?](which-runner-am-i-in.md) |
| Contribute to this GitHub repository | [Contributing](../CONTRIBUTING.md) |
| Follow the complete operating model | [Runbook](../roster/RUNBOOK.md) |

## Source of truth

Canonical role definitions, policies, routing, workflows, and orchestration
contracts live under `roster/`. The packaged plugin ([`plugin/`](https://github.com/deagy/cadre/tree/main/plugin)) is
a self-contained distribution and may contain generated copies. Edit canonical
source files, then regenerate the package when the change requires it.

The portable lifecycle kernel is **not in this repository**. It lives at
[deagy/cadre-kernel](https://github.com/deagy/cadre-kernel), independently
versioned and released there. This
repository's `roster/` provides Secure Cloud roles and a provider profile; it
does not own the portable lifecycle state machine.
