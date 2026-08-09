# Cline Agents (Cadre role presets)

A distinct plugin from [`cline/`](../cline) (which only exposes the
`agents_select` *planning* tool and never spawns anything). This plugin,
`cline-agents`, ports this repository's 74 Cadre catalog roles (`agents/*.md`,
the Claude Code / Codex subagent presets defined in this repository) into
Cline SDK **agent presets** that a Cline session can actually dispatch as
background subagents.

Structurally, this plugin adapts the Cline SDK's own
[`examples/plugins/agents-squad`](https://github.com/cline/cline) reference
plugin (preset discovery from Markdown+YAML-frontmatter files, `start_subagent`
/ `message_subagent` / `get_subagent` / `list_agent_presets` / `list_skills` /
`get_skill` / `save_handoff` / `read_handoff`), hardened per this port's own
threat-modeling pass -- see ["Hardening vs. upstream
template"](#hardening-vs-upstream-template) below.

## `agents/` and `skills/` are regenerated content, not hand-authored

The 74 files under `agents/` and the 8 files under `skills/` are produced by
[`tools/port_cline_agents.py`](../tools/port_cline_agents.py) (run from the
repository root), which this repository's release-triggered regeneration
workflow (`regenerate.yml`) now runs automatically alongside the rest of the
Claude Code / Codex regeneration -- see root `README.md`'s "Regenerating
Assets". It reads this repository's own `agents/*.md`/`skills/*/SKILL.md`
and rewrites source-repo-relative path references (e.g.
`` `../../shared/team-profile.yaml` ``) into consumer-neutral prose via a
fixed lookup table, plus one-off handling for 4 roles that needed a closer
look than the generic table: `debugging-engineer` and
`knowledge-store-steward` via the script's own `ROLE_OVERRIDES`;
`application-engineer` via its own dedicated code path plus
`APPLICATION_ENGINEER_PORT_NOTE` (its role text is literally about this
suite's own tooling, so most of the table doesn't apply to it at all); and
`threat-modeler`, whose one non-generic rewrite lives directly in the
shared `PATH_SUBSTITUTIONS` table since the source string it matches is
unique to that one file. It fails loudly (nonzero exit, stopping
the regeneration job before any PR opens) on any reference it doesn't
recognize, rather than silently shipping a leaked path -- extend the script's
substitution table when that happens, not this README.

`cline-agents/index.ts`, its `package.json`, `test/`, and this README remain
hand-authored; only `agents/*.md` and `skills/*.md` are generated. To
regenerate locally: `python3 tools/port_cline_agents.py --root .` from the
repository root.

## Quick start

```ts
import { ClineCore } from "@cline/sdk";

const cline = await ClineCore.create({ backendMode: "auto" });

await cline.start({
  config: {
    // Your own provider/model -- this plugin ships no default, and this
    // outer session is yours to configure. The presets it dispatches resolve
    // theirs from CLINE_AGENTS_PROVIDER_ID / CLINE_AGENTS_MODEL_* below.
    providerId: "your-provider",
    modelId: "your/model",
    cwd: process.cwd(),
    enableTools: true,
    systemPrompt: "You are a coding assistant with access to Cadre role subagents.",
    pluginPaths: ["./cline-agents"],
  },
  prompt: "Use start_subagent with preset \"security-reviewer\" to review this diff.",
  interactive: true,
});
```

Pass this plugin's **directory** as the path. The loader reads `package.json`
and discovers the entry point from the `cline.plugins` field.

The `systemPrompt` shown above is host-application config — set by whatever
calls `ClineCore.create()`/`cline.start()`, not by this plugin itself (a
Cline plugin's `setup(api, ctx)` has no field for that). It is shown here as
the recommended value because it is still worth setting explicitly: it is
what actually establishes the model's framing before its first turn, whereas
this plugin's own registered rule (below) only *appends* additional content
once a run starts composing its system prompt. Installing this plugin does
not require setting it, though — see "System prompt" below.

## System prompt

This plugin also registers a rule (`api.registerRule`, the "rules"
capability declared in [`index.ts`](index.ts)'s manifest and
[`package.json`](package.json)'s `cline.plugins[0].capabilities`) whose
content is appended to the session's composed system prompt automatically,
independent of whether a host sets `systemPrompt` as shown above.
`registerRule` is a genuine, plugin-controlled injection point — see
[`../cline/README.md`](../cline/README.md)'s "System prompt" section for the
`@cline/core`/`@cline/shared` source confirming this, which applies
identically here. The registered content begins with the exact sentence
`"You are a coding assistant with access to Cadre role subagents."` and adds
a clause naming `dispatch_selected_roles`/`start_subagent` and the
discovery tools (`list_agent_presets`/`list_skills`).

## Tools

| Tool | Purpose |
|---|---|
| `start_subagent` | Start a subagent in the background and return a session ID immediately. **`preset` is required** -- see "Preset-only dispatch" below. |
| `dispatch_selected_roles` | Call `bin/cadre select` (the same authoritative selector the `cadre` plugin's `agents_select` tool uses) and, if the plan is staffed, immediately `start_subagent` every selected primary/reviewer role in one call. Support roles are returned in the plan but never auto-dispatched -- start them explicitly if wanted. Pass `retrieveKnowledge: true` (opt-in, not the default -- `classification` is caller-asserted, not authenticated) to also retrieve knowledge-store context per role before dispatch and inject it as fenced, labeled untrusted reference material with a trailing authority re-assertion -- a retrieval failure or timeout for one role never blocks dispatch or broadens access for any role. Closes the plan-to-dispatch gap `agents_select`'s own tool description points at. |
| `message_subagent` | Send a follow-up message to a running subagent. |
| `get_subagent` | Poll status, output, or error for a subagent session. |
| `list_agent_presets` | List the 74 bundled Cadre role presets plus any accepted global/project overrides. |
| `list_skills` / `get_skill` | Discover and load skill instructions: this repository's own 7 bundled skills (a static port of `skills/*/SKILL.md`, with any `references/*.md` inlined -- see `skills/*.md` in this plugin), plus any accepted global/project overlays. Like agent presets, a bundled skill name cannot be silently shadowed by a same-named global/project skill. |
| `save_handoff` / `read_handoff` | Share text between subagents in the same conversation. |
| `create_review_subtask` / `write_wiki_page` / `write_evidence_comment` | GitLab evidence tools, reached via `cadre gitlab-evidence` (this plugin has no MCP client, so it cannot attach `suite/roster/orchestration/mcp/gitlab_server.py` directly -- see `suite/roster/orchestration/mcp/GITLAB-EVIDENCE.md`). All three require `GITLAB_SVC_TOKEN`/`GITLAB_BASE_URL`/`GITLAB_DOCS_PROJECT_ID` in this process's environment and return `status="unavailable"` if unset. `create_review_subtask`/`write_evidence_comment` are create-only, single-call. `write_wiki_page` is the `human_approval`-tier tool: its first call never writes -- it returns `status="confirmation_required"` plus a token that must be shown to a human and replayed unchanged on a second call before anything is written. |

Unlike the upstream `agents-squad` template, `start_subagent` has **no
default preset**. Every call must name a known preset; there is no
fallback to a full-tool, unrestricted subagent.

### Knowledge retrieval is an accepted, documented deviation from default-on

`suite/roster/shared/knowledge-use-policy.md`/`team-profile.yaml` describe
pre-dispatch retrieval as happening by default "when an authorized store is
available." `dispatch_selected_roles` deliberately does not do that here:
`classification` is a caller/model-asserted field, not an authenticated one
(the knowledge store's own classification filtering is exact-match, not a
permission check -- see `suite/roster/knowledge-store/SECURITY.md`), so this
plugin cannot tell "authorized" apart from "asserted." Retrieval is opt-in
(`retrieveKnowledge: true`) rather than defaulting on.

This mitigates the retrieval-tool's own behavior, not the underlying data
access: `dispatch_selected_roles` always returns the full plan, including
`knowledge_context.requests[].invocation.args` -- the knowledge-store CLI's
path plus the same `--classification`/`--source` flags retrieval would have
used. A host session with `run_commands` could execute that argv itself
regardless of `retrieveKnowledge`. This is the same plan contract `cline/`'s
`agents_select` already exposes, not something this tool introduces; treat
it as a property of the plan format, not a bypass of this opt-in gate.

## Model tiers and provider selection

Presets carry a capability **tier** and nothing else about the model:

| Source `model:` tier | Preset `modelTier` |
|---|---|
| `opus` | `opus` |
| `sonnet` | `sonnet` |
| `haiku` | `haiku` |

The tier is this suite's own domain knowledge — `roster/catalog.yaml`'s header
documents the heuristic that assigns it. Which provider and which concrete
model serve a tier is **operator configuration**, resolved at dispatch time.

**This plugin ships no default provider, deliberately.** A bundled default
picks a vendor on your behalf, and where that vendor's credentials happen to
exist it will silently route your task text — and any knowledge-store content
retrieved into a role's instructions — to it. Earlier versions defaulted to
Anthropic and requested `ANTHROPIC_API_KEY` regardless of how Cline itself was
configured (issue #142).

Configure at least a provider and one model:

```sh
export CLINE_AGENTS_PROVIDER_ID=your-provider
export CLINE_AGENTS_MODEL_OPUS=your/opus-class-model
export CLINE_AGENTS_MODEL_SONNET=your/sonnet-class-model
export CLINE_AGENTS_MODEL_HAIKU=your/haiku-class-model
```

`CLINE_AGENTS_MODEL_ID` sets one model for every tier if you would rather not
map them individually; the per-tier variables take precedence where set.

Resolution order, most specific first: a per-call `providerId`/`modelId` on
`start_subagent` or `dispatch_selected_roles` → an explicit `providerId`/
`modelId` in the preset's own frontmatter (project-local presets may set one;
bundled ones do not) → the per-tier variable → `CLINE_AGENTS_MODEL_ID`.

If nothing resolves, dispatch **fails before any session starts**, naming the
missing variable. It does not fall back to a vendor.

### Migrating from a version that defaulted to Anthropic

Prior versions behaved as though these were set. To reproduce that exactly:

```sh
export CLINE_AGENTS_PROVIDER_ID=anthropic
export CLINE_AGENTS_MODEL_OPUS=anthropic/claude-opus-4.6
export CLINE_AGENTS_MODEL_SONNET=anthropic/claude-sonnet-4.6
export CLINE_AGENTS_MODEL_HAIKU=anthropic/claude-haiku-4.6
```

There is no transition period during which the old default still applies —
that would reinstate the surprise this change removes.

**Caveat carried forward:** those model ids were never independently verified
against Cline's supported model catalog, `haiku` least of all. Confirm the ids
you configure resolve in your own installation; a wrong id now fails at
session start rather than silently selecting something else.

## Hardening vs. upstream template

This port intentionally departs from `examples/plugins/agents-squad` in three ways (verified accurate as of this port; see `index.ts` for the implementation):

1. **Real, not advisory, tool enforcement.** Each preset's source `tools:` frontmatter is translated into Cline's canonical `allowedTools` names, then turned into an explicit deny-by-default `toolPolicies` map at dispatch time (`resolveToolPolicyConfig`). Genuinely read-only roles (28 of 74, no `run_commands`/`editor`/`apply_patch`) additionally get `mode: "plan"` as defense-in-depth.
2. **Reserved bundled names.** Unlike the upstream template's project > global > bundled override precedence, this port rejects (not silently overrides) any global-/project-tier file whose `name:` collides with one of the 74 bundled role names.
3. **Preset-only dispatch, containment-checked `cwd`.** `start_subagent` rejects a missing/unknown `preset` rather than defaulting to an unrestricted subagent. A caller-supplied `cwd`/`workingDirectory` that would escape the workspace root (e.g. `../../etc`) is rejected, not clamped.

## Custom agents and skills

Same discovery model as the upstream template, minus bundled skills and
minus the ability to shadow a reserved bundled agent name:

| Kind | Bundled | Global | Project |
|---|---|---|---|
| Agents | `agents/` next to `index.ts` (74 Cadre roles, reserved names) | `~/.cline/data/settings/agents/` | `<workspaceRoot>/.cline/agents/` |
| Skills | `skills/` next to `index.ts` (8 skills, reserved names) | `~/.cline/data/settings/skills/` | `<workspaceRoot>/.cline/skills/` |

## Field mapping (source `agents/*.md` -> `cline-agents/agents/*.md`)

| Source field | Target |
|---|---|
| `name` | `name` (verbatim) |
| `description` | `description` (verbatim) |
| `model` tier | `modelId` (see table above) |
| `tools` | Not carried into output frontmatter verbatim (Cline doesn't recognize that field name). Mapped to `allowedTools` (Cline canonical tool names) and consumed for `toolPolicies`/`mode` at dispatch time -- see "Hardening" above. |
| `effort`, `generated` | Dropped -- no target equivalent, and `generated: true` would be actively misleading (this is a hand-authored port, not live-generated). |
| `canonical_source` | Kept, renamed `canonicalSource` (inert to Cline's loader; preserved for traceability back to the source register). |
| *(new)* | `convertedFrom: agents/<role>.md` -- points back at this repository's own source file. |
| Body | Used as `systemPrompt`, near-verbatim, minus a leading `# Role: <name>` catalog-artifact heading, and with cadre-source-repo-relative path references rewritten (see below). |
| *(new)* | `maxIterations` left unset for every role -- there is no source-catalog equivalent field, so none was fabricated. |

## Path-reference rewrites

Each source role body ends with an identical appended shared-policy block containing source-repo-relative path references (e.g. `` `../../shared/team-profile.yaml` ``, `roster/shared/README.md`) that resolve inside the *source* Cadre register/catalog layout but would 404 in an arbitrary consumer project. `tools/port_cline_agents.py`'s `PATH_SUBSTITUTIONS` table is the authoritative, current list of every such rewrite -- read that, not this paragraph, for the exact current mapping; duplicating it here would just go stale.

Four roles (`application-engineer`, `debugging-engineer`, `threat-modeler`,
`knowledge-store-steward`) need a closer look rather than a purely mechanical
rewrite; `tools/port_cline_agents.py`'s `ROLE_OVERRIDES` table and its
surrounding comments are the authoritative, current list of exactly what and
why -- read that, not this paragraph, for the per-role detail. The
regeneration's own regression test (`tools/test_port_cline_agents.py`) fails
if any of them silently reverts to the old committed behavior.

`skills/*.md` get the equivalent treatment via `SKILL_PATH_SUBSTITUTIONS` (a separate table, since skills reference this suite's CLI/data files rather than the shared-policy doc set agents reference) -- including replacing the "Packaged suite note" callout every `SKILL.md` carries (which points at a `suite/` directory this plugin doesn't ship) with an accurate Cline-specific note, and rewriting dangling internal `[references/X.md](references/X.md)`-style links into prose pointers at the now-inlined `# Reference: X.md` sections.

Both tables end in the same fail-loud safety net: any `roster/`-relative or `../`-relative reference left in a generated body that isn't covered by a table entry or a named exception stops the script (nonzero exit) rather than shipping a leaked path.

## Dependencies

`yaml` is declared as a direct dependency because this plugin's own code
(`parseFrontmatter` in `index.ts`) calls it directly to parse each preset's
Markdown frontmatter block; the pin is kept in step with the version
`@cline/core` already resolves to, so npm dedupes to a single installed
copy. See `package.json` for the exact pinned version.

## Configuration

| Variable | Default |
|---|---|
| `CLINE_AGENTS_BACKEND_MODE` | `auto` (`auto` \| `hub` \| `local`) |
| `CLINE_AGENTS_PROVIDER_ID` | *(none — required, see above)* |
| `CLINE_AGENTS_MODEL_OPUS` / `_SONNET` / `_HAIKU` | *(none — per-tier model id)* |
| `CLINE_AGENTS_MODEL_ID` | *(none — one model for every tier)* |
| `CLINE_DATA_DIR` | `~/.cline/data` |
| `CLINE_DIR` | `~/.cline` |

## Observability

Same feature-detected `ctx.logger`/`ctx.telemetry` pattern as the upstream
template: plugin setup, subagent starts, and queued follow-ups are logged;
a `cline_agents_setup` event, a `cline_agents.subagents.started` counter, a
`cline_agents_subagent_turn_completed` event, and a
`cline_agents.subagents.turn_duration_ms` histogram are emitted. Properties
stay low-cardinality (status, preset, provider) -- never task text or
subagent output.
