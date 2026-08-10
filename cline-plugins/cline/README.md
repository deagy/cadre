# Cline Plugin (`cadre`'s `agents_select` tool)

A distinct plugin from [`cline-agents/`](../cline-agents) (which ports the 159 Cadre
roles into dispatchable Cline agent presets). This plugin, `cline`, exposes exactly
one tool call, `agents_select`: deterministic, reviewable agent *dispatch planning*
from this repository's Cadre catalog. It never invokes agents, retrieves knowledge,
merges, deploys, or mutates infrastructure or approvals — it only returns a plan.

This plugin belongs to the `cadre` plugin (see the root [README.md](../README.md));
there is nothing forge- or lifecycle-specific to install here.

## Install

This plugin is a subdirectory of the `deagy/cadre` monorepo, so install it by
pointing Cline at that subdirectory of a checkout:

```sh
git clone https://github.com/deagy/cadre.git
cline plugin install ./cadre/cline-plugins/cline --force
```

If `agents_select` doesn't show up after installing, restart the Cline hub daemon:

```sh
cline doctor fix
```

## The `agents_select` tool

| Tool | Purpose |
|---|---|
| `agents_select` | Returns a dispatch plan (routes, primary/reviewer/support roles, quality gates) from the Cadre catalog bundled with this plugin. Plan only — never invokes agents. |

The role catalog is bundled with this plugin itself, not read from the target
workspace, so `root` may be any project — it does not need to be a checkout of
`deagy/cadre` or contain its own `catalog.yaml`.

`agents_select` has a single execution path: it shells out to `bin/cadre select`
(this repository's `cadre` CLI), the sole authoritative dispatch implementation.

### Example

```typescript
agents_select({
  task: "Implement user authentication with OAuth2",
  files: "src/auth/,tests/test_auth.py",
  base: "main",
  classification: "internal"
})
```

`task` is required; `files`, `base`, `taskId`, `classification`, and `requireSdlc`
are optional (see `AgentsSelectInputSchema` in [`index.ts`](index.ts)).

### Dispatching the plan

This plugin cannot dispatch the selected role(s) itself — a Cline plugin's
`setup(api, ctx)` only exposes `registerTool`/`registerCommand`/etc., not the
session's spawn-agent or team primitives. After calling `agents_select`, the
orchestrating Cline session must dispatch manually — see the "## Cline" section of
[`../skills/run-agent-orchestration/references/runner-adapters.md`](../skills/run-agent-orchestration/references/runner-adapters.md)
for the current manual-injection workaround and `/team` limitations.

## System prompt

This plugin registers a rule (`api.registerRule`, the "rules" capability
declared in [`index.ts`](index.ts)'s manifest and
[`package.json`](package.json)'s `cline.plugins[0].capabilities`) whose
content is appended to the session's composed system prompt automatically —
no host-application configuration required. `registerRule` is a genuine,
plugin-controlled system-prompt injection point, confirmed by reading
`@cline/shared`'s `AgentExtensionApi.registerRule` type declaration
("Register prompt rules included in the runtime system prompt") and
`@cline/core`'s compiled `SessionRuntime.composeSystemPrompt()`, which joins
the host's own `config.systemPrompt` (if any) with every registered rule's
content, in that order. This is distinct from — and additive to — a host
application's own `systemPrompt` field on `ClineCore.create()`/
`cline.start()`, which a Cline plugin cannot set itself (see
[`../skills/run-agent-orchestration/references/runner-adapters.md`](../skills/run-agent-orchestration/references/runner-adapters.md)'s
"## Cline" section, "Why a plugin can't dispatch," for the fuller picture of
what a plugin's `setup(api, ctx)` can and cannot reach).

The registered content begins with the exact sentence
`"You are a coding assistant with access to Cadre role subagents."` and adds
one clause naming `agents_select` and its plan-only scope. A host
application may still set its own `systemPrompt` for a different framing —
the two compose rather than conflict — but nothing further is required for
this identity sentence to reach the model once this plugin is installed.

## Behavioral detail

`agents_select` is the plugin's only tool. Called with no arguments it
discovers changed files from git status against the working tree; pass
`base` to diff `<base>...HEAD` instead. An explicit `taskId` is honored
verbatim in the returned plan; otherwise one is derived. A scope with no
matching route returns `status: "needs-triage"` rather than an empty or
guessed plan. `requireSdlc` is off by default (the plan degrades to
standalone mode when Agentic SDLC is unavailable) and, when set, hard-fails
instead of silently continuing without gate metadata. Dispatch failures and
an unresolvable workspace root are both returned as structured errors, never
thrown. See [`index.test.mts`](index.test.mts) for the exact assertions.

## Development

```sh
cd cline && npm test        # run tests
cd cline && npm run typecheck  # TypeScript type checking
```
