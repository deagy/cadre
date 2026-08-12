# Implementation Plan — A roster-neutral platform

**Plan ID:** `PLAN-CADRE-PORTABLE-PLATFORM`
**Revision:** 5
**Status:** draft — **not scheduled.** G1 approved 2026-08-11; blocked on OD-7,
**OD-9**, and G2.
**Date:** 2026-08-11
**Implements:** `REQ-CADRE-PORTABLE-PLATFORM` (`requirements.md`, **Revision 5**)
**Decomposes:** `INTENT-CADRE-PORTABLE-PLATFORM` (`product-intent.md`, **Revision 4**)
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

---

## 0. Read this first

Three decisions that gated this plan at Revision 1 were taken by the Product
Owner on 2026-08-11 (`product-intent.md` §16):

- **OD-1 — resolved.** The 2026-08-09 deferral is reversed; this proceeds.
- **OD-2 — resolved.** `roster.root` is **project-local, overlay-style**, on the
  `routing_overlay.py` precedent, *conditional on the resolved roster's id and
  digest surfacing in the dispatch plan* (PP-FR-1b). The visibility is the
  control that made project-local acceptable — it is not an optional extra to be
  dropped if Phase A runs long.
- **OD-5 — resolved.** A **sibling `roster.json`** the kernel never reads.

**One new blocker was created by OD-2's answer.** Surfacing roster identity in
the plan is a new emitted field, forcing `selection.schema.json` 6 → 7
(PP-NFR-3b). That is a change to a published, vendored contract, it is **OD-7**,
and it blocks G2. Phases A–B can be built and reviewed before it is settled;
they must not be merged with a bumped schema until it is.

**Revision 3 raised a second blocker (OD-8) and Revision 4 withdrew it.** It
claimed `_select_workflow()` classifies by Cadre route id, so a foreign roster
would always get `"unclassified"`. Its final stage (`build_dispatch_plan.py:255-265`)
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

G2 itself remains unapproved and requires `product_owner` **and**
`engineering_lead`.

The phase order is chosen so the **cheapest falsification comes first**, which is
also `docs/proposals/governance-as-product-2026-08.md`'s own recommendation:
*"condition 2 is the cheapest way to find out whether the rest is real, and
should be attempted before anything is moved."* If Phase A and B do not work,
nothing later is worth doing, and nothing has been moved.

Each phase is independently shippable and leaves the tree better than it found
it, so the sequence can stop at any point without stranding work.

## 1. Phases

### Phase A — Roster-root resolution (PP-FR-1, PP-FR-2)

**Files:**
- `roster/shared/src/settings.py` — new `roster.root` FieldSpec after
  `context_store.home` (`:690-697`), at **project tier**, per OD-2, with
  **`default_computed`** (the `agentic_sdlc.bin_path` form at `:665-672`), not
  `default_static=None`. It will be the only path-like setting in the file that
  is not `SCOPE_GLOBAL_ONLY`, so write a comment saying *why*. **Follow
  `context_store.home`'s comment at `:681-689`** — it is the only one of the
  three siblings that carries its reasoning (`agentic_sdlc.bin_path` and
  `knowledge_store.home` have none), and it is the one whose objection this
  setting has to answer. Point it at PP-FR-1b: the redirect is permitted because
  it is made visible, not because the objection stopped applying.
- New `roster/orchestration/roster.schema.json` + a `roster.json` at
  `provider/roster.json`. Validated by the existing
  `roster/orchestration/src/schema_validate.py` and its pre-commit hook — no new
  validation machinery. **Also add `"roster.json"` to `PROVIDER_BUNDLE`**
  (`generate_global_plugin.py:101`) — see §2, and PP-FR-2 for why this is the
  side of the PP-NFR-1 collision that gives.
- Roster identity in the plan (PP-FR-1b) + `selection.schema.json` 6 → 7
  (PP-NFR-3b). **Gated on OD-7.** Build it behind the decision; do not merge the
  bump until OD-7 is answered.
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
- **`roster/orchestration/mcp/dispatch_core.py:56` and `mcp/dispatch_server.py:63`
  — the second selection entry point, missing from Revisions 1–4.**
  `cadre mcp-dispatch-server` resolves `catalog.yaml` and `routing.yaml`
  checkout-relative and entirely independently of `select_agents.py`. Left
  alone, `--roster <fixture>` redirects `cadre select` while the MCP server
  keeps serving Cadre's roles — two dispatch surfaces disagreeing about which
  roles exist, silently.
- `roster/orchestration/src/schema_validate.py` — `:329-332` hardwires two
  instance/schema pairs; `roster.json` needs a third. Small, but it is a fourth
  file mirrored into `plugin/suite/` (see §2).

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

**This is the falsification step.** If a foreign roster cannot produce a plan,
the seam is theoretical and Phases C–E should not be attempted.

Four acceptance cases, the third being the one that usually gets skipped:
plan-is-valid, no-match-returns-`needs-triage`, missing-`catalog.yaml`-fails-
by-name, and **a matching fixture task classifies to a `workflow` other than
`unclassified`** (PP-FR-3(d)). Give the fixture's routes a `workflow_shape` and
the fourth passes without touching `_select_workflow()` — it is a regression
pin, not a falsification, and it is worth having for exactly that reason: it is
the property most likely to be lost silently while the fixture is edited for
some other purpose.

### Phase C — The mirror boundary guard (PP-FR-6). **Blocked on OD-9.**

**This phase has been mis-sized in every revision, in both directions, and is
now the largest one here.** Revisions 1–2 listed only the new test. Revision 3
added two fixes, one of which was not a defect. Revision 4 cut back to "one
small fix." Running the selector shows that fix changes every lifecycle-aware
Cadre plan, and scanning the modules by PP-FR-6's own method finds roughly nine
more violations.

**Files:** new `roster/orchestration/test/test_roster_boundary.py`, a new
lifecycle-aware selection test (see below), **and** the category-B path fixes
across `select_agents.py`, `routing.py`, `routing_overlay.py`,
`mcp/dispatch_core.py`, `mcp/dispatch_server.py`, and
`build_dispatch_plan.py`.

**Model the test on `roster/orchestration/test/test_context_boundary.py:157-215`**,
which already does this exact job for the knowledge/context store pair — both the
import check and the string-literal check. Carry over its **self-vacuity guard**
at `:150-155` (assert the directories exist and contain modules), so a rename
cannot make every check pass over an empty set. Add the PP-FR-1 assertions that
`select_agents.py:18` and `:24` do not resolve through `roster.root`.

**The violations, by category (`requirements.md` PP-FR-6 has the full table):**

- **A — one, and it is blocked on OD-9.** `build_dispatch_plan.py:107`'s
  `["code-reviewer"]` default. No gate contract declares `review_agents`, so it
  fires for every gate and lands in `support` (`:673`). Confirm before touching
  it: `./bin/cadre select --task "Update the OpenTofu module for the VPC" …`
  currently yields `support: [product-intent-agent, requirements-agent,
  code-reviewer]`. Removing the hardcode drops the third entry from **every**
  lifecycle-aware plan. **Do not "just fix it" — that is the change OD-9 exists
  to decide.**
- **B — roughly nine, and this is the phase's real body.** Hardcoded
  `catalog.yaml` / `routing.yaml` / `roster/`-relative paths in
  `select_agents.py:203-204`, `routing.py:154,198,209,212,279`,
  `routing_overlay.py:97,102`, `mcp/dispatch_core.py:56`,
  `mcp/dispatch_server.py:63`. PP-FR-2 already prescribes the fix: these come
  from `roster.json`. This is what makes the manifest load-bearing rather than
  decorative.
- **C — permitted, and the guard must say so by rule.** `roster/`-relative paths
  inside user-facing error strings (`knowledge-store/src/staged_records.py:141,147`,
  `finding_record.py:139`, `config.py:79,136,183`). Exempt literals reachable
  only from diagnostics; **do not** write a file allowlist, which is how a guard
  quietly stops meaning anything.

**A second detector is required, not optional.** The golden corpus cannot see
category A: `test_selection_golden_corpus.py:135` patches
`try_lifecycle_contract` to `None`, so `_gate_agents()` never runs and none of
the 175 cases carries `code-reviewer` in `expected.support`. Add a
lifecycle-aware selection test that pins the `support` list for at least one
gate-bearing task with the in-tree kernel resolvable. Without it, PP-NFR-1
asserts an invariant it cannot check — while citing the corpus as proof.

**Not a violation, though Revision 3 said it was:** `_select_workflow()`. Its
final stage reads roster-declared `workflow_shape`; the earlier precedence
branches (`:151-196`) do name Cadre *route* ids, but route ids are not on
PP-FR-6's forbidden list and `routing.schema.json:193-201` documents that split
deliberately. A foreign roster cannot reach `rollback` or `production-release`
— a real limitation, recorded in `requirements.md` PP-FR-3, not work in this
phase. **Do not widen the `workflow` enum.**

Runs after B deliberately: before a second roster exists there is no way to tell
a guard that works from a guard that cannot fail.

### Phase D — Knowledge-store path resolution (PP-FR-5)

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

### Phase E — Let `cadre sdlc` run *without* Cadre's provider (PP-FR-4)

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
3. **`plugin/suite/roster/orchestration/selection.schema.json` — modified**, by
   the 6 → 7 bump, through that same prefix copy. Revision 4 cited the copy as
   the reason the file is already present, in the sentence before omitting it.
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
- Phase D: a scratch branch routing `build_dispatch_plan.py:29` through the
  roster resolver → the `cli.py`-exists assertion fails. Phase D changes no
  behaviour, so this run is the only thing distinguishing its tests from two
  assertions that have always held.
- Phase C: hardcode a Cadre role id in `select_agents.py` → boundary test fails.
- Phase C: the lifecycle-aware detector must **fail** when `:107`'s default is
  removed without OD-9's chosen compensation. If it passes either way it is
  measuring nothing, which is exactly the state the golden corpus is in.
- Phase E: run `./bin/cadre sdlc --provider providers/agentic-sdlc-defaults/provider.json provider list`
  **before** the change and record the `duplicates profile ids` error. That
  error is the requirement; without it the acceptance passes on unmodified code.
- Phase C: point the module lists at an empty directory → self-vacuity guard fires.
- Phase C: point `_SHARED_SRC_DIR` at the resolved roster root → the PP-FR-1
  assertion fails. This is the one that matters most: it is the assertion
  standing between OD-2's project-local scope and a `.agents/cadre.yaml` that
  chooses which `settings.py` the platform executes.

## 3. Sequencing and stop points

| After | Delivered | Safe to stop? |
| --- | --- | --- |
| A | Roster root resolvable; default unchanged | Yes — a latent capability, no behaviour change |
| B | **Proof the seam is real** | Yes, and this is the natural stop if the answer is "it isn't" |
| C | Boundary guard, ~9 category-B path fixes, the lifecycle-aware detector, and (pending OD-9) the `code-reviewer` default | **Only after OD-9.** This is the one phase that can change default Cadre selection, so "safe to stop" depends on which OD-9 option was taken |
| D | Knowledge store roster-independent in fact, not just in principle | Yes |
| E | Kernel reachable with a foreign bundle | Complete |

## 4. What this plan does not do

- No git repository split, no directory move, no rename.
- No kernel or engine edit. **If an implementation finds itself editing
  `kernel/` or `engine/`, it has silently reversed OD-5** — stop and revise
  `requirements.md`, rather than patching around it in the implementation.
- ~~No `selection.schema.json` bump.~~ **Reversed at Revision 2**: OD-2's
  disposition forces 6 → 7 (PP-NFR-3b), gated on OD-7.
- No fix for G-1 (`aides.yaml` authority duplication), G-2 (stray-copy check
  covers one contract file), or G-4 (`sample-selection-output.md` drift guard).
  All three are real, all three are adjacent, all three deserve their own change.
- No G1 or G2 approval. `@deagy` decides both.
- No resolution of **OD-9**. Whether Cadre's `support` lists may lose
  `code-reviewer` is a Product Owner / Engineering Lead decision, not an
  implementation detail to be settled by whoever reaches Phase C first.
