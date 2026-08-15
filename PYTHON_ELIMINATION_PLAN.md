# Python Elimination Plan

**Target: zero lines of Python in this repository.**

This is the sequencing document for that goal. `REMAINING_PYTHON_SCOPE.md`
records *what* is still Python and why; this records *in what order it goes,
what gates each step, and what breaks*.

Status as of 2026-08-15, measured (not estimated) against the tree:

| | production | tests |
|---|---:|---:|
| `roster/orchestration/src` | 10,314 | 26,941 |
| `roster/orchestration/mcp` | 6,822 | — |
| `roster/shared/src` | 4,347 | 3,440 |
| `roster/context-store/src` | 2,058 | 2,496 |
| `cadre_cli/` (PyPI entry point) | 500 | — |
| `kernel/agentic_sdlc` | 9,626 | 6,883 |
| `engine/agentic_sdlc_langgraph` | 5,995 | 5,933 |
| `plugin/tools` | — | 8,029 |
| **total** | **39,662** | **53,722** |

**93,384 lines.** `plugin/suite/` holds another 24,534 lines, but it is a
vendored copy of `roster/` produced by `generate-plugin` — it retires
automatically when its source does and is not separate work.

## The method, stated once

Every phase below uses the pattern that got `cadre select` across
(PR #274), because it is the only thing that reliably catches a port that
looks right:

1. **Build the gate before the port.** A differential harness that runs both
   implementations on the same inputs and compares outputs byte-for-byte.
2. **Port in increments, each measured against a space chosen to exercise
   *it*** — not against the corpus, which reaches only a handful of shapes.
3. **Falsify every gate.** Break the implementation deliberately; confirm the
   gate fails. A falsification that does not compile, or that patches a
   comment instead of the branch, is not evidence — discard it and redo it.
4. **Check the gate compares what ships.** Three bugs survived every parity
   number in #274 because the harness and the CLI fed the same code
   different value shapes, and because the one plan field excluded from the
   fingerprint was also excluded from the comparison.

Point 4 is the expensive lesson. Budget for it in every phase.

## What fixes the order

Three constraints, not preference:

- **Tests retire with their subject, not at the end.** A module's Python
  tests are replaced by Go tests as part of porting that module. The 53,722
  test lines are not a final phase — they are distributed across every phase.
  What *is* a final phase is the handful of repo-wide guards
  (`test_repository_health`, `test_kernel_boundary`, `test_cli_surface`)
  that check the repository rather than any Python in it.
- **The kernel handshake must survive the kernel port.** Go `select` calls
  `agentic-sdlc show-contract lifecycle-gates` and parses contract v2. A Go
  kernel must keep that CLI surface byte-compatible, or `select` breaks on
  the same day the kernel ships.
- **Three distribution channels, and a channel is only Python-free when all
  of them are.** Checkout and plugin are done. PyPI is not — and Phase 1's
  measurement showed the PyPI wheel is what keeps nearly all of `roster/`
  alive, so closing that channel is the precondition for deleting any of
  it, not merely an independent convenience.

---

## Phase 1 — Delete what is provably dead — **COMPLETE: nothing was**

**Result, measured 2026-08-15: zero deletable lines.** Every non-test Python
module in the checkout is reachable from a live entry point. Phase 1 is
closed without a deletion, and that is the finding rather than a failure to
find one.

`roster/orchestration/test/probe_python_reachability.py` is the instrument,
committed so every later phase can re-ask the question after the previous
phase changes the answer. It walks real entry points and follows real
imports; run it with `--production` to exclude tests and surface modules
that nothing but their own test uses.

**Why the yield is zero, and why that matters for the ordering:**

`cadre_cli/_SUBCOMMANDS` keeps almost all of `roster/` alive on its own. The
PyPI wheel dispatches to Python for `select`, `context`, `upgrade`, the
generators and more, so a module the checkout and plugin channels never
touch is still live for anyone who ran `pip install cadre`. **Phase 2 is not
an independent track that happens to be convenient — it is the precondition
for deleting any of `roster/`.** The plan's earlier claim that Phases 1 and 2
were independent was wrong, and this measurement is what corrected it.

**Three false-positive classes, each of which cost real time:**

- **Documented operator commands are entry points.** A first pass reported
  `routing_health.py` and `validate_runner_capabilities.py` as dead. Both are
  invoked by hand per `RUNBOOK.md`, and *neither has a Go equivalent* —
  deleting them would have removed capability, not duplication.
  `glob_containment.py` fell out of the same mistake, being imported by the
  first.
- **Data modules consumed by their own contract test are alive.**
  `plugin/tools/binary_platforms.py` is the platform-matrix source of truth
  cited in `DISTRIBUTION.md`; its test importing it is the guard working, not
  evidence of orphanhood.
- **A function can share a module's name.** `generated_package` matched
  dozens of times in `test_repository_health.py` — all of them a local
  function, none of them the module. Grep could not tell the difference.

A symbol grep had already wrongly flagged three files earlier in this
migration. That is now four separate occasions on which grep produced a
delete list that would have removed working code.

## Phase 2 — Close the PyPI channel

**Scope:** `cadre_cli/` (500 lines) and the vendored `roster/` Python the
wheel carries.

Today `pip install cadre` ships a pure-Python wheel with its own
`_SUBCOMMANDS` dispatcher, because — per `pyproject.toml:73` — a
pure-Python wheel cannot carry the Go binary. This is the only reason an
entire distribution channel is still Python.

**Method:** platform-tagged wheels containing the cross-compiled Go binary,
one per `(os, arch)`, with `cadre_cli` reduced to a launcher that `exec`s
it — or removed entirely in favour of a `[project.scripts]` shim. Already on
the roadmap as `DISTRIBUTION.md`'s v0.24.0+ item.

**Gate:** an install test per platform tag that runs `cadre select` from the
installed wheel and compares its plan against the checkout's, exactly as
`test_repository_health.py` already compares the wrapper against direct
invocation. The release workflow already cross-compiles; this extends it.

**Breaks:** nothing for users — `pip install cadre` keeps working and stops
needing a Python runtime for anything but the installer. The wheel build and
the release matrix both change.

**Why this early:** it depends on no other phase, and until it lands, "Cadre
is Python-free" is false for anyone who installed it the documented way.

## Phase 3 — Retire the `select` escape hatch

**Scope:** ~2,900 lines — `select_agents`, `build_dispatch_plan`, `routing`,
`routing_overlay`, `plan_text_format`, `route_near_miss`, `risk_classifier`,
`agentic_sdlc_contracts` — plus the Python half of the differential suite.

**Trigger, not a date.** Delete when all three hold:

1. Go `select` has been the default through at least one full release cycle.
2. No divergence has been reported that required `CADRE_SELECT_IMPL=python`.
3. The corpus has absorbed any input shape that *did* surface.

**Method:** the differential gate loses its second implementation, so
convert it to a golden gate. `select_golden.json` already stores portable
canonical plans for all 25 cases — those keep their teeth without Python.
What is genuinely lost is the ability to ask "what does Python do here?"
about a *new* input. Record that in the commit message rather than
discovering it later.

**Gate:** golden comparison for all 25 corpus cases, plus the discovery,
overlay, presentation and telemetry suites converted from cross-
implementation to golden assertions.

**Breaks:** `CADRE_SELECT_IMPL=python` stops working. It is undocumented
outside `select_agents.go`'s header, so the blast radius is this repo.

## Phase 4 — The rest of `roster/`

**Scope:** `roster/orchestration/mcp` (6,822), `roster/shared/src` (4,347),
`roster/context-store/src` (2,058), `plugin/tools` (8,029 — packaging and
the Cline mirror).

**Method:** ordinary ports, one module at a time, each behind its own
differential harness. Two have real surface area worth calling out:

- **`mcp/`** is invoked by Codex over stdio, not by the CLI, so its gate is
  a protocol-level conformance suite — same JSON-RPC in, same out — rather
  than an output comparison.
- **`plugin/tools`** generates committed content that CI already guards with
  `--check`. That guard *is* the differential gate: a Go port that produces
  byte-identical output passes it, and one that does not cannot merge.

**Gate:** per-module differential, plus the existing `--check` guards.

**Breaks:** nothing user-visible.

## Phase 5 — Kernel to Go, distributed as binaries

**Scope:** 9,626 production lines across 30 CLI subcommands, plus 6,883
lines of tests. The 10 JSON schemas under `kernel/contracts/` are **data,
not code** — they stay exactly as they are.

This is the largest single item and the one with a real consumer cost.

**Must survive the port, without exception:**

- `show-contract lifecycle-gates` byte-compatible at contract v2, because
  Go `select` parses it. Port this subcommand first and gate it against the
  Python kernel before touching anything else.
- The **ownership boundary**. `kernel/` owns gate schemas, run-record
  validation, and gate-authority semantics, permanently. A Go kernel is
  still shelled out to — `roster/` asks, the kernel answers. A single Go
  module tempts an in-process import that dissolves the boundary silently;
  `test_kernel_boundary.py` (itself Python, retiring in Phase 7) is what
  currently prevents that, so its Go replacement must land *with* this
  phase, not after it.
- **Authorship/approval separation.** Roughly a third of the kernel is
  GitHub/GitLab gate-approval plumbing (`gate_issues*`, `gate_reviewers*`,
  `*_write.py`). These enforce that no identity approves its own work. Port
  them with adversarial tests, not just happy-path parity.

**Breaks: `pipx install agentic-sdlc`.** Chosen deliberately. Mitigation:
cut a final Python release whose console script prints the binary install
instructions and exits non-zero, so an existing consumer gets a clear
message rather than a version that silently stops updating. This is the same
failure mode the archived-marketplace note in `CLAUDE.md` describes, and the
same fix.

**Gate:** differential across all 30 subcommands against the Python kernel,
run-record schema validation unchanged, and the boundary test ported first.

## Phase 6 — Reimplement the graph runtime

**Scope:** 5,995 production lines, 5,933 test lines.

Better news than expected: **only 5 of 16 modules import LangGraph.** The
other 11 — `requirement_issues` (884), `gitlab_issue`, `github_approval`,
`contracts`, `export`, `provider`, `state`, `validate`, `planning`,
`agents`, `service` — are ordinary ports.

The LangGraph surface actually in use is small and nameable:

| primitive | used for | Go replacement |
|---|---|---|
| `StateGraph`, `START`, `END` | the gate graph | a node/edge executor over a typed state struct |
| `Send` | fan-out to parallel agents | goroutines + a collected result set |
| `interrupt` | human-in-the-loop gate pauses | suspend to checkpoint, resume by task id |
| `SqliteSaver` | checkpointing | the SQLite driver already used by the knowledge store |
| `Command` | resume-with-value | the resume payload on the same checkpoint API |

`anthropic` and `openai` both have usable Go clients, and `fastapi`'s role
(`service.py`) maps onto `net/http`.

**Method:** build the graph runtime first as a standalone Go package with
its own tests — a graph engine is testable without any of the SDLC
semantics on top of it. Then port the 11 non-LangGraph modules against a
differential harness. Then move the 5 graph-coupled modules onto the Go
runtime last, when both halves are already proven.

**Gate:** the engine's own suite ported alongside, plus an end-to-end run of
a task through G1–G10 compared against the Python engine's checkpoint
sequence.

**Breaks:** the `agentic-sdlc-lg` console script, same deprecation treatment
as Phase 5.

**Risk worth stating plainly:** this is the one phase that is a rewrite
rather than a port. Interrupt-and-resume semantics under checkpointing are
where a subtle difference will hide, and a differential harness over a graph
engine is harder to build than one over a pure function. Budget accordingly.

## Phase 7 — The repo-wide guards

**Scope:** whatever remains of `roster/orchestration/test`,
`roster/shared/test`, and the packaging guards — specifically the tests that
check the *repository* rather than any Python in it:
`test_repository_health.py`, `test_kernel_boundary.py`,
`test_cli_surface.py`, the drift guards.

These go last because they are what proves every earlier phase did not break
anything. Porting them early removes the net while the trapeze work is still
happening.

**Gate:** the Go replacements must fail on the same conditions. Falsify each
one against the drift it exists to catch — a hand-edited generated file, a
`roster/` module importing the kernel, a subcommand that stops naming
itself.

---

## Consolidated: what breaks, and for whom

| change | who notices | mitigation |
|---|---|---|
| PyPI wheels become platform-tagged | nobody, if the matrix is complete | install test per tag |
| `CADRE_SELECT_IMPL=python` removed | this repo only | corpus absorbs any shape that surfaced first |
| `pipx install agentic-sdlc` retired | external kernel consumers | final Python release that prints install instructions and exits non-zero |
| `agentic-sdlc-lg` retired | engine users | same |

## Sequencing at a glance

Phase 1 is complete and deleted nothing; its measurement moved Phase 2 from
"independent, do it early" to "precondition for Phase 4".
Phase 3 is trigger-gated and can land any time after its trigger. Phase 4 is
independent of 3. Phase 5 must port `show-contract` first and land its
boundary guard with it. Phase 6 should build the graph runtime before
anything depends on it. Phase 7 is last by construction.

```
1 (done, empty) ──▶ 2 ──▶ 4 ──▶ 5 ──▶ 6 ──▶ 7
                3 ──────┘  (trigger-gated, independent)
```

## What I would revisit before starting Phase 5 or 6

Two things in this plan are decisions rather than facts, and both were made
2026-08-15 with the information above:

- **Retiring the `agentic-sdlc` pip package** is a deliberate break of a
  published artifact other projects install. If any consumer outside this
  workspace depends on it, the "keep a PyPI shim that fetches the binary"
  option costs one extra release and breaks nobody.
- **Reimplementing the graph engine** is justified only if the engine is
  load-bearing. Nothing in this repository's CLI path depends on it; it is
  reached through its own console script. Confirming who actually runs it is
  cheaper than the rewrite and should happen before Phase 6 starts, not
  during it.

## What this plan does not claim

It does not claim 93,384 lines is the true cost. Tests usually shrink on a
port (Go table tests are denser than unittest classes) and the LangGraph
rewrite will grow. It does not put dates on anything. And it assumes the
differential-gate method keeps working — which held for `select`, on a pure
function with a byte-exact output contract, and will be harder to apply to a
stateful graph engine than to anything else here.
