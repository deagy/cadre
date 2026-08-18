---
name: lifecycle-review-generic-github
description: Conversationally record a human's approve/reject/request-changes decision for an Agentic SDLC lifecycle gate (G1-G10), for a human who does not want to touch a CLI or JSON directly. Use when a user asks to "approve this gate," "review G<N>," "sign off on requirements/architecture/etc," "reject this," or "request changes" for a project already onboarded with lifecycle-onboarding-github. Bundled with cadre-lifecycle-github so this skill is available without installing cadre-lifecycle-core separately.
---

> Packaged suite note: when the current project has no local `roster/` tree, resolve suite files under `../../../../suite/roster/` relative to this `SKILL.md`. The packaged plugin is self-contained; do not look for the source checkout.

> Duplication note: this skill's body is intentionally duplicated across the core plugin and both forge plugins so each plugin is self-sufficient and needs no dependency on the others (see AGENTS.md's plugin-split rationale). Frontmatter `name`/`description` and forge-specific cross-references intentionally differ per copy; the body must otherwise stay in sync -- `internal/generators/plugin_duplication_health_test.go` enforces it, comparing the copies section by section after normalizing forge-specific vocabulary.


# Lifecycle review

Use this skill to drive `agentic-sdlc decide` end to end through a
plain-language conversation — the recurring act of a human approving,
rejecting, or requesting changes on a lifecycle gate, as opposed to
`lifecycle-onboarding-github`'s one-time initial setup. The human you are talking to
may have no CLI or JSON literacy at all — you run every command on their
behalf. Never show them raw flags or JSON unless they explicitly ask to see
it. Summarize everything in prose.

`decide` is the kernel's single authoritative decision verb — generic,
evidence-URI based, not tied to GitHub/GitLab review flows. This skill only
ever calls `decide`. If the human has a real GitHub PR review or GitLab MR
approval to cite as their evidence, and the matching forge-specific plugin
(`cadre-lifecycle-github`/`cadre-lifecycle-gitlab`) is installed, prefer that
plugin's own review-recording skill for this decision instead — it drives
`approve-from-github*`/`approve-from-gitlab*` (which require a real platform
review), sourced from the platform review rather than a free-text evidence
URI. Otherwise use `decide` here (see Step 4). Prefer `decide` over
`invalidate` either way — `invalidate` is a blunt whole-gate-and-downstream
reset, not a scoped decision.

## Before you start

Confirm the target root (`.` if you are already working inside it) and
confirm `.agentic-sdlc/runs/<task-id>/run-record.json` already exists for the
task they mean — if the project has never been onboarded or planned, stop and
point them at the `lifecycle-onboarding-github` skill (setup) or `cadre sdlc plan`
first; this skill only records decisions on gates that already exist in a run
record.

Check that the installed `agentic-sdlc` kernel actually has `decide` (run
`./bin/cadre sdlc --help` and look for it in the subcommand list — same
reachability check as `lifecycle-onboarding-github`'s "Before you start"). If it's
missing, tell the human in plain terms that a kernel upgrade is needed before
this skill can run, and stop — do not silently fall back to `invalidate` or a
platform-specific `approve-from-*` command as a workaround; those have
different semantics (see the note above) and picking one for them without
asking would misrepresent what they intended to do.

Prefer `./bin/cadre sdlc decide ...` (and `./bin/cadre sdlc status ...`) over
the bare `agentic-sdlc` binary, exactly as `lifecycle-onboarding-github` does, so
this suite's own provider profile resolves.

## Step 1 — Find the task and the gate

Ask which task/run this is about if not already obvious from context (the
`--task-id` used at `plan` time). Run:

```sh
./bin/cadre sdlc status --root <path> --task-id <task-id>
```

Parse the JSON yourself. Summarize the current gate in plain language using
this mapping (same as `lifecycle-onboarding-github`'s gate/phase vocabulary):

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

If the human names a gate directly ("I want to sign off on the architecture
review"), map their words to the `G<N>` id yourself rather than asking them
for the id. If they don't name one, default to whichever gate `status`
reports as next awaiting a decision.

## Step 2 — Confirm who is deciding, and refuse on their behalf if it isn't them

Read that gate's `authority_requirements` from
`.agentic-sdlc/runs/<task-id>/run-record.json` (`status` reports gate
status/applicability but not the per-gate authority list) — only ask about
roles the gate actually requires, not the full 8-role list. Ask the human's role in plain language, reusing
`lifecycle-onboarding-github`'s Step 4 role table (e.g. "Are you deciding this as
the person with final technical sign-off — the engineering lead?").

Look up that role's assigned identity in `.agentic-sdlc/authorities.json`.
This is the load-bearing check in this whole skill: `decide` refuses to
record a decision from anyone other than the exact assigned identity, and it
also refuses if that identity is a preparer or the independent verifier for
this gate (self-approval). Do not try to work around either refusal on the
human's behalf — a mismatch means either they are not the assigned authority
for this gate (tell them plainly who is, and that only that person can
decide it), or they prepared/verified this gate's own work (tell them a
different, independent person needs to make this call). Offer to help them
reassign the authority (point back at `lifecycle-onboarding-github`'s Step 4
pattern for hand-editing `authorities.json`) only if they say the assignment
itself is wrong — never silently substitute a different actor id to make the
command succeed.

## Step 3 — Get the decision

Ask plainly: "Do you approve this, do you want changes made first, or are
you rejecting it outright?" Map the answer to `--decision approved` /
`request-changes` / `rejected`. Do not show these literal strings to the
human unless they ask.

## Step 4 — Get evidence (required — never invent or skip this)

Ask: "What's your basis for this — a document, ticket, or review link I
should attach as the record of this decision?" This is required; `decide`
will not run without `--evidence-uri`, and this skill must not fabricate a
placeholder value on the human's behalf. If they have a real GitHub PR review
or GitLab MR approval already, and the matching forge plugin is installed,
prefer that plugin's skill instead of continuing here (see the note above).
Otherwise accept whatever they give you (a URL, a ticket id, a document
name) and turn it into a plain evidence URI yourself, e.g. `doc:<what they
described>` or `ticket:<id>` — tell them what you're recording it as in one
short sentence so nothing is silently invented without their awareness.

Optionally ask if they want a short rationale note attached (`--note`) —
this is not required.

## Step 5 — Run decide

```sh
./bin/cadre sdlc decide --root <path> --task-id <task-id> --gate <G_id> \
  --role <role> --decision <mapped> --actor-id <assignee> \
  --evidence-uri <uri> [--note <note>] [--decided-at <RFC3339>]
```

Parse the returned JSON yourself. Report the outcome in one plain sentence
("Recorded — Architecture is now approved" / "Recorded — this gate now needs
changes before it can move forward"), never the raw JSON, unless they ask to
see it.

## Step 6 — Translate errors, don't just print them

If the command exits non-zero, translate the `error` message the same way
`lifecycle-onboarding-github`'s Step 9 translates `validate` blockers, for example:

- `"actor <id> does not match assigned authority <id> for role <role>"` →
  re-explain who the assigned decision-maker actually is (Step 2).
- `"... is a preparer for <gate>; cannot decide on own work"` / `"... is the
  independent verifier for <gate>; cannot also decide"` → explain plainly
  that they can't sign off on their own work here and a different person
  needs to.
- `"<gate> does not require authority role <role>"` → re-ask Step 2's role
  question; this gate doesn't need that role's sign-off.
- `"authority <role> is not assigned"` → this role has no one assigned yet;
  point back at `lifecycle-onboarding-github`'s Step 4.

## Throughout

- Never show raw JSON, YAML, or CLI flags to the human unless they explicitly
  ask to see the underlying files or commands.
- Never let an actor-identity or self-decision refusal be worked around
  silently — those are the point of this command, not friction to route
  past.
- Summarize the outcome in prose after each decision.
