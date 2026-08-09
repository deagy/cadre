# Adopt-Cadre quickstart

A concrete, copy-pasteable walkthrough for a new project adopting this suite.
For background and full detail, see [Getting started](getting-started.md),
[Lifecycle and plugin operations](lifecycle-and-plugin-operations.md), and
[roster/RUNBOOK.md](../roster/RUNBOOK.md). See [CHANGELOG.md](../CHANGELOG.md)
for what's changed in the suite recently.

## 1. Install `cadre`

See **[Installing Cadre](INSTALL.md)** — one command for whichever runner you
use. This guide does not repeat the steps; a second install path is one
nobody remembers to update.

Confirm it resolves:

```sh
cadre select --help
```

Two subcommands are maintainer-only and need a full checkout of this
repository: `cadre generate-plugin` and `cadre generate-authority-aides`.
`cadre generate-role-metadata --check` works from any install; its write mode
is checkout-only for the same reason. Everything else works everywhere.

## 2. Initialize lifecycle tracking for your project

Lifecycle gates (G1–G10) are driven by the Agentic SDLC kernel. It ships in
this repository, so a checkout needs no separate install and no
`AGENTIC_SDLC_BIN` — `cadre sdlc` finds it. If you installed Cadre as a
plugin instead, install the kernel once with the `/cadre-install-kernel`
skill; see [Adding lifecycle
governance](INSTALL.md#adding-lifecycle-governance).

Then initialize your project through this suite's compatibility launcher,
using `--profile secure-cloud` if your project actually runs on this suite's
own target stack (Proxmox, Talos, Kubernetes, Helm, OpenTofu, GitLab CI,
PostgreSQL, React/TypeScript, Go). Use `quick`, `generic`, or `web-service`
instead for a different stack — `secure-cloud` pulls in 19 roles opinionated
toward that infrastructure and shouldn't be forced onto an unrelated one.

```sh
cadre sdlc init --root /path/to/your-project --profile secure-cloud \
  --project-id your-project --classification internal --runner claude
```

`--runner {codex,claude,both}` generates subagent wrapper files in your
project; omit it if your project doesn't want generated wrappers written.
This writes `.agentic-sdlc/` into your project — review the detected
technologies and commands, then assign human authorities (Product Owner,
Engineering Lead, System Architect, Governance Lead, Security Lead, Release
Owner, Release Authority, Service Owner) and decide environment
persistence/production status before expecting gates to pass. `init` never
infers approval, risk acceptance, or compliance applicability for you.

If you'd rather not touch a CLI, JSON, or YAML at all, ask an agent in your
project to run the `lifecycle-onboarding` skill instead — it drives this same
flow (and the optional shared-policy overlay in step 4) conversationally. See
[.agents/skills/lifecycle-onboarding/SKILL.md](../.agents/skills/lifecycle-onboarding/SKILL.md).

Validate once you've made the required decisions:

```sh
cadre sdlc validate --root /path/to/your-project
```

## 3. Run a first `cadre select`

From inside your project (or with `--root /path/to/your-project`), generate a
deterministic dispatch plan for a real task — no agents run, nothing is
mutated, nothing is approved:

```sh
cadre select \
  --task "Add input validation to the upload endpoint" \
  --files backend/internal/api/upload.go \
  --classification internal \
  --task-id PROJ-101
```

This prints a plan (schema-versioned JSON) naming the primary/reviewer/support
roles, matched routes, required quality gates vs. mutation-oriented human
gates, and a `teams` array for any matching team recipe. It works standalone;
when `agentic-sdlc`/`AGENTIC_SDLC_BIN` is also resolvable it's automatically
enriched with lifecycle-gate tracking (`lifecycle_tracking.status:
"integrated"`) — pass `--require-sdlc` to fail instead of silently falling
back to standalone mode if that integration is required for your workflow.

If nothing matches your task, the selector returns `needs-triage` rather than
guessing — that's expected for a task that doesn't fit any routing rule yet
(see step 5 to add one).

## 4. Set up a project-local knowledge store (optional)

Without any setup, agents fall back to a store shared across every project on
the machine (`~/.agents/knowledge-store/`). If your project wants its content
kept isolated instead, create a project-local config at your project's
`.agents/knowledge-store/config.json` (found by walking up to the nearest
`.git` boundary, same as the shared-policy overlay below):

```sh
mkdir -p /path/to/your-project/.agents/knowledge-store
cp /path/to/cadre-checkout/roster/knowledge-store/config.example.json \
   /path/to/your-project/.agents/knowledge-store/config.json
```

Nothing else changes — every `cadre knowledge ...` command works identically
once the file exists; it just resolves to your project's own store instead of
the shared default. See
[roster/knowledge-store/README.md](../roster/knowledge-store/README.md) and
`roster/knowledge-store/SECURITY.md` before ingesting real content: retrieved
text is always untrusted reference data, never executable instruction.

## 5. Add a project-local routing overlay (optional)

If your project wants an extra route, a widened risk-rule keyword, or an
additional team recipe, you don't need to fork `orchestration/routing.yaml`.
Add a plain JSON file (not YAML) at your project's
`.agents/orchestration/routing-overlay.json` — discovered the same way as
`.agents/shared/` and `.agents/knowledge-store/config.json`, by walking up to
the nearest `.git` boundary. With no overlay present, behavior is unchanged.

The overlay's merge rule differs by section, because most of `routing.yaml`
carries gating semantics an ordinary policy overlay doesn't:

- `routes[]` / `risk_rules[]`: add a new non-colliding `id`, or *widen* an
  existing entry's `keywords`/`keyword_groups`/`paths` (every base value must
  still be present — narrowing fails closed). Every other field on that same
  entry (`primary`, `reviewers`, `support`, `quality_gates`, `human_gate`,
  `exclude_paths`) must match the base value exactly. A *new* overlay entry
  may declare its own `exclude_paths`; it just can't be changed on an
  existing one, because a longer exclusion list narrows the match rather
  than widening it.
- `team_recipes[]`: additive only — a new `id`; existing entries are
  immutable.
- `change_intake`: `keywords`/`agents`/`quality_gates` are additive only.
- `cross_stack`: `route_ids`/`support` are additive only; `minimum_matches`
  may only decrease.
- `knowledge_focus`: ordinary deep-merge, overlay wins per key.
- `ignored_gates`: may only shrink.
- `version`: fixed; may be repeated but not changed.

Validate and materialize the effective (merged) configuration:

```sh
python3 roster/orchestration/src/routing_overlay.py --check
python3 roster/orchestration/src/routing_overlay.py --out /tmp/effective-routing.json
```

The materialized file is plain `routing.yaml`-shaped JSON, so you can also
point `routing_health.py --routing <path>` or `schema_validate.py --routing
<path>` at it to validate the effective configuration your project actually
dispatches against, not just the unmodified base file.

## 6. Check for drift against this suite's canonical profile later

Once your project has its own copy of `provider.json` /
`profiles/<id>/profile.json` from `cadre sdlc init`, check later whether that
copy has drifted from this checkout's current release, without changing
anything:

```sh
cadre profile diff \
  --copy-provider  /path/to/your-project/.agentic-sdlc/provider.json \
  --copy-profile   /path/to/your-project/.agentic-sdlc/profile.json \
  --original-provider /path/to/captured-original-provider.json \
  --original-profile  /path/to/captured-original-profile.json
```

`--copy-provider`/`--copy-profile` (required) point at your project's current
copy — this suite doesn't assume a specific `.agentic-sdlc/` internal layout,
so use whatever path your project actually keeps them at.
`--original-provider`/`--original-profile` (optional) point at a snapshot of
what that copy was originally captured from, if you kept one; omitting them
is expected and reported as its own `provenance-undetermined` state rather
than guessed.

The report classifies each artifact independently as `current` (matches this
release exactly), `stale-unmodified` (matches ORIGINAL, which is now behind),
`diverged` (no longer matches ORIGINAL), `copy-invalid` (malformed JSON or a
missing required field), or `provenance-undetermined` (no ORIGINAL supplied),
naming every differing field in one pass. Exit code `0` means both artifacts
are `current`; anything else exits non-zero. **This is a drift report, not an
approval, gate-pass, or compliance signal** — it never re-syncs your project,
and it never reads or interprets your project's `.agentic-sdlc/`
gate-approval or human-authority state.

## What's next

- [Orchestration guide](orchestration.md) for dispatching agents against a
  real task once you have a plan you like.
- [Role index](role-index.md) to find the right specialist by name.
- [Lifecycle and plugin operations](lifecycle-and-plugin-operations.md) for
  GitHub-review-backed approvals, upgrades, and the full CLI reference.
- [CHANGELOG.md](../CHANGELOG.md) to track what's new in this suite over
  time.
