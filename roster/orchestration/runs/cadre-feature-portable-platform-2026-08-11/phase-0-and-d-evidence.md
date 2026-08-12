# Evidence — Phase D and the Phase 0 spike

**Date:** 2026-08-11
**Plan:** `implementation-plan.md` Revision 8, §1 Phases D and 0
**Status of the work:** Phase D landed. Phase 0 run and discarded, as designed.

Phase D owes PP-NFR-4 non-vacuity evidence, and the plan requires it be
*attached* rather than asserted — it is the one item an implementer could
otherwise claim while leaving nothing a reviewer can reproduce. Phase 0 owes an
answer rather than an artifact. Both are below.

---

## Phase 0 — falsification spike

Throwaway harness at a hand-built roster sharing no role id, phase, or keyword
with Cadre's (`widget-smith` / `widget-inspector` / `widget-scribe`; phases
`forge` / `assay` / `chronicle`), with one route declaring `quality_gates`. No
production file was changed. Nothing from it is merged.

### Result 1 — the seam is real for standalone selection

With lifecycle contracts forced to `None`:

```
status='ready'  workflow='new-service'  lifecycle='standalone'
primary=['widget-smith']  reviewers=['widget-inspector']  support=['widget-scribe']
leaked Cadre roles: NONE
schema_version=6  dispatch_fingerprint=sha256:ae68580f4...
selection.schema.json: VALID
```

A roster the binary did not ship with produces a schema-valid plan naming only
its own roles. **This is the answer Phase B was scheduled to obtain after Phase A
had shipped a resolver, a manifest, a schema, a packaging entry and eight
constant rewrites.** It cost a fixture directory and no production change.

It also settles `workflow` classification empirically: `new-service` comes from
the fixture's own declared `workflow_shape`, confirming OD-8's withdrawal was
correct and that `_select_workflow()`'s final stage is genuinely roster-driven.

### Result 2 — lifecycle-aware selection is blocked, exactly as OD-9 says

Same fixture, same task, with the kernel resolvable:

```
ValueError: Routing selected an unknown agent: code-reviewer
```

`build_dispatch_plan.py:547-551`, reached via the `["code-reviewer"]` default at
`:107`. **OD-9's premise is now observed rather than reasoned.** The plan does
not degrade; it raises and emits nothing.

Worth recording precisely because the records got here by reading: the claim was
correct. Phase 0's value was not catching an error — it was converting a
five-revision inference into a run.

### Result 3 — the finding nobody had

**`roster/orchestration/routing.yaml` is JSON, and the `.yaml` extension is a
lie.** `routing_overlay.py:502` parses the base routing file with
`json.loads(base_text)`. Cadre's file happens to be JSON-formatted, so nothing
has ever noticed.

The fixture was authored as real YAML — the obvious reading of a file called
`routing.yaml` — and the first run died:

```
json.decoder.JSONDecodeError: Expecting value: line 1 column 1 (char 0)
```

An error naming neither the file, nor the format, nor the requirement. A second
roster author hits this immediately and has nothing to go on.

**This was not reachable by reading.** Six revisions of these records cite
`routing.yaml` constantly; PP-FR-2 specifies a `routing` path in `roster.json`;
PP-FR-3 requires a fixture roster with "its own `routing.yaml`". None of it
noticed the format, because Cadre's own file satisfies an assumption nobody knew
was being made. This is assumption **A3** — *"a plugin architecture with exactly
one plugin has never been tested, so every assumption the reference roster
happens to satisfy is currently invisible"* — made concrete on the first run of
the first foreign roster this repository has ever had.

Filed as **G-12**. It is not fixed here: whether to parse YAML, rename the file,
or fail with a message naming the format is a real design call with a packaging
blast radius.

### Result 4 — the two dispatch surfaces resolve independently

`mcp/dispatch_core.py:56`'s `CATALOG_PATH` and the selector's catalog path
resolve to the same file today by separate expressions with no shared resolver.
Confirmed structurally; redirect one and the other does not move. This is
OD-10's observation, which survived OD-10's withdrawal and still binds PP-FR-6.

### What Phase 0 changes about the plan

- **Phase B's acceptance case (e) is vindicated and is not optional.** Without a
  fixture route declaring `quality_gates`, the spike would have reported "the
  seam is real" and stopped — Result 1 alone, with Result 2 invisible.
- **G-12 belongs in Phase A or B**, ahead of authoring the real fixture.
- **Phases A–C remain worth doing.** The mechanism works; one known blocker sits
  in front of it and OD-9 has already chosen the fix.

---

## Phase D — non-vacuity evidence (PP-NFR-4)

New guard: `roster/orchestration/test/test_knowledge_store_anchor.py`, 7 tests.
Clean run: `OK`. Full suite 1228 → 1235, no drift.

Two defects planted in `build_dispatch_plan.py`, each reverted, each confirmed
to fail **naming the real cause**.

### Injection 1 — the adjacent-line mistake

```diff
-KNOWLEDGE_STORE_ROOT = Path(__file__).resolve().parents[2] / "knowledge-store"
-ROSTER_ROOT = Path(__file__).resolve().parents[2]
+ROSTER_ROOT = Path(__file__).resolve().parents[2]
+KNOWLEDGE_STORE_ROOT = ROSTER_ROOT / "knowledge-store"
```

```
FAIL: test_knowledge_store_root_does_not_resolve_through_the_roster_root
AssertionError: 'ROSTER_ROOT' unexpectedly found in {'ROSTER_ROOT'} :
  KNOWLEDGE_STORE_ROOT is derived from ROSTER_ROOT. The knowledge store is
  platform-owned (PP-FR-6) and must not follow a resolved roster: a plan's
  emitted cli.py path would stop existing whenever roster.root points at a
  directory with no knowledge store. Re-derive it from Path(__file__).

FAIL: test_knowledge_store_root_is_anchored_on_this_files_location
AssertionError: '__file__' not found in {'ROSTER_ROOT'}

Ran 7 tests — FAILED (failures=2)
```

### Injection 2 — Phase A's resolver, taking `:29` with it

```diff
+import os as _os  # PLANTED DEFECT -- simulates Phase A's roster.root resolver
+ROSTER_ROOT = Path(_os.environ.get("CADRE_ROSTER_ROOT") or Path(__file__).resolve().parents[2])
+KNOWLEDGE_STORE_ROOT = ROSTER_ROOT / "knowledge-store"
```

```
FAIL: test_emitted_cli_path_survives_a_roster_root_pointed_elsewhere
AssertionError: False is not true : emitted CLI path stopped existing with
  CADRE_ROSTER_ROOT=/tmp/tmp8gzouby1: /tmp/tmp8gzouby1/knowledge-store/src/cli.py

Ran 7 tests — FAILED
```

**Injection 2 is the one that matters**, and it is what the plan predicted:
*"Phase D changes no behaviour, so this run is the only thing distinguishing its
tests from two assertions that have always passed."* The forward pin
(`test_emitted_cli_path_survives_a_roster_root_pointed_elsewhere`) passes
trivially today because nothing reads `CADRE_ROSTER_ROOT` yet. Injection 2
creates the reader and the assertion fires immediately — so the test is proved
non-vacuous *against the future state it exists to guard*, not merely against
the present one.

### Revert confirmed

```
git diff --stat roster/orchestration/src/build_dispatch_plan.py   # (empty)
Ran 7 tests — OK
```

---

## Filed, not fixed

- **G-12** — `routing.yaml` is JSON-parsed despite its extension
  (`routing_overlay.py:502`). A foreign roster written as YAML fails with a
  `JSONDecodeError` naming nothing useful. Design call with packaging blast
  radius; see Result 3.

---

## Phase C′-1 and C′-2 — non-vacuity evidence (PP-NFR-4)

New guard: `roster/orchestration/test/test_roster_boundary.py`, 14 tests.
Orchestration suite 1249 → 1263. Golden corpus untouched.

**Five defects planted, each reverted. The first run is the interesting one.**

| Plant | Result on first attempt |
| --- | --- |
| Cadre role id in `mcp/dispatch_core.py` | **PASSED — guard had a hole** |
| `catalog.yaml` literal in `routing.py` | failed correctly |
| `["code-reviewer"]` restored to `build_dispatch_plan.py` | failed correctly |
| `_SHARED_SRC_DIR` follows `ROSTER_ROOT` | failed correctly |
| `mcp/*` dropped from the platform list | failed correctly |

**The guard passed 12 of 12 tests with category A scoped to one module.** The
first draft asserted it only against `build_dispatch_plan.py` — where the known
defect lived — so a role id planted in `mcp/dispatch_core.py` went straight
through. That module is the one five revisions of the requirements baseline
forgot existed.

This is the "non-vacuous but incomplete" failure the guard's own self-vacuity
section warns about, committed inside the file that warns about it. Fault
injection is the only thing that found it: every test passed, the coverage was
wrong.

Fixed by checking category A across **all** platform modules, with role ids read
from `catalog.yaml` rather than hand-listed — so adding a role extends the check
and nobody has to remember to.

### Two further corrections the plants forced

**Substring matching produced a false positive.** The gate id
`halt-authority-determination` contains the role id `halt-authority` and has
nothing to do with it. A guard that cries wolf teaches its next reader to loosen
it rather than fix the code, so role ids now match as whole kebab-case tokens.
Filenames still match as substrings, which is correct for them.

**Two genuine violations surfaced once the false positive cleared.**
`build_dispatch_plan.py:354-355` emitted human-gate descriptions naming
`halt-authority` and `architecture-authority` — Cadre role names in text that
ships in every plan, including a foreign roster's. Fixed rather than exempted:
the descriptions instruct a human what to do, and the role attribution added
nothing.

### The fail-closed correction

An earlier draft of `mcp/dispatch_core.py` and `routing_overlay.py` caught a
manifest error at import and fell back to Cadre's hardcoded layout. **The guard
rejected it, and the rejection was right** — intent §7 C4 forbids degrading to
the built-in roster, and a fallback reproducing Cadre's directory layout is that
degradation wearing a robustness costume. Both now fail closed.

## The seam, end to end

```console
$ ./bin/cadre select --roster <fixture> --task "Forge a new sprocket flange assembly" …
status    : ready
workflow  : new-service
lifecycle : integrated
primary   : ['widget-smith']
reviewers : ['widget-inspector']
support   : ['widget-scribe']
LEAKED    : none
```

A foreign roster, with lifecycle contracts resolvable and a route declaring
`quality_gates`, now produces a valid plan naming only its own roles. Before
C′-2 the same command raised `ValueError: Routing selected an unknown agent:
code-reviewer`. **This is PP-FR-1's headline deliverable, met.**

Cadre's own output is unchanged: `support` still carries `code-reviewer` on
lifecycle-aware plans, the golden corpus is unedited, and the ~15
`test_selector.py` assertions did not move — which is what OD-9 option 1 was
chosen to guarantee.
