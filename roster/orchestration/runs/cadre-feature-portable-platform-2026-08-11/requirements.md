# Requirements Baseline — A roster-neutral platform

**Requirements ID:** `REQ-CADRE-PORTABLE-PLATFORM`
**Revision:** 12
**Status:** **G2 APPROVED** by `@deagy` on 2026-08-11, against Revision 10's
content. G1 approved and re-affirmed the same day. See §8.
**No open decision blocks G2 any more.** All five that did were closed or
withdrawn by the Product Owner on 2026-08-11 (`product-intent.md` §17). **OD-2
was reversed**: `roster.root` is `SCOPE_GLOBAL_ONLY`, which retracts PP-FR-1b and
PP-NFR-3b and withdraws OD-7 and OD-10 outright. **OD-12 is also closed** — G1
was re-affirmed against the current intent record on 2026-08-11
(`product-intent.md` §18). Only the G2 decision itself remains.
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

**Revision 6 follows an eight-role independent review, and its corrections run
in a different direction from every previous revision's.** Revisions 2–5 each
found that this baseline had *under-read* the tree. Revision 6 finds the
opposite in two places: PP-FR-6's category table forbids work that must not be
done, and inflates the phase's size by the same mistake.

- **PP-FR-6's category B is wrong in both directions at once, and the count is
  six, not "roughly nine."** `routing.py:154,198,209,212,279` — listed as
  forbidden — are all `raise ValueError(...)` diagnostics, i.e. the category
  this baseline explicitly permits. Meanwhile `config.py:79,136,183`, offered as
  the *evidence* for that permission, do not demonstrate it: two are path
  construction and the third contains no path at all.
- **OD-9 is not an output-churn decision and its third option is
  non-viable.** `build_dispatch_plan.py:547-551` raises on any selected agent
  absent from the catalog, so the `["code-reviewer"]` default makes a foreign
  roster with lifecycle gates emit **no plan at all**.
- **PP-FR-3's acceptance cases cannot observe that**, because none requires the
  fixture to declare `quality_gates`. The fixture reproduces the golden corpus's
  blind spot.
- **PP-FR-1's project-tier scope has an unpriced implementation cost**:
  `mcp/dispatch_server.py` resolves routing at import time and disables
  project-tier resolution deliberately.
- **PP-NFR-1's `plugin/` diff is wrong for the sixth time** — under OD-9 option 1
  it gains a fifth mirrored file.
- **PP-FR-2 has no compatibility window** (now OD-11), and points at the wrong
  containment helper.

Every executable claim in Revisions 1–5 was re-run and **all held**. The
corrections above are all classification, tracing, or completeness — none is a
claim that was checkable by running something and was not run.

**Author (agent):** requirements-agent, consolidated by the orchestrating session
**Date:** 2026-08-11
**Repository:** `/home/deagy/sdk/cadre`
**Classification:** internal
**Decomposes:** `INTENT-CADRE-PORTABLE-PLATFORM` (`product-intent.md`, **Revision 5**)
**Revision-pin note:** Revisions 3–5 of this baseline all carried "Revision 3"
on that line while the intent record moved to 4. A stale pin in the trailer of a
document whose §0 is about stale pins — the same defect `implementation-plan.md`
§0 records against itself one revision earlier.

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

**Revision 6 tested that discipline directly, and it passed — which is why the
remaining errors are interesting.** An independent reviewer re-ran every
executable claim in this baseline: the provider collision, the `support` list,
the absence of `review_agents` from every gate contract, the corpus figures, all
five suites, the drift check, determinism. **Every one held.** The discipline
§0 arrived at works.

The errors Revision 6 corrects were unreachable by it. Three classes:

1. **Classification.** PP-FR-6's category table sorts string literals into
   forbidden and permitted. Running a command does not test a sort. Both example
   sets turned out wrong, *in opposite directions within one table* — which is
   the tell, because a systematic bias would push both the same way. What
   produced it is that categorisation is a judgment call written in the
   grammar of an observation.
2. **Tracing.** OD-9's real consequence is one function call past a line this
   baseline cited correctly five times. `cadre select` against Cadre's own
   roster succeeds, so no available command exposes it; the failure exists only
   against a roster that does not exist yet.
3. **Completeness.** A guard's module list, a manifest's key set, and a
   non-vacuity checklist are all propositions about what is *absent*. Nothing
   you can run enumerates what you failed to include.

So the method note, restated once more and hopefully last: **execution
falsifies claims, and this baseline's remaining failures are not claims.** They
are classifications, one-hop consequences, and omissions. The countermeasures
differ per class — derive a categorisation from the rule that will enforce it
rather than hand-sorting candidates; ask what a cited line's callee does; and
derive membership lists from directory structure rather than enumerating them.
None of the three is "read more carefully," which is what Revisions 3 and 4 both
concluded and neither achieved.

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

**"Both sites route through the resolver" is not a small edit under project
scope, and Revision 5 wrote it as though it were.** Two properties of that
module make a project-tier setting structurally hard to deliver there, and both
are deliberate:

- `mcp/dispatch_server.py:48` calls `settings.disable_project_tier_cwd_fallback()`
  **on purpose** — the server is long-lived and project-agnostic, so its cwd is
  wherever the host CLI was launched and is *not* the project any given tool
  call concerns.
- `:63` loads `_ROUTING_CONFIG` at **import time**, before any call knows which
  project it is about.

So a project-tier `roster.root` cannot reach this surface without converting
import-time resolution into per-call resolution with an explicit `start=` — real
scope appearing in no phase estimate. Under **global-only** scope the module
needs no restructuring at all: it resolves once at import, exactly as today.
This is one of three places where OD-2's disposition changes the size of the
work rather than only its risk.

**And the compensating control OD-2 was granted in exchange for does not exist
here.** PP-FR-1b places roster identity *in the dispatch plan*; this surface
emits no dispatch plan — it dispatches child agents directly
(`dispatch_core.py:511-528`, `:985-1005`). A redirect is silent. Registered as
**OD-10** (`product-intent.md` §13) rather than settled here, because it asks
whether a prior deliberate design decision should be reversed.

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

**Scope: `SCOPE_GLOBAL_ONLY`** (OD-2, **reversed** 2026-08-11 —
`product-intent.md` §17). `roster.root` behaves exactly like its three sibling
path/executable settings: `agentic_sdlc.bin_path` (`settings.py:665-672`),
`knowledge_store.home` (`:673-680`), and `context_store.home` (`:690-697`). Env
var or user-global config only; a project-local `.agents/cadre.yaml` cannot set
it. Per-invocation redirection is `cadre select --roster <path>`.

**This reverses Revisions 2–6, which specified project tier.** The comment at
`:681-689` states the objection those revisions were answering — a project-local
file *"arrives with `git clone` and is editable by anyone who can open a pull
request"* — and choosing a roster is strictly more powerful than choosing a
database path, because it selects the role prose an agent is handed. Revisions
2–6 answered it with visibility (PP-FR-1b). The Product Owner instead removed
the exposure, on the ground that global-only → project-local is an additive
change later while the reverse takes away a capability people have built on.

*Acceptance:* `cadre select --roster <path>` loads catalog, routing, and role
definitions from `<path>`; `CADRE_ROSTER_ROOT` or user-global config sets the
default; with neither set, behaviour is byte-identical to today (pinned by the
golden corpus, `fixtures/selection_golden_corpus.json`, which must not be
edited — see PP-NFR-1). *Verifier:* `test_selector.py`, plus a scope test on the
model of `test_kernel_boundary.py:129-140` asserting the field **is**
`SCOPE_GLOBAL_ONLY` — the same assertion as that precedent rather than its
inverse, which is what Revisions 2–6 asked for.

**~~PP-FR-1b — the resolved roster is legible in the plan.~~ RETRACTED at
Revision 7.** It required the dispatch plan to carry the resolved roster's `id`
and manifest digest whenever the roster was not the default. Its entire purpose
was to make a *silent* redirect visible, and OD-2's reversal removes the silence:
`--roster <path>` is explicit in the invocation, in shell history, and in CI
logs, and a global config file is the operator's own.

Retained struck through rather than deleted, so a reader of Revisions 2–6 finds
out what happened to it. Two consequences follow and are recorded at their own
requirements: **PP-NFR-3b is retracted with it** (no new emitted field, so
nothing forces a schema bump), and the kernel `fingerprint()` port this
requirement called for is no longer needed at all.

**PP-FR-2 — A roster package is declared by a sibling `roster.json`.**
Resolved at OD-5 (2026-08-11). Chosen because it requires **zero** change to
`kernel/` and `engine/`; see §4 for the constraint this places on
implementation.
The kernel never reads it. It declares, at minimum: `schema_version`, `id`,
`version`, `catalog`, `routing`, `role_root` (path under which `definition`
entries resolve), and `shared_policy_root`.

**The two data files are in different formats, and the manifest must say so
rather than leaving a roster author to infer it from an extension.** Added at
Revision 10, as G-12's disposition required: **`catalog` is YAML** and
**`routing` is JSON**. That asymmetry is not incidental — it is the entire
reason G-12 existed. One roster package carried both behind `.yaml` extensions,
`schema_validate.py` quietly kept a loader for each (`:71`, `:76`), and the
first foreign roster author to write actual YAML got
`JSONDecodeError: Expecting value: line 1 column 1 (char 0)`.

Renaming `routing.yaml` to `routing.json` makes the extensions honest but does
not remove the asymmetry, so the requirement states it. `roster.schema.json`'s
field descriptions carry it too, which is where an author will actually look. Every path resolves relative to the manifest directory and
must reject escapes, porting the containment logic the kernel already applies in
`provider_resource()` (`kernel/agentic_sdlc/__init__.py:159-169`).
A `roster.schema.json` alongside `routing.schema.json` and
`selection.schema.json`, validated by `roster/orchestration/src/schema_validate.py`
and its pre-commit hook.

**`glob_containment.py` is not an alternative, and Revisions 1–5 offered it as
one.** It answers glob-language subset containment — whether one glob pattern's
matches are a subset of another's, for `exclude_paths` shadowing. That is a
different question from filesystem path escape, and reaching for it here would
produce a containment check that compiles, passes its own tests, and does not
contain anything. Port `provider_resource()`'s two lines:

```python
candidate = (root / value).resolve()
if not candidate.is_relative_to(root):
    raise ValueError(f"roster {field} escapes its manifest directory")
```

Vectors this covers, and the one condition it depends on:

| Vector | Covered | Note |
| --- | --- | --- |
| `..` after resolution | yes | `.resolve()` collapses before the check |
| absolute-path value | yes, **incidentally** | `Path("/a") / "/etc/passwd"` is `Path("/etc/passwd")` — caught by `is_relative_to`, but via a pathlib join quirk rather than a visible `is_absolute()` guard, so it needs its own test or a later refactor can silently remove it |
| symlink escape | yes, **conditionally** | only if `root` is itself `.resolve()`d before the call. The kernel's caller does this; the roster loader must too, or it will both under-reject and raise false positives |
| case/Unicode normalisation | no | same limitation as the kernel's; out of scope |
| TOCTOU | no | manifest is read once from a local checkout |

Put it in **one** module under `roster/orchestration/src/`, imported by both the
selector and the MCP loader. Two copies of a containment check is the failure
mode PP-NFR-2 exists to prevent, applied to the one function where drift is a
security bug rather than a maintenance cost.

**PP-FR-2 has no compatibility window, and `provider.json` — the thing it is
modelled on — does.** `provider.json` declares `kernel_compatibility`, which
`load_provider()` checks against the consuming kernel's version with an
actionable error (`kernel/agentic_sdlc/__init__.py:208-220`). The key set above
has no equivalent, so a `roster.json` authored against one selector's semantics
and loaded by another fails in whatever way its differences happen to produce,
rather than by name.

That is exactly the failure PP-NFR-3b argues the schema bump exists to prevent —
*"a silent failure naming the wrong cause"* — reproduced one layer down, in the
manifest the bump is being made for. `schema_version` does not cover it: it
versions the document, not the platform behaviour the document depends on.

And it is not optional to get right first time. `product-intent.md` §9
dispositions the parked proposal's condition 1 — *"`provider.json` becomes a real
third-party contract"* — as **"No, deliberately not."** `roster.json` must then
satisfy that condition itself, inheriting none of `provider.json`'s answers. A
schema shipped without a compatibility window cannot gain one compatibly later.

**OD-11 — RESOLVED at Revision 7: no window. `schema_version` only.**
(`product-intent.md` §17.) The proposal below is **not adopted**:

```json
"platform_compatibility": {"minimum": "<semver>", "maximum_exclusive": "<semver>"}
```

The cost is accepted rather than overlooked, and is restated here so nobody
reopens it as an oversight: a `roster.json` authored against different selector
semantics will fail however its differences happen to present rather than by
name, and a window cannot be added later without a breaking change to a shipped
schema.

**One mitigation is adopted with the decision, and it is a requirement, not a
suggestion: the loader must REJECT an unrecognised `schema_version` rather than
ignoring it.** `schema_version` versions the document rather than the platform
behaviour it depends on, so this does not recover what a window would have
given. What it does recover is the most common case — a manifest written for a
different generation of the format — turning it from silent misbehaviour into an
error naming the manifest. *Acceptance:* a `roster.json` carrying an unknown
`schema_version` is rejected by name; a test asserts it, and asserts the failure
rather than only the success.

**A second, smaller gap: nothing binds `roster.json` to its sibling
`provider.json`.** They describe one bundle with two ids and two versions, and
`test_repository_health.py:890-891` already pins `catalog.yaml` ↔
`provider/agent-catalog.json` agreement — so this would be the only
role-describing document in the tree with no binding test. Suggested: an
**optional** `paired_provider: {"id", "version"}` checked only when a
`provider.json` is actually present. Optional rather than required, because
PP-FR-3's fixture roster has no provider bundle and a required key would break
it. Whether `provider/roster.json` itself must declare it is a second call,
folded into OD-11.

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

**(e), added at Revision 6, and without it (a)–(d) can all pass while the seam
is broken.** The fixture must declare `quality_gates` on at least one route, and
that case must run **with lifecycle contracts resolvable**. As scoped through
five revisions, none of (a)–(d) requires either — so `_gate_agents()` never
executes against the fixture, and the one known blocker for a foreign roster
(category A: `build_dispatch_plan.py:107` → `:547-551` → `ValueError`) is
invisible to the test written to falsify exactly that.

This is worth stating as plainly as the baseline states it about the corpus.
PP-NFR-1 correctly identifies that `test_selection_golden_corpus.py:135` patches
`try_lifecycle_contract` to `None`, making 175 cases structurally blind to
lifecycle-derived agents. **PP-FR-3 then specifies a new fixture with the same
blind spot**, in the section whose stated purpose is *"if a foreign roster
cannot produce a plan, the seam is theoretical."* Finding a failure mode, naming
it precisely, and rebuilding it two requirements later is a distinct error from
the ones §0 catalogues — not under-reading, but failing to apply a diagnosis to
the next artifact.

**(f)** A manifest whose declared path escapes its directory is rejected naming
the offending field. PP-FR-2 states this acceptance and names
`test_roster_package.py` as its verifier, but it appears in none of (a)–(e) —
so the security-relevant case is specified in one requirement and scheduled in
neither. Cover symlink, `..`, and absolute-path values explicitly rather than
"an escape" generically.

**(g)** A malformed `roster.json` — missing a required key, or naming a
`catalog`/`routing` path that does not exist — fails by field name, the same way
(c) fails by file name. C4 is "fail closed naming the file"; a manifest is now
the thing most likely to be wrong, and only its total absence is currently
tested.

**Also worth pinning, though it needs no new case:** the fixture's role
definitions must be shown to actually load — frontmatter parsed, `role_root`
honoured — not merely that selection resolves ids. A broken `role_root` passes
(a)–(d) unchanged if nothing ever dereferences a `definition` path.

**(d) needs no code change, and Revision 3 was wrong to claim it did.**
`_select_workflow()`'s final stage (`build_dispatch_plan.py:254-265`) does not
branch on Cadre route ids: it collects each matched route's own declared
`workflow_shape` from `routing.yaml` — a four-value roster-supplied field
(`routing.schema.json:193-201`) — and maps it to `new-service`,
`infrastructure-change`, or `pipeline-change`. A fixture roster whose routes
declare `workflow_shape` classifies correctly today. So (d) is a **regression
pin**, not a falsification: it costs one assertion and it locks in a property
the fixture would otherwise be free to lose. It is stated as such rather than
dropped, because "already works" is exactly the claim that stops being true
without a test.

The *earlier* precedence stages (`:150-196`) do branch on Cadre route ids
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
| **A. Cadre role ids in resolution logic** | `build_dispatch_plan.py:107` `["code-reviewer"]` | **Forbidden.** Real defect, and a functional prerequisite rather than hygiene. **Fix resolved at OD-9 (Revision 7): the default moves to a `default_gate_review_agents` key in `routing.yaml`.** See below. |
| **B. Roster-package filenames in path resolution** | `select_agents.py:203`, `:204`; `routing_overlay.py:97`, `:102`; `mcp/dispatch_core.py:56`; `mcp/dispatch_server.py:63` — **six sites, enumerated exhaustively** | **Forbidden**, and PP-FR-2 already prescribes the fix: these paths come from `roster.json`, not from string literals. |
| **C. Paths in user-facing message text** | `routing.py:154,198,209,212,279`; `knowledge-store/src/{staged_records.py:141,147, finding_record.py:139}` | **Permitted, explicitly.** A diagnostic naming the file a user must edit is not a resolution path. The guard must exempt them by *rule* — see the rule sketch below — not by an ad-hoc file allowlist, which is how a guard stops meaning anything. |

**Corrected at Revision 6, and the correction runs in both directions inside
this one table.** Revisions 3–5 sized category B at "two", "one", and "roughly
nine" respectively. The answer is **six**, and the drift came from
misclassification rather than miscounting:

- **`routing.py:154,198,209,212,279` were listed as forbidden and are
  permitted.** All five are string literals inside `raise ValueError(...)`:
  `"routing.yaml must contain version 1 routes and risk_rules"`,
  `"routing.yaml context_packs must be a list"`, and so on. None constructs a
  `Path`, opens a file, or resolves anything. By this table's own category-C
  rule they are diagnostics. Left uncorrected, an implementer working the table
  literally would rewrite five error messages to resolve through `roster.json` —
  work this baseline forbids, done in the name of a requirement that forbids it.
- **`config.py:79,136,183` were offered as evidence for category C and do not
  demonstrate it.** `:79` is `PROJECT_LOCAL_RELATIVE_PATH = Path(".agents") /
  "knowledge-store" / "config.json"` and `:136` is the `Path.home()` fallback —
  both path *construction*, feeding a real filesystem walk. `:183` is a
  `raise ValueError` with no path fragment in it at all. And none of the three
  contains a `roster/`-relative literal: `grep -n roster` on that file matches
  only comments at `:13`, `:43`, `:119`. Whatever exempts them, it is not the
  rule as stated — they are self-referential `.agents/knowledge-store/`
  construction inside the store's own module, which is a different exemption
  and needs saying so.

**Consequence for Phase C's size, in the opposite direction from Revision 5.**
Revision 5 grew the phase on the strength of "roughly nine more violations."
Six of the nine were real; five of what it counted were not violations at all
and two real ones (`select_agents.py:203-204`) were already scheduled in Phase A.
Category B is still the phase's body and still makes PP-FR-2's manifest
load-bearing rather than decorative — it is simply a six-line change, not a
nine-plus-unknown one.

**The exemption must be a rule over call targets, not over files, and
`test_context_boundary.py`'s existing method cannot express it.** That test's
`_non_docstring_string_literals()` returns bare `ast.Constant` nodes with no
parent pointer, which is sufficient there because that boundary has no
legitimate use of its forbidden tokens anywhere. This one does. Sketch:

- build an `{id(child): parent}` map in one `ast.walk()` pass (Python's `ast`
  carries no parent links);
- for each candidate literal, walk to the nearest enclosing `ast.Call`;
- **category C** iff that call's target is in a closed sink set — `raise`
  statement values, `ValueError`/`RuntimeError`/exception subclasses, `print`,
  `logging.*`, `sys.stderr.write`;
- **category B** iff not a C-sink and the literal is a roster-package filename
  or joins into a `roster/`-relative path inside a `Path(...)`/`open(...)`;
- **category A** iff not a C-sink and the literal equals a role id drawn from
  `catalog.yaml` — a *generated* set, matching this requirement's own preference
  against hand-maintained allowlists.

**Re-derive the example sets from that rule once it exists, rather than carrying
the ones above forward.** Both example sets in this table were hand-classified
in prose and both were wrong; a third hand-classification is not the fix.

**The category-A violation is larger than every revision has recorded, and this
is the finding that most changes what PP-FR-6 is for.** Revisions 4 and 5 framed
it as changing Cadre's `support` lists. One call further on,
`build_dispatch_plan.py:547-551`:

```python
raise ValueError(f"Routing selected an unknown agent: {agent}")
```

Every selected agent is validated against the catalog. A foreign roster has no
`code-reviewer`, so for any roster whose routes declare `quality_gates`, with
lifecycle contracts resolvable, **the selector raises and emits no plan** — PP-FR-1's
own acceptance criterion. So PP-FR-6 category A is not boundary hygiene that can
be deferred or narrowed; it is a **functional prerequisite** for PP-FR-1 and
PP-FR-3. OD-9's option 3 is withdrawn on this basis (`product-intent.md` §13).

**OD-9 — RESOLVED at Revision 7: option 1, via `routing.yaml`.**
(`product-intent.md` §17.) `_gate_agents()` takes its fallback from a new
top-level `default_gate_review_agents` key rather than a Python literal:

```yaml
# roster/orchestration/routing.yaml
default_gate_review_agents: ["code-reviewer"]
```

```python
# build_dispatch_plan.py:107
*contracts[gate_id].get("review_agents", default_review_agents)
```

threaded from the loaded routing config at the `:673` call site. Cadre's own
`routing.yaml` declares the same literal the Python default held, so **Cadre's
plans stay byte-identical** and the ~15 `test_selector.py` assertions that pin
`code-reviewer` in `support` do not move. A foreign roster that omits the key
gets `[]` — no injected reviewer, and no `ValueError` from `:547-551`.

**Not provider-profile `gate_bindings`**, which was the live alternative inside
option 1. That mechanism exists and the kernel already models it
(`kernel/agentic_sdlc/__init__.py:1643-1646`, `:1761-1771`), but it binds gates
to *approval authority* — a different axis from dispatch reviewer selection —
and using it would place roster-side dispatch defaults inside a kernel-owned
concept, in a change whose constraint is to leave that boundary alone.

**One observation from that alternative survives it, and is worth filing
separately.** `_gate_agents()` reads `author_agents` and `review_agents` from a
contract that has never declared either, in any version. Both are dead paths
wearing the costume of fallbacks — which is exactly how the category-A defect
stayed invisible for five revisions. Retiring them is a change against the
kernel contract, not this one. Recorded as **G-11**.

**Consequence for PP-NFR-1:** `routing.yaml` is mirrored into `plugin/suite/` by
the same prefix copy as the schemas, so this adds one modified file to the
expected diff. That is item 5 in the list below, and it is the reason that list
exists at all.

**Why it fires universally, and why no existing detector sees it.**
`build_dispatch_plan.py:107` defaults a gate's reviewers to `["code-reviewer"]`
whenever a lifecycle gate contract declares no `review_agents`. **No gate in
`kernel/contracts/lifecycle-gates.json` declares one** — the string does not
appear in the file, and neither does `author_agents`, so `_gate_agents()`
returns exactly `["code-reviewer"]` and nothing else. The default fires for
every configured gate on every lifecycle-aware plan, and `:673` appends the
result to `support`. Observed, not inferred, and re-observed at Revision 6:

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
(`:254-265`) reads each matched route's declared `workflow_shape`, which the
roster supplies. Revision 3 read the earlier precedence branches (`:150-196`),
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
- `git status --porcelain` shows no change under `provider/roles/` or
  `roster/catalog.yaml`.

  **Amended at Revision 9: `roster/orchestration/routing.yaml` is exempt,
  because G-12's disposition renames it to `routing.json`.** This is a
  deliberate, Product-Owner-authorised narrowing of "Cadre is observably
  unchanged", and it is the first one. It is recorded rather than quietly
  applied because PP-NFR-1 is the requirement that makes the whole "leave Cadre
  alone" constraint checkable, and an exemption slipped into it silently would
  hollow it out. The rename also changes 159 generated Codex wrappers, which
  item 6 below forbade; item 6 is narrowed to match.

  **What PP-NFR-1 still means, undiminished:** Cadre's *roles*, *routing rules*,
  *selection behaviour*, and *CLI surface* do not change. The golden corpus
  stays unedited and default selection stays byte-identical. A file rename that
  the generator propagates is a different class of change from a behavioural
  one, and this exemption covers only the former.

  **Under `plugin/`, the diff must match this list exactly, plus the rename's
  mechanical propagation:**

  1. **`plugin/roster.json`** — new. Note the path: `generate_global_plugin.py:813,819`
     copies each `PROVIDER_BUNDLE` member to `plugin_root / name`, so the bundle
     lands *flattened at the plugin root* (`plugin/provider.json`,
     `plugin/profiles/`, `plugin/extensions/`). **There is no
     `plugin/provider/` directory**, and Revision 4 named one.
  2. `plugin/suite/roster/orchestration/roster.schema.json` — new. Not optional
     and not avoidable: `generate_global_plugin.py:1426` copies
     `roster/orchestration/` into `suite/` by prefix, which is why
     `routing.schema.json` and `selection.schema.json` are already there.
  3. ~~`plugin/suite/roster/orchestration/selection.schema.json` — modified.~~
     **Removed at Revision 7.** PP-NFR-3b is retracted, so there is no bump and
     the file does not change. Struck rather than deleted because Revision 4
     omitted it, Revision 5 added it back, and a reader tracking this list
     deserves to see it leave for a reason rather than vanish.
  4. `plugin/suite/` mirrors of every platform source file this work edits —
     `select_agents.py`, `build_dispatch_plan.py`, `settings.py`,
     **`schema_validate.py`** (PP-FR-2 needs new arguments there; see below),
     `mcp/dispatch_core.py`, `mcp/dispatch_server.py`, and `bin/`. `suite/` is a
     **copy** of that source; editing the source *is* editing `plugin/`.
  5. **`plugin/suite/roster/orchestration/routing.yaml` — modified.** Added at
     Revision 6 as conditional; **confirmed at Revision 7**, since OD-9 resolved
     to option 1. `routing.yaml` gains `default_gate_review_agents` and is
     carried into `suite/` by the same `:1426` prefix copy as the schemas.

     Note the net effect on this list across two revisions: item 3 left and
     item 5 arrived, so the count is unchanged and the contents are not. That is
     the whole argument for item 6.
  6. **The rename's propagation**: `plugin/suite/roster/orchestration/routing.json`
     replacing `routing.yaml`, and the `routing.yaml` → `routing.json` string in
     every generated Codex wrapper and Cline port. Mechanical, generator-driven,
     and covered by `--check`.
  7. Nothing else. In particular: no change to `agent-catalog.json`,
     `provider/roles/` content, or any plugin manifest, and **no change to any
     wrapper beyond the renamed path string**.

  **This bullet has been unsatisfiable in every revision to date, including
  both revisions whose stated purpose was to fix it.** Revision 1: "no change
  under `plugin/`", while scheduling edits to three bundled files. Revision 2:
  "the only change is `provider/roster.json`", same problem. Revision 4: an
  exact four-item list that named a directory which does not exist, omitted a
  modified file it had just finished explaining, and missed a fourth mirror.
  Revision 5 corrected all three of those and missed `routing.yaml`. **Six
  revisions, six wrong lists, each one produced by reasoning about the
  generator instead of running it.** The gate that makes "leave Cadre alone"
  checkable is the most-revised and most-wrong thing in this baseline.

  **Revision 6 stops correcting the list and deletes the instruction to trust
  it.** Item 6 is where the real constraint lives. Items 1–5 are bookkeeping
  with a 100% historical error rate, and the plan already says the right thing
  (`implementation-plan.md` §2: *"generate the package and read the diff rather
  than predicting it"*). Treat items 1–5 as a **hint about where to look**, not
  as an acceptance criterion — the acceptance criterion is item 6 plus
  `--check`, which costs one command and has never been wrong.

  **Observed at Revision 10, after Phases A′ and C′ actually ran.** The list
  above was a prediction; this is what the generator produced. It is recorded
  as an observation, not promoted back into a criterion:

  - `plugin/suite/roster/roster.json` — new, **and it did not arrive by
    itself.** `generate_global_plugin.py` has a **second closed allowlist** for
    roster-root files, separate from `PROVIDER_BUNDLE` at `:101` and unmentioned
    in any revision of this baseline. `roster.json` was silently skipped by it,
    and seven `test_repository_health` tests failed against a packaged selector
    that could not find its own manifest. **This baseline recorded that exact
    trap for `provider/` and did not know there were two of them.**
  - `plugin/suite/roster/orchestration/roster.schema.json` — new, automatic via
    the `:1426` prefix copy, exactly as predicted.
  - `plugin/suite/roster/orchestration/routing.json` — modified, by OD-9's
    `default_gate_review_agents` key. Predicted at Revision 6.
  - `plugin/suite/` mirrors of `select_agents.py`, `build_dispatch_plan.py`,
    `routing_overlay.py`, `settings.py`, `schema_validate.py`,
    `roster_manifest.py` (new), `mcp/dispatch_core.py`, `mcp/dispatch_server.py`.
  - `selection.schema.json` — **unchanged**, as PP-NFR-3b's retraction requires.
  - The golden corpus — **unedited**, verified after every phase.

  The prediction was closer this time and still incomplete, in a way no amount
  of re-reading would have closed: nobody knew the second allowlist existed
  until a file needed to pass through it.

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

  **"With the in-tree kernel resolvable" is ambiguous, and one reading
  reintroduces the host-dependence the corpus exists to remove.** Resolved at
  Revision 6. Two existing tests already make this assertion —
  `test_selector.py:964` and the gate-agents subtest at `:1109-1114` — and both
  are `@unittest.skipUnless(AGENTIC_SDLC_AVAILABLE, ...)`, where availability is
  `os.environ.get("AGENTIC_SDLC_BIN") or shutil.which("agentic-sdlc")`
  (`:41`). That is *ambient* resolvability. It passes in CI because
  `.github/workflows/validate.yml:46` sets the variable, and on a developer
  machine where the kernel happens to be pip-installed — and **silently skips**,
  not fails, on a bare checkout. A detector that disappears on the hosts least
  likely to have the dependency is not a detector.

  **Force it instead.** `bin/agentic-sdlc` is an in-tree wrapper that runs
  `kernel/` with no install (verified: `./bin/agentic-sdlc show-contract
  lifecycle-gates` returns the live contract on a bare checkout, no network).
  Point at it explicitly inside the test:

  ```python
  with mock.patch.dict(os.environ, {"AGENTIC_SDLC_BIN": str(IN_TREE_WRAPPER)}, clear=False):
      result = plan(task="Update the OpenTofu module for the VPC", ...)
  self.assertIn("code-reviewer", result["agents"]["support"])
  ```

  This is deterministic, checkout-only, and symmetrical with how the corpus
  forces the *opposite* condition. One gate-bearing task suffices for OD-9's
  defect specifically — it is binary and uniform across gate-bearing routes —
  but pin the negative case too (a route with no `required_quality_gates` must
  *not* acquire `code-reviewer`), which is what `:1109-1114` already asserts and
  is cheaper to extend than to duplicate. Note `_fetch_contract` is
  `@lru_cache(maxsize=1)` keyed on the executable path, so resolve inside the
  patched block rather than before it.

- **Phase C changes roughly fifteen existing assertions, and no revision has
  said so.** `test_selector.py` asserts `code-reviewer in agents.support` at
  `:342`, `:466`, `:964`, `:979`, `:1109-1114` and elsewhere, all currently
  green under CI's forced `AGENTIC_SDLC_BIN`. Under OD-9 option 2 they all
  change; under option 1 they must be confirmed unchanged, which is the point.
  **Produce that inventory before OD-9 is decided, not after** — the decision is
  between "Cadre's output is preserved" and "Cadre's output changes," and the
  people making it should see the real number rather than an abstraction about
  corpus blindness.

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

**~~PP-NFR-3b — `selection.schema.json` bumps 6 → 7.~~ RETRACTED at Revision 7.**
It existed only because PP-FR-1b added an emitted field, and PP-FR-1b is
retracted by OD-2's reversal. No new property is emitted, so the schema stays at
`const: 6`, no vendored consumer copy needs updating, and OD-7 — which asked
whether to authorise that contract change — is **withdrawn rather than decided**
(`product-intent.md` §13, §17).

The reasoning it carried is still correct and worth keeping for the next time
this comes up: the schema is closed (`additionalProperties: false`, `:6`) and
vendored away from its producer into both the wheel and `plugin/`, so a pinned
consumer copy rejects a plan carrying an unknown property *while the plan
truthfully reports the version that copy claims to handle* — a silent failure
naming the wrong cause. Any future emitted field faces the same bump for the
same reason. This work simply no longer adds one.

**Consequence for PP-NFR-5, which shrinks:** without the bump, no plan's
`schema_version` or `dispatch_fingerprint` changes at all. The churn PP-NFR-5
was written to authorise does not occur, and its same-version reproducibility
requirement stands on its own.

**PP-NFR-4 — Every new guard is proved non-vacuous.** For each of PP-FR-2,
PP-FR-3(c), and PP-FR-6: plant the defect, confirm the check **fails** with a
message naming the real cause, revert, confirm the tree is clean. Recorded in the
pull request. This is the repository's settled bar and, given §0's history, not
discretionary.

**Three gaps in the coverage, found at Revision 6 by checking the requirement
against `implementation-plan.md` §2's actual list rather than against its
intent.**

1. **The second entry point has no planted-defect case.** `mcp/dispatch_core.py`
   and `mcp/dispatch_server.py` are added to PP-FR-6's platform list precisely
   because five revisions missed them, yet the only planted role-id case targets
   `select_agents.py`. That is backwards: plant it in the module most recently
   discovered to be out of scope, because the self-vacuity guard detects an
   **empty** module list and never an **incomplete** one.
2. **Category B has no planted-defect case at all**, though it is the phase's
   body. Reintroduce a literal `"catalog.yaml"` into a resolution path and
   confirm the guard names it.
3. **The category-C rule needs a false-positive case.** A guard that is
   non-vacuous can still be over-broad. Confirm a genuine diagnostic literal
   inside a `raise ValueError(...)` **passes** — otherwise the rule earns its
   category-C exemption by never being tested against one, and the correction at
   PP-FR-6 above shows exactly how easily that goes wrong in prose.

**And one item's evidence must become an artifact rather than an assertion.**
PP-FR-5's non-vacuity is a scratch branch on which `build_dispatch_plan.py:29`
follows the resolver. Every other item in the list plants a defect *in the tree*
and can be reproduced by a reviewer; this one is the sole item an implementer
can claim without leaving anything checkable behind. Require the scratch
branch's diff and the failing output be attached to the pull request. Phase D
changes no behaviour, so this run is the only thing distinguishing it from two
assertions that have always passed.

**PP-NFR-5 — Determinism preserved. ~~Fingerprint churn expected.~~ No churn, as of Revision 7** — PP-NFR-3b's retraction removes the bump that would have caused it, so every default-roster plan keeps today's `schema_version` and `dispatch_fingerprint` byte for byte. What follows is retained because the same-version reproducibility requirement is unaffected and is the part that matters. Revision 1
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
  2026-08-11, after the fact, and **re-affirmed against the current intent
  record later the same day** (`product-intent.md` §18). **Neither act closes
  this gap and the re-affirmation does not touch it** — approving an intent gate
  does not retroactively reorder the requirements work that ran ahead of it. The
  gap is disclosed, not closed, and stays that way.
- **G-6: the G1 approval has no machine-checkable evidence — and there are now
  two of them.** The 2026-08-11 re-affirmation (`product-intent.md` §18) is the
  same evidence class as the original: prose transcribed by the authoring
  session. The `.github/CODEOWNERS` merge review remains the available,
  machine-checkable corroboration and remains unused. This repository
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

  **Confirmed again at Revision 6, and the cost is now measured rather than
  argued.** The review's own retrieval returned one result at score 0.033 — an
  unrelated Compose/PostgreSQL sample — and then re-derived from scratch two
  findings already sitting in `staged_records`. It also proposed **six new
  durable findings**, which are now staged alongside the twelve and equally
  unreachable. The staging half works so well that the gap widens with every
  session that uses it.

- **G-8: PP-FR-6's forbidden-token list does not cover route ids or risk ids,
  and `_select_workflow()` is full of them.** `build_dispatch_plan.py:129-265`
  names Cadre route ids at `:150`, `:154`, `:156`, `:158-159`, `:177`, `:181`,
  `:183`, `:185-196` and risk ids at `:152` and `:263` — the last **inside** the
  stage PP-FR-3 describes as roster-driven. The rule covers role ids, phase
  names, and `roster/`-relative paths, so success criterion 5 (plant a role id →
  guard fails) can pass while a dozen Cadre identifiers remain in the function
  assigning every plan's workflow. Intent §4 outcome 4 would be met in letter
  and not in substance.

  PP-FR-3 already records the *consequence* — a foreign roster cannot reach
  `rollback`, `production-release`, or `support-escalation` — as a known
  limitation. What was not recorded is that **those are the three workflows
  bound to human gates**, which makes "roster-neutral except for the highest-risk
  paths" a scope statement rather than a footnote. Whether that is acceptable is
  a decision; it is deliberately not raised as one here because the answer may
  well be yes, but it should be answered rather than inherited.

- **G-9: the golden corpus has no generator.** 195 KB, 4,727 lines, 175 cases,
  maintained by run-test/read-diff/hand-edit. PP-NFR-1 leans on "the corpus is
  not edited" as its primary signal, which makes the file a change-detector that
  is itself hand-maintained — and a reviewer approving a 175-case diff by eye is
  the same trust-the-shape failure PP-NFR-4 exists to prevent everywhere else.
  Out of scope here and correctly so; worth filing.

- **G-12: `routing.yaml` is JSON, and its extension says otherwise.**
  `routing_overlay.py:502` parses the base routing file with
  `json.loads(base_text)`. Cadre's own file is JSON-formatted, so nothing has
  ever noticed. **Found by the Phase 0 spike on the first run of the first
  foreign roster this repository has had** — a fixture authored as real YAML,
  which is the obvious reading of a file named `routing.yaml`, died with
  `json.decoder.JSONDecodeError: Expecting value: line 1 column 1 (char 0)`,
  naming neither the file nor the format nor the requirement.

  This is **A3 made concrete** (*"every assumption the reference roster happens
  to satisfy is currently invisible"*), and it was not reachable by reading:
  six revisions of these records cite `routing.yaml` constantly, PP-FR-2
  specifies a `routing` path in `roster.json`, and PP-FR-3 requires a fixture
  with "its own `routing.yaml`". None of it noticed the format.

  **RESOLVED 2026-08-11: rename `routing.yaml` to `routing.json`.** Product
  Owner decision. The file says what it is, and the trap is removed permanently
  rather than labelled.

  Two options were rejected with evidence. **Parsing YAML instead is actively
  dangerous for this file**: `yaml.safe_load("keywords: [no, on, off, yes]")`
  returns `[False, True, False, True]`, and this is a *keyword-matching* file —
  a routing keyword coerced to a boolean stops matching silently, with no error
  and no test failure unless someone happens to pin that exact keyword. JSON is
  the safer parser here, which is plausibly why it was chosen and was never
  written down. **Improving the error message alone** was rejected as labelling
  the trap rather than removing it.

  **Scope of the rename, which is narrower than a `grep` suggests.** 1,099
  occurrences across 622 files, of which 517 are generated or mirrored and
  regenerate for free. Three further categories are excluded **on doctrine, not
  convenience**, and a naive `sed` across all of them would do real damage:

  - **Historical run records** (nine sibling directories under
    `roster/orchestration/runs/`). They record decisions taken when the file
    genuinely was `routing.yaml`. Rewriting them falsifies the archive, on the
    same principle that makes `docs/proposals/` never-revised
    (`test_repository_health.py:2154`).
  - **`docs/proposals/`** — same rule, stated directly by that test.
  - **`fixtures/selection_golden_corpus.json`** — its 18 occurrences are all
    inside `notes` prose explaining route overlaps, never in path resolution.
    Editing them would touch the one file PP-NFR-1 relies on staying untouched,
    for no functional gain.
  - **`roster/knowledge-store/proposed-knowledge/*.md`** — staged records carry
    a `content_digest` over their body; hand-editing invalidates it.

  **PP-FR-2 must still state the required format explicitly**, because the
  rename fixes one file and not the underlying gap: **`catalog.yaml` is real
  YAML and `routing.yaml` was JSON**, two siblings in one roster package with
  one extension between them. `schema_validate.py` carries both loaders
  (`:71` yaml, `:76` json) to cope. After the rename the extensions are honest,
  but a roster author still needs telling which file is which format — see
  `phase-0-and-d-evidence.md`.

- **G-13: implementation ran ahead of the gate approving its requirements.**
  Sibling to G-5, which records that this baseline was drafted before G1. Phases
  A′, B′ and C′ built against requirements G2 had not yet approved. Partially
  mitigated — every phase followed the Product Owner's detailed dispositions
  (OD-2, OD-9, OD-11 all closed first), and several requirements were *corrected*
  by the implementation rather than merely satisfied by it. Approving G2 accepts
  the baseline as it stands; it does not make the order correct. Disclosed, not
  closed, on the same terms as G-5.

- **G-11: `_gate_agents()` reads two contract keys that have never existed.**
  `build_dispatch_plan.py:107` calls `.get("author_agents", [])` and
  `.get("review_agents", <default>)` against `kernel/contracts/lifecycle-gates.json`,
  which declares neither, in any version. Both are dead paths wearing the costume
  of fallbacks — and that costume is precisely why the category-A defect survived
  five revisions of review: a `.get(key, default)` against a contract that never
  declares `key` is not a fallback, it is an unconditional hardcode. OD-9 fixes
  the `review_agents` side by making the default roster-declared; the reads
  themselves should be retired against the kernel contract, which is a different
  change with a different owner. Filed at Revision 7.

- **G-10: `docs/proposals/` has no supersession pointer and no index.**
  `test_repository_health.py:2154` makes those records point-in-time and never
  revised, so cite-and-supersede is the conforming handling and this work did it
  correctly. But `governance-as-product-2026-08.md` still reads `Status:
  BACKLOG — explicitly parked` with nothing indicating it was reversed, and its
  filename is the more discoverable entry point than this run directory. A
  reader who opens it first gets a stale picture. A one-line `Superseded by:`
  treated as metadata rather than revision, or a directory index, would close it
  without touching the decision content.

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

**Option (3) is withdrawn at Revision 6** — see PP-FR-6's category-A note.
`build_dispatch_plan.py:547-551` raises on an agent absent from the catalog, so
leaving `:107` alone does not preserve a documented wart; it makes PP-FR-1's own
acceptance unreachable for any roster that uses lifecycle gates. It must not be
offered as a choice.

**And (1) has a live disagreement about *where* the default should live, which
the choice between (1) and (2) does not settle.** Two reviewers reached opposite
conclusions independently:

- `architecture-authority` observes that the kernel already models gate → agent
  binding as provider-profile `gate_bindings`
  (`kernel/agentic_sdlc/__init__.py:1643-1646`, `:1761-1771`), and that
  `_gate_agents` currently reads two keys — `author_agents`, `review_agents` —
  from a kernel contract that has **never declared either**. Homing the default
  in profile data would retire those reads rather than relocate them, collapsing
  a duplicated concept instead of adding a third location.
- `application-engineer` argues against precisely that: `gate_bindings` binds
  gates to *approval authority*, a different axis from dispatch reviewer
  selection, and conflating them crosses the kernel-ownership boundary §4
  forbids this work from touching. It proposes a `routing.yaml` key
  (`default_gate_review_agents`), which a foreign roster already supplies via
  `roster.json`'s `routing` path, and which keeps Cadre byte-identical by
  declaring the same literal the Python default held.

Neither saw the other's reasoning. **This is a genuine architectural fork inside
OD-9 and is recorded rather than resolved** — it touches the kernel boundary, so
it is not the authoring session's to settle in either direction. Note the second
option is the one that adds `routing.yaml` to PP-NFR-1's mirrored-file list.

**OD-8, raised at Revision 3 as blocking Phase C, is withdrawn at Revision 4.**
It asked a System Architect whether a foreign roster's workflow classification
should come from a mapping in `roster.json` or from opening a published schema
enum. `_select_workflow()` already answers it from roster-declared
`workflow_shape`. Nothing was blocked and there was nothing to decide.

**All blockers cleared on 2026-08-11** (`product-intent.md` §17). For the record,
since five revisions of this baseline were organised around them:

| Was blocking | Outcome |
| --- | --- |
| **OD-7** — `selection.schema.json` 6 → 7 | **Withdrawn.** PP-FR-1b retracted, so no field, so no bump, so no question. |
| **OD-9** — the `["code-reviewer"]` default | **Resolved** — option 1 via a `routing.yaml` key. Cadre's output stays byte-identical. |
| **OD-10** — OD-2's control absent on the MCP surface | **Withdrawn.** No project-tier redirect exists for it to miss. The independent-resolution observation stands and still binds PP-FR-6. |
| **OD-11** — `roster.json` compatibility window | **Resolved** — no window, with unknown-`schema_version` rejection adopted as the mitigation. |
| **OD-13** — G2's second authority | **Resolved** — both roles assigned to `@deagy`, recorded. The kernel permits it; only author-versus-approver separation is enforced, and every author here is an agent. |

**Four of the five were closed by one decision.** OD-2's reversal to
`SCOPE_GLOBAL_ONLY` withdrew OD-7 and OD-10 outright and shrank Phase A by
removing the identity field, the schema bump, and the MCP restructuring. That is
worth noting against how this baseline had been reasoning: it treated the five
as five, and priced OD-2 as a settled input rather than the largest lever on the
list.

**Still open, and non-blocking:** OD-3 (naming), OD-4 (`knowledge-store/AGENT.md`
location), and **OD-12** — whether G1, granted against intent Revision 1,
extends to this baseline. OD-12 got *sharper* at Revision 7 rather than
softer: the Product Owner has now reversed one of their own recorded
dispositions, which changes the approval's evidence base more than any of the
factual corrections that raised the question.

OD-6 was closed as "yes" at Revision 6.

Per `roster/workflows/product-intake.md`, objective conflicts return to G1 rather
than proceeding. Nothing here is approved by its presence in this file.


---

## 8. G2 — Requirements Baseline: APPROVED

**Decision.** `@deagy` approved G2 on 2026-08-11, against **Revision 10's
content** — the revision this one records the approval into, which adds the
record and nothing else.

**Both authority requirements are satisfied by one human, and that is stated
rather than implied.** `kernel/contracts/lifecycle-gates.json` requires
`["product_owner", "engineering_lead"]` for G2. Per **OD-13**, `@deagy` holds
both roles, which the kernel permits: `validate_repository()`
(`__init__.py:1948-1963`) requires each role in `AUTHORITY_ROLES` to have *an*
assignee and contains no check that two roles are two people. The separation the
kernel does enforce is author-versus-approver, and every author of these records
is an agent.

Recording it this way is the point of OD-13's disposition. One signature
covering two required authorities is weaker evidence than two people
disagreeing, and writing it down is what keeps that weakness visible instead of
letting a satisfied checkbox imply something it does not.

### What this approval rests on that G1's did not

G1 was granted against a planning record. G2 is granted against a baseline whose
requirements have since been **executed**: six phases landed (D, 0, A′, C′-1,
C′-2, B′), with eleven planted defects as attached non-vacuity evidence
(`phase-0-and-d-evidence.md`), 1276/208/218/309 tests green, no plugin drift,
and the 175-case golden corpus never edited.

That is an unusually strong evidence base for a requirements gate, and it is
strong in the specific way this baseline argued for: the claims were not merely
re-read, they were run. Three of the findings behind those phases — G-12, the
second closed allowlist, and a guard passing twelve of twelve with the coverage
wrong — were unreachable by any amount of reading.

### The disclosure this approval does not close

**Implementation ran ahead of the gate that approves its requirements**, and
that is the same class of ordering defect as **G-5**, which records that this
baseline was drafted before G1.

Phases D and 0 were genuinely gated on nothing and cost nothing to run early.
A′, B′ and C′ were not: they built against requirements this gate had not yet
approved. The mitigation is real but partial — every phase was built against a
baseline the Product Owner had already dispositioned in detail (OD-2, OD-9,
OD-11 all closed before A′ started), and several requirements were *corrected*
by the implementation rather than merely satisfied by it.

Approving G2 now does not retroactively make that ordering correct. It records
that the baseline is accepted as it stands, including the parts the
implementation revised. **G-5's disclosure gains a sibling and neither is
closed** — see G-13.

### What it does not do

- **G-6 stands, and now covers three records.** §16, §17, §18 and this section
  are all prose transcribed by the authoring session. This repository runs no
  `.agentic-sdlc/` overlay, so no gate state transitioned, no `agentic-sdlc
  decide` was invoked, and nothing in CI can verify any of them.
- **No later gate is implied.** G2 approves the requirements baseline. Phase E
  remains unbuilt and is not covered by anything here.
- **The open non-blocking decisions stay open**: OD-3 (naming) and OD-4
  (`knowledge-store/AGENT.md` location).

**A GitHub review remains the only machine-checkable corroboration available,
and remains unused.** Three transcribed approvals now rest on this record set
where one did. Cross-referencing one here, if given, would do more for the audit
trail than any further prose about what transcription is worth.

**Corrected before merge:** an earlier revision of this paragraph said that
review "has to happen before this branch can merge". It does not.
`.github/CODEOWNERS` auto-requests a review from `@deagy`; `main`'s ruleset sets
`required_approving_review_count: 0` and `require_code_owner_review: false`, so
nothing blocks a merge on it — consistent with the decision
`docs/migration/monorepo-migration.md` records, which this baseline cites
elsewhere and contradicted here. The review is worth having on its merits, not
because it is unavoidable.
