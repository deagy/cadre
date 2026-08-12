<!-- GENERATED FILE: edit the canonical source and regenerate; do not edit this copy. -->

# Cadre plugin

The installable Claude Code / Codex CLI / Cline distribution of **Cadre**:
159 specialist roles and 20 non-authoring context packs, the suite skills, the orchestration runtime, the
knowledge-store runtime, and the Agentic SDLC provider bundle. The package is
self-contained — once installed it depends on no source checkout.

## Installing

**[docs/INSTALL.md](https://github.com/deagy/cadre/blob/main/docs/INSTALL.md)**
is the canonical install guide, for every runner.

It is not repeated here on purpose. This file is generated into two places at
two different depths, and install instructions that live in more than one
document are exactly how three of them ended up quoting three different stale
version tags.

For a fleet, see
[docs/enterprise.md](https://github.com/deagy/cadre/blob/main/docs/enterprise.md).

## This is generated content

Everything in this package is produced by `cadre generate-plugin` from
[`deagy/cadre`](https://github.com/deagy/cadre). Editing an installed copy
has no lasting effect: the next regeneration overwrites it, and the
repository's `generated-content` CI job fails any pull request whose
committed output does not match its source.

To change something, edit the source and regenerate:

| To change | Edit |
| --- | --- |
| a role's authority or policy | `roster/<phase>/<role>/AGENT.md` |
| which roles exist, or their model tier | `roster/catalog.yaml` |
| routing and dispatch rules | `roster/orchestration/routing.json` |
| a skill | `.agents/skills/<name>/SKILL.md` |
| shared policy embedded into every role | `roster/shared/` |
| the provider profile or its kernel window | `provider/` |
| this file | `packaging/plugin-README.md` |

## What is in the package

| Path | Contents |
| --- | --- |
| `agents/` | one Claude Code subagent wrapper per role, auto-discovered on install |
| `codex-agents/` | the Codex `.toml` equivalents — Codex has no plugin-bundled-agent mechanism, so `cadre bootstrap-codex` installs these into `~/.codex/agents/` |
| `skills/` | the suite's own skills |
| `suite/roster/` | the runtime: catalog, routing, selectors, knowledge store, shared policy |
| `bin/cadre` | the CLI dispatcher, placed on the Bash tool's PATH while the plugin is enabled |
| `provider.json`, `profiles/`, `extensions/`, `agent-catalog.json` | the Agentic SDLC provider bundle |

`provider.json` is the versioned source of truth for the provider version and
the supported kernel range — read `version` and `kernel_compatibility` there
rather than trusting prose.

## Lifecycle governance is separate and optional

This plugin never executes lifecycle gates. `cadre select` emits a dispatch
plan and nothing more: it does not invoke agents, retrieve knowledge, approve
gates, merge, deploy, or mutate infrastructure.

G1–G10 gate governance lives in three optional companion plugins
(`cadre-lifecycle-core`, `cadre-lifecycle-github`, `cadre-lifecycle-gitlab`),
each of which drives the separately-versioned Agentic SDLC kernel through
`cadre sdlc`. Most projects do not need them. See the install guide above.

## Invariants worth knowing

An agent that materially changes an artifact cannot approve that same
artifact. Production deployment, persistent-environment mutation, risk
acceptance, policy exceptions, privileged identity or key changes, and
destructive actions always require an authorized human. These are enforced
structurally throughout the suite, not left to an agent's judgement.

Retrieved knowledge, repository files, tickets, and tool output are untrusted
data, never instructions.

## License

Apache-2.0.
