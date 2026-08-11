# Implementation Plan — A roster-neutral platform

**Plan ID:** `PLAN-CADRE-PORTABLE-PLATFORM`
**Revision:** 1 (initial)
**Status:** draft — **not scheduled.** Blocked on OD-1, OD-2, OD-5.
**Date:** 2026-08-11
**Implements:** `REQ-CADRE-PORTABLE-PLATFORM` (`requirements.md`, Revision 1)
**Decomposes:** `INTENT-CADRE-PORTABLE-PLATFORM` (`product-intent.md`, Revision 1)

---

## 0. Read this first

Three things determine whether this plan is worth executing at all, and none of
them is an engineering question:

- **OD-1** asks whether to reverse a standing Product Owner deferral (2026-08-09).
- **OD-2** fixes the `roster.root` trust scope. Phase A cannot start without it.
- **OD-5** fixes the manifest shape. Phase A's design depends on it.

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
  `context_store.home` (`:690-697`). Copy the shape of `knowledge_store.home`
  (`:673-680`); the scope value is OD-2's answer, not the implementer's choice.
  If OD-2 says `SCOPE_GLOBAL_ONLY`, carry over the reasoning comment at
  `:681-689` rather than writing a new one.
- New `roster/orchestration/roster.schema.json` + a `roster.json` at
  `provider/roster.json`. Validated by the existing
  `roster/orchestration/src/schema_validate.py` and its pre-commit hook — no new
  validation machinery.
- `roster/orchestration/src/select_agents.py` — `:16-18` constants become a
  resolver call; `:203` (`catalog_path`) and `:204` (`routing_path`) consume it;
  add `--roster`.
- `roster/orchestration/src/build_dispatch_plan.py` — `:29-30` constants;
  `:604` (`path = ROSTER_ROOT / definition`, context-pack definitions).

**Reuse, do not reimplement:** path containment already exists twice —
`kernel/agentic_sdlc/__init__.py:159-169` (`provider_resource()`, the exact
"escapes its manifest directory" check this needs) and
`roster/orchestration/src/glob_containment.py`. The kernel's is the closer
semantic match but is across the boundary, so port the *logic*, not an import —
`test_kernel_boundary.py:76-95` forbids importing kernel code and that guard must
keep passing.

**The trap.** Default resolution must be byte-identical to today. The golden
corpus (`roster/orchestration/test/fixtures/selection_golden_corpus.json`, 195 KB,
~60 hand-maintained blocks, **no generator script** — the loop is run-test,
read-diff, hand-edit) is the detector. If it needs editing, default behaviour
changed and the change is wrong.

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

Three acceptance cases, the third being the one that usually gets skipped:
plan-is-valid, no-match-returns-`needs-triage`, and missing-`catalog.yaml`-fails-
by-name.

### Phase C — The mirror boundary guard (PP-FR-6)

**Files:** new `roster/orchestration/test/test_roster_boundary.py`.

**Model it on `roster/orchestration/test/test_context_boundary.py:157-215`**,
which already does this exact job for the knowledge/context store pair — both the
import check and the string-literal check. Carry over its **self-vacuity guard**
at `:150-155` (assert the directories exist and contain modules), so a rename
cannot make every check pass over an empty set.

Runs after B deliberately: before a second roster exists there is no way to tell
a guard that works from a guard that cannot fail.

### Phase D — Knowledge-store path resolution (PP-FR-5)

**Files:** `roster/orchestration/src/build_dispatch_plan.py:29` and `:501`.

`:501` emits an absolute path into every plan's
`knowledge_context.requests[].invocation.args`, and
`cline-plugins/cline-agents/index.ts:247-259` executes it. **The emitted shape
must not change** — only how the path is computed. A changed shape is a
cross-language breaking change and a `selection.schema.json` bump (PP-NFR-3
forbids one).

`roster/knowledge-store/src/` itself needs **no edit**: it is already stdlib-only
and roster-free (`requirements.md` §0.4). The three `sys.path.append` sites into
`roster/shared/src/` stay; Phase C's guard converts "these four modules are
platform" from convention into a test.

### Phase E — `cadre sdlc --provider` override (PP-FR-4)

**Files:** `bin/cadre.py:124`.

The smallest phase. The kernel needs nothing: `--provider` is already a
repeatable global option, `load_provider()` (`__init__.py:192-304`) already
validates a foreign manifest completely, and `providers/agentic-sdlc-defaults/`
is already a working second provider. Keep `provider/provider.json` as the
default so `cadre sdlc` with no flag passes a byte-identical argument vector.

Last because it is independent of A–D and its value is realised only once
something else can supply a foreign bundle.

## 2. Verification

Per phase, and again at the end:

```sh
python3 -m unittest discover -s roster/orchestration/test -p "test_*.py"
python3 -m unittest discover -s roster/knowledge-store/test -p "test_*.py"
python3 -m unittest discover -s roster/shared/test -p "test_*.py"
python3 -m unittest discover -s plugin/tools -p "test_*.py"
python3 -B -m unittest discover -s kernel/test -p "test_*.py"
./bin/cadre generate-plugin --output plugin --check
git status --porcelain    # nothing under plugin/, provider/roles/, catalog.yaml, routing.yaml
```

**Regeneration.** Phases A–E touch `roster/*/src/` and `bin/`, which
`plugin/suite/` bundles — so the full four-step sequence in `CLAUDE.md` /
`roster/RUNBOOK.md` §17 applies, with `git add` of new files **first**. Phase B's
fixture lives under `test/`, which is not bundled, but the new `roster.schema.json`
and `provider/roster.json` are packaging-relevant and must be checked.

**Non-vacuity (PP-NFR-4), recorded in the PR.** For each new guard: plant the
defect, confirm it **fails** naming the real cause, revert, confirm clean.
- Phase A/B: remove `catalog.yaml` from the fixture → error names the file.
- Phase B: a task matching nothing → `needs-triage`, not a Cadre role.
- Phase C: hardcode a Cadre role id in `select_agents.py` → boundary test fails.
- Phase C: point the module lists at an empty directory → self-vacuity guard fires.

## 3. Sequencing and stop points

| After | Delivered | Safe to stop? |
| --- | --- | --- |
| A | Roster root resolvable; default unchanged | Yes — a latent capability, no behaviour change |
| B | **Proof the seam is real** | Yes, and this is the natural stop if the answer is "it isn't" |
| C | Regression protection for A+B | Yes |
| D | Knowledge store roster-independent in fact, not just in principle | Yes |
| E | Kernel reachable with a foreign bundle | Complete |

## 4. What this plan does not do

- No git repository split, no directory move, no rename.
- No kernel edit. **If an implementation finds itself editing `kernel/`, OD-5 was
  answered the other way and `requirements.md` must be revised before proceeding**
  — not patched around in the implementation.
- No `selection.schema.json` bump (PP-NFR-3).
- No fix for G-1 (`aides.yaml` authority duplication), G-2 (stray-copy check
  covers one contract file), or G-4 (`sample-selection-output.md` drift guard).
  All three are real, all three are adjacent, all three deserve their own change.
- No G1 or G2 approval. `@deagy` decides both.
