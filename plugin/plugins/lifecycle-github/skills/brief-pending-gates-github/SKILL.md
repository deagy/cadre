---
name: brief-pending-gates-github
description: Produce a local-only, forge-agnostic briefing of a task's pending Agentic SDLC lifecycle gates (G1-G10) -- which gate(s) are still awaiting a decision and which authority role/person is required for each. Use when a user asks "what's blocking this," "who needs to sign off," "what gates are pending," or "who do I need to chase" for a project already onboarded with lifecycle-onboarding-github, especially teams recording approvals with plain `agentic-sdlc decide` rather than a GitHub/GitLab review flow. Bundled with cadre-lifecycle-github so this skill is available without installing cadre-lifecycle-core separately.
---

> Packaged suite note: when the current project has no local `roster/` tree, resolve suite files under `../../../../suite/roster/` relative to this `SKILL.md`. The packaged plugin is self-contained; do not look for the source checkout.

> Duplication note: this skill's body is intentionally duplicated across the core plugin and both forge plugins so each plugin is self-sufficient and needs no dependency on the others (see AGENTS.md's plugin-split rationale). Frontmatter `name`/`description` and forge-specific cross-references intentionally differ per copy; the body must otherwise stay in sync -- `internal/generators/plugin_duplication_health_test.go` enforces it, comparing the copies section by section after normalizing forge-specific vocabulary.


# Brief pending gates

Use this skill to answer, in plain language, "which gate is next and who
needs to decide it" for a task already tracked by Agentic SDLC. This is a
read-only briefing, not a decision or a change: it never writes anything, and
it never calls a forge. Teams whose approvals go through `agentic-sdlc
decide` on the strength of an email or verbal sign-off get none of the
pending-reviewer visibility that `report-gate-reviewers-*` /
`publish-gate-status-*` give PR-centric teams (those post to a real GitHub/
GitLab review thread); this skill closes that gap locally instead, for
exactly the same underlying data.

This skill only composes information three sources already expose. It never
modifies `agentic-sdlc status`'s output or `gate_status_projection()`
themselves, and it never tries to make `status` reveal authority/assignee
identity -- that omission is deliberate (the same identity-minimization
boundary `publish-gate-status-*` relies on), not a gap to work around.

## Before you start

Confirm the target root (`.` if you are already working inside it) and
confirm `.agentic-sdlc/runs/<task-id>/run-record.json` already exists for the
task they mean -- if the project has never been onboarded or planned, stop
and point them at the `lifecycle-onboarding-github` skill (setup) or `cadre sdlc
plan` first; this skill only briefs on gates that already exist in a run
record.

Prefer `./bin/cadre sdlc status ...` over the bare `agentic-sdlc` binary,
exactly as `lifecycle-onboarding-github` and `lifecycle-review-generic-github` do, so this suite's
own provider profile resolves.

## Step 1 -- Get gate status

Run:

```sh
./bin/cadre sdlc status --root <path> --task-id <task-id>
```

Parse the JSON yourself. For every gate, note its `status` and
`applicability`. Filter to gates that are `applicability: applicable` and
whose `status` is not `approved` -- this is the full pending set, not just
the next one. Note `current_phase` too, since it tells you which of these is
the immediately-next gate versus one further down the sequence
(`required_reentry_gate`, if present on any gate, means that gate was reset
and needs a fresh decision even if a later gate once passed it).

Map each gate id to its plain name, the same vocabulary every other skill in
this suite uses:

| Gate | Plain name |
| --- | --- |
| G1 | Intent |
| G2 | Requirements Baseline |
| G3 | Architecture |
| G4 | Governance and Data |
| G5 | Security and Crypto |
| G6 | Verification and Test |
| G7 | Evidence |
| G8 | Release Readiness |
| G9 | Deployment Authorization |
| G10 | Runtime Conformance |

If nothing is pending (every applicable gate is `approved`), say so plainly
and stop -- there is no briefing to give.

## Step 2 -- Get each pending gate's required roles

`status` deliberately does not carry authority/assignee identity. Read
`.agentic-sdlc/runs/<task-id>/run-record.json` directly (the same file
`lifecycle-review-generic-github`/`link-source-issue-*` already read) and, for each gate
from Step 1, collect its `authority_requirements[]` entries
(`authority_id`, `role`, `applicability`). Keep only entries with
`applicability: applicable` -- a role listed but not applicable to this gate
does not need a sign-off from anyone.

## Step 3 -- Resolve each role to an assigned identity

Read `.agentic-sdlc/authorities.json` directly (the same file
`lifecycle-onboarding-github`/`lifecycle-review-generic-github` already read). For each
`authority_id` collected in Step 2, look up its entry and note the
`assignee` and `status` (e.g. `assigned` vs. unassigned). Do not invent an
assignee for a role that has none -- report it as unassigned instead.

## Step 4 -- Summarize

Give one plain-language briefing covering the full pending set, ordered by
gate sequence, for example:

> Two gates are waiting on a decision:
> - **Architecture (G3)** needs sign-off from the System Architect --
>   currently assigned to `jordan`.
> - **Security and Crypto (G5)** needs sign-off from the Security Lead --
>   no one is currently assigned to that role, so this can't move until
>   someone is.
>
> Everything before G3 is already approved.

Call out unassigned roles explicitly rather than burying them -- that is
often the actual blocker. If a gate requires more than one role, list all of
them for that gate. Never show the raw JSON, file paths as the primary
content, or CLI flags unless the human asks to see the underlying files or
commands.

## Throughout

- This is read-only: never run `decide`, `invalidate`, or any writing
  command from this skill. If the human wants to act on the briefing, hand
  off to `lifecycle-review-generic-github`.
- Never attempt to derive authority/assignee identity from `status` alone,
  and never suggest widening what `status` reports -- read
  `run-record.json` and `authorities.json` directly instead, as described
  above.
- Never fabricate an assignee for an unassigned role; report the gap.
- Summarize in prose; only show raw JSON/YAML/file contents if the human
  explicitly asks to see them.
