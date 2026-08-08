# Requirements Baseline — Proposal 01: Stop discarding route match reasons

**Requirements ID:** `REQ-CADRE-PROPOSAL-1`
**Revision:** 1 (initial)
**Status:** draft — awaiting human review
**Author (agent):** requirements-agent, consolidated by the orchestrating session
**Date:** 2026-08-08
**Repository:** `/home/deagy/sdk/cadre`
**Classification:** internal
**Decomposes:** `INTENT-CADRE-PROPOSAL-1` (`product-intent.md`, Revision 1)

---

## 0. Grounding

Every claim below was verified by reading the tree at the time of writing, not
inferred from the proposal text. Two corrections to the proposal's own framing
are load-bearing and recorded here rather than silently applied:

1. **The proposal's "roughly ten lines" estimate covers the emission only.**
   Its headline wording ("mirror the `matched_risks` shape at
   `build_dispatch_plan.py:545`") describes changing `matched_routes`'
   element type in place. That is what shipped, and the emission is indeed one
   line — but the retype has a real blast radius across consumers, tests, and
   the published contract (§3). The line count was never the cost.
2. **There is no purely additive field on this schema, and `schema_version`
   must be bumped.** An early analysis pass said as much; a later pass
   overrode it, arguing `additionalProperties: false` merely means the schema
   must *learn* the new property. **The first pass was right.** The schema is
   vendored away from the producer — into the pip wheel (`pyproject.toml`'s
   `cadre_cli/_vendor/...` force-include) and the plugin distribution — so a
   consumer validates a freshly generated plan against whatever copy they
   installed. A pinned older copy rejects a document carrying an unknown
   property, and without a version bump the plan reports the exact
   `schema_version` that copy claims to handle: a silent failure naming the
   wrong cause. This was verified while an intermediate revision added a
   sibling field: a plan from that revision failed the previous v3 schema with
   *"Additional properties are not allowed"* while self-reporting
   `schema_version: 3`. The final retype breaks a pinned v3 copy just as
   surely, which is why the bump is not optional.

   This also means `dispatch_disposition`'s precedent — added to `required`
   with no bump, recorded in `CHANGELOG.md` as "Additive and non-breaking" —
   was itself incorrect, for the same reason. It held for readers of the
   producer's output, not for a pinned schema copy. `RUNBOOK.md` now states
   when `schema_version` increments so the argument is not re-litigated per
   field.

## 1. Traceability

| Intent section | Requirements |
|---|---|
| §2 problem, §4 outcome | R1, R2 |
| §5 success criteria | R2, R3, R7, R8, R9 |
| §6 decisions | R3, R4, R5, R5b |
| §6 telemetry, §7 non-goals | R5, R10 |
| §8 out of scope | R6 |

## 2. Functional requirements

**R1 — Emit route match reasons.**
`matched_routes` becomes one object per matched route, `{"id": <route id>,
"reasons": {"keywords": [...], "keyword_groups": [...], "paths": [{"pattern":
..., "file": ...}, ...]}}`, built with the existing `_reasons()` helper
(`build_dispatch_plan.py:105-110`) reused unchanged — byte-for-byte the same
construction `matched_risks` already uses.
*Acceptance:* for a task matching ≥1 route, each entry's `reasons` equals what
`match_rule()` produced for that route. Pinned by
`RouteMatchReasonTests::test_keyword_match_names_the_keyword_that_fired` and
`::test_path_match_names_the_pattern_and_the_file` in `test_selector.py`.

**R2 — Every entry carries exactly `id` and `reasons`.**
No entry is a bare string, and none carries a third key — routes and risks
must stay one shape (R4).
*Acceptance:* `::test_every_entry_carries_an_id_and_reasons`;
`::test_empty_when_nothing_matches` covers the empty case.

**R3 — Breaking retype of `matched_routes`; `schema_version` goes 3 → 4.**
Both compatibility directions break, and the version bump is what makes that
legible instead of silent:

- *Readers of the producer's output* must project ids: `set(plan[...])`
  becomes `{r["id"] for r in plan[...]}`. Every in-repo consumer is updated
  under R5/R5b.
- *Validators holding a pinned schema copy* break because the schema is closed
  and vendored (§0.2) — true for any change to the emitted field set, retype
  or addition alike.

*Acceptance:* the emitted `schema_version` and the schema's `const` both read
`4`; a plan from this branch is confirmed to *fail* the previous v3 schema,
proving the bump is meaningful rather than cosmetic; and no
`fixtures/selection_golden_corpus.json` block is edited — the corpus keeps
pinning route *ids*, with the comparison projecting them from the richer
entries (§5).

**R4 — One schema definition backs both fields.**
The `reasons` shape was inlined inside `matched_risks`. It is lifted into
`$defs/matchReasons`, wrapped by `$defs/idWithReasonsArray`, and `$ref`d from
both `matched_routes` and `matched_risks` — rather than duplicating ~30
lines, which would let the two shapes drift.
*Acceptance:* both fields `$ref` the same definition;
`::test_route_and_risk_reasons_share_one_shape` asserts the emitted key sets
are identical across both fields at runtime, so a split `$ref` fails a test and
not just review.

**R5 — Telemetry records keep their existing shape.**
`selection_telemetry.py` now projects route ids the same way it already
projected risk ids, so a record's `matched_routes` stays a bare string array
and its `Counter` aggregation needs no change. Reasons must **not** reach a
record: `reasons.paths[].file` entries are changed-file paths, and `RUNBOOK.md`
limits records to structural facts precisely so raw paths stay out of a
plaintext local log. That rationale is now stated in `RUNBOOK.md` itself, so
the omission reads as a decision rather than an oversight.
*Acceptance:* a record still contains `matched_routes: [<string>, ...]` and no
`reasons`; `test_selection_telemetry.py` asserts the record's ids equal the
ids projected from the plan.

**R5b — Every other in-repo reader projects ids.**
`team_recipe_dryrun.py`'s `set(plan["matched_routes"])` and its test
counterpart become `{route["id"] for route in ...}`; the golden corpus
comparison projects ids from the richer entries rather than comparing raw.
*Acceptance:* the full orchestration suite passes; no fixture block is edited.

**R6 — Kernel contracts are a different artifact, not a stale copy.**
`kernel/contracts/selection.schema.json` describes the *kernel's own* portable
dispatch plan (`kernel/agentic_sdlc/__init__.py:1777-1793`), not `cadre
select`'s. Same filename, different producer: `schema_version: 2` with a
required `gate_dispatch`, versus roster's `4` with `teams`,
`lifecycle_tracking`, and `dispatch_disposition`. `select_agents.py` never
writes into `.agentic-sdlc/runs/`, so a roster plan never reaches the kernel
validator at `__init__.py:2318`. Verified: the kernel's emitted key set equals
its schema's `required` list exactly, and `agentic-sdlc validate` on a freshly
planned project returns `"valid": true, "errors": []`. The filename collision
reads as version skew and has already invited one "fix" that would have broken
a working contract — hence intent §8.
*Acceptance:* this change touches no path under `kernel/` except the
regression tests added for G-3.

## 3. What the retype cost

Every consumer that treated `matched_routes` as a list of strings. Recorded so
the migration is auditable, and because an intermediate revision avoided this
list entirely with a sibling field before the retype was taken deliberately
(intent §6). Each row below was observed as a real failure, then fixed:

| Consumer | Failure |
|---|---|
| `team_recipe_dryrun.py:323` | `set(plan["matched_routes"])` → `TypeError: unhashable type: 'dict'` on every invocation |
| `selection_telemetry.py:137` | silently writes dicts into telemetry records |
| `fixtures/selection_golden_corpus.json` | ~60 hand-maintained blocks rewritten; **no generator script exists** — the loop is run-test, read-diff, hand-edit |
| `test_selection_golden_corpus.py:147` | route-id set comprehension over unhashable dicts |
| `test_team_recipe_dryrun.py:416`, `test_selection_telemetry.py:114,218,234,250` | string-shape assertions |
| `selection.schema.json` | `schema_version` 3 → 4, plus every external validator |

## 4. Non-functional requirements

**R7 — Determinism.**
The reasons sit inside the fingerprinted payload, so unstable
ordering would make `dispatch_fingerprint` non-reproducible. No new ordering
logic is needed: `match_rule()` builds `keywords`/`keyword_groups` in
`routing.yaml` declaration order and `paths` in pattern-then-`changed_files`
order — the same path `matched_risks` already relies on.
*Acceptance:* `::test_reasons_are_deterministic_across_identical_calls` asserts
two identical `build_dispatch_plan()` calls produce identical reasons JSON and
an identical `dispatch_fingerprint`.

**R8 — Fingerprint churn is expected and documented.**
Every plan's `dispatch_fingerprint` changes, because the emitted field set
changed. `RUNBOOK.md`'s fingerprint passage now states that fingerprints are
comparable only between plans from the same producer version, so this is not
misread as a determinism regression.
*Acceptance:* `RUNBOOK.md` carries the statement; `CHANGELOG.md` names the
consequence explicitly.

**R9 — Schema rejects malformed reasons.**
*Acceptance:* `test_selection_schema_rejects_malformed_closed_contracts` gains
two cases — a non-array `reasons.keywords`, and a missing `reasons` key — both
of which must fail validation.

**R10 — Documentation updated in lockstep.**
- `docs/sample-selection-output.md` — the worked example carries the real
  regenerated `matched_routes` block, plus a glossary entry explaining
  when to reach for it. Note this page is **not** asserted by any test
  (`grep` for it across `roster/orchestration/test/` and `plugin/tools/`
  returns nothing) despite describing itself as pinned byte-for-byte; it is
  therefore updated by hand from real regenerated output.
- `roster/workflows/unclassified.md` — step 1 now points at the field that
  actually answers the question it asks.
- `docs/terminology.md` — the `Route` glossary entry.
- `roster/RUNBOOK.md` — fingerprint comparability (R8) and the telemetry
  rationale (R5).
- `plugin/suite/**` — regenerated, never hand-edited.
*Acceptance:* `cadre generate-plugin --output plugin --check` reports no drift.

## 5. Deliberately unchanged

- The two committed example plans under `roster/orchestration/examples/`
  (`sample-plan.json`, and the `design-resolution-plan.json` inside the sample
  task archive) — frozen `schema_version: 2` artifacts. Nothing validates them
  against the current schema. Retrofitting a v3 field onto a v2 archive would
  falsify it. Their paths are deliberately not spelled out here:
  `test_repository_health.py::test_sample_references_are_limited_to_allowed_archives`
  fails any tracked file outside the allowlist that names the sample task id
  literally, so this record describes them instead of citing them.
- `roster/orchestration/mcp/dispatch_server.py` — its `matched_route_ids`
  parameter is caller-supplied, not read from `plan["matched_routes"]`. Since
  `matched_routes` is unchanged, its docstring stays accurate.

## 6. Out of scope

- Numeric confidence/score/weight/ranking on a match, under any field name
  (intent §7 — rejected on the deterministic-selection invariant).
- Near-miss / why-a-route-didn't-match reasoning (Proposal 07).
- Tightening `_keyword_matches`' substring boundary (Proposal 05). This change
  makes the behaviour visible and pins that visibility with a test; combining
  the two would erase the evidence that it works.
- Any change to `matched_risks`' computation or shape.
- `cadre doctor` (Proposal 06) and `cadre select --explain` (Proposal 07).
- Repairing `kernel/contracts/selection.schema.json` (R6).

## 7. Known gaps

- **G-1: `docs/sample-selection-output.md` has no drift guard.** The page
  claims to be pinned byte-for-byte by `test_selection_golden_corpus.py`; no
  such assertion exists. This change updated it by hand and left the gap open
  rather than widening scope. Worth filing — it is the same recurring
  stale-documentation failure shape Proposal 02 targets.
- **G-3: no kernel test asserted that `validate` succeeds — now closed.** Every
  `validate` invocation across `kernel/test/` passed `expected=1`, i.e. each one
  injected a specific defect and asserted it was caught. Nothing exercised the
  clean path, so a genuine disagreement between the kernel's plan producer and
  `kernel/contracts/selection.schema.json` would have shipped silently — and
  doubly so, since schema validation is skipped entirely when `jsonschema` is
  absent (`kernel/agentic_sdlc/__init__.py:2318`). This is precisely the blind
  spot that made the retracted §8 claim plausible enough to survive review.
  Closed by `test_planned_project_validates_clean_against_selection_schema`.

- **G-2: CLAUDE.md's single-test invocations are stale.** `python3 -m unittest
  agents.orchestration.test.test_selector` fails with `ModuleNotFoundError: No
  module named 'agents'` — the directory was renamed to `roster/`. Encountered
  while running this work's own tests. Not fixed here (unrelated to the
  proposal), but it misleads anyone following the documented commands.
