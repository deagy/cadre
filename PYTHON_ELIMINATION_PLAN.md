# Python Elimination Plan

> **Historical record — this finished.** `git ls-files '*.py'` returns nothing;
> `kernel/` and `engine/`, the last two Python components, were deleted at
> `11eefd47` and `2ccfbf0f`. The sequencing below is what was planned and in
> what order, not work that remains. `REMAINING_PYTHON_SCOPE.md` records the
> end state.

**Target: zero lines of Python in this repository.**

This is the sequencing document for that goal. `REMAINING_PYTHON_SCOPE.md`
records *what* is still Python and why; this records *in what order it goes,
what gates each step, and what breaks*.

Status as of 2026-08-17, measured (not estimated) against the tree:

| | production | tests |
|---|---:|---:|
| `.claude/hooks` + `plugin/hooks` (the workspace guard, 2 copies) | 3,888 | 1,556 |
| `plugin/tools` + `plugin/plugins/*/tools` (kernel bootstrap, 4 copies) | 2,884 | 991 |
| `engine/agentic_sdlc_langgraph` | 5,995 | 5,933 |
| **total** | **12,767** | **8,480** |

**21,247 lines**, down from 93,384 on 2026-08-15. Phases 1 through 4 are
complete and Phase 7 is complete for everything except the two scripts
above: `roster/`, `kernel/`, `cadre_cli/` and every `plugin/tools` guard are
Go. `plugin/suite/` is a vendored copy of `roster/` produced by
`generate-plugin`; it retired automatically with its source.

What is left is two groups, and neither is a porting problem:

- **The workspace guard and the kernel bootstrap** are blocked on the packaged
  `bin/cadre` being a downloader. Making a `PreToolUse` safety hook depend on a
  successful network fetch is a worse property than the language it is written
  in, so this waits on a distribution decision. Their tests stay with them --
  porting a test of a Python script to Go means shelling out to `python3` and
  rewriting it again when the script moves.
- **`engine/`** is Phase 6: a LangGraph runtime, where the work is
  reimplementing the graph engine rather than translating a module.

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

### `mcp/` was blocked: the Go dispatch engine was not wired up — **now fixed**

**Measured 2026-08-15, on `fix/untrusted-fencing`.** Every tool in `mcp/` now
has a Go counterpart, which is what this plan treated as the precondition for
deleting it. That precondition was the wrong one.

`dispatch_secure_cloud_role` — the flagship tool — routes
`HandleDispatchSecureCloudRole` → `DispatchSecureCloudRole`
(`dispatch_core_phase2.go`) → `dispatchSync`, which does this:

```go
developerInstructions := "Role instructions would be loaded from role file here"
prompt := ComposePrompt(developerInstructions, brief)
result, err := SpawnAndWait("echo", []string{prompt}, env, DefaultTimeoutSeconds)
```

It never resolves a role file, spawns `echo`, and returns
`{"status": "success", "exit_code": 0}`. Driven end-to-end against a real role
file with a marker string in its `developer_instructions`, the marker never
appears in the result — the file is not opened.

`dispatch_team` routes each member through the same function, so it is hollow
too, and so is `dispatch_team_recipe`, which feeds into it.

Meanwhile the *real* engine exists and is tested: `ResolveRoleFileCodex`,
`ResolveClaudeCodeRoleFile`, `BuildDispatchContext`, `ExecuteDispatchChild`,
`SpawnCodexChild`, `SpawnClaudeCodeChild`, `SpawnAPIChild`. Reachability says
it is production-dead — `BuildDispatchContext` and `ExecuteDispatchChild` have
**no non-test callers**. `ResolveRoleForRunner` still answers
`runner "api" not yet implemented`, months after the API runner was ported.

So the entire hardened path — tier resolution, the git-clean gate, sandbox
narrowing, symlink refusal, the classification ceiling — sits behind a
function nothing in production calls.

**What this changes:**

1. `roster/orchestration/mcp/` is the only working dispatch implementation in
   this repository. Deleting it removes dispatch and leaves a stub that
   reports success for work it did not do. **Do not delete it on the strength
   of tool-for-tool parity.**
2. The precondition for Phase 4's `mcp/` half is not "every tool has a Go
   counterpart", it is "the Go dispatch path resolves a role, spawns the
   named runner, and records what it did". Wiring phase2 to phase3 is the
   work; the pieces already exist and are tested individually.
3. The gate must be behavioural, not protocol-level. A JSON-RPC conformance
   suite compares framing, and the framing was already correct here — the
   stub answers `tools/call` perfectly well. What it cannot do is dispatch.
   The gate has to assert that a named role's instructions reached a child
   process.

**Why nothing caught it.** Every phase-3 test calls the phase-3 functions
directly, so they pass while nothing calls them. Nothing tested the seam. This
is the same shape as the fencing regressions found the same day: a test that
asserts a component works, with no test that the component is *reached*.

**Fixed 2026-08-15** on `fix/untrusted-fencing`. `DispatchSecureCloudRole` and
`DispatchTeam` take `DispatchRoots` explicitly, resolve the role before
deciding anything about it, and call `ExecuteDispatchChild`. Both CLI spawners
were rebuilt: `SpawnCodexChild` was a stub, and `SpawnClaudeCodeChild` invoked
a command form the CLI does not accept while passing no `--permission-mode`,
so the sandbox never reached the child. They share one spawner that feeds the
prompt on stdin, runs the child in its own process group, enforces a deadline,
caps output, and pins the working directory.

The gate is `dispatch_reaches_child_test.go`, which points the runner binary
at a script that echoes stdin: the role file's own text coming back out of a
child process is the assertion. That is the behavioural gate this section
called for, and it is what a protocol-level conformance suite would have
missed.

### What the comparison found, and what is left

`test_mcp_dispatch.py` (294 tests) is now largely worked through.
`test_gitlab_integration.py` (89) is untouched.

Fixed on `fix/untrusted-fencing`:

| Gap | Effect |
| --- | --- |
| Dispatch never dispatched | `echo` and a placeholder; role file never opened |
| Confirmation gate could not fire | Sandbox computed from a hard-coded `""`, always read-only |
| `SpawnCodexChild` was a stub | The **default** runner could not run |
| `SpawnClaudeCodeChild` invented a CLI shape | No `--permission-mode`: the sandbox never reached the child |
| `runner="api"` unreachable | `not yet implemented`, long after the runner was ported |
| Both fences degraded | Static/absent markers, no per-call token |
| Context content relayed unfenced | While the tool description said it was fenced |
| Role files followed symlinks | Cap measured the link, not the target |
| `ensureContained` compared string prefixes | `/srv/project` contained `/srv/project-attacker` |
| Instructions digest discarded | No record of *which* role text ran |
| Catalog allowlist was an empty-map stub | Any pattern-matching id was dispatchable |
| `runners.forward_env` never consumed | Registered, validated, ignored |
| `runners.local_model_<tier>` never consumed | Tier semantics lost on self-hosted models |
| Refusals never audited | The log recorded only successes |
| Unparseable dispatch depth read as `0` | Failed open on the recursion cap |
| No child timeout, process group, or output cap | A hung child hung the dispatch |
| git-clean gate had no timeout and no test | — |

**The final-handoff channel is ported** (`internal/orchestration/final_handoff.go`).
The private fd-based result file, the protocol paragraph appended to the
prompt, the size cap, and the identity-checked cleanup are all present, with
tests for the properties that justify it: the retained descriptor is read
rather than a replacement, a substituted FIFO cannot block the read, cleanup
removes nested content but refuses a directory it did not create, and
malformed content is reported rather than stored.

Not yet ported alongside it: `automatic_context_capture`, which takes the
captured handoff and writes it into the context store. The capture itself now
happens and lands in the dispatch result; what is missing is the automatic
*storage* step and its dispatch-derived source/scope rules.

`automatic_context_capture` is ported
(`internal/orchestration/final_handoff_capture.go`), including the envelope
validation that decides what may be stored: five top-level keys only, free
text bounded in bytes *and* lines, artifacts as identifiers rather than
locations, provenance limited to handles this store issued, and every cited
handle also declared as provenance.

`test_gitlab_integration.py` has been compared.
`internal/orchestration/gitlab.go` was already correct on every transport and
validation property — quick-action rejection, no ambient proxy, certificate
verification, cross-host and scheme-downgrade redirect refusals. What was
missing was the *structural* half of the create-only invariant, now added in
`gitlab_create_only_test.go`: no function named for a state transition, no
`state_event` outside a comment, no DELETE or PATCH, and exactly three
exposed tools.

**`mcp/` is now unblocked.** Every module has a Go counterpart that is
reached, and every Python suite has been compared. The deletion itself is the
next change: remove `roster/orchestration/mcp/` (6,822 lines) and the Python
suites that test it, keeping the Go seam tests as the gate.

**The pattern worth carrying forward.** Every gap above shares one shape: a
component was implemented and tested in isolation, and nothing tested that it
was *reached*. Component tests cannot fail for a component nobody calls. Where
a port is gated, gate it on observable behaviour at the seam — for dispatch
that meant pointing the runner binary at a script that echoes stdin, so the
role file's own text coming back out of a child process is the assertion.

## Phase 5 — Kernel to Go, distributed as binaries

**Scope:** 9,626 production lines across **32** CLI subcommands (this said
30; the parser reports 32), plus 6,883 lines of tests. The 10 JSON schemas
under `kernel/contracts/` are **data, not code** — they stay exactly as they
are.

**Progress: 18 of 32.** `show-contract` (#290), `detect`, the `provider` /
`profile` / `extension` introspection trio (#291), `validate`, and the four `list-*` readers (#292),
each behind a differential that runs both kernels on the same machine
(`kernel/test/test_kernel_differential.py`, and for `validate` the Go-side
`internal/kernel/validate_differential_test.go` and
`validate_runrecord_differential_test.go`).

**`status` is not read-only, whatever its name says.** It is documented as
"Show a task's gate state" and it calls `advance_lifecycle` and then
`write_json`: it moves the next eligible gate to `ready` and persists the run
record. `advance_lifecycle` is careful about it ("never infer approval"), but
a caller running `status` to look at something changes it. Group it with the
mutating subcommands, not the inspecting ones — it was mis-grouped here, and
the mistake is easy to repeat because the name invites it.

The `list-*` sidecar readers (`list-gate-issues`,
`list-github-gate-issues`, `list-gate-status`, `list-reviewer-nudge`)
landed with it, compared byte for byte rather than as parsed values —
these print a document other tooling reads, so key order, Python's
`\uXXXX` escaping and exact indentation are all part of the contract.
`internal/kernel/echo.go` is the piece that reproduces
`json.dumps(value, indent=2)`; every remaining subcommand that echoes a
JSON document needs it.

**With that, the read-only group is done.** `plan` followed it as the
first of the writers, compared byte for byte on all three documents it
produces — the printed plan, `dispatch-plan.json`, and `run-record.json`
— with only the two wall-clock timestamps blanked. Those are precisely
the fields the dispatch fingerprint excludes, so the fingerprint itself
is part of the comparison.

`decide` followed it, and it is the port where the standard changes:
every other subcommand so far reads, while this one writes an approval
into a run record. A defect here would not miss a problem, it would
manufacture one. So its four refusals are synchronous, at write time,
rather than left to `validate` — the actor must be the assigned
identity, must not have prepared the work, must not have verified it,
and an approval under a forge-review policy must cite a well-formed
review URI. Each is falsified individually, and each case compares the
resulting run record byte for byte, because a refusal that reports
itself correctly while still writing the approval would pass any weaker
check.

`invalidate`, `reenter` and `upgrade` followed as a group — the three
commands that reach into a record, or a lock, after the fact. They are
compared against a fixture whose G1 is genuinely approved, with bound
artifacts and a source link populating a top-level record field:
invalidating a run where nothing had been decided would agree
trivially.

`status` came next, and the warning above it stands: it is documented
as "show a task's gate state" and it writes, advancing the next
eligible gate to `ready` and persisting the record. Ported as-is — a Go
`status` that only read would quietly stop advancing gates for every
project relying on it, a worse surprise than the one already there. The
read-only projection stays separate, because the gate-status publishers
render a task onto a pull request and that render must not move the
task on.

`init` came next, and it is the port where byte-for-byte comparison
earned its keep twice over. Comparing only the overlay's JSON documents
would have passed while two things were wrong: the order agents are
collected from gate bindings (which feeds the profile digest), and how
a role definition's em-dash is escaped into a Codex wrapper's TOML.
Both only showed up because every generated file is compared, wrappers
included.

That second one also revealed a latent defect elsewhere:
`provider.go` kept its own `fingerprint`, encoding with encoding/json
rather than Python's `ensure_ascii=True`. Provider digests over any
manifest containing one accented character would have differed
silently, and the two kernels would have disagreed about whether a
provider had changed.

`repair` followed, with its own descriptor-confined filesystem. Every
other command resolves a path and then opens it; repair does not,
because it writes into a project it did not create, on a filesystem
somebody else may be touching. It pins the root, walks one component at
a time refusing symlinks, and installs through a temporary file — using
`link` (atomic, no-clobber) to create and `rename` to overwrite, so a
decision appearing between planning and writing makes repair lose the
race rather than win it. `os.Root` supplies the confinement; the
symlink refusal is stricter than `os.Root` alone, which follows links
that stay inside the root.

The port also surfaced an inconsistency in the Python kernel worth
fixing once it is the only kernel left: **`init` and `repair` render a
Claude wrapper one blank line apart**, so a project's wrapper content
depends on which command created it. Reproduced faithfully rather than
corrected — fixing it during the port would make every repair differ
from the kernel it is checked against, hiding real divergences behind
an intentional one.

**What remains is the GitHub/GitLab gate-approval plumbing: 5,922 lines
across twelve modules**, more than half the kernel's production code and
the only part that talks to a network. It decomposes as:

| Piece | Lines | What it is |
| --- | --- | --- |
| `_forge_text`, `_forge_ledger` | 206 | Shared text sanitization and ledger/lock mechanics |
| ~~`github_write`, `gitlab_write`~~ | ~~829~~ | **Ported.** The forge clients |
| ~~`gate_reviewers`, `gate_reviewers_gitlab`~~ | ~~890~~ | **Ported.** Reviewer reporting |
| ~~`gate_status`, `github_status_write`~~ | ~~803~~ | **Ported.** The gate-status comment |
| ~~`reviewer_nudge`~~ | ~~458~~ | **Ported.** The advisory reviewer comment |
| ~~`gate_issues`, `gate_issues_github`, `github_issue_write`~~ | ~~2,736~~ | **Ported, then deleted with the rest of the package.** |

Both shared primitives landed first, since everything else builds on
them. The sanitizers are pure enough to compare exhaustively: 48 inputs
run through both implementations in one pass, compared on verdict *and*
message. The ledger write is compared on bytes, because the Python
kernel reads these files back for as long as both exist.

The two forge clients followed. Neither speaks HTTP: they run `gh api`
and `glab api`, so credentials never pass through this process. The
comparison that mattered there was not the network path — which cannot
be compared without a network — but the **mock conventions**, because
they are how every module above these clients is tested. Twenty-four
cases feed one mock file to both implementations and compare the result
or the refusal; if the conventions had differed, every fixture written
against the Python modules would have stopped working silently.

One property of the ledger deserves carrying forward into the modules
above it: **the lock is never broken on a timeout**. A stale lock means
somebody's publication was interrupted, and resuming it automatically
would create forge artifacts the interrupted run may already have
created. Only an explicit `--break-lock` takes it.

**`validate` landed in #292**, and it was the largest single read-only item:
the overlay loader, `approval_source_policy`, the agent catalog, path
confinement, and JSON Schema Draft 2020-12 over both run records and dispatch
plans, via `github.com/santhosh-tekuri/jsonschema/v5`.

Two things are worth carrying forward from it:

- **Schema *messages* are not portable.** Both sides report a violation at
  the same location; the sentence after it belongs to the validating library
  ("'owner' is a required property" against "missing properties: 'owner'").
  The differential compares file and location exactly and drops that
  sentence. Everything the kernel words itself is compared in full.
- **The fingerprint is now shared at the encoder and duplicated at the
  policy.** `internal/canonicaljson` holds the byte-exact JSON encoder both
  the selector and the kernel hash with; each still owns its own excluded-key
  set, held together by `internal/canonicaljson/agreement_test.go`. That
  split exists because the two sides disagreed once over `provenance` and
  the kernel then rejected every plan the selector produced.

**Every subcommand is now in Go.** The last eight were the forge approval
adapters (`approve-from-github`, `approve-from-github-pr`,
`approve-from-gitlab`, `approve-from-gitlab-mr`) and the four
`link-*-from-*-issue` commands. Nothing in `agentic_sdlc/__init__.py`'s
parser now falls through to the "not ported yet" branch.

Three things the last stretch surfaced, all recorded rather than fixed in
place:

- **The Go CLI prints no usage block.** argparse prints a wrapped usage
  summary above every argument error; the Go parser prints only the error
  line. The error lines themselves now match exactly -- the port had been
  using Go's `%q` where every argparse message uses `%r`, so `invalid choice:
  "G99"` where Python says `invalid choice: 'G99'`, across `decide`,
  `request-gate-reviewers`, `publish-gate-status`, `publish-reviewer-nudge`
  and the new adapters. That is fixed. The missing usage block is not, and it
  is a real regression for somebody who mistypes a flag: reproducing
  argparse's wrapping for every subcommand would be a facsimile of something
  that disappears with the Python kernel, so the right fix is a usage summary
  the Go CLI owns, written once the comparison no longer constrains it.

- **`github_issue_write.py`'s docstring is wrong about its own exit code.**
  It says a secondary rate limit maps to "a `secondary-rate-limit` block (CLI
  exit 2)". `SecondaryRateLimitError` subclasses `ValueError`, and
  `_process_gate_issue`'s `except ValueError` wraps it into
  `GateIssuesGithubError` -- exit 1. The port mirrors the behaviour, because
  the differential requires it. Correct the docstring when the module goes.

- **One eligibility checker served three commands that disagree about its
  failures.** `gate_reviewers.py` raises one exception class for every
  eligibility failure; `gate_issues.py` and `gate_issues_github.py` split
  them -- an unknown gate id is a typo (exit 1), a gate the task is not
  configured for is a statement about the project (exit 2). The shared Go
  helper collapsed the two, so `create-gate-issues --gates G9` exited 1 where
  Python exits 2. Fixed in #303 by giving the shared error a `NeedsHuman`
  flag each caller maps itself. The lesson generalises: **sharing a helper
  across commands shares its error taxonomy too, and that is the part nobody
  checks.**

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

**Phase 5 is complete.** `kernel/agentic_sdlc/` (9,626 lines), `kernel/test/`
(6,883), the 26 differential tests, and the pip packaging are deleted --
21,705 lines. `kernel/` now holds `contracts/` and a README; the
implementation is `internal/kernel/`.

The pip retirement needed no mitigation in the end: the maintainer confirmed
the package had pre-release testers only and no current consumers, so the
"final release whose console script prints install instructions" step above
was not required. The PyPI *name* still belongs to an unrelated third party,
which makes `SECURITY.md`'s warning more pointed rather than less -- there is
now no package of ours that `pip install agentic-sdlc` could be confused
with.

What made the deletion cost nothing was four changes that landed first, and
each was worth more than the deletion:

| | |
| --- | --- |
| #309 | the differentials stopped passing when they compared nothing |
| #310, #311 | the command layer and forge paths got tests of their own -- 55 functions went from full coverage to zero without them |
| #312 | shared fixtures moved out of the files about to be deleted |
| #313 | invariants split from comparisons, so the deletion removed one set and not both |

Measured before deleting anything: removing the differential *files* dropped
coverage to 48%; removing only the Python-comparing *tests* left 67.3%. The
split is the difference.

**One pattern surfaced four times and is worth carrying into Phase 6.** A
test reports success because something underneath it quietly gave up: a
fixture that skipped when its Python builder failed (#309, and twice more in
#312), a parent that passed because every subtest skipped (#313), and a
discovery test that skipped on error (#315). None was found by reading code.
All four were found by removing the thing they depended on and looking at
what happened -- which is the check to run against the graph engine before
trusting any differential built for it.

## Phase 6 — Reimplement the graph runtime

**Scope:** 5,995 production lines, 5,933 test lines, 254 tests.
**Scoped in detail 2026-08-18**, by reading the engine rather than estimating
from its size. What follows replaces the earlier sketch; where the two differ,
this is the measured version.

### Decide this before writing any code: nothing consumes the engine

`engine/` has **no callers in code.** Nothing in `cmd/`, `internal/`,
`roster/`, `kernel/`, `plugin/` or `bin/` imports it or shells out to
`agentic-sdlc-lg`. It is not in the plugin, not in the wheel, and not in any
release artifact — CI runs its tests and nothing else.

It does have *documented* users: `docs/kernel/usage-overview.md` names it as
"real orchestration" and tells an operator to run `plan`/`resume` directly.
So this is a user-facing tool invoked by hand from a checkout, not dead code —
which matters, because a hand-run tool has no automated caller to catch a
behavioural regression the way a library would.

Its own README says it "replaces the plugin's earlier skill-based
orchestration" and is "the only way to actually drive a task through the
lifecycle now." Both can be true at once: it is the intended lifecycle driver
and currently drives nothing here.

That makes this phase different from every other one in this document. The
others removed Python that something ran. This one removes Python that nothing
runs, at a cost of ~6,000 lines of rewrite. **The question is not how to port
it — that is answered below — but whether the engine is the direction.** If it
is going to be wired into `cadre`, port it and do that first so the port has a
consumer to be correct *for*. If it might be replaced, porting it now is
building the second version of something whose first version has no users.

### What the LangGraph coupling actually is

Five primitives, not a framework:

| primitive | used for | Go replacement |
|---|---|---|
| `StateGraph`, `START`, `END` | the gate graph | a node/edge executor over a typed state struct |
| `Send` | fan-out to parallel agents | goroutines plus a collected result set |
| `interrupt` | human-in-the-loop gate pauses | suspend to checkpoint, resume by task id |
| `Command` | resume-with-value | the resume payload on the same checkpoint API |
| `SqliteSaver` | checkpointing | the SQLite driver the knowledge store already uses |

And the coupling is far shallower than "5 of 16 modules import LangGraph"
suggests, because four of those five barely touch it:

| | lines | what it is |
|---|---:|---|
| **deep** — `graph.py` (28 refs), `runtime.py` (25) | 1,243 | the actual engine: graph construction and cross-process rebuild |
| **shallow** — `cli.py` (5), `github_approval.py` (7), `a2a/server.py` (3), `export.py` (3), `reentry.py` (3), `state.py` (1), `gitlab_issue.py` (1), `planning.py` (1) | 2,212 | mostly `Command` to resume and `get_state` to read status |
| **independent** — `requirement_issues.py` (884), `agents.py` (572), `provider.py` (405), `validate.py` (266), and five smaller | 2,540 | ordinary ports: JSON, HTTP, file I/O |

`state.py` is counted shallow rather than independent on purpose: it imports
nothing from LangGraph but defines the three reducers LangGraph applies
(`merge_gate_updates`, `merge_agent_outputs`, `operator.add`). Two are dict
merges and one is a list append — the reducer surface is three functions, not a
system.

### The replay semantics are not load-bearing, which is the main de-risking find

`interrupt()` pauses by raising; on `Command(resume=...)` LangGraph
**re-executes the node from the top**, with `interrupt()` returning the resume
value instead of pausing. Anything before the interrupt therefore runs twice.
That is the semantic a naive port gets wrong, and it is why the earlier sketch
called this a rewrite.

There are exactly **two** interrupt sites — `mutation_gate_check` and
`human_approval` — and both are pure computation, then `interrupt`, then record
the decision. Nothing before either has a side effect. So a Go port can **split
each node at its interrupt** into a pre-state and a post-state and never
replay anything, rather than reproducing replay. That is a simpler engine and a
more predictable one; it needs a test asserting the pre-interrupt half stays
side-effect-free, since the property is what licenses the simplification.

Only the interrupted node replays — completed nodes resume from the checkpoint,
so this is the whole exposure.

### The gate already exists, mostly

Three things a differential needs are already there:

- **An observable output contract.** The checkpointed state *is* the run
  record; `export.py` reshapes it into `run-record.schema.json` and
  `validate.py` checks it. That schema is owned by `kernel/`, not by the
  engine, so it is a fixed target both implementations can be compared against.
  Run both engines over the same task and diff the exported record.
- **Hermetic tests.** 254 of them, with a `FakeModelClient` seam and a
  `:memory:` checkpointer. No test needs an API key.
- **Deterministic graph rebuild across processes.** `runtime.py` already writes
  a per-task `graph-config.json` recording everything needed to rebuild the
  identical graph shape in a later process, because the CLI is a new process
  every invocation. A Go port inherits that design rather than inventing it.

### Method

1. **The graph executor first, standalone**, with its own tests. A node/edge
   executor over a typed state with three reducers is testable with no SDLC
   semantics on top, and it is the only genuinely new thing here.
2. **The 2,540 independent lines next**, against a differential over their
   existing tests. `requirement_issues.py` is the largest single module and is
   explicitly never wired into graph dispatch — a test enforces that — so it
   can move without touching graph semantics at all.
3. **The shallow modules**, which mostly need `Command`/`get_state` replaced by
   the new executor's equivalents.
4. **`graph.py` and `runtime.py` last**, when both halves are proven.

### Risks, in the order they will bite

1. **Concurrent `Send` fan-out into three reducers.** Parallel agent nodes
   write `lifecycle_gates` and `agent_outputs` concurrently, and the merge is
   last-write-wins per key. Go will surface ordering non-determinism that
   Python's GIL hides. Needs a deterministic merge order and a `-race` run over
   a fan-out wider than the fixtures use.
2. **Checkpoint format.** A Go engine will not read Python `SqliteSaver`
   checkpoints. Either accept that in-flight tasks cannot cross the cutover, or
   write a converter — decide deliberately rather than discovering it.
3. **`jsonschema`.** `validate.py` leans on Draft 2020-12 plus a format
   checker for `date-time`. Go's options are less mature; confirm one handles
   this repository's schemas before committing to the port.
4. **The model clients.** `agents.py` hides Anthropic and OpenAI behind a
   `ModelClient` protocol with one method, lazily imported. That is a clean
   seam, and the Go side can be plain HTTP rather than an SDK.
5. **FastAPI.** `service.py` (95 lines) and `a2a/server.py` (311) map onto
   `net/http`; the A2A wire types are already explicit in `a2a/types.py`.

**Breaks:** the `agentic-sdlc-lg` console script, same deprecation treatment as
Phase 5.

**Estimate:** the executor is small; the bulk is the 2,540 independent lines,
which are ordinary. The earlier framing of "a rewrite, budget accordingly"
overstates it now that replay is known not to be load-bearing — but only if the
split-at-interrupt property is tested rather than assumed.

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
