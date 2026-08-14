# Which runner am I in?

This repository's roles and skills run unmodified on multiple AI coding
runners, but a few things they can do differ by runner: whether a
plugin-generated wrapper exists for a role, whether named-role subagent
dispatch is even possible, and whether teammates can talk to each other
peer-to-peer. Use this page to identify which runner is hosting the current
session, then read the row of the second table that applies.

The facts below are grounded in this repository's runner capability data —
primarily [`.agents/skills/run-agent-orchestration/references/runner-adapters.md`](../.agents/skills/run-agent-orchestration/references/runner-adapters.md)
(on `main`), cross-checked against the structured
[`roster/runner-capabilities.json`](../roster/runner-capabilities.json)
manifest, which is a machine-readable representation of the same rules
`runner-adapters.md` documents in prose. `roster/orchestration/test/test_runner_capabilities.py`
validates it against `roster/runner-capabilities.schema.json`.

## How to tell which runner you're in

In practice, an agent session already knows which product is hosting it —
this section is for the cases where that isn't obvious (for example,
documentation or automation reasoning about a session from the outside).

| Signal | Claude Code | Codex CLI | Cline |
| --- | --- | --- | --- |
| Generated per-role wrapper present | the plugin package's `agents/*.md` (or a project-local `.claude/agents/<role-id>.md` override) | `provider/codex-agents/agents-*.toml`, synced to `~/.codex/agents/` | None — no generated wrapper exists for Cline yet |
| Project config directory | `.claude/` (`.claude/agents/`, `.claude/skills/`) | `.codex/` (`.codex/agents/`, `.codex/config.toml`) | `.clinerules/` (one general pointer file, not per-role) |
| Subagent-dispatch tool name in the session | `Agent`/`Task` tool referencing a named subagent type | `spawn_agent` tool with a generic `agent_type` argument | Host-registered tools only; the Cline plugin in [`cline-plugins/cline/`](https://github.com/deagy/cadre/tree/main/cline-plugins/cline) (`index.ts`) registers exactly one tool, `agents_select`, which plans only and never dispatches |
| Distinguishing environment/config signal | `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS` env var gates Agent Teams | `config.toml` `[mcp_servers.*]` / `[agents]` blocks | `~/.cline/data/teams/[team-name]/` if team mode has run |

## What that implies

| Property | Claude Code | Codex CLI | Cline |
| --- | --- | --- | --- |
| Generated wrapper exists for this repo's roles | Yes | Yes | No |
| How a role is named for dispatch | `agents:<role-id>` (plugin-installed) or bare `<role-id>` (project-local override) | `.codex/agents/<role-id>.toml` (project) or `~/.codex/agents/agents-<role-id>.toml` (global) | Not applicable — no per-role naming mechanism exists |
| Can the model-visible dispatch tool select a *named* custom role directly? | Yes | **No** — `spawn_agent` only accepts a generic `agent_type`; there is no parameter for a named `.codex/agents/` entry (tracked upstream as openai/codex#15250 and related issues) | No — no plugin-facing spawn/team-dispatch API exists (confirmed gap, not an oversight) |
| Workaround when named dispatch isn't supported | Not applicable (natively supported) | Preferred: register this repo's MCP dispatch server (`cadre mcp-dispatch-server`) and call `dispatch_secure_cloud_role`. Fallback: read the target `.toml` file's `developer_instructions`/`model` and inject them manually into `spawn_agent` | Manual per-file injection only — read the role's `AGENT.md` (or its Codex `.toml` wrapper if already synced) and inject its content as the task/system framing for a fresh turn or spawned subagent; no MCP-equivalent documented |
| Peer-to-peer teammate messaging (`communication_mode: "peer"`) | Supported, but gated: requires `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`; falls back to `orchestrator-relayed` if unset | Not supported — no peer messaging or shared task list; coordination is entirely orchestrator-centric | Best-effort only — team mode exists (`/team`, `cline --team-name`) but the *coordinator's own model* decides teammate composition and messaging; not guaranteed the way Claude Code's gated Agent Teams are |
| Nested teams (a teammate spawning its own teammates) | Not supported — runner limitation | Not applicable (no team primitive at all) | Not applicable in the same sense; team state persists under `~/.cline/data/teams/[team-name]/` but there is no per-role teammate naming to nest |
| Team size guidance | 3–5 teammates, disjoint file ownership per teammate | Not applicable | Not applicable |
| Concurrency bound | Not separately documented beyond the Agent tool's own session limits | `agents.max_concurrent_threads_per_session` (`[agents]` block, native `spawn_agent`) or `MAX_CONCURRENT_CHILDREN` (this repo's MCP dispatch server) | Not separately documented |
| Can roles run against a self-hosted model? | Not through this repo's tooling — Claude Code has no custom-provider mechanism | Yes: a `[model_providers.*]` block in a Codex config profile, selected by `runners.codex_profile`, with `runners.local_model_<tier>` supplying the per-tier model. See [README.md#self-hosted-models](../README.md#self-hosted-models-llamacpp-ollama-vlm) | Yes, but through Cline's own provider config: `CLINE_AGENTS_PROVIDER_ID` + `CLINE_AGENTS_MODEL_<TIER>` |

**None of the above installed?** The MCP dispatch server's `runner="api"`
needs no coding CLI at all — it drives an OpenAI-compatible
`/chat/completions` endpoint directly, supplying its own agent loop and its
own (in-process, weaker) sandbox. That makes it the answer to "I'm in none of
these three runners, but I have a model endpoint." Read
`roster/orchestration/SECURITY-CONTROLS.md`'s "API runner" section first.

## Practical effect

Every named role and every `team_recipes` entry in `cadre select`'s output
works on every runner — the role list and each role's distinct focus are
runner-agnostic. What changes is *how much of the coordination effort a
runner does for you*:

- **Claude Code** with `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` set is the
  only runner where teammates can genuinely challenge or build on each
  other's findings before you see a synthesized result.
- **Codex CLI**, and Claude Code without that flag, still run the same role
  set as an ordinary parallel wave, but the orchestrating session performs
  all synthesis and reconciliation itself — never report that agents
  "discussed" or "challenged" each other's findings when this fallback
  actually ran.
- **Cline** additionally has no generated per-role wrapper and no
  plugin-facing dispatch API yet; any role dispatch beyond `agents_select`'s
  plan-only output requires manually injecting the role's `AGENT.md` content
  into a fresh turn or the host's own subagent primitive, and any team
  coordination is delegated to the team coordinator's own judgment rather
  than following `cadre select`'s `teams` field mechanically.

See [runner-adapters.md](../.agents/skills/run-agent-orchestration/references/runner-adapters.md)
for the full detail behind every row above, including the exact upstream
issue numbers, setup steps for the MCP dispatch server, and known
authentication-mode caveats for Codex's `model` override.
