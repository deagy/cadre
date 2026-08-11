# Requirements Baseline — A roster-neutral platform

**Requirements ID:** `REQ-CADRE-PORTABLE-PLATFORM`
**Revision:** 2
**Status:** draft — **G1 approved 2026-08-11; awaiting G2.** OD-7 blocking.
**Revision note:** Revision 2 folds in the Product Owner's OD-2 and OD-5
dispositions (`product-intent.md` §16), which resolve PP-FR-1's scope and
PP-FR-2's manifest shape, and **retracts PP-NFR-3** — OD-2's answer forces a
`selection.schema.json` bump that Revision 1 forbade. Revision 1's requirement
identifiers are preserved; none was renumbered.
**Author (agent):** requirements-agent, consolidated by the orchestrating session
**Date:** 2026-08-11
**Repository:** `/home/deagy/sdk/cadre`
**Classification:** internal
**Decomposes:** `INTENT-CADRE-PORTABLE-PLATFORM` (`product-intent.md`, Revision 1)

---

## 0. Grounding

Every file:line below was read in the tree on 2026-08-11, not inferred from the
intent record or from `docs/proposals/governance-as-product-2026-08.md`. Four
corrections are load-bearing and are recorded here rather than silently applied.

1. **Sequencing was out of order, and is disclosed rather than tidied away.**
   `roster/workflows/product-intake.md` step 4 places requirements decomposition
   *after* a human G1 approval. Revision 1 was drafted alongside the intent
   record, **before** any G1 decision. G1 was subsequently approved on
   2026-08-11 (`product-intent.md` §16), which makes this baseline reviewable —
   it does not retroactively make the order correct, and this note stays.

2. **Extending `provider.json` requires editing the kernel.** The intent record's
   first draft recommended it. `kernel/agentic_sdlc/__init__.py:197` holds a
   closed `allowed_manifest_keys` set and `:199-200` raises *"provider manifest
   contains unknown fields"* on anything else; `engine/agentic_sdlc_langgraph/provider.py`
   duplicates the set as `_ALLOWED_MANIFEST_KEYS`. Adding a roster-side key means
   a coordinated change to two codebases, one of them the kernel — inside a
   change whose constraint is to leave that boundary alone. **PP-FR-2 therefore
   specifies a sibling manifest** and the alternative survives only as OD-5.

3. **`selection.schema.json` is at `schema_version: 6`, and Revision 2 now
   requires a bump to 7.** The sibling record
   `cadre-proposal-01-route-match-reasons-2026-08-08/requirements.md` documents
   a 3 → 4 bump; two further bumps have landed since (5, then 6 for
   `workflow_shape` reporting — see that field's description at
   `selection.schema.json:38`).

   Revision 1 asserted that PP-FR-1..6 emit no new plan field and pinned that as
   PP-NFR-3. **OD-2's disposition invalidated it**: project-local roster
   selection was accepted on condition that the resolved roster's id and digest
   *surface in the dispatch plan*, which is by definition a new emitted field.
   PP-NFR-3 is retracted and replaced by PP-NFR-3b. The visibility is the
   security control that made project-local scope acceptable, so it is not
   negotiable against the bump — OD-7 asks only whether the identity surfaces in
   the plan or somewhere outside it.

4. **The knowledge store's `--agent` argument validates nothing.**
   `service.py:164-167` checks only non-emptiness; the value reaches only the
   audit row (`:176`) and the returned bundle (`:184`), never a filter. All
   roster-side validation is upstream in `build_dispatch_plan.py:496-498`, which
   raises on a missing `knowledge_focus`, and `knowledge_focus` composes *query
   text* — a prompt fragment, not an access-control key. Consequence for this
   baseline: **PP-FR-5 is a path-resolution change only.** It moves no security
   boundary, because there is no roster-derived boundary there to move.

## 1. Traceability

| Intent section | Requirements |
| --- | --- |
| §2 problem (selector), §4 outcome 1 | PP-FR-1, PP-FR-2, PP-FR-3 |
| §2 problem (kernel), §4 outcome 2 | PP-FR-4 |
| §2 problem (knowledge store), §4 outcome 3 | PP-FR-5 |
| §4 outcome 4, §9 condition 5 | PP-FR-6 |
| §6 exclusions, §7 C1/C2 | PP-NFR-1, PP-NFR-2 |
| §7 C3/C4 | PP-FR-3, PP-NFR-4 |
| §12 success criteria 1–7 | PP-FR-1..6, PP-NFR-1..4 |
| §16 OD-2 disposition | PP-FR-1 (project-local scope), **PP-FR-1b** (visibility) |
| §16 OD-5 disposition | PP-FR-2 (sibling manifest), §4 (kernel untouched) |
| §16 OD-7 (open, blocking G2) | **PP-NFR-3b** (schema 6 → 7), PP-NFR-5 (churn) |

## 2. Functional requirements

**PP-FR-1 — The roster root is resolved, not computed.**
A new `roster.root` FieldSpec in `roster/shared/src/settings.py`, following the
shape of `knowledge_store.home` (`:673-680`) and `context_store.home` (`:690-697`):
`env_var="CADRE_ROSTER_ROOT"`, `kind="path"`, `required=False`,
`default_static=None`, falling back to today's checkout-relative value. Plus a
`--roster` flag on `cadre select`. The three computed constants —
`select_agents.py:16-18`, `build_dispatch_plan.py:29-30` — and their five use
sites (`select_agents.py:203` catalog, `:204` routing, `build_dispatch_plan.py:501`
knowledge CLI, `:604` context-pack definitions, plus role-definition reads) route
through the resolver instead.

**Scope: project-local, on the overlay pattern** (OD-2, resolved 2026-08-11 —
`product-intent.md` §16). This is a deliberate departure from the three sibling
path/executable settings, all of which are `SCOPE_GLOBAL_ONLY`, so the departure
is justified rather than assumed: `agentic_sdlc.bin_path` (`settings.py:665-672`),
`knowledge_store.home` (`:673-680`), and `context_store.home` (`:690-697`). The
comment at `:681-689` states the objection verbatim — a project-local
`.agents/cadre.yaml` *"arrives with `git clone` and is editable by anyone who can
open a pull request"* — and choosing a roster is strictly more powerful than
choosing a database path, because it selects the role prose an agent is handed.

The objection is answered by **visibility, not prohibition** (PP-FR-1b), on the
precedent of `roster/orchestration/src/routing_overlay.py`, which already permits
a project-local `.agents/orchestration/routing-overlay.json` under fail-closed
narrowing restrictions with `human_gate`/`reviewers` immutable.

**PP-FR-1b — the resolved roster is legible in the plan.**
Whenever the roster resolves to anything other than the default, the dispatch
plan carries the resolved roster's `id` and a digest of its manifest. A
redirected roster must be visible to a human reading the plan rather than
silent. Digest computation should reuse the kernel's `fingerprint()` shape
(key-sorted, whitespace-free JSON sha256, `sha256:<hex>`) for consistency with
`provider_bindings`, ported rather than imported (PP-FR-6 / `test_kernel_boundary.py`).
*This forces PP-NFR-3b.* *Acceptance:* a plan generated against the fixture
roster names it; a plan against the default is unchanged. *Verifier:*
`test_roster_package.py`.

*Acceptance:* `cadre select --roster <path>` loads catalog, routing, and role
definitions from `<path>`; a project-local `.agents/cadre.yaml` may set
`roster.root`; with neither set, behaviour is byte-identical to today (pinned by
the existing golden corpus, `fixtures/selection_golden_corpus.json`, which must
not be edited — see PP-NFR-1). *Verifier:* `test_selector.py`, plus a scope test
on the model of `test_kernel_boundary.py:129-140` asserting the field is **not**
`SCOPE_GLOBAL_ONLY` — the inverse of that precedent, and worth pinning precisely
because it departs from three siblings and would otherwise look like an
oversight to a later reader.

**PP-FR-2 — A roster package is declared by a sibling `roster.json`.**
Resolved at OD-5 (2026-08-11). Chosen because it requires **zero** change to
`kernel/` and `engine/`; see §4 for the constraint this places on
implementation.
The kernel never reads it. It declares, at minimum: `schema_version`, `id`,
`version`, `catalog` (path to `catalog.yaml`), `routing` (path to `routing.yaml`),
`role_root` (path under which `definition` entries resolve), and
`shared_policy_root`. Every path resolves relative to the manifest directory and
must reject escapes, reusing the containment logic the kernel already applies in
`provider_resource()` (`kernel/agentic_sdlc/__init__.py:159-169`) — or
`roster/orchestration/src/glob_containment.py`, which exists in-tree for related
work. A `roster.schema.json` alongside `routing.schema.json` and
`selection.schema.json`, validated by the existing
`roster/orchestration/src/schema_validate.py` and its pre-commit hook.

`provider/` gains a `roster.json` describing what it already contains: 159
`AGENT.md` files under `provider/roles/`, `agent-catalog.json`, `profiles/`,
`extensions/`. Verified count: 159 files, matching `roster/catalog.yaml`'s 159
`definition:` entries. Only `catalog.yaml` and `routing.yaml` need to join it.

*Acceptance:* a manifest declaring `catalog` + `routing` drives selection; a
manifest with a path escaping its directory is rejected with a message naming the
offending field. *Verifier:* new `test_roster_package.py`.

**PP-FR-3 — A second roster exists, and is exercised.**
A deliberately minimal fixture roster under
`roster/orchestration/test/fixtures/minimal-roster/`: ≈3 roles with their own
`AGENT.md` files, its own `catalog.yaml`, its own `routing.yaml` with rules
naming only those roles, and a `roster.json`. It must **not** be a subset copy of
Cadre's — a copy would satisfy every assumption Cadre happens to satisfy, which
is the exact blindness the parked proposal's condition 3 names.

*Acceptance:* (a) `cadre select --roster <fixture> --task "…"` emits a plan
validating against `roster/orchestration/selection.schema.json` and naming only
fixture role ids; (b) a task matching no fixture rule returns `needs-triage`, not
a Cadre role and not a guess (intent §7 C3); (c) a fixture with `catalog.yaml`
removed fails with a message naming `catalog.yaml` (C4). *Verifier:*
`test_roster_package.py`. **(c) is non-vacuity and is not optional** — see
PP-NFR-4.

**PP-FR-4 — `cadre sdlc` accepts a provider override.**
`bin/cadre.py:124` currently hardcodes `provider = REPO_ROOT / "provider" / "provider.json"`
and passes it as `--provider` to the kernel binary (`:125-127`). The kernel side
needs **no change**: `--provider` is already a repeatable global option there,
`load_provider()` (`__init__.py:192-304`) already validates a foreign manifest
fully, and `providers/agentic-sdlc-defaults/` is already a working second
provider. This requirement is a wrapper flag and a default, nothing more.

*Acceptance:* `cadre sdlc --provider <other>/provider.json provider list` reports
that provider loaded; with no flag, the argument vector passed to the kernel is
byte-identical to today's. *Verifier:* new case in the `bin/` dispatch tests.

**PP-FR-5 — The knowledge store resolves without a roster.**
`build_dispatch_plan.py:29` computes `KNOWLEDGE_STORE_ROOT = Path(__file__).resolve().parents[2] / "knowledge-store"`
and `:501` emits `str(KNOWLEDGE_STORE_ROOT / "src" / "cli.py")` into every
dispatch plan's `knowledge_context.requests[].invocation.args`. That absolute
path is consumed cross-language — `cline-plugins/cline-agents/index.ts:247-259`
executes it directly — so this is a published contract, not an internal detail.
Resolution moves to the settings resolver; the emitted *shape* is unchanged.

Per §0.4 this moves no security boundary. The `knowledge_store.home` global-only
scope control (`settings.py:673-680`) is untouched.

*Acceptance:* with `CADRE_ROSTER_ROOT` pointing at a directory containing no
roster, `cadre knowledge context …` succeeds; a generated plan's `invocation.args`
still carries an absolute path to a `cli.py` that exists. *Verifier:*
`test_scope_enforcement.py` (which already asserts the dispatch plan always
supplies `--source`, at `:135-155`) plus `test_selector.py`.

**PP-FR-6 — The mirror boundary guard.**
`test_kernel_boundary.py` guards roster → kernel (four tests, `:76-167`). Nothing
guards platform → roster, because until PP-FR-1 nothing could violate it. Add
`roster/orchestration/test/test_roster_boundary.py`, modelled on
`test_context_boundary.py:157-215`, which already enforces a two-way
don't-name-each-other rule between the knowledge and context stores — including
the string-literal check, not just imports.

Platform modules, for this purpose: `select_agents.py`, `build_dispatch_plan.py`,
`risk_classifier.py`, `routing.py`, `routing_overlay.py`, and
`roster/knowledge-store/src/`. Forbidden: a hardcoded Cadre role id, phase name,
or `roster/`-relative path in any of them.

Note `test_context_boundary.py:150-155` carries a **self-vacuity guard** — it
asserts the directories exist and contain modules, so that a rename cannot make
every check silently pass over an empty set. Reproduce it. This repository has
found "a guard that passes while verifying nothing" roughly a dozen times in a
single session (`docs/proposals/durable-knowledge-capture-2026-08.md`).

*Acceptance:* a planted violation fails the test with a message naming the file
and the offending token; the self-vacuity guard fails if the module lists resolve
empty. *Verifier:* the test itself, plus a recorded planted-violation run.

## 3. Non-functional requirements

**PP-NFR-1 — Cadre is observably unchanged.** The "leave cadre alone" constraint,
made checkable rather than asserted.
*Acceptance, all four:*
- `./bin/cadre generate-plugin --output plugin --check` reports no drift.
- `roster/orchestration/test/test_repository_health.py` and
  `python3 -m unittest discover -s plugin/tools -p "test_*.py"` pass.
- `fixtures/selection_golden_corpus.json` is **not edited** — ~60 hand-maintained
  blocks with no generator script (recorded in
  `cadre-proposal-01-route-match-reasons-2026-08-08/requirements.md` §3). Editing
  it would mean default selection behaviour changed.

  **This detector survives PP-NFR-3b's schema bump, and that was verified rather
  than assumed.** The corpus pins selection *outcomes* only — each case carries
  `expected.primary` / `.reviewers` / `.support`, and the file contains **zero**
  occurrences of `schema_version` or `dispatch_fingerprint`. So a bump and a new
  emitted field leave it untouched, and it keeps meaning what PP-NFR-1 needs it
  to mean. Had it pinned either, this requirement and PP-NFR-3b would have been
  in direct conflict and one would have had to give.
- `git status --porcelain` shows no change under `plugin/`, `provider/roles/`,
  `roster/catalog.yaml`, or `roster/orchestration/routing.yaml`.

**PP-NFR-2 — No fourth copy.** Knowledge-store code exists in three places today:
`plugin/suite/roster/knowledge-store/` (15 files, selected by
`generate_global_plugin.py:1443-1451`), the wheel's `cadre_cli/_vendor/` (via
`pyproject.toml:118-124`), and the Cline path-rewrite table
(`port_cline_agents.py:174-448`). Selector code is similarly vendored.
*Acceptance:* the file count under `plugin/suite/roster/knowledge-store/` is
unchanged and no new vendored tree appears; asserted by the packaging tests.

**~~PP-NFR-3 — No `selection.schema.json` bump.~~ RETRACTED at Revision 2.**
Invalidated by OD-2's disposition, which requires the resolved roster to surface
in the plan (PP-FR-1b). Retained struck through rather than deleted, so a reader
of Revision 1 finds out what happened to it.

**PP-NFR-3b — `selection.schema.json` bumps 6 → 7, and the bump is proved
meaningful.** The schema is closed (`additionalProperties: false`, `:6`) and
**vendored away from its producer** into both the wheel (`pyproject.toml`'s
`cadre_cli/_vendor/` force-include) and `plugin/`. A pinned consumer copy
therefore rejects a plan carrying an unknown property *while the plan truthfully
reports the `schema_version` that copy claims to handle* — a silent failure
naming the wrong cause. The bump converts that into an error naming the real one.
`RUNBOOK.md`'s "When `schema_version` increments" rule governs; the "optional and
not emitted by default" carve-out **does not apply**, for the same reason it did
not apply at 5 → 6 (`selection.schema.json:38`): the consumer this field exists
for sees it unconditionally.
*Acceptance:* the emitted `schema_version` and the schema's `const` both read
`7`; **a plan from this branch is confirmed to fail the previous v6 schema**,
proving the bump is meaningful rather than cosmetic. Every `dispatch_fingerprint`
changes as a consequence — expected, documented in `CHANGELOG.md`, and not a
determinism regression (PP-NFR-5 governs same-version reproducibility only).

**PP-NFR-4 — Every new guard is proved non-vacuous.** For each of PP-FR-2,
PP-FR-3(c), and PP-FR-6: plant the defect, confirm the check **fails** with a
message naming the real cause, revert, confirm the tree is clean. Recorded in the
pull request. This is the repository's settled bar and, given §0's history, not
discretionary.

**PP-NFR-5 — Determinism preserved; fingerprint churn expected.** Revision 1
required `dispatch_fingerprint` to be *identical before and after* for a default-
roster task. **That is now false and is corrected here.**
`build_dispatch_plan.py:840` fingerprints the whole dispatch dict minus
`generated_at`, `dispatch_fingerprint`, and `provenance` — and `schema_version`
is inside that payload. PP-NFR-3b's 6 → 7 bump therefore changes **every** plan's
fingerprint, default roster included, before PP-FR-1b's new field is considered
at all.

The requirement is restated as same-version reproducibility, matching the
precedent set at 3 → 4 (`cadre-proposal-01-route-match-reasons-2026-08-08/requirements.md`
R7/R8): two identical `build_dispatch_plan()` calls produce byte-identical output
and an identical fingerprint, and fingerprints are comparable only between plans
from the same producer version. Any new ordering introduced by PP-FR-1b (roster
id, digest) must be stable for the same reason.
*Acceptance:* two identical calls agree byte-for-byte; `CHANGELOG.md` names the
churn as a consequence so it is not later misread as a determinism regression.

## 4. Deliberately unchanged

- **`kernel/` — nothing, and this is now a decision rather than an expectation.**
  OD-5 chose the sibling manifest *specifically* to avoid a kernel edit. PP-FR-4
  is a wrapper flag; `load_provider()` needs none. **If an implementation finds
  itself editing `kernel/` or `engine/`, it has silently reversed OD-5** — stop
  and revise this baseline rather than proceeding. PP-FR-1b's digest is a port of
  the kernel's `fingerprint()` shape, not an import, for the same reason
  (`test_kernel_boundary.py:76-95`).
- **`engine/`** — its provider loader is already pure and reentrant
  (`engine/agentic_sdlc_langgraph/provider.py`). Its checkout-only status
  (`runtime.py:147`, `pyproject.toml` version pinned `0.0.0` with
  `Private :: Do Not Upload`) is a packaging defect, not a roster coupling, and
  is out of scope.
- **`roster/shared/src/{settings,content_protection,text_chunking,text_embedding}.py`
  stay where they are**, declared platform-owned by PP-FR-6's guard rather than
  moved. Moving them would touch `plugin/suite/`, the wheel force-include list,
  and the Cline rewrite table — precisely the cost C1/C2 exist to avoid.
- **The `AUTHORITY_ROLES` enum** (`kernel/agentic_sdlc/__init__.py:65-92`) and all
  gate semantics.
- **`roster/knowledge-store/AGENT.md`** stays put pending OD-4.
- **`docs/proposals/governance-as-product-2026-08.md`** — never revised once
  decided (`test_repository_health.py:2154`).

## 5. Out of scope

- Any git repository split (intent §6, §7 C1).
- Any directory move.
- Publishing, PyPI, or release-line changes.
- Making the LangGraph engine installable.
- Renaming Cadre, the platform, or `provider.json`'s `"id": "cadre"` (OD-3).
- Authoring a real second roster as a product. PP-FR-3 delivers a test fixture.
- Answering whether the platform or the roster is the product.

## 6. Known gaps

- **G-1: `roster/authority/aides.yaml` silently duplicates kernel authority, and
  nothing catches divergence.** `aides.yaml:20-22` declares
  `product-owner-aide: gates [1, 2, 6]`, mirroring the kernel's
  `"product_owner": ["G1", "G2", "G6"]` (`__init__.py:66`).
  `validate_gates_against_kernel_contract()` (`generate_authority_aides.py:97-124`)
  checks only that each referenced **gate id exists** in the live contract — not
  that the role→gate *mapping* agrees — and **returns early when the kernel is
  unreachable** (`:110-111`), matching every other lifecycle-aware path. So a
  kernel-side change to *which authority owns a gate* is caught nowhere, and a
  gate renumber is caught only when a kernel happens to be installed. Directly
  adjacent to this work's subject (roster/kernel separation) but not fixed by it.
  Worth filing.
- **G-2: `test_kernel_boundary.py`'s stray-copy check covers one contract file.**
  `:158-167` asserts `ROSTER_ROOT.rglob("lifecycle-gates.json")` is empty, but
  makes no equivalent assertion for `mutation-gates.json` or
  `run-record.schema.json`, which it requires to exist under `kernel/contracts/`
  in the same test. A roster-side copy of either would drift undetected.
- **G-3: the parked proposal is stale on role count** — 74 at lines 24 and 126;
  the catalog holds 159. Its argument is unaffected.
- **G-4: `docs/sample-selection-output.md` still has no drift guard.** Recorded as
  G-1 in the 2026-08-08 sibling record and still open; PP-NFR-1 does not close it.
- **G-5: this baseline was drafted before its own G1** (§0.1). G1 approved
  2026-08-11, after the fact. The gap is disclosed, not closed.
- **G-6: the G1 approval has no machine-checkable evidence.** This repository
  runs no `.agentic-sdlc/` overlay and holds no run records, so the approval
  exists as prose transcribed by the authoring agent (`product-intent.md` §16).
  Nothing in CI can verify it, and no `agentic-sdlc decide` was invoked because
  there is no record to decide against. A consuming project would have had a run
  record here; this one deliberately does not.

## 7. Handoff

**To:** the human Product Owner (`@deagy`) **and Engineering Lead** for a **G2**
decision. G1 approved 2026-08-11 (`product-intent.md` §16). G2 requires **both**
authorities per `kernel/contracts/lifecycle-gates.json` —
`"G2": {"authority_requirements": ["product_owner", "engineering_lead"]}` — and
this repository has one maintainer, which is a real obstacle rather than a
formality. `docs/migration/monorepo-migration.md` records the parallel case:
`required_approving_review_count` stays `0` because "a required-review setting
with nobody to satisfy it blocks releases without adding a reviewer."

**Blocking before this baseline can be accepted:** **OD-7** — the
`selection.schema.json` 6 → 7 bump forced by OD-2's disposition. It changes a
published, vendored contract, which is not the authoring session's call.
Non-blocking: OD-3, OD-4, OD-6.

**Resolved and folded in at Revision 2:** OD-1 (proceed), OD-2 (project-local,
overlay-style), OD-5 (sibling manifest).

Per `roster/workflows/product-intake.md`, objective conflicts return to G1 rather
than proceeding. Nothing here is approved by its presence in this file.
