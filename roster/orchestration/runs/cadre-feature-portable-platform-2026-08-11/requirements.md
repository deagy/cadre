# Requirements Baseline — A roster-neutral platform

**Requirements ID:** `REQ-CADRE-PORTABLE-PLATFORM`
**Revision:** 1 (initial)
**Status:** draft — **awaiting G1**, then human review
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

1. **Sequencing is out of order, and disclosed.** `roster/workflows/product-intake.md`
   step 4 places requirements decomposition *after* a human G1 approval. This
   baseline was drafted alongside the intent record, before any G1 decision. It
   is therefore a **draft against an unapproved intent** and is invalidated
   wholesale if the Product Owner rejects OD-1. Stated so a reader does not
   mistake its existence for evidence that G1 passed.

2. **Extending `provider.json` requires editing the kernel.** The intent record's
   first draft recommended it. `kernel/agentic_sdlc/__init__.py:197` holds a
   closed `allowed_manifest_keys` set and `:199-200` raises *"provider manifest
   contains unknown fields"* on anything else; `engine/agentic_sdlc_langgraph/provider.py`
   duplicates the set as `_ALLOWED_MANIFEST_KEYS`. Adding a roster-side key means
   a coordinated change to two codebases, one of them the kernel — inside a
   change whose constraint is to leave that boundary alone. **PP-FR-2 therefore
   specifies a sibling manifest** and the alternative survives only as OD-5.

3. **`selection.schema.json` is at `schema_version: 6`, not 4.** The sibling
   record `cadre-proposal-01-route-match-reasons-2026-08-08/requirements.md`
   documents a 3 → 4 bump; two further bumps have landed since (5, then 6 for
   `workflow_shape` reporting — see that field's description at
   `selection.schema.json:38`). Any requirement here that changes the emitted
   field set must bump to 7, and `RUNBOOK.md`'s "When `schema_version`
   increments" rule governs. **PP-FR-1..6 are designed to emit no new plan
   field**, so no bump is required — but that is a constraint on the
   implementation, not an accident, and PP-NFR-3 pins it.

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
| §7 C3/C4 | PP-FR-3, PP-NFR-3, PP-NFR-4 |
| §12 success criteria 1–7 | PP-FR-1..6, PP-NFR-1..4 |
| §13 OD-2 | PP-FR-1 (scope choice) |
| §13 OD-5 | PP-FR-2 (manifest choice) |

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

*Scope is OD-2 and is not settled here.* The two candidates carry different
failure modes and both must be stated:
- `SCOPE_GLOBAL_ONLY` matches `agentic_sdlc.bin_path` (`settings.py:665-672`) and
  `knowledge_store.home`, whose comment at `:681-689` gives the reasoning
  verbatim: a project-local `.agents/cadre.yaml` *"arrives with `git clone` and
  is editable by anyone who can open a pull request."* A roster chooses which
  role prose is dispatched — strictly more powerful than choosing a database
  path.
- Project-local scope is what the feature is *for*: a project using its own
  roster.

*Acceptance:* `cadre select --roster <path>` loads catalog, routing, and role
definitions from `<path>`; with no flag and no env var, behaviour is
byte-identical to today (pinned by the existing golden corpus,
`fixtures/selection_golden_corpus.json`, which must not be edited — see
PP-NFR-1). *Verifier:* `test_selector.py`, plus whichever scope test OD-2 implies
(`test_kernel_boundary.py:129-140` is the model for a scope assertion).

**PP-FR-2 — A roster package is declared by a sibling `roster.json`.**
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
- `git status --porcelain` shows no change under `plugin/`, `provider/roles/`,
  `roster/catalog.yaml`, or `roster/orchestration/routing.yaml`.

**PP-NFR-2 — No fourth copy.** Knowledge-store code exists in three places today:
`plugin/suite/roster/knowledge-store/` (15 files, selected by
`generate_global_plugin.py:1443-1451`), the wheel's `cadre_cli/_vendor/` (via
`pyproject.toml:118-124`), and the Cline path-rewrite table
(`port_cline_agents.py:174-448`). Selector code is similarly vendored.
*Acceptance:* the file count under `plugin/suite/roster/knowledge-store/` is
unchanged and no new vendored tree appears; asserted by the packaging tests.

**PP-NFR-3 — No `selection.schema.json` bump.** Per §0.3 the schema is at
`const: 6`, is closed (`additionalProperties: false`), and is **vendored away
from its producer** into both the wheel and the plugin — so a pinned consumer
copy rejects any change to the emitted field set while truthfully reporting the
version it handles. PP-FR-1..6 must emit no new plan field and retype none.
*Acceptance:* `schema_version` still reads `6`; a plan generated on the branch
validates against the committed schema unmodified.

**PP-NFR-4 — Every new guard is proved non-vacuous.** For each of PP-FR-2,
PP-FR-3(c), and PP-FR-6: plant the defect, confirm the check **fails** with a
message naming the real cause, revert, confirm the tree is clean. Recorded in the
pull request. This is the repository's settled bar and, given §0's history, not
discretionary.

**PP-NFR-5 — Determinism preserved.** `dispatch_fingerprint` for an unchanged
task against the default roster must be identical before and after.
*Acceptance:* asserted directly, and implied by PP-NFR-1's golden-corpus clause.

## 4. Deliberately unchanged

- **`kernel/` — nothing.** PP-FR-4 is a wrapper flag; `load_provider()` needs no
  edit. If an implementation finds itself editing the kernel, OD-5 was answered
  the other way and this baseline needs revising first.
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
- **G-5: this baseline precedes its own G1** (§0.1).

## 7. Handoff

**To:** the human Product Owner (`@deagy`) and Engineering Lead for a **G2**
decision — *after* G1, and void if G1 rejects.

**Blocking before this baseline can be accepted:** OD-1 (bring the parked
proposal forward at all), OD-2 (`roster.root` scope — PP-FR-1 cannot be
implemented without it), OD-5 (manifest shape — PP-FR-2 depends on it).
Non-blocking: OD-3, OD-4, OD-6.

Per `roster/workflows/product-intake.md`, objective conflicts return to G1 rather
than proceeding. Nothing here is approved by its presence in this file.
