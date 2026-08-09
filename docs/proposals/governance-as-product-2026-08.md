# Proposal: make the governance platform the product and the roster a plugin

Status: **BACKLOG — proposed, deliberately not scheduled.** Recorded now
because the question is live and the evidence is fresh; explicitly parked by
the Product Owner on 2026-08-09. Nothing here should be started without a
decision to bring it forward.
Task ID: `governance-as-product-2026-08-09`
Classification: internal
Author role: governance-planner (Cadre suite)
Requested by: repository owner / declared Product Owner
(`roster/shared/team-profile.yaml`)
Required approver: Product Owner. Nothing is approved by its presence here.

Answers the standing question "is Cadre an agent-prompt library or a governance
control layer?", open since the `cadre-review-brainstorm-2026-08-08` review and
carried unanswered through sixteen commits because it is a policy call rather
than a code path.

## The proposition

The product is the governance platform: the G1–G10 lifecycle kernel, the
orchestration engine, the selection and dispatch contracts, the skills, and the
knowledge-retrieval layer. A **roster** — roles, routing rules, and policy
profiles — is content that plugs into it. This repository's own 74 roles become
the reference roster, not the thing being sold.

## Why this is naming what already exists, not a new direction

Three pieces of evidence, all verified in the tree on 2026-08-09.

**The plugin seam is already built.** `provider/provider.json` is a manifest,
not a config file:

```json
{"schema_version": 1, "id": "cadre", "version": "0.3.0",
 "kernel_compatibility": {"minimum": "0.13.2", "maximum_exclusive": "1.0.0"},
 "agent_catalog": "agent-catalog.json",
 "profile_roots": ["profiles"], "extension_roots": ["extensions"]}
```

That is a roster declaring itself installable against a range of kernel
versions. A compatibility window only makes sense between separately versioned
things.

**The kernel already ships an example of the plugged-in thing.**
`providers/agentic-sdlc-defaults/` carries its own `provider.json`,
`agent-catalog.json`, and `profiles/`. The kernel was built expecting a
provider it did not author.

**The dependency already runs one way, and is enforced.**
`roster/orchestration/test/test_kernel_boundary.py` permits exactly two
couplings: shelling out to the kernel CLI, and reading `kernel/contracts/*.json`
as data. Roster asks; the kernel answers; never the reverse. That is the
platform/plugin relationship, already structural rather than conventional.

`CLAUDE.md` states the same thing in passing — Cadre "supplies a role catalog
and a `secure-cloud` provider profile *into* projects that adopt the kernel" —
without drawing the conclusion.

Adopting this framing is therefore cheap in architecture terms. It also
retroactively justifies the kernel's separate versioning and separate
publishability, which otherwise read as over-engineering for a single-consumer
component.

## The real problem it exposes: the seam is drawn in the wrong place

`roster/orchestration/` mixes platform machinery with roster content in one
directory. Today that is invisible because both ship together. Under this
proposal it is the whole of the work.

| Platform, currently inside `roster/` | Genuinely roster content |
| --- | --- |
| `orchestration/src/` — `select_agents.py`, `build_dispatch_plan.py`, `risk_classifier.py` | `catalog.yaml` and the 74 `<phase>/<role>/AGENT.md` files |
| `orchestration/mcp/` — the dispatch server and its core | `orchestration/routing.yaml` — the rules themselves |
| `orchestration/selection.schema.json`, `routing.schema.json` | `shared/*.yaml` / `*.md` policy defaults |
| `orchestration/handoff-contracts.md`, `task-brief-template.md`, `escalation-policy.md` | `provider/profiles/secure-cloud/` |
| `knowledge-store/` — retrieval infrastructure, schema, validator | |
| `shared/src/settings.py` — the operator-settings resolver | |

Two cases are worth calling out because they are where the argument is won or
lost.

**The knowledge store is the clearest.** Nothing about it is role content. The
handoff contract, `proposed-knowledge.schema.json`, and `staged_records.py`
landed in August 2026 as platform machinery — and landed *inside* the component
this proposal would make a plugin. Every future roster would want it, and none
of them should have to vendor a copy.

**`routing.yaml` is the genuinely hard one.** The rules are roster content: they
name specific roles and would differ entirely for a different roster. But
`routing.schema.json` and the selector that consumes them are platform. The
split is data-versus-engine, and it runs straight through a directory that is
currently one thing. Any credible version of this work starts here, not with
the easy cases.

## What would have to be true

Not a plan — the conditions a plan would have to satisfy, recorded so that a
later reader can judge whether the idea survived contact.

1. **`provider.json` becomes a real third-party contract.** Today it describes
   this repository's own bundle. It would need to specify what a roster must
   supply, what the platform guarantees in return, and how a roster declares
   the contract version it was written against — the same problem
   `kernel_compatibility` already solves in one direction.
2. **The selector must run against a roster it did not ship with.** Concretely:
   `cadre select` resolving `routing.yaml`, `catalog.yaml`, and the role
   definitions from an installed provider rather than from paths relative to
   the checkout. Until that works with a second roster, the seam is theoretical.
3. **A second roster must exist**, even a deliberately small one. A plugin
   architecture with exactly one plugin has never been tested; every assumption
   the reference roster happens to satisfy is invisible until something else
   does not.
4. **The distribution story must survive.** `plugin/` is roster-shaped: the
   marketplace serves subagent wrappers generated from `catalog.yaml`. If the
   platform is the product, what does a user install first, and what do the
   existing marketplace entries become?
5. **The boundary test must be extended, not just preserved.**
   `test_kernel_boundary.py` guards roster-to-kernel. A platform/plugin split
   needs the mirror guard: the platform must not reach into a specific roster's
   content. Nothing checks that today because nothing could violate it yet.

## The honest counterargument

The roster is what has visible value on day one. A governance platform with no
roles is a harder thing to adopt than 74 working specialists, and the existing
marketplace distribution is roster-shaped. So "governance is the product" can be
right about the architecture and wrong about the go-to-market at the same time,
and those two answers do not have to match.

There is also a naming consequence nobody has costed. "Cadre" currently names
the whole repository. If the platform is the product, either the platform takes
the name and the roster needs a new one, or the roster keeps it and the platform
is unnamed. `provider.json` already uses `"id": "cadre"` for the *roster*, which
suggests the second, and contradicts the README's framing.

## Non-goals

- **No directory moves, no refactor, no renames** are proposed here. This
  document exists to record the question, the evidence, and the conditions —
  not to start work.
- **No change to the kernel ownership boundary.** `kernel/` owns lifecycle gate
  schemas, run-record validation, and gate-authority semantics permanently, and
  this proposal strengthens rather than revisits that.
- **No claim that the roster is unimportant.** Reference content is a deliverable,
  not a demo.
- **No answer to the go-to-market question.** That is a separate decision and is
  explicitly left open above.

## Why this is in the backlog

The architecture already permits it, so nothing is degrading while it waits.
The cost of deferring is that platform machinery keeps accumulating inside
`roster/` — the knowledge-store work is the most recent example — and every
addition makes the eventual seam more expensive to draw. That is a slow cost,
not an urgent one, which is what makes deferring reasonable rather than risky.

If it is brought forward, condition 2 (the selector running against an external
roster) is the cheapest way to find out whether the rest is real, and should be
attempted before anything is moved.
