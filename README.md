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
| Install Cadre | [Installation](#installation) section above |
| Understand the suite | [IDENTITY.md](IDENTITY.md), then [documentation index](docs/README.md) |
| Adopt this suite in a new project, start to finish | [Adopt-Cadre quickstart](docs/adopt-cadre-quickstart.md) |
| Work from a checkout | [Getting started](docs/getting-started.md) |
| Select and coordinate agents | [Orchestration guide](docs/orchestration.md) |
| Set up lifecycle gates conversationally (non-engineers) | `lifecycle-onboarding` skill — ask an agent to run it |
| Approve/reject/request changes on a lifecycle gate conversationally | `lifecycle-review` skill — ask an agent to run it |
| Set up lifecycle gates in a target project (direct CLI) | [Lifecycle and plugin operations](docs/lifecycle-and-plugin-operations.md) |
| Find the right specialist | [Role index](docs/role-index.md), or ask an agent to run the `role-discovery` skill for a guided conversation |
| See what changed recently | [CHANGELOG.md](CHANGELOG.md) |
| Contribute here | [CONTRIBUTING.md](CONTRIBUTING.md) |
| Operate the full system | [roster/RUNBOOK.md](roster/RUNBOOK.md) |

Key areas:

- [bin/cadre](bin/cadre) dispatches the suite tools (`cadre select`, `cadre selection-telemetry`, `cadre knowledge`, `cadre sdlc`, `cadre generate-plugin`, `cadre generate-authority-aides`, `cadre generate-role-metadata`, `cadre bootstrap-codex`, `cadre resolve-shared`, `cadre mcp-dispatch-server`, `cadre profile`, `cadre init`, `cadre gitlab-evidence`, `cadre config`, and `cadre doctor`). `cadre select` works standalone by default and optionally enriches its plan when the standalone `agentic-sdlc` CLI is also available — see [RUNBOOK.md §2 "Select agents locally"](roster/RUNBOOK.md#select-agents-locally) for the standalone-vs-integrated behavior and `--require-sdlc`. That separate `agentic-sdlc` CLI always provides lifecycle *validation*; this suite never does. `cadre doctor` reports which `cadre` binary actually ran (checkout, pip/pipx install, or Claude Code plugin-cache copy) and warns when the cwd sits inside a checkout but a different install answered the command.
- [roster/catalog.yaml](roster/catalog.yaml) is the machine-readable role inventory.
- [roster/RUNBOOK.md](roster/RUNBOOK.md) explains how to select, dispatch, review, and escalate agent work.
- [roster/orchestration/](roster/orchestration/) contains routing rules, lifecycle applicability mappings, handoff contracts, escalation policy, selectors, and tests.
- [roster/shared/](roster/shared/) contains operating principles, autonomy policy, technology standards, library standards, knowledge-store rules, and risk guidance — these are global defaults; a project can extend or override them per-project the same way it can isolate its own knowledge store below, see [roster/shared/README.md](roster/shared/README.md).
- [roster/workflows/](roster/workflows/) defines workflows for new services, infrastructure, CI/CD, releases, rollback, knowledge ingestion, and support escalation.
- [roster/knowledge-store/](roster/knowledge-store/) contains the retrieval layer for approved historical context.
- [roster/testing/](roster/testing/) and [roster/support/](roster/support/) define black-box testing, end-user testing, support triage, and escalation roles.
- [.agents/skills/](.agents/skills/) contains this repository's skills, packaged for Codex CLI directly and pointed to from `.claude/skills/` for Claude Code.
- [`kernel/`](kernel/) owns the portable lifecycle kernel, initializer, validator, and lifecycle skills — a separately versioned, separately released pip distribution (see "Releasing" below), even though its source now lives in this repository. It was `deagy/agentic-sdlc` before the monorepo merge.
- [`plugin/`](plugin/) packages this suite, its 159 roles and 20 non-authoring context packs, and the `secure-cloud` provider profile as installable Claude Code / Codex plugins, alongside the optional Agentic SDLC lifecycle-governance plugins. It was `deagy/cadre-lifecycle` before the monorepo merge.
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
| Codex CLI | Generated wrapper, packaged in the Cadre plugin | See [Codex CLI section](#codex-cli) in Installation. |
| Claude Code | Generated wrapper, packaged in the Cadre plugin | See [Claude Code section](#claude-code) in Installation. |
| [Cline](https://docs.cline.bot) | Native `AGENTS.md` support, plus an installable CLI plugin | [Reads `AGENTS.md` natively](https://docs.cline.bot/customization/cline-rules); this repository also provides `.clinerules/agents-repository.md`, pointing at the same canonical `AGENTS.md`/`roster/RUNBOOK.md` sources — works for any Cline session with this repository as its working directory, no install required. Separately, [`cline-plugins/`](cline-plugins/) holds three real, hand-authored (not generated) installable Cline CLI plugins. See [Cline CLI section](#cline-cli) in Installation. Applies to the Cline CLI, SDK, and Kanban only — not the VSCode/JetBrains extension. |

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
python3 -m unittest discover -b -s roster/knowledge-store/test -p "test_*.py"
python3 -m unittest discover -b -s roster/orchestration/test -p "test_*.py"
python3 -B -m unittest discover -b -s kernel/test -p "test_*.py"
```

The kernel is in-tree, so `cadre sdlc` and the kernel's own tests above need
no separate install and no `AGENTIC_SDLC_BIN`. The orchestration tests also
run with neither, but resolve the lifecycle contract by looking for an
`agentic-sdlc` executable on `PATH`, so without one they run in *standalone*
mode; CI sets `AGENTIC_SDLC_BIN` to this repository's own `bin/agentic-sdlc`
to exercise the integrated paths, and you can too. Point the variable
somewhere else only to use a *different* kernel deliberately.

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

If the target project actually uses this repository's own cloud stack (Proxmox, Talos, Kubernetes, Helm, OpenTofu, GitLab CI, PostgreSQL), use `--profile secure-cloud` instead of the default. This is the **recommended** way to get this repository's 159 roles into a project: scoped to that one project, generated once as static files the project owns from then on (no live link back to this checkout, so a later role edit here doesn't silently change that project's behavior):

```sh
cadre sdlc init --root /path/to/target --profile secure-cloud
```

A project with a different stack should stay on `quick`/`generic`/`web-service` — `secure-cloud` extends `generic` with 19 roles opinionated toward this repository's own infrastructure, and installing it onto an unrelated stack forces subagents shaped around infrastructure that project doesn't have.

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

## Installation

Pick your use case from the table, then see the detailed section below.

| You are | Do this |
| --- | --- |
| Claude Code user | `/plugin marketplace add deagy/cadre` → `/plugin install cadre@cadre-team` |
| Any runner (fast) | `curl -fsSL https://raw.githubusercontent.com/deagy/cadre/main/install.sh \| sh` |
| Working on Cadre itself | `git clone https://github.com/deagy/cadre.git` → `./bin/cadre select ...` |
| Rolling out to a fleet | See [Enterprise deployment](#enterprise-deployment) section below |
| Codex CLI | `codex plugin marketplace add deagy/cadre` → `codex plugin add cadre@cadre-team` |
| Cline CLI | `cline plugin install https://github.com/deagy/cadre --force` |

Lifecycle governance (G1–G10 gates) is **optional** and most projects don't need it. Nothing above installs it. See [Adding lifecycle governance](#adding-lifecycle-governance) if you want it.

### Claude Code

```text
/plugin marketplace add deagy/cadre
/plugin install cadre@cadre-team
```

Deliberately unpinned — the version comes from the plugin's manifest, and releases only tag `main` when every plugin manifest agrees, so the marketplace ref needs no tag. Use `/plugin update` to move forward. If your policy requires a pinned source, append `@<tag>` from [releases](https://github.com/deagy/cadre/releases).

Set `CLAUDE_CODE_PLUGIN_PREFER_HTTPS=1` if your policy requires HTTPS over SSH.

### One-command install (all runners)

```sh
curl -fsSL https://raw.githubusercontent.com/deagy/cadre/main/install.sh | sh
```

On Windows, use [`install.ps1`](https://github.com/deagy/cadre/blob/main/install.ps1) instead (or via `PowerShell -ExecutionPolicy Bypass -File install.ps1`).

Flags:

```sh
./install.sh --dry-run              # print actions, change nothing
./install.sh --runner=codex         # just one runner (codex, claude, or cline)
./install.sh --with-lifecycle       # include optional G1-G10 governance
./install.sh --uninstall            # remove everything
```

Touches only: `~/.cadre/dist`, `~/.local/bin/cadre`, `~/.codex/config.toml` (MCP entry), and each runner's plugin store. Safe to re-run (updates in place).

### From a checkout

For working on Cadre itself, or running without installing:

```sh
git clone https://github.com/deagy/cadre.git && cd cadre
./bin/cadre select --task "..." --files file.go --task-id T-1
```

On PowerShell, use `.\bin\cadre.ps1`. To put `cadre` on `PATH`:

```sh
ln -s "$PWD/bin/cadre" ~/.local/bin/cadre      # on POSIX
# or on PowerShell:
function cadre { & "C:\path\to\checkout\bin\cadre.ps1" @args }
```

The in-tree kernel means `cadre sdlc` works immediately with no separate install.

### Codex CLI

The install script covers this, or directly:

```sh
codex plugin marketplace add deagy/cadre
codex plugin marketplace upgrade cadre-team
codex plugin add cadre@cadre-team
cadre bootstrap-codex                 # Write agent wrappers to ~/.codex/agents/
```

For mid-session dispatch, add MCP to `~/.codex/config.toml`:

```toml
[mcp_servers.cadre-dispatch]
command = "cadre"
args = ["mcp-dispatch-server"]
```

Requires `cadre` on `PATH` and the MCP extra: `pip install -r roster/orchestration/mcp/requirements-mcp.txt`.

#### Self-hosted models (llama.cpp, Ollama, vLLM)

Create `$CODEX_HOME/cadre-local.config.toml` (default `~/.codex`):

```toml
model = "qwen3-coder-30b"
model_provider = "llamacpp"

[model_providers.llamacpp]
name = "llama.cpp"
base_url = "http://<host>:8080/v1"
wire_api = "chat"
```

Then point Cadre at it:

```sh
export SECURE_CLOUD_AGENTS_CODEX_PROFILE=cadre-local
export SECURE_CLOUD_AGENTS_LOCAL_MODEL_OPUS=qwen3-coder-30b
export SECURE_CLOUD_AGENTS_LOCAL_MODEL_SONNET=qwen3-coder-30b
export SECURE_CLOUD_AGENTS_LOCAL_MODEL_HAIKU=qwen3-4b
```

To dispatch with no coding CLI installed at all, use `runner="api"` and point at an endpoint:

```sh
export SECURE_CLOUD_AGENTS_API_BASE_URL=http://<host>:8080/v1
```

See `roster/orchestration/SECURITY-CONTROLS.md` before enabling `SECURE_CLOUD_AGENTS_API_ALLOW_WRITES` or `SECURE_CLOUD_AGENTS_API_COMMAND_ALLOWLIST`.

### Cline CLI

```sh
cline plugin install https://github.com/deagy/cadre --force
```

Installs three plugins together (`cline`, `cline-agents`, `cline-lifecycle`). Local development: `cline plugin install ./cadre --force` from a checkout after running `npm ci` in `cline-plugins/`.

**Known upstream issue**: as of Cline 3.0.46, invoking any locally-installed plugin's tool fails with a cyclic-structure error (also affects Cline's own example plugin). Expected to resolve when Cline ships a fix.

### pip / pipx install

For running the CLI from anywhere on a machine that's never cloned this repository:

```sh
python3 -m build                      # produces dist/cadre-*.whl
pipx install dist/cadre-*.whl         # puts cadre on PATH
```

Puts a `cadre` console script on `PATH`. Not published to PyPI (an unrelated project owns that name). Always install from your own `dist/` build.

Optional extras:

```sh
pipx install "dist/cadre-*.whl[yaml]"        # or [mcp], or [yaml,mcp]
```

**Limitation**: `cadre generate-plugin` and `cadre generate-authority-aides` require a git checkout; not available from pip/pipx.

### Enterprise deployment

Deploy one managed-settings file to equip a fleet with no per-user install:

**File content:**

```json
{
  "extraKnownMarketplaces": {
    "cadre-team": {
      "source": { "source": "github", "repo": "deagy/cadre" },
      "autoUpdate": true
    }
  },
  "enabledPlugins": {
    "cadre@cadre-team": true
  }
}
```

**File location:**

| Platform | Path |
| --- | --- |
| macOS | `/Library/Application Support/ClaudeCode/managed-settings.json` |
| Linux / WSL | `/etc/claude-code/managed-settings.json` |
| Windows | `C:\Program Files\ClaudeCode\managed-settings.json` |

**Important**: `enabledPlugins` declares intent to *install*; users still see a prompt to accept marketplace/plugin setup. For genuinely zero-touch, pair with:

```sh
claude plugin install cadre@cadre-team --scope user
```

**Do not remove `extraKnownMarketplaces` as a pause mechanism** — it uninstalls the plugin. Instead, set `autoUpdate: false` or set `enabledPlugins: { "cadre@cadre-team": false }`.

To pre-configure lifecycle plugins (if you're deploying them):

```json
{
  "pluginConfigs": {
    "cadre-lifecycle-github@cadre-team": {
      "options": {
        "kernelInstall": "auto",
        "profile": "secure-cloud"
      }
    }
  }
}
```

`kernelInstall` options: `auto` (manages own copy), `system` (never installs), `off` (disables check).

### Adding lifecycle governance

Three optional plugins, self-sufficient — install whichever matches your approval flow:

```text
/plugin install cadre-lifecycle-core@cadre-team        # forge-agnostic
/plugin install cadre-lifecycle-github@cadre-team      # GitHub PR review source
/plugin install cadre-lifecycle-gitlab@cadre-team      # GitLab MR approval source
```

Each depends on `cadre`, so it comes along automatically. They need the Agentic SDLC kernel. At first invocation, the plugin will prompt:

```text
/cadre-install-kernel
```

This installs a copy under the plugin's data directory. It never modifies your own `AGENTIC_SDLC_BIN` or replaces a kernel on `PATH`. Deleting the plugin's data directory undoes the install.

Then set up your project:

```text
/lifecycle-onboarding
```

Use the skill — assigning the eight human authorities is a decision people make, and the skill guides through it.

### PyPI security note

**Neither `agentic-sdlc` nor `cadre` is on PyPI.** Both names belong to unrelated projects.

- `pip install agentic-sdlc` installs an unrelated third-party project.
- The PyPI `cadre` package is a placeholder from 2022.

Install only from this repository — a checkout, the marketplace, the install script, or a release artifact (verify against `SHA256SUMS`). See [SECURITY.md](SECURITY.md).

### Verify installation

```sh
cadre select --task "smoke test" --files README.md --task-id SMOKE-1
cadre sdlc --version        # only if you installed lifecycle governance
```

In Claude Code, `/plugin details cadre@cadre-team` shows what the plugin contributes and its context cost.

## Put `cadre` on `PATH` (optional shortcut)

Already done by `install.sh`. If working from a checkout, symlink it:
real checkout, never an installed site-packages copy. `cadre
generate-role-metadata` is a partial case: `--check` works fully from a
pip/pipx install (it only verifies the installed package's own bundled
`roster/catalog.yaml`/`roster/orchestration/routing.json` are internally
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
