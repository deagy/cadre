# Implementation Plan — A roster-neutral platform

**Plan ID:** `PLAN-CADRE-PORTABLE-PLATFORM`
**Revision:** 11
**Status:** **four phases landed** (D, 0, A′, C′-1, C′-2). B′ and E remain, and
neither is blocked. G1 approved 2026-08-11 against intent Revision 1 and
re-affirmed against the current record the same day (`product-intent.md` §18).
**No open decision blocks anything, and G2 is approved** (2026-08-11, `requirements.md` §8). **All seven phases are landed.**
**Date:** 2026-08-11
**Implements:** `REQ-CADRE-PORTABLE-PLATFORM` (`requirements.md`, **Revision 11**)
**Decomposes:** `INTENT-CADRE-PORTABLE-PLATFORM` (`product-intent.md`, **Revision 9**)
**Revision note:** Revision 2 folded in the OD-2/OD-5 dispositions while still
citing `requirements.md` Revision 1 — a stale pin that would have landed a
reader on the retracted PP-NFR-3. Revision 3 fixed the pin and tracked that
baseline's corrections.

**Revision 4 fixes a line in Revision 3's Phase A that would have shipped the
exact vulnerability Phase A's own warning describes.** Revision 3 wrote that
`select_agents.py:18` and `:24` "do not change." Both are *derived from*
`ROSTER_ROOT` (`:17`), which Phase A makes resolver-driven — so changing
nothing redirects both, and a project-local `.agents/cadre.yaml` chooses which
`settings.py` the platform executes. They must be **rewritten**. Revision 4 also
drops OD-8 and Phase C's "violation 2" (a misreading of `_select_workflow()`),
restates Phase D as the regression pin it actually is, and replaces §2's
`git status` expectation, which was unsatisfiable in all three earlier
revisions.

**Revision 5 follows a review that ran the code, and two phases change size.**
**Phase E shrinks and changes shape**: `--provider` already reaches the kernel;
the work is suppressing Cadre's injected bundle, which collides with the
caller's. **Phase C grows substantially**: its one "small" fix changes every
lifecycle-aware Cadre plan (now **OD-9**, blocking), the boundary rule has
roughly nine more violations than Revision 4 counted, and a second selection
entry point — `cadre mcp-dispatch-server` — was missing from every earlier
revision. §2's `plugin/` diff is corrected for the fourth time, this time
against the generator rather than against reasoning about it.

**Revision 6 changes the phase order itself, which no previous revision
touched.** An eight-role review found that this plan violates its own stated
principle and double-counts its own work:

- **The order does not deliver cheapest-falsification-first.** Phase B cannot
  run until Phase A ships a resolver, a manifest, a schema, a packaging
  allowlist entry and eight constant rewrites. §0 quotes the parked proposal's
  *"before anything is moved"* and then moves everything first. **A new Phase 0**
  — a throwaway spike depending on none of that — is what the principle actually
  asks for.
- **Phase A and Phase C schedule the same edits.** `select_agents.py:203-204`
  and the `routing_overlay.py` / `mcp/*` path fixes appear in both. "Each phase
  is independently shippable" was false as written, and part of Phase C's
  Revision 5 growth was counting work twice.
- **Phase C is oversized by its own headline number.** Category B is **six**
  sites, not "roughly nine": five of the nine were `raise ValueError(...)`
  diagnostics that this plan's own category-C rule forbids touching.
- **Phase C's one fix is a functional prerequisite, not hygiene.** OD-9's third
  option is withdrawn — see §0.
- **Phase B's falsification is vacuous as specified**, reproducing the golden
  corpus's blind spot in the fixture built to detect it.
- **Phase D moves first.** It is zero-risk, depends on nothing, and is a
  tripwire for the exact mistake Phase A is most likely to make.
- **Phase E drops in priority.** `./bin/agentic-sdlc --provider <foreign>` already
  works today; the capability is not blocked, only the ergonomics.

Also corrected: §2's `plugin/` diff, for the **sixth** time — it gains a fifth
mirrored file under OD-9 option 1. Revision 6 stops correcting that list and
demotes it to a hint (`requirements.md` PP-NFR-1).

**Revision 7 records the Product Owner's dispositions of 2026-08-11 and shrinks
the plan accordingly.** Nothing is added; three things are removed.

- **OD-2 reversed to `SCOPE_GLOBAL_ONLY`.** Phase A loses the roster-identity
  field, the `selection.schema.json` bump, and the MCP restructuring — three
  items it had been carrying, two of them gated. **OD-7 and OD-10 are withdrawn
  rather than decided**, their subjects removed.
- **OD-9 resolved: a `default_gate_review_agents` key in `routing.yaml`.**
  Phase C′-2 is unblocked and is now a specified change rather than a pending
  decision. Cadre's plans stay byte-identical, so the ~15 `test_selector.py`
  assertions do not move.
- **OD-11 resolved: no compatibility window**, with unknown-`schema_version`
  rejection adopted as the mitigation — a small addition to Phase A.
- **OD-13 resolved**, so nothing procedural gates G2 either.

**Every phase is now schedulable.** The gating matrix at §3.1 is kept, with its
rows resolved, because the shape of what depended on what is the most reusable
thing this plan produced.

---

## 0. Read this first

Three decisions that gated this plan at Revision 1 were taken by the Product
Owner on 2026-08-11 (`product-intent.md` §16):

- **OD-1 — resolved.** The 2026-08-09 deferral is reversed; this proceeds.
- **OD-2 — resolved, then REVERSED on 2026-08-11.** It was project-local,
  overlay-style, conditional on roster identity surfacing in the plan
  (PP-FR-1b). It is now **`SCOPE_GLOBAL_ONLY`**, like every other path setting,
  with `--roster <path>` as the sole per-invocation redirect. The visibility
  control is retracted along with the exposure it answered. See
  `product-intent.md` §17.
- **OD-5 — resolved.** A **sibling `roster.json`** the kernel never reads.

**~~One new blocker was created by OD-2's answer.~~ WITHDRAWN at Revision 7.**
Surfacing roster identity would have added an emitted field, forcing
`selection.schema.json` 6 → 7 (PP-NFR-3b) — a change to a published, vendored
contract, raised as **OD-7**. OD-2's reversal removes the field, so there is no
bump and no decision. **Phase A no longer builds anything behind a gate**, which
is the single largest change Revision 7 makes to this plan.

**Revision 3 raised a second blocker (OD-8) and Revision 4 withdrew it.** It
claimed `_select_workflow()` classifies by Cadre route id, so a foreign roster
would always get `"unclassified"`. Its final stage (`build_dispatch_plan.py:254-265`)
reads each route's declared `workflow_shape` instead — roster-supplied
(`routing.schema.json:193-201`) — so a fixture roster classifies correctly with
no code change and no enum bump. **OD-7 is the only open decision gating this
plan.**

**A genuine second blocker was found at Revision 5: OD-9.** Phase C's
`code-reviewer` fix removes an agent from **every** lifecycle-aware Cadre plan's
`support` list, and the golden corpus is structurally unable to notice. Whether
to preserve Cadre's output by moving the default into roster data, accept the
change, or narrow PP-FR-6 is a Product Owner / Engineering Lead call. Phase C
must not start before it is answered.

**Revision 6 added three blockers and withdrew one option. Revision 7 closes all
of them** (`product-intent.md` §17):

| | Outcome |
| --- | --- |
| **OD-9** | **Resolved — option 1 via `routing.yaml`.** A `default_gate_review_agents` key; Cadre declares `["code-reviewer"]` so its plans stay byte-identical, and a foreign roster omitting it gets `[]` rather than a `ValueError` from `:547-551`. Option 3 stays withdrawn: leaving `:107` alone makes a foreign roster with lifecycle gates emit no plan at all. |
| **OD-10** | **Withdrawn.** With `roster.root` global-only there is no project-tier redirect for `cadre mcp-dispatch-server` to fail to surface, and no reason to convert its import-time resolution to per-call. The *observation* stands — the two surfaces resolve independently — and still binds PP-FR-6. |
| **OD-11** | **Resolved — no compatibility window**, `schema_version` only, with one requirement attached: the loader must **reject** an unrecognised `schema_version` rather than ignore it. |
| **OD-13** | **Resolved** — both authority roles assigned to `@deagy` and recorded as such. The kernel permits it (`__init__.py:1948-1963` checks each role has an assignee, never that two roles differ); only author-versus-approver separation is enforced, and every author here is an agent. |

**Four of the five blockers were closed by one decision.** OD-2's reversal
withdrew OD-7 and OD-10 outright and removed three items from Phase A. Worth
noting against how this plan had been reasoning: it treated the blockers as five
independent gates and priced OD-2 as a settled input rather than the largest
lever on the list.

G2 itself remains unapproved and requires `product_owner` **and**
`engineering_lead`.

The phase order is chosen so the **cheapest falsification comes first**, which is
also `docs/proposals/governance-as-product-2026-08.md`'s own recommendation:
*"condition 2 is the cheapest way to find out whether the rest is real, and
should be attempted before anything is moved."*

**Revisions 1–5 stated that principle and then violated it, and Revision 6
reorders the plan rather than restating the principle a sixth time.** Phase B was
nominated as the falsification step, but it cannot run until Phase A has shipped
a resolver, a manifest, a new schema, a packaging allowlist entry and eight
constant rewrites — and the stop table called Phase A *"safe to stop, a latent
capability."* Safe, but not cheap, and by then everything has been moved. The
proposal asks for the answer **before** that.

So the order is now **D → 0 → A′ → B′ → C′ → E**:

- **D first** because it is zero-risk, depends on nothing, and is a tripwire for
  the specific mistake Phase A is most likely to make.
- **0 next** because it answers "is the seam real" with a throwaway harness and
  no production change at all. This is the falsification the principle was
  asking for.
- **E last** because a working bypass already exists (`product-intent.md` §2), so
  it now unblocks nothing.

Each phase is independently shippable and leaves the tree better than it found
it, so the sequence can stop at any point without stranding work — **which was
also not true as written.** Phase A and Phase C scheduled the same six edits;
Revision 6 puts each in exactly one phase.

## 1. Phases

### Phase D — Knowledge-store regression pin (PP-FR-5). **LANDED 2026-08-11.**

Moved from last to first at Revision 6. The content is unchanged and is
described in full below under its original heading; what changed is *when*.

**Delivered:** `roster/orchestration/test/test_knowledge_store_anchor.py`,
7 tests, suite 1228 → 1235. Two planted defects confirmed it fails naming the
real cause, and the second is the one that mattered — it creates the
`CADRE_ROSTER_ROOT` reader that does not exist yet, so the forward pin is proved
non-vacuous against the future state it guards rather than the present one.
Evidence at `phase-0-and-d-evidence.md`.

It depends on nothing, changes no behaviour, and asserts that
`build_dispatch_plan.py:29` stays `Path(__file__)`-derived. The live risk it
guards against is created by **Phase A** — an implementer routing `:30` through
the resolver and taking `:29` with it, same shape, adjacent line, looking like
tidying. Landing the assertion *before* the phase that can break it is the
difference between a regression test and a post-mortem.

Zero risk, zero decisions, always safe to stop.

### Phase 0 — Falsification spike (NEW at Revision 6). **RUN 2026-08-11, discarded.**

**Files:** none in the tree. A disposable script or branch, deleted afterwards.

**Outcome — the seam is real, with one known blocker in front of it.** A foreign
roster sharing no id, phase or keyword with Cadre's produced a schema-valid plan
naming only its own roles (`workflow='new-service'`, from its own declared
`workflow_shape`, zero Cadre leakage). With the kernel resolvable the same task
raised `ValueError: Routing selected an unknown agent: code-reviewer` —
**OD-9's premise observed rather than reasoned.** Both surfaces confirmed to
resolve independently.

**And one finding nobody had: `routing.yaml` is JSON** (`routing_overlay.py:502`
parses it with `json.loads`). A fixture written as actual YAML died on the first
run with a `JSONDecodeError` naming nothing useful. Filed as **G-12**; it
belongs in Phase A or B, ahead of authoring the real fixture. Full evidence at
`phase-0-and-d-evidence.md`.

**What this cost, against what Phase B was scheduled to cost.** The answer above
arrived with no manifest, no schema, no FieldSpec, no constant rewrite and no
regeneration — which is the entire argument Revision 6 made for adding this
phase, now tested.

Monkeypatch or env-redirect the four resolution sites — `select_agents.ROSTER_ROOT`,
`build_dispatch_plan.ROSTER_ROOT`, `mcp/dispatch_core.CATALOG_PATH`,
`mcp/dispatch_server._ROUTING_CONFIG` — at a hand-built minimal roster that
**declares `quality_gates`**, and run both `cadre select` and the MCP dispatch
path against it end to end.

**Depends on nothing.** No `settings.py` FieldSpec, no `roster.json`, no
`roster.schema.json`, no `PROVIDER_BUNDLE` entry, no constant rewrites, no
schema bump, no regeneration. That is the entire point: every one of those is
Phase A, and Phase A is what the proposal says should come *after* the answer.

**What it answers, that nothing else currently does:**

1. Does lifecycle-aware selection work against a foreign roster at all, or does
   it hit `build_dispatch_plan.py:547-551` and raise? This is OD-9's real
   consequence, and it is worth *observing* before the Product Owner decides
   OD-9 on the strength of a code-reading.
2. Do the two dispatch surfaces actually diverge, and how visibly? This is
   OD-10's premise, currently established by reading `dispatch_server.py:48`
   and `:63` rather than by running the server.

**Safe to stop after — and this is the natural stop if the answer is "the seam
isn't real."** Nothing has been moved, nothing shipped, nothing to revert but a
deleted branch. If Phase 0 shows the mechanism cannot work, Phases A–C are
re-scoped before a line of production code changes, which is exactly what
Revisions 1–5 intended and their ordering prevented.

**Exempt from the regeneration sequence** (§2), because it is never merged.

### Phase A′ — Roster-root resolution (PP-FR-1, PP-FR-2). **LANDED 2026-08-11.**

**Delivered:** `roster/orchestration/roster.schema.json`, `roster/roster.json`,
`roster/orchestration/src/roster_manifest.py`, the `roster.root` FieldSpec at
`SCOPE_GLOBAL_ONLY`, the `--roster` flag, `schema_validate.py`'s third
instance/schema pair, and 21 tests. Suite 1235 → 1249, golden corpus untouched.

**One item deferred, with the reason recorded rather than left to a reader to
reconstruct: `provider/roster.json` and its `PROVIDER_BUNDLE` entry.** This
plan scheduled both here, on the intent record's finding that `provider/` is
"already a complete roster package missing exactly two files". That finding is
correct — and it is exactly why the manifest cannot land yet. `provider/`
contains neither `catalog.yaml` nor `routing.json`, so a `roster.json` there
would declare two files that do not exist and fail its own loader. **Shipping a
manifest that names absent files is worse than shipping none**, so the order is
inverted from what §2 assumed: the two files must be generated into `provider/`
first, and that is new generated content with its own PP-NFR-1 surface. Deferred
to whichever increment makes `provider/` a valid roster package, which is not
this one.

**A second finding, and it is the same trap this plan already warned about.**
`generate_global_plugin.py` has a **second closed allowlist** — for roster-root
files, distinct from `PROVIDER_BUNDLE`. `roster.json` was silently skipped and
seven `test_repository_health` tests failed against a packaged selector that
could not find its own manifest. §2 warned about the `provider/` allowlist in
bold; nobody knew there were two. Now listed, with a comment saying why.

---

**Files:**
- `roster/shared/src/settings.py` — new `roster.root` FieldSpec after
  `context_store.home` (`:690-697`), **`SCOPE_GLOBAL_ONLY`** per OD-2 as
  reversed, with **`default_computed`** (the `agentic_sdlc.bin_path` form at
  `:665-672`), not `default_static=None`. `default_static=None` yields no default
  and pushes the checkout-relative computation back out to every call site,
  which is the duplication PP-FR-1 exists to delete.

  **Revisions 2–6 specified project tier plus a comment justifying the
  exception. Neither is needed now.** The field is unremarkable: same scope as
  its three siblings, no departure to explain, no `context_store.home`-style
  objection to answer. If you find yourself writing a comment about why this
  setting's scope is unusual, you are working from a stale revision.
- New `roster/orchestration/roster.schema.json` + a `roster.json` at
  `provider/roster.json`. Validated by the existing
  `roster/orchestration/src/schema_validate.py` and its pre-commit hook — no new
  validation machinery. **Also add `"roster.json"` to `PROVIDER_BUNDLE`**
  (`generate_global_plugin.py:101`) — see §2, and PP-FR-2 for why this is the
  side of the PP-NFR-1 collision that gives.
- ~~Roster identity in the plan (PP-FR-1b) + `selection.schema.json` 6 → 7
  (PP-NFR-3b), gated on OD-7.~~ **Removed at Revision 7.** OD-2's reversal
  retracts both requirements and withdraws OD-7. No field is emitted, the schema
  stays at `const: 6`, no `dispatch_fingerprint` changes, and **nothing in this
  phase is built behind a pending decision.**
- `roster/orchestration/src/select_agents.py` — `:17` (`ROSTER_ROOT`) becomes a
  resolver call; `:203` (`catalog_path`) and `:204` (`routing_path`) take their
  paths **from `roster.json`**, not from `ORCHESTRATION_ROOT / "routing.yaml"`;
  add `--roster`. **`:18` (`REPOSITORY_ROOT`) and `:24` (`_SHARED_SRC_DIR`) must
  be REWRITTEN to re-derive from `Path(__file__)`** — see below. Not "left
  alone": they read `ROSTER_ROOT` today, so leaving them alone is what breaks
  them. **`:16` (`ORCHESTRATION_ROOT`) likewise stays `Path(__file__)`-derived**
  — routing it through the resolver would force every foreign roster to
  reproduce Cadre's internal directory layout, contradicting PP-FR-2.
- `roster/orchestration/src/build_dispatch_plan.py` — `:30` (`ROSTER_ROOT`) and
  `:604` (`path = ROSTER_ROOT / definition`, context-pack definitions). `:29`
  (`KNOWLEDGE_STORE_ROOT`) is Phase D and is **platform**-anchored, not
  roster-anchored.
- ~~`roster/orchestration/mcp/dispatch_core.py:56` and `mcp/dispatch_server.py:63`~~
  — **moved to Phase C′-1 at Revision 6.** Revision 5 added the second entry
  point here *and* listed the same two lines under Phase C's category B. They
  are one edit, and they belong with the guard that enforces them. The finding
  that they exist stands; only the phase changed.

  **Revisions 5–6 warned that under project-tier scope this was not a two-line
  change** — `mcp/dispatch_server.py:48` deliberately calls
  `settings.disable_project_tier_cwd_fallback()` and `:63` loads routing at
  import time, so a project-tier value would have required converting
  import-time resolution to per-call with an explicit `start=`. **OD-2's
  reversal removes that work entirely.** A global-only setting resolves once at
  import, which is exactly what the module already does. Both lines still change
  in C′-1 as category-B path fixes; neither needs restructuring.
- `roster/orchestration/src/schema_validate.py` — `:329-332` hardwires two
  instance/schema pairs; `roster.json` needs a third. Small, but it is a fourth
  file mirrored into `plugin/suite/` (see §2).
- **The roster-manifest loader must reject an unrecognised `schema_version`**
  rather than ignoring it (OD-11's adopted mitigation, `requirements.md`
  PP-FR-2). OD-11 declined a `platform_compatibility` window, so this is the
  only signal a mismatched manifest gets — it must fail by name, and the test
  must assert the failure rather than only the success.

**Two constants must not follow the roster root, and the default outcome is that
they do.** Read the code before writing any of it:

```python
ROSTER_ROOT     = ORCHESTRATION_ROOT.parent               # :17  <- becomes resolver-driven
REPOSITORY_ROOT = ROSTER_ROOT.parent                      # :18  <- follows :17 silently
_SHARED_SRC_DIR = ROSTER_ROOT / "shared" / "src"          # :24  <- follows :17 silently
```

`:18` and `:24` are **derived from** `:17`. Redirect `:17` and both move with it
whether or not anyone touched them, which is why "these two are unchanged" is a
description of the bug rather than of the fix. Each must be re-derived
independently from `Path(__file__)`.

Why it matters: `_SHARED_SRC_DIR` is the `sys.path` bootstrap for `settings`,
`routing_overlay`, `text_embedding`, and `content_protection` — all declared
platform-owned. If it follows the roster, a project-local `.agents/cadre.yaml`
(which OD-2 permits, and which arrives with `git clone`) chooses which
`settings.py` the platform executes. It is also circular: `settings.py` *is* the
resolver that would compute the new path. `REPOSITORY_ROOT` is the default
working tree for `discover_changed_files` (`:120`) and knowledge-source
resolution (`:202`) — it answers "which tree is being changed," and a roster
redirect must not move the diff.

Phase C's guard asserts both, and PP-NFR-4 requires confirming that guard
**fails** against the untouched `ROSTER_ROOT`-derived form.

**Reuse, do not reimplement:** path containment already exists twice —
`kernel/agentic_sdlc/__init__.py:159-169` (`provider_resource()`, the exact
"escapes its manifest directory" check this needs) and
`roster/orchestration/src/glob_containment.py`. The kernel's is the closer
semantic match but is across the boundary, so port the *logic*, not an import —
`test_kernel_boundary.py:76-95` forbids importing kernel code and that guard must
keep passing.

**The trap.** Default *selection* must be byte-identical to today. The golden
corpus (`roster/orchestration/test/fixtures/selection_golden_corpus.json`, 195 KB,
**175 hand-maintained cases**, **no generator script** — the loop is run-test,
read-diff, hand-edit) is the detector. If it needs editing, default behaviour
changed and the change is wrong.

Verified that this detector survives the schema bump: the corpus pins
`expected.primary`/`.reviewers`/`.support` and contains **zero** occurrences of
`schema_version` or `dispatch_fingerprint`. Fingerprints *will* all change
(`build_dispatch_plan.py:840` hashes the plan including `schema_version`) — that
is PP-NFR-5's expected churn, not a corpus failure, and confusing the two sends
someone hand-editing **175** blocks for no reason. (Revisions 1–2 said "~60"
here, a figure borrowed from the 2026-08-08 sibling record, which was counting
the cases *that* change rewrote rather than the file's size. The one number in
this plan meant to convey how expensive a mistake is was three times too small.)

### Phase B — The second roster (PP-FR-3)

**Files:** `roster/orchestration/test/fixtures/minimal-roster/` — `roster.json`,
`catalog.yaml`, `routing.yaml`, and ≈3 role directories with `AGENT.md`. New
`roster/orchestration/test/test_roster_package.py`.

**Author it fresh; do not subset Cadre's.** A copy satisfies every assumption
Cadre happens to satisfy, which is exactly the blindness condition 3 names. Give
it role ids, phases, and routing keywords that share nothing with Cadre's, so a
leaked default shows up as a wrong name rather than a plausible one.

**This is the falsification step** *(as re-scoped — Phase 0 now takes the
cheapest part of this job, and this phase makes it permanent)*. If a foreign
roster cannot produce a plan, the seam is theoretical and Phases C–E should not
be attempted.

Four acceptance cases, the third being the one that usually gets skipped:
plan-is-valid, no-match-returns-`needs-triage`, missing-`catalog.yaml`-fails-
by-name, and **a matching fixture task classifies to a `workflow` other than
`unclassified`** (PP-FR-3(d)). Give the fixture's routes a `workflow_shape` and
the fourth passes without touching `_select_workflow()` — it is a regression
pin, not a falsification, and it is worth having for exactly that reason: it is
the property most likely to be lost silently while the fixture is edited for
some other purpose.

**Three more cases at Revision 6, and the first of them is the difference
between a falsification and a decoration.**

- **(e) The fixture must declare `quality_gates`, and this case must run with
  lifecycle contracts resolvable.** None of (a)–(d) requires either, so
  `_gate_agents()` never fires against the fixture — and that is where the only
  known blocker for a foreign roster lives (`build_dispatch_plan.py:107` →
  `:547-551` → `ValueError`). Without (e), **this phase reports "the seam is
  real" for every roster that does not use the lifecycle, and says nothing about
  the ones that do.** It is the golden corpus's blind spot, rebuilt in the
  fixture written to expose blind spots.
- **(f) Path-escape rejection**, naming the offending field — symlink, `..`, and
  absolute-path values enumerated rather than "an escape." PP-FR-2 states this
  acceptance and names this file as its verifier; it was scheduled in no phase.
- **(g) A malformed `roster.json`** — missing key, or a `catalog`/`routing` path
  that does not exist — fails by field name. Only the manifest's total absence
  is currently covered, and the manifest is now the thing most likely to be
  wrong.

Also confirm the fixture's `AGENT.md` files actually load (frontmatter parsed,
`role_root` honoured). A broken `role_root` passes (a)–(d) untouched if nothing
dereferences a `definition` path.

### Phase C′ — The mirror boundary guard (PP-FR-6). **LANDED 2026-08-11.**

**Delivered:** `roster/orchestration/test/test_roster_boundary.py` (14 tests),
the six category-B path fixes, and OD-9's `default_gate_review_agents` key.
Suite 1249 → 1263. Cadre's plans byte-identical; the golden corpus unedited.

**The seam works end to end.** `cadre select --roster <fixture>` against a
roster sharing no id, phase or keyword with Cadre's now emits a schema-valid
plan — `status: ready`, `workflow: new-service`, `lifecycle: integrated`, only
fixture roles, zero leakage. The same command raised `ValueError: Routing
selected an unknown agent: code-reviewer` before C′-2.

**Fault injection found a hole the suite could not**, and it is worth recording
against PP-NFR-4's own framing. Five defects planted; four failed correctly and
the fifth — a Cadre role id in `mcp/dispatch_core.py` — **passed**, because
category A had been scoped to `build_dispatch_plan.py` where the known defect
lived. Twelve of twelve green, with the coverage wrong, in the file whose own
self-vacuity section warns about exactly that. PP-NFR-4 asks whether a guard
*can* fail; this was a guard that could fail and still did not cover the thing
it was for. Category A now runs across every platform module with role ids read
from `catalog.yaml` rather than hand-listed.

Two corrections the plants then forced: role ids match as whole kebab-case
tokens (the gate id `halt-authority-determination` contains the role id
`halt-authority` and is unrelated — a guard that cries wolf teaches its reader
to loosen it); and `build_dispatch_plan.py:354-355` emitted human-gate
descriptions naming `halt-authority` and `architecture-authority` into every
plan, fixed rather than exempted.

**And the guard rejected this implementation's own first draft.** Both MCP and
overlay resolvers originally caught a manifest error at import and fell back to
Cadre's hardcoded layout. Intent §7 C4 forbids degrading to the built-in roster;
a fallback reproducing Cadre's directory layout is that degradation wearing a
robustness costume. Both fail closed now.

---

**This phase has been mis-sized in every revision, in both directions, and
Revision 5 was wrong in both directions at once.** Revisions 1–2 listed only the
new test. Revision 3 added two fixes, one of which was not a defect. Revision 4
cut back to "one small fix." Revision 5 grew it on the strength of "roughly nine
more violations" — of which **five were not violations** (they are
`raise ValueError(...)` diagnostics, i.e. the category this plan's own rule
permits) and **two were already scheduled in Phase A**.

The real shape: **one category-A fix, six category-B sites, two of which move
here from Phase A.** See `requirements.md` PP-FR-6 for the corrected table and
the AST-sink rule that must generate the categories rather than restating them
in prose a fourth time.

**Still split, though the blocker is gone** — the halves have different risk
profiles and C′-2 is the only step that touches selection output:

- **C′-1.** The six category-B path fixes, the boundary test with its
  self-vacuity guard, the PP-FR-1 assertions on `:18`/`:24`, and the
  lifecycle-aware detector.
- **C′-2 — unblocked at Revision 7, and now a specified change.** OD-9 resolved
  to option 1, so this is a `default_gate_review_agents` key in `routing.yaml`
  threaded into `_gate_agents()`, **not** a pending choice. Cadre's plans stay
  byte-identical, so the ~15 `test_selector.py` assertions are a *confirmation*
  step rather than a migration.

**Files:** new `roster/orchestration/test/test_roster_boundary.py`, a new
lifecycle-aware selection test (see below), **and** the six category-B path
fixes across `select_agents.py:203-204`, `routing_overlay.py:97,102`,
`mcp/dispatch_core.py:56`, `mcp/dispatch_server.py:63`. **Not `routing.py`** —
its five cited lines are diagnostics and must be left alone.

**Model the test on `roster/orchestration/test/test_context_boundary.py:157-215`**,
which already does this exact job for the knowledge/context store pair — both the
import check and the string-literal check. Carry over its **self-vacuity guard**
at `:150-155` (assert the directories exist and contain modules), so a rename
cannot make every check pass over an empty set. Add the PP-FR-1 assertions that
`select_agents.py:18` and `:24` do not resolve through `roster.root`.

**The violations, by category (`requirements.md` PP-FR-6 has the full table):**

- **A — one, and it is a functional prerequisite, not hygiene.**
  `build_dispatch_plan.py:107`'s `["code-reviewer"]` default. No gate contract
  declares `review_agents` *or* `author_agents`, so `_gate_agents()` returns
  exactly `["code-reviewer"]` for every gate and lands in `support` (`:673`).
  Confirm before touching it: `./bin/cadre select --task "Update the OpenTofu
  module for the VPC" …` currently yields `support: [product-intent-agent,
  requirements-agent, code-reviewer]`.

  **Revision 6: the consequence one call further on is the one that matters.**
  `:547-551` validates every selected agent against the catalog and raises
  `ValueError: Routing selected an unknown agent`. A foreign roster has no
  `code-reviewer`, so this is not "Cadre's lists change" — it is **PP-FR-1 does
  not work**.

  **Revision 7 — the fix, resolved at OD-9 (`product-intent.md` §17):**

  ```yaml
  # roster/orchestration/routing.yaml
  default_gate_review_agents: ["code-reviewer"]
  ```

  ```python
  # build_dispatch_plan.py:107
  *contracts[gate_id].get("review_agents", default_review_agents)
  ```

  threaded from the loaded routing config at the `:673` call site. Cadre's own
  `routing.yaml` declares the literal the Python default held, so Cadre's output
  is unchanged; a foreign roster omitting the key gets `[]`. **Not** provider
  profile `gate_bindings` — that binds gates to approval authority, a different
  axis, and would put roster-side dispatch defaults inside a kernel-owned
  concept. Adds `routing.yaml` to §2's `plugin/suite/` mirror list.
- **B — six, and this is the phase's real body.** Hardcoded `catalog.yaml` /
  `routing.yaml` / `roster/`-relative paths in `select_agents.py:203`, `:204`,
  `routing_overlay.py:97`, `:102`, `mcp/dispatch_core.py:56`,
  `mcp/dispatch_server.py:63`. PP-FR-2 already prescribes the fix: these come
  from `roster.json`. This is what makes the manifest load-bearing rather than
  decorative.
- **C — permitted, and the guard must say so by rule.** `roster/`-relative paths
  inside user-facing error strings — **including `routing.py:154,198,209,212,279`,
  which Revision 5 listed as category B and which are all
  `raise ValueError("routing.yaml must contain …")`** — plus
  `knowledge-store/src/staged_records.py:141,147` and `finding_record.py:139`.
  Exempt literals reachable only from diagnostics; **do not** write a file
  allowlist, which is how a guard quietly stops meaning anything.

  **Drop `config.py:79,136,183` from the example set.** Revisions 1–5 offered
  them as the evidence for this exemption and they do not demonstrate it: `:79`
  and `:136` are path *construction* feeding a real filesystem walk, `:183` has
  no path in it, and none of the three contains a `roster/`-relative literal at
  all. Whatever exempts them is a different rule.

  **Derive the example sets from the rule once written, rather than carrying any
  of these forward.** Both sets above were hand-classified in prose and both were
  wrong, in opposite directions. A third hand-classification is not the fix —
  `requirements.md` PP-FR-6 sketches the AST call-target rule that should
  generate them.

**A second detector is required, not optional.** The golden corpus cannot see
category A: `test_selection_golden_corpus.py:135` patches
`try_lifecycle_contract` to `None`, so `_gate_agents()` never runs and none of
the 175 cases carries `code-reviewer` in `expected.support`. Add a
lifecycle-aware selection test that pins the `support` list for at least one
gate-bearing task with the in-tree kernel resolvable. Without it, PP-NFR-1
asserts an invariant it cannot check — while citing the corpus as proof.

**"With the in-tree kernel resolvable" must mean forced, not ambient.** Two
existing tests already make this assertion — `test_selector.py:964` and the
gate-agents subtest at `:1109-1114` — and both are
`@unittest.skipUnless(AGENTIC_SDLC_AVAILABLE, …)`, i.e. they run when
`AGENTIC_SDLC_BIN` or `PATH` happens to supply a kernel and **silently skip
otherwise**. In CI they pass because `.github/workflows/validate.yml:46` sets
the variable. On a bare checkout they vanish. Writing the new detector that way
reintroduces exactly the host-dependence the golden corpus was made
deterministic to remove — in the test added to cover the corpus's gap.

Force it instead: `bin/agentic-sdlc` is an in-tree wrapper that runs `kernel/`
with no install, so `mock.patch.dict(os.environ, {"AGENTIC_SDLC_BIN": …})` inside
the test is deterministic and checkout-only. Pin the negative case too (a route
with no `required_quality_gates` must **not** acquire `code-reviewer`).
`_fetch_contract` is `@lru_cache(maxsize=1)` keyed on the executable path, so
resolve inside the patched block, not before it.

**The ~15 assertions this phase moves, which no revision has listed.**
`test_selector.py` asserts `code-reviewer in agents.support` at `:342`, `:466`,
`:964`, `:979`, `:1109-1114` and elsewhere. Under OD-9 option 2 they all change;
under option 1 they must be confirmed unchanged, which is the whole point of
option 1. **Produce that inventory before OD-9 is decided** — the decision is
"preserve Cadre's output" versus "change it," and fifteen concrete assertions
size that better than an abstraction about corpus blindness.

**Not a violation, though Revision 3 said it was:** `_select_workflow()`. Its
final stage reads roster-declared `workflow_shape`; the earlier precedence
branches (`:150-196`) do name Cadre *route* ids, but route ids are not on
PP-FR-6's forbidden list and `routing.schema.json:193-201` documents that split
deliberately. A foreign roster cannot reach `rollback` or `production-release`
— a real limitation, recorded in `requirements.md` PP-FR-3, not work in this
phase. **Do not widen the `workflow` enum.**

Runs after B deliberately: before a second roster exists there is no way to tell
a guard that works from a guard that cannot fail.

### Phase D (full detail) — Knowledge-store path resolution (PP-FR-5)

*Scheduled first; see the stub at the head of §1. Kept in place here so a
reader of Revision 5 finds the content where they left it.*

**Files:** `roster/orchestration/src/build_dispatch_plan.py:29` and `:501`.

**This phase writes a test and changes no behaviour, which Revision 3 obscured
by describing it as a move.** `:29` is already `Path(__file__)`-derived and
already ignores `roster.root`. The work is to *keep* it that way: the live risk
is Phase A routing `:30` through the resolver and taking `:29` — same shape,
adjacent line — with it. Do that and PP-FR-5's acceptance breaks: point
`CADRE_ROSTER_ROOT` at a roster-less directory and the emitted `cli.py` path
stops existing, in a plan whose consumer is TypeScript in another package.

So: leave `:29` alone deliberately, and add the assertion that says why. Stat
the emitted path rather than string-matching it. **The PP-NFR-4 evidence for
this phase is not a planted defect in the store** — it is a scratch branch where
`:29` follows the resolver, confirming the assertion fails there. Without that
run this phase is two tests that have always passed.

`knowledge_store.home` continues to govern where the *data* lives, unchanged and
still `SCOPE_GLOBAL_ONLY`.

`:501` emits an absolute path into every plan's
`knowledge_context.requests[].invocation.args`, and
`cline-plugins/cline-agents/index.ts:247-259` executes it. **The emitted shape
must not change** — only how the path is computed. A changed shape would be a
cross-language breaking change against a consumer in another language and
another package.

Note this is now a *narrower* constraint than at Revision 1, and the difference
matters: PP-NFR-3b already bumps the schema for PP-FR-1b's roster identity, so
"no bump" is no longer the reason to leave this field alone. The reason is the
Cline consumer, which is unaffected by a version number and would break on a
reshaped `invocation.args`. Do not let the bump already in flight license a
second, unrelated contract change riding along with it.

`roster/knowledge-store/src/` itself needs **no edit**: it is already stdlib-only
and roster-free (`requirements.md` §0.4). The three `sys.path.append` sites into
`roster/shared/src/` stay; Phase C's guard converts "these four modules are
platform" from convention into a test.

### Phase E — Let `cadre sdlc` run *without* Cadre's provider (PP-FR-4). **LANDED 2026-08-11.**

**Delivered:** `bin/cadre.py`'s `_resolve_provider_injection()` and six
dispatcher tests. Suite 1276 → 1282. Acceptance (a) and (b) both met:

```console
$ ./bin/cadre sdlc --provider providers/agentic-sdlc-defaults/provider.json provider list
[{"id": "agentic-sdlc-defaults", "version": "0.3.0", …}]     # was: duplicates profile ids
$ ./bin/cadre sdlc provider list
[{"id": "cadre", …}]                                          # argv byte-identical to before
```

**Implemented against the review's recommendation, and the deviation is the
point.** The implementation review proposed an explicit `--no-default-provider`
flag *instead of* detecting the caller's `--provider`, on the ground that the
kernel's flag is `action="append"`, accepts `--provider=X` and `--provider X`,
may repeat, and that a wrapper-side scan must reproduce argparse's tokenisation
exactly or silently over- or under-suppress.

**That objection is correct about string scanning and is answered by not doing
any.** Detection uses `argparse.parse_known_args`, so the tokenisation is not
reproduced — it is the same implementation. The property that makes this safe is
not cleverness but identity: for a genuinely ambiguous argv, the wrapper reads
it exactly as the kernel will, because both are argparse with `--provider` as an
appending option. A wrapper that guessed *differently* from the kernel would be
the actual hazard, and this cannot.

Without detection, acceptance (a) as written could not pass — the flag alone
would still require a second flag beside it. Both are implemented: detection for
(a), and `--no-default-provider` for the case detection cannot cover, running
the kernel with no provider at all.

**One bug caught before it shipped.** An earlier draft prepended each supplied
`--provider` in turn and silently reversed the order for any caller passing more
than one. `action="append"` means list order is the caller's stated precedence.
Fixed and pinned.

---

**Files:** `bin/cadre.py:125-127`.

**Still the smallest phase, but not the one Revisions 1–4 described.** They had
it as "add a `--provider` flag." The flag already works: `*rest` carries the
caller's argv through and the kernel's `--provider` is `action="append"`. Run it
and the actual failure appears:

```console
$ ./bin/cadre sdlc --provider providers/agentic-sdlc-defaults/provider.json provider list
{ "error": "provider agentic-sdlc-defaults duplicates profile ids: ['generic']" }
```

The foreign provider **loaded**, then collided with Cadre's, which is injected
unconditionally. So the work is: **when the caller supplies `--provider`,
suppress the injected default** (or add an explicit `--no-default-provider`).
The kernel still needs nothing — `load_provider()` already validates a foreign
manifest completely and `providers/agentic-sdlc-defaults/` is already a working
second provider.

With no flag, the argument vector must stay byte-identical to today's.

Last because it is independent of A–D and its value is realised only once
something else can supply a foreign bundle. **Its acceptance is the command
above succeeding** — Revisions 1–4 asked only that the provider "reach the
kernel," which already passes.

**Revision 6: lower priority again, and for a new reason.** The capability is
not blocked at all today — `./bin/agentic-sdlc --provider <foreign>/provider.json
provider list` **succeeds**, because it runs the in-tree kernel directly and
injects nothing (`product-intent.md` §2). So the coupling is to one wrapper, not
to `cadre` and not to the kernel. This phase fixes the ergonomics of the command
users are actually told to run, which is worth doing — an undocumented byproduct
of a wrapper's implementation is not a supported interface — but it now unblocks
nothing and can be deferred past everything else without cost.

**On the implementation: prefer an explicit `--no-default-provider` flag over
sniffing `--provider` out of `*rest`.** The kernel's flag is `action="append"`,
accepts both `--provider=path` and `--provider path`, and may repeat — so a
wrapper-side scan has to reproduce argparse's tokenisation exactly or it
silently over- or under-suppresses, and a caller's own path value containing the
substring would fool it. An explicit flag is auditable and cannot misfire:

```python
provider_args = [] if options.no_default_provider else ["--provider", str(provider)]
result = subprocess.run([sdlc_bin, *provider_args, *rest], ...)
```

With no flag, the argument vector stays byte-identical to today's.

## 2. Verification

Per phase, and again at the end:

```sh
python3 -m unittest discover -s roster/orchestration/test -p "test_*.py"
python3 -m unittest discover -s roster/knowledge-store/test -p "test_*.py"
python3 -m unittest discover -s roster/shared/test -p "test_*.py"
python3 -m unittest discover -s plugin/tools -p "test_*.py"
python3 -B -m unittest discover -s kernel/test -p "test_*.py"
./bin/cadre generate-plugin --output plugin --check
git status --porcelain    # provider/roles/, catalog.yaml, routing.yaml clean;
                          # under plugin/, exactly the 4-item diff in PP-NFR-1
```

**Add a lifecycle-aware selection check to this list** (Phase C). Every command
above runs with lifecycle contracts stubbed out or absent, which is why the
`code-reviewer` default survived four revisions of review unnoticed.

**Regeneration.** Phases A–E touch `roster/*/src/` and `bin/`, which
`plugin/suite/` bundles — so the full four-step sequence in `CLAUDE.md` /
`roster/RUNBOOK.md` §17 applies, with `git add` of new files **first**. Phase B's
fixture lives under `test/`, which is not bundled.

**`provider/roster.json` needs an explicit packaging change, and this is the
detail most likely to be missed.** `generate_global_plugin.py:101` copies
`provider/` through a **closed allowlist**, not a directory walk:

```python
PROVIDER_BUNDLE = ("provider.json", "agent-catalog.json", "profiles", "extensions", "codex-agents")
```

An unlisted file is silently skipped, so without adding `"roster.json"` the
distribution ships every role and no manifest — a `provider/` bundle that is not
a valid roster package, failing in the installed plugin and nowhere in CI.

**`plugin/` will change in four ways, and this list has been wrong in every
previous revision — including Revision 4, whose stated purpose was to fix it:**

1. **`plugin/roster.json`** — new. Note the path.
   `generate_global_plugin.py:813,819` copies each `PROVIDER_BUNDLE` member to
   `plugin_root / name`, so the bundle lands **flattened at the plugin root**
   (`plugin/provider.json`, `plugin/profiles/`, …). Revision 4 wrote
   `plugin/provider/roster.json`; **there is no `plugin/provider/` directory.**
2. `plugin/suite/roster/orchestration/roster.schema.json` — new, automatic.
   `generate_global_plugin.py:1426` copies `roster/orchestration/` into `suite/`
   **by prefix**, not by allowlist, which is why `routing.schema.json` and
   `selection.schema.json` are already there.
3. ~~`plugin/suite/roster/orchestration/selection.schema.json` — modified by the
   6 → 7 bump.~~ **Removed at Revision 7** — PP-NFR-3b is retracted, so there is
   no bump. Replaced in the list by
   `plugin/suite/roster/orchestration/routing.yaml`, **modified** by OD-9's
   `default_gate_review_agents` key through the same prefix copy.
4. `plugin/suite/` mirrors of `select_agents.py`, `build_dispatch_plan.py`,
   `settings.py`, `schema_validate.py`, `mcp/dispatch_core.py`,
   `mcp/dispatch_server.py`, and `bin/`. `suite/` is a copy of that source —
   editing the source is editing `plugin/`. No phase can avoid this.

Anything under `plugin/` beyond those four fails PP-NFR-1. **Generate the
package and read the diff rather than predicting it**: four revisions predicted
it wrongly, and `--check` costs one command.

**Non-vacuity (PP-NFR-4), recorded in the PR.** For each new guard: plant the
defect, confirm it **fails** naming the real cause, revert, confirm clean.
- Phase A/B: remove `catalog.yaml` from the fixture → error names the file.
- Phase B: a task matching nothing → `needs-triage`, not a Cadre role.
- Phase B: a fixture task matching a fixture route → a `workflow` that is not
  `unclassified` (PP-FR-3(d)). Passes as soon as the fixture routes declare
  `workflow_shape`; strip the declaration to confirm it fails.
- Phase B: a manifest path escaping its directory → rejected naming the field
  (PP-FR-3(f)); and a `roster.json` missing a required key → rejected naming the
  key (PP-FR-3(g)). Both added at Revision 6.
- **Phase B: strip `quality_gates` from the fixture's routes → the lifecycle
  acceptance case (PP-FR-3(e)) must stop testing anything.** Added at
  Revision 6, and it is the one that decides whether this phase falsifies
  anything at all.
- Phase D: a scratch branch routing `build_dispatch_plan.py:29` through the
  roster resolver → the `cli.py`-exists assertion fails. Phase D changes no
  behaviour, so this run is the only thing distinguishing its tests from two
  assertions that have always held. **Attach the scratch branch's diff and the
  failing output to the PR.** Every other item here plants a defect *in the
  tree* and any reviewer can reproduce it; this is the sole item an implementer
  can claim while leaving nothing checkable behind.
- Phase C: hardcode a Cadre role id in `select_agents.py` → boundary test fails.
- **Phase C: hardcode one in `mcp/dispatch_core.py` too.** Added at Revision 6,
  and the more informative of the two: that module was absent from the platform
  list through five revisions, so it is the likeliest place for the guard to be
  silently out of scope — and the self-vacuity guard detects an **empty** module
  list, never an **incomplete** one.
- **Phase C: plant a category-B violation** — reintroduce a literal
  `"catalog.yaml"` into a resolution path → the guard names it. Added at
  Revision 6: category B is the phase's body and had no non-vacuity case at all.
- **Phase C: confirm the category-C rule is not over-broad** — a genuine
  `raise ValueError("routing.yaml must …")` diagnostic must **pass**. Added at
  Revision 6, because a rule can be non-vacuous and still wrong in the permissive
  direction, and PP-FR-6's own example sets are the proof that it happens.
- Phase C: the lifecycle-aware detector must **fail** when `:107`'s default is
  removed without OD-9's chosen compensation. If it passes either way it is
  measuring nothing, which is exactly the state the golden corpus is in.
  **Confirm it also fails when run without `AGENTIC_SDLC_BIN` forced** — if it
  skips instead, it is the old `skipUnless` pattern wearing a new name.
- Phase E: run `./bin/cadre sdlc --provider providers/agentic-sdlc-defaults/provider.json provider list`
  **before** the change and record the `duplicates profile ids` error. That
  error is the requirement; without it the acceptance passes on unmodified code.
- Phase C: point the module lists at an empty directory → self-vacuity guard fires.
- Phase C: point `_SHARED_SRC_DIR` at the resolved roster root → the PP-FR-1
  assertion fails. This is the one that matters most: it is the assertion
  standing between OD-2's project-local scope and a `.agents/cadre.yaml` that
  chooses which `settings.py` the platform executes.

## 3. Sequencing and stop points

**Reordered at Revision 6.** The old table is preserved below the new one,
because its row for Phase C carried the "~9 category-B" figure that oversized
the phase, and a reader of Revision 5 should be able to see what happened to it.

| Order | Phase | Delivered | Status |
| --- | --- | --- | --- |
| 1 | **D** | Knowledge store pinned platform-anchored, before the phase that can break it | **Landed.** 7 tests, evidence attached |
| 2 | **0** | Proof the seam is real, with nothing moved | **Run and discarded.** Also found G-12 |
| 3 | **A′** | Roster root resolved from a manifest; default unchanged | **Landed.** `provider/roster.json` deferred — see the phase |
| 4 | **C′-1** | Boundary guard, self-vacuity, `:18`/`:24` assertions, six category-B fixes, forced lifecycle detector | **Landed.** Ran ahead of B′ — see below |
| 5 | **C′-2** | OD-9's roster-declared gate reviewer | **Landed.** Cadre byte-identical |
| 6 | **B′** | The fixture roster makes Phase 0's answer permanent and regression-tested | Next. Gated on nothing |
| 7 | **E** | `cadre sdlc` ergonomics | Deferrable indefinitely — the capability already exists via `bin/agentic-sdlc` |

**C′ ran before B′, inverting Revision 6's order, and the reason is worth
recording.** That order existed so the guard could be written against a real
second roster — *"before a second roster exists there is no way to tell a guard
that works from a guard that cannot fail."* The Phase 0 spike supplied that
roster without B′ having landed, so the guard was written and fault-injected
against a genuine foreign roster anyway. The dependency was on **a** second
roster, not on B′ specifically, and Revision 6 conflated the two.

**Superseded (Revision 5's table), kept for the record:**

| After | Delivered | Safe to stop? |
| --- | --- | --- |
| A | Roster root resolvable; default unchanged | Yes — a latent capability, no behaviour change |
| B | **Proof the seam is real** | Yes, and this is the natural stop if the answer is "it isn't" |
| C | Boundary guard, ~~~9~~ **6** category-B path fixes, the lifecycle-aware detector, and (pending OD-9) the `code-reviewer` default | **Only after OD-9.** This is the one phase that can change default Cadre selection, so "safe to stop" depends on which OD-9 option was taken |
| D | Knowledge store roster-independent in fact, not just in principle | Yes |
| E | Kernel reachable with a foreign bundle | Complete |

### 3.1 Decision gating — all resolved at Revision 7

Kept with its rows answered rather than deleted, because the shape of what
depended on what is the most reusable thing this plan produced. Every cell that
read *gated* now reads *proceed*.

| Phase | Gating at Revision 6 | Now |
| --- | --- | --- |
| **D**, **0** | proceed | proceed — unchanged, and still the two to start with |
| **A′** core | proceed; redone if OD-2 narrowed | **proceed.** OD-2 narrowed, so the simpler form is the one being built: no visibility plumbing, no MCP restructuring. Gains one item — unknown-`schema_version` rejection (OD-11) |
| **A′** identity + bump | gated on OD-7; ceases to exist if OD-2 narrows | **ceased to exist.** Removed from the phase |
| **B′** | proceed | proceed |
| **C′-1** | proceed | proceed |
| **C′-2** | hard-gated on OD-9 | **proceed** — OD-9 resolved to option 1, so this is a specified change |
| **C′** MCP redirect parity | hard-gated on OD-10; ceases to exist if OD-2 narrows | **ceased to exist.** The two `mcp/*` lines still change in C′-1 as category-B fixes; nothing else is owed |
| **E** | proceed | proceed, and still last |

**What that table records, read as a whole: one decision closed four rows.**
OD-2's reversal removed two phases' worth of gated work outright and simplified
a third. The matrix was built at Revision 6 to show what was blocked; its more
useful reading turned out to be which single answer unblocked the most, and the
plan did not have that view of itself until the matrix existed.

**Critical path, updated:** `D → 0 → A′ → B′ → C′-1 → C′-2`, with nothing
hanging off it waiting on a human. **E, and the ~15-assertion confirmation, are
off the path entirely** — the latter is now a check that Cadre's output did not
move, not an input to a decision, since OD-9 chose the option that preserves it.

## 4. What this plan does not do

- No git repository split, no directory move, no rename.
- No kernel or engine edit. **If an implementation finds itself editing
  `kernel/` or `engine/`, it has silently reversed OD-5** — stop and revise
  `requirements.md`, rather than patching around it in the implementation.
- ~~No `selection.schema.json` bump.~~ **Reversed at Revision 2**: OD-2's
  disposition forces 6 → 7 (PP-NFR-3b), gated on OD-7.
- No fix for G-1 (`aides.yaml` authority duplication), G-2 (stray-copy check
  covers one contract file), G-4 (`sample-selection-output.md` drift guard), or
  — added at Revision 6 — **G-8** (`_select_workflow()`'s Cadre route and risk
  ids sit outside PP-FR-6's forbidden-token list, so a foreign roster can never
  reach `rollback`, `production-release`, or `support-escalation`), **G-9** (the
  golden corpus has no generator), or **G-10** (`docs/proposals/` has no
  supersession pointer). All are real, all are adjacent, all deserve their own
  change.
- No G1 or G2 approval. `@deagy` decides both.
- ~~No resolution of **OD-9**.~~ **Resolved 2026-08-11** — option 1 via a
  `routing.yaml` key, with the fork inside it settled against provider-profile
  `gate_bindings` (`product-intent.md` §17). The reasoning stands and is worth
  keeping: whether Cadre's `support` lists may lose `code-reviewer` was never an
  implementation detail for whoever reached Phase C first, and the option chosen
  is the one under which they do not.
- ~~No resolution of OD-10, OD-11, OD-12, or OD-13.~~ **OD-10, OD-11 and OD-13
  were closed on 2026-08-11** (`product-intent.md` §17). **OD-12 remains open**
  and this plan does not touch it: whether G1 extends to the current revision is
  the Product Owner's judgment, and it got sharper rather than softer once they
  reversed one of their own dispositions.
- ~~No reversal of OD-2.~~ **Reversed by the Product Owner on 2026-08-11**, to
  `SCOPE_GLOBAL_ONLY`. This plan recorded the reopening request and held no
  authority to decide it; the decision was made where it belonged.
- **No fix for G-11** (`_gate_agents()` reads two contract keys the kernel has
  never declared). OD-9 makes the `review_agents` default roster-supplied, which
  is this work's business; retiring the dead reads is a change against the
  kernel contract and is not.
