# Product Intent Record — A roster-neutral platform: separating the knowledge store, the roster, and the lifecycle

**Intent ID:** `INTENT-CADRE-PORTABLE-PLATFORM`
**Revision:** 1 (initial)
**Status:** draft — awaiting human review
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

`cadre sdlc` always injects *this* repository's bundle, with no override. The
underlying `agentic-sdlc` binary accepts `--provider` as a repeatable global
option; only the Cadre wrapper hardcodes it.

**"Separate the knowledge store."** Also already true, and more so than any
document claims. `roster/knowledge-store/src/` is stdlib-only, reads neither
`catalog.yaml` nor `routing.yaml`, and has exactly three outbound imports, all
into `roster/shared/src/` (`settings`, `content_protection` + `text_chunking`,
`text_embedding`). Most decisively: **the `--agent` argument is not validated
against any roster and selects nothing.** `build_agent_context()`
(`service.py:164-167`) checks only that it is non-empty; the value then appears
only in the retrieval audit row (`:176`) and echoed back in the returned bundle
(`:184`). It is a label, not an access-control key. The store has no principal,
no identity store, and no role-to-classification mapping — `SECURITY.md:48` says
so: *"caller flags are not authentication."*

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
   provider bundle.
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
| 1. `provider.json` becomes a real third-party contract | **Partially.** Extended with roster-side entries; a full third-party contract specification is not attempted. |
| 2. The selector runs against a roster it did not ship with | **Directly in scope.** The proposal names this the cheapest falsification and says it should be attempted before anything is moved. This record agrees. |
| 3. A second roster must exist | **Directly in scope**, as a minimal test fixture. |
| 4. The distribution story must survive | **Deferred.** Constraint C1/C2 preserve today's story; the "what does a user install first" question is not answered. |
| 5. The boundary test must be extended | **Directly in scope.** |

Two corrections to the proposal, recorded rather than silently applied:

- **It says 74 roles** (lines 24 and 126). The catalog holds **159**. Its
  argument is unaffected; its numbers are stale.
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
3. `cadre sdlc --provider <other>/provider.json` reaches the kernel with that
   bundle loaded, confirmed via `agentic-sdlc provider list`.
4. `cadre knowledge context …` runs with `CADRE_ROSTER_ROOT` pointing at a
   directory containing no roster.
5. A planted violation — a Cadre role id hardcoded into `select_agents.py` —
   **fails** `test_roster_boundary.py`. A guard that passes against a planted
   violation is this repository's most frequently rediscovered failure mode
   (`docs/proposals/durable-knowledge-capture-2026-08.md`), so non-vacuity is a
   criterion, not a nicety.
6. `cadre generate-plugin --output plugin --check` reports no drift, and
   `git status --porcelain` shows no change under `plugin/`, `provider/roles/`,
   or `roster/catalog.yaml`.
7. No new copy of knowledge-store or selector code appears in `plugin/suite/`,
   `cadre_cli/_vendor/`, or the Cline rewrite table.

## 13. Open-decision register

| ID | Decision | Owner | Blocking? |
| --- | --- | --- | --- |
| **OD-1** | Bring the parked `governance-as-product-2026-08` proposal forward, against the 2026-08-09 deferral? | Product Owner (`@deagy`) | **Yes — blocks G1 itself.** Nothing below matters if the deferral stands. |
| **OD-2** | `roster.root` scope: `SCOPE_GLOBAL_ONLY` (mirroring `agentic_sdlc.bin_path`, so a cloned repo cannot redirect which role prose is dispatched) or project-local (so a project picks its own roster)? | Product Owner + Security Lead | **Yes.** These pull opposite ways: the whole feature is "a project uses its own roster," and the whole security precedent is "a project-local file must not choose what an agent executes." |
| **OD-3** | `provider.json`'s `"id": "cadre"` names the *roster*; `README.md` names the repository. If the platform is a distinct thing, one of them needs a new name. | Product Owner | No — recording is enough; renaming is out of scope (§6). |
| **OD-4** | Where does `roster/knowledge-store/AGENT.md` live? It is a *roster role definition* (`roster/catalog.yaml:596-597`) sitting inside what this work declares platform. | Engineering Lead | No — the seam works either way; it is a tidiness call. |
| **OD-5** | Extend `provider.json` with roster-side keys, or add a sibling `roster.json` the kernel never reads? Extending requires a coordinated change to the kernel **and** the engine (A2); a sibling manifest requires neither. Recommendation: sibling manifest. | System Architect | **Yes.** It determines Phase A's shape. |
| **OD-6** | Does the mirror boundary guard apply to `roster/context-store/` too? | Engineering Lead | No. |

## 14. Knowledge retrieval status

**Performed, and immaterial.** Query ID `2ab218a2b25bba60`, retrieved
2026-08-11T21:30:42Z, agent `product-intent-agent`, classification `internal`,
project-local tier (`.agents/knowledge-store/config.json`). One result returned,
score **0.144** — `SAMPLE-001-compose-runtime-lessons`, concerning Docker Compose
volume layout and PostgreSQL 18 mount paths. It has no bearing on this work and
is not relied upon. `untrusted_instruction_risk: false`.

This is itself corroborating evidence for
`docs/proposals/durable-knowledge-capture-2026-08.md`'s central observation: the
store *"has never received one"* durable finding, and a retrieval against a
central architectural question returns a single sample record about container
volumes. **No material knowledge was unavailable — there is essentially no
knowledge in the store to be unavailable.** Every claim in this record is
therefore grounded in direct reading of the tree at the cited file:line, not in
retrieval.

## 15. Handoff

**To:** the human Product Owner (`@deagy`, per `.github/CODEOWNERS` and
RUNBOOK:730), for a **G1** decision.

**OD-1 blocks the gate itself** — it asks whether a standing Product Owner
deferral should be reversed, which only the Product Owner may answer. OD-2 and
OD-5 are blocking for the requirements baseline but not for G1.

**Then to:** `requirements-agent`. The sibling `requirements.md` in this
directory is drafted at Revision 1 **in anticipation**, not on the strength of a
G1 approval; `roster/workflows/product-intake.md` step 4 places requirements
decomposition *after* G1, and this ordering is disclosed rather than concealed.
Per that workflow, objective conflicts return to G1 rather than proceeding.

Nothing in this directory is approved by its presence here.
