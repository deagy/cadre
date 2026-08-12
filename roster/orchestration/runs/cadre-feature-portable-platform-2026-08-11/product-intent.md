# Product Intent Record — A roster-neutral platform: separating the knowledge store, the roster, and the lifecycle

**Intent ID:** `INTENT-CADRE-PORTABLE-PLATFORM`
**Revision:** 4
**Status:** **G1 APPROVED** by `@deagy` (Product Owner) on 2026-08-11 against
Revision 1. See §16.
**Revision note:** Revisions 2 and 3 change **no intent, no scope, and no
decision**. Both correct factual defects found by review.

Revision 2 fixed §9's disposition table row 1 (which still stated the
pre-reversal position §11 and §16/OD-5 reject), §2's `SECURITY.md` citation
(one heading off), and rewrote §14 — and **Revision 3 rewrites §14 again,
because Revision 2's replacement was also wrong, in a more confident way.**
Revision 1 said the store was empty. Revision 2 said the twelve committed
records prove a retrieval failure. Neither holds: the records are **staged, not
ingested**, and staging *"confers no retrievability"* by design
(`.agents/skills/run-agent-orchestration/SKILL.md:94`; `search_store()` scores
only `chunks` rows, while staged records live in `staged_records`). The
retrieval behaved correctly. Revision 3 states the real gap, which is neither of
the first two, and drops the "weakens the G1 approval" disclosure Revision 2
attached to its own mistake.

Revision 3 also corrects §12 criterion 6 (Revision 2's narrowing was still
unsatisfiable — see `requirements.md` PP-NFR-1), **withdraws OD-8** from §13
(its premise was a misreading of `_select_workflow()`'s final stage), and fixes
two counts: §14's accepted-record count, and §9's list of stale "74 roles"
sites.

The G1 approval in §16 stands and is not re-opened. Nothing corrected in either
revision changes the problem, the outcome, the scope, or the constraints it was
granted against.

**Revision 4** corrects §2, where a claim about `cadre sdlc` turns out to be
half false — `--provider` already passes through to the kernel; what fails is
that Cadre's bundle cannot be suppressed, so a foreign provider loads and is
then rejected for colliding with it. §2's "with no override" and §4's outcome 2
are restated. It also fixes §2's outbound-import count, §12 criterion 3
(vacuous as written), and adds **OD-9** to §13. None of this changes the intent
or the scope; the seam described in §4 is the same seam, and the work behind
outcome 2 is smaller than stated, not larger.

**The pattern is worth naming, since this record's subject is a repository that
keeps rediscovering its own failure modes.** Three revisions of §14 produced
three different confident claims about the knowledge store, each written after
reading more than the last. And §2 asserted for four revisions that `cadre sdlc`
offers no provider override, in a record whose whole method is citing
`file:line` — the line was cited correctly and its behaviour was never checked.
**Both errors share a shape: reasoning about a running system from its source
alone.** One shell command falsified each.
**Author (agent):** product-intent-agent, consolidated by the orchestrating session
**Date:** 2026-08-11
**Repository:** `/home/deagy/sdk/cadre`
**Classification:** internal
**Source:** Feature request from the repository owner, 2026-08-11: *"separate knowledge store, agent rosters, and the SDLC into separate projects. We should be able to use any roster of agents with the SDLC, not necessarily just the one currently built into cadre. Ideally this separation would leave cadre alone."*
**Supersedes:** `docs/proposals/governance-as-product-2026-08.md` (BACKLOG, parked by the Product Owner 2026-08-09) — see §9.

---

## 0. Authorship note (read before the rest)

This record was written by the session that also explored the tree and drafted
the sibling `requirements.md` and `implementation-plan.md`. Under this
repository's authorship/approval separation invariant it carries **no approval
authority**. A human decides G1. §11 lists the decisions a reviewer should
specifically push back on, and §6 lists what a reviewer must not read as settled.

**On §16's approval record.** The G1 approval and the OD dispositions in §16 were
made by the human Product Owner and *transcribed* by the authoring session at
their instruction. The authoring agent did not approve, could not
(`roster/shared/agent-autonomy.yaml`: `approve_own_work: never`;
`kernel/contracts/lifecycle-gates.json`: `author_cannot_review_or_approve_same_revision: true`),
and the decision content in §16 originates entirely with the Product Owner. A
transcription is weaker evidence than a countersigned record — see §16's own
caveat on what this repository can and cannot attest.

No code was changed by this work. The three documents in this directory are the
entire deliverable.

## 1. Owner

**Accountable Product Owner:** `@deagy`, resolved from `.github/CODEOWNERS` per
`roster/RUNBOOK.md:730`, which states directly that *"a `product-intent-agent`
dispatch against this repository's own backlog should resolve the Product Owner
from `.github/CODEOWNERS` rather than re-logging its absence as a blocking gap."*

This is a deliberate departure from the archived intent records in sibling `runs/`
directories, every one of which logs "no standing Product Owner" as a blocking
open decision (e.g. `cadre-feature-agent-context-store-2026-08-11/product-intent.md`
OD-1, `cadre-proposal-01-route-match-reasons-2026-08-08/product-intent.md` §1).
Those predate RUNBOOK:730 and are superseded by it. **G1 has a named authority
here; the gate is not blocked on ownership.**

**Working owner for authorship:** the orchestrating session. Authorship does not
confer approval.

## 2. The user problem

The request contains three claims. Two are already true and undocumented; one is
false today and is the entire cost of the work.

**"Use any roster of agents with the SDLC."** The G1–G10 kernel already supports
this. `load_provider()` (`kernel/agentic_sdlc/__init__.py:192-304`) is a
fully-validated plugin point against `kernel/contracts/provider.schema.json`,
enforcing schema version, id uniqueness, a semver `kernel_compatibility` window,
declared dependencies, path-escape rejection, and — notably — the
authorship/approval invariant at load time: a catalog agent with
`kind: "reviewer"` may hold only `["reviewer"]` capabilities, or the provider is
rejected. `providers/agentic-sdlc-defaults/` is a **second, working provider
today**, carrying 15 generic agents and three profiles. The kernel knows agent
roles only as opaque provider-supplied ids; its own `AUTHORITY_ROLES`
(`__init__.py:65-92`) are a closed, kernel-owned enum of snake_case authority
names (`product_owner`, `security_lead`, …) that has nothing to do with roster
role ids. `kernel/README.md:103` says it plainly: *"The kernel ships no profiles
or agent catalog."*

The residual coupling is one line. `bin/cadre.py:124`:

```python
    provider = REPO_ROOT / "provider" / "provider.json"
```

`cadre sdlc` always injects *this* repository's bundle. **It is not, however,
true that there is "no override" — Revisions 1–3 said so and were wrong.**
`bin/cadre.py:125-127` appends the user's own argv (`*rest`) after the injected
flag, and the kernel's `--provider` is `action="append"`
(`kernel/agentic_sdlc/__init__.py:2918-2923`), so a caller's `--provider`
already reaches the kernel and is already validated. What fails is the
*combination*:

```console
$ ./bin/cadre sdlc --provider providers/agentic-sdlc-defaults/provider.json provider list
{ "error": "provider agentic-sdlc-defaults duplicates profile ids: ['generic']" }
```

The foreign manifest loaded. It was rejected for colliding with Cadre's, which
is loaded alongside it and cannot be turned off. So the residual coupling is
narrower than this record claimed for four revisions, and differently shaped:
not "no override" but **"no way to run without Cadre's bundle."**

**"Separate the knowledge store."** Also already true, and more so than any
document claims. `roster/knowledge-store/src/` is stdlib-only, reads neither
`catalog.yaml` nor `routing.yaml`, and reaches outside itself only into
`roster/shared/src/` — **four modules across five import sites**: `settings`
(`config.py:21`, `cli.py:23`), `content_protection` and `text_chunking`
(`content.py:26,31`), and `text_embedding` (`embeddings.py:33`). (Revisions 1–3
said "three outbound imports", which contradicted A4's own "those four
modules".) Most decisively: **the `--agent` argument is not validated
against any roster and selects nothing.** `build_agent_context()`
(`service.py:164-167`) checks only that it is non-empty; the value then appears
only in the retrieval audit row (`:176`) and echoed back in the returned bundle
(`:184`). It is a label, not an access-control key. The store has no principal,
no identity store, and no role-to-classification mapping — `SECURITY.md:50`
("Known limitations") says so: *"caller flags are not authentication."*

The consequence is worth stating precisely, because it cuts both ways:
extracting the store costs almost nothing, **and removes nothing from the trust
model, because the trust model never used roster identity in the first place.**

**"Not necessarily just the one currently built into cadre."** This is the false
one. `cadre select` cannot run against a roster it did not ship with:

```python
ORCHESTRATION_ROOT = Path(__file__).resolve().parent.parent   # select_agents.py:16
ROSTER_ROOT = ORCHESTRATION_ROOT.parent                       # :17
...
    catalog_path = ROSTER_ROOT / "catalog.yaml"               # :203
```

with the same shape in `build_dispatch_plan.py:29-30` (`KNOWLEDGE_STORE_ROOT`,
`ROSTER_ROOT`) and `:604` (every context-pack definition). Role definitions,
routing rules, the catalog, shared policy, and the knowledge-store CLI location
are all resolved relative to the checkout.

So the honest problem statement is not "three things are tangled." It is: **three
of the four components are already separable and were never advertised as such,
and the fourth silently prevents anyone from finding that out.**

## 3. Users and beneficiaries

- **A team that wants the G1–G10 lifecycle with their own roles.** Today they
  must either adopt Cadre's 159 roles wholesale or fork. The kernel would already
  serve them; nothing tells them so and `cadre sdlc` would not let them.
- **This repository's own maintainer.** Every future addition of platform
  machinery inside `roster/` makes the seam more expensive. The parked proposal
  named the knowledge store as the most recent example; the context store
  (`roster/context-store/`, merged 2026-08-11) is a second one that landed after
  the proposal was written.
- **A second roster author, hypothetical today.** The absence of one is itself
  the finding — see §10, assumption A3.
- **The knowledge store as a standalone consumer.** Any project wanting an
  audited, classification-filtered retrieval layer without adopting a role
  catalog.

Explicitly **not** a beneficiary: the LangGraph engine's release story. Its
checkout-only status (`engine/pyproject.toml`, `runtime.py:147`) is a packaging
defect independent of roster coupling, and is not fixed here.

## 4. Intended outcome and observable change

Four seams become real, none of them a directory move:

1. `cadre select --roster <path>` produces a schema-valid dispatch plan against a
   roster package the binary did not ship with.
2. `cadre sdlc --provider <other>/provider.json` drives the kernel with a foreign
   provider bundle **instead of Cadre's**, rather than alongside it. The flag
   already reaches the kernel today; what does not exist is any way to stop
   Cadre's bundle loading beside it, which makes the two collide.
3. The knowledge store's location is resolved rather than computed from
   `parents[2]`, so it runs with no roster present.
4. A structural test forbids platform code from naming Cadre-specific role ids,
   phases, or paths — the mirror of the existing `test_kernel_boundary.py`.

And one non-change, which is the point of the "leave cadre alone" constraint:
Cadre's install path, marketplace entry, generated `plugin/` output, CLI surface,
and role catalog are byte-identical afterwards.

## 5. Scope

- Roster-root resolution in the selector and dispatch-plan builder.
- A **roster package** definition. `provider/` already contains all 159 role
  `AGENT.md` files under `provider/roles/`, plus `agent-catalog.json`,
  `profiles/`, `extensions/`, and generated Codex wrappers. Verified: 159
  `AGENT.md` files under `provider/roles/`, 159 `definition:` entries in
  `roster/catalog.yaml`. It is already a complete roster package missing exactly
  two files — `routing.yaml` and `catalog.yaml`.

  **Recommended shape: a sibling `roster.json` the kernel never reads**, placed
  alongside `provider.json` and consumed only by the selector. This is a reversal
  of the drafting session's first instinct (extend `provider.json`), forced by
  A2: the manifest's key set is closed in two implementations, one of them the
  kernel, so extending it would mean changing the kernel in a change whose stated
  constraint is not to. A sibling manifest costs one more file and needs **zero**
  kernel or engine change. Settled at OD-5, not here.
- A deliberately minimal second roster, as a test fixture.
- A mirror boundary guard.
- A `--provider` override on `cadre sdlc`.
- Knowledge-store path resolution.

## 6. Exclusions

- **No repository splits.** See §7, C1.
- **No directory moves.** `roster/` keeps its shape; `roster/shared/src/`'s
  platform modules are *declared* platform, not relocated.
- **No change to the kernel ownership boundary**, the `AUTHORITY_ROLES` enum, or
  gate semantics. This work strengthens that boundary rather than revisiting it.
- **No publishing or PyPI change.** Both distribution names are squatted and that
  analysis stands (`docs/migration/monorepo-migration.md`).
- **No answer to the go-to-market question** — "is the platform or the roster the
  product." The parked proposal left it open deliberately and this record does
  not close it. Architecture and go-to-market can have different answers.
- **No engine release work.**
- **No renaming.** See OD-3.

## 7. Constraints

**C1 — Separation must not reintroduce cross-repo reconciliation.** This is the
binding one, and it is measured rather than asserted.
`docs/migration/monorepo-migration.md` records that of `deagy/cadre-lifecycle`'s
500 tracked files, **~340 were generated copies**, supported by an entire
coordination layer (`cadre-ref.txt`, `drift-check.yml`, `regenerate.yml`,
`notify-lifecycle.yml`, `apply_regeneration.py`), all deleted at the merge. The
merge also surfaced bugs that had shipped *because* the distribution lived in
another repository — 43 of 71 Cline agent files ported from a stale revision,
and a `bootstrap_sdlc.py` that could never have worked from an installed plugin.
The requester's "leave cadre alone" and this constraint point the same way.

**C2 — No fourth copy.** Knowledge-store code already exists in three places:
`plugin/suite/roster/knowledge-store/` (15 files), the wheel's
`cadre_cli/_vendor/` (via `pyproject.toml:118-124` force-include), and the Cline
path-rewrite table (`plugin/tools/port_cline_agents.py:174-448`). Any seam that
adds a fourth has failed.

**C3 — Selection stays deterministic.** `cadre select` returns `needs-triage`
rather than guessing when no rule matches. A roster-resolution mechanism must not
introduce a fallback that silently selects against the wrong roster.

**C4 — Fail closed.** A roster package missing a required file must fail with a
message naming the file, not degrade to the built-in roster.

## 8. Environments

Local developer checkouts, the pip/pipx `cadre` distribution, and the packaged
`plugin/` marketplace distribution. No persistent environment, no production
system, and no deployment is involved. This repository runs no `.agentic-sdlc/`
overlay of its own (`CLAUDE.md:20,81`, `AGENTS.md:57-58`, confirmed: no
`.agentic-sdlc/` directory exists), so these artifacts live under
`roster/orchestration/runs/<task-id>/` per
`.agents/skills/run-agent-orchestration/SKILL.md:92`.

## 9. Relationship to the parked proposal

`docs/proposals/governance-as-product-2026-08.md` states the same proposition and
was **explicitly parked by the Product Owner on 2026-08-09**. Bringing it forward
is a Product Owner decision, not this record's to make — that is OD-1.

Its five "what would have to be true" conditions, against this scope:

| Condition | Disposition |
| --- | --- |
| 1. `provider.json` becomes a real third-party contract | **No — deliberately not.** OD-5 (§16) resolved the opposite of what this row said at Revision 1: `provider.json` is left **untouched**, and roster identity goes into a sibling `roster.json` the kernel never reads. Extending it would have meant editing the kernel's closed `allowed_manifest_keys` inside a change whose constraint is not to. The proposal's condition is therefore *not* met, and that is the chosen answer rather than a shortfall. |
| 2. The selector runs against a roster it did not ship with | **Directly in scope.** The proposal names this the cheapest falsification and says it should be attempted before anything is moved. This record agrees. |
| 3. A second roster must exist | **Directly in scope**, as a minimal test fixture. |
| 4. The distribution story must survive | **Deferred.** Constraint C1/C2 preserve today's story; the "what does a user install first" question is not answered. |
| 5. The boundary test must be extended | **Directly in scope.** |

Two corrections to the proposal, recorded rather than silently applied:

- **It says 74 roles** — at lines 24, **73**, and 126. The catalog holds **159**.
  Its argument is unaffected; its numbers are stale. (Revisions 1–2 listed only
  two of the three sites, so a reader correcting from this note would have left
  one behind.)
- **Its platform/roster table is missing the context store**, which did not exist
  when it was written. `roster/context-store/` landed 2026-08-11 and is platform
  machinery inside `roster/` by the proposal's own criteria — a second instance
  of the accumulation it warned about, arriving two days after the warning.

Per `test_repository_health.py:2154`, `docs/proposals/` holds *"point-in-time
decision records, never revised once decided."* This record therefore cites and
supersedes that document rather than editing it.

## 10. Assumptions

- **A1.** The kernel's provider interface is sufficient for a foreign roster
  without modification. Grounded in `load_provider()` and the working
  `agentic-sdlc-defaults` provider, but **never tested with a roster carrying
  role prose that the kernel did not generate**.
- **A2.** `provider.json` can absorb roster-side entries without disturbing
  kernel validation. **This is the assumption most likely to be wrong.** The key
  set is closed and duplicated in two independent implementations —
  `allowed_manifest_keys` (a local in `kernel/agentic_sdlc/__init__.py:197`) and
  `_ALLOWED_MANIFEST_KEYS` (a module constant in
  `engine/agentic_sdlc_langgraph/provider.py`) — and an unknown key raises
  *"provider manifest contains unknown fields"* (`__init__.py:199-200`). Adding a
  roster-side key therefore **requires a coordinated change to two codebases,
  one of which is the kernel this work is meant not to disturb.** That cost is
  the substance of OD-5.
- **A3.** No second roster exists anywhere today. A plugin architecture with
  exactly one plugin has never been tested, so every assumption the reference
  roster happens to satisfy is currently invisible.
- **A4.** The knowledge store's three `roster/shared/src/` imports can be
  declared platform-owned in place. If a genuine consumer later needs the store
  without Cadre at all, those four modules must move or be published — deferred.

## 11. Conflicts and decisions a reviewer should push back on

- **This work is against a standing "parked" decision.** The Product Owner
  deliberately deferred this on 2026-08-09, reasoning that *"the architecture
  already permits it, so nothing is degrading while it waits."* That reasoning is
  still largely sound. The counter-evidence is that the context store landed
  inside `roster/` two days later — the slow cost the proposal named is being
  paid at a measurable rate. A reviewer who thinks the deferral should stand
  should say so; this record does not overrule it (OD-1).
- **A sibling `roster.json` rather than extending `provider.json`.** The drafting
  session initially recommended the opposite, on the reasoning that reusing an
  already kernel-versioned manifest is cheaper. Reading
  `kernel/agentic_sdlc/__init__.py:197-200` reversed it: the key set is closed
  and duplicated in the engine, so "cheaper" would have meant editing the kernel.
  The cost of the reversal is a second manifest and a second thing to keep in
  step. A reviewer who would rather pay the kernel edit once — and get one
  manifest describing one bundle — should say so.
- **"Declare, don't move"** for `roster/shared/src/`'s platform modules. This
  keeps C1/C2 satisfied at the cost of leaving platform code in a directory named
  `roster/` — a seam that is real in test but invisible in the tree.
- **The minimal second roster is a test fixture, not a product.** It proves the
  mechanism and nothing about whether anyone would author a real one.

## 12. Success criteria (observable)

1. `cadre select --roster <fixture>` emits a plan validating against
   `roster/orchestration/selection.schema.json`, naming only fixture roles.
2. A roster package missing `catalog.yaml` or `routing.yaml` fails with a message
   naming the missing file — verified by a test that asserts the failure, not
   just the success.
3. `cadre sdlc --provider providers/agentic-sdlc-defaults/provider.json provider list`
   lists **only** that provider's profiles and does not error. Revisions 1–3
   asked only that the bundle "reach the kernel with that bundle loaded", which
   **passes on unmodified code** — the flag already works. The command above is
   the one that fails today, with
   `provider agentic-sdlc-defaults duplicates profile ids: ['generic']`.
4. `cadre knowledge context …` runs with `CADRE_ROSTER_ROOT` pointing at a
   directory containing no roster. (Also passes today — see `requirements.md`
   PP-FR-5, which is a regression pin rather than a change.)
5. A planted violation — a Cadre role id hardcoded into `select_agents.py` —
   **fails** `test_roster_boundary.py`. A guard that passes against a planted
   violation is this repository's most frequently rediscovered failure mode
   (`docs/proposals/durable-knowledge-capture-2026-08.md`), so non-vacuity is a
   criterion, not a nicety.
6. `cadre generate-plugin --output plugin --check` reports no drift, and
   `git status --porcelain` shows no change under `provider/roles/` or
   `roster/catalog.yaml`. **Changes under `plugin/` are expected and are
   enumerated in `requirements.md` PP-NFR-1** — two new files and the mechanical
   `plugin/suite/` mirror of the platform source this work edits. Anything
   beyond that list fails this criterion.

   Revision 1 wrote "no change under `plugin/`", Revision 2 narrowed it to one
   permitted file, and **both were unsatisfiable**: `plugin/suite/` bundles
   `select_agents.py`, `build_dispatch_plan.py`, and `settings.py`, every one of
   which this work edits. The criterion is delegated to PP-NFR-1 rather than
   restated here so the two cannot drift apart again.
7. No new copy of knowledge-store or selector code appears in `plugin/suite/`,
   `cadre_cli/_vendor/`, or the Cline rewrite table.

## 13. Open-decision register

| ID | Decision | Owner | Status |
| --- | --- | --- | --- |
| **OD-1** | Bring the parked `governance-as-product-2026-08` proposal forward, against the 2026-08-09 deferral? | Product Owner (`@deagy`) | **RESOLVED** — yes, by the G1 approval itself (§16). |
| **OD-2** | `roster.root` trust scope. | Product Owner | **RESOLVED** — project-local, overlay-style (§16). |
| **OD-3** | `provider.json`'s `"id": "cadre"` names the *roster*; `README.md` names the repository. If the platform is a distinct thing, one of them needs a new name. | Product Owner | **OPEN**, non-blocking. Recording is enough; renaming is out of scope (§6). |
| **OD-4** | Where does `roster/knowledge-store/AGENT.md` live? It is a *roster role definition* (`roster/catalog.yaml:596-597`) sitting inside what this work declares platform. | Engineering Lead | **OPEN**, non-blocking. The seam works either way; a tidiness call. |
| **OD-5** | Extend `provider.json` with roster-side keys, or add a sibling `roster.json` the kernel never reads? | System Architect | **RESOLVED** — sibling manifest (§16). |
| **OD-6** | Does the mirror boundary guard apply to `roster/context-store/` too? | Engineering Lead | **OPEN**, non-blocking. |
| **OD-7** | *New, raised by OD-2's answer.* Surfacing the resolved roster id + digest in the dispatch plan adds an emitted field, forcing `selection.schema.json` 6 → 7. Accept the bump, or surface the identity outside the plan? | System Architect | **OPEN — blocking for G2.** See §16. |
| **OD-9** | *New at Revision 4.* Removing the hardcoded `["code-reviewer"]` gate-reviewer default (`build_dispatch_plan.py:107`) **changes every lifecycle-aware Cadre plan's `support` list** — no gate contract declares `review_agents`, so it fires for all of them — and the golden corpus cannot see it (`test_selection_golden_corpus.py:135` patches lifecycle contracts to `None`). Move the default into roster-declared data (recommended: Cadre's plans stay byte-identical), accept the output change and re-baseline, or leave `:107` alone and narrow PP-FR-6? | Product Owner + Engineering Lead | **OPEN — blocking for G2.** Option 2 alters published dispatch output. |
| ~~**OD-8**~~ | ~~Does a foreign roster need a route → workflow mapping, or the `workflow` enum opened?~~ | System Architect | **WITHDRAWN at Revision 3 — the premise was wrong.** Raised one revision earlier on a misreading of `_select_workflow()`: its final stage (`build_dispatch_plan.py:255-265`) does **not** branch on Cadre route ids. It reads each matched route's own declared `workflow_shape` from `routing.yaml` — a four-value field the roster supplies (`routing.schema.json:193-201`) — so a fixture roster whose routes declare it reaches `new-service` / `infrastructure-change` / `pipeline-change` today, with no code change and no enum bump. Kept struck through so a reader of Revision 2 finds out what happened to it. |

## 14. Knowledge retrieval status

**Performed, and it behaved correctly. This section has been wrong twice and is
rewritten a third time; both earlier readings are recorded below, because the
way they were wrong is more useful than the conclusion.**

*What was run.* Query ID `2ab218a2b25bba60`, retrieved 2026-08-11T21:30:42Z,
agent `product-intent-agent`, classification `internal`, project-local tier
(`.agents/knowledge-store/config.json`). One result returned, score **0.144** —
a sample record about Docker Compose volume layout and PostgreSQL 18 mount
paths, with no bearing on this work and not relied upon.
`untrusted_instruction_risk: false`. Its identifier is deliberately not written
out: `test_repository_health.py::test_sample_references_are_limited_to_allowed_archives`
fails any tracked file outside the allowlist naming the sample task id
literally, the same accommodation `docs/proposals/governance-as-product-2026-08.md`
and the 2026-08-08 sibling record both make.

*What Revision 1 said, and why it was wrong.* It reported the one hit as *"the
single committed sample record"* in the store and concluded **"there is
essentially no knowledge in the store to be unavailable."** The committed export
at `roster/knowledge-store/proposed-knowledge/` holds **twelve records** dated
2026-07-21 through 2026-08-09, **two** of them `status: "accepted"`
(`KS-20260808-glob-regex-asymmetries`, `KS-20260809-non-vacuity-fault-injection`).
"Essentially no knowledge" was not a description of anything that had been
checked.

*What Revision 2 said, and why it was also wrong — more confidently.* Having
found twelve records, it concluded **"this is a retrieval failure, not an empty
store,"** on the reasoning that the store held the answer and did not return it.
Two of those records are squarely on this work's own subject matter:

- `KS-20260809-a-single-maintainer-repository-cannot-re-16ef457779e1` — *"a
  single-maintainer repository cannot require pull-request approvals,"* the
  obstacle to G2's two-authority requirement that §15 and `requirements.md` §7
  reason out from scratch.
- `KS-20260809-non-vacuity-fault-injection` (`accepted`) — *"prove a guard is
  non-vacuous by injecting a fault,"* which is the whole of PP-NFR-4.

**But those records were never retrievable, and that is by design.** They are
*staged*, not ingested. Staged records live in the `staged_records` table
(`roster/knowledge-store/src/staged_store.py:44`); `search_store()`
(`service.py:121-129`) scores only rows from `load_chunks()` — the `chunks`
table, populated by ingestion. `.agents/skills/run-agent-orchestration/SKILL.md:94`
says it outright: staging *"is not ingestion, **confers no retrievability**, and
is not approval."* A steward disposition is what moves a record across that
line. So the retrieval did exactly what it should have: it searched the ingested
corpus and returned what was in it.

*The stale citation, corrected.*
`docs/proposals/durable-knowledge-capture-2026-08.md:38` says the store *"has
never received one"* durable finding. True when written, superseded by #180,
which made `proposed-knowledge/` a generated export of the **staged** store
(`proposed-knowledge/README.md`). Revision 1 quoted it to corroborate emptiness;
Revision 2 cited #180 to refute it. Both overshot: #180 built the *capture*
half, and capture is working — twelve records in three weeks.

*What is actually true, stated once.* **Capture works; the pipeline stops at
staging.** Twelve records were captured, two were dispositioned `accepted`, and
none of the twelve is reachable by a query, because nothing ingested them.
Retrieval is not broken and the store is not empty — the two findings this work
re-derived from scratch were written down by someone who had learned them, and
then sat one steward action away from being usable. That is a narrower and more
fixable gap than either earlier revision described, and it is the one worth
filing.

*Consequence for this record.* None of its claims changes. Every one is grounded
in direct reading of the tree at the cited `file:line`, which is what makes it
checkable — and that, rather than an assertion about what the store did or did
not hold, is what it rests on. See **G-7** in `requirements.md` §6.

## 15. Handoff

**To:** the human Product Owner (`@deagy`, per `.github/CODEOWNERS` and
RUNBOOK:730), for a **G1** decision. **Discharged — see §16.**

**Then to:** `requirements-agent`. The sibling `requirements.md` in this
directory was drafted at Revision 1 **in anticipation**, not on the strength of a
G1 approval; `roster/workflows/product-intake.md` step 4 places requirements
decomposition *after* G1, and this ordering is disclosed rather than concealed.
The approval in §16 arrives after the fact and does not retroactively make the
sequencing correct — it makes the baseline reviewable, which is a weaker claim.
Per that workflow, objective conflicts return to G1 rather than proceeding.

**G2 is not approved.** It requires `product_owner` **and** `engineering_lead`
(`kernel/contracts/lifecycle-gates.json`), and OD-7 is blocking for it.

## 16. Decisions taken

Recorded per the house pattern (`docs/proposals/durable-knowledge-capture-2026-08.md`'s
"Decisions taken"), so a later reader does not reopen them as oversights.

**G1 — Intent: APPROVED.** By `@deagy`, Product Owner per `.github/CODEOWNERS`
and `roster/RUNBOOK.md:730`, on 2026-08-11. Approving the intent gate *is* the
answer to OD-1: the 2026-08-09 deferral is reversed and this work proceeds.

**What this approval is not.** This repository runs no `.agentic-sdlc/` overlay
and holds no run records — verified: no `.agentic-sdlc/` directory, no
`run-record.json` anywhere in the tree. So there is no kernel gate state to
transition and no `agentic-sdlc decide` invocation behind this line. It is a
**prose record of a human decision, transcribed by the authoring agent**, and it
carries exactly the weight of that: it is not a countersigned approval, not
gate evidence in the kernel's sense, and not verifiable by any check in this
repository. Anyone relying on it should read it as the Product Owner's recorded
intent, which is what it is.

**OD-2 — `roster.root` is project-local, on the overlay pattern.** A project may
point at its own roster via `.agents/cadre.yaml`, under the fail-closed
discipline `roster/orchestration/src/routing_overlay.py` already establishes for
project-local routing overlays. The security objection recorded at §13's original
OD-2 — a project-local file "arrives with `git clone` and is editable by anyone
who can open a pull request" (`settings.py:681-689`) — is answered by *visibility
rather than prohibition*: **the resolved roster's id and digest surface in the
dispatch plan**, so a redirected roster is legible to a human reading the plan
rather than silent. Global-only was rejected because it would have removed most
of the feature; unrestricted project-local was rejected because it makes the
redirect invisible.

**OD-5 — a sibling `roster.json` the kernel never reads.** Chosen precisely
because it needs **zero** change to `kernel/` and `engine/`, which is what keeps
the "leave Cadre alone" constraint (§7 C1) true rather than aspirational.
Extending `provider.json` would have meant editing the kernel's closed
`allowed_manifest_keys` (`__init__.py:197`) and the engine's duplicate
`_ALLOWED_MANIFEST_KEYS`. The cost accepted: two manifests describing one bundle,
and a second thing to keep in step.

**OD-7 — raised by OD-2's answer, and left open.** Surfacing the roster id and
digest in the dispatch plan **adds an emitted field**, which forces
`selection.schema.json` from `const: 6` to `7`. That directly contradicts
`requirements.md`'s PP-NFR-3 ("no bump"), which was written before OD-2 was
answered. The contradiction is recorded rather than resolved by quietly amending
one side: the schema is closed *and* vendored away from its producer into both
the wheel and `plugin/`, so a pinned consumer copy rejects any plan carrying an
unknown property while truthfully reporting the version it handles. The bump is
therefore not cosmetic — it is what converts a silent rejection into an error
naming the real cause. **Blocking for G2**, because it changes a published
contract and that is not the authoring session's call.
