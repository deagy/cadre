# Product Intent Record — Stop discarding route match reasons

**Intent ID:** `INTENT-CADRE-PROPOSAL-1`
**Revision:** 1 (initial)
**Status:** draft — awaiting human review
**Author (agent):** product-intent-agent, consolidated by the orchestrating session
**Date:** 2026-08-08
**Repository:** `/home/deagy/sdk/cadre`
**Classification:** internal
**Source:** Feature Proposal 01 of the `cadre-review-brainstorm-2026-08-08` eight-agent repository review, ranked first by value-per-effort in the "Do now" tier.

---

## 0. Authorship note (read before the rest)

This record was written **alongside** the implementation, not before it. It is a
record of intent and scope, not evidence that intent was reviewed prior to
build. The agent that authored the change also authored this record, so under
this repository's authorship/approval separation invariant it carries **no
approval authority** — a human reviewer decides whether the intent below was
the right one. Section 6 states the decisions a reviewer should specifically
push back on.

## 1. Owner

**Accountable Product Owner:** not designated. Consistent with the finding
already logged in the sibling idea #7 and #10 intent records, nothing in
`team-profile.yaml`, `AGENTS.md`, `CLAUDE.md`, or `RUNBOOK.md` names a Product
Owner for this repository's own feature backlog. Not assumed here.

**Working owner for authorship:** the orchestrating session acting on the
review's Proposal 01. Authorship does not confer approval.

## 2. The user problem

A person debugging why `cadre select` routed a task the way it did cannot
answer "why did this route match?" from the plan. `match_routes()`
(`roster/orchestration/src/routing.py:159-166`) computes a full `reasons`
record for every matched route — the literal `keywords` that hit, any
conjunctive `keyword_groups` that were satisfied, and the `paths` as
`pattern`/`file` pairs — structurally identical to what risk rules produce.
`build_dispatch_plan()` then keeps it for risks and throws it away for routes,
on adjacent lines:

```python
"matched_routes": [match["id"] for match in matched_routes],                                     # reasons discarded
"matched_risks":  [{"id": match["id"], "reasons": _reasons(match)} for match in matched_risks],  # reasons kept
```

The cost was measured, not hypothesised. During the review that produced this
proposal, working out why the `pipeline` route fired on a UX-phrased task
required reading `routing.py` to discover that its `"runner"` keyword
substring-matches `"cross-runner"`. The plan named the route and stayed silent
on the trigger.

This is sharpened by `roster/workflows/unclassified.md`, which already
instructs a reader to "read `matched_routes` … to understand what was actually
matched" — pointing at a field that structurally cannot answer the question it
is being cited for.

## 3. Users and beneficiaries

Traced to concrete consumers rather than left generic:

- **A human reading plan JSON** while diagnosing an unexpected or missing
  route. The primary beneficiary, and the one the measured cost above landed on.
- **An agent following `unclassified.md`'s step 1**, which sends the reader to
  the plan to understand an unrecognized route/risk combination.
- **A maintainer deciding whether a routing rule is too broad.** "Matched by a
  specific path pattern" and "matched by one generic keyword" are very
  different evidence for that judgement, and today the plan renders them
  identically.
- **Proposals 06 (`cadre doctor`) and 07 (`cadre select --explain`)**, both of
  which need route reasons to exist in the plan rather than be re-derived.
  This proposal is a prerequisite for both.

Explicitly **not** a beneficiary: `roster/orchestration/src/selection_telemetry.py`.
See §5.

## 4. Intended outcome and observable change

Each entry in a `cadre select` plan's `matched_routes` gains its own
`reasons`: the array changes from route-id strings to `{"id", "reasons"}`
objects, the same shape `matched_risks` already emits. A reader can see which
keyword, keyword group, or path pattern caused each route match without
opening `routing.yaml` or any source file.

Concretely, the case that motivated this now reads directly off the plan:

```json
{"id": "pipeline", "reasons": {"keywords": ["runner"], "keyword_groups": [], "paths": []}}
```

## 5. Success criteria (observable)

1. For a task matching a route on a keyword quirk, the plan names the keyword.
   Verified on the original `cross-runner` case and pinned by a test.
2. Routes and risks are read the same way: one field each, one shape, so a
   consumer that can interpret either can interpret both.
3. That shape is provably one thing — a single schema definition backs both,
   so they cannot drift.
4. Every consumer that needs bare ids is updated to project them, and the
   golden corpus keeps asserting *which* routes matched without hand-encoding
   every pattern/file pair across ~60 fixtures.
5. Telemetry records are byte-shape-unchanged.
6. Proposals 06 and 07 can be built by *reading* this field rather than
   re-deriving it.

## 6. Decisions a reviewer should push back on

- **`matched_routes` was retyped rather than given a sibling field.** The
  first implementation added `matched_route_reasons` alongside an unchanged
  `matched_routes`, to avoid a breaking change. That bought compatibility at
  the cost of asymmetry — two fields for routes, one for risks, with the
  first strictly derivable from the second — and it was a deferral, not a
  resolution: whoever later wanted symmetry faced the same retype plus a
  second version bump. Since the version bump below was already required
  (see the next item), the retype rides along for free and was taken now.
  **The cost is real and paid here:** `team_recipe_dryrun.py` and
  `selection_telemetry.py` now project ids explicitly, and the golden corpus
  compares projected ids rather than raw entries. A reviewer who would rather
  have kept `matched_routes` as strings for consumer stability should say so.
- **`schema_version` goes 3 → 4.** An earlier revision claimed a sibling
  field avoided this, on the `dispatch_disposition` precedent. Wrong: the
  schema is closed *and* vendored to consumers, so any change to the emitted
  field set breaks a pinned copy regardless of `required` membership. See
  `requirements.md` §0.2 — the bump costs one integer and a regenerate, and
  buys an in-band signal instead of a silent rejection.
- **Every `dispatch_fingerprint` changes.** Unavoidable for any change to the
  emitted field set, and correct behaviour, but it is a visible consequence.
- **Reasons are withheld from telemetry deliberately.** `reasons.paths[].file`
  entries are changed-file paths, and `RUNBOOK.md` limits telemetry records to
  structural facts precisely to keep such paths out of a plaintext local log.
  Recorded so a later reader does not "fix" the omission.

## 7. Non-goals

- **No numeric confidence, score, weight, or ranking on a match, under any
  field name.** The review rejected this explicitly: it would erode the stated
  invariant that selection is deterministic, not agent judgment. The
  qualitative *why* is the entire deliverable, and a renamed
  `match_strength`/`certainty` field would smuggle the rejected design back in.
- **No near-miss reasoning.** Why a route did *not* match is Proposal 07.
- **No change to `_keyword_matches`' substring behaviour.** This change makes
  that behaviour visible; correcting it is Proposal 05. Combining them would
  make the diff unreviewable and would erase the evidence that the visibility
  works.
- **No change to how `matched_risks` is computed or shaped** — it is the
  template, not the subject.
- **No `cadre doctor` or `--explain` CLI surface** — Proposals 06 and 07.

## 8. Two same-named schemas

`kernel/contracts/selection.schema.json` and
`roster/orchestration/selection.schema.json` share a filename and describe
different artifacts from different producers. Recorded because the collision
reads as version skew and invites a "fix" that would break a working contract:

| | `kernel/contracts/selection.schema.json` | `roster/orchestration/selection.schema.json` |
|---|---|---|
| Producer | the kernel's own `plan` command (`kernel/agentic_sdlc/__init__.py:1777-1793`) | `cadre select` |
| `schema_version` | `2` | `4` |
| Distinctive fields | requires `gate_dispatch` | `teams`, `lifecycle_tracking`, `dispatch_disposition` |
| Validated at | `.agentic-sdlc/runs/<id>/dispatch-plan.json` (`__init__.py:2318`) | plan output |

The kernel's emitted key set is *exactly* its schema's `required` list — 15
keys, no more, no less — and `agentic-sdlc validate` on a freshly planned
project returns `"valid": true, "errors": []`. `select_agents.py` contains no
reference to the overlay, so a roster plan never reaches the kernel validator.

The one real gap found while checking this is recorded as G-3 in
`requirements.md`: no kernel test asserts that `validate` *succeeds*, so real
producer/schema drift inside the kernel would go uncaught.
