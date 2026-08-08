# Cadre

[![validate](https://github.com/deagy/cadre/actions/workflows/validate.yml/badge.svg)](https://github.com/deagy/cadre/actions/workflows/validate.yml)

A secure cloud agent suite for teams building self-hosted infrastructure and applications with Proxmox, Talos, Kubernetes, Helm, OpenTofu, GitLab CI/CD, Go, PostgreSQL, React, TypeScript, Python where useful, and Gherkin-based integration/regression testing.

The suite selects, coordinates, tests, reviews, documents, supports, and escalates work across specialized roles. Agents may prepare scoped repository changes and evidence; human approval is still required for production, persistent infrastructure, destructive actions, policy exceptions, privileged access, and risk acceptance.

## Repository layout

```text
.
├── AGENTS.md                 # Repository-wide contributor and safety rules
├── bin/cadre                 # CLI dispatcher for every Python tool below (bin/cadre.ps1 for PowerShell)
├── roster/                   # Agent roles, policies, workflows, orchestration, support, tests
├── .agents/skills/           # Publishable skills for this repository (Codex CLI; pointed to from .claude/skills/)
├── .claude/skills/           # Thin pointers to .agents/skills/* for Claude Code discovery
├── .clinerules/              # Pointer to AGENTS.md/RUNBOOK.md for Cline CLI discovery
├── provider/                 # Agentic SDLC provider bundle (contributed to the kernel; copied into the plugin)
├── packaging/                # Register-owned source for the packaged plugin's own README
├── .github/workflows/        # GitHub Actions: validate.yml (tests, bin/cadre smoke test, pip package, secret scan)
├── docs/                     # Audience-oriented guides and human-readable role index
├── IDENTITY.md               # Informational suite identity; never an authority source
├── CONTRIBUTING.md           # GitHub contribution and review workflow
├── CHANGELOG.md              # Consumer-visible changes to what this suite ships
└── README.md                 # This overview
```

## Choose your path

| Goal | Start here |
| --- | --- |
| Understand the suite | [IDENTITY.md](IDENTITY.md), then [documentation index](docs/README.md) |
| Adopt this suite in a new project, start to finish | [Adopt-Cadre quickstart](docs/adopt-cadre-quickstart.md) |
| Use the suite from a checkout | [Getting started](docs/getting-started.md) |
| Select and coordinate agents | [Orchestration guide](docs/orchestration.md) |
| Set up lifecycle gates conversationally (non-engineers) | `lifecycle-onboarding` skill — ask an agent to run it |
| Approve/reject/request changes on a lifecycle gate conversationally | `lifecycle-review` skill — ask an agent to run it |
| Set up lifecycle gates in a target project (direct CLI) | [Lifecycle and plugin operations](docs/lifecycle-and-plugin-operations.md) |
| Find the right specialist | [Role index](docs/role-index.md), or ask an agent to run the `role-discovery` skill for a guided conversation |
| See what changed recently | [CHANGELOG.md](CHANGELOG.md) |
| Contribute here | [CONTRIBUTING.md](CONTRIBUTING.md) |
| Operate the full system | [roster/RUNBOOK.md](roster/RUNBOOK.md) |

Key areas:

- [bin/cadre](bin/cadre) dispatches the suite tools (`cadre select`, `cadre selection-telemetry`, `cadre knowledge`, `cadre sdlc`, `cadre generate-plugin`, `cadre generate-authority-aides`, `cadre generate-role-metadata`, `cadre bootstrap-codex`, `cadre resolve-shared`, `cadre mcp-dispatch-server`, `cadre profile`, and `cadre init`). `cadre select` works standalone by default and optionally enriches its plan when the standalone `agentic-sdlc` CLI is also available — see [RUNBOOK.md §2 "Select agents locally"](roster/RUNBOOK.md#select-agents-locally) for the standalone-vs-integrated behavior and `--require-sdlc`. That separate `agentic-sdlc` CLI always provides lifecycle *validation*; this suite never does.
- [roster/catalog.yaml](roster/catalog.yaml) is the machine-readable role inventory.
- [roster/RUNBOOK.md](roster/RUNBOOK.md) explains how to select, dispatch, review, and escalate agent work.
- [roster/orchestration/](roster/orchestration/) contains routing rules, lifecycle applicability mappings, handoff contracts, escalation policy, selectors, and tests.
- [roster/shared/](roster/shared/) contains operating principles, autonomy policy, technology standards, library standards, knowledge-store rules, and risk guidance — these are global defaults; a project can extend or override them per-project the same way it can isolate its own knowledge store below, see [roster/shared/README.md](roster/shared/README.md).
- [roster/workflows/](roster/workflows/) defines workflows for new services, infrastructure, CI/CD, releases, rollback, knowledge ingestion, and support escalation.
- [roster/knowledge-store/](roster/knowledge-store/) contains the retrieval layer for approved historical context.
- [roster/testing/](roster/testing/) and [roster/support/](roster/support/) define black-box testing, end-user testing, support triage, and escalation roles.
- [.agents/skills/](.agents/skills/) contains this repository's skills, packaged for Codex CLI directly and pointed to from `.claude/skills/` for Claude Code.
- [`kernel/`](kernel/) owns the portable lifecycle kernel, initializer, validator, and lifecycle skills — a separately versioned, separately released pip distribution (see "Releasing" below), even though its source now lives in this repository. It was `deagy/agentic-sdlc` before the monorepo merge.
- [`plugin/`](plugin/) packages this suite, its 74 roles, and the `secure-cloud` provider profile as installable Claude Code / Codex plugins, alongside the optional Agentic SDLC lifecycle-governance plugins. It was `deagy/cadre-lifecycle` before the monorepo merge.
- [`cline-plugins/`](cline-plugins/) holds the three Cline CLI plugins and the single npm workspace that backs them — the only Node code in this repository. It is deliberately *not* inside `plugin/`: an npm workspace root there made installing the Claude Code plugin run `npm install` and write 263 MB of Cline SDK dependencies into every user's plugin cache.

The boundary is intentional: Agentic SDLC owns lifecycle state, schemas, gate
transitions, approval-source policy, and portable commands. This repository
owns the Secure Cloud role catalog, role policies, workflows, knowledge store,
and the `secure-cloud` provider. That schema/validator/gate-authority ownership
never moves into this repository, for any project. A consuming target project
records its own decisions and run state under `.agentic-sdlc/`; installing or
upgrading a plugin does not grant approval or rewrite those records. This
repository does not run its own `.agentic-sdlc/` overlay.

## Supported runners

Every role definition and orchestration tool is runner-neutral text and data. Lifecycle contracts and runner adapters are versioned by the Agentic SDLC kernel, released independently from [`kernel/`](kernel/) (see "Releasing" below).

| Runner | Support | Notes |
| --- | --- | --- |
| Codex CLI | Generated wrapper, packaged in the Cadre plugin | See [`docs/INSTALL.md`](docs/INSTALL.md#codex-cli). |
| Claude Code | Generated wrapper, packaged in the Cadre plugin | See [`docs/INSTALL.md`](docs/INSTALL.md#claude-code). |
| [Cline](https://docs.cline.bot) | Native `AGENTS.md` support, plus an installable CLI plugin | [Reads `AGENTS.md` natively](https://docs.cline.bot/customization/cline-rules); this repository also provides `.clinerules/agents-repository.md`, pointing at the same canonical `AGENTS.md`/`roster/RUNBOOK.md` sources — works for any Cline session with this repository as its working directory, no install required. Separately, [`cline-plugins/cline/`](cline-plugins/cline/) is a real, hand-authored (not generated) installable Cline CLI plugin (`cline plugin install ./cline-plugins/cline` from a checkout) exposing an `agents_select` tool that wraps `cadre select`. Applies to the Cline CLI, SDK, and Kanban only — not the VSCode/JetBrains extension. |

<details>
<summary>Known Cline plugin limitations</summary>

**Tool invocation fails with a cyclic-structure error.** As of `cline` CLI
`3.0.46` (the latest published version at the time this was built), invoking
any locally-installed plugin's tool fails with `JSON.stringify cannot
serialize cyclic structures` — confirmed as an upstream Cline bug, not
specific to this plugin, by reproducing the identical failure with
`cline/cline`'s own unmodified example plugin. The plugin installs and
uninstalls cleanly; tool invocation should start working once Cline ships a
fix.

**Git-URL install loads all three Cline plugins.** Cline's Git source format
cannot select a subdirectory, so this repository's root `package.json`
explicitly declares the `cline`, `cline-agents`, and `cline-lifecycle`
entrypoints. It also carries their two plugin-owned runtime dependencies
(`zod` and `yaml`) without moving the full Cline npm workspace beneath
[`plugin/`](plugin/), which would make Claude Code/Codex marketplace
installs download dependencies they never use. The entrypoints' test files
use `.mts`, so Cline's TypeScript scanner does not load the `vitest`
development dependency during installation.

</details>

## Quick start

Read [AGENTS.md](AGENTS.md) first, then use the [getting-started guide](docs/getting-started.md). `bin/cadre` resolves a Python 3.10+ interpreter for you (checks `python3`/`python`; `.\bin\cadre.ps1` also checks `py -3` in PowerShell) — see "Put `cadre` on `PATH`" to put it on `PATH`, or run it as `./bin/cadre` (`.\bin\cadre.ps1` in PowerShell) from the repository root. Then validate the suite-only component, the orchestration tools, and the in-tree lifecycle kernel:

```sh
python3 -m unittest discover -s roster/knowledge-store/test -p "test_*.py"
python3 -m unittest discover -s roster/orchestration/test -p "test_*.py"
python3 -B -m unittest discover -s kernel/test -p "test_*.py"
```

The kernel is in-tree, so `cadre sdlc` and the lifecycle-contract tests above
need no separate install and no `AGENTIC_SDLC_BIN` — set that env var only to
point at a *different* kernel deliberately.

Generate a reviewable dispatch plan:

```sh
cadre select \
  --task "Review a React and Go upload feature" \
  --files frontend/src/App.tsx,services/internal/api/api.go \
  --classification internal \
  --task-id EXAMPLE-1
```

The selector emits a plan only. It does not run agents, retrieve knowledge, deploy, mutate infrastructure, merge, push, or approve anything.

## Agentic SDLC quick start

Install the reusable G1-G10 lifecycle kernel, then use this suite's
compatibility command to inject the Cadre provider. The kernel's source lives
in this same repository, under [`kernel/`](kernel/), but it is still released
independently — pin to a reviewed `kernel-v*` tag in automation, since `main`
is fine for exploration but not an immutable dependency. Check
[this repository's releases](https://github.com/deagy/cadre/releases)
(filter for `kernel-v*`) for the current tag rather than hardcoding one here,
since this section goes stale otherwise.

The kernel is a real pip/pipx-installable distribution (puts `agentic-sdlc`
directly on `PATH`, no repository checkout needed at runtime). See
[`kernel/README.md`](kernel/README.md) for the exact `pipx install` command;
duplicating that command here would just go stale again.

For development against an unreleased change, or if you already have this
repository checked out, run from the checkout instead — no separate clone
needed:

```sh
pipx install ./kernel
```

Put the resulting `agentic-sdlc` executable on `PATH` (pipx does this by
default), or set `AGENTIC_SDLC_BIN=/path/to/agentic-sdlc`.

Either way, once `agentic-sdlc` resolves on `PATH` (or via `AGENTIC_SDLC_BIN`),
run `cadre sdlc init --root /path/to/target`.

This defaults to the low-ceremony `quick` profile and generates subagent wrappers for both runners (`init --runner {codex,claude,both}`).

If the target project actually uses this repository's own cloud stack (Proxmox, Talos, Kubernetes, Helm, OpenTofu, GitLab CI, PostgreSQL), use `--profile secure-cloud` instead of the default. This is the **recommended** way to get this repository's 74 roles into a project: scoped to that one project, generated once as static files the project owns from then on (no live link back to this checkout, so a later role edit here doesn't silently change that project's behavior):

```sh
cadre sdlc init --root /path/to/target --profile secure-cloud
```

A project with a different stack should stay on `quick`/`generic`/`web-service` — `secure-cloud` extends `generic` with 16 roles opinionated toward this repository's own infrastructure, and installing it onto an unrelated stack forces subagents shaped around infrastructure that project doesn't have.

Initialization detects candidate technologies and validation commands, but deliberately leaves human authorities, compliance applicability, persistent/production environment classification, and other consequential decisions unresolved. The target project owns those decisions and its lifecycle records under `.agentic-sdlc/`.

See [`kernel/README.md`](kernel/README.md) for commands and upgrades.

### No A2A surface today

This suite has no A2A surface today. A2A was evaluated as a Codex-dispatch
mechanism and deferred (not in progress), pending a confirmed second consumer
that isn't Codex CLI and pending A2A protocol conformance/auth maturity. If
this suite ever adopts A2A, it will be a standalone, standards-compliant layer
owned by this repo — not built on Agentic SDLC's SDLC-task-bound A2A
implementation — and it will carry no lifecycle authority over any other
project's gates, per the boundary described in [AGENTS.md](AGENTS.md). The
identified fix path for the underlying Codex-dispatch limitation is a Python
MCP server, owned by this repo, currently in development.

### GitHub review-backed approvals

Projects can make an approved GitHub pull-request review the authoritative
source for human gate decisions (`approval_sources.human_gate_default:
"github-review"`, `cadre sdlc approve-from-github-pr`, fails closed without
authenticated `gh` access or a matching review). See [Lifecycle and plugin
operations §GitHub-backed human
approvals](docs/lifecycle-and-plugin-operations.md#github-backed-human-approvals)
for the full setup and command reference, or [RUNBOOK.md
§18](roster/RUNBOOK.md#18-record-a-github-backed-human-gate-approval) for the
two supported recording paths and the evidence-URI format.

## Advanced: install every role globally

Most projects want the per-project `--profile secure-cloud` path above instead
of this section — it avoids forcing this repository's cloud-specific roles
onto projects with a different stack, and each project's generated wrappers
are static files it owns, not a live link back to this checkout. This section
is for the narrower case of genuinely wanting all 74 roles, the 12 skills, and
the knowledge store reachable from *every* project on the machine
unconditionally.

```text
/plugin marketplace add deagy/cadre
/plugin install cadre@cadre-team
```

**[`docs/INSTALL.md`](docs/INSTALL.md) is the canonical install guide** —
Codex, Cline, the one-command install script, pinning, and the optional
lifecycle plugins are all covered there. This section is a pointer, not a
second copy: three documents quoting three different stale version tags is
exactly what one canonical page exists to prevent.

For a fleet, see [`docs/enterprise.md`](docs/enterprise.md).

The first `run-agent-orchestration` or `knowledge-ingestion` invocation with no
knowledge-store config anywhere asks whether to create an isolated project-local
one or use this shared global one — it does not create the global one silently.
See [roster/RUNBOOK.md](roster/RUNBOOK.md)
for how namespaced Codex subagent wrappers get into `~/.codex/agents/` without
overwriting bare project/global roles or unowned namespaced files (Codex has no
plugin-bundled-agent mechanism) and for how to regenerate after adding a role.

## Put `cadre` on `PATH`

Optional, and useful regardless of which path above you took: put `bin/cadre`
on `PATH` so the `cadre` command in this README and `roster/RUNBOOK.md` works
from any directory, not just this checkout (an orchestrating Claude Code agent
doesn't need this — the installed plugins already put `bin/cadre` on the Bash
tool's PATH for it). Symlink it (a copy would break its reach-back into this
repository) into a directory already on `PATH`, e.g.:

```sh
mkdir -p ~/.local/bin
ln -s "$(pwd)/bin/cadre" ~/.local/bin/cadre   # ensure ~/.local/bin is on PATH
```

PowerShell has no bare-name script execution by default; wrap `bin/cadre.ps1`
in a `$PROFILE` function instead:

```powershell
function cadre { & "C:\path\to\this\checkout\bin\cadre.ps1" @args }
```

## pip / pipx install (additional distribution channel)

This is a second, independent way to run the `cadre` CLI, alongside the
checkout path above (`./bin/cadre` / `bin/cadre.py` / `bin/cadre.ps1`) — it
does not replace it, and the checkout path keeps working unmodified whether
or not you ever build or install this package. `pyproject.toml` at the
repository root packages the CLI (subcommand table, dispatch logic, and
every resource each subcommand reads: `roster/`, `.agents/skills/`, and
`provider/` for `cadre sdlc`/`cadre bootstrap-codex`) as an installable
`cadre` distribution, runnable from any directory on a machine that has
never cloned this repository.

Build and install a local wheel:

```sh
python3 -m pip install --upgrade build
python3 -m build                      # produces dist/cadre-*.whl (and an sdist)
pipx install dist/cadre-*.whl         # or: pip install dist/cadre-*.whl
```

This puts a `cadre` console script directly on `PATH`. It is not published to
PyPI and there is no publish automation for it — install only from a local
build or a built artifact you trust.

`cadre resolve-shared` / `cadre init` can optionally parse YAML shared-config
overlays (JSON-only overlays work without this), and `cadre
mcp-dispatch-server` requires the official MCP SDK — both are optional
extras, so installing the built wheel without them stays dependency-light
(note: an unrelated third-party project already owns the name `cadre` on
PyPI — always install from your own `dist/` build, never from PyPI):

```sh
pipx install "dist/cadre-*.whl[yaml]"        # or [mcp], or [yaml,mcp]
```

`cadre sdlc` still shells out to a separately installed `agentic-sdlc`
binary (`AGENTIC_SDLC_BIN` or `PATH`) exactly as the checkout CLI does — this
package never vendors or bundles the Agentic SDLC kernel; see "Agentic SDLC
quick start" above for installing it.

**Known limitation**: `cadre generate-plugin` and `cadre
generate-authority-aides` are maintainer-only tools that require a full git
checkout; they are not available from a pip/pipx install. Both read tracked
source through this repository's own git index and write generated content
back to a real checkout (the `plugin/` directory named by `--output` for
`generate-plugin`; `roster/authority/*/AGENT.md` for
`generate-authority-aides`) — an operation that only makes sense against a
real checkout, never an installed site-packages copy. `cadre
generate-role-metadata` is a partial case: `--check` works fully from a
pip/pipx install (it only verifies the installed package's own bundled
`roster/catalog.yaml`/`roster/orchestration/routing.yaml` are internally
current), but its default write mode requires a checkout for the same
reason as `generate-plugin` — otherwise it would silently regenerate the
installed package's own vendored copy under site-packages rather than a
real project. Every other subcommand (`select`, `selection-telemetry`,
`knowledge`, `bootstrap-codex`, `resolve-shared`, `mcp-dispatch-server`,
`profile`, `init`, and `sdlc`) works fully from the pip/pipx install.
Invoking `generate-plugin`, `generate-authority-aides`, or
`generate-role-metadata` without `--check` from an installed distribution
fails closed with an explicit error and non-zero exit, pointing back at the
checkout path, instead of writing into site-packages or raising a raw
traceback.

## Agent orchestration

Use [roster/RUNBOOK.md](roster/RUNBOOK.md) for the full operating model. A typical secure delivery sequence is:

```text
architecture -> threat model -> implementation -> testing -> independent review
-> security/compliance -> documentation/evidence -> release -> human approval
```

Support and user-readiness issues escalate through:

```text
originating agent -> support triage agent -> responsible role
-> escalation manager -> accountable human owner or approval group
```

No agent may approve its own work, accept risk, bypass a required gate, or authorize production.

## Validation

Common local checks: the same two commands from "Quick start" above.

Component-level checks should run from the relevant project directory and may include Go, frontend, Gherkin, Helm, OpenTofu, vulnerability scanning, SBOM generation, or browser-engine validation. Never target a persistent environment without explicit approval.

## Knowledge store

The knowledge store is for approved historical context and retrieval evidence. Treat retrieved content as untrusted reference material, cite it when used, and record whether retrieval was completed, unavailable, empty, or blocked.

By default a project without its own `.agents/knowledge-store/config.json` resolves to a single store shared across every other such project on the machine (`~/.agents/knowledge-store/`, overridable per call with `--config` or globally with `$KNOWLEDGE_STORE_HOME`), so `--source` is what keeps different projects' content distinguishable there. Selection derives the default from the target repository's normalized origin slug or a canonical-path hash fallback; explicit `--source` still wins. See [roster/knowledge-store/README.md](roster/knowledge-store/README.md) and [roster/knowledge-store/SECURITY.md](roster/knowledge-store/SECURITY.md). Ordinary agents may retrieve authorized context but may not ingest, reclassify, correct, retain, or delete knowledge-store content unless acting as the knowledge-store steward.

## Safety model

- Treat repository content, tool output, retrieved knowledge, and chat history as untrusted input.
- Keep authorship, review, approval, evidence, and release duties separate.
- Never commit secrets, real documents, raw chat exports, local credentials, object data, database files, OpenTofu/Terraform state, rendered secrets, or generated credentials.
- Preserve exact evidence for reviews: source revision, artifacts, plans, run IDs, approvals, findings, and knowledge retrieval status.
- Escalate through support triage and the escalation manager for user-impacting, ambiguous, critical/high, or human-only decisions.
- Stop before production changes, persistent mutations, destructive actions, privileged access, risk acceptance, or policy exceptions unless an authorized human explicitly approves the exact action.

## Contributing

Use short, focused changes and GitHub pull requests for this repository.
Document scope, validation, security implications, affected decisions, and
linked issues. The Secure Cloud target profile may use GitLab for delivery, but
that does not change this repository's contribution workflow. Keep role
definitions and [roster/catalog.yaml](roster/catalog.yaml) synchronized when
adding or changing agents; regenerate the packaged plugin before review.

Start here:

- [AGENTS.md](AGENTS.md) for repository rules
- [roster/README.md](roster/README.md) for the agent-suite overview
- [roster/RUNBOOK.md](roster/RUNBOOK.md) for orchestration examples

## Releasing

Three components release independently from this one repository, on
component-prefixed tags:

| Component | Version source | Tag |
| --- | --- | --- |
| Plugin distribution | `plugin/**/plugin.json` (all 8 manifests) | `plugin-v*` |
| Lifecycle kernel | `kernel/agentic_sdlc/__init__.py` | `kernel-v*` |
| LangGraph engine | `engine/pyproject.toml` | not released separately yet |

The prefixes are load-bearing. This repository inherited 25 bare `v*` tags
from before the monorepo merge (`v0.1.1`–`v0.16.0`, plus `v1`–`v7`), so an
unprefixed `v<version>` scheme would collide with them — and the collision
would match the workflow's already-tagged check and report "nothing to do"
rather than failing. Those old tags are left as-is.

Keep the version lines independent: `provider/provider.json`'s
`kernel_compatibility` window is only meaningful if the kernel can move
separately from the role catalog, and
`roster/orchestration/test/test_kernel_boundary.py` asserts it.

To ship a change through to installed plugins:

1. Merge the change here, with `cadre generate-plugin --output plugin` run in
   the same pull request — the `generated-content` CI job fails otherwise.
2. Bump the plugin version when it should reach existing installs:
   `python3 plugin/tools/plugin_version.py --set X.Y.Z`

Once that lands on `main`, [`release.yml`](.github/workflows/release.yml)
tags and publishes automatically — no manual `git tag`. The kernel job does
the same for `kernel/`, and additionally attaches a wheel, an sdist, and
`SHA256SUMS`, which is what lets `bootstrap_sdlc.py` verify what it installs.
Both jobs are idempotent and only ever tag reviewed, merged content.

**Merging a version-bump PR is the release approval.** The workflow itself
runs unattended and asks no further confirmation, so review of that PR is
where a human deliberately authorizes the release — treat a `version` bump
in a PR's diff as an explicit release request, not an incidental change, and
review it accordingly.

## Examples

See [docs/examples/](docs/examples/) for end-to-end workflow documentation:

- [Role selection workflow](docs/examples/role-selection-workflow.md) — from task to dispatched agents
