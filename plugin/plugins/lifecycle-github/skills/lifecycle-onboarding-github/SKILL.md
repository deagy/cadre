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

## Step 4 — Authorities interview

`.agentic-sdlc/authorities.json` has one entry per role. Ask one
plain-language question per **required** role instead of the technical
name:

| Role | Plain-language question |
| --- | --- |
| `product_owner` | Who decides what this project should actually do — final word on scope and priorities? |
| `engineering_lead` | Who has final technical sign-off on how it's built? |
| `system_architect` | Who approves the overall design/architecture? |
| `governance_lead` | Who's accountable for policy/compliance decisions here? |
| `security_lead` | Who signs off on security-sensitive changes? |
| `release_owner` | Who confirms a release is actually ready to ship? |
| `release_authority` | Who has final say on whether this can go live? |
| `service_owner` | Once it's running, who's responsible for it day-to-day? |

For each, set `status: "assigned"` and `assignee` to the identity they give
you (a name, email, or handle — whatever they naturally offer; ask for a
GitHub login or GitLab username too only if they said they want
GitHub/GitLab-review-backed approvals in Step 6).

If the human says the same person (often themselves) holds several or all
of these roles, tell them explicitly that this is valid and normal for a
solo maintainer or small team — the kernel's author/reviewer separation
check applies to agent roles assigned to a route, not to which human holds
which named authority.

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

Then, for the 5 **conditional** roles, ask a gating yes/no question first
and only ask for an assignee if the answer is yes:

| Role | Gating question |
| --- | --- |
| `data_control_owner` | Does this project store or process personal/customer data? |
| `human_key_owner` | Does this project manage its own encryption keys or certificates? |
| `uat_product_owner` | Is there a separate user-acceptance-testing phase with its own stakeholder, distinct from the product owner? |
| `implicated_security_lead` | Is this itself a deployed, running service (not just a library/tool)? |
| `implicated_governance_lead` | (same as above) |

If no: set `applicability: "not-applicable"` with a short `rationale`
sentence built from their answer (e.g. "Project holds no customer data").
If yes: set `applicability: "applicable"`, `status: "assigned"`, and
`assignee` to who they name.

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
subcommand takes `--target <path>` (not `--root`), and always previews by
default — it only writes when `--force` is also passed.

Start from the defaults. `cadre init` keeps every shipped default unless
told otherwise, so the common case needs no answers at all:

```sh
./bin/cadre init --target <path>
```

That writes nothing, and it is not a half-finished run: the shared overlays
are sparse, so "keep the default" means "write no overlay for that field",
and a project with no overlay resolves to exactly the shipped values. Say so
plainly ("nothing to change — it'll use the standard policy") rather than
implying a step was skipped.

When the human does want something different, override just those fields
with `--set [REGION:]PATH=VALUE`, which is repeatable:

```sh
./bin/cadre init --target <path> --set platform.hosting_model=cloud --force
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

## Step 9 — Validate and loop

Run:

```sh
./bin/cadre sdlc validate --root <path>
```

Parse the `errors`/`blockers` JSON yourself. For each blocker, translate it
back into a plain follow-up question or explanation instead of showing the
raw message, for example:

- `"authority <role> is unresolved"` → re-ask that role's question (it was
  likely skipped).
- `"environment persistence is unknown: <name>"` → "Is your `<name>`
  environment temporary/disposable, or does it stick around?"
- `"impact applicability is unknown: <id>"` → re-ask the relevant Step 7
  question.
- `"detected project commands are not confirmed"` → return to Step 6.

Loop until `valid: true` and either `ready: true`, or the only remaining
blockers are legitimately human/release-time-only (e.g. a specific G9
deployment authorization pending an actual release) — explain those as
"expected, and will resolve itself when you actually ship," not as
something broken.

## Throughout

- Never show raw JSON, YAML, or CLI flags to the human unless they
  explicitly ask to see the underlying files (you may mention file paths
  for their own audit-trail reference).
- Summarize progress and next steps in prose after each step.
- If the human seems to want engineer-level detail instead, point them at
  `docs/lifecycle-and-plugin-operations.md` and `roster/RUNBOOK.md` §16 for
  the direct CLI reference and stop running this conversational flow.
