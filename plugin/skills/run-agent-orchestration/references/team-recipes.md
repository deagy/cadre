# Team Recipes

Three team compositions drawn from signals already present in this repo — not
invented groupings. Each is now also a deterministic entry in
`roster/orchestration/routing.json`'s `team_recipes` list: `cadre select`
evaluates the same trigger described here and, when it matches, emits the
team in its `teams` field with a members/role list already intersected with
whichever agents routing actually selected — no team recipe ever pulls in an
agent that wasn't already going to be dispatched. Treat that emitted `teams`
entry as the trigger source of record; this document adds the operational
detail the selector can't decide (each teammate's distinct focus, how the
lead synthesizes, file-ownership assignment). See
[runner-adapters.md](runner-adapters.md) for how to actually spawn these on
each runner, what `communication_mode`/`fallback` mean, and what changes on
Codex.

## Parallel review team

**Roles**: `code-reviewer` + `infrastructure-reviewer` +
`pipeline-security-reviewer` + `supply-chain-security-reviewer`.

**Selector trigger**: `team_recipes` id `parallel-review` in `routing.json` —
fires when 2 or more of `frontend`/`backend`/`infrastructure`/`pipeline`/
`supply-chain` routes match and at least 2 of the four roles above are
already selected; the emitted `teams` entry's `members` is that intersection,
not always all four.

**When**: a change touches multiple review-relevant surfaces at once
(application code, infrastructure, pipeline, dependencies). This is exactly
the group `roster/RUNBOOK.md`'s own implementation/review sequence already
lists together ("Code reviewer + Infrastructure reviewer + Pipeline security
reviewer + Supply chain security reviewer") — today dispatched as an ordinary
parallel wave; a team lets them challenge each other's findings before you see
the consolidated list.

**Each teammate's focus** (per their `AGENT.md`): `code-reviewer` —
correctness, security, maintainability, tests of the exact revision;
`infrastructure-reviewer` — IaC security, correctness, resilience, drift;
`pipeline-security-reviewer` — CI/CD trust boundaries, runner/token/artifact
controls; `supply-chain-security-reviewer` — dependency/SBOM/provenance/
signing/image risk.

**Synthesis**: the lead consolidates all four into one severity-ordered
findings list, same as an ordinary wave's "Consolidate Results" step — the
difference a team adds is teammates can flag interactions across each other's
domains first (for example, `pipeline-security-reviewer` noticing that an
infrastructure change `infrastructure-reviewer` approved actually widens CI
runner exposure).

**Downstream — do not fold into this team**: `security-reviewer` and
`compliance-reviewer` stay a *separate, sequential* step after this team's
findings synthesize. `RUNBOOK.md` documents this ordering explicitly
("Security reviewer -> Compliance reviewer"): compliance-reviewer's control
mapping depends on security-reviewer's consolidated risk assessment, so it
can't run as an independent peer in the same team.

**Gates**: G6–G8 (per `routing.json`'s `infrastructure` and `pipeline` routes).

## Cross-stack build team

**Roles**: `frontend-engineer` + `backend-engineer` +
`infrastructure-provisioner` + `cicd-engineer`.

**Selector trigger**: `team_recipes` id `cross-stack-build` in
`routing.json`, sharing its trigger with the existing `cross_stack` block
(2 or more of `frontend`/`backend`/`infrastructure`/`pipeline` routes match
the same task) — that block separately adds any of `frontend-engineer`/
`backend-engineer` not already selected as `primary` into `support` (a
no-op when, as in the common case, both are already primary); the team
recipe additionally surfaces the matching engineers themselves as a named
team. That shared trigger is this repo's own existing evidence these four
roles' work is independent and commonly concurrent — `RUNBOOK.md`:
"Implementation roles may work concurrently after architecture and threat
requirements are stable."

**Each teammate's focus**: build only their own layer — `frontend-engineer`
(React/TypeScript UI), `backend-engineer` (Go/PostgreSQL service),
`infrastructure-provisioner` (OpenTofu/Helm/Kubernetes),
`cicd-engineer` (pipeline for the new artifact). Cross-stack contract
questions are the teammates' own direct coordination — `application-engineer`
is not part of this path at all; it is scoped to this suite's own tooling,
not a target project's application (see its `AGENT.md`).

**Synthesis**: before spawning, the lead assigns each teammate a disjoint file
set — two teammates editing the same file causes silent overwrites, exactly
the failure mode Claude Code's own agent-teams guidance warns about. After
completion, hand the combined output to the parallel review team above.

**Gates**: varies by which routes matched (G3–G8).

## Competing-hypotheses debugging team

**Roles**: `debugging-engineer`, spawned 2–4 times — this is the one recipe
built on multiple instances of a *single* role pursuing different theories,
not multiple different roles.

**Selector trigger**: `team_recipes` id `competing-hypotheses-debugging` in
`routing.json`, `type: dynamic` — fires when the `debugging` route matches,
`debugging-engineer` is selected, and the task text carries an
intermittent/flaky/recurring/unconverged signal. The emitted `teams` entry
gives a `role` and an `instances: {min: 2, max: 4}` range, not fixed
membership or named hypotheses — those are decided at dispatch time, below.
A plain "debug this and find the root cause" task without that signal does
not trigger this recipe; it dispatches a single `debugging-engineer` as
usual.

**When**: `roster/workflows/debugging.md`'s root-cause loop hasn't converged
on one explanation from a single investigation, or the failure is
intermittent/environment-dependent enough that more than one theory is
plausible.

**Each teammate's focus**: one specific, named hypothesis assigned in the
spawn prompt (for example: "race condition in the connection pool," "stale
cache TTL," "upstream rate limiting") — naming them explicitly up front keeps
teammates from converging on the same theory.

**Synthesis**: unlike the other two recipes, this one is designed for active
mid-investigation challenge, not independent reporting — each teammate's job
includes trying to disprove the others' theories. The lead's role is to keep
that debate happening (prompt teammates to review each other's evidence)
rather than just collecting separate reports and picking one.

**Guardrail**: each teammate still operates under `debugging-engineer`'s
normal authority — reproduce, diagnose, apply the smallest safe fix; no
teammate may approve its own fix. Independent review is still required
afterward, per `debugging.md`.

**Gates**: none directly (debugging is typically pre-gate or gate-agnostic
root-cause work); the resulting fix still goes through the normal review
chain.

## On Codex CLI

None of the "synthesize via peer challenge" mechanics above are available —
see [runner-adapters.md](runner-adapters.md). Run the same role list as an
ordinary parallel wave on Codex, and perform the challenge/reconciliation step
yourself as the orchestrating session. For the debugging recipe specifically:
collect each spawned instance's hypothesis and evidence, then reason about
contradictions between them yourself before proposing a fix — Codex has no
way to let the instances do that directly.
