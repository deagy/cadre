# Remaining Python -> Go Scope

**Status:** The porting backlog is empty. Every module this document once
described as pending has shipped as native Go, and `roster/` now contains no
Python at all. What remains is 13,736 lines in three groups, two of which are
Python for a structural reason rather than for want of a port -- see
"What actually remains" below.
**Last verified:** 2026-08-17, against `main`, by deleting the last
`roster/` Python module and re-reading how each remaining script is invoked
(`plugin/hooks/hooks.json`, `plugin/plugins/lifecycle/bin/cadre-install-kernel`,
and the regeneration sequence in `CLAUDE.md`). Re-verify against the tree
before trusting a specific number here; these move.

Do not read the git history of this file's earlier revisions as a reliable
timeline — an earlier version of this document (dated the same day) claimed
"no implementation started" for scope that had, in fact, already shipped.

---

> **Sequencing lives in [`PYTHON_ELIMINATION_PLAN.md`](PYTHON_ELIMINATION_PLAN.md).**
> This file records *what* is still Python and why. That one records the order
> it goes in, what gates each step, and what breaks — against the stated goal
> of zero Python in this repository.

## What actually remains

Two groups now, and the distinction between them matters more than the line
count.

### Two that are Python for a structural reason, and stay

**`guard_workspace_mutation.py`** (1,944 lines, plus an identical packaged
copy). Claude Code invokes it as
`python3 "${CLAUDE_PLUGIN_ROOT}/hooks/guard_workspace_mutation.py"`. A
`PreToolUse` hook has to run on the installer's machine with no setup, and the
packaged `bin/cadre` is a *downloader* -- it fetches a platform binary on
first use. Porting the guard to Go would make a safety hook conditional on a
successful network download, which is a worse property than the language it is
written in.

**`bootstrap_sdlc.py`** (721 lines, plus three packaged copies). Invoked by
`plugin/plugins/lifecycle/bin/cadre-install-kernel` through `$AGENT_PYTHON`.
Its job is installing the kernel, so it runs *before* a working cadre
installation is a given. Same bootstrap paradox.

Neither is blocked on effort. Both would need the packaged wrapper to stop
being a downloader first, which is a distribution decision rather than a
porting one.

### The one that was portable is ported

**`port_cline_agents.py` (deleted) is gone.** It rendered the packaged plugin's agents
and skills into the Cline preset formats, and was the last step of the
documented regeneration sequence needing an interpreter. It is now
`internal/generators/cline_port.go`, reached as `cadre port-cline-agents`.

Its 124 substitution pairs were extracted from the Python mechanically rather
than retyped, and the port is verified the only way this one can be: it
reproduces all 159 committed presets and 9 skills byte-for-byte. A
transcription slip would have changed one generated file in a way that reads
as intentional.

The remaining test modules under `plugin/tools/` cover the three scripts
above and go with whichever of them moves.


### `cadre select` — ported; the Python is gone

**This is done, and the escape hatch is gone with it.** `internal/selector/`
is the only implementation. `CADRE_SELECT_IMPL=python` no longer reverses
anything, the Python selector modules are deleted, and so is the differential
that gated the port — a parity test needs two implementations, and there is
one.

That gate did its job while it existed. It ran both implementations on the
same machine in the same run and required byte equality including
`dispatch_fingerprint`, which mattered because that fingerprint is
checkout-location dependent: the plan embeds absolute paths inside the hashed
payload, so a stored value would have pinned one developer's directory
layout. Each layer was additionally compared over a space chosen to exercise
*it*, because the 25-case corpus reaches only a handful of shapes — the
matcher over 4,725 rule×case evaluations, workflow precedence over 44,101
decisions, the overlay merge over 69 documents, and so on.

What replaced it is not a weaker check but a different kind. Byte equality
against a second implementation only ever ran on inputs both implementations
were given; it could not say whether either was right. The property tests
that now hold `internal/selector` are written against what the code is
supposed to do rather than against what the other copy did, and auditing the
Python tests before deleting them found eleven defects that byte equality had
been agreeing about for months. `internal/selector/golden_corpus_test.go` and
the tests around it are the gate now.

The reimplementation that briefly existed here before — smaller flag set, a
repurposed `--output` flag, a JSON-vs-text default mismatch, a plan of its
own invention — is why the gate was built before the port rather than after.

### `cadre init --interactive` — deliberately not ported, fails closed

`internal/initproject/` ports `internal/cli/init.go`'s full **non-interactive**
surface (`--answers`, `--set`, `--stack` presets, defaults-mode,
`--dry-run`/`--force`/`--repair`/`--print-answers`) faithfully, including
its named security properties (A-001 through at least A-005 per that
package's own header). It does **not** port `internal/initproject`
(445 lines — the questionnaire UI `internal/cli/init.go` only imports when
`--interactive` is passed). `cadre init --interactive` in the Go CLI fails
closed with a message pointing at `--answers`/`--set`, rather than silently
behaving differently from the Python original. There is no existing
terminal-prompt-library precedent in this Go codebase, so a real port needs
that design decision first, not just a line-by-line translation.

### A resolved architectural divergence

`internal/orchestration/{routing,route_matching,dispatch_plan,workflow}.go`
built a **second, independent** `DispatchPlan` type, reachable through an
undocumented `cadre execute`. **Removed 2026-08-14.**

It was not merely a different shape. Run against the same task and files as
`cadre select`, it matched a different route set (`supply-chain` only, where
the selector matched `backend` and `supply-chain`), emitted no
`schema_version` and no `dispatch_fingerprint`, invented a `code-review`
quality gate unrelated to the G1-G10 lifecycle gates, named a retired
knowledge source (`cadre-agents`), and requested knowledge at classification
`medium` for a task the caller had classified `internal` -- then executed
agents against that plan. Documenting it would have blessed those as
contract, so it was deleted.

The execution engine reachable only through that command went with it
(executor, agent pool, API and subprocess runners, Claude/OpenAI providers,
execution context, result cache, result consolidation and formatting,
structured audit logging, and the workflow-coupled telemetry writers). The
MCP dispatch path is untouched -- it has its own `WriteAuditLog` and shares
none of that code.

Deliberately kept: `routing.go`'s `RoutingConfig`/`Route`/`Catalog` types and
loaders, `routing_overlay.go`, and `glob_containment.go`. Those are ported
foundations for an eventual `select` port, not part of the divergence;
`globToRegex` moved from the removed `route_matching.go` into
`glob_containment.go`, where its remaining user is the test that checks a
`NotContained` witness independently rather than trusting the verdict.

---

## Why ~23,500 lines of Python still sit under `roster/`

Almost none of it remains "because Go can't do it yet." The checkout's own
`./bin/cadre` (a shell shim that builds and execs `cmd/cadre`) has a Go
implementation for every subcommand except `select`. The Python
knowledge-store implementation has been deleted outright — there is no
`roster/knowledge-store/src/` any more; `cadre knowledge` is Go-only
(`internal/knowledge/`), and `cadre_cli/__init__.py` says so explicitly in
its own comments.

`roster/orchestration/mcp/` is gone -- deleted once every module had a Go
counterpart that was actually reached, and every one of its Python suites had
been compared against that counterpart.

What keeps the rest of `roster/{shared,context-store,orchestration}/src` in
the tree is that **two non-checkout distribution channels still dispatch
straight to Python, not to the Go binary**:

- `plugin/bin/cadre` (the packaged Claude Code / Codex plugin's shell shim)
  execs Python for `select`, `selection-telemetry`, `context`,
  `bootstrap-codex`, `resolve-shared`, `mcp-dispatch-server`, `init`,
  `profile`, `gitlab-evidence`, `config`, `doctor`, `role-fidelity`, and
  `upgrade` — every subcommand except `sdlc`. It has **no case for
  `knowledge` at all**, and the Python source it would have execed no
  longer exists — worth a look by whoever owns `plugin/bin/cadre` (out of
  this document's scope; flagged here for that owner, not fixed here).
- `cadre_cli/__init__.py` (the pip/pipx console-script entry point)
  dispatches the same Python scripts, but does explicitly special-case
  `knowledge` — it tells the caller to use a checkout's Go binary instead
  of failing silently.

So retiring this Python is a **distribution-strategy** question — when do
these two channels ship or build a Go binary instead of vendoring the
scripts — not a remaining line-by-line Go-porting backlog. That question is
`DISTRIBUTION.md`'s, not this document's.

The largest still-vendored-and-executed (by those two channels only, not by
the checkout) pieces, approximately:

| Area | Files | Lines |
|---|---|---:|
| Context store | `roster/context-store/src/*.py` | 2,058 |
| Settings / resolve / init (non-interactive) | `roster/shared/src/{settings,resolve,init_project}.py` | 3,710 |
| Init questionnaire | `internal/initproject` | 445 |
| Codex sync / profile diff / upgrade | `roster/orchestration/src/{sync_codex_agents,profile_diff,upgrade}.py` | 995 |
| Doctor / role-fidelity / telemetry / text libs | `roster/orchestration/src/{doctor,role_fidelity,selection_telemetry}.py`, `roster/shared/src/{content_protection,text_chunking,text_embedding}.py` | 2,158 |
| `cadre select`'s stack (see above — the one item still live in the checkout too) | — | 1,879 |

**This table is not exhaustive** — it does not account for every file under
the ~23,500-line total (e.g. `internal/selector/match.go`, `internal/selector/rostermanifest.go`,
`internal/orchestration/provenance.go`, `internal/selector/overlay.go`, `internal/generators/frontmatter.go`, the
`generate-*` scripts, and their test suites are not itemized here). Re-derive from the tree rather than treating this as a
complete inventory.

---

## Recommendation

This document's original four-tier, "~200 hours remaining" framing was
describing work that had, in fact, already shipped. Given how little
CLI-porting scope genuinely remains (`select`, and the `init --interactive`
questionnaire), a future scope-tracking document — if one is wanted at all
— should track the distribution-channel question above (owned by
`DISTRIBUTION.md`) rather than reconstruct a CLI-porting backlog that no
longer exists.

---

## Forward plan (recorded 2026-08-14, after PR #267 merged as `d8b80073`)

PR #267 put the compiled Go binary into the packaged plugin channel
(verified download, sidecar-verified offline cache, permission-gated exec,
unconditional Python fallback). That closed the `cadre knowledge` gap for
plugin installs, which previously had no route to the knowledge store at
all.

What follows is ordered by risk relative to effort, not by size. Each item
states what would make it *done*, because several of these are one-line
changes gated on a decision rather than on work.

### 1. A second, drifted producer of `plugin/bin/cadre`

**Status: in progress.** `generate_bin_wrapper()` in
`internal/generators/plugin_generation.go` emits a `bin/cadre`
carrying none of #267's hardening — no binary resolution, no checksum
verification, no cache. The Go generator
(`internal/generators/plugin_generation.go`) emits the hardened one, and
that is what ships.

Both are live, which is why this is a real risk rather than tidy-up:
`cadre_cli/__init__.py` dispatches `generate-plugin` to the Python script and
`_requires_checkout()` fails closed only for a *bundled* install, so an
editable checkout install regenerates the unhardened shim; and
`internal/generators/`'s tests invoke the Go
generator directly as one of the two drift guards.

Note the module itself is **not** dead and must not simply be deleted:
`internal/generators/catalog_generation.go` and `internal/generators/authority_aides.go` import
constants and `GENERATED_MARKER` from it. The fix is to remove the *second
shim producer*, not the module.

**Done when:** exactly one generator produces `bin/cadre`, the
repository-health guard exercises the generator that actually ships, and no
install path can regenerate an unhardened shim.

### 2. `cadre execute` ships a second, incompatible dispatch plan

`internal/orchestration/{routing,route_matching,dispatch_plan,workflow}.go`
build a `DispatchPlan` with no `dispatch_fingerprint` and a shape
incompatible with `selection.schema.json` v7. It is reachable through
`cadre execute`, which appears in neither `bin/subcommands.tsv` nor
`internal/cli/usage.go`. This is the exact divergence
`internal/cli/select_agents.go`'s header warns against, shipping
undocumented.

**Done when:** either the command is documented and its plan shape declared
non-contractual, or it is removed and its route-matching folded into the
`select` port below. Small either way; the cost is deciding which.

### 3. `PythonCLIBridge` is dead

`internal/orchestration/python_integration.go` (197 lines) has zero
production callers — only its own tests. Deletion, no decision needed.

### 4. `cadre upgrade` still assumes pip/pipx

`internal/cli/upgrade.go` shells to `pipx upgrade cadre` and
`pip install --upgrade cadre`, which is wrong for a checkout or `go install`
user. `internal/orchestration/doctor.go` already classifies install kind and
is not consulted.

**Blocked on a product decision, not on effort** — this was flagged when the
command was first ported and the decision was never made. Route on install
kind: checkout → `git pull`; go-install → `go install` the latest tag;
plugin-cache → the marketplace path; wheel → keep pip/pipx.

**Resolved 2026-08-14** along exactly those lines, and the rework turned up
three further defects the original framing had not named:

- It checked **PyPI** for the latest version. That is coherent for one of
  the four install kinds; a checkout, a `go install` binary and a
  plugin-cache install are none of them updated from PyPI, and a wheel
  publish can lag or fail independently of the binaries. It now reads the
  newest `cli-v*` GitHub release — the tag `release.yml`'s `cli-publish` job
  creates and attaches the per-platform binaries to. (`/releases/latest` is
  unusable: this repository also publishes `plugin-v*` and kernel tags.)
- `detectInstallMethod()` re-derived install detection by probing for a
  `pyproject.toml`, then shelling out to `pipx list --json`, then
  **defaulting to pip** — so a checkout whose probe missed was told to run
  `pip install --upgrade cadre`, installing a wheel over the CLI it was
  already running from a git tree. It now reuses
  `orchestration.ClassifyRunningBinary`, which `cadre doctor` already uses
  and which has its own tests.
- The source-checkout branch told users to run `make generate`, a target
  that does not exist in the `Makefile`. It now points at `git pull
  --ff-only` and, only when relevant, `roster/RUNBOOK.md` §17.

Only the wheel channel is updated in place. Every other kind prints its one
correct instruction and changes nothing, so there is nothing to confirm and
`--force` does not apply to them.

### 5. Knowledge-store config vocabulary drift

The Go port renamed the embedding provider from `hashing` to
`local-hashing` (`internal/knowledge/config.go`'s
`SupportedEmbeddingProviders`). An existing `~/.agents/knowledge-store/config.json`
written by the Python implementation is now rejected outright, so
`cadre knowledge` fails from any directory outside a project with its own
config. A silent breaking rename with no migration path.

**Done when:** the legacy name is accepted (with or without a deprecation
notice), or a migration is provided. Small, and it is a live user-facing
break.

**Resolved 2026-08-14.** The legacy name is normalised to `local-hashing`
when a config is parsed, before validation and before the
implicit-project-config trust guard. Normalising rather than widening
`SupportedEmbeddingProviders` is load-bearing: that guard compares against
`local-hashing` exactly, so merely accepting `hashing` would have left a
project-local config naming the offline provider refused *as though it had
asked for remote embeddings*. Both halves are covered by tests, and the
naive fix was measured to fail both.

The sibling context store was checked and is **not** affected:
`internal/contextstore/config.go` keeps `"hashing"` as a deliberate
single-element list with no remote provider, matching its own Python
original. The two stores therefore name the same offline algorithm
differently — `hashing` in the context store, `local-hashing` in the
knowledge store — which is worth knowing before assuming one is a typo for
the other.

### 6. `cadre select` — done

Go by default, `CADRE_SELECT_IMPL=python` to reverse. See the section above
for how it was verified.

The prediction recorded here beforehand — *"the port is not the hard part,
the harness is"* — held up, and is worth keeping for the next port of this
shape. The harness was built first and every increment landed behind it;
the two bugs that got furthest were both cases where a probe and the real
CLI fed the code different shapes, which is precisely the gap a harness
built afterwards would not have closed either.

What is left before a plugin install is genuinely Python-free is no longer
`select` but the *retained* Python: the escape hatch, and the modules the
differential gate compares against. Removing those is a separate decision
with its own trade-off, not a continuation of this work.
