# Remaining Python -> Go Scope

**Status:** Nearly complete, not "scoping only." Every prior tier this
document described (settings/resolve, context-store, sync-codex/profile,
MCP servers) has already shipped as native Go. What genuinely remains is
one deliberately-unported command plus a distribution-channel question that
is not a Go-porting backlog at all.
**Last verified:** 2026-08-14, against commit `3dcb3a46`, by reading
`internal/cli/dispatcher.go`'s full command list, grepping for
`PythonSubcommand`/`exec.Command` call sites across `internal/`, and
`wc -l`'ing the Python files named below. Re-verify against the tree before
trusting a specific number here; these move.

Do not read the git history of this file's earlier revisions as a reliable
timeline — an earlier version of this document (dated the same day) claimed
"no implementation started" for scope that had, in fact, already shipped.

---

## What actually remains

### `cadre select` — deliberately still Python

`internal/cli/select_agents.go` dispatches every invocation, argv
unmodified, straight to `roster/orchestration/src/select_agents.py`. This
is not an unfinished port sitting in a queue: `select_agents.go`'s own
header explains why, at length. The plan is `selection.schema.json` v7, it
carries a `dispatch_fingerprint` (a SHA-256 over the plan's own canonical
form) compared byte-for-byte across invocation paths by
`roster/orchestration/test/test_repository_health.py`, and this file's
comments record that a from-scratch Go reimplementation briefly existed in
this repository and diverged from the contract — smaller flag set, a
repurposed `--output` flag, a JSON-vs-text default mismatch, a plan of its
own invention rather than v7 — before being replaced by dispatch-through.

Remaining scope, if this is ever ported for real: roughly 1,880 lines —

| File | Lines |
|---|---:|
| `select_agents.py` (own argparse surface + entry point) | 450 |
| `build_dispatch_plan.py` | 940 |
| `plan_text_format.py` | 185 |
| `agentic_sdlc_contracts.py` | 146 |
| `route_near_miss.py` | 130 |
| `risk_classifier.py` | 28 |

`routing_overlay`, `glob_containment`, `roster_manifest`, `provenance`, and
`role_metadata` already have Go equivalents used elsewhere in
`internal/orchestration/`; only the modules above are select-specific and
still Python-only. Porting this seriously would mean reproducing the
fingerprint, the canonical JSON encoding, catalog ordering, and the
lifecycle-contract handshake byte-for-byte — the risk `select_agents.go`'s
header explicitly declines to take on casually.

### `cadre init --interactive` — deliberately not ported, fails closed

`internal/initproject/` ports `init_project.py`'s full **non-interactive**
surface (`--answers`, `--set`, `--stack` presets, defaults-mode,
`--dry-run`/`--force`/`--repair`/`--print-answers`) faithfully, including
its named security properties (A-001 through at least A-005 per that
package's own header). It does **not** port `init_project_interactive.py`
(445 lines — the questionnaire UI `init_project.py` only imports when
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

What keeps the rest of `roster/{shared,context-store,orchestration}/src`
and `roster/orchestration/mcp` in the tree is that **two non-checkout
distribution channels still dispatch straight to Python, not to the Go
binary**:

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
| Init questionnaire | `roster/shared/src/init_project_interactive.py` | 445 |
| Codex sync / profile diff / upgrade | `roster/orchestration/src/{sync_codex_agents,profile_diff,upgrade}.py` | 995 |
| MCP servers | `roster/orchestration/mcp/{dispatch_server,gitlab_server}.py` | 708 |
| Doctor / role-fidelity / telemetry / text libs / gitlab CLI | `roster/orchestration/src/{doctor,role_fidelity,selection_telemetry}.py`, `roster/shared/src/{content_protection,text_chunking,text_embedding}.py`, `roster/orchestration/mcp/gitlab_cli.py` | 2,270 |
| `cadre select`'s stack (see above — the one item still live in the checkout too) | — | 1,879 |

**This table is not exhaustive** — it does not account for every file under
the ~23,500-line total (e.g. `routing.py`, `roster_manifest.py`,
`provenance.py`, `routing_overlay.py`, `role_metadata.py`, the
`generate-*` scripts, `gitlab_core.py`, and their test suites are not
itemized here). Re-derive from the tree rather than treating this as a
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
`roster/orchestration/src/generate_global_plugin.py` emits a `bin/cadre`
carrying none of #267's hardening — no binary resolution, no checksum
verification, no cache. The Go generator
(`internal/generators/plugin_generation.go`) emits the hardened one, and
that is what ships.

Both are live, which is why this is a real risk rather than tidy-up:
`cadre_cli/__init__.py` dispatches `generate-plugin` to the Python script and
`_requires_checkout()` fails closed only for a *bundled* install, so an
editable checkout install regenerates the unhardened shim; and
`roster/orchestration/test/test_repository_health.py` invokes the Python
generator directly as one of the two drift guards.

Note the module itself is **not** dead and must not simply be deleted:
`generate_role_metadata.py` and `generate_authority_aides.py` import
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

### 6. `cadre select` — the last Python in the shipped product

Now that the shim execs the Go binary, `select` is the only reason a plugin
install still needs Python at all. Porting it makes the distribution
genuinely Python-free.

Roughly 1,900 lines remain (`build_dispatch_plan.py` 940,
`select_agents.py`'s argparse surface 450, `plan_text_format.py` 185,
`agentic_sdlc_contracts.py` 146, `route_near_miss.py` 130,
`risk_classifier.py` 28); `routing_overlay`, `glob_containment`,
`roster_manifest`, `provenance`, `role_metadata` and `settings` already have
Go equivalents.

**The port is not the hard part — the harness is.** The plan is
`selection.schema.json` v7 and carries a `dispatch_fingerprint` that is a
SHA-256 over its own canonical form, compared byte-for-byte across
invocation paths. Build a differential harness that runs both
implementations over a task corpus and asserts byte equality including the
fingerprint, *first*; port behind it. A near-miss port is worse than no
port, because every consumer reads the v7 plan.

**Done when:** `internal/cli/select_agents.go` no longer calls
`interop.PythonSubcommand`, and the differential harness is green over the
corpus and committed as a regression guard.
