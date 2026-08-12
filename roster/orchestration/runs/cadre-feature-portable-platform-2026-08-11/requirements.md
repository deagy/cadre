# Requirements Baseline — A roster-neutral platform

**Requirements ID:** `REQ-CADRE-PORTABLE-PLATFORM`
**Revision:** 5
**Status:** draft — **G1 approved 2026-08-11; awaiting G2.** OD-7 blocking;
**OD-9 new and blocking G2.**
**Revision note:** Revision 2 folded in the Product Owner's OD-2 and OD-5
dispositions (`product-intent.md` §16), which resolve PP-FR-1's scope and
PP-FR-2's manifest shape, and **retracted PP-NFR-3** — OD-2's answer forces a
`selection.schema.json` bump that Revision 1 forbade.

**Revision 3 corrects this baseline against the tree**, after a review that
re-read every cited `file:line`. Six of its corrections change what an
implementation would do, and none is cosmetic: PP-FR-1 was missing two of the
six computed constants, one of which (`select_agents.py:24`) cannot follow the
roster root without importing the platform's own resolver out of a foreign
roster; PP-FR-5's acceptance criterion was unsatisfiable as written; PP-FR-6's
forbidden-token rule already fails against `build_dispatch_plan.py` today and
that is now scoped rather than discovered; PP-FR-2's manifest location collides
with the plugin packaging allowlist and the collision is resolved here; PP-FR-1b
and PP-NFR-5 asserted contradictory things about the same plan; and the golden
corpus holds **175** cases, not the "~60" carried over from a sibling record
that was counting something else. Requirement identifiers are preserved; none
was renumbered.

**Revision 4 corrects Revision 3.** A second review found that four of
Revision 3's corrections were themselves wrong, two of them in the direction
that ships a defect:

- **PP-FR-1's exceptions were stated as "unchanged" when they must be
  *rewritten*.** `select_agents.py:18` and `:24` are *derived from* `ROSTER_ROOT`
  (`:17`). Leaving those two lines alone while `:17` becomes resolver-driven is
  exactly the vulnerability Revision 3 raised them to prevent.
- **OD-8 is withdrawn**, and PP-FR-6's second "violation" with it. Revision 3
  misread `_select_workflow()`: its final stage reads each route's declared
  `workflow_shape`, which a foreign roster supplies. There was no blocker.
- **PP-NFR-1's fourth bullet was still unsatisfiable** — `roster.schema.json` is
  a second file bundled into `plugin/suite/`, and `plugin/suite/` mirrors source
  this work edits, so "byte-identical" could never hold.
- **PP-FR-5 as Revision 3 wrote it is a no-op** whose acceptance passes against
  unmodified code. Restated as what it actually is.

`G-7` is rewritten: the twelve knowledge records are *staged*, not ingested, and
staging confers no retrievability — so the retrieval Revision 3 called a failure
was correct behaviour (`product-intent.md` §14).

**Revision 5 is the largest correction so far, and two of its findings change
what this work is.** A third review ran the code instead of only reading it:

- **PP-FR-4 was based on a false premise.** `--provider` *already* reaches the
  kernel — `bin/cadre.py:125` passes `*rest` through and the kernel's flag is
  `action="append"`. Verified by running it: a foreign provider loads and is
  then **rejected for colliding with Cadre's injected bundle**. The requirement
  is not "add a flag"; it is "let the caller suppress the injected default."
  Its Revision 1–4 acceptance passes on unmodified code.
- **PP-FR-6's `code-reviewer` fix changes default Cadre selection**, and the
  detector PP-NFR-1 relies on is structurally blind to it. Verified by running
  `cadre select`: `code-reviewer` is in `support` today and disappears when the
  default is removed. This is now **OD-9**.
- **A second selection entry point was missing from all five revisions** —
  `roster/orchestration/mcp/dispatch_core.py:56` and `mcp/dispatch_server.py:63`
  resolve catalog and routing checkout-relative, independently of
  `select_agents.py`.
- **PP-NFR-1's "exact `plugin/` diff" was wrong in three further ways**: the
  copy target is `plugin/roster.json`, not `plugin/provider/roster.json`;
  `selection.schema.json`'s bump propagates into `plugin/suite/`; and
  `schema_validate.py` must change too, adding a fourth mirror.
- **PP-FR-6's forbidden-token rule has many more than one violation**, so
  Phase C as scoped cannot go green.
**Author (agent):** requirements-agent, consolidated by the orchestrating session
**Date:** 2026-08-11
**Repository:** `/home/deagy/sdk/cadre`
**Classification:** internal
**Decomposes:** `INTENT-CADRE-PORTABLE-PLATFORM` (`product-intent.md`, Revision 3)

---

## 0. Grounding

Every file:line below was read in the tree on 2026-08-11, not inferred from the
intent record or from `docs/proposals/governance-as-product-2026-08.md`, and
re-read at Revisions 3 and 4 — which is how the corrections in those revisions'
notes were found.

**The method note is worth keeping, and it has changed shape three times.**
Revision 3 recorded that a first pass which reads widely still miscounts (three
constants for six, four tests for five, ~60 corpus cases for 175) and concluded
*"citation density is not verification; re-reading is."* Revision 4 falsified
the second half: it re-read and still got four things wrong, two of them *more*
wrong than what they replaced. It concluded that what mattered was tracing one
hop — `:18` derives from `:17`; a staged record is not a chunk; `plugin/suite/`
copies `src/`.

**Revision 5 falsified that too, and this time the correction is not another
reading discipline.** Two of its findings were not reachable by reading at all.
That `--provider` already works, and that `code-reviewer` is in every plan's
`support` list today, were each found by *running the command and looking at the
output* — one line of shell apiece, against requirements that four revisions had
reasoned about confidently from source. The pattern across all three reviews is
not that the reader read too little. It is that a document describing a running
system was written entirely by reading it. **Where a claim is executable, execute
it**: the acceptance criteria in this baseline are now marked with whether they
pass against unmodified code, because three of them did and nobody had checked.

Four corrections from Revisions 1–2 are load-bearing and are recorded here
rather than silently applied.

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
| ~~§13 OD-8~~ | **Withdrawn at Revision 4** — the premise was a misreading of `_select_workflow()`. See PP-FR-6. |
| §13 OD-9 (open, blocking G2) | PP-FR-6 (category A), PP-NFR-1 (second detector) |

## 2. Functional requirements

**PP-FR-1 — The roster root is resolved, not computed.**
A new `roster.root` FieldSpec in `roster/shared/src/settings.py`, taking
`env_var` and `kind` from the shape of `knowledge_store.home` (`:673-680`) and
`context_store.home` (`:690-697`) — `env_var="CADRE_ROSTER_ROOT"`,
`kind="path"`, `required=False` — but taking its **default from
`agentic_sdlc.bin_path` (`:665-672`)**: `default_computed=<today's
checkout-relative value>`, not `default_static=None`. Those three siblings can
all default to `None` because each has a downstream fallback that means "not
configured"; this one does not. `default_static=None` yields no default at all
and pushes the checkout-relative computation back out to every call site, which
is precisely the duplication this requirement exists to delete. Plus a
`--roster` flag on `cadre select`.

**Six computed constants are in scope, not three, and two of them must
deliberately *not* follow the roster root.**

| Constant | Site | Disposition |
| --- | --- | --- |
| `ROSTER_ROOT` | `select_agents.py:17` | **the one constant the resolver drives** |
| `ORCHESTRATION_ROOT` | `select_agents.py:16` | **retired as a roster-path source** — see below |
| `REPOSITORY_ROOT` | `select_agents.py:18` | **rewritten** to re-derive from `Path(__file__)` |
| `_SHARED_SRC_DIR` | `select_agents.py:24` | **rewritten** to re-derive from `Path(__file__)` |
| `KNOWLEDGE_STORE_ROOT` | `build_dispatch_plan.py:29` | already platform-anchored; pinned, not moved (PP-FR-5) |
| `ROSTER_ROOT` | `build_dispatch_plan.py:30` | resolver |
| `CATALOG_PATH` | `mcp/dispatch_core.py:56` | resolver — **second entry point**, see below |
| `_ROUTING_CONFIG` | `mcp/dispatch_server.py:63` | resolver — **second entry point**, see below |

**`ORCHESTRATION_ROOT` must not simply follow the resolver, and Revision 4's
table said it should.** `select_agents.py:204` is
`routing_path = ORCHESTRATION_ROOT / "routing.yaml"`. Routing `:16` through
`roster.root` would force every foreign roster to reproduce Cadre's internal
`<root>/orchestration/routing.yaml` layout — while PP-FR-2 exists precisely so
that `roster.json` declares `catalog`, `routing`, `role_root`, and
`shared_policy_root` as explicit manifest paths. The two requirements
contradicted each other. **Catalog and routing paths come from the manifest;**
`ORCHESTRATION_ROOT` keeps only its platform uses (schemas, orchestration
policy) and is `Path(__file__)`-derived like the other two exceptions. The rows
were also not independent — `ROSTER_ROOT = ORCHESTRATION_ROOT.parent` (`:17`) —
so listing both as "resolver" hid which one the resolver actually drives. That
is the same failure to trace one hop §0 says separates this baseline's surviving
claims from its retracted ones, committed in the paragraph that says so.

**`cadre mcp-dispatch-server` is a second selection entry point, and Revisions
1–4 did not know it existed.** `mcp/dispatch_core.py:56`
(`CATALOG_PATH = REPOSITORY_ROOT / "roster" / "catalog.yaml"`) and
`mcp/dispatch_server.py:63`
(`load_routing(core.REPOSITORY_ROOT / "roster" / "orchestration" / "routing.yaml")`)
resolve checkout-relative and independently of `select_agents.py`. It is a
shipped dispatch surface (`bin/subcommands.tsv`). Left alone, `--roster
<fixture>` redirects `cadre select` while the MCP server keeps dispatching
Cadre's roster — two surfaces disagreeing about which roles exist, with no
error. Both sites route through the resolver, and both are added to PP-FR-6's
platform-module list so the guard covers them.

**"Rewritten," not "left alone" — this is the single most dangerous line in the
baseline and Revision 3 got it backwards.** Both constants are *derived from*
`ROSTER_ROOT` today:

```python
REPOSITORY_ROOT = ROSTER_ROOT.parent                    # :18
_SHARED_SRC_DIR = ROSTER_ROOT / "shared" / "src"        # :24
```

Once `:17` resolves through `roster.root`, an implementer who touches neither
line has *silently redirected both* — which is precisely the outcome the two
paragraphs below exist to prevent. Their current text is not the desired end
state; it is the defect waiting for `:17` to change under it. Each must be
re-derived independently from `Path(__file__)`.

Use sites that consume the resolved value: `select_agents.py:203` (catalog),
`:204` (routing), `build_dispatch_plan.py:501` (knowledge CLI), `:604`
(context-pack definitions), plus role-definition reads.

**`_SHARED_SRC_DIR` (`select_agents.py:24`) is the exception that matters.** It
is the `sys.path` bootstrap through which `settings`, `routing_overlay`,
`text_embedding`, and `content_protection` are imported. §4 declares those
modules **platform-owned**, so pointing this constant at a resolved roster root
would import the platform's own settings resolver, overlay validator, and
embedder *out of the foreign roster* — the exact inversion this work exists to
prevent, and under OD-2's project-local scope an arbitrary-code-execution path
for anyone who can commit an `.agents/cadre.yaml`. It is also circular:
`settings.py` **is** the resolver and lives under this directory, so it cannot
be imported in order to compute its own location. It must be re-derived from
`Path(__file__)` at the point `:17` changes, and PP-FR-6's guard must assert
that it is.

**`REPOSITORY_ROOT` (`select_agents.py:18`) stays platform-anchored for a
different reason.** It is the default working tree for change discovery
(`discover_changed_files`, `:120`) and knowledge-source resolution (`:202`). It
answers *"which tree is being changed,"* not *"which roster describes the
roles."* A roster redirect that silently moved the diff would make a plan
describe work that is not the work in front of the user. Same treatment:
re-derived from `Path(__file__)`, not from `ROSTER_ROOT`.

*Acceptance for both exceptions:* with `--roster <fixture>` in force, `settings`
still imports from the platform checkout and `discover_changed_files` still
resolves against the platform checkout. **Non-vacuity (PP-NFR-4): confirm both
assertions FAIL against a build that leaves `:18` and `:24` in their present
`ROSTER_ROOT`-derived form.** That failing run is the point — it is what
separates a guard from a comment, and in this case it is also the difference
between shipping the feature and shipping the vulnerability. *Verifier:*
`test_roster_package.py`.

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
roster names it; a plan against the default carries **no roster-identity
field**.

"Unchanged" is scoped to that field alone, and the scoping is not pedantry.
Revision 2 wrote "a plan against the default is unchanged" while PP-NFR-5 states
that PP-NFR-3b's 6 → 7 bump changes **every** plan's `schema_version` and
`dispatch_fingerprint`, default roster included. Read against the whole plan the
two acceptance criteria could not both pass, and a reader could have satisfied
either one by breaking the other. *Verifier:* `test_roster_package.py`.

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
`selection.schema.json`, validated by `roster/orchestration/src/schema_validate.py`
and its pre-commit hook.

**That last clause said "no new validation machinery" until Revision 5, and it
was wrong.** `schema_validate.py:329-332` hardwires exactly two instance/schema
pairs — `--catalog`/`--routing` and their schemas, defaulting to
`DEFAULT_CATALOG`/`DEFAULT_ROUTING` — and `.pre-commit-config.yaml:28` invokes
it bare. Validating a third document needs a new argument pair and a new
validation call. Small, but it makes `schema_validate.py` another
`roster/orchestration/src/` file mirrored into `plugin/suite/`, which is why it
appears in PP-NFR-1's expected diff.

`provider/` gains a `roster.json` describing what it already contains: 159
`AGENT.md` files under `provider/roles/`, `agent-catalog.json`, `profiles/`,
`extensions/`. Verified count: 159 files, matching `roster/catalog.yaml`'s 159
`definition:` entries. Only `catalog.yaml` and `routing.yaml` need to join it.

**Placing it there collides with PP-NFR-1, and the collision is resolved here
rather than discovered mid-implementation.** `generate_global_plugin.py:101`
copies `provider/` into the distribution through a closed allowlist:

```python
PROVIDER_BUNDLE = ("provider.json", "agent-catalog.json", "profiles", "extensions", "codex-agents")
```

`roster.json` is not a member, so the packaged plugin would ship a `provider/`
bundle that is **not a valid roster package** — every role present, and the one
file that declares them left behind. Adding it to the allowlist writes a new
file under `plugin/`, which PP-NFR-1's fourth acceptance bullet forbids as
Revision 2 worded it. The two requirements could not both be satisfied.

**Resolution: `PROVIDER_BUNDLE` gains `"roster.json"`, and PP-NFR-1's bullet is
narrowed to match.** The constraint PP-NFR-1 encodes is *"Cadre's roles,
routing, and CLI surface do not change,"* not *"no file may be added to the
distribution."* Shipping a distribution whose roster manifest is missing is a
worse failure than a one-file addition, and the addition is itself covered by
`--check` drift detection rather than escaping it. See PP-NFR-1.

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
removed fails with a message naming `catalog.yaml` (C4); **(d) a fixture task
that *does* match a fixture route produces a `workflow` other than
`unclassified`.** *Verifier:* `test_roster_package.py`. **(c) is the non-vacuity
case and is not optional** — see PP-NFR-4.

**(d) needs no code change, and Revision 3 was wrong to claim it did.**
`_select_workflow()`'s final stage (`build_dispatch_plan.py:255-265`) does not
branch on Cadre route ids: it collects each matched route's own declared
`workflow_shape` from `routing.yaml` — a four-value roster-supplied field
(`routing.schema.json:193-201`) — and maps it to `new-service`,
`infrastructure-change`, or `pipeline-change`. A fixture roster whose routes
declare `workflow_shape` classifies correctly today. So (d) is a **regression
pin**, not a falsification: it costs one assertion and it locks in a property
the fixture would otherwise be free to lose. It is stated as such rather than
dropped, because "already works" is exactly the claim that stops being true
without a test.

The *earlier* precedence stages (`:151-196`) do branch on Cadre route ids
(`rollback`, `production`, `support`, …), so a foreign roster cannot reach
`rollback` or `production-release`. That is a real limitation, and it is **not**
a PP-FR-6 violation — those are route ids, which the forbidden-token list does
not cover, and `routing.schema.json:193-201` documents the split deliberately.
Recorded as a known limitation, not scoped as work.

**PP-FR-4 — `cadre sdlc` lets the caller *suppress* Cadre's provider, not merely
add one. Rewritten at Revision 5: Revisions 1–4 solved a problem that did not
exist and left the real one untouched.**

The premise was that `cadre sdlc` "always injects *this* repository's bundle,
with no override." **Half of that is false.** `bin/cadre.py:125-127` runs
`[sdlc_bin, "--provider", str(provider), *rest]` — `*rest` is the user's own
argv — and the kernel's `--provider` is `action="append"`
(`kernel/agentic_sdlc/__init__.py:2918-2923`). A user-supplied `--provider`
therefore already reaches the kernel and is already loaded and validated today,
with no change to anything.

**Run it and the real defect appears immediately:**

```console
$ ./bin/cadre sdlc --provider providers/agentic-sdlc-defaults/provider.json provider list
{ "error": "provider agentic-sdlc-defaults duplicates profile ids: ['generic']" }
```

The foreign manifest loaded. It was rejected because **Cadre's bundle is always
loaded alongside it** and the two collide on profile ids. So the requirement is
not a flag — the flag exists. It is: **a caller must be able to run the kernel
with their provider *instead of* Cadre's.** That is a wrapper change (suppress
the injected default when the caller supplies `--provider`, or an explicit
`--no-default-provider`), still with no kernel edit.

This also disposes of the old acceptance criterion, which was vacuous: *"reports
that provider loaded"* passes on unmodified code for any provider that happens
not to collide. It tested the half that already worked.

*Acceptance:* (a) `cadre sdlc --provider providers/agentic-sdlc-defaults/provider.json provider list`
lists **only** that provider's profiles and does not error — the exact command
that fails today; (b) with no flag, the argument vector passed to the kernel is
byte-identical to today's. *Verifier:* new case in the `bin/` dispatch tests.
**(a) fails against unmodified code — confirmed by running it, and that failing
run is this requirement's PP-NFR-4 evidence.**

**PP-FR-5 — The knowledge store resolves without a roster.**
`build_dispatch_plan.py:29` computes `KNOWLEDGE_STORE_ROOT = Path(__file__).resolve().parents[2] / "knowledge-store"`
and `:501` emits `str(KNOWLEDGE_STORE_ROOT / "src" / "cli.py")` into every
dispatch plan's `knowledge_context.requests[].invocation.args`. That absolute
path is consumed cross-language — `cline-plugins/cline-agents/index.ts:247-259`
executes it directly — so this is a published contract, not an internal detail.

**This requirement is a regression pin, not a change, and Revision 3 specified a
no-op without noticing.** `KNOWLEDGE_STORE_ROOT` is *already*
`Path(__file__)`-derived and already never consults `roster.root` — there is no
such setting yet. Revision 3 required it to resolve "against the platform
checkout — the same `Path(__file__)`-derived anchor," which is the value it
holds today, and then wrote acceptance criteria that pass against unmodified
code. By PP-NFR-4's own bar, a test that cannot fail.

**What is actually at stake is a change PP-FR-1 could make and must not.** The
`parents[2]` walk lands under `roster/`, so the store *looks* roster-owned; §4
and PP-FR-6 declare it platform. The live risk is that an implementer routing
`ROSTER_ROOT` (`:30`) through the resolver takes `KNOWLEDGE_STORE_ROOT` (`:29`)
with it — same shape, adjacent line, and it would look like tidying. If they do,
this requirement's acceptance breaks: point `CADRE_ROSTER_ROOT` at a roster-less
directory and the emitted `cli.py` path stops existing, in a plan whose consumer
is a TypeScript file in another package.

So PP-FR-5 is restated as: **`KNOWLEDGE_STORE_ROOT` stays platform-anchored, and
a test says so.** The emitted shape is unchanged and the computation does not
move. `knowledge_store.home` continues to govern where the *data* lives,
unchanged and still `SCOPE_GLOBAL_ONLY` (`settings.py:673-680`). Intent §4
outcome 3 ("resolved rather than computed from `parents[2]`") overstates the
work: the store already runs with no roster present, and this baseline pins that
rather than delivering it.

Per §0.4 this moves no security boundary.

*Acceptance:* with `CADRE_ROSTER_ROOT` pointing at a directory containing no
roster, (a) `cadre knowledge context …` succeeds, and (b) a generated plan's
`invocation.args` carries an absolute path to a `cli.py` that **exists on
disk** — by stat, not by string shape. **Both pass today, and that is stated
plainly rather than dressed up as a deliverable.** The PP-NFR-4 evidence here is
therefore not a planted defect in the store: it is confirming these assertions
**fail** on a branch where `:29` was routed through the resolver alongside
`:30`. *Verifier:* `roster/knowledge-store/test/test_scope_enforcement.py`
(which already asserts the dispatch plan always supplies `--source`, at
`:135-155`) plus `test_selector.py`. The path is spelled out because
`roster/context-store/` has a file of the same name.

**PP-FR-6 — The mirror boundary guard.**
`test_kernel_boundary.py` guards roster → kernel (five tests, at `:76`, `:97`,
`:129`, `:144`, and `:158`). Nothing
guards platform → roster, because until PP-FR-1 nothing could violate it. Add
`roster/orchestration/test/test_roster_boundary.py`, modelled on
`test_context_boundary.py:157-215`, which already enforces a two-way
don't-name-each-other rule between the knowledge and context stores — including
the string-literal check, not just imports.

Platform modules, for this purpose: `select_agents.py`, `build_dispatch_plan.py`,
`risk_classifier.py`, `routing.py`, `routing_overlay.py`,
**`mcp/dispatch_core.py`, `mcp/dispatch_server.py`**, and
`roster/knowledge-store/src/`. The guard must also assert that
`select_agents.py:24`'s `_SHARED_SRC_DIR` and `:18`'s `REPOSITORY_ROOT` do **not**
resolve through `roster.root` (PP-FR-1).

**The forbidden-token rule needs the precision Revisions 1–4 did not give it.**
"A hardcoded Cadre role id, phase name, or `roster/`-relative path" sounds
exact and is not: scanning non-docstring string literals in the modules above
(the `test_context_boundary.py:187-229` method this requirement adopts) returns
violations in three distinct categories, only one of which is a defect.

| Category | Examples | Disposition |
| --- | --- | --- |
| **A. Cadre role ids in resolution logic** | `build_dispatch_plan.py:107` `["code-reviewer"]` | **Forbidden.** Real defect — see OD-9. |
| **B. Roster-package filenames in path resolution** | `select_agents.py:203-204`; `routing.py:154,198,209,212,279`; `routing_overlay.py:97,102`; `mcp/dispatch_core.py:56`; `mcp/dispatch_server.py:63` | **Forbidden**, and PP-FR-2 already prescribes the fix: these paths come from `roster.json`, not from string literals. This is the bulk of the work. |
| **C. Paths in user-facing message text** | `knowledge-store/src/{staged_records.py:141,147, finding_record.py:139, config.py:79,136,183}` | **Permitted, explicitly.** A diagnostic naming the file a user must edit is not a resolution path. The guard must exempt them by *rule* — literals reachable only from error/help strings — not by an ad-hoc file allowlist, which is how a guard stops meaning anything. |

**Consequence: Phase C as Revision 4 scoped it (one fix) cannot go green.**
Category B is the real body of PP-FR-6 and it is not small; it is also the work
that makes PP-FR-2's manifest load-bearing rather than decorative. Revision 3
said "two violations", Revision 4 said "one"; the answer is one of category A,
roughly nine of category B, and a rule that has to be written carefully enough
to let category C through.

**The category-A violation is not small, and Revision 4 called it small without
running anything.** `build_dispatch_plan.py:107` defaults a gate's reviewers to
`["code-reviewer"]` whenever a lifecycle gate contract declares no
`review_agents`. **No gate in `kernel/contracts/lifecycle-gates.json` declares
one** — the string does not appear in the file — so the default fires for every
configured gate on every lifecycle-aware plan, and `:673` appends the result to
`support`. Observed, not inferred:

```console
$ ./bin/cadre select --task "Update the OpenTofu module for the VPC" ...
support: ['product-intent-agent', 'requirements-agent', 'code-reviewer']
```

Removing the hardcode drops `code-reviewer` from that list. **That is a change
to default Cadre selection — the precise thing PP-NFR-1 exists to forbid** — and
it is invisible to the detector PP-NFR-1 names: `test_selection_golden_corpus.py:135`
patches `try_lifecycle_contract` to return `None` so the corpus is deterministic
across hosts, which means `_gate_agents()` never runs there and 0 of 175 cases
carry `code-reviewer` in `expected.support`. **The corpus cannot see this change
at all.** How to resolve it is **OD-9**.

**Revision 3 claimed a second violation in `_select_workflow()`. There is
none — it was a misreading, and it is withdrawn along with OD-8.** The function
does not classify a foreign roster to `"unclassified"`: its final stage
(`:255-265`) reads each matched route's declared `workflow_shape`, which the
roster supplies. Revision 3 read the earlier precedence branches (`:151-196`),
which *do* name Cadre route ids, and stopped there — never reaching the stage
that actually decides the common case. It then registered a blocking open
decision (OD-8) proposing to open a published schema enum to fix a problem that
does not exist. **A misreading that invents work is not cheaper than one that
misses it**, and this one would have spent a System Architect decision and a
contract change on nothing.

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
- `fixtures/selection_golden_corpus.json` is **not edited** — **175 cases**,
  hand-maintained, no generator script (the loop is run-test, read-diff,
  hand-edit). Editing it would mean default selection behaviour changed.

  **The "~60 blocks" figure this baseline carried until Revision 3 was not this
  file's size.** `cadre-proposal-01-route-match-reasons-2026-08-08/requirements.md:150`
  counts the blocks *that* change rewrote, not the blocks the file contains.
  Carrying it forward understated the cost of a mistaken edit roughly threefold,
  in the one place this baseline sets out to size that risk.

  **This detector survives PP-NFR-3b's schema bump, and that was verified rather
  than assumed.** The corpus pins selection *outcomes* only — each case carries
  `expected.primary` / `.reviewers` / `.support`, and the file contains **zero**
  occurrences of `schema_version` or `dispatch_fingerprint`. So a bump and a new
  emitted field leave it untouched, and it keeps meaning what PP-NFR-1 needs it
  to mean. Had it pinned either, this requirement and PP-NFR-3b would have been
  in direct conflict and one would have had to give.
- `git status --porcelain` shows no change under `provider/roles/`,
  `roster/catalog.yaml`, or `roster/orchestration/routing.yaml`. **Under
  `plugin/`, the diff must match this list exactly:**

  1. **`plugin/roster.json`** — new. Note the path: `generate_global_plugin.py:813,819`
     copies each `PROVIDER_BUNDLE` member to `plugin_root / name`, so the bundle
     lands *flattened at the plugin root* (`plugin/provider.json`,
     `plugin/profiles/`, `plugin/extensions/`). **There is no
     `plugin/provider/` directory**, and Revision 4 named one.
  2. `plugin/suite/roster/orchestration/roster.schema.json` — new. Not optional
     and not avoidable: `generate_global_plugin.py:1426` copies
     `roster/orchestration/` into `suite/` by prefix, which is why
     `routing.schema.json` and `selection.schema.json` are already there.
  3. **`plugin/suite/roster/orchestration/selection.schema.json` — modified**,
     by PP-NFR-3b's 6 → 7 bump, through that same prefix copy. Revision 4 cited
     the copy as the reason the file is already there and then omitted it from
     the list.
  4. `plugin/suite/` mirrors of every platform source file this work edits —
     `select_agents.py`, `build_dispatch_plan.py`, `settings.py`,
     **`schema_validate.py`** (PP-FR-2 needs new arguments there; see below),
     `mcp/dispatch_core.py`, `mcp/dispatch_server.py`, and `bin/`. `suite/` is a
     **copy** of that source; editing the source *is* editing `plugin/`.
  5. Nothing else. In particular: no change to any generated subagent wrapper,
     `skills/`, `agent-catalog.json`, `provider/roles/`, or any plugin manifest.

  **This bullet has been unsatisfiable in every revision to date, including the
  one whose stated purpose was to fix it.** Revision 1: "no change under
  `plugin/`", while scheduling edits to three bundled files. Revision 2: "the
  only change is `provider/roster.json`", same problem. Revision 4: an exact
  four-item list that named a directory which does not exist, omitted a modified
  file it had just finished explaining, and missed a fourth mirror. The gate
  that makes "leave Cadre alone" checkable is the single most-revised and
  most-wrong thing in this baseline — because nobody ran `generate-plugin` and
  looked. Item 5 is where the real constraint lives; items 1–4 are bookkeeping
  and should be **regenerated and read**, not reasoned about.

- **The corpus does not prove what the first bullet claims it proves.**
  "Corpus unedited ⇒ default selection unchanged" holds only for behaviour the
  corpus can observe, and `test_selection_golden_corpus.py:135` patches
  `try_lifecycle_contract` to `None` — so every lifecycle-derived agent,
  including the `code-reviewer` default at `build_dispatch_plan.py:107`, is
  outside its reach. PP-FR-6's category-A fix changes default selection in a way
  all 175 cases pass through unmoved. **PP-NFR-1 therefore needs a second
  detector**: a lifecycle-aware selection assertion, run with the in-tree kernel
  resolvable, pinning the `support` list for at least one gate-bearing task.
  Without it this requirement asserts an invariant it cannot check, which is
  this repository's most frequently rediscovered failure mode occurring inside
  the requirement that cites it.

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
  unreachable** (`:109-110`), matching every other lifecycle-aware path. So a
  kernel-side change to *which authority owns a gate* is caught nowhere, and a
  gate renumber is caught only when a kernel happens to be installed. Directly
  adjacent to this work's subject (roster/kernel separation) but not fixed by it.
  Worth filing.
- **G-2: `test_kernel_boundary.py`'s stray-copy check covers one contract file.**
  `:158-167` asserts `ROSTER_ROOT.rglob("lifecycle-gates.json")` is empty, but
  makes no equivalent assertion for `mutation-gates.json` or
  `run-record.schema.json`, which it requires to exist under `kernel/contracts/`
  in the same test. A roster-side copy of either would drift undetected.
- **G-3: the parked proposal is stale on role count** — 74 at lines 24, 73, and
  126 (Revisions 1–3 listed only two of the three, so correcting from that note
  would have left one behind);
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
- **G-7: durable findings are captured and staged, and then stop.** Rewritten at
  Revision 4; detailed in `product-intent.md` §14. Twelve records sit under
  `roster/knowledge-store/proposed-knowledge/`, two `accepted`, including
  `KS-20260809-a-single-maintainer-repository-cannot-re-16ef457779e1` (G2's
  two-authority obstacle, reasoned out from scratch in §7 below) and
  `KS-20260809-non-vacuity-fault-injection` (the whole of PP-NFR-4). **None is
  retrievable**, because staging is not ingestion and *"confers no
  retrievability"* (`.agents/skills/run-agent-orchestration/SKILL.md:94`;
  `search_store()` scores `chunks` rows, staged records live in
  `staged_records`). So retrieval is not broken and the store is not empty —
  **the pipeline stops one steward action short of usefulness**, and findings a
  previous session wrote down were re-derived here from scratch.

  Revision 3 recorded this gap as a *retrieval-quality* defect, which was wrong
  and would have sent someone tuning a scorer that was working correctly.
  `docs/proposals/durable-knowledge-capture-2026-08.md` asked for the capture
  half and #180 built it; nothing yet drives disposition-to-ingestion. Adjacent
  to this work only in that this work tripped over it. Worth filing.

## 7. Handoff

**To:** the human Product Owner (`@deagy`) **and Engineering Lead** for a **G2**
decision. G1 approved 2026-08-11 (`product-intent.md` §16). G2 requires **both**
authorities per `kernel/contracts/lifecycle-gates.json:5`, whose gates are a
list of objects rather than a map keyed by id —
`{"id": "G2", …, "authority_requirements": ["product_owner", "engineering_lead"]}` — and
this repository has one maintainer, which is a real obstacle rather than a
formality. `docs/migration/monorepo-migration.md` records the parallel case:
`required_approving_review_count` stays `0` because "a required-review setting
with nobody to satisfy it blocks releases without adding a reviewer."

**Blocking before this baseline can be accepted:** **OD-7** — the
`selection.schema.json` 6 → 7 bump forced by OD-2's disposition. It changes a
published, vendored contract, which is not the authoring session's call.

**Also blocking, raised at Revision 5:** **OD-9** — removing the
`["code-reviewer"]` default at `build_dispatch_plan.py:107` **changes every
lifecycle-aware Cadre plan's `support` list**, which PP-NFR-1 forbids and the
golden corpus cannot detect. Three ways out, and the choice is not the
implementer's:

1. **Move the default into roster data** — `routing.yaml` or `roster.json`
   declares Cadre's default gate reviewer, so Cadre's plans are byte-identical
   and a foreign roster supplies its own. Preserves PP-NFR-1 intact; costs a
   schema field.
2. **Accept the change** and re-baseline: Cadre's `support` lists lose
   `code-reviewer`, PP-NFR-1 gets an explicit carve-out, and the change is
   announced in `CHANGELOG.md`.
3. **Leave `:107` alone** and narrow PP-FR-6 to exclude it, accepting one
   documented Cadre role id in platform code.

(1) looks right, and this baseline recommends it, but it is a Product Owner /
Engineering Lead call because (2) alters published dispatch output. **Either
way PP-NFR-1 needs the lifecycle-aware detector**, because the corpus's blind
spot exists regardless of which option is chosen.

**OD-8, raised at Revision 3 as blocking Phase C, is withdrawn at Revision 4.**
It asked a System Architect whether a foreign roster's workflow classification
should come from a mapping in `roster.json` or from opening a published schema
enum. `_select_workflow()` already answers it from roster-declared
`workflow_shape`. Nothing was blocked and there was nothing to decide.

Non-blocking: OD-3, OD-4, OD-6.

**Resolved and folded in at Revision 2:** OD-1 (proceed), OD-2 (project-local,
overlay-style), OD-5 (sibling manifest).

Per `roster/workflows/product-intake.md`, objective conflicts return to G1 rather
than proceeding. Nothing here is approved by its presence in this file.
