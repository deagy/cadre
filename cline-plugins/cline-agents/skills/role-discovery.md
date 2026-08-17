---
name: role-discovery
description: Conversationally help a new or occasional user figure out which of this repository's 159 specialist roles fit their task, and how to phrase a real `cadre select` call. Use when a user asks "which agent should I use," "who does X kind of work," "what role fits this task," or seems unsure how the role catalog or routing works.
canonicalSource: skills/role-discovery/SKILL.md
---

> Cline packaging note: this skill's instructions describe this repository's own `roster/`-layout tooling in the abstract (the role catalog, routing configuration, and selector this plugin bundles) -- they are not literal paths to look up in an arbitrary target project. When dispatching, use `start_subagent`/`dispatch_selected_roles`/`bin/cadre select` rather than reading these files directly.


# Role Discovery

Use this skill when someone does not yet know this suite's 159 roles well
enough to name one, or does not know how `cadre select`'s deterministic
routing actually decides. Your job is to hold a short conversation that ends
in either a confident role recommendation with a real `cadre select` command
they can run, or an honest "here's what's still unclear, here's what I'd ask
next."

This skill is a conversational **front end**, not a second selector. The
only authoritative, deterministic answer is `cadre select` itself
(`internal/selector`, driven by
this repository's bundled routing configuration). Never present a role you name in
conversation as final — always frame it as "this looks like the
`<route-id>` route, primary role `<role>`; run `cadre select` to confirm and
get the full plan (reviewers, support, gates)." If your read of the catalog
and the actual `cadre select` output ever disagree, the command output wins,
every time.

## Step 1 — Read the ground truth first

Before saying anything about roles, read the current bundled role catalog
(role inventory: `phase`, `capability`, `definition` path) and
this repository's bundled routing configuration (the `routes` list: `id`, `paths`,
`keywords`, `primary`/`reviewers`/`support`, `quality_gates`). Do this every
time — do not rely on role names or routes from your own memory or from an
earlier conversation, since both files are the single source of truth and
can change. If `docs/role-index.md` exists in the target repository, it is
a convenient human-readable grouping of the same catalog by lifecycle phase
(see also — it is not a separate source of truth, and this skill does not
depend on it existing).

## Step 2 — If the ask is vague, ask before answering

A surprising number of first questions are just "which agent do I need?"
with no other context. Do not guess at that point — ask 2-3 short
clarifying questions, only as many as you actually need:

- **What kind of work is this?** Building/implementing something new,
  reviewing existing work, writing or running tests, a security or
  compliance question, documentation, an operational/incident situation, or
  a design/architecture decision still to be made?
- **What kind of artifact does it touch?** Application code (and which
  layer — frontend, backend, both), infrastructure (Terraform/OpenTofu,
  Kubernetes/Helm, Talos), a CI/CD pipeline, a database/migration, docs, or
  something else (secrets/identity, policy-as-code, observability)?
- **Is this a decision or an execution?** Deciding *what* to build or *how*
  to design it (architecture, threat modeling, governance planning) versus
  actually building, fixing, or reviewing something that already has a
  direction?

Skip a question the user has already answered in their first message —
don't make them repeat themselves. If they only have a vague worry ("this
feels risky, I don't know who should look at it") rather than a task, that
itself is useful signal: ask what specifically feels risky (data handling,
production impact, secrets, external-facing surface) rather than pushing
for an artifact type they may not have yet.

## Step 3 — Match against real routes, and explain why

Once you have enough detail, look for a matching entry in
`routing.json`'s `routes` list: does the artifact type match a route's
`paths` glob, or does the described work match a route's `keywords`? State
the match plainly, for example:

> This sounds like the `backend` route (this repository's bundled routing configuration)
> — it matches on `**/*.go` and keywords like "api", "service
> implementation", "migration". Primary role: `backend-engineer`
> (its own role-definition file, phase `build`).
> Reviewers: `test-engineer`, `code-reviewer`.

If the description spans more than one route (e.g. a change that touches
both a Go backend and its Helm deployment), say so — name both routes and
both primary roles, and note that `cadre select` will select all
matching routes from the actual changed files, not just one.

If nothing in `routing.json` matches convincingly, say that plainly instead
of forcing a fit — `cadre select` itself returns `needs-triage` in that
case rather than guessing, and that is the correct, honest outcome to
surface, not a failure to paper over.

A few grounding examples worth knowing (verify these are still accurate
against the live files before repeating them, since routing evolves):

- "I need to design the system before any code exists" → `architecture-design`
  route, primary `cloud-architect` (phase `design`), reviewed by
  `threat-modeler`.
- "I want someone to review a pull request's Go changes" → `backend` route
  (or `frontend` for `.tsx`/`.ts`), reviewers `test-engineer` and
  `code-reviewer`.
- "Our GitLab pipeline needs a new stage" → `pipeline` route, primary
  `cicd-engineer`, reviewed by `pipeline-security-reviewer`.
- "I have a security question about how we manage secrets or RBAC" →
  `secrets-identity` route, primary `secrets-identity-engineer`.
- "A production incident just happened and needs coordinating" →
  `incident-response` route, primary `incident-commander`.
- "I want someone to write or improve docs" → `documentation` route,
  primary `technical-writer`.

Don't invent a role, phase, or capability that isn't actually present in
this repository's bundled role catalog — if you are not sure a name is real, re-read the
file rather than guessing from a plausible-sounding pattern.

## Step 4 — Hand them a real command, not just a name

Once there's a confident match (or even a partial one worth trying), give
the user a concrete `cadre select` invocation built from what they told
you, and explain what each part means in plain terms:

```sh
cadre select --task "<their objective, in their own words>" \
  --files "<comma-separated changed paths, or omit to use git status>" \
  --task-id "<short-slug>" --classification "<internal|confidential|restricted|public>"
```

Explain briefly: `--files` (or letting it default to git status/`--base`)
is what actually drives path-based route matching — the `--task` text only
matters for keyword matching, so an accurate `--files` list matters more
than wordsmithing the task description. If they don't know their
classification, suggest `internal` as the conservative default for internal
tooling unless something more sensitive applies, but don't decide it for
them if it's genuinely ambiguous — that's their call, not this skill's.

Make clear that running that command is the authoritative next step: it
will show the exact primary/reviewer/support roles, applicable quality
gates, and (if `agentic-sdlc` is available) lifecycle gate status — all of
which this conversation can approximate but not guarantee, since routing
data can change between this conversation and that command actually
running.

## Step 5 — Point past this skill for what happens next

This skill stops at "here's the role(s) and the command to confirm it."
Once the user has run `cadre select` (or is ready to actually dispatch the
identified role), hand off to
the `run-agent-orchestration` skill for
selection execution, knowledge retrieval, staged dispatch, and result
consolidation — do not duplicate that skill's dispatch-wave or
result-consolidation instructions here. If the user just wants to read a
role's own authority/inputs/outputs before deciding, point them at that
role's `AGENT.md` under its own role-definition file directly (and
`docs/role-index.md` if present, as a browsable overview across all of
them).

## Throughout

- Keep this a conversation, not a dump of the whole catalog — surface only
  the routes and roles relevant to what the user actually described.
- Never claim `cadre select` will return a specific plan before it has
  actually run; describe your read of the catalog as a well-grounded guess
  that the real command will confirm or correct.
- If the user seems to want the full reference instead of a conversation
  (e.g. "just show me every role"), point them at this repository's bundled role catalog and
  `docs/role-index.md` directly rather than pasting the whole list into
  chat.
