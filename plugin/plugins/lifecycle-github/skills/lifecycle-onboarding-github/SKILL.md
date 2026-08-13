---
name: lifecycle-onboarding-github
description: Conversationally set up Agentic SDLC lifecycle tracking (G1-G10 gates) for a project, for a human who does not want to touch a CLI, YAML, or JSON directly. Use when a user asks to "set up feature tracking," "onboard this project," "start tracking gates/progress," or "initialize lifecycle" for this repository or any other project. Bundled with cadre-lifecycle-github so this skill is available without installing cadre-lifecycle-core separately.
---

> Packaged suite note: when the current project has no local `roster/` tree, resolve suite files under `../../../../suite/roster/` relative to this `SKILL.md`. The packaged plugin is self-contained; do not look for the source checkout.

> Duplication note: this skill's body is intentionally duplicated across the core plugin and both forge plugins so each plugin is self-sufficient and needs no dependency on the others (see AGENTS.md's plugin-split rationale). Frontmatter `name`/`description` and forge-specific cross-references intentionally differ per copy; the body must otherwise stay in sync -- `tools/test_plugin_duplication_health.py` enforces it, comparing the copies section by section after normalizing forge-specific vocabulary.


# Lifecycle onboarding

Use this skill to drive `agentic-sdlc init` (and, optionally, this suite's
`cadre init` policy overlay) end to end through a plain-language
conversation. The human you are talking to may have no CLI, YAML, or JSON
literacy at all — you run every command and edit every file on their behalf.
Never show them raw flags, JSON, or YAML unless they explicitly ask to see
it. Summarize everything in prose.

There is no CLI subcommand for setting authorities, commands, or
environments after `init` — `authorities.json`, `commands.json`, and
`project.json`'s `environments` are hand-edited JSON files with a fixed
schema (see below). This is a real gap in the kernel's command surface, not
an oversight in this skill — do not invent a wrapper command that does not
exist.

## Before you start

Confirm the target root (the project directory to set this up in — `.` if
you are already working inside it) and confirm the human actually wants
lifecycle/gate tracking, not just a to-do list. If they want something much
lighter (a plain checklist, GitHub Issues), say so and stop — this skill is
specifically for G1-G10 gate tracking.

Check whether `agentic-sdlc` is reachable (`AGENTIC_SDLC_BIN` env var, or
`agentic-sdlc` on `PATH`, or this repository's own in-tree `kernel/`). If
not, tell the human in plain terms that a one-time install step is needed,
offer to do it (`pipx install ./kernel` from this checkout, or `pipx
install` a pinned `kernel-v*` release per `kernel/README.md`), and proceed
once it is available.

Prefer running through this suite's compatibility launcher,
`./bin/cadre sdlc <subcommand>`, rather than the bare `agentic-sdlc`
binary — it automatically wires in this repository's own provider profile
(`provider/provider.json`) so `secure-cloud`-derived profiles resolve.

## Step 1 — Resolve the profile

Do not show `--profile {quick,generic,web-service,secure-cloud}` as a raw
choice. Instead ask about the project's stack, e.g.: "Does this run on
Kubernetes, Helm, OpenTofu, and GitLab CI, similar to this suite's own
target infrastructure?" → `secure-cloud`. If not, ask a couple of narrowing
questions (is it a deployed web service with its own environments? or is it
lightweight tooling/a library/a script?) to land on `web-service`,
`generic`, or `quick` (`quick` is the low-ceremony default when unsure).

Never propose `secure-cloud` for a project that isn't actually the same
kind of cloud-infrastructure stack this suite documents — it pulls in 19
opinionated roles shaped around that infrastructure. When onboarding a
project that is itself a role/tooling catalog like this one (not a deployed
service), `generic` is usually the right choice, and no `--runner` should be
passed (see the note below).

## Step 2 — Resolve classification

Ask: "Does this involve customer data or anything sensitive, or is it
purely internal tooling?" Map the answer to `internal`, `confidential`,
`restricted`, or `public` — ask a clarifying follow-up if the answer is
ambiguous rather than guessing.

## Step 3 — Run init

```sh
./bin/cadre sdlc init --root <path> --profile <resolved> \
  --project-id <slug> --classification <resolved> [--runner <resolved>]
```

Ask separately whether the human wants generated Claude Code / Codex
subagent wrapper files written into the target project (`--runner claude`,
`--runner codex`, or `--runner both`) — most projects want this so the
roles are directly dispatchable; omit `--runner` entirely for a project
that, like this suite's own repository, is itself the *source* of those
wrappers rather than a consumer of them (check for a repo-specific rule
against local `.claude/agents/`/`.codex/agents/` overrides before defaulting
to a runner — this repository's own health test forbids exactly that).

Run with `--dry-run` first, inspect the `would_create` list yourself (not
shown to the human), then run for real. Report the outcome in one sentence
("I've set up the basic lifecycle tracking — now I need a few decisions
from you").

## Step 4 — Authorities: resolve what's needed now, defer the rest

`.agentic-sdlc/authorities.json` has one entry per role, and `init` leaves
all thirteen unresolved. Do **not** interview the human for all of them up
front. That is the single biggest reason this conversation used to run long,
and most of those roles cannot matter yet.

An authority only ever gates the specific G-gate(s) that name it:

| Gate | Authorities it needs |
| --- | --- |
| G1 Intent | `product_owner` |
| G2 Requirements Baseline | `product_owner`, `engineering_lead` |
| G3 Architecture | `system_architect` |
| G4 Governance and Data | `governance_lead`, `data_control_owner` |
| G5 Security and Crypto | `security_lead`, `human_key_owner` |
| G6 Verification and Test | `engineering_lead`, `uat_product_owner` |
| G7 Evidence | `release_owner` |
| G8 Release Readiness | `release_owner` |
| G9 Deployment Authorization | `release_authority` |
| G10 Runtime Conformance | `service_owner`, `implicated_security_lead`, `implicated_governance_lead` |

That table mirrors the kernel's own `kernel/contracts/lifecycle-gates.json`;
re-read the contract rather than trusting this copy if the two disagree.

An unresolved role is a supported state, not a broken one. The project still
validates (`valid: true`), tasks can still be planned, and every gate whose
authorities *are* resolved can still be approved. The only thing an
unresolved role stops is approval of its own gate — which is the protection
you want, not a defect.

**Ask for two roles, then stop.**

| Role | Plain-language question |
| --- | --- |
| `product_owner` | Who decides what this project should actually do — final word on scope and priorities? |
| `engineering_lead` | Who has final technical sign-off on how it's built? |

Those two clear G1 and G2, which is everything needed to start work. Set
`status: "assigned"` and `assignee` to the identity they give you (a name,
email, or handle — whatever they naturally offer; ask for a GitHub login or
GitLab username too only if they said they want GitHub/GitLab-review-backed
approvals in Step 6).

If the human volunteers that the same person (often themselves) holds
several or all of the roles, take that as the answer for all of them and say
so — it is valid and normal for a solo maintainer or small team, and the
kernel's author/reviewer separation check applies to agent roles assigned to
a route, not to which human holds which named authority. Offer it as a
single yes/no ("same person for the rest too?"); do not walk the remaining
eleven questions just because one answer would have covered them.

Then tell the human plainly what you deferred and when it comes back, e.g.
"I've recorded you as product owner and engineering lead, which covers the
first two gates. I'll ask who signs off on architecture, security, and
release when you first reach those — nothing to decide now."

**One timing rule you must respect** (see "Resolving a deferred authority"
below): a task's run record snapshots which authorities were assigned at the
moment it was *planned*. Resolve a role before planning the task that will
need its gate, not after.

**Preflight-check every identity before writing it.** As soon as the human
gives you an identity for a role — whether it's an explicit
`gitlab_username`/`github_login`, or a name/email/handle, or a
`gitlab.com/<user>` / `github.com/<user>` URI-style `assignee` — parse it the
same way the kernel itself resolves it (explicit field wins, then
URI-form `assignee`, then unresolved) and tell the human plainly whether it
looks like a usable forge binding *before* you write it to
`authorities.json`, not after. Use plain language, translating the same
reason-code vocabulary `create-github-gate-issues` uses later, so the human hears
about a problem now instead of mid-run:

- Looks fine (an explicit `gitlab_username`/`github_login`, or an `assignee`
  in `gitlab.com/<user>` / `github.com/<user>` form) → confirm briefly and
  move on.
- Nothing forge-shaped at all (just a name or bare email, and they said they
  don't need GitHub/GitLab-review-backed approvals) → that's fine as-is; no
  need to press further.
- They *do* want GitHub/GitLab-review-backed approvals (per Step 6) but gave
  a bare name or email with no forge form (this is `no-github-binding`/the
  GitLab equivalent) → ask them for the actual GitHub login or GitLab
  username now, before writing the file, rather than leaving it to be
  discovered later.

**Known limitation — say this out loud to the human:** this preflight only
checks that the identity is *shaped* like a usable binding (explicit field
present, or a well-formed `gitlab.com/`/`github.com/` URI). There is no
kernel command exposed today that verifies the account actually exists on
GitHub/GitLab from here — that live check only happens the first time a
forge-write skill (like `create-github-gate-issues`) actually runs and calls the
forge API, and it can still come back `github-user-unresolved` (no such
account) at that point even though this preflight looked fine — unlike
GitLab, GitHub's lookup is exact-match, so there is no separate
ambiguous-match case here. Tell the human this explicitly rather
than implying the binding has been fully verified.

The 5 **conditional** roles are deferred the same way — each belongs to a
gate well past where onboarding ends, so do not ask these now. Keep the
questions for when that gate comes into view (G4 for `data_control_owner`,
G5 for `human_key_owner`, G6 for `uat_product_owner`, G10 for the two
`implicated_*` roles):

| Role | Gating question |
| --- | --- |
| `data_control_owner` | Does this project store or process personal/customer data? |
| `human_key_owner` | Does this project manage its own encryption keys or certificates? |
| `uat_product_owner` | Is there a separate user-acceptance-testing phase with its own stakeholder, distinct from the product owner? |
| `implicated_security_lead` | Is this itself a deployed, running service (not just a library/tool)? |
| `implicated_governance_lead` | (same as above) |

When you do ask — if no: set `applicability: "not-applicable"` with a short
`rationale` sentence built from their answer (e.g. "Project holds no
customer data"). If yes: set `applicability: "applicable"`, `status:
"assigned"`, and `assignee` to who they name.

The one time to settle a conditional role during onboarding is when the
human has *already* answered it while discussing something else. If they
said in Step 2 that the project holds no customer data, record
`data_control_owner` as not-applicable with that rationale rather than
making them say it twice.

## Step 5 — Environments

Ask: "What environments does this run in (e.g. local, staging,
production), and for each — is it something that gets thrown away/rebuilt
often, or does it stick around? And is it a real production environment or
not?" Edit `.agentic-sdlc/project.json`'s `environments` list: set
`persistence` to a value like `"disposable"` or `"persistent"` and
`production` to a value like `"production"` or `"non-production"` per their
answers (never leave either as the literal `"unknown"` — that is what
blocks validation).

## Step 6 — Commands

Read `.agentic-sdlc/commands.json` and whatever `detected.command_candidates`
`init` reported. Describe what was found in plain terms ("I found what
looks like your test command: ... — is that right, or should I use a
different one?"). Let them correct in natural language, then write the
final commands into `commands.json` and set `"confirmed": true`.

## Step 7 — Impact/BOM applicability

Read `.agentic-sdlc/impact-profile.json`. For each entry under
`impact_categories`/`specialized_boms` with `applicability: "unknown"`, ask
a plain question derived from its purpose (e.g. "does this store or process
personal/customer data?", "does this involve cryptographic key material?")
rather than naming the BOM/category jargon. Record `applicable` or
`not-applicable` with a short rationale until `blocking_unknowns` is empty.

## Step 8 — Optional: this suite's shared policy overlay

If the target project also wants this suite's own `.agents/shared/*`
policy overlays (team profile, library/technology standards, cloud
guardrails, autonomy policy), separately ask if they want that. This
subcommand takes an optional project-root argument (or the legacy `--target
<path>` spelling, not `--root`), and always previews by default — it only
writes when `--force` is also passed. With no project root, it uses the nearest
enclosing Git worktree; outside Git, it asks for the root explicitly.

Start from the defaults. `cadre init` keeps every shipped default unless
told otherwise, so the common case needs no answers at all:

```sh
# From this Cadre suite checkout, name the selected target explicitly.
./bin/cadre init <absolute-target-project-root>
```

When `cadre` is installed and the shell is already in the target project's Git
worktree, the target may instead be inferred:

```sh
cadre init
```

That writes nothing, and it is not a half-finished run: the shared overlays
are sparse, so "keep the default" means "write no overlay for that field",
and a project with no overlay resolves to exactly the shipped values. Say so
plainly ("nothing to change — it'll use the standard policy") rather than
implying a step was skipped.

When the human does want something different, override just those fields
with `--set [REGION:]PATH=VALUE`, which is repeatable:

```sh
./bin/cadre init <absolute-target-project-root> --set platform.hosting_model=cloud --force
```

Prefer `--set` over authoring an `--answers` file. It needs no file, and it
records the required `field_decisions` entry for you with the category
derived from the field's own home file rather than from something you
assert. If a path is ambiguous the command names the regions that
matched — qualify it (`autonomy:policy_version=...`) instead of guessing.

Reach for `--answers <file>` (`schema_version: 1`; see `cadre init --help`
and `roster/shared/src/init_project.py` for its shape and the
`field_decisions` entry required per touched field) only when there are
enough overrides that a file is genuinely clearer. `--interactive`
(prompt-flow mode) drives a live terminal prompt loop meant for a human
typing directly at it — you cannot reliably drive it as an agent through
non-interactive command execution, so do not try.

Whatever you use, run once with `--dry-run` (the default without `--force`)
to preview, then re-run with `--force` to actually write, translating
whatever `cadre init` rejects into plain questions for the human rather than
showing them the file or the flags. One rejection worth recognizing: a
`--set` on an `agent-autonomy.yaml` field can only ever *narrow* the shipped
policy, so if one is refused, tell the human their request would have
loosened the governance baseline — do not retry it a different way.

This is a distinct, optional step from `agentic-sdlc init`; do not
conflate the two internally, but you don't need to explain the
distinction to the human unless they ask.

## Step 9 — Validate, and read blockers correctly

Run:

```sh
./bin/cadre sdlc validate --root <path>
```

Parse the `errors`/`blockers` JSON yourself. They are not the same thing and
must not be treated alike:

- **`errors`** mean the configuration is genuinely wrong (`valid: false`).
  These always have to be fixed before you finish.
- **`blockers`** mean a decision is still open (`ready: false`). A deferred
  authority lands here *by design*. It is not a failure and must not be
  reported to the human as one.

Translate each one into plain language rather than showing the raw message:

- `"authority <role> is unresolved"` → if you deliberately deferred it, say
  nothing and move on. Re-ask only if it is a role the human's own next gate
  needs.
- `"conditional authority applicability <role> is unresolved"` → same:
  deferred until its gate comes into view.
- `"environment persistence is unknown: <name>"` → "Is your `<name>`
  environment temporary/disposable, or does it stick around?"
- `"impact applicability is unknown: <id>"` → re-ask the relevant Step 7
  question.
- `"detected project commands are not confirmed"` → return to Step 6.

Finish when `valid: true` and the only remaining blockers are deferred
authorities or genuinely release-time decisions. Do **not** loop until
`ready: true` — on a deliberately deferred setup that state is not reachable
at onboarding time, and chasing it re-creates the thirteen-question
interview this step exists to avoid.

Report completion in prose: "Lifecycle tracking is set up and valid. Eleven
sign-off roles are still open — that's expected, and I'll ask about each one
when you first hit the gate that needs it."

## Resolving a deferred authority later

When a task reaches a gate whose authority is still unresolved, resolve it
then: ask that gate's question from the Step 4 table, write `status:
"assigned"` and `assignee` into `.agentic-sdlc/authorities.json`, and carry
on. The `brief-pending-gates-github` skill reports which gates are waiting
and on whom; `lifecycle-review-generic-github` records the decision once a
human makes it (`lifecycle-review-github` instead when the decision is
backed by a real PR).

**Assign the role before planning the task that needs it.** A run record
snapshots each gate's authority applicability when the record is first
created, so a role assigned afterwards does not retroactively unblock a task
that was already planned. Neither re-running `plan` with the same task ID nor
`reenter` refreshes that snapshot — that task needs a new task ID. Check
before planning and this never comes up.

Translate one error here carefully. Approving a gate whose authority was
unresolved at plan time fails with `"<gate> authority role <role> is not
applicable"`. That wording is misleading: it does *not* mean the gate is
inapplicable to this project. It means the authority was not assigned when
the task was planned. Tell the human the second thing.

## Throughout

- Never show raw JSON, YAML, or CLI flags to the human unless they
  explicitly ask to see the underlying files (you may mention file paths
  for their own audit-trail reference).
- Summarize progress and next steps in prose after each step.
- If the human seems to want engineer-level detail instead, point them at
  `docs/lifecycle-and-plugin-operations.md` and `roster/RUNBOOK.md` §16 for
  the direct CLI reference and stop running this conversational flow.
