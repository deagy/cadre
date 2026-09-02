# `plugin/` — the packaged distribution

This directory is the source for what users actually install. It is **not a
repository**: before the monorepo merge it was `deagy/cadre-lifecycle`, which
is now archived, and the old README here described that repository.

**Installing is documented in [`docs/INSTALL.md`](../docs/INSTALL.md).** Do
not add install instructions here — one canonical page is what stopped three
documents quoting three different stale version tags.

## What is generated and what is not

Most of this directory is produced by `cadre generate-plugin` from
`roster/`, `.agents/skills/`, and `provider/` at the repository root. Editing
a generated file is silently undone on the next regeneration, and the
`generated-content` CI job fails the pull request that does it.

| Generated — edit the source and regenerate | Hand-authored — edit here |
| --- | --- |
| `agents/`, `skills/`, `suite/`, `codex-agents/` | `.claude-plugin/`, `.codex-plugin/` (manifests, versions) |
| `agent-catalog.json`, `provider.json`, `profiles/`, `extensions/` | `plugins/lifecycle-github/`, `plugins/lifecycle-gitlab/` |
| `bin/cadre` | `cline/`, `cline-agents/index.ts`, `cline-lifecycle/` |
| `plugins/lifecycle/skills/` | `tools/` |
| each lifecycle plugin's `tools/`, `bin/`, `hooks/` | `README.md` (this file) |

```sh
cadre generate-plugin --output plugin      # regenerate
cadre generate-plugin --check --output plugin   # what CI runs
```

`cline-agents/agents/` and `cline-agents/skills/` are a third category: ported
from the generated content by `tools/port_cline_agents.py`, not written by
hand and not written by the generator.

## The four plugins

The marketplace manifests live at the **repository root**, not here:
`.claude-plugin/marketplace.json` for Claude Code and
`.agents/plugins/marketplace.json` for Codex. Both declare the marketplace
`cadre-team`.

| Plugin | Summary | Skills |
|---|---|---|
| **`cadre`** | Role definitions/catalog/routing, and the `agents_select` Cline tool call (never talks to the lifecycle kernel). | — |
| **`cadre-lifecycle-core`** | Forge-agnostic lifecycle governance UX: conversational wrappers around `bin/cadre sdlc` (a thin pass-through to the external kernel), plus a local-only pending-gates briefing and the kernel bootstrap script. | `lifecycle-onboarding`, `lifecycle-review`, `brief-pending-gates` |
| **`cadre-lifecycle-github`** | GitHub-flavored gate governance: PR-review-sourced decisions, G1/G2 source-issue linking, gate-status PR comments, read-only PR-reviewer reporting, gate/approval tracking issues, and an advisory (never a formal request) reviewer nudge. Self-sufficient — bundles its own onboarding/review/pending-gates skills and kernel bootstrap. Requires `agentic-sdlc` [v0.14.4](https://github.com/deagy/cadre-kernel/releases/tag/v0.14.4)+. | `lifecycle-onboarding-github`, `lifecycle-review-github`, `lifecycle-review-generic-github`, `brief-pending-gates-github`, `link-source-issue-github`, `publish-gate-status-github`, `report-gate-reviewers-github`, `create-github-gate-issues`, `publish-reviewer-nudge-github` |
| **`cadre-lifecycle-gitlab`** | GitLab-flavored gate governance: MR-approval-sourced decisions, G1/G2 source-issue linking, gate-status MR notes, read-only MR-reviewer reporting, and gate/approval tracking issues. Self-sufficient — bundles its own onboarding/review/pending-gates skills and kernel bootstrap. Requires `agentic-sdlc` [v0.14.4](https://github.com/deagy/cadre-kernel/releases/tag/v0.14.4)+. | `lifecycle-onboarding-gitlab`, `lifecycle-review-gitlab`, `lifecycle-review-generic-gitlab`, `brief-pending-gates-gitlab`, `link-source-issue-gitlab`, `publish-gate-status-gitlab`, `gitlab-gate-tracking` |

`roster/` at the repository root remains the source of truth for role definitions, and also generates `cadre-lifecycle-core`'s three skills into `plugins/lifecycle/skills/`. `cadre-lifecycle-github`/`cadre-lifecycle-gitlab` are entirely hand-authored here; `roster/` has no concept of them, including their bundled onboarding/review/pending-gates skill copies, which are hand-maintained duplicates of `cadre-lifecycle-core`'s generated skills — kept in sync by `tools/test_plugin_duplication_health.py`, which compares the copies section by section after normalizing forge-specific vocabulary and fails on any unexplained difference. Assets here are generated from `roster/` — see "Regenerating Assets" below.

Each lifecycle plugin declares a `dependencies` entry on `cadre`, so
installing one pulls it in. `cadre` itself declares none — being installable
on its own is the point of it.

## Versioning

All eight plugin manifests share one version, bumped together:

```sh
./bin/cadre plugin-version --set 0.11.0
```

Pushing that to `main` triggers the `plugin` job in `release.yml`, which tags
`plugin-v<version>` and publishes a release. The kernel has its own separate
version line and `kernel-v*` tags — see the root
[`CLAUDE.md`](../CLAUDE.md) for why they must stay independent.

## System Prompt

Each runner's plugin mechanism was investigated for a real, plugin-controlled
way to inject a standard identity sentence
(`"You are a coding assistant with access to Cadre role subagents."`) into a
session, as opposed to documentation a human has to paste in themselves:

- **Cline** has one: `AgentExtensionApi.registerRule` (the "rules"
  capability) appends registered content to the session's composed system
  prompt at runtime — confirmed against `@cline/shared`'s type declarations
  and `@cline/core`'s compiled `SessionRuntime.composeSystemPrompt()`. All
  three Cline plugins (`cline/`, `cline-agents/`, `cline-lifecycle/`) now
  register a rule via this mechanism; see each plugin's own README "System
  prompt" section for its exact registered content and why it repeats the
  base sentence per plugin rather than assuming a sibling plugin already
  registered it. This is distinct from — and additive to — a host
  application's own `systemPrompt` field on `ClineCore.create()`/
  `cline.start()`, which no Cline plugin can set itself; `cline-agents/README.md`'s
  Quick start still documents that field as the recommended host-level value
  for embedders that want to set their own framing regardless.
- **Claude Code** has no plugin-level API to inject a global system prompt
  outside its own subagent/skill bodies (which are scoped to the subagent
  they define, not the orchestrating session). The closest real equivalent
  is a project's own `CLAUDE.md` — this repository already has one at its
  root — not something the packaged plugin (`.claude-plugin/plugin.json`,
  which only declares `skills`) can ship and have applied automatically to
  every consuming project. Not implemented here for that reason; see this
  section's "Recommendations" note below.
- **Codex CLI**: `.codex-plugin/plugin.json`'s `interface.defaultPrompt`
  field (present on all four packaged plugins) is a list of suggested
  starter *user* prompts surfaced by Codex's plugin UI, not a system prompt —
  every existing value in this repository follows that "Use X to Y" pattern,
  never identity-establishing prose. `developer_instructions` in the
  generated `codex-agents/agents-*.toml` wrappers is scoped per role, not
  session-wide (see `skills/run-agent-orchestration/references/runner-adapters.md`'s
  "## Codex CLI" section). Codex CLI's own `config.toml` is user/project-level
  configuration outside a plugin's reach to set on a consumer's behalf. The
  closest real equivalent this repository can offer is the same one Claude
  Code gets: a project's own `AGENTS.md`, which Codex CLI reads natively as
  project instructions — this repository already has one at its root. Not
  implemented as a plugin-shipped config file for the same reason as Claude
  Code above.

No fabricated config knob was added for Claude Code or Codex CLI. Full
investigation notes, refined-wording recommendations, and the
install-combination question (whether the prompt should differ depending on
which lifecycle plugin(s) are also installed) are recorded in this task's
final report rather than duplicated here.

## Using the `agents_select` Tool Call

The `agents_select` tool call provides deterministic agent dispatch from the Cadre catalog. It returns a plan (routes, primary/reviewer/support roles, quality gates) without invoking agents or mutating state. See [`cline/README.md`](cline/README.md) for full detail.

```typescript
// Example tool call
agents_select({
  task: "Implement user authentication with OAuth2",
  files: "src/auth/,tests/test_auth.py",
  base: "main",
  classification: "internal"
})
```

The tool call:
- Routes the task to appropriate specialist roles
- Identifies primary authors, independent reviewers, and support roles
- Defines quality gates and approval requirements
- Never invokes agents or makes lifecycle decisions

## Lifecycle governance

See [`docs/INSTALL.md`](../docs/INSTALL.md#adding-lifecycle-governance) for
installing it and [`docs/enterprise.md`](../docs/enterprise.md) for
configuring it across a fleet.

## Tests

```sh
python3 -m unittest discover -s tools -p "test_*.py"   # from plugin/
cd cline && npm test && npm run typecheck              # and cline-agents, cline-lifecycle
```

## License

Apache-2.0. See [LICENSE](LICENSE).
